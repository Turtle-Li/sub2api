#!/usr/bin/env bash

# The caller owns the shared maintenance lock.  Keeping the lock outside this
# helper lets one cutover retain it across database promotion, Redis promotion,
# application start, and the Caddy switch.

set -Eeuo pipefail

APP_DIR="${APP_DIR:-/opt/sub2api}"
OLD_CONTAINER="${OLD_CONTAINER:-sub2api}"
NEW_CONTAINER="${NEW_CONTAINER:-sub2api-green}"
NEW_IMAGE="${NEW_IMAGE:-}"
NETWORK="${NETWORK:-${SUB2API_RUNTIME_GUARD_NETWORK:-sub2api_default}}"
DATA_VOLUME="${DATA_VOLUME:-${SUB2API_RUNTIME_GUARD_DATA_VOLUME:-sub2api_sub2api_data}}"
APP_PORT="${APP_PORT:-8080}"
CADDY_CONTAINER="${CADDY_CONTAINER:-${SUB2API_CADDY_CONTAINER:-sub2api-caddy}}"
CADDYFILE="${CADDYFILE:-$APP_DIR/Caddyfile}"
CADDY_CONFIG_PATH="${CADDY_CONFIG_PATH:-/etc/caddy/Caddyfile}"
CADDY_TRANSACTION_PATH="${APP_DIR}/.gcp-tw-caddy-transaction.env"
CADDY_CUSTOMER_HOST_TRANSACTION_PATH="${APP_DIR}/.cf-opt-totools-caddy.env"
CADDY_UPSTREAM_FROM="${CADDY_UPSTREAM_FROM:-$OLD_CONTAINER:$APP_PORT}"
CADDY_UPSTREAM_TO="${CADDY_UPSTREAM_TO:-$NEW_CONTAINER:$APP_PORT}"
RUN_BACKUP="${RUN_BACKUP:-true}"
PULL_IMAGE="${PULL_IMAGE:-true}"
PRECREATE_ONLY="${PRECREATE_ONLY:-false}"
HEALTH_ATTEMPTS="${HEALTH_ATTEMPTS:-60}"
HEALTH_INTERVAL_SECONDS="${HEALTH_INTERVAL_SECONDS:-3}"
DRAIN_INTERVAL_SECONDS="${DRAIN_INTERVAL_SECONDS:-60}"
DRAIN_ACTIVE_WINDOW_SECONDS="${DRAIN_ACTIVE_WINDOW_SECONDS:-${DRAIN_MAX_WAIT_SECONDS:-600}}"
DRAIN_RETRY_DELAY_SECONDS="${DRAIN_RETRY_DELAY_SECONDS:-3600}"
DRAIN_MAX_RUNTIME_SECONDS="${DRAIN_MAX_RUNTIME_SECONDS:-0}"
DRAIN_LOG_FILE="${DRAIN_LOG_FILE:-/var/log/sub2api-drain-$NEW_CONTAINER.log}"
DRAIN_NOHUP_FILE="${DRAIN_NOHUP_FILE:-/var/log/sub2api-drain-$NEW_CONTAINER.nohup}"
DRAIN_PID_FILE="${DRAIN_PID_FILE:-/var/run/sub2api-drain-$NEW_CONTAINER.pid}"
REMOVE_EXISTING_NEW_CONTAINER="${REMOVE_EXISTING_NEW_CONTAINER:-true}"
ALLOW_ISOLATED_OLD_CONTAINER="${ALLOW_ISOLATED_OLD_CONTAINER:-false}"
DUAL_NODE_RUNTIME_ENABLED="${SUB2API_DUAL_NODE_RUNTIME_ENABLED:-false}"
# This narrow mode validates the externally supplied dependency/runtime files
# and exits before any Docker or Caddy lifecycle operation.  The server
# coordinator uses it before discarding a stale stopped external target.
VALIDATE_EXTERNAL_RUNTIME_ONLY="${VALIDATE_EXTERNAL_RUNTIME_ONLY:-false}"

# An unset mode retains legacy local behavior.  An explicitly blank or unknown
# mode is invalid rather than silently allowing local dependency use.
DEPENDENCY_MODE="${SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE-local}"
EXTERNAL_RUNTIME_ENV_FILE="${SUB2API_EXTERNAL_RUNTIME_ENV_FILE:-}"
EXTERNAL_CA_FILE="${SUB2API_EXTERNAL_CA_FILE:-}"
CONTAINER_PG_CA_PATH="/etc/sub2api-db-ca/ca.crt"
CONTAINER_REDIS_CA_PATH="/etc/ssl/certs/sub2api-db-ca.pem"
TRAFFIC_STATE_FILE="${SUB2API_TRAFFIC_STATE_FILE_HOST:-${SUB2API_TRAFFIC_STATE_FILE:-/var/lib/sub2api/runtime/traffic-state}}"
BACKGROUND_STATE_DIR="${SUB2API_BACKGROUND_STATE_DIR_HOST:-/var/lib/sub2api/runtime/background}"
if [ -n "${SUB2API_BACKGROUND_STATE_DIR_HOST:-}" ]; then
  BACKGROUND_STATE_FILE="${BACKGROUND_STATE_DIR}/${NEW_CONTAINER}"
else
  # Preserve the legacy per-file override while standardizing new host-side
  # configuration on SUB2API_BACKGROUND_STATE_DIR_HOST.
  BACKGROUND_STATE_FILE="${SUB2API_BACKGROUND_STATE_FILE:-${BACKGROUND_STATE_DIR}/${NEW_CONTAINER}}"
fi
HEALTH_TOKEN_FILE="${SUB2API_INTERNAL_HEALTH_TOKEN_FILE:-${APP_DIR}/secrets/internal-health-token}"
CONTAINER_TRAFFIC_STATE_PATH="/run/sub2api-runtime/traffic-state"
CONTAINER_BACKGROUND_STATE_PATH="/run/sub2api-runtime/background-state"
CONTAINER_HEALTH_TOKEN_PATH="/run/sub2api-runtime/health-token"
UNIFIED_PAYMENT_VAULT_VOLUME="${SUB2API_UNIFIED_PAYMENT_VAULT_VOLUME:-}"
CONTAINER_UNIFIED_PAYMENT_VAULT_PATH="/run/sub2api-payment-vault"
UNIFIED_PAYMENT_OVERRIDE_CONFIGURED=false
if [ "${UNIFIED_PAYMENT_ENABLED+x}" = x ]; then
  UNIFIED_PAYMENT_OVERRIDE_CONFIGURED=true
fi

