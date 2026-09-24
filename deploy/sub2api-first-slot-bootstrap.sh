#!/usr/bin/env bash

# Bootstrap the first Sub2API blue/green application slot on an otherwise empty
# host. This path deliberately does not inspect, mutate, or reload Caddy. It
# creates the runtime fence files before the process starts, keeps request
# admission draining, and keeps background work in standby. Normal releases
# must use sub2api-server-release.sh after this first slot is verified.

set -Eeuo pipefail

APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
CONTAINER="${SUB2API_FIRST_SLOT_CONTAINER:-sub2api-blue}"
IMAGE="${SUB2API_FIRST_SLOT_IMAGE:-}"
EXPECTED_REVISION="${SUB2API_FIRST_SLOT_EXPECTED_REVISION:-}"
NETWORK="${SUB2API_RUNTIME_GUARD_NETWORK:-sub2api-candidate-network}"
DATA_VOLUME="${SUB2API_RUNTIME_GUARD_DATA_VOLUME:-sub2api-candidate-data}"
APP_ENV_FILE="${SUB2API_FIRST_SLOT_APP_ENV_FILE:-/etc/sub2api-candidate-app.env}"
EXTERNAL_ENV_FILE="${SUB2API_EXTERNAL_RUNTIME_ENV_FILE:-/etc/sub2api-external-runtime.env}"
EXTERNAL_CA_FILE="${SUB2API_EXTERNAL_CA_FILE:-${APP_DIR}/db-host-ca/ca.crt}"
TRAFFIC_STATE_FILE="${SUB2API_TRAFFIC_STATE_FILE_HOST:-/var/lib/sub2api/runtime/traffic-state}"
BACKGROUND_STATE_DIR="${SUB2API_BACKGROUND_STATE_DIR:-/var/lib/sub2api/runtime/background}"
BACKGROUND_STATE_FILE="${BACKGROUND_STATE_DIR}/${CONTAINER}"
HEALTH_TOKEN_FILE="${SUB2API_INTERNAL_HEALTH_TOKEN_FILE_HOST:-${APP_DIR}/secrets/internal-health-token}"
PAYMENT_CONTAINER=sub2api-payment-vault
PAYMENT_VOLUME=sub2api_unified_payment_vault
PAYMENT_PATH=/run/sub2api-payment-vault
FEISHU_CONTAINER=sub2api-feishu-vault
FEISHU_VOLUME=sub2api_feishu_vault
FEISHU_PATH=/run/sub2api-feishu-vault
CONTAINER_PG_CA_PATH=/etc/sub2api-db-ca/ca.crt
CONTAINER_REDIS_CA_PATH=/etc/ssl/certs/sub2api-db-ca.pem
CONTAINER_TRAFFIC_STATE_PATH=/run/sub2api-runtime/traffic-state
CONTAINER_BACKGROUND_STATE_PATH=/run/sub2api-runtime/background-state
CONTAINER_HEALTH_TOKEN_PATH=/run/sub2api-runtime/health-token
HEALTH_ATTEMPTS="${SUB2API_FIRST_SLOT_HEALTH_ATTEMPTS:-60}"
HEALTH_INTERVAL_SECONDS="${SUB2API_FIRST_SLOT_HEALTH_INTERVAL_SECONDS:-3}"
ACTION="${1:-bootstrap}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAINTENANCE_LOCK_HELPER="${SCRIPT_DIR}/sub2api-maintenance-lock.sh"
LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-/run/sub2api-maintenance/sub2api-maintenance.lock}"
TEMP_FILES=()
CREATED_CONTAINER=false

