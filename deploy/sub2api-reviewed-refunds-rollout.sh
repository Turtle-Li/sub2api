#!/usr/bin/env bash

# Change the reviewed-refunds rollout flag only on the Caddy-selected local
# generation.  The application owns the PostgreSQL advisory lock and compare-
# and-swap; this helper owns the matching host maintenance boundary, runtime
# admission transition, and immutable deployment checks.

set -Eeuo pipefail
umask 077

usage() {
  cat >&2 <<'EOF'
Usage: sub2api-reviewed-refunds-rollout.sh EXPECTED_COMMIT TARGET_CONTAINER EXPECTED_FLAG_STATE

EXPECTED_COMMIT      Full 40-character lowercase Git commit.
TARGET_CONTAINER     One of sub2api, sub2api-blue, or sub2api-green.
EXPECTED_FLAG_STATE  absent, false, or true.

The desired transition is absent|false -> true and true -> false.
EOF
  exit 2
}

log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

die() {
  log "ERROR: $*" >&2
  exit 1
}

[ "$#" -eq 3 ] || usage
EXPECTED_COMMIT="$1"
TARGET_CONTAINER="$2"
EXPECTED_FLAG_STATE="$3"

case "$EXPECTED_COMMIT" in
  *[!0-9a-f]*|'') die 'EXPECTED_COMMIT must be a full 40-character lowercase Git commit' ;;
esac
[ "${#EXPECTED_COMMIT}" -eq 40 ] \
  || die 'EXPECTED_COMMIT must be a full 40-character lowercase Git commit'
case "$TARGET_CONTAINER" in
  sub2api|sub2api-blue|sub2api-green) ;;
  *) die 'TARGET_CONTAINER must be sub2api, sub2api-blue, or sub2api-green' ;;
esac
case "$EXPECTED_FLAG_STATE" in
  absent)
    API_EXPECTED=''
    TARGET_ENABLED=true
    ;;
  false)
    API_EXPECTED=false
    TARGET_ENABLED=true
    ;;
  true)
    API_EXPECTED=true
    TARGET_ENABLED=false
    ;;
  *) die 'EXPECTED_FLAG_STATE must be absent, false, or true' ;;
esac

TEST_MODE="${SUB2API_REVIEWED_REFUNDS_ROLLOUT_ALLOW_NON_ROOT_FOR_TESTS:-0}"
case "$TEST_MODE" in
  0|1) ;;
  *) die 'SUB2API_REVIEWED_REFUNDS_ROLLOUT_ALLOW_NON_ROOT_FOR_TESTS must be 0 or 1' ;;
esac
if [ "$TEST_MODE" != 1 ]; then
  [ "$(id -u)" -eq 0 ] || die 'reviewed-refunds rollout requires root'
fi

CONFIG_FILE="${SUB2API_REVIEWED_REFUNDS_ROLLOUT_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"

file_metadata() {
  stat -c '%u:%g:%a' "$1" 2>/dev/null || stat -f '%u:%g:%Lp' "$1"
}

validate_protected_config() {
  local metadata uid gid mode

  [ -f "$CONFIG_FILE" ] && [ ! -L "$CONFIG_FILE" ] \
    || die "automatic-release configuration is missing or unsafe: ${CONFIG_FILE}"
  metadata="$(file_metadata "$CONFIG_FILE")" \
    || die "could not inspect automatic-release configuration: ${CONFIG_FILE}"
  IFS=: read -r uid gid mode <<EOF
$metadata
EOF
  [ "$mode" = 600 ] \
    || die "automatic-release configuration must be mode 0600: ${CONFIG_FILE}"
  if [ "$TEST_MODE" != 1 ]; then
    [ "$uid" = 0 ] && [ "$gid" = 0 ] \
      || die "automatic-release configuration must be root-owned: ${CONFIG_FILE}"
  fi
}

validate_protected_config
set -a
# shellcheck disable=SC1090 # The checked root-owned config is the deployment authority.
. "$CONFIG_FILE"
set +a

APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
CADDY_CONTAINER="${SUB2API_CADDY_CONTAINER:-sub2api-caddy}"
CADDYFILE="${SUB2API_REVIEWED_REFUNDS_ROLLOUT_CADDYFILE:-${APP_DIR}/Caddyfile}"
CADDY_CONFIG_PATH="${SUB2API_REVIEWED_REFUNDS_ROLLOUT_CADDY_CONFIG_PATH:-${SUB2API_RUNTIME_GUARD_CADDY_CONFIG_PATH:-/etc/caddy/Caddyfile}}"
NODE_STATE_SCRIPT="${SUB2API_NODE_STATE_SCRIPT:-${APP_DIR}/scripts/sub2api-node-state.sh}"
NODE_STATE_DIR="${SUB2API_NODE_STATE_DIR:-/var/lib/sub2api/runtime}"
LOCAL_TRANSACTION_PATH="${SUB2API_LOCAL_RELEASE_STATE_FILE_HOST:-${NODE_STATE_DIR}/local-release.env}"
GCP_TRANSACTION_PATH="${SUB2API_REVIEWED_REFUNDS_ROLLOUT_GCP_TRANSACTION_PATH:-${APP_DIR}/.gcp-tw-caddy-transaction.env}"
CUSTOMER_TRANSACTION_PATH="${SUB2API_REVIEWED_REFUNDS_ROLLOUT_CUSTOMER_TRANSACTION_PATH:-${APP_DIR}/.cf-opt-totools-caddy.env}"
CADDY_SWITCH_TRANSACTION_PATH="${SUB2API_REVIEWED_REFUNDS_ROLLOUT_CADDY_SWITCH_TRANSACTION_PATH:-${APP_DIR}/.sub2api-blue-green-caddy-transaction.env}"
EXPECTED_SOURCE="${SUB2API_GITHUB_IMAGE_SOURCE:-https://github.com/Turtle-Li/sub2api}"
DRAIN_ATTEMPTS="${SUB2API_REVIEWED_REFUNDS_ROLLOUT_DRAIN_ATTEMPTS:-20}"
DRAIN_INTERVAL_SECONDS="${SUB2API_REVIEWED_REFUNDS_ROLLOUT_DRAIN_INTERVAL_SECONDS:-3}"
CONTAINER_HEALTH_TOKEN_PATH='/run/sub2api-runtime/health-token'

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

require_non_negative_integer() {
  case "$2" in
    ''|*[!0-9]*) die "$1 must be a non-negative integer" ;;
  esac
}

require_positive_integer() {
  require_non_negative_integer "$1" "$2"
  [ "$2" -gt 0 ] || die "$1 must be a positive integer"
}

require_simple_container_name() {
  case "$2" in
    ''|.*|*..*|*.|-*|*[!A-Za-z0-9_.-]*) die "$1 is not a safe Docker container name" ;;
  esac
}

