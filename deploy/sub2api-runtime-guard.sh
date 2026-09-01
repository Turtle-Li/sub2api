#!/usr/bin/env bash

# Recover the currently selected Sub2API runtime after a host or Docker
# restart.  This is deliberately a narrow, Type=oneshot repair path: it
# never builds or pulls an image, and delegates every Caddy switch to the
# existing blue-green helper which owns the bind-mount/startup-file safety
# protocol.

set -Eeuo pipefail

# Use the same root-owned configuration file as the release helpers.  systemd
# supplies it as an EnvironmentFile too, while sourcing it here makes a manual
# invocation resolve the same non-secret settings.
CONFIG_FILE="${SUB2API_RUNTIME_GUARD_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"
if [ -r "$CONFIG_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$CONFIG_FILE"
  set +a
fi

APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
CADDYFILE="${SUB2API_RUNTIME_GUARD_CADDYFILE:-${APP_DIR}/Caddyfile}"
CADDY_TRANSACTION_PATH="${APP_DIR}/.gcp-tw-caddy-transaction.env"
CADDY_CUSTOMER_HOST_TRANSACTION_PATH="${APP_DIR}/.cf-opt-totools-caddy.env"
CADDY_CONTAINER="${SUB2API_CADDY_CONTAINER:-sub2api-caddy}"
CADDY_CONFIG_PATH="${SUB2API_RUNTIME_GUARD_CADDY_CONFIG_PATH:-/etc/caddy/Caddyfile}"
POSTGRES_CONTAINER="${SUB2API_RUNTIME_GUARD_POSTGRES_CONTAINER:-sub2api-postgres}"
REDIS_CONTAINER="${SUB2API_RUNTIME_GUARD_REDIS_CONTAINER:-sub2api-redis}"
# An unset mode retains the legacy local-dependency behavior. An explicitly
# blank or unknown value is rejected rather than touching the wrong runtime.
DEPENDENCY_MODE="${SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE-local}"
BLUE_GREEN_SCRIPT="${SUB2API_RUNTIME_GUARD_BLUE_GREEN_SCRIPT:-${APP_DIR}/scripts/sub2api-blue-green-release.sh}"
NODE_STATE_SCRIPT="${SUB2API_NODE_STATE_SCRIPT:-${APP_DIR}/scripts/sub2api-node-state.sh}"
PUBLIC_HEALTH_URL="${SUB2API_PUBLIC_HEALTH_URL:-https://www.turtleligpt.com/health}"
PUBLIC_HEALTH_RESOLVE="${SUB2API_PUBLIC_HEALTH_RESOLVE:-}"
MAINTENANCE_LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-/run/lock/sub2api-maintenance.lock}"
STATE_DIR="${SUB2API_RUNTIME_GUARD_STATE_DIR:-/var/lib/sub2api-runtime-guard}"
FAILURE_FILE="${STATE_DIR}/last-failure.env"

# The retry controls cover container health, the Caddy admin API, and the
# fallback's in-container health endpoint.  Cooldown starts only after the
# active slot failed its own restart, so a healthy active slot always wins.
RETRY_ATTEMPTS="${SUB2API_RUNTIME_GUARD_RETRY_ATTEMPTS:-20}"
RETRY_INTERVAL_SECONDS="${SUB2API_RUNTIME_GUARD_RETRY_INTERVAL_SECONDS:-3}"
COOLDOWN_SECONDS="${SUB2API_RUNTIME_GUARD_COOLDOWN_SECONDS:-300}"
PUBLIC_HEALTH_ATTEMPTS="${SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_ATTEMPTS:-3}"
PUBLIC_HEALTH_INTERVAL_SECONDS="${SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_INTERVAL_SECONDS:-3}"
PUBLIC_HEALTH_MAX_TIME_SECONDS="${SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_MAX_TIME_SECONDS:-20}"
APP_PORT="${SUB2API_RUNTIME_GUARD_APP_PORT:-8080}"
DUAL_NODE_RUNTIME_ENABLED="${SUB2API_DUAL_NODE_RUNTIME_ENABLED:-false}"
RUNTIME_GUARD_NETWORK="${SUB2API_RUNTIME_GUARD_NETWORK:-sub2api_default}"
RUNTIME_GUARD_DATA_VOLUME="${SUB2API_RUNTIME_GUARD_DATA_VOLUME:-sub2api_sub2api_data}"
EXTERNAL_RUNTIME_ENV_FILE="${SUB2API_EXTERNAL_RUNTIME_ENV_FILE:-}"
EXTERNAL_CA_FILE="${SUB2API_EXTERNAL_CA_FILE:-}"
TRAFFIC_STATE_FILE="${SUB2API_TRAFFIC_STATE_FILE_HOST:-${SUB2API_TRAFFIC_STATE_FILE:-/var/lib/sub2api/runtime/traffic-state}}"
BACKGROUND_STATE_DIR="${SUB2API_BACKGROUND_STATE_DIR_HOST:-/var/lib/sub2api/runtime/background}"
HEALTH_TOKEN_FILE="${SUB2API_INTERNAL_HEALTH_TOKEN_FILE:-${APP_DIR}/secrets/internal-health-token}"
CONTAINER_PG_CA_PATH="/etc/sub2api-db-ca/ca.crt"
CONTAINER_REDIS_CA_PATH="/etc/ssl/certs/sub2api-db-ca.pem"
CONTAINER_TRAFFIC_STATE_PATH="/run/sub2api-runtime/traffic-state"
CONTAINER_BACKGROUND_STATE_PATH="/run/sub2api-runtime/background-state"
CONTAINER_HEALTH_TOKEN_PATH="/run/sub2api-runtime/health-token"

