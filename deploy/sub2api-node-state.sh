#!/usr/bin/env bash

# Root-owned node admission control shared by single-host blue/green and
# multi-node rolling releases. It never edits DNS or Cloudflare configuration.

set -Eeuo pipefail
umask 077

STATE_DIR="${SUB2API_NODE_STATE_DIR:-/var/lib/sub2api/runtime}"
TRAFFIC_FILE="${SUB2API_TRAFFIC_STATE_FILE_HOST:-${STATE_DIR}/traffic-state}"
BACKGROUND_DIR="${SUB2API_BACKGROUND_STATE_DIR_HOST:-${STATE_DIR}/background}"
LOCAL_TRANSACTION_FILE="${SUB2API_LOCAL_RELEASE_STATE_FILE_HOST:-${STATE_DIR}/local-release.env}"
APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
CADDYFILE="${SUB2API_NODE_STATE_CADDYFILE:-${APP_DIR}/Caddyfile}"
LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-/run/lock/sub2api-maintenance.lock}"

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

write_state() {
  local path="$1"
  local value="$2"
  local temporary="${path}.tmp.$$"
  printf '%s\n' "$value" >"$temporary"
  chmod 644 "$temporary"
  mv -f -- "$temporary" "$path"
}

ensure_state() {
  local path="$1"
  local default_value="$2"
  local allowed_values="$3"
  local value
  if [ -L "$path" ] || { [ -e "$path" ] && [ ! -f "$path" ]; }; then
    die "runtime state path is not a regular file: $path"
  fi
  if [ -f "$path" ]; then
    value="$(read_state "$path")"
    case " ${allowed_values} " in
      *" ${value} "*) ;;
      *) die "runtime state file contains an invalid value: $path" ;;
    esac
    chmod 644 "$path"
    return
  fi
  write_state "$path" "$default_value"
}

metadata_value() {
  local file="$1"
  local key="$2"
  awk -F= -v key="$key" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$file"
}

write_local_transaction() {
  local previous="$1"
  local candidate="$2"
  local temporary="${LOCAL_TRANSACTION_FILE}.tmp.$$"
  {
    printf 'state=local-switching\n'
    printf 'previous=%s\n' "$previous"
    printf 'candidate=%s\n' "$candidate"
  } >"$temporary"
  chmod 600 "$temporary"
  mv -f -- "$temporary" "$LOCAL_TRANSACTION_FILE"
}

read_local_transaction() {
  [ -f "$LOCAL_TRANSACTION_FILE" ] && [ ! -L "$LOCAL_TRANSACTION_FILE" ] \
    || die "local release transaction is missing or unsafe"
  local_state="$(metadata_value "$LOCAL_TRANSACTION_FILE" state)"
  local_previous="$(metadata_value "$LOCAL_TRANSACTION_FILE" previous)"
  local_candidate="$(metadata_value "$LOCAL_TRANSACTION_FILE" candidate)"
  [ "$local_state" = local-switching ] || die "local release transaction state is invalid"
  validate_container "$local_previous"
  validate_container "$local_candidate"
  [ "$local_previous" != "$local_candidate" ] || die "local release transaction containers must differ"
}

require_no_local_transaction() {
  [ ! -e "$LOCAL_TRANSACTION_FILE" ] && [ ! -L "$LOCAL_TRANSACTION_FILE" ] \
    || die "an unfinished local release transaction exists; run recover-local first"
}

read_state() {
  local path="$1"
  if [ -f "$path" ] && [ ! -L "$path" ]; then
    tr -d '\r\n' <"$path"
  else
    printf 'missing'
  fi
}

active_container() {
  local upstream count
  [ -r "$CADDYFILE" ] || die "Caddyfile is not readable: $CADDYFILE"
  upstream="$(grep -oE 'sub2api(-(blue|green))?:8080' "$CADDYFILE" | sort -u || true)"
  count="$(printf '%s\n' "$upstream" | sed '/^$/d' | wc -l | tr -d '[:space:]')"
  [ "$count" -eq 1 ] || die "Caddy upstream is ambiguous: $upstream"
  printf '%s' "${upstream%:8080}"
}

validate_container() {
  case "$1" in
    sub2api|sub2api-blue|sub2api-green) ;;
    *) die "unsupported application container: $1" ;;
  esac
}

mark_other_generations_standby() {
  local active="$1"
  local candidate path
  for candidate in sub2api sub2api-blue sub2api-green; do
    [ "$candidate" != "$active" ] || continue
    path="${BACKGROUND_DIR}/${candidate}"
    if [ -L "$path" ] || { [ -e "$path" ] && [ ! -f "$path" ]; }; then
      die "runtime state path is not a regular file: $path"
    fi
    [ ! -f "$path" ] || write_state "$path" standby
  done
}