log() { printf '[sub2api-first-slot] %s\n' "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

cleanup() {
  local file
  for file in "${TEMP_FILES[@]+${TEMP_FILES[@]}}"; do
    [ ! -e "$file" ] || rm -f -- "$file"
  done
}

failure_fence() {
  local status=$?
  trap - ERR
  if [ "$ACTION" = bootstrap ] && [ "$CREATED_CONTAINER" = true ]; then
    docker update --restart no "$CONTAINER" >/dev/null 2>&1 || true
    docker stop -t 10 "$CONTAINER" >/dev/null 2>&1 || true
    log "bootstrap failed; $CONTAINER was fenced and retained for review"
  fi
  exit "$status"
}

trap cleanup EXIT
trap failure_fence ERR

new_temp_file() {
  local file
  file="$(mktemp)"
  chmod 600 "$file"
  TEMP_FILES+=("$file")
  printf '%s\n' "$file"
}

require_regular_file() {
  local label="$1" path="$2" expected_owner="$3" expected_mode="$4" canonical
  [ -f "$path" ] && [ ! -L "$path" ] || die "$label must be a regular non-symlink file"
  canonical="$(realpath -e -- "$path")" || die "$label must resolve canonically"
  [ "$canonical" = "$path" ] || die "$label must use a canonical path"
  [ "$(stat -c '%u:%g' "$path")" = "$expected_owner" ] || die "$label has an unexpected owner"
  [ "$(stat -c '%a' "$path")" = "$expected_mode" ] || die "$label has an unexpected mode"
}

validate_env_file() {
  local label="$1" path="$2" allowed="${3:-}" allow_empty="${4:-false}"
  awk -v allowed="$allowed" -v allow_empty="$allow_empty" '
    BEGIN {
      count=split(allowed,items,",")
      for (i=1; i<=count; i++) if (items[i] != "") approved[items[i]]=1
    }
    /^$/ || /^#/ { next }
    {
      separator=index($0,"=")
      if (separator < 2) exit 1
      key=substr($0,1,separator-1)
      value=substr($0,separator+1)
      if (key !~ /^[A-Z][A-Z0-9_]*$/ || seen[key]++) exit 1
      if (value == "" && allow_empty != "true") exit 1
      if (allowed != "" && !(key in approved)) exit 1
    }
  ' "$path" || die "$label environment file is invalid"
}

env_value() {
  local path="$1" key="$2"
  awk -v wanted="$key" 'index($0,wanted "=")==1 { count += 1; value=substr($0,length(wanted)+2) } END { if (count != 1) exit 1; print value }' "$path"
}

write_state() {
  local path="$1" value="$2" parent temporary
  parent="$(dirname "$path")"
  install -d -o root -g root -m 0755 "$parent"
  [ ! -L "$path" ] || die "runtime state path is a symlink"
  temporary="$(mktemp "${parent}/.state.XXXXXX")"
  printf '%s\n' "$value" >"$temporary"
  chown root:root "$temporary"
  chmod 0644 "$temporary"
  mv -fT "$temporary" "$path"
}

require_bool() {
  case "$2" in true|false) ;; *) die "$1 must be true or false" ;; esac
}

container_exists() { docker container inspect "$1" >/dev/null 2>&1; }

require_agent() {
  local enabled="$1" container="$2" volume="$3"
  [ "$enabled" = true ] || return 0
  docker volume inspect "$volume" >/dev/null 2>&1 || die "required Vault Agent volume is missing"
  container_exists "$container" || die "required Vault Agent container is missing"
  [ "$(docker inspect "$container" --format '{{.State.Running}}')" = true ] || die "required Vault Agent is not running"
  [ "$(docker inspect "$container" --format '{{.State.Health.Status}}')" = healthy ] || die "required Vault Agent is not healthy"
}

prepare_runtime_env() {
  local output key value
  output="$(new_temp_file)"
  awk '
    BEGIN {
      split("DATABASE_HOST DATABASE_PORT DATABASE_USER DATABASE_PASSWORD DATABASE_DBNAME DATABASE_SSLMODE REDIS_HOST REDIS_PORT REDIS_USERNAME REDIS_PASSWORD REDIS_DB REDIS_ENABLE_TLS PGSSLROOTCERT SUB2API_TRAFFIC_STATE_FILE SUB2API_BACKGROUND_STATE_FILE SUB2API_INTERNAL_HEALTH_TOKEN_FILE SUB2API_IMAGE UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_BASE64 SUB2API_FEISHU_WEBHOOK_URL",items," ")
      for (i in items) drop[items[i]]=1
    }
    /^$/ || /^#/ { next }
    { key=$0; sub(/=.*/,"",key); if (!(key in drop)) print }
  ' "$APP_ENV_FILE" >"$output"
  for key in DATABASE_HOST DATABASE_PORT DATABASE_USER DATABASE_PASSWORD DATABASE_DBNAME DATABASE_SSLMODE REDIS_HOST REDIS_PORT REDIS_USERNAME REDIS_PASSWORD REDIS_DB REDIS_ENABLE_TLS; do
    value="$(env_value "$EXTERNAL_ENV_FILE" "$key")" || die "external runtime is missing $key"
    printf '%s=%s\n' "$key" "$value" >>"$output"
  done
  {
    printf 'PGSSLROOTCERT=%s\n' "$CONTAINER_PG_CA_PATH"
    printf 'SUB2API_TRAFFIC_STATE_FILE=%s\n' "$CONTAINER_TRAFFIC_STATE_PATH"
    printf 'SUB2API_BACKGROUND_STATE_FILE=%s\n' "$CONTAINER_BACKGROUND_STATE_PATH"
    printf 'SUB2API_INTERNAL_HEALTH_TOKEN_FILE=%s\n' "$CONTAINER_HEALTH_TOKEN_PATH"
    printf 'SUB2API_IMAGE=%s\n' "$IMAGE"
  } >>"$output"
  validate_env_file runtime "$output" '' true
  RUNTIME_ENV_FILE="$output"
}