ACTIVE_CONTAINER=""
ACTIVE_UPSTREAM=""
FALLBACK_CONTAINER=""
FALLBACK_IMAGE=""
EXTERNAL_ENV_KEYS=(
  DATABASE_HOST DATABASE_PORT DATABASE_USER DATABASE_PASSWORD DATABASE_DBNAME DATABASE_SSLMODE
  REDIS_HOST REDIS_PORT REDIS_USERNAME REDIS_PASSWORD REDIS_DB REDIS_ENABLE_TLS
)
EXTERNAL_OVERRIDE_KEYS=("${EXTERNAL_ENV_KEYS[@]}" PGSSLROOTCERT)
RUNTIME_OVERRIDE_KEYS=(
  SUB2API_TRAFFIC_STATE_FILE SUB2API_BACKGROUND_STATE_FILE SUB2API_INTERNAL_HEALTH_TOKEN_FILE
)

timestamp() {
  date '+%Y-%m-%d %H:%M:%S'
}

log() {
  printf '[%s] %s\n' "$(timestamp)" "$*"
}

die() {
  log "ERROR: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

require_positive_integer() {
  case "$2" in
    ''|*[!0-9]*) die "$1 must be a positive integer" ;;
  esac
  [ "$2" -gt 0 ] || die "$1 must be a positive integer"
}

require_non_negative_integer() {
  case "$2" in
    ''|*[!0-9]*) die "$1 must be a non-negative integer" ;;
  esac
}

require_bool() {
  case "$2" in
    true|false) ;;
    *) die "$1 must be true or false" ;;
  esac
}

validate_health_resolve() {
  local value="$1" url="$2" host port address extra octet url_authority url_host url_port
  local -a octets
  [ -n "$value" ] || return 0
  case "$value" in
    -*|*$'\n'*|*$'\r'*|*' '*) die "SUB2API_PUBLIC_HEALTH_RESOLVE contains unsupported characters" ;;
  esac
  IFS=: read -r host port address extra <<<"$value"
  [ -n "$host" ] && [ -n "$port" ] && [ -n "$address" ] && [ -z "$extra" ] \
    || die "SUB2API_PUBLIC_HEALTH_RESOLVE must be HOST:PORT:IPV4"
  case "$host" in
    *[!A-Za-z0-9.-]*|.*|*..*|*.) die "SUB2API_PUBLIC_HEALTH_RESOLVE has an invalid host" ;;
  esac
  require_positive_integer SUB2API_PUBLIC_HEALTH_RESOLVE_PORT "$port"
  [ "$port" -le 65535 ] || die "SUB2API_PUBLIC_HEALTH_RESOLVE port is out of range"
  IFS=. read -r -a octets <<<"$address"
  [ "${#octets[@]}" -eq 4 ] || die "SUB2API_PUBLIC_HEALTH_RESOLVE must use IPv4"
  for octet in "${octets[@]}"; do
    case "$octet" in ''|*[!0-9]*) die "SUB2API_PUBLIC_HEALTH_RESOLVE must use IPv4" ;; esac
    [ "$octet" -le 255 ] || die "SUB2API_PUBLIC_HEALTH_RESOLVE must use IPv4"
  done
  case "$url" in
    https://*) url_port=443 ;;
    http://*) url_port=80 ;;
    *) die "SUB2API_PUBLIC_HEALTH_URL must use http or https when an origin override is configured" ;;
  esac
  url_authority="${url#*://}"
  url_authority="${url_authority%%/*}"
  case "$url_authority" in
    *@*|'') die "SUB2API_PUBLIC_HEALTH_URL has an invalid authority" ;;
    *:*)
      url_host="${url_authority%%:*}"
      url_port="${url_authority##*:}"
      ;;
    *) url_host="$url_authority" ;;
  esac
  [ "$url_host" = "$host" ] && [ "$url_port" = "$port" ] \
    || die "SUB2API_PUBLIC_HEALTH_RESOLVE host/port must match SUB2API_PUBLIC_HEALTH_URL"
}

container_exists() {
  docker inspect "$1" >/dev/null 2>&1
}

container_field() {
  docker inspect "$1" --format "$2" 2>/dev/null || true
}

container_running() {
  [ "$(container_field "$1" '{{.State.Running}}')" = "true" ]
}

container_health() {
  container_field "$1" '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}'
}

container_is_healthy() {
  container_running "$1" && [ "$(container_health "$1")" = "healthy" ]
}

container_image() {
  container_field "$1" '{{.Config.Image}}'
}

container_oom_killed() {
  [ "$(container_field "$1" '{{.State.OOMKilled}}')" = "true" ]
}

container_exit_code() {
  container_field "$1" '{{.State.ExitCode}}'
}