main() {
  local lock_parent container local_state local_previous local_candidate
  [ "${SUB2API_NODE_STATE_ALLOW_NON_ROOT_FOR_TESTS:-0}" = 1 ] \
    || [ "$(id -u)" -eq 0 ] || die "node state changes require root"
  for command_name in chmod dirname flock grep install mv sed sort tr wc; do
    command -v "$command_name" >/dev/null 2>&1 || die "$command_name is required"
  done
  install -d -m 755 "$STATE_DIR" "$BACKGROUND_DIR"
  lock_parent="$(dirname "$LOCK_FILE")"
  [ -d "$lock_parent" ] || die "maintenance lock directory does not exist: $lock_parent"
  if [ "${SUB2API_NODE_STATE_LOCK_HELD:-0}" != 1 ]; then
    exec 9>"$LOCK_FILE"
    flock -w 30 -x 9 || die "timed out waiting for the maintenance lock"
  fi

  case "${1:-}" in
    bootstrap)
      [ "$#" -eq 1 ] || die "bootstrap accepts no arguments"
      container="$(active_container)"
      ensure_state "$TRAFFIC_FILE" accepting "accepting draining"
      ensure_state "${BACKGROUND_DIR}/${container}" active "active standby"
      printf 'BOOTSTRAPPED\n'
      ;;
    status)
      [ "$#" -eq 1 ] || die "status accepts no arguments"
      container="$(active_container)"
      printf 'traffic=%s active_container=%s background=%s\n' \
        "$(read_state "$TRAFFIC_FILE")" "$container" "$(read_state "${BACKGROUND_DIR}/${container}")"
      ;;
    standby)
      [ "$#" -eq 2 ] || die "standby requires CONTAINER"
      validate_container "$2"
      container="$(active_container)"
      [ "$2" != "$container" ] || die "the Caddy-active container cannot be prepared as standby"
      write_state "${BACKGROUND_DIR}/$2" standby
      printf 'STANDBY %s\n' "$2"
      ;;
    local-standby)
      [ "$#" -eq 2 ] || die "local-standby requires CONTAINER"
      validate_container "$2"
      require_no_local_transaction
      container="$(active_container)"
      [ "$2" != "$container" ] || die "the Caddy-active container cannot be prepared as standby"
      write_state "${BACKGROUND_DIR}/$2" standby
      write_local_transaction "$container" "$2"
      printf 'LOCAL_STANDBY %s\n' "$2"
      ;;
    drain)
      [ "$#" -eq 1 ] || die "drain accepts no arguments"
      # A cluster transition must never race the recovery of an interrupted
      # single-host blue/green release, which could re-admit this node.
      require_no_local_transaction
      # Remove the node from readiness before stopping new shared-work claims.
      container="$(active_container)"
      write_state "$TRAFFIC_FILE" draining
      write_state "${BACKGROUND_DIR}/${container}" standby
      printf 'DRAINING\n'
      ;;
    preflight)
      [ "$#" -eq 1 ] || die "preflight accepts no arguments"
      require_no_local_transaction
      # The cross-node controller may dependency-smoke a standby generation
      # only after this node has been removed from the external traffic pool.
      # Do not re-enable shared work for the old active generation here.
      write_state "$TRAFFIC_FILE" accepting
      printf 'PREFLIGHT\n'
      ;;
    commit-local)
	  [ "$#" -eq 1 ] || die "commit-local accepts no arguments"
	  read_local_transaction
	  container="$(active_container)"
	  [ "$container" = "$local_candidate" ] \
	    || die "Caddy active container does not match the local release candidate"
	  mark_other_generations_standby "$container"
	  write_state "${BACKGROUND_DIR}/${container}" active
	  write_state "$TRAFFIC_FILE" accepting
	  rm -f -- "$LOCAL_TRANSACTION_FILE"
	  printf 'COMMITTED_LOCAL %s\n' "$container"
	  ;;
    abort-local)
	  [ "$#" -eq 1 ] || die "abort-local accepts no arguments"
	  read_local_transaction
	  container="$(active_container)"
	  [ "$container" = "$local_previous" ] \
	    || die "Caddy active container does not match the pre-release container"
	  mark_other_generations_standby "$container"
	  write_state "${BACKGROUND_DIR}/${container}" active
	  write_state "$TRAFFIC_FILE" accepting
	  rm -f -- "$LOCAL_TRANSACTION_FILE"
	  printf 'ABORTED_LOCAL %s\n' "$container"
	  ;;
    activate)
      [ "$#" -eq 1 ] || die "activate accepts no arguments"
	  require_no_local_transaction
      # Allow dynamic task competition before advertising request readiness.
      container="$(active_container)"
      mark_other_generations_standby "$container"
      write_state "${BACKGROUND_DIR}/${container}" active
      write_state "$TRAFFIC_FILE" accepting
      printf 'ACTIVE\n'
      ;;
    abort)
      [ "$#" -eq 1 ] || die "abort accepts no arguments"
	  require_no_local_transaction
      # Restore the Caddy-selected generation after a failed local or
      # cross-node release attempt. This is intentionally equivalent to the
      # stable active state but has a distinct auditable response.
      container="$(active_container)"
      mark_other_generations_standby "$container"
      write_state "${BACKGROUND_DIR}/${container}" active
      write_state "$TRAFFIC_FILE" accepting
      printf 'ABORTED\n'
      ;;
    recover-local)
      [ "$#" -eq 1 ] || die "recover-local accepts no arguments"
      if [ ! -e "$LOCAL_TRANSACTION_FILE" ] && [ ! -L "$LOCAL_TRANSACTION_FILE" ]; then
        printf 'NO_LOCAL_RECOVERY\n'
        return
      fi
      read_local_transaction
      container="$(active_container)"
      case "$container" in
        "$local_candidate")
          mark_other_generations_standby "$container"
          write_state "${BACKGROUND_DIR}/${container}" active
          write_state "$TRAFFIC_FILE" accepting
          rm -f -- "$LOCAL_TRANSACTION_FILE"
          printf 'RECOVERED_LOCAL %s\n' "$container"
          ;;
        "$local_previous")
          mark_other_generations_standby "$container"
          write_state "${BACKGROUND_DIR}/${container}" active
          write_state "$TRAFFIC_FILE" accepting
          rm -f -- "$LOCAL_TRANSACTION_FILE"
          printf 'ABORTED_LOCAL %s\n' "$container"
          ;;
        *) die "Caddy active container does not match the local release transaction" ;;
      esac
      ;;
    *) die "only bootstrap, status, standby, local-standby, drain, preflight, activate, abort, and recover-local are supported" ;;
  esac
}

main "$@"
