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

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

NODE_STATE_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAINTENANCE_LOCK_HELPER="${NODE_STATE_SCRIPT_DIR}/sub2api-maintenance-lock.sh"
[ -r "$MAINTENANCE_LOCK_HELPER" ] && [ ! -L "$MAINTENANCE_LOCK_HELPER" ] \
  || die "maintenance lock helper is missing or unsafe: ${MAINTENANCE_LOCK_HELPER}"
# shellcheck disable=SC1090,SC1091 # Installed alongside this root-owned executable.
. "$MAINTENANCE_LOCK_HELPER"
LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE}"

write_state() {
  local path="$1"
  local value="$2"
  local temporary="${path}.tmp.$$"

  if [ -L "$path" ] || { [ -e "$path" ] && [ ! -f "$path" ]; }; then
    die "runtime state path is not a regular file: $path"
  fi
  if [ -f "$path" ]; then
    if [ "$(read_state "$path")" = "$value" ]; then
      chmod 644 "$path" \
        || die "could not set runtime state file permissions: $path"
      return
    fi
    # Docker single-file bind mounts pin the current inode. Replacing this path
    # with rename(2) leaves every already-running container on the old inode,
    # so future drain/activate transitions never become visible. The shared
    # maintenance lock serializes writers; a short truncate/write window fails
    # closed in the application instead of serving stale admission state.
    printf '%s\n' "$value" >"$path" \
      || die "could not update runtime state file: $path"
    chmod 644 "$path" \
      || die "could not set runtime state file permissions: $path"
    return
  fi

  # Before the first bind mount exists, retain atomic creation so readers never
  # observe a partially initialized state file.
  if ! printf '%s\n' "$value" >"$temporary" \
    || ! chmod 644 "$temporary" \
    || ! mv -f -- "$temporary" "$path"; then
    rm -f -- "$temporary"
    die "could not create runtime state file: $path"
  fi
}

ensure_state() {
  local path="$1"
  local default_value="$2"
  local allowed_values="$3"
  local repair_value="$4"
  local value
  if [ -L "$path" ] || { [ -e "$path" ] && [ ! -f "$path" ]; }; then
    die "runtime state path is not a regular file: $path"
  fi
  if [ -f "$path" ]; then
    value="$(read_state "$path")"
    case " ${allowed_values} " in
      *" ${value} "*) ;;
      *)
        if interrupted_state_value "$value" "$allowed_values"; then
          printf 'WARNING: repairing interrupted runtime state to %s: %s\n' \
            "$repair_value" "$path" >&2
          write_state "$path" "$repair_value"
          return
        fi
        die "runtime state file contains an invalid value: $path"
        ;;
    esac
    chmod 644 "$path"
    return
  fi
  write_state "$path" "$default_value"
}

interrupted_state_value() {
  local value="$1" allowed_values="$2" allowed
  [ -z "$value" ] && return 0
  for allowed in $allowed_values; do
    if [ "$value" != "$allowed" ]; then
      case "$allowed" in
        "$value"*) return 0 ;;
      esac
    fi
  done
  return 1
}

metadata_value() {
  local file="$1"
  local key="$2"
  awk -F= -v key="$key" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$file"
}

write_local_transaction() {
  local previous="$1"
  local candidate="$2"
  local final_background="$3"
  local temporary="${LOCAL_TRANSACTION_FILE}.tmp.$$"
  validate_background_state "$final_background"
  {
    printf 'state=local-switching\n'
    printf 'previous=%s\n' "$previous"
    printf 'candidate=%s\n' "$candidate"
    printf 'final_background=%s\n' "$final_background"
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
  local_final_background="$(metadata_value "$LOCAL_TRANSACTION_FILE" final_background)"
  # Transactions written by earlier releases always finalized the selected
  # generation as active. Preserve that recovery contract after an upgrade.
  [ -n "$local_final_background" ] || local_final_background=active
  [ "$local_state" = local-switching ] || die "local release transaction state is invalid"
  validate_container "$local_previous"
  validate_container "$local_candidate"
  validate_background_state "$local_final_background"
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

validate_background_state() {
  case "$1" in
    active|standby) ;;
    *) die "unsupported final background state: $1" ;;
  esac
}