validate_external_file_path() {
  local label="$1" path="$2" canonical_path
  case "$path" in /*) ;; *) die "$label must be an absolute path" ;; esac
  case "$path" in
    *$'\n'*|*$'\r'*|*,*|*'|'*) die "$label contains an unsupported path character" ;;
  esac
  canonical_path="$(realpath -e -- "$path")" \
    || die "$label must resolve to an existing canonical file"
  [ "$canonical_path" = "$path" ] \
    || die "$label must not traverse a symlink or contain a non-canonical path"
}

external_value() {
  local key="$1"
  sed -n "/^$key=/ { s/^$key=//; p; q; }" "$EXTERNAL_RUNTIME_ENV_FILE"
}

load_external_runtime_env() {
  local line key value count
  validate_external_file_path SUB2API_EXTERNAL_RUNTIME_ENV_FILE "$EXTERNAL_RUNTIME_ENV_FILE"
  [ -f "$EXTERNAL_RUNTIME_ENV_FILE" ] && [ ! -L "$EXTERNAL_RUNTIME_ENV_FILE" ] \
    || die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE must be a regular non-symlink file"
  [ "$(stat -c '%u' "$EXTERNAL_RUNTIME_ENV_FILE")" = 0 ] \
    || die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE must be owned by root"
  [ "$(stat -c '%a' "$EXTERNAL_RUNTIME_ENV_FILE")" = 600 ] \
    || die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE must have mode 0600"

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
    count="$(grep -c "^$key=" "$EXTERNAL_RUNTIME_ENV_FILE" || true)"
    [ "$count" = 1 ] || die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE contains a duplicate key"
    [ -n "$value" ] || die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE contains an empty required setting"
  done <"$EXTERNAL_RUNTIME_ENV_FILE"

  for key in "${EXTERNAL_ENV_KEYS[@]}"; do
    [ -n "$(external_value "$key")" ] \
      || die "SUB2API_EXTERNAL_RUNTIME_ENV_FILE is missing a required setting"
  done
  [ "$(external_value DATABASE_SSLMODE)" = verify-full ] \
    || die "DATABASE_SSLMODE must be verify-full in external dependency mode"
  [ "$(external_value REDIS_ENABLE_TLS)" = true ] \
    || die "REDIS_ENABLE_TLS must be true in external dependency mode"
  for key in DATABASE_PORT REDIS_PORT REDIS_DB; do
    case "$(external_value "$key")" in
      ''|*[!0-9]*) die "$key must be numeric in external dependency mode" ;;
    esac
  done
}

validate_external_ca_file() {
  local mode group_permissions other_permissions
  validate_external_file_path SUB2API_EXTERNAL_CA_FILE "$EXTERNAL_CA_FILE"
  [ -f "$EXTERNAL_CA_FILE" ] && [ ! -L "$EXTERNAL_CA_FILE" ] \
    || die "SUB2API_EXTERNAL_CA_FILE must be a regular non-symlink file"
  [ "$(stat -c '%u' "$EXTERNAL_CA_FILE")" = 0 ] \
    || die "SUB2API_EXTERNAL_CA_FILE must be owned by root"
  mode="$(stat -c '%a' "$EXTERNAL_CA_FILE")"
  case "$mode" in ''|*[!0-9]*) die "could not read SUB2API_EXTERNAL_CA_FILE mode" ;; esac
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
  validate_external_file_path "$label" "$path"
  [ -f "$path" ] && [ ! -L "$path" ] || die "$label must be a regular non-symlink file"
  [ "$(stat -c '%u' "$path")" = "$expected_uid" ] || die "$label has an unexpected owner"
  [ "$(stat -c '%g' "$path")" = "$expected_gid" ] || die "$label has an unexpected group"
  [ "$(stat -c '%a' "$path")" = "$expected_mode" ] || die "$label has an unexpected mode"
}

validate_runtime_files_for_container() {
  local container_name="$1"
  [ "$DUAL_NODE_RUNTIME_ENABLED" = true ] || return 0
  validate_runtime_file SUB2API_TRAFFIC_STATE_FILE "$TRAFFIC_STATE_FILE" 0 0 644
  validate_runtime_file SUB2API_BACKGROUND_STATE_FILE "${BACKGROUND_STATE_DIR}/${container_name}" 0 0 644
  validate_runtime_file SUB2API_INTERNAL_HEALTH_TOKEN_FILE "$HEALTH_TOKEN_FILE" 1000 1000 600
}

environment_value_once() {
  local environment="$1" key="$2"
  printf '%s\n' "$environment" | awk -v expected_key="$key" '
    index($0, expected_key "=") == 1 {
      count += 1
      value = substr($0, length(expected_key) + 2)
    }
    END {
      if (count != 1) exit 1
      print value
    }'
}

application_runtime_matches() {
  local container_name="$1" networks mounts environment network_count mount_count expected_mount_count
  local key expected_value actual_value
  container_exists "$container_name" || return 1
  [ "$(container_field "$container_name" '{{.HostConfig.RestartPolicy.Name}}')" = unless-stopped ] || return 1
  networks="$(docker inspect "$container_name" --format '{{range $network, $_ := .NetworkSettings.Networks}}{{println $network}}{{end}}')" || return 1
  mounts="$(docker inspect "$container_name" --format '{{range .Mounts}}{{if eq .Type "volume"}}{{printf "%s|%s|%s|%t\n" .Type .Name .Destination .RW}}{{else}}{{printf "%s|%s|%s|%t\n" .Type .Source .Destination .RW}}{{end}}{{end}}')" || return 1
  environment="$(docker inspect "$container_name" --format '{{range .Config.Env}}{{println .}}{{end}}')" || return 1

  network_count="$(printf '%s\n' "$networks" | awk 'NF { count += 1 } END { print count + 0 }')"
  [ "$network_count" -eq 1 ] && printf '%s\n' "$networks" | grep -qxF "$RUNTIME_GUARD_NETWORK" || return 1
  mount_count="$(printf '%s\n' "$mounts" | awk 'NF { count += 1 } END { print count + 0 }')"
  expected_mount_count=1
  printf '%s\n' "$mounts" | grep -qxF "volume|$RUNTIME_GUARD_DATA_VOLUME|/app/data|true" || return 1
  if [ "$DEPENDENCY_MODE" = external ]; then
    expected_mount_count=$((expected_mount_count + 2))
    printf '%s\n' "$mounts" | grep -qxF "bind|$EXTERNAL_CA_FILE|$CONTAINER_PG_CA_PATH|false" || return 1
    printf '%s\n' "$mounts" | grep -qxF "bind|$EXTERNAL_CA_FILE|$CONTAINER_REDIS_CA_PATH|false" || return 1
    for key in "${EXTERNAL_OVERRIDE_KEYS[@]}"; do
      if [ "$key" = PGSSLROOTCERT ]; then
        expected_value="$CONTAINER_PG_CA_PATH"
      else
        expected_value="$(external_value "$key")"
      fi
      actual_value="$(environment_value_once "$environment" "$key")" || return 1
      [ "$actual_value" = "$expected_value" ] || return 1
    done
  fi
  if [ "$DUAL_NODE_RUNTIME_ENABLED" = true ]; then
    expected_mount_count=$((expected_mount_count + 3))
    printf '%s\n' "$mounts" | grep -qxF "bind|$TRAFFIC_STATE_FILE|$CONTAINER_TRAFFIC_STATE_PATH|false" || return 1
    printf '%s\n' "$mounts" | grep -qxF "bind|${BACKGROUND_STATE_DIR}/${container_name}|$CONTAINER_BACKGROUND_STATE_PATH|false" || return 1
    printf '%s\n' "$mounts" | grep -qxF "bind|$HEALTH_TOKEN_FILE|$CONTAINER_HEALTH_TOKEN_PATH|false" || return 1
    for key in "${RUNTIME_OVERRIDE_KEYS[@]}"; do
      case "$key" in
        SUB2API_TRAFFIC_STATE_FILE) expected_value="$CONTAINER_TRAFFIC_STATE_PATH" ;;
        SUB2API_BACKGROUND_STATE_FILE) expected_value="$CONTAINER_BACKGROUND_STATE_PATH" ;;
        SUB2API_INTERNAL_HEALTH_TOKEN_FILE) expected_value="$CONTAINER_HEALTH_TOKEN_PATH" ;;
      esac
      actual_value="$(environment_value_once "$environment" "$key")" || return 1
      [ "$actual_value" = "$expected_value" ] || return 1
    done
  else
    for key in "${RUNTIME_OVERRIDE_KEYS[@]}"; do
      if printf '%s\n' "$environment" | grep -q "^${key}="; then
        return 1
      fi
    done
  fi
  [ "$mount_count" -eq "$expected_mount_count" ] || return 1
  return 0
}

verify_application_runtime_before_lifecycle() (
  # The validators deliberately use die() so strict callers get precise
  # diagnostics. Isolate them in a subshell so lifecycle callers can convert
  # validation failure into a transaction result and persist cooldown state.
  local container_name="$1"
  if [ "$DEPENDENCY_MODE" = external ]; then
    load_external_runtime_env
    validate_external_ca_file
  fi
  validate_runtime_files_for_container "$container_name"
  if [ "$DEPENDENCY_MODE" = external ] || [ "$DUAL_NODE_RUNTIME_ENABLED" = true ]; then
    application_runtime_matches "$container_name" || {
      log "application runtime verification failed before lifecycle action: ${container_name}" >&2
      return 1
    }
  fi
  return 0
)

unique_upstream_from_file() {
  local config_file="$1"
  local matches
  local count

  [ -r "$config_file" ] || return 1
  matches="$(grep -oE 'sub2api(-(blue|green))?:8080' "$config_file" | sort -u || true)"
  count="$(printf '%s\n' "$matches" | awk 'NF { count += 1 } END { print count + 0 }')"
  [ "$count" -eq 1 ] || return 1
  printf '%s\n' "$matches"
}

unique_upstream_from_text() {
  local config_text="$1"
  local matches
  local count

  matches="$(printf '%s\n' "$config_text" | grep -oE 'sub2api(-(blue|green))?:8080' | sort -u || true)"
  count="$(printf '%s\n' "$matches" | awk 'NF { count += 1 } END { print count + 0 }')"
  [ "$count" -eq 1 ] || return 1
  printf '%s\n' "$matches"
}

caddy_admin_config() {
  docker exec "$CADDY_CONTAINER" sh -c \
    'wget -qO- http://127.0.0.1:2019/config/ 2>/dev/null || curl -fsS http://127.0.0.1:2019/config/'
}

caddy_startup_config() {
  docker exec \
    -e "CADDY_CHECK_PATH=${CADDY_CONFIG_PATH}" \
    "$CADDY_CONTAINER" \
    sh -c 'cat "$CADDY_CHECK_PATH"'
}

app_internal_health() {
  local container_name="$1"

  docker exec \
    -e "SUB2API_GUARD_APP_PORT=${APP_PORT}" \
    "$container_name" \
    sh -c 'wget -qO- "http://127.0.0.1:${SUB2API_GUARD_APP_PORT}/health" >/dev/null || curl -fsS "http://127.0.0.1:${SUB2API_GUARD_APP_PORT}/health" >/dev/null' \
    >/dev/null
}

wait_for_container_health() {
  local container_name="$1"
  local description="$2"
  local attempt=1

  while [ "$attempt" -le "$RETRY_ATTEMPTS" ]; do
    if container_is_healthy "$container_name"; then
      return 0
    fi
    log "waiting for ${description}: attempt=${attempt}/${RETRY_ATTEMPTS} status=$(container_health "$container_name")"
    if [ "$attempt" -lt "$RETRY_ATTEMPTS" ]; then
      sleep "$RETRY_INTERVAL_SECONDS"
    fi
    attempt=$((attempt + 1))
  done
  return 1
}

wait_for_app_ready() {
  local container_name="$1"
  local attempt=1

  while [ "$attempt" -le "$RETRY_ATTEMPTS" ]; do
    if container_is_healthy "$container_name" && app_internal_health "$container_name"; then
      return 0
    fi
    log "waiting for ${container_name} health and internal /health: attempt=${attempt}/${RETRY_ATTEMPTS} status=$(container_health "$container_name")"
    if [ "$attempt" -lt "$RETRY_ATTEMPTS" ]; then
      sleep "$RETRY_INTERVAL_SECONDS"
    fi
    attempt=$((attempt + 1))
  done
  return 1
}

wait_for_caddy_admin() {
  local attempt=1
  local active_config

  while [ "$attempt" -le "$RETRY_ATTEMPTS" ]; do
    if container_running "$CADDY_CONTAINER" \
      && active_config="$(caddy_admin_config 2>/dev/null)" \
      && [ -n "$active_config" ]; then
      return 0
    fi
    log "waiting for Caddy admin API: attempt=${attempt}/${RETRY_ATTEMPTS}"
    if [ "$attempt" -lt "$RETRY_ATTEMPTS" ]; then
      sleep "$RETRY_INTERVAL_SECONDS"
    fi
    attempt=$((attempt + 1))
  done
  return 1
}

wait_for_public_health() {
  local attempt=1
  local -a curl_args=(-fsS --max-time "$PUBLIC_HEALTH_MAX_TIME_SECONDS")
  if [ -n "$PUBLIC_HEALTH_RESOLVE" ]; then
    curl_args+=(--resolve "$PUBLIC_HEALTH_RESOLVE")
  fi

  while [ "$attempt" -le "$PUBLIC_HEALTH_ATTEMPTS" ]; do
    if curl "${curl_args[@]}" "$PUBLIC_HEALTH_URL" >/dev/null; then
      return 0
    fi
    log "waiting for public health endpoint: attempt=${attempt}/${PUBLIC_HEALTH_ATTEMPTS} url=${PUBLIC_HEALTH_URL}"
    if [ "$attempt" -lt "$PUBLIC_HEALTH_ATTEMPTS" ]; then
      sleep "$PUBLIC_HEALTH_INTERVAL_SECONDS"
    fi
    attempt=$((attempt + 1))
  done
  return 1
}

ensure_dependency() {
  local container_name="$1"
  local description="$2"

  container_exists "$container_name" || die "${description} container is missing: ${container_name}"
  if ! container_running "$container_name"; then
    log "starting stopped ${description} container ${container_name}"
    docker start "$container_name" >/dev/null || die "could not start ${description} container ${container_name}"
  fi
  if wait_for_container_health "$container_name" "$description"; then
    return 0
  fi

  log "restarting unhealthy ${description} container ${container_name}"
  docker restart "$container_name" >/dev/null \
    || die "could not restart ${description} container ${container_name}"
  wait_for_container_health "$container_name" "$description" \
    || die "${description} did not become healthy after restart: ${container_name}"
}

ensure_caddy() {
  container_exists "$CADDY_CONTAINER" || die "Caddy container is missing: ${CADDY_CONTAINER}"
  if ! container_running "$CADDY_CONTAINER"; then
    log "starting stopped Caddy container ${CADDY_CONTAINER}"
    docker start "$CADDY_CONTAINER" >/dev/null || die "could not start Caddy container ${CADDY_CONTAINER}"
  fi
  if wait_for_caddy_admin; then
    return 0
  fi

  log "restarting Caddy container after its admin API remained unavailable: ${CADDY_CONTAINER}"
  docker restart "$CADDY_CONTAINER" >/dev/null || die "could not restart Caddy container ${CADDY_CONTAINER}"
  wait_for_caddy_admin || die "Caddy is not running with a readable admin configuration after restart"
}

verify_caddy_matches() {
  local expected_upstream="$1"
  local host_upstream
  local active_config
  local admin_upstream

  if ! host_upstream="$(unique_upstream_from_file "$CADDYFILE")"; then
    log "Caddyfile must contain exactly one sub2api upstream: ${CADDYFILE}" >&2
    return 1
  fi
  if ! active_config="$(caddy_admin_config)"; then
    log "could not read the Caddy admin configuration" >&2
    return 1
  fi
  if ! admin_upstream="$(unique_upstream_from_text "$active_config")"; then
    log "Caddy admin configuration must contain exactly one sub2api upstream" >&2
    return 1
  fi
  if [ "$host_upstream" != "$expected_upstream" ] || [ "$admin_upstream" != "$expected_upstream" ]; then
    log "Caddy host/admin mismatch: host=${host_upstream} admin=${admin_upstream} expected=${expected_upstream}" >&2
    return 1
  fi
  return 0
}

verify_caddy_startup_file() {
  local expected_upstream="$1"
  local startup_config
  local startup_upstream

  if ! startup_config="$(caddy_startup_config)"; then
    log "could not read Caddy startup file ${CADDY_CONFIG_PATH}" >&2
    return 1
  fi
  if ! startup_upstream="$(unique_upstream_from_text "$startup_config")"; then
    log "Caddy startup file must contain exactly one sub2api upstream" >&2
    return 1
  fi
  if [ "$startup_upstream" != "$expected_upstream" ]; then
    log "Caddy startup file mismatch: startup=${startup_upstream} expected=${expected_upstream}" >&2
    return 1
  fi
  return 0
}

write_failure_state() {
  local reason="$1"
  local temporary_file

  mkdir -p "$STATE_DIR"
  umask 077
  temporary_file="$(mktemp "${STATE_DIR}/.last-failure.XXXXXX")"
  printf 'failed_at_epoch=%s\nactive_container=%s\nreason=%s\n' \
    "$(date +%s)" "$ACTIVE_CONTAINER" "$reason" >"$temporary_file"
  mv -f "$temporary_file" "$FAILURE_FILE"
}

clear_failure_state() {
  rm -f "$FAILURE_FILE"
}

cooldown_is_active() {
  local failed_container
  local failed_at
  local now_epoch
  local elapsed

  [ "$COOLDOWN_SECONDS" -gt 0 ] || return 1
  [ -r "$FAILURE_FILE" ] || return 1
  failed_container="$(sed -n 's/^active_container=//p' "$FAILURE_FILE" | head -n 1)"
  [ "$failed_container" = "$ACTIVE_CONTAINER" ] || return 1
  failed_at="$(sed -n 's/^failed_at_epoch=//p' "$FAILURE_FILE" | head -n 1)"
  case "$failed_at" in
    ''|*[!0-9]*) return 1 ;;
  esac
  now_epoch="$(date +%s)"
  elapsed=$((now_epoch - failed_at))
  if [ "$elapsed" -lt "$COOLDOWN_SECONDS" ]; then
    log "runtime recovery is cooling down for another $((COOLDOWN_SECONDS - elapsed))s; leaving the failed active slot isolated"
    return 0
  fi
  return 1
}

try_restore_active() {
  local status

  if ! container_exists "$ACTIVE_CONTAINER"; then
    log "active container is absent: ${ACTIVE_CONTAINER}"
    return 1
  fi

  status="$(container_health "$ACTIVE_CONTAINER")"
  if ! container_running "$ACTIVE_CONTAINER"; then
    verify_application_runtime_before_lifecycle "$ACTIVE_CONTAINER" || return 1
    log "recovering stopped active container ${ACTIVE_CONTAINER}"
    docker start "$ACTIVE_CONTAINER" >/dev/null || return 1
  elif [ "$status" = "unhealthy" ]; then
    verify_application_runtime_before_lifecycle "$ACTIVE_CONTAINER" || return 1
    log "restarting unhealthy active container ${ACTIVE_CONTAINER}"
    docker restart "$ACTIVE_CONTAINER" >/dev/null || return 1
  fi

  if wait_for_app_ready "$ACTIVE_CONTAINER"; then
    return 0
  fi

  # A merely-starting container gets its configured startup window first.  If
  # it still cannot serve internal health, make one explicit same-slot restart
  # before considering any historical image.
  if container_running "$ACTIVE_CONTAINER"; then
    verify_application_runtime_before_lifecycle "$ACTIVE_CONTAINER" || return 1
    log "active container did not recover; retrying one explicit restart: ${ACTIVE_CONTAINER}"
    docker restart "$ACTIVE_CONTAINER" >/dev/null || return 1
    if wait_for_app_ready "$ACTIVE_CONTAINER"; then
      return 0
    fi
  fi
  return 1
}

isolate_active_container() {
  if ! container_exists "$ACTIVE_CONTAINER"; then
    log "active container is already absent and therefore isolated: ${ACTIVE_CONTAINER}"
    return 0
  fi
  if container_running "$ACTIVE_CONTAINER"; then
    log "stopping failed active container before any fallback starts: ${ACTIVE_CONTAINER}"
    docker stop "$ACTIVE_CONTAINER" >/dev/null || return 1
  fi
  if container_running "$ACTIVE_CONTAINER"; then
    log "failed active container remained running after isolation attempt: ${ACTIVE_CONTAINER}" >&2
    return 1
  fi
  return 0
}

fallback_candidates() {
  case "$ACTIVE_CONTAINER" in
    sub2api-blue)
      printf '%s\n' sub2api-green sub2api
      ;;
    sub2api-green)
      printf '%s\n' sub2api-blue sub2api
      ;;
    sub2api)
      printf '%s\n' sub2api-green sub2api-blue
      ;;
    *)
      return 1
      ;;
  esac
}

running_inactive_apps() {
  local candidate

  for candidate in sub2api-blue sub2api-green sub2api; do
    [ "$candidate" = "$ACTIVE_CONTAINER" ] && continue
    if container_exists "$candidate" && container_running "$candidate"; then
      printf '%s\n' "$candidate"
    fi
  done
}

select_known_good_fallback() {
  local candidate
  local image

  FALLBACK_CONTAINER=""
  FALLBACK_IMAGE=""
  while IFS= read -r candidate; do
    [ -n "$candidate" ] || continue
    if ! container_exists "$candidate"; then
      log "historical fallback candidate is absent: ${candidate}"
      continue
    fi
    # A running historical app could be a connection drain or an unexpected
    # queue consumer.  Do not create another concurrently healthy app while
    # recovery is in progress; an operator must resolve that state explicitly.
    if container_running "$candidate"; then
      log "refusing fallback while historical candidate is still running: ${candidate}" >&2
      return 1
    fi
    if container_oom_killed "$candidate"; then
      log "rejecting OOM-killed historical candidate: ${candidate}"
      continue
    fi
    if [ "$(container_exit_code "$candidate")" != "0" ]; then
      log "rejecting non-zero-exit historical candidate: ${candidate}"
      continue
    fi
    image="$(container_image "$candidate")"
    if [ -z "$image" ]; then
      log "rejecting historical candidate without its image reference: ${candidate}"
      continue
    fi
    FALLBACK_CONTAINER="$candidate"
    FALLBACK_IMAGE="$image"
    log "selected last known good fallback: container=${FALLBACK_CONTAINER} image=${FALLBACK_IMAGE}"
    return 0
  done < <(fallback_candidates)
  return 1
}

start_fallback() {
  verify_application_runtime_before_lifecycle "$FALLBACK_CONTAINER" || return 1
  log "starting historical fallback ${FALLBACK_CONTAINER}"
  docker start "$FALLBACK_CONTAINER" >/dev/null || return 1
  if wait_for_app_ready "$FALLBACK_CONTAINER"; then
    return 0
  fi
  log "fallback did not become healthy; stopping it before trying no further candidates: ${FALLBACK_CONTAINER}" >&2
  docker stop "$FALLBACK_CONTAINER" >/dev/null || true
  return 1
}

switch_caddy_to_fallback() {
  local fallback_upstream="${FALLBACK_CONTAINER}:${APP_PORT}"

  [ -x "$BLUE_GREEN_SCRIPT" ] || {
    log "blue-green script is missing or not executable: ${BLUE_GREEN_SCRIPT}" >&2
    return 1
  }

  # The active container was stopped above.  The helper sees an already
  # healthy, existing NEW_CONTAINER, so it only performs its audited Caddy
  # host-file, bind-mount startup-file, validation, reload, and admin checks.
  if ! env \
    APP_DIR="$APP_DIR" \
    CADDY_CONTAINER="$CADDY_CONTAINER" \
    CADDYFILE="$CADDYFILE" \
    CADDY_CONFIG_PATH="$CADDY_CONFIG_PATH" \
    OLD_CONTAINER="$ACTIVE_CONTAINER" \
    NEW_CONTAINER="$FALLBACK_CONTAINER" \
    NEW_IMAGE="$FALLBACK_IMAGE" \
    CADDY_UPSTREAM_FROM="$ACTIVE_UPSTREAM" \
    CADDY_UPSTREAM_TO="$fallback_upstream" \
    RUN_BACKUP=false \
    PULL_IMAGE=false \
    REMOVE_EXISTING_NEW_CONTAINER=false \
    ALLOW_ISOLATED_OLD_CONTAINER=true \
    HEALTH_ATTEMPTS="$RETRY_ATTEMPTS" \
    HEALTH_INTERVAL_SECONDS="$RETRY_INTERVAL_SECONDS" \
    bash "$BLUE_GREEN_SCRIPT"; then
    return 1
  fi
  return 0
}

fence_fallback_if_caddy_still_uses_failed_active() {
  # A helper failure may have rolled Caddy fully back to the failed active
  # upstream.  Only in that conclusive state is it safe to stop the fallback;
  # if any Caddy view is ambiguous, leave state untouched and fail closed on
  # the next timer run instead of guessing which container serves traffic.
  if verify_caddy_matches "$ACTIVE_UPSTREAM" \
    && verify_caddy_startup_file "$ACTIVE_UPSTREAM"; then
    log "Caddy still uses the failed active upstream; stopping inactive fallback ${FALLBACK_CONTAINER}"
    docker stop "$FALLBACK_CONTAINER" >/dev/null \
      || log "WARNING: could not stop inactive fallback ${FALLBACK_CONTAINER}" >&2
  fi
}

verify_fallback_switch() {
  local fallback_upstream="${FALLBACK_CONTAINER}:${APP_PORT}"

  if container_running "$ACTIVE_CONTAINER"; then
    log "failed active container unexpectedly restarted during fallback: ${ACTIVE_CONTAINER}" >&2
    return 1
  fi
  if ! container_is_healthy "$FALLBACK_CONTAINER"; then
    log "fallback lost health after Caddy switch: ${FALLBACK_CONTAINER}" >&2
    return 1
  fi
  verify_caddy_matches "$fallback_upstream" || return 1
  verify_caddy_startup_file "$fallback_upstream" || return 1
  wait_for_public_health || return 1
  return 0
}

reconcile_local_release_state() {
  local result
  [ -x "$NODE_STATE_SCRIPT" ] || {
    log "node state helper is missing or not executable: ${NODE_STATE_SCRIPT}" >&2
    return 1
  }
  result="$(env SUB2API_NODE_STATE_LOCK_HELD=1 "$NODE_STATE_SCRIPT" recover-local)" \
    || return 1
  if [ "$result" != NO_LOCAL_RECOVERY ]; then
    log "node runtime state reconciled after interrupted local release: ${result}"
  fi
}

acquire_maintenance_lock() {
  local lock_dir

  case "$MAINTENANCE_LOCK_FILE" in
    /*) ;;
    *) die "SUB2API_MAINTENANCE_LOCK_FILE must be an absolute path" ;;
  esac
  lock_dir="${MAINTENANCE_LOCK_FILE%/*}"
  [ -d "$lock_dir" ] || die "maintenance lock directory does not exist: ${lock_dir}"

  # Append mode preserves any existing inode/content.  No container, Caddy,
  # state-file, or cooldown change happens until this non-blocking flock wins.
  exec 9>>"$MAINTENANCE_LOCK_FILE"
  if ! flock -n 9; then
    log "maintenance lock is held; exiting without runtime changes"
    return 1
  fi
  return 0
}

case "$DEPENDENCY_MODE" in
  local|external) ;;
  *) die "SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE must be local or external (got: ${DEPENDENCY_MODE})" ;;
esac

for command_name in docker curl flock grep sort awk sed mktemp mkdir mv rm sleep date; do
  require_cmd "$command_name"
done
require_positive_integer SUB2API_RUNTIME_GUARD_RETRY_ATTEMPTS "$RETRY_ATTEMPTS"
require_non_negative_integer SUB2API_RUNTIME_GUARD_RETRY_INTERVAL_SECONDS "$RETRY_INTERVAL_SECONDS"
require_non_negative_integer SUB2API_RUNTIME_GUARD_COOLDOWN_SECONDS "$COOLDOWN_SECONDS"
require_positive_integer SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_ATTEMPTS "$PUBLIC_HEALTH_ATTEMPTS"
require_non_negative_integer SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_INTERVAL_SECONDS "$PUBLIC_HEALTH_INTERVAL_SECONDS"
require_positive_integer SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_MAX_TIME_SECONDS "$PUBLIC_HEALTH_MAX_TIME_SECONDS"
validate_health_resolve "$PUBLIC_HEALTH_RESOLVE" "$PUBLIC_HEALTH_URL"
require_bool SUB2API_DUAL_NODE_RUNTIME_ENABLED "$DUAL_NODE_RUNTIME_ENABLED"
require_positive_integer SUB2API_RUNTIME_GUARD_APP_PORT "$APP_PORT"
[ "$APP_PORT" = "8080" ] || die "SUB2API_RUNTIME_GUARD_APP_PORT must remain 8080 for the Caddy upstream contract"
case "$CADDY_CONFIG_PATH" in
  /*) ;;
  *) die "SUB2API_RUNTIME_GUARD_CADDY_CONFIG_PATH must be an absolute path" ;;
esac
if [ "$DEPENDENCY_MODE" = external ] || [ "$DUAL_NODE_RUNTIME_ENABLED" = true ]; then
  require_cmd realpath
  require_cmd stat
fi
if [ "$DEPENDENCY_MODE" = external ]; then
  load_external_runtime_env
  validate_external_ca_file
fi

if ! acquire_maintenance_lock; then
  # A release, database cutover, or another guard owns the global maintenance
  # boundary.  Lock contention is expected and therefore a successful no-op.
  exit 0
fi

[ ! -e "$CADDY_TRANSACTION_PATH" ] && [ ! -L "$CADDY_TRANSACTION_PATH" ] \
  || die "unfinished GCP Taiwan Caddy listener transaction exists; runtime recovery is fenced until commit or rollback"
[ ! -e "$CADDY_CUSTOMER_HOST_TRANSACTION_PATH" ] && [ ! -L "$CADDY_CUSTOMER_HOST_TRANSACTION_PATH" ] \
  || die "unfinished customer Host Caddy transaction exists; runtime recovery is fenced until commit or rollback"

if ! ACTIVE_UPSTREAM="$(unique_upstream_from_file "$CADDYFILE")"; then
  die "Caddyfile must contain exactly one supported active upstream: ${CADDYFILE}"
fi
ACTIVE_CONTAINER="${ACTIVE_UPSTREAM%:8080}"
case "$ACTIVE_CONTAINER" in
  sub2api|sub2api-blue|sub2api-green) ;;
  *) die "unsupported active container parsed from Caddyfile: ${ACTIVE_CONTAINER}" ;;
esac

if container_exists "$ACTIVE_CONTAINER"; then
  verify_application_runtime_before_lifecycle "$ACTIVE_CONTAINER" \
    || die "active application runtime does not match the configured dependency and dual-node contract"
else
  log "Caddy-selected active container is absent; deferring runtime contract validation to recovery candidates: ${ACTIVE_CONTAINER}"
fi

case "$DEPENDENCY_MODE" in
  local)
    log "runtime dependency mode=local; verifying local PostgreSQL and Redis containers"
    ensure_dependency "$POSTGRES_CONTAINER" PostgreSQL
    ensure_dependency "$REDIS_CONTAINER" Redis
    ;;
  external)
    log "runtime dependency mode=external; skipping local PostgreSQL and Redis container inspection and lifecycle actions"
    ;;
esac
ensure_caddy
verify_caddy_matches "$ACTIVE_UPSTREAM" \
  || die "host Caddyfile and Caddy admin configuration are not consistent"
verify_caddy_startup_file "$ACTIVE_UPSTREAM" \
  || die "host Caddyfile and Caddy startup configuration are not consistent"

if container_is_healthy "$ACTIVE_CONTAINER"; then
  wait_for_public_health || die "public health endpoint is unavailable while the active container is healthy"
  reconcile_local_release_state \
    || die "could not reconcile node state for the healthy Caddy-selected container"
  clear_failure_state
  log "active container is already healthy: ${ACTIVE_CONTAINER}"
  exit 0
fi

if cooldown_is_active; then
  # A prior recovery attempt already proved this same selected slot bad and
  # isolated it. If an external actor restarted it but it is still unhealthy,
  # re-establish the fence without entering another restart loop.
  if container_exists "$ACTIVE_CONTAINER" && container_running "$ACTIVE_CONTAINER"; then
    isolate_active_container \
      || die "could not keep failed active container isolated during recovery cooldown"
  fi
  exit 1
fi

if try_restore_active; then
  verify_caddy_matches "$ACTIVE_UPSTREAM" \
    || die "Caddy changed while the active container was being recovered"
  verify_caddy_startup_file "$ACTIVE_UPSTREAM" \
    || die "Caddy startup file changed while the active container was being recovered"
  wait_for_public_health || die "public health endpoint is unavailable after active-container recovery"
  reconcile_local_release_state \
    || die "could not reconcile node state after active-container recovery"
  clear_failure_state
  log "active container recovered in place: ${ACTIVE_CONTAINER}"
  exit 0
fi

# A previous release may still have its old color draining.  Only after the
# selected active slot has exhausted same-slot recovery do we promote that one
# already-healthy old color.  More than one running inactive app is ambiguous
# and therefore fails closed.
running_inactive="$(running_inactive_apps)"
running_inactive_count="$(printf '%s\n' "$running_inactive" | awk 'NF { count += 1 } END { print count + 0 }')"
if [ "$running_inactive_count" -gt 0 ]; then
  [ "$running_inactive_count" -eq 1 ] \
    || die "multiple inactive application containers are running; refusing automatic recovery"
  FALLBACK_CONTAINER="$(printf '%s\n' "$running_inactive" | awk 'NF { print; exit }')"
  FALLBACK_IMAGE="$(container_image "$FALLBACK_CONTAINER")"
  [ -n "$FALLBACK_IMAGE" ] \
    || die "running inactive fallback has no image reference: ${FALLBACK_CONTAINER}"
  verify_application_runtime_before_lifecycle "$FALLBACK_CONTAINER" \
    || die "running inactive fallback does not match the configured dependency and dual-node contract: ${FALLBACK_CONTAINER}"
  container_is_healthy "$FALLBACK_CONTAINER" && app_internal_health "$FALLBACK_CONTAINER" \
    || die "running inactive fallback is not healthy: ${FALLBACK_CONTAINER}"
  isolate_active_container \
    || die "could not isolate failed active container before promoting ${FALLBACK_CONTAINER}"
  log "promoting already-running healthy historical fallback: ${FALLBACK_CONTAINER}"
  if ! switch_caddy_to_fallback; then
    fence_fallback_if_caddy_still_uses_failed_active
    write_failure_state 'running-fallback-caddy-switch-failed'
    die "could not promote running historical fallback"
  fi
  if ! verify_fallback_switch; then
    fence_fallback_if_caddy_still_uses_failed_active
    write_failure_state 'running-fallback-verification-failed'
    die "running historical fallback failed post-switch verification"
  fi
  reconcile_local_release_state \
    || die "could not reconcile node state after running fallback promotion"
  clear_failure_state
  log "runtime fallback verified: active=${FALLBACK_CONTAINER} image=${FALLBACK_IMAGE}"
  exit 0
fi

if ! isolate_active_container; then
  write_failure_state 'could-not-isolate-active'
  die "could not isolate failed active container; fallback was not started"
fi

if ! select_known_good_fallback; then
  write_failure_state 'no-known-good-fallback'
  die "no stopped, non-OOM, zero-exit historical fallback is available"
fi

if ! start_fallback; then
  write_failure_state 'fallback-did-not-become-healthy'
  die "historical fallback did not become healthy"
fi

if ! switch_caddy_to_fallback; then
  fence_fallback_if_caddy_still_uses_failed_active
  write_failure_state 'caddy-switch-failed'
  die "blue-green Caddy synchronization failed; failed active remains stopped"
fi

if ! verify_fallback_switch; then
  fence_fallback_if_caddy_still_uses_failed_active
  write_failure_state 'fallback-verification-failed'
  die "fallback switch verification failed; failed active remains stopped"
fi

reconcile_local_release_state \
  || die "could not reconcile node state after fallback promotion"

clear_failure_state
log "runtime fallback verified: active=${FALLBACK_CONTAINER} image=${FALLBACK_IMAGE}"