require_absolute_path() {
  case "$2" in
    /*) ;;
    *) die "$1 must be an absolute path" ;;
  esac
  case "$2" in
    *$'\n'*|*$'\r'*|*'//'*) die "$1 contains unsupported path components" ;;
  esac
}

validate_root_owned_executable() {
  local label="$1" path="$2" metadata uid gid mode

  [ -f "$path" ] && [ ! -L "$path" ] && [ -x "$path" ] \
    || die "${label} is missing or unsafe: ${path}"
  if [ "$TEST_MODE" = 1 ]; then
    return
  fi
  metadata="$(file_metadata "$path")" || die "could not inspect ${label}: ${path}"
  IFS=: read -r uid gid mode <<EOF
$metadata
EOF
  [ "$uid" = 0 ] && [ "$gid" = 0 ] && [ "$mode" = 750 ] \
    || die "${label} must be root-owned mode 0750: ${path}"
}

for command_name in cat date docker flock id python3 sleep stat; do
  require_cmd "$command_name"
done
require_positive_integer SUB2API_REVIEWED_REFUNDS_ROLLOUT_DRAIN_ATTEMPTS "$DRAIN_ATTEMPTS"
require_non_negative_integer SUB2API_REVIEWED_REFUNDS_ROLLOUT_DRAIN_INTERVAL_SECONDS "$DRAIN_INTERVAL_SECONDS"
require_simple_container_name SUB2API_CADDY_CONTAINER "$CADDY_CONTAINER"
require_absolute_path SUB2API_APP_DIR "$APP_DIR"
require_absolute_path SUB2API_REVIEWED_REFUNDS_ROLLOUT_CADDYFILE "$CADDYFILE"
require_absolute_path SUB2API_REVIEWED_REFUNDS_ROLLOUT_CADDY_CONFIG_PATH "$CADDY_CONFIG_PATH"
require_absolute_path SUB2API_LOCAL_RELEASE_STATE_FILE_HOST "$LOCAL_TRANSACTION_PATH"
case "$EXPECTED_SOURCE" in
  https://github.com/*) ;;
  *) die 'SUB2API_GITHUB_IMAGE_SOURCE must be a canonical https://github.com source URL' ;;
esac
validate_root_owned_executable 'node-state helper' "$NODE_STATE_SCRIPT"

ROLLOUT_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAINTENANCE_LOCK_HELPER="${ROLLOUT_SCRIPT_DIR}/sub2api-maintenance-lock.sh"
[ -r "$MAINTENANCE_LOCK_HELPER" ] && [ ! -L "$MAINTENANCE_LOCK_HELPER" ] \
  || die "maintenance lock helper is missing or unsafe: ${MAINTENANCE_LOCK_HELPER}"
# shellcheck disable=SC1090,SC1091 # Installed beside this root-owned executable.
. "$MAINTENANCE_LOCK_HELPER"
if [ "$TEST_MODE" = 1 ]; then
  # shellcheck disable=SC2034 # Read by the sourced maintenance-lock helper.
  SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1
fi
MAINTENANCE_LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE}"
if ! sub2api_maintenance_lock_validate_configured_path "$MAINTENANCE_LOCK_FILE"; then
  die "unsafe maintenance lock: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
fi

require_no_pending_transactions() {
  local transaction_path

  for transaction_path in \
    "$LOCAL_TRANSACTION_PATH" \
    "$GCP_TRANSACTION_PATH" \
    "$CUSTOMER_TRANSACTION_PATH" \
    "$CADDY_SWITCH_TRANSACTION_PATH"; do
    [ ! -e "$transaction_path" ] && [ ! -L "$transaction_path" ] \
      || die "unfinished release transaction exists: ${transaction_path}"
  done
}

container_running_state() {
  docker inspect "$1" --format '{{.State.Running}}' 2>/dev/null || true
}

container_health_state() {
  docker inspect "$1" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' 2>/dev/null || true
}

validate_target_image_and_health() {
  local running health image_id image_hex inspected_image_id source revision

  running="$(container_running_state "$TARGET_CONTAINER")"
  [ "$running" = true ] || die "target container is not running: ${TARGET_CONTAINER}"
  health="$(container_health_state "$TARGET_CONTAINER")"
  [ "$health" = healthy ] || die "target container is not healthy: ${TARGET_CONTAINER} (${health:-missing})"

  image_id="$(docker inspect "$TARGET_CONTAINER" --format '{{.Image}}' 2>/dev/null || true)"
  case "$image_id" in sha256:*) image_hex="${image_id#sha256:}" ;; *) die "target container has no immutable image ID: ${TARGET_CONTAINER}" ;; esac
  case "$image_hex" in *[!0-9a-f]*|'') die "target container image ID is invalid: ${TARGET_CONTAINER}" ;; esac
  [ "${#image_hex}" -eq 64 ] || die "target container image ID is invalid: ${TARGET_CONTAINER}"

  inspected_image_id="$(docker image inspect "$image_id" --format '{{.Id}}' 2>/dev/null || true)"
  [ "$inspected_image_id" = "$image_id" ] \
    || die "target container image ID no longer resolves immutably: ${TARGET_CONTAINER}"
  source="$(docker image inspect "$image_id" --format '{{index .Config.Labels "org.opencontainers.image.source"}}' 2>/dev/null || true)"
  revision="$(docker image inspect "$image_id" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' 2>/dev/null || true)"
  [ "$source" = "$EXPECTED_SOURCE" ] \
    || die "target image source label does not match ${EXPECTED_SOURCE}"
  [ "$revision" = "$EXPECTED_COMMIT" ] \
    || die "target image revision label does not match ${EXPECTED_COMMIT}"
}

caddy_json_points_uniquely_to_target() {
  local config_json="$1"

  printf '%s' "$config_json" | python3 -c '
import json
import sys

expected = sys.argv[1]
config = json.load(sys.stdin)
if type(config) is not dict:
    raise SystemExit(1)

dials = []
def walk(value):
    if type(value) is dict:
        for key, child in value.items():
            if key == "dial":
                if type(child) is not str:
                    raise SystemExit(1)
                dials.append(child)
            walk(child)
    elif type(value) is list:
        for child in value:
            walk(child)

walk(config)
if not dials or set(dials) != {expected}:
    raise SystemExit(1)
' "${TARGET_CONTAINER}:8080"
}

verify_caddy_views() {
  local caddy_running host_config startup_config admin_config config_json

  caddy_running="$(container_running_state "$CADDY_CONTAINER")"
  [ "$caddy_running" = true ] || die "Caddy container is not running: ${CADDY_CONTAINER}"
  [ -f "$CADDYFILE" ] && [ ! -L "$CADDYFILE" ] && [ -r "$CADDYFILE" ] \
    || die "host Caddyfile is missing or unsafe: ${CADDYFILE}"
  host_config="$(docker exec -i "$CADDY_CONTAINER" \
    caddy adapt --config /dev/stdin --adapter caddyfile <"$CADDYFILE")" \
    || die "could not adapt host Caddyfile"
  startup_config="$(docker exec \
    -e "SUB2API_REVIEWED_REFUNDS_ROLLOUT_CADDY_PATH=${CADDY_CONFIG_PATH}" \
    "$CADDY_CONTAINER" \
    sh -ceu 'caddy adapt --config "$SUB2API_REVIEWED_REFUNDS_ROLLOUT_CADDY_PATH" --adapter caddyfile')" \
    || die "could not adapt Caddy startup configuration"
  admin_config="$(docker exec "$CADDY_CONTAINER" sh -ceu \
    'wget -Y off -qO- http://127.0.0.1:2019/config/ 2>/dev/null || curl --noproxy "*" -fsS http://127.0.0.1:2019/config/')" \
    || die "could not read Caddy Admin configuration"

  for config_json in "$host_config" "$startup_config" "$admin_config"; do
    caddy_json_points_uniquely_to_target "$config_json" \
      || die "Caddy host/startup/Admin views do not uniquely select ${TARGET_CONTAINER}:8080"
  done
}

verify_no_other_app_writers() {
  local candidate state container_id container_name source matching_containers

  for candidate in sub2api sub2api-blue sub2api-green; do
    [ "$candidate" = "$TARGET_CONTAINER" ] && continue
    state="$(container_running_state "$candidate")"
    case "$state" in
      ''|false) ;;
      true) die "another canonical app writer is running: ${candidate}" ;;
      *) die "could not determine canonical app writer state: ${candidate}" ;;
    esac
  done

  # Container names can be retained after a release (for example,
  # sub2api-pre-cny-legacy-*). Docker filters this read-only query by the
  # image-source label, so unrelated containers are never inspected or
  # touched.
  matching_containers="$(docker ps -aq --filter "label=org.opencontainers.image.source=${EXPECTED_SOURCE}")" \
    || die 'could not list source-labeled Sub2API container candidates'
  while IFS= read -r container_id; do
    [ -n "$container_id" ] || continue
    container_name="$(docker inspect "$container_id" --format '{{.Name}}' 2>/dev/null || true)"
    state="$(docker inspect "$container_id" --format '{{.State.Running}}' 2>/dev/null || true)"
    source="$(docker inspect "$container_id" --format '{{with index .Config.Labels "org.opencontainers.image.source"}}{{.}}{{end}}' 2>/dev/null || true)"
    [ -n "$container_name" ] && [ -n "$state" ] \
      || die "could not inspect a running-container candidate"
    if [ "$source" = "$EXPECTED_SOURCE" ] \
      && [ "$container_name" != "/${TARGET_CONTAINER}" ] \
      && [ "$state" = true ]; then
      die "another Sub2API writer is running: ${container_name#/}"
    fi
  done <<<"$matching_containers"
}

verify_topology() {
  require_no_pending_transactions
  validate_target_image_and_health
  verify_caddy_views
  verify_no_other_app_writers
}

read_node_state() {
  local output traffic active background extra

  output="$(env SUB2API_NODE_STATE_LOCK_HELD=1 "$NODE_STATE_SCRIPT" status)" \
    || die 'could not read local runtime node state'
  case "$output" in *$'\n'*) die 'node-state helper returned multiple records' ;; esac
  IFS=' ' read -r traffic active background extra <<<"$output"
  [ -z "${extra:-}" ] \
    && [ "$traffic" = "traffic=${1}" ] \
    && [ "$active" = "active_container=${TARGET_CONTAINER}" ] \
    && [ "$background" = "background=${2}" ] \
    || die "local runtime state is not ${1}/${2} for ${TARGET_CONTAINER}"
}

internal_get() {
  local path="$1"

  case "$path" in
    /internal/livez|/internal/readyz|/internal/refund-rollback-readiness) ;;
    *) die "unsupported internal probe path: ${path}" ;;
  esac
  docker exec \
    -e "SUB2API_REVIEWED_REFUNDS_ROLLOUT_PATH=${path}" \
    -e "SUB2API_REVIEWED_REFUNDS_ROLLOUT_TOKEN_PATH=${CONTAINER_HEALTH_TOKEN_PATH}" \
    "$TARGET_CONTAINER" \
    sh -ceu '
      token="$(cat "$SUB2API_REVIEWED_REFUNDS_ROLLOUT_TOKEN_PATH")"
      [ -n "$token" ]
      wget -Y off -q -T 10 -O - \
        --header="X-Monitor-Token: ${token}" \
        "http://127.0.0.1:8080${SUB2API_REVIEWED_REFUNDS_ROLLOUT_PATH}"
    '
}

livez_in_flight() {
  local response in_flight

  response="$(internal_get /internal/livez)" \
    || { log 'internal livez probe is unreachable or rejected' >&2; return 1; }
  if ! in_flight="$(printf '%s' "$response" | python3 -c '
import json
import sys

value = json.load(sys.stdin)
if type(value) is not dict or value.get("live") is not True:
    raise SystemExit(1)
count = value.get("in_flight_requests")
if type(count) is not int or count < 0:
    raise SystemExit(1)
print(count)
')"; then
    log 'internal livez response is not valid JSON with a non-negative in-flight count' >&2
    return 1
  fi
  printf '%s\n' "$in_flight"
}

readyz_ok() {
  local response

  response="$(internal_get /internal/readyz)" \
    || { log 'internal readyz probe is unreachable or rejected' >&2; return 1; }
  if ! printf '%s' "$response" | python3 -c '
import json
import sys

value = json.load(sys.stdin)
if type(value) is not dict or value.get("ready") is not True:
    raise SystemExit(1)
'; then
    log 'internal readyz response is not valid ready JSON' >&2
    return 1
  fi
}

refund_readiness_ok() {
  local response

  response="$(internal_get /internal/refund-rollback-readiness)" \
    || { log 'internal refund readiness probe is unreachable or rejected' >&2; return 1; }
  if ! printf '%s' "$response" | python3 -c '
import json
import sys

value = json.load(sys.stdin)
if type(value) is not dict or value.get("ready") is not True:
    raise SystemExit(1)
pending = value.get("entitlement_reserved_reviewed_pending_count")
if type(pending) is not int or pending != 0:
    raise SystemExit(1)
'; then
    log 'internal refund readiness response is not valid zero-pending JSON' >&2
    return 1
  fi
}

require_enable_readiness() {
  livez_in_flight >/dev/null || die 'live runtime readiness failed before enable'
  readyz_ok || die 'dependency readiness failed before enable'
  refund_readiness_ok || die 'refund reconciliation readiness failed before enable'
}

wait_for_disable_readiness() {
  local attempt=1 in_flight

  while [ "$attempt" -le "$DRAIN_ATTEMPTS" ]; do
    if in_flight="$(livez_in_flight)" \
      && refund_readiness_ok \
      && [ "$in_flight" -eq 0 ]; then
      return 0
    fi
    if [ "$attempt" -lt "$DRAIN_ATTEMPTS" ] && [ "$DRAIN_INTERVAL_SECONDS" -gt 0 ]; then
      sleep "$DRAIN_INTERVAL_SECONDS"
    fi
    attempt=$((attempt + 1))
  done
  return 1
}

prepare_disable_drain() {
  local output

  output="$(env SUB2API_NODE_STATE_LOCK_HELD=1 "$NODE_STATE_SCRIPT" status)" \
    || die 'could not read local runtime node state before drain'
  if [ "$output" = "traffic=accepting active_container=${TARGET_CONTAINER} background=active" ]; then
    env SUB2API_NODE_STATE_LOCK_HELD=1 "$NODE_STATE_SCRIPT" drain >/dev/null \
      || die 'could not set local runtime traffic state to draining'
  elif [ "$output" != "traffic=draining active_container=${TARGET_CONTAINER} background=standby" ]; then
    die "local runtime state is not eligible for disable: ${output}"
  fi
  read_node_state draining standby
}

post_rollout_compare_and_swap() {
  local response

  response="$(docker exec \
    -e "SUB2API_REVIEWED_REFUNDS_ROLLOUT_EXPECTED=${API_EXPECTED}" \
    -e "SUB2API_REVIEWED_REFUNDS_ROLLOUT_ENABLED=${TARGET_ENABLED}" \
    -e "SUB2API_REVIEWED_REFUNDS_ROLLOUT_TOKEN_PATH=${CONTAINER_HEALTH_TOKEN_PATH}" \
    "$TARGET_CONTAINER" \
    sh -ceu '
      token="$(cat "$SUB2API_REVIEWED_REFUNDS_ROLLOUT_TOKEN_PATH")"
      [ -n "$token" ]
      case "${SUB2API_REVIEWED_REFUNDS_ROLLOUT_EXPECTED}:${SUB2API_REVIEWED_REFUNDS_ROLLOUT_ENABLED}" in
        :true) payload="{\"expected\":\"\",\"enabled\":true}" ;;
        false:true) payload="{\"expected\":\"false\",\"enabled\":true}" ;;
        true:false) payload="{\"expected\":\"true\",\"enabled\":false}" ;;
        *) exit 64 ;;
      esac
      wget -Y off -q -T 10 -O - \
        --header="X-Monitor-Token: ${token}" \
        --header="Content-Type: application/json" \
        --post-data="$payload" \
        "http://127.0.0.1:8080/internal/reviewed-refunds-rollout"
    ')" \
    || return 1

  printf '%s' "$response" | python3 -c '
import json
import sys

expected_enabled = sys.argv[1] == "true"
value = json.load(sys.stdin)
if (
    type(value) is not dict
    or set(value) != {"changed", "enabled"}
    or value.get("changed") is not True
    or type(value.get("enabled")) is not bool
    or value["enabled"] is not expected_enabled
):
    raise SystemExit(1)
' "$TARGET_ENABLED"
}

if ! sub2api_maintenance_lock_open "$MAINTENANCE_LOCK_FILE"; then
  die "unsafe maintenance lock: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
fi
flock -n 8 || die 'production maintenance is already running'

verify_topology
if [ "$TARGET_ENABLED" = true ]; then
  read_node_state accepting active
  require_enable_readiness
else
  prepare_disable_drain
  if ! wait_for_disable_readiness; then
    die 'drained runtime did not reach zero in-flight requests and refund readiness; leaving traffic drained'
  fi
fi

# The wait above can be long enough for an operator error or stale lifecycle
# action to surface. Recheck the host topology under the same FD 8 immediately
# before the database-serialized application CAS.
verify_topology
if [ "$TARGET_ENABLED" = true ]; then
  read_node_state accepting active
  require_enable_readiness
else
  read_node_state draining standby
  if ! wait_for_disable_readiness; then
    die 'drained runtime no longer satisfies zero in-flight refund readiness; leaving traffic drained'
  fi
fi

if ! post_rollout_compare_and_swap; then
  if [ "$TARGET_ENABLED" = false ]; then
    die 'reviewed-refunds rollout CAS was rejected; leaving traffic drained'
  fi
  die 'reviewed-refunds rollout CAS was rejected'
fi

log "reviewed-refunds rollout completed: enabled=${TARGET_ENABLED} target=${TARGET_CONTAINER}"