verify_image() {
  local revision source platform version
  case "$IMAGE" in sub2api:*) ;; *) die "first-slot image must be a local sub2api tag" ;; esac
  platform="$(docker image inspect "$IMAGE" --format '{{.Os}}/{{.Architecture}}')" || die "first-slot image is missing"
  [ "$platform" = linux/amd64 ] || die "first-slot image must be linux/amd64"
  source="$(docker image inspect "$IMAGE" --format '{{index .Config.Labels "org.opencontainers.image.source"}}')"
  [ "$source" = https://github.com/Turtle-Li/sub2api ] || die "first-slot image source is not approved"
  revision="$(docker image inspect "$IMAGE" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')"
  [ "$revision" = "$EXPECTED_REVISION" ] || die "first-slot image revision mismatch"
  version="$(docker image inspect "$IMAGE" --format '{{index .Config.Labels "org.opencontainers.image.version"}}')"
  [ -n "$version" ] || die "first-slot image version is missing"
}

verify_slot() {
  container_exists "$CONTAINER" || die "first-slot container is missing"
  [ "$(docker inspect "$CONTAINER" --format '{{.Config.Image}}')" = "$IMAGE" ] || die "first-slot container image mismatch"
  [ "$(docker inspect "$CONTAINER" --format '{{.State.Running}}')" = true ] || die "first-slot container is not running"
  [ "$(docker inspect "$CONTAINER" --format '{{.State.Health.Status}}')" = healthy ] || die "first-slot container is not healthy"
  [ "$(cat "$TRAFFIC_STATE_FILE")" = draining ] || die "first-slot traffic fence is not draining"
  [ "$(cat "$BACKGROUND_STATE_FILE")" = standby ] || die "first-slot background fence is not standby"
  docker exec "$CONTAINER" sh -c 'wget -Y off -qO- http://127.0.0.1:8080/health >/dev/null || curl --noproxy "*" -fsS http://127.0.0.1:8080/health >/dev/null' \
    || die "first-slot loopback health check failed"
}

case "$ACTION" in bootstrap|verify) ;; *) die "usage: $0 [bootstrap|verify]" ;; esac
case "$CONTAINER" in sub2api-blue|sub2api-green) ;; *) die "first-slot container must be sub2api-blue or sub2api-green" ;; esac
[ -n "$IMAGE" ] || die "SUB2API_FIRST_SLOT_IMAGE is required"
case "$EXPECTED_REVISION" in ""|*[!0-9a-f]*) die "SUB2API_FIRST_SLOT_EXPECTED_REVISION must be a full hexadecimal commit" ;; esac
[ "${#EXPECTED_REVISION}" -eq 40 ] || die "SUB2API_FIRST_SLOT_EXPECTED_REVISION must be a full hexadecimal commit"
case "$HEALTH_ATTEMPTS:$HEALTH_INTERVAL_SECONDS" in *[!0-9:]*) die "health timing must be numeric" ;; esac
[ "$HEALTH_ATTEMPTS" -gt 0 ] && [ "$HEALTH_INTERVAL_SECONDS" -gt 0 ] || die "health timing must be positive"
case "${SUB2API_FIRST_SLOT_ALLOW_NON_ROOT_FOR_TESTS:-0}" in 1) ;; 0) [ "$(id -u)" -eq 0 ] || die "first-slot bootstrap must run as root" ;; *) die "invalid test override" ;; esac

[ -r "$MAINTENANCE_LOCK_HELPER" ] && [ ! -L "$MAINTENANCE_LOCK_HELPER" ] || die "maintenance lock helper is missing"
# shellcheck disable=SC1090,SC1091
. "$MAINTENANCE_LOCK_HELPER"
if [ "${SUB2API_FIRST_SLOT_ALLOW_NON_ROOT_FOR_TESTS:-0}" = 1 ]; then
  # shellcheck disable=SC2034 # Read by the sourced maintenance-lock helper.
  SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1
fi
sub2api_maintenance_lock_validate_configured_path "$LOCK_FILE" || die "maintenance lock path is invalid"
sub2api_maintenance_lock_open "$LOCK_FILE" || die "maintenance lock could not be opened"
flock -n "$SUB2API_MAINTENANCE_LOCK_FD" || die "maintenance lock is busy"