TEMP_FILES=()
TEMP_FILE=""
RUNTIME_ENV_FILE=""
EXTERNAL_VALUES_FILE=""
CADDY_RW_PID=""
EXTERNAL_ENV_KEYS=(
  DATABASE_HOST DATABASE_PORT DATABASE_USER DATABASE_PASSWORD DATABASE_DBNAME DATABASE_SSLMODE
  REDIS_HOST REDIS_PORT REDIS_USERNAME REDIS_PASSWORD REDIS_DB REDIS_ENABLE_TLS
)
EXTERNAL_OVERRIDE_KEYS=("${EXTERNAL_ENV_KEYS[@]}" PGSSLROOTCERT)
RUNTIME_OVERRIDE_KEYS=(
  SUB2API_TRAFFIC_STATE_FILE SUB2API_BACKGROUND_STATE_FILE SUB2API_INTERNAL_HEALTH_TOKEN_FILE
)
UNIFIED_PAYMENT_ENV_KEYS=(
  UNIFIED_PAYMENT_ENABLED UNIFIED_PAYMENT_BASE_URL UNIFIED_PAYMENT_ENVIRONMENT
  UNIFIED_PAYMENT_ORGANIZATION_ID UNIFIED_PAYMENT_PRODUCT_ID UNIFIED_PAYMENT_APP_ID
  UNIFIED_PAYMENT_REQUEST_KEY_ID UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF
  UNIFIED_PAYMENT_VAULT_AGENT_SOCKET UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON
  UNIFIED_PAYMENT_RETURN_URL
)
PAYMENT_VAULT_MOUNT_ARGS=()

log() {
  printf '%s %s\n' "$(date -Is)" "$*"
}

die() {
  log "ERROR: $*" >&2
  exit 1
}

cleanup() {
  local file
  if [ -n "$CADDY_RW_PID" ]; then
	nsenter -t "$CADDY_RW_PID" -m -- \
	  mount -n -o remount,ro,bind "$CADDY_CONFIG_PATH" "$CADDY_CONFIG_PATH" >/dev/null 2>&1 || true
	CADDY_RW_PID=""
  fi
  for file in "${TEMP_FILES[@]:-}"; do
    [ -n "$file" ] || continue
    rm -f -- "$file"
  done
}
trap cleanup EXIT

new_temp_file() {
  TEMP_FILE="$(mktemp)"
  chmod 600 "$TEMP_FILE"
  TEMP_FILES+=("$TEMP_FILE")
}

container_exists() {
  docker inspect "$1" >/dev/null 2>&1
}

container_status() {
  docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$1" 2>/dev/null || true
}

container_running() {
  docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null | grep -qx true
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

require_bool() {
  case "$2" in
    true|false) ;;
    *) die "$1 must be true or false" ;;
  esac
}

require_positive_integer() {
  case "$2" in
    ''|*[!0-9]*) die "$1 must be a positive integer" ;;
  esac
  [ "$2" -gt 0 ] || die "$1 must be a positive integer"
}

require_docker_name() {
  case "$2" in
    ''|*[!A-Za-z0-9_.-]*) die "$1 contains unsupported characters" ;;
  esac
}

validate_unified_payment_runtime() {
  local key value webhook_prefix webhook_key
  if [ "${UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_BASE64:-}" != "" ]; then
    die "UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_BASE64 is forbidden; use the memory-only Vault agent"
  fi
  [ "$UNIFIED_PAYMENT_OVERRIDE_CONFIGURED" = true ] || return 0
  require_bool UNIFIED_PAYMENT_ENABLED "$UNIFIED_PAYMENT_ENABLED"
  if [ "$UNIFIED_PAYMENT_ENABLED" = false ]; then
    return 0
  fi
  for key in "${UNIFIED_PAYMENT_ENV_KEYS[@]}"; do
    value="${!key-}"
    [ -n "$value" ] || die "$key is required when unified payment is enabled"
    case "$value" in *$'\n'*|*$'\r'*) die "$key contains unsupported characters" ;; esac
  done
  [ "$UNIFIED_PAYMENT_ENVIRONMENT" = sandbox ] \
    || die "only the unified payment sandbox is approved"
  [ "$UNIFIED_PAYMENT_BASE_URL" = "https://pay.totools.cn" ] \
    || die "UNIFIED_PAYMENT_BASE_URL does not match the approved sandbox service"
  [ "$UNIFIED_PAYMENT_APP_ID" = "app.sub2.sandbox" ] \
    || die "UNIFIED_PAYMENT_APP_ID does not match the approved Sub2 sandbox app"
  [ "$UNIFIED_PAYMENT_ORGANIZATION_ID" = "84fc3e66-e959-4bc8-8d78-6f8c3d3483fb" ] \
    || die "UNIFIED_PAYMENT_ORGANIZATION_ID does not match the approved Sub2 sandbox scope"
  [ "$UNIFIED_PAYMENT_PRODUCT_ID" = "00da03c5-bc5c-4edb-9d4c-c77da0e969d5" ] \
    || die "UNIFIED_PAYMENT_PRODUCT_ID does not match the approved Sub2 sandbox scope"
  [ "$UNIFIED_PAYMENT_REQUEST_KEY_ID" = "sub2.request.sandbox.v1" ] \
    || die "UNIFIED_PAYMENT_REQUEST_KEY_ID does not match the approved Sub2 sandbox key"
  [ "$UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF" = "vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64" ] \
    || die "UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF does not match the approved Vault field"
  [ "$UNIFIED_PAYMENT_VAULT_AGENT_SOCKET" = "$CONTAINER_UNIFIED_PAYMENT_VAULT_PATH/public.sock" ] \
    || die "UNIFIED_PAYMENT_VAULT_AGENT_SOCKET does not match the mounted agent socket"
  [ "$UNIFIED_PAYMENT_RETURN_URL" = "https://www.turtleligpt.com/payment/result" ] \
    || die "UNIFIED_PAYMENT_RETURN_URL does not match the approved Sub2 result page"
  require_docker_name SUB2API_UNIFIED_PAYMENT_VAULT_VOLUME "$UNIFIED_PAYMENT_VAULT_VOLUME"
  [ "$UNIFIED_PAYMENT_VAULT_VOLUME" = sub2api_unified_payment_vault ] \
    || die "SUB2API_UNIFIED_PAYMENT_VAULT_VOLUME does not match the approved Sub2 volume"
  webhook_prefix='{"sub2.webhook.sandbox.v1":"'
  case "$UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON" in
    "$webhook_prefix"*'"}') ;;
    *) die "UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON is invalid" ;;
  esac
  webhook_key="${UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON#"$webhook_prefix"}"
  webhook_key="${webhook_key%\"\}}"
  [ "${#webhook_key}" -eq 44 ] || die "UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON is invalid"
  case "$webhook_key" in
    *[!A-Za-z0-9+/=]*|*=*=*|*==*) die "UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON is invalid" ;;
  esac
  [ "${webhook_key#???????????????????????????????????????????}" = = ] \
    || die "UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON is invalid"
  PAYMENT_VAULT_MOUNT_ARGS=(
    --mount "type=volume,source=$UNIFIED_PAYMENT_VAULT_VOLUME,target=$CONTAINER_UNIFIED_PAYMENT_VAULT_PATH,readonly"
  )
}