finalize_local_transaction() {
  local container="$1"
  local action="$2"

  mark_other_generations_standby "$container"
  write_state "${BACKGROUND_DIR}/${container}" "$local_final_background"
  write_state "$TRAFFIC_FILE" accepting
  rm -f -- "$LOCAL_TRANSACTION_FILE"
  if [ "$local_final_background" = standby ]; then
    printf '%s_LOCAL_STANDBY %s\n' "$action" "$container"
  else
    printf '%s_LOCAL %s\n' "$action" "$container"
  fi
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
  local container local_state local_previous local_candidate local_final_background
  if [ "${SUB2API_NODE_STATE_ALLOW_NON_ROOT_FOR_TESTS:-0}" = 1 ]; then
    # shellcheck disable=SC2034 # Read by the sourced maintenance-lock helper.
    SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1
  else
    [ "$(id -u)" -eq 0 ] || die "node state changes require root"
  fi
  if ! sub2api_maintenance_lock_validate_configured_path "$LOCK_FILE"; then
    die "unsafe maintenance lock: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
  fi
  for command_name in chmod flock grep id install mkdir mv sed sort stat tr wc; do
    command -v "$command_name" >/dev/null 2>&1 || die "$command_name is required"
  done
  install -d -m 755 "$STATE_DIR" "$BACKGROUND_DIR"
  if [ "${SUB2API_NODE_STATE_LOCK_HELD:-0}" != 1 ]; then
    if ! sub2api_maintenance_lock_open "$LOCK_FILE"; then
      die "unsafe maintenance lock: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
    fi
    flock -w 30 -x 8 || die "timed out waiting for the maintenance lock"
  fi

  case "${1:-}" in
    bootstrap)
      [ "$#" -eq 1 ] || die "bootstrap accepts no arguments"
      container="$(active_container)"
      ensure_state "$TRAFFIC_FILE" accepting "accepting draining" draining
      ensure_state "${BACKGROUND_DIR}/${container}" active "active standby" standby
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
      write_local_transaction "$container" "$2" active
      printf 'LOCAL_STANDBY %s\n' "$2"
      ;;
    local-preserve-standby)
      [ "$#" -eq 2 ] || die "local-preserve-standby requires CONTAINER"
      validate_container "$2"
      require_no_local_transaction
      container="$(active_container)"
      [ "$2" != "$container" ] || die "the Caddy-active container cannot be prepared as standby"
      [ "$(read_state "$TRAFFIC_FILE")" = accepting ] \
        || die "standby-preserving release requires accepting traffic state"
      [ "$(read_state "${BACKGROUND_DIR}/${container}")" = standby ] \
        || die "standby-preserving release requires the current generation to be standby"
      write_state "${BACKGROUND_DIR}/$2" standby
      write_local_transaction "$container" "$2" standby
      printf 'LOCAL_PRESERVE_STANDBY %s\n' "$2"
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
    rollback-standby)
      [ "$#" -eq 1 ] || die "rollback-standby accepts no arguments"
      require_no_local_transaction
      # Keep this origin available for direct users and DNS rollback while
      # preventing every local generation from acquiring new shared leases,
      # OAuth refresh work, or queues.  Background fencing is written before
      # request readiness is admitted.
      container="$(active_container)"
      mark_other_generations_standby "$container"
      write_state "${BACKGROUND_DIR}/${container}" standby
      write_state "$TRAFFIC_FILE" accepting
      printf 'ROLLBACK_STANDBY %s\n' "$container"
      ;;
    commit-local)
	  [ "$#" -eq 1 ] || die "commit-local accepts no arguments"
	  read_local_transaction
	  container="$(active_container)"
	  [ "$container" = "$local_candidate" ] \
	    || die "Caddy active container does not match the local release candidate"
	  finalize_local_transaction "$container" COMMITTED
	  ;;
    abort-local)
	  [ "$#" -eq 1 ] || die "abort-local accepts no arguments"
	  read_local_transaction
	  container="$(active_container)"
	  [ "$container" = "$local_previous" ] \
	    || die "Caddy active container does not match the pre-release container"
	  finalize_local_transaction "$container" ABORTED
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
          finalize_local_transaction "$container" RECOVERED
          ;;
        "$local_previous")
          finalize_local_transaction "$container" ABORTED
          ;;
        *) die "Caddy active container does not match the local release transaction" ;;
      esac
      ;;
    *) die "only bootstrap, status, standby, local-standby, local-preserve-standby, drain, preflight, rollback-standby, commit-local, abort-local, activate, abort, and recover-local are supported" ;;
  esac
}

main "$@"