require_regular_file app-runtime "$APP_ENV_FILE" 0:0 600
require_regular_file external-runtime "$EXTERNAL_ENV_FILE" 0:0 600
require_regular_file database-ca "$EXTERNAL_CA_FILE" 0:0 644
require_regular_file health-token "$HEALTH_TOKEN_FILE" 1000:1000 600
validate_env_file app-runtime "$APP_ENV_FILE" '' true
validate_env_file external-runtime "$EXTERNAL_ENV_FILE" 'DATABASE_HOST,DATABASE_PORT,DATABASE_USER,DATABASE_PASSWORD,DATABASE_DBNAME,DATABASE_SSLMODE,REDIS_HOST,REDIS_PORT,REDIS_USERNAME,REDIS_PASSWORD,REDIS_DB,REDIS_ENABLE_TLS'
[ "$(env_value "$EXTERNAL_ENV_FILE" DATABASE_SSLMODE)" = verify-full ] || die "external PostgreSQL must use verify-full"
[ "$(env_value "$EXTERNAL_ENV_FILE" REDIS_ENABLE_TLS)" = true ] || die "external Redis TLS must be enabled"
PAYMENT_ENABLED="$(env_value "$APP_ENV_FILE" UNIFIED_PAYMENT_ENABLED)" || die "app runtime is missing payment state"
FEISHU_ENABLED="$(env_value "$APP_ENV_FILE" SUB2API_FEISHU_ENABLED)" || die "app runtime is missing Feishu state"
require_bool UNIFIED_PAYMENT_ENABLED "$PAYMENT_ENABLED"
require_bool SUB2API_FEISHU_ENABLED "$FEISHU_ENABLED"
verify_image

if [ "$ACTION" = verify ]; then
  verify_slot
  printf '%s\n' 'SUB2API_FIRST_SLOT_VERIFIED'
  exit 0
fi

for candidate in sub2api sub2api-blue sub2api-green; do
  ! container_exists "$candidate" || die "application slot already exists: $candidate"
done

docker network inspect "$NETWORK" >/dev/null 2>&1 || docker network create --driver bridge "$NETWORK" >/dev/null
docker volume inspect "$DATA_VOLUME" >/dev/null 2>&1 || docker volume create "$DATA_VOLUME" >/dev/null
require_agent "$PAYMENT_ENABLED" "$PAYMENT_CONTAINER" "$PAYMENT_VOLUME"
require_agent "$FEISHU_ENABLED" "$FEISHU_CONTAINER" "$FEISHU_VOLUME"
write_state "$TRAFFIC_STATE_FILE" draining
write_state "$BACKGROUND_STATE_FILE" standby
prepare_runtime_env

mounts=(
  --mount "type=volume,source=$DATA_VOLUME,target=/app/data"
  --mount "type=bind,source=$EXTERNAL_CA_FILE,target=$CONTAINER_PG_CA_PATH,readonly"
  --mount "type=bind,source=$EXTERNAL_CA_FILE,target=$CONTAINER_REDIS_CA_PATH,readonly"
  --mount "type=bind,source=$TRAFFIC_STATE_FILE,target=$CONTAINER_TRAFFIC_STATE_PATH,readonly"
  --mount "type=bind,source=$BACKGROUND_STATE_FILE,target=$CONTAINER_BACKGROUND_STATE_PATH,readonly"
  --mount "type=bind,source=$HEALTH_TOKEN_FILE,target=$CONTAINER_HEALTH_TOKEN_PATH,readonly"
)
[ "$PAYMENT_ENABLED" != true ] || mounts+=(--mount "type=volume,source=$PAYMENT_VOLUME,target=$PAYMENT_PATH,readonly")
[ "$FEISHU_ENABLED" != true ] || mounts+=(--mount "type=volume,source=$FEISHU_VOLUME,target=$FEISHU_PATH,readonly")

log "creating fenced first slot $CONTAINER from $IMAGE"
docker create --name "$CONTAINER" --network "$NETWORK" --env-file "$RUNTIME_ENV_FILE" "${mounts[@]}" --restart no "$IMAGE" >/dev/null
CREATED_CONTAINER=true
docker update --restart unless-stopped "$CONTAINER" >/dev/null
docker start "$CONTAINER" >/dev/null

for _ in $(seq 1 "$HEALTH_ATTEMPTS"); do
  [ "$(docker inspect "$CONTAINER" --format '{{.State.Health.Status}}')" = healthy ] && break
  sleep "$HEALTH_INTERVAL_SECONDS"
done
verify_slot
CREATED_CONTAINER=false
printf '%s\n' 'SUB2API_FIRST_SLOT_BOOTSTRAPPED traffic=draining background=standby'