validate_absolute_path() {
  local label="$1"
  local path="$2"
  case "$path" in
    /*) ;;
    *) die "$label must be an absolute path" ;;
  esac
  case "$path" in
    *$'\n'*|*$'\r'*) die "$label must not contain a line break" ;;
    *,*) die "$label must not contain a comma" ;;
    *'|'*) die "$label must not contain a pipe" ;;
  esac
}

validate_canonical_file_path() {
  local label="$1"
  local path="$2"
  local canonical_path

  validate_absolute_path "$label" "$path"
  canonical_path="$(realpath -e -- "$path")" \
    || die "$label must resolve to an existing canonical file"
  [ "$canonical_path" = "$path" ] \
    || die "$label must not traverse a symlink or contain a non-canonical path"
}

load_external_runtime_env() {
  local line key value

  validate_canonical_file_path SUB2API_EXTERNAL_RUNTIME_ENV_FILE "$EXTERNAL_RUNTIME_ENV_FILE"
  [ -f "$EXTERNAL_RUNTIME_ENV_FILE" ] && [ ! -L "$EXTERNAL_RUNTIME_ENV_FILE" ] \
    || die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE must be a regular non-symlink file"
  [ "$(stat -c '%u' "$EXTERNAL_RUNTIME_ENV_FILE")" = "0" ] \
    || die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE must be owned by root"
  [ "$(stat -c '%a' "$EXTERNAL_RUNTIME_ENV_FILE")" = "600" ] \
    || die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE must have mode 0600"

  new_temp_file
  EXTERNAL_VALUES_FILE="$TEMP_FILE"
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      ''|\#*) continue ;;
      *$'\r'*) die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE contains a carriage return" ;;
      *=*) ;;
      *) die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE has an invalid entry" ;;
    esac
    key="${line%%=*}"
    value="${line#*=}"
    case "$key" in
      DATABASE_HOST|DATABASE_PORT|DATABASE_USER|DATABASE_PASSWORD|DATABASE_DBNAME|DATABASE_SSLMODE|REDIS_HOST|REDIS_PORT|REDIS_USERNAME|REDIS_PASSWORD|REDIS_DB|REDIS_ENABLE_TLS) ;;
      PGSSLROOTCERT) die "PGSSLROOTCERT is managed from SUB2API_EXTERNAL_CA_FILE" ;;
      *) die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE contains an unsupported key" ;;
    esac
    if grep -q "^$key=" "$EXTERNAL_VALUES_FILE"; then
      die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE contains a duplicate key"
    fi
    [ -n "$value" ] \
      || die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE contains an empty required setting"
    printf '%s=%s\n' "$key" "$value" >>"$EXTERNAL_VALUES_FILE"
  done <"$EXTERNAL_RUNTIME_ENV_FILE"

  for key in "${EXTERNAL_ENV_KEYS[@]}"; do
    [ -n "$(external_value "$key")" ] \
      || die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE is missing a required setting"
  done
  [ "$(external_value DATABASE_SSLMODE)" = verify-full ] \
    || die "DATABASE_SSLMODE must be verify-full in external dependency mode"
  [ "$(external_value REDIS_ENABLE_TLS)" = true ] \
    || die "REDIS_ENABLE_TLS must be true in external dependency mode"
  case "$(external_value DATABASE_PORT)" in
    ''|*[!0-9]*) die "DATABASE_PORT must be numeric" ;;
  esac
  case "$(external_value REDIS_PORT)" in
    ''|*[!0-9]*) die "REDIS_PORT must be numeric" ;;
  esac
  case "$(external_value REDIS_DB)" in
    ''|*[!0-9]*) die "REDIS_DB must be numeric" ;;
  esac
}

external_value() {
  local key="$1"
  sed -n "/^$key=/ { s/^$key=//; p; q; }" "$EXTERNAL_VALUES_FILE"
}

validate_external_ca_file() {
  local mode group_permissions other_permissions

  validate_canonical_file_path SUB2API_EXTERNAL_CA_FILE "$EXTERNAL_CA_FILE"
  [ -f "$EXTERNAL_CA_FILE" ] && [ ! -L "$EXTERNAL_CA_FILE" ] \
    || die "SUB2API_EXTERNAL_CA_FILE must be a regular non-symlink file"
  [ "$(stat -c '%u' "$EXTERNAL_CA_FILE")" = "0" ] \
    || die "SUB2API_EXTERNAL_CA_FILE must be owned by root"
  mode="$(stat -c '%a' "$EXTERNAL_CA_FILE")"
  case "$mode" in
    ''|*[!0-9]*) die "could not read SUB2API_EXTERNAL_CA_FILE mode" ;;
  esac
  other_permissions=$((10#$mode % 10))
  group_permissions=$((10#$mode / 10 % 10))
  [ $((group_permissions & 2)) -eq 0 ] \
    || die "SUB2API_EXTERNAL_CA_FILE must not be group-writable"
  [ $((other_permissions & 2)) -eq 0 ] \
    || die "SUB2API_EXTERNAL_CA_FILE must not be other-writable"
  [ $((other_permissions & 4)) -ne 0 ] \
    || die "SUB2API_EXTERNAL_CA_FILE must be readable by container UID 1000"
}

validate_runtime_file() {
  local label="$1" path="$2" expected_uid="$3" expected_gid="$4" expected_mode="$5"
  validate_canonical_file_path "$label" "$path"
  [ -f "$path" ] && [ ! -L "$path" ] || die "$label must be a regular non-symlink file"
  [ "$(stat -c '%u' "$path")" = "$expected_uid" ] || die "$label has an unexpected owner"
  [ "$(stat -c '%g' "$path")" = "$expected_gid" ] || die "$label has an unexpected group"
  [ "$(stat -c '%a' "$path")" = "$expected_mode" ] || die "$label has an unexpected mode"
}

validate_runtime_files() {
  validate_runtime_file SUB2API_TRAFFIC_STATE_FILE "$TRAFFIC_STATE_FILE" 0 0 644
  validate_runtime_file SUB2API_BACKGROUND_STATE_FILE "$BACKGROUND_STATE_FILE" 0 0 644
  validate_runtime_file SUB2API_INTERNAL_HEALTH_TOKEN_FILE "$HEALTH_TOKEN_FILE" 1000 1000 600
}

write_external_overrides() {
  local output_file="$1"
  local key value
  for key in "${EXTERNAL_ENV_KEYS[@]}"; do
    value="$(external_value "$key")"
    printf '%s=%s\n' "$key" "$value" >>"$output_file"
  done
  printf '%s=%s\n' PGSSLROOTCERT "$CONTAINER_PG_CA_PATH" >>"$output_file"
}

write_unified_payment_overrides() {
  local output_file="$1"
  local key value
  [ "$UNIFIED_PAYMENT_OVERRIDE_CONFIGURED" = true ] || return 0
  for key in "${UNIFIED_PAYMENT_ENV_KEYS[@]}"; do
    value="${!key-}"
    printf '%s=%s\n' "$key" "$value" >>"$output_file"
  done
}

container_matches_unified_payment_env() {
  local inspect_env="$1"
  local key expected_value actual_value
  ! grep -q '^UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_BASE64=' "$inspect_env" || return 1
  [ "$UNIFIED_PAYMENT_OVERRIDE_CONFIGURED" = true ] || return 0
  for key in "${UNIFIED_PAYMENT_ENV_KEYS[@]}"; do
    expected_value="${!key-}"
    if ! actual_value="$(awk -v expected_key="$key" '
      index($0, expected_key "=") == 1 { count += 1; value = substr($0, length(expected_key) + 2) }
      END { if (count != 1) exit 1; print value }
    ' "$inspect_env")"; then
      return 1
    fi
    [ "$actual_value" = "$expected_value" ] || return 1
  done
}

make_runtime_env_file() {
  local old_env_file output_file line key

  new_temp_file
  old_env_file="$TEMP_FILE"
  docker inspect "$OLD_CONTAINER" --format '{{range .Config.Env}}{{println .}}{{end}}' >"$old_env_file"
  new_temp_file
  output_file="$TEMP_FILE"
  while IFS= read -r line || [ -n "$line" ]; do
    key="${line%%=*}"
	case "$key" in
	  UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_BASE64)
		continue
		;;
	  UNIFIED_PAYMENT_ENABLED|UNIFIED_PAYMENT_BASE_URL|UNIFIED_PAYMENT_ENVIRONMENT|UNIFIED_PAYMENT_ORGANIZATION_ID|UNIFIED_PAYMENT_PRODUCT_ID|UNIFIED_PAYMENT_APP_ID|UNIFIED_PAYMENT_REQUEST_KEY_ID|UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF|UNIFIED_PAYMENT_VAULT_AGENT_SOCKET|UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON|UNIFIED_PAYMENT_RETURN_URL)
		[ "$UNIFIED_PAYMENT_OVERRIDE_CONFIGURED" = true ] && continue
		;;
	  SUB2API_TRAFFIC_STATE_FILE|SUB2API_BACKGROUND_STATE_FILE|SUB2API_INTERNAL_HEALTH_TOKEN_FILE)
		continue
		;;
	  DATABASE_HOST|DATABASE_PORT|DATABASE_USER|DATABASE_PASSWORD|DATABASE_DBNAME|DATABASE_SSLMODE|REDIS_HOST|REDIS_PORT|REDIS_USERNAME|REDIS_PASSWORD|REDIS_DB|REDIS_ENABLE_TLS|PGSSLROOTCERT)
		[ "$DEPENDENCY_MODE" = external ] && continue
		;;
	esac
	printf '%s\n' "$line" >>"$output_file"
  done <"$old_env_file"
  if [ "$DEPENDENCY_MODE" = external ]; then
	write_external_overrides "$output_file"
  fi
  if [ "$DUAL_NODE_RUNTIME_ENABLED" = true ]; then
	printf 'SUB2API_TRAFFIC_STATE_FILE=%s\n' "$CONTAINER_TRAFFIC_STATE_PATH" >>"$output_file"
	printf 'SUB2API_BACKGROUND_STATE_FILE=%s\n' "$CONTAINER_BACKGROUND_STATE_PATH" >>"$output_file"
	printf 'SUB2API_INTERNAL_HEALTH_TOKEN_FILE=%s\n' "$CONTAINER_HEALTH_TOKEN_PATH" >>"$output_file"
  fi
  write_unified_payment_overrides "$output_file"
  RUNTIME_ENV_FILE="$output_file"
}

container_matches_external_runtime() {
  local container="$1"
  local expected_restart="$2"
  local expected_running="$3"
  local actual expected_value actual_value key
  local inspect_env inspect_networks inspect_mounts network_count mount_count expected_mount_count

  [ "$(docker inspect "$container" --format '{{.Config.Image}}')" = "$NEW_IMAGE" ] || return 1
  [ "$(docker inspect "$container" --format '{{.HostConfig.RestartPolicy.Name}}')" = "$expected_restart" ] || return 1
  actual="$(docker inspect "$container" --format '{{.State.Running}}')"
  [ "$actual" = "$expected_running" ] || return 1

  new_temp_file
  inspect_networks="$TEMP_FILE"
  docker inspect "$container" --format '{{range $network, $_ := .NetworkSettings.Networks}}{{println $network}}{{end}}' >"$inspect_networks"
  network_count="$(awk 'NF { count += 1 } END { print count + 0 }' "$inspect_networks")"
  [ "$network_count" -eq 1 ] || return 1
  grep -qxF "$NETWORK" "$inspect_networks" || return 1

  new_temp_file
  inspect_mounts="$TEMP_FILE"
  docker inspect "$container" --format '{{range .Mounts}}{{if eq .Type "volume"}}{{printf "%s|%s|%s|%t\n" .Type .Name .Destination .RW}}{{else}}{{printf "%s|%s|%s|%t\n" .Type .Source .Destination .RW}}{{end}}{{end}}' >"$inspect_mounts"
  mount_count="$(awk 'NF { count += 1 } END { print count + 0 }' "$inspect_mounts")"
  expected_mount_count=3
  [ "$DUAL_NODE_RUNTIME_ENABLED" != true ] || expected_mount_count=$((expected_mount_count + 3))
  [ "${UNIFIED_PAYMENT_ENABLED:-false}" != true ] || expected_mount_count=$((expected_mount_count + 1))
  [ "$mount_count" -eq "$expected_mount_count" ] || return 1
  grep -qxF "volume|$DATA_VOLUME|/app/data|true" "$inspect_mounts" || return 1
  grep -qxF "bind|$EXTERNAL_CA_FILE|$CONTAINER_PG_CA_PATH|false" "$inspect_mounts" || return 1
  grep -qxF "bind|$EXTERNAL_CA_FILE|$CONTAINER_REDIS_CA_PATH|false" "$inspect_mounts" || return 1
  if [ "$DUAL_NODE_RUNTIME_ENABLED" = true ]; then
	grep -qxF "bind|$TRAFFIC_STATE_FILE|$CONTAINER_TRAFFIC_STATE_PATH|false" "$inspect_mounts" || return 1
	grep -qxF "bind|$BACKGROUND_STATE_FILE|$CONTAINER_BACKGROUND_STATE_PATH|false" "$inspect_mounts" || return 1
	grep -qxF "bind|$HEALTH_TOKEN_FILE|$CONTAINER_HEALTH_TOKEN_PATH|false" "$inspect_mounts" || return 1
  fi
  if [ "${UNIFIED_PAYMENT_ENABLED:-false}" = true ]; then
	grep -qxF "volume|$UNIFIED_PAYMENT_VAULT_VOLUME|$CONTAINER_UNIFIED_PAYMENT_VAULT_PATH|false" "$inspect_mounts" || return 1
  fi

  new_temp_file
  inspect_env="$TEMP_FILE"
  docker inspect "$container" --format '{{range .Config.Env}}{{println .}}{{end}}' >"$inspect_env"
  container_matches_unified_payment_env "$inspect_env" || return 1
  for key in "${EXTERNAL_OVERRIDE_KEYS[@]}"; do
    if [ "$key" = PGSSLROOTCERT ]; then
      expected_value="$CONTAINER_PG_CA_PATH"
    else
      expected_value="$(external_value "$key")"
    fi
    if ! actual_value="$(awk -v expected_key="$key" '
      index($0, expected_key "=") == 1 {
        count += 1
        value = substr($0, length(expected_key) + 2)
      }
      END {
        if (count != 1) exit 1
        print value
      }' "$inspect_env")"; then
      return 1
    fi
    [ "$actual_value" = "$expected_value" ] || return 1
  done
  if [ "$DUAL_NODE_RUNTIME_ENABLED" = true ]; then
  for key in "${RUNTIME_OVERRIDE_KEYS[@]}"; do
	case "$key" in
	  SUB2API_TRAFFIC_STATE_FILE) expected_value="$CONTAINER_TRAFFIC_STATE_PATH" ;;
	  SUB2API_BACKGROUND_STATE_FILE) expected_value="$CONTAINER_BACKGROUND_STATE_PATH" ;;
	  SUB2API_INTERNAL_HEALTH_TOKEN_FILE) expected_value="$CONTAINER_HEALTH_TOKEN_PATH" ;;
	esac
	if ! actual_value="$(awk -v expected_key="$key" '
	  index($0, expected_key "=") == 1 { count += 1; value = substr($0, length(expected_key) + 2) }
	  END { if (count != 1) exit 1; print value }
	' "$inspect_env")"; then
	  return 1
	fi
	[ "$actual_value" = "$expected_value" ] || return 1
  done
  fi
}

container_matches_local_runtime() {
  local container="$1" expected_running="$2"
  local inspect_env inspect_mounts inspect_networks key expected_value actual_value
  local mount_count network_count expected_mount_count

  [ "$(docker inspect "$container" --format '{{.Config.Image}}')" = "$NEW_IMAGE" ] || return 1
  [ "$(docker inspect "$container" --format '{{.State.Running}}')" = "$expected_running" ] || return 1

  new_temp_file
  inspect_networks="$TEMP_FILE"
  docker inspect "$container" --format '{{range $network, $_ := .NetworkSettings.Networks}}{{println $network}}{{end}}' >"$inspect_networks"
  network_count="$(awk 'NF { count += 1 } END { print count + 0 }' "$inspect_networks")"
  [ "$network_count" -eq 1 ] || return 1
  grep -qxF "$NETWORK" "$inspect_networks" || return 1

  new_temp_file
  inspect_mounts="$TEMP_FILE"
  docker inspect "$container" --format '{{range .Mounts}}{{if eq .Type "volume"}}{{printf "%s|%s|%s|%t\n" .Type .Name .Destination .RW}}{{else}}{{printf "%s|%s|%s|%t\n" .Type .Source .Destination .RW}}{{end}}{{end}}' >"$inspect_mounts"
  mount_count="$(awk 'NF { count += 1 } END { print count + 0 }' "$inspect_mounts")"
  expected_mount_count=4
  [ "${UNIFIED_PAYMENT_ENABLED:-false}" != true ] || expected_mount_count=$((expected_mount_count + 1))
  [ "$mount_count" -eq "$expected_mount_count" ] || return 1
  grep -qxF "volume|$DATA_VOLUME|/app/data|true" "$inspect_mounts" || return 1
  grep -qxF "bind|$TRAFFIC_STATE_FILE|$CONTAINER_TRAFFIC_STATE_PATH|false" "$inspect_mounts" || return 1
  grep -qxF "bind|$BACKGROUND_STATE_FILE|$CONTAINER_BACKGROUND_STATE_PATH|false" "$inspect_mounts" || return 1
  grep -qxF "bind|$HEALTH_TOKEN_FILE|$CONTAINER_HEALTH_TOKEN_PATH|false" "$inspect_mounts" || return 1
  if [ "${UNIFIED_PAYMENT_ENABLED:-false}" = true ]; then
	grep -qxF "volume|$UNIFIED_PAYMENT_VAULT_VOLUME|$CONTAINER_UNIFIED_PAYMENT_VAULT_PATH|false" "$inspect_mounts" || return 1
  fi

  new_temp_file
  inspect_env="$TEMP_FILE"
  docker inspect "$container" --format '{{range .Config.Env}}{{println .}}{{end}}' >"$inspect_env"
  container_matches_unified_payment_env "$inspect_env" || return 1
  for key in "${RUNTIME_OVERRIDE_KEYS[@]}"; do
	case "$key" in
	  SUB2API_TRAFFIC_STATE_FILE) expected_value="$CONTAINER_TRAFFIC_STATE_PATH" ;;
	  SUB2API_BACKGROUND_STATE_FILE) expected_value="$CONTAINER_BACKGROUND_STATE_PATH" ;;
	  SUB2API_INTERNAL_HEALTH_TOKEN_FILE) expected_value="$CONTAINER_HEALTH_TOKEN_PATH" ;;
	esac
	actual_value="$(awk -v expected_key="$key" '
	  index($0, expected_key "=") == 1 { count += 1; value = substr($0, length(expected_key) + 2) }
	  END { if (count != 1) exit 1; print value }
	' "$inspect_env")" || return 1
	[ "$actual_value" = "$expected_value" ] || return 1
  done
}

create_external_target() {
  local restart_policy="$1"
  local env_file
  make_runtime_env_file
  env_file="$RUNTIME_ENV_FILE"
  if [ "$DUAL_NODE_RUNTIME_ENABLED" = true ]; then
	docker create \
      --name "$NEW_CONTAINER" \
      --network "$NETWORK" \
      --env-file "$env_file" \
      --mount "type=volume,source=$DATA_VOLUME,target=/app/data" \
      "${PAYMENT_VAULT_MOUNT_ARGS[@]+${PAYMENT_VAULT_MOUNT_ARGS[@]}}" \
      --mount "type=bind,source=$EXTERNAL_CA_FILE,target=$CONTAINER_PG_CA_PATH,readonly" \
	  --mount "type=bind,source=$EXTERNAL_CA_FILE,target=$CONTAINER_REDIS_CA_PATH,readonly" \
	  --mount "type=bind,source=$TRAFFIC_STATE_FILE,target=$CONTAINER_TRAFFIC_STATE_PATH,readonly" \
	  --mount "type=bind,source=$BACKGROUND_STATE_FILE,target=$CONTAINER_BACKGROUND_STATE_PATH,readonly" \
	  --mount "type=bind,source=$HEALTH_TOKEN_FILE,target=$CONTAINER_HEALTH_TOKEN_PATH,readonly" \
      --restart "$restart_policy" \
      "$NEW_IMAGE" >/dev/null
  else
	docker create \
    --name "$NEW_CONTAINER" \
    --network "$NETWORK" \
    --env-file "$env_file" \
    --mount "type=volume,source=$DATA_VOLUME,target=/app/data" \
    "${PAYMENT_VAULT_MOUNT_ARGS[@]+${PAYMENT_VAULT_MOUNT_ARGS[@]}}" \
    --mount "type=bind,source=$EXTERNAL_CA_FILE,target=$CONTAINER_PG_CA_PATH,readonly" \
	--mount "type=bind,source=$EXTERNAL_CA_FILE,target=$CONTAINER_REDIS_CA_PATH,readonly" \
    --restart "$restart_policy" \
    "$NEW_IMAGE" >/dev/null
  fi
}

create_local_target() {
  local env_file
  make_runtime_env_file
  env_file="$RUNTIME_ENV_FILE"
  if [ "$DUAL_NODE_RUNTIME_ENABLED" = true ]; then
	docker run -d \
      --name "$NEW_CONTAINER" \
      --network "$NETWORK" \
      --env-file "$env_file" \
	  --mount "type=volume,source=$DATA_VOLUME,target=/app/data" \
	  "${PAYMENT_VAULT_MOUNT_ARGS[@]+${PAYMENT_VAULT_MOUNT_ARGS[@]}}" \
	  --mount "type=bind,source=$TRAFFIC_STATE_FILE,target=$CONTAINER_TRAFFIC_STATE_PATH,readonly" \
	  --mount "type=bind,source=$BACKGROUND_STATE_FILE,target=$CONTAINER_BACKGROUND_STATE_PATH,readonly" \
	  --mount "type=bind,source=$HEALTH_TOKEN_FILE,target=$CONTAINER_HEALTH_TOKEN_PATH,readonly" \
      --restart unless-stopped \
      "$NEW_IMAGE" >/dev/null
  else
	docker run -d \
    --name "$NEW_CONTAINER" \
    --network "$NETWORK" \
    --env-file "$env_file" \
	--mount "type=volume,source=$DATA_VOLUME,target=/app/data" \
	"${PAYMENT_VAULT_MOUNT_ARGS[@]+${PAYMENT_VAULT_MOUNT_ARGS[@]}}" \
    --restart unless-stopped \
    "$NEW_IMAGE" >/dev/null
  fi
}

caddy_config_contains() {
  local path="$1" text="$2"
  docker exec -e CADDY_CHECK_PATH="$path" -e CADDY_CHECK_TEXT="$text" \
    "$CADDY_CONTAINER" sh -c 'grep -qF "$CADDY_CHECK_TEXT" "$CADDY_CHECK_PATH"'
}

caddy_active_config_contains() {
  local text="$1"
  docker exec -e CADDY_CHECK_TEXT="$text" "$CADDY_CONTAINER" sh -c \
    '(wget -qO- http://127.0.0.1:2019/config/ 2>/dev/null || curl -fsS http://127.0.0.1:2019/config/) | grep -qF "$CADDY_CHECK_TEXT"'
}

write_file_preserving_inode() {
  python3 - "$1" "$2" <<'PY'
import os
import sys

source_path, destination_path = sys.argv[1:]
with open(source_path, "rb") as source:
    intended = source.read()
with open(destination_path, "rb") as destination:
    original = destination.read()

def write_all(payload: bytes) -> None:
    descriptor = os.open(destination_path, os.O_WRONLY | os.O_TRUNC | os.O_CLOEXEC)
    try:
        view = memoryview(payload)
        written = 0
        while written < len(view):
            written += os.write(descriptor, view[written:])
        os.fsync(descriptor)
    finally:
        os.close(descriptor)

try:
    write_all(intended)
except BaseException:
    write_all(original)
    raise
PY
}

sync_caddy_startup_file() {
  local container_pid target_path
  container_pid="$(docker inspect "$CADDY_CONTAINER" --format '{{.State.Pid}}')"
  case "$container_pid" in
    ''|*[!0-9]*) die "could not resolve $CADDY_CONTAINER host PID" ;;
  esac
  [ "$container_pid" -gt 1 ] || die "$CADDY_CONTAINER host PID is invalid"
  case "$CADDY_CONFIG_PATH" in
    /*) ;;
    *) die "Caddy startup config path must be absolute" ;;
  esac
  target_path="/proc/$container_pid/root$CADDY_CONFIG_PATH"
  [ -f "$target_path" ] || die "Caddy startup config is missing through container mount namespace"

  nsenter -t "$container_pid" -m -- \
    mount -n -o remount,rw,bind "$CADDY_CONFIG_PATH" "$CADDY_CONFIG_PATH" \
    || die "could not temporarily unlock Caddy startup config bind"
  CADDY_RW_PID="$container_pid"
  if ! python3 - "$CADDYFILE" "$target_path" <<'PY'
import os
import sys

source_path, target_path = sys.argv[1:]
with open(source_path, "rb") as source:
    intended = source.read()
with open(target_path, "rb") as target:
    original = target.read()

def write_all(payload: bytes) -> None:
    descriptor = os.open(target_path, os.O_WRONLY | os.O_TRUNC | os.O_CLOEXEC)
    try:
        view = memoryview(payload)
        written = 0
        while written < len(view):
            written += os.write(descriptor, view[written:])
        os.fsync(descriptor)
    finally:
        os.close(descriptor)

try:
    write_all(intended)
except Exception:
    write_all(original)
    raise
PY
  then
    nsenter -t "$container_pid" -m -- \
      mount -n -o remount,ro,bind "$CADDY_CONFIG_PATH" "$CADDY_CONFIG_PATH" >/dev/null 2>&1 || true
    die "could not synchronize Caddy startup config"
  fi
  nsenter -t "$container_pid" -m -- \
    mount -n -o remount,ro,bind "$CADDY_CONFIG_PATH" "$CADDY_CONFIG_PATH" \
    || die "could not restore read-only Caddy startup config bind"
  CADDY_RW_PID=""
}

restore_caddy_switch() {
  local rollback_config
  [ "${changed_caddy:-false}" = true ] || return 1
  write_file_preserving_inode "$caddy_backup" "$CADDYFILE" || return 1
  sync_caddy_startup_file || return 1
  rollback_config="/tmp/sub2api-release-rollback-$NEW_CONTAINER.Caddyfile"
  docker cp "$CADDYFILE" "$CADDY_CONTAINER:$rollback_config" || return 1
  docker exec "$CADDY_CONTAINER" caddy validate --config "$rollback_config" || return 1
  docker exec "$CADDY_CONTAINER" caddy reload --force --config "$rollback_config" || return 1
  caddy_active_config_contains "$CADDY_UPSTREAM_FROM" || return 1
  if caddy_active_config_contains "$CADDY_UPSTREAM_TO"; then
	return 1
  fi
  return 0
}

[ -n "$NEW_IMAGE" ] || die "set NEW_IMAGE, for example NEW_IMAGE=weishaw/sub2api:0.1.138"
case "$DEPENDENCY_MODE" in
  local|external) ;;
  *) die "SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE must be local or external" ;;
esac
require_bool PRECREATE_ONLY "$PRECREATE_ONLY"
require_bool REMOVE_EXISTING_NEW_CONTAINER "$REMOVE_EXISTING_NEW_CONTAINER"
require_bool ALLOW_ISOLATED_OLD_CONTAINER "$ALLOW_ISOLATED_OLD_CONTAINER"
require_bool RUN_BACKUP "$RUN_BACKUP"
require_bool PULL_IMAGE "$PULL_IMAGE"
require_bool SUB2API_DUAL_NODE_RUNTIME_ENABLED "$DUAL_NODE_RUNTIME_ENABLED"
require_bool VALIDATE_EXTERNAL_RUNTIME_ONLY "$VALIDATE_EXTERNAL_RUNTIME_ONLY"
require_positive_integer HEALTH_ATTEMPTS "$HEALTH_ATTEMPTS"
require_positive_integer HEALTH_INTERVAL_SECONDS "$HEALTH_INTERVAL_SECONDS"
require_docker_name NETWORK "$NETWORK"
require_docker_name DATA_VOLUME "$DATA_VOLUME"
for command_name in docker nsenter perl python3 awk grep stat realpath mktemp chmod rm; do
  require_cmd "$command_name"
done
validate_unified_payment_runtime
if [ "$DEPENDENCY_MODE" = external ]; then
  load_external_runtime_env
  validate_external_ca_file
fi
if [ "$DUAL_NODE_RUNTIME_ENABLED" = true ]; then
  validate_runtime_files
fi
if [ "$VALIDATE_EXTERNAL_RUNTIME_ONLY" = true ]; then
  [ "$DEPENDENCY_MODE" = external ] \
    || die "VALIDATE_EXTERNAL_RUNTIME_ONLY requires external dependency mode"
  log "external runtime contract validated; exiting before Docker or Caddy lifecycle actions"
  exit 0
fi

cd "$APP_DIR"
[ ! -e "$CADDY_TRANSACTION_PATH" ] && [ ! -L "$CADDY_TRANSACTION_PATH" ] \
  || die "unfinished GCP Taiwan Caddy listener transaction exists; commit or rollback it before a blue-green release"
[ ! -e "$CADDY_CUSTOMER_HOST_TRANSACTION_PATH" ] && [ ! -L "$CADDY_CUSTOMER_HOST_TRANSACTION_PATH" ] \
  || die "unfinished customer Host Caddy transaction exists; commit or rollback it before a blue-green release"
container_exists "$CADDY_CONTAINER" || die "Caddy container $CADDY_CONTAINER does not exist"
[ -f "$CADDYFILE" ] || die "Caddyfile not found: $CADDYFILE"
if [ "$ALLOW_ISOLATED_OLD_CONTAINER" = true ]; then
  [ "$PRECREATE_ONLY" = false ] \
    || die "isolated-old recovery cannot precreate a target"
  [ "$RUN_BACKUP" = false ] \
    || die "isolated-old recovery cannot run a backup"
  [ "$PULL_IMAGE" = false ] \
    || die "isolated-old recovery cannot pull an image"
  [ "$REMOVE_EXISTING_NEW_CONTAINER" = false ] \
    || die "isolated-old recovery cannot remove the target container"
  [ "$NEW_CONTAINER" != "$OLD_CONTAINER" ] \
    || die "isolated-old recovery requires distinct old and new containers"
  if container_exists "$OLD_CONTAINER" && container_running "$OLD_CONTAINER"; then
    die "isolated-old recovery requires $OLD_CONTAINER to be stopped before the Caddy switch"
  fi
  container_exists "$NEW_CONTAINER" \
    || die "isolated-old recovery requires an existing target container: $NEW_CONTAINER"
  container_running "$NEW_CONTAINER" \
    || die "isolated-old recovery requires a running target container: $NEW_CONTAINER"
else
  container_exists "$OLD_CONTAINER" || die "old container $OLD_CONTAINER does not exist"
  container_running "$OLD_CONTAINER" || die "old container $OLD_CONTAINER is not running; refusing to release"
fi
docker network inspect "$NETWORK" >/dev/null 2>&1 || die "Docker network $NETWORK does not exist"
docker volume inspect "$DATA_VOLUME" >/dev/null 2>&1 || die "Docker volume $DATA_VOLUME does not exist"
if [ "${UNIFIED_PAYMENT_ENABLED:-false}" = true ]; then
  docker volume inspect "$UNIFIED_PAYMENT_VAULT_VOLUME" >/dev/null 2>&1 \
    || die "Docker volume $UNIFIED_PAYMENT_VAULT_VOLUME does not exist"
fi

if [ "$DEPENDENCY_MODE" = external ]; then
  if container_exists "$NEW_CONTAINER"; then
    if [ "$PRECREATE_ONLY" = true ]; then
      container_matches_external_runtime "$NEW_CONTAINER" no false \
        || die "existing external precreated target does not match the requested image, dependency configuration, data volume, or read-only CA mounts"
    elif container_running "$NEW_CONTAINER"; then
      container_matches_external_runtime "$NEW_CONTAINER" unless-stopped true \
        || die "running external target does not match the requested image, dependency configuration, data volume, or read-only CA mounts"
    else
      container_matches_external_runtime "$NEW_CONTAINER" no false \
        || die "stopped external target does not match the requested precreated configuration; refusing to reuse or remove it"
    fi
  else
    if [ "$PRECREATE_ONLY" != true ] && [ "$RUN_BACKUP" = true ]; then
      log "running backup before release"
      bash scripts/backup.sh
    fi
    if [ "$PULL_IMAGE" = true ]; then
      log "pulling $NEW_IMAGE"
      docker pull "$NEW_IMAGE"
    fi
    if [ "$PRECREATE_ONLY" = true ]; then
      log "precreating external target $NEW_CONTAINER from $NEW_IMAGE without starting it"
      create_external_target no
      container_matches_external_runtime "$NEW_CONTAINER" no false \
        || die "new external precreated target failed configuration verification"
    else
      log "creating external target $NEW_CONTAINER from $NEW_IMAGE"
      create_external_target no
      container_matches_external_runtime "$NEW_CONTAINER" no false \
        || die "new external target failed configuration verification before start"
    fi
  fi

  if [ "$PRECREATE_ONLY" = true ]; then
    log "external target $NEW_CONTAINER is precreated, stopped, and verified; Caddy was not changed"
    exit 0
  fi
  if ! container_running "$NEW_CONTAINER"; then
    container_matches_external_runtime "$NEW_CONTAINER" no false \
      || die "external target changed after preflight; refusing to start it"
    docker update --restart unless-stopped "$NEW_CONTAINER" >/dev/null
    docker start "$NEW_CONTAINER" >/dev/null
  fi
else
  # This branch intentionally retains the existing local release semantics.
  if container_exists "$NEW_CONTAINER"; then
    status="$(container_status "$NEW_CONTAINER")"
    if [ "$PRECREATE_ONLY" = true ]; then
      container_running "$NEW_CONTAINER" && die "cannot precreate because $NEW_CONTAINER is already running"
      [ "$REMOVE_EXISTING_NEW_CONTAINER" = true ] || die "$NEW_CONTAINER already exists with status $status"
      [ "$NEW_CONTAINER" != "$OLD_CONTAINER" ] || die "refusing to remove active old container $OLD_CONTAINER"
      log "removing existing inactive $NEW_CONTAINER with status $status"
      docker rm "$NEW_CONTAINER" >/dev/null
    elif [ "$status" = healthy ] || [ "$status" = running ]; then
	  if [ "$DUAL_NODE_RUNTIME_ENABLED" = true ]; then
		container_matches_local_runtime "$NEW_CONTAINER" true \
		  || die "running local target does not match the requested image or dual-node runtime contract"
	  fi
      log "$NEW_CONTAINER already exists with status $status; reusing it"
    else
      [ "$REMOVE_EXISTING_NEW_CONTAINER" = true ] || die "$NEW_CONTAINER already exists with status $status"
      [ "$NEW_CONTAINER" != "$OLD_CONTAINER" ] || die "refusing to remove active old container $OLD_CONTAINER"
      log "removing existing inactive $NEW_CONTAINER with status $status"
      docker rm "$NEW_CONTAINER" >/dev/null
    fi
  fi
  if ! container_exists "$NEW_CONTAINER"; then
    if [ "$PRECREATE_ONLY" != true ] && [ "$RUN_BACKUP" = true ]; then
      log "running backup before release"
      bash scripts/backup.sh
    fi
    if [ "$PULL_IMAGE" = true ]; then
      log "pulling $NEW_IMAGE"
      docker pull "$NEW_IMAGE"
    fi
    if [ "$PRECREATE_ONLY" = true ]; then
      make_runtime_env_file
      env_file="$RUNTIME_ENV_FILE"
      if [ "$DUAL_NODE_RUNTIME_ENABLED" = true ]; then
		log "precreating local target $NEW_CONTAINER from $NEW_IMAGE without starting it"
		docker create --name "$NEW_CONTAINER" --network "$NETWORK" --env-file "$env_file" \
		  --mount "type=volume,source=$DATA_VOLUME,target=/app/data" \
		  "${PAYMENT_VAULT_MOUNT_ARGS[@]+${PAYMENT_VAULT_MOUNT_ARGS[@]}}" \
		  --mount "type=bind,source=$TRAFFIC_STATE_FILE,target=$CONTAINER_TRAFFIC_STATE_PATH,readonly" \
		  --mount "type=bind,source=$BACKGROUND_STATE_FILE,target=$CONTAINER_BACKGROUND_STATE_PATH,readonly" \
		  --mount "type=bind,source=$HEALTH_TOKEN_FILE,target=$CONTAINER_HEALTH_TOKEN_PATH,readonly" \
		  --restart no "$NEW_IMAGE" >/dev/null
		container_matches_local_runtime "$NEW_CONTAINER" false \
		  || die "local precreated target failed dual-node runtime verification"
      else
		log "precreating local target $NEW_CONTAINER from $NEW_IMAGE without starting it"
		docker create --name "$NEW_CONTAINER" --network "$NETWORK" --env-file "$env_file" \
		  --mount "type=volume,source=$DATA_VOLUME,target=/app/data" \
		  "${PAYMENT_VAULT_MOUNT_ARGS[@]+${PAYMENT_VAULT_MOUNT_ARGS[@]}}" \
		  --restart no "$NEW_IMAGE" >/dev/null
      fi
    else
      log "starting $NEW_CONTAINER from $NEW_IMAGE"
      create_local_target
	  if [ "$DUAL_NODE_RUNTIME_ENABLED" = true ]; then
		container_matches_local_runtime "$NEW_CONTAINER" true \
		  || die "new local target failed dual-node runtime verification"
	  fi
    fi
  else
    log "$NEW_CONTAINER is present; skipping create"
  fi
  if [ "$PRECREATE_ONLY" = true ]; then
    container_running "$NEW_CONTAINER" && die "local precreated target is unexpectedly running"
    log "local target $NEW_CONTAINER is precreated and stopped; Caddy was not changed"
    exit 0
  fi
fi

for attempt in $(seq 1 "$HEALTH_ATTEMPTS"); do
  status="$(container_status "$NEW_CONTAINER")"
  log "health attempt=$attempt container=$NEW_CONTAINER status=$status"
  [ "$status" = healthy ] && break
  sleep "$HEALTH_INTERVAL_SECONDS"
done
status="$(container_status "$NEW_CONTAINER")"
[ "$status" = healthy ] || {
  docker logs --tail=120 "$NEW_CONTAINER" || true
  die "$NEW_CONTAINER did not become healthy"
}
log "checking app health inside $NEW_CONTAINER"
docker exec "$NEW_CONTAINER" sh -c "wget -qO- http://127.0.0.1:$APP_PORT/health >/dev/null || curl -fsS http://127.0.0.1:$APP_PORT/health >/dev/null"

changed_caddy=false
if ! grep -qF "$CADDY_UPSTREAM_TO" "$CADDYFILE"; then
  grep -qF "$CADDY_UPSTREAM_FROM" "$CADDYFILE" || die "$CADDY_UPSTREAM_FROM not found in $CADDYFILE"
  stamp="$(date +%Y%m%d-%H%M%S)"
  caddy_backup="$(mktemp "${CADDYFILE}.bak-blue-green-${stamp}.XXXXXX")"
  cp -a "$CADDYFILE" "$caddy_backup"
  log "switching Caddy upstream $CADDY_UPSTREAM_FROM -> $CADDY_UPSTREAM_TO"
  caddy_tmp="$(mktemp)"
  TEMP_FILES+=("$caddy_tmp")
  perl -0pe "s/\\Q$CADDY_UPSTREAM_FROM\\E/$CADDY_UPSTREAM_TO/g" "$CADDYFILE" >"$caddy_tmp"
  write_file_preserving_inode "$caddy_tmp" "$CADDYFILE"
  rm -f -- "$caddy_tmp"
  changed_caddy=true
else
  log "host Caddyfile already points at $CADDY_UPSTREAM_TO"
fi

log "synchronizing Caddy startup file seen inside container"
sync_caddy_startup_file
container_release_caddy="/tmp/sub2api-release-$NEW_CONTAINER.Caddyfile"
docker cp "$CADDYFILE" "$CADDY_CONTAINER:$container_release_caddy"
if ! docker exec "$CADDY_CONTAINER" caddy validate --config "$container_release_caddy"; then
  if restore_caddy_switch; then
	die "Caddy validation failed; restored host, startup, and active Caddy state"
  fi
  die "Caddy validation failed and automatic Caddy restoration failed"
fi
if ! docker exec "$CADDY_CONTAINER" caddy reload --config "$container_release_caddy"; then
  restore_caddy_switch \
	|| die "Caddy reload failed and automatic Caddy restoration failed"
  die "Caddy reload failed; restored host, startup, and active Caddy state"
fi
log "verifying active Caddy config points at $CADDY_UPSTREAM_TO"
caddy_active_config_contains "$CADDY_UPSTREAM_TO" || {
  restore_caddy_switch \
	|| die "active Caddy verification failed and automatic restoration failed"
  die "active Caddy config did not contain the target; restored the previous state"
}
if caddy_active_config_contains "$CADDY_UPSTREAM_FROM"; then
  restore_caddy_switch \
	|| die "active Caddy retained the old upstream and automatic restoration failed"
  die "active Caddy retained the old upstream; restored the previous state"
fi
log "verifying Caddy startup file seen inside container"
caddy_config_contains "$CADDY_CONFIG_PATH" "$CADDY_UPSTREAM_TO" \
  || {
	restore_caddy_switch \
	  || die "startup Caddy verification failed and automatic restoration failed"
	die "startup Caddy did not contain the target; restored the previous state"
  }
if caddy_config_contains "$CADDY_CONFIG_PATH" "$CADDY_UPSTREAM_FROM"; then
  restore_caddy_switch \
	|| die "startup Caddy retained the old upstream and automatic restoration failed"
  die "startup Caddy retained the old upstream; restored the previous state"
fi

log "starting drain monitor for $OLD_CONTAINER"
nohup env \
  APP_DIR="$APP_DIR" \
  DRAIN_CONTAINER="$OLD_CONTAINER" \
  ACTIVE_CONTAINER="$NEW_CONTAINER" \
  REQUIRED_CADDY_UPSTREAM="$CADDY_UPSTREAM_TO" \
  FORBIDDEN_CADDY_UPSTREAM="$CADDY_UPSTREAM_FROM" \
  CADDY_CONTAINER="$CADDY_CONTAINER" \
  CADDY_ACTIVE_CONFIG_PATH="$CADDY_CONFIG_PATH" \
  INTERVAL_SECONDS="$DRAIN_INTERVAL_SECONDS" \
  ACTIVE_WINDOW_SECONDS="$DRAIN_ACTIVE_WINDOW_SECONDS" \
  RETRY_DELAY_SECONDS="$DRAIN_RETRY_DELAY_SECONDS" \
  MAX_RUNTIME_SECONDS="$DRAIN_MAX_RUNTIME_SECONDS" \
  STOP_DRAIN_CONTAINER=true \
  LOG_FILE="$DRAIN_LOG_FILE" \
  PID_FILE="$DRAIN_PID_FILE" \
  scripts/sub2api-drain-monitor.sh >"$DRAIN_NOHUP_FILE" 2>&1 &
echo $! >"$DRAIN_PID_FILE"
log "release switched; drain monitor pid=$(cat "$DRAIN_PID_FILE") log=$DRAIN_LOG_FILE"
