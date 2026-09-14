#!/usr/bin/env bash

# Release either a legacy server-prepared source tree or a prebuilt image
# through the existing blue-green switch. GitHub's production workflow always
# uses --prebuilt, so production performs no source checkout or compilation.
# The legacy source mode remains available for explicit recovery operations.

set -Eeuo pipefail

RECOVER_RETAINED_CADDY_EXPOSURE=false
if [ "$#" -eq 1 ] && [ "$1" = "--recover-retained-caddy-exposure" ]; then
  # This is the only restart-safe path for a wrapper-owned transaction after a
  # live Caddy reload was attempted. It deliberately has no image/build input:
  # the durable local and Caddy transactions supply the exact old/candidate
  # pair, then this coordinator runs the same refund rollback gate.
  RECOVER_RETAINED_CADDY_EXPOSURE=true
  PREBUILT_MODE=true
  SOURCE_DIR=""
  IMAGE=""
  COMMIT="retained-caddy-recovery"
  VERSION="retained-caddy-recovery"
  PUBLIC_HEALTH_URL=""
  PUBLIC_HEALTH_RESOLVE=""
  RUN_ID="retained-caddy-recovery-$(date -u '+%Y%m%d-%H%M%S')-$$"
elif [ "$#" -ne 6 ]; then
  echo "Usage: sub2api-server-release.sh SOURCE_DIR IMAGE COMMIT VERSION HEALTH_URL RUN_ID" >&2
  echo "   or: sub2api-server-release.sh --prebuilt IMAGE COMMIT VERSION HEALTH_URL RUN_ID" >&2
  echo "   or: sub2api-server-release.sh --recover-retained-caddy-exposure" >&2
  exit 2
else
  PREBUILT_MODE=false
  if [ "$1" = "--prebuilt" ]; then
    PREBUILT_MODE=true
    SOURCE_DIR=""
  else
    SOURCE_DIR="$1"
  fi
  IMAGE="$2"
  COMMIT="$3"
  VERSION="$4"
  PUBLIC_HEALTH_URL="$5"
  PUBLIC_HEALTH_RESOLVE="${SUB2API_PUBLIC_HEALTH_RESOLVE:-}"
  RUN_ID="$6"
fi

APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
WORK_ROOT="${SUB2API_AUTODEPLOY_WORK_ROOT:-/var/lib/sub2api-autodeploy/worktrees}"
BLUE_GREEN_SCRIPT="${APP_DIR}/scripts/sub2api-blue-green-release.sh"
LOG_ROOT="${SUB2API_RELEASE_LOG_DIR:-/var/log/sub2api-release}"
LOG_DIR="${LOG_ROOT}/${RUN_ID}"
BUILD_LOG="${LOG_DIR}/build.log"
SWITCH_LOG="${LOG_DIR}/switch.log"
LOCK_FILE="${SUB2API_RELEASE_LOCK_FILE:-/var/lock/sub2api-release.lock}"
MIN_FREE_BYTES="${SUB2API_RELEASE_MIN_FREE_BYTES:-8589934592}"
BUILD_TIMEOUT_SECONDS="${SUB2API_RELEASE_BUILD_TIMEOUT_SECONDS:-3000}"
BUILD_GOMAXPROCS="${SUB2API_RELEASE_BUILD_GOMAXPROCS:-1}"
BUILD_GO_PARALLELISM="${SUB2API_RELEASE_BUILD_GO_PARALLELISM:-1}"
BUILD_GO_MEMORY_LIMIT="${SUB2API_RELEASE_BUILD_GO_MEMORY_LIMIT:-768MiB}"
# When set, production must receive an image built elsewhere before a release.
# The full commit SHA is appended to this prefix (for example,
# sub2api:prebuilt-<commit>) and the server never invokes docker build.
PREBUILT_IMAGE_PREFIX="${SUB2API_RELEASE_PREBUILT_IMAGE_PREFIX:-}"
CADDY_CONTAINER="${SUB2API_CADDY_CONTAINER:-sub2api-caddy}"
IMAGE_ROUTE_CONTRACT_VERIFIER="${SUB2API_IMAGE_ROUTE_CONTRACT_VERIFIER:-${APP_DIR}/scripts/verify_image_route_contract.py}"
IMAGE_ROUTE_API_HOST="${SUB2API_IMAGE_ROUTE_API_HOST:-api.turtleligpt.com}"
CADDY_TRANSACTION_PATH="${APP_DIR}/.gcp-tw-caddy-transaction.env"
CADDY_CUSTOMER_HOST_TRANSACTION_PATH="${APP_DIR}/.cf-opt-totools-caddy.env"
CADDY_SWITCH_TRANSACTION_PATH="${APP_DIR}/.sub2api-blue-green-caddy-transaction.env"
ALLOW_PREEXISTING_DRAINING_CONTAINER="${SUB2API_RELEASE_ALLOW_PREEXISTING_DRAINING_CONTAINER:-false}"
DRAIN_MONITOR_SCRIPT="${SUB2API_DRAIN_MONITOR_SCRIPT:-${APP_DIR}/scripts/sub2api-drain-monitor.sh}"
DRAIN_INTERVAL_SECONDS="${SUB2API_RELEASE_DRAIN_INTERVAL_SECONDS:-60}"
DRAIN_ACTIVE_WINDOW_SECONDS="${SUB2API_RELEASE_DRAIN_ACTIVE_WINDOW_SECONDS:-600}"
DRAIN_RETRY_DELAY_SECONDS="${SUB2API_RELEASE_DRAIN_RETRY_DELAY_SECONDS:-3600}"
DRAIN_MAX_RUNTIME_SECONDS="${SUB2API_RELEASE_DRAIN_MAX_RUNTIME_SECONDS:-0}"
DRAIN_CADDY_CONFIG_PATH="${SUB2API_RELEASE_CADDY_CONFIG_PATH:-/etc/caddy/Caddyfile}"
NODE_STATE_SCRIPT="${SUB2API_NODE_STATE_SCRIPT:-${APP_DIR}/scripts/sub2api-node-state.sh}"
DUAL_NODE_RUNTIME_ENABLED="${SUB2API_DUAL_NODE_RUNTIME_ENABLED:-false}"
# Production can require a real authenticated request against the candidate
# slot before the Caddy switch. Keep the key in a root-only runtime file; the
# release helper never receives the key value itself.
REAL_REQUEST_PROBE_ENABLED="${SUB2API_RELEASE_REAL_REQUEST_PROBE_ENABLED:-false}"
REAL_REQUEST_PROBE_SCRIPT="${SUB2API_RELEASE_REAL_REQUEST_PROBE_SCRIPT:-${APP_DIR}/scripts/sub2api-real-request-probe.sh}"
REAL_REQUEST_PROBE_KEY_FILE="${SUB2API_RELEASE_REAL_REQUEST_PROBE_KEY_FILE:-${APP_DIR}/secrets/release-probe-api-key}"
REAL_REQUEST_PROBE_MODEL="${SUB2API_RELEASE_REAL_REQUEST_PROBE_MODEL:-gpt-5.6-sol}"
NODE_STATE_DIR="${SUB2API_NODE_STATE_DIR:-/var/lib/sub2api/runtime}"
LOCAL_RELEASE_STATE_FILE_HOST="${SUB2API_LOCAL_RELEASE_STATE_FILE_HOST:-${NODE_STATE_DIR}/local-release.env}"
# The traffic-state file is a single-file bind mount. During a post-switch
# rollback it is updated in place while the shared maintenance lock is held so
# the candidate stops admitting requests before its refund reservation state is
# inspected. Never replace it with rename(2): already-running containers would
# remain bound to the old inode.
RUNTIME_TRAFFIC_STATE_FILE="${SUB2API_TRAFFIC_STATE_FILE_HOST:-${NODE_STATE_DIR}/traffic-state}"
REFUND_ROLLBACK_READINESS_PATH="/internal/refund-rollback-readiness"
# Set only after the authenticated candidate gate passes. It lets later
# rollback failures restore the admission state only while Caddy still proves
# that the candidate remains the serving generation.
ROLLBACK_GATE_PRIOR_TRAFFIC_STATE=""
# Set only while a wrapper-owned Caddy transaction proves that a live reload
# was attempted. A failed readiness probe must keep admission drained whenever
# Caddy is old or ambiguous, because a brief candidate exposure may already
# have created a reviewed refund reservation.
RETAINED_CADDY_EXPOSURE_RECOVERY=false
# Keep an explicitly blank value invalid. An unset setting preserves the
# deployed local-dependency release behavior.
DEPENDENCY_MODE="${SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE-local}"
EXTERNAL_RUNTIME_ENV_FILE="${SUB2API_EXTERNAL_RUNTIME_ENV_FILE:-}"
EXTERNAL_CA_FILE="${SUB2API_EXTERNAL_CA_FILE:-}"
# Ordinary releases activate the newly selected generation as this node's
# background-work owner. A request-serving rollback/canary node can explicitly
# preserve its existing standby fence across the entire blue-green transaction.
RELEASE_BACKGROUND_MODE="${SUB2API_RELEASE_BACKGROUND_MODE:-activate}"
FIXED_EGRESS_COMPATIBILITY_MODE="${SUB2API_RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE:-preserve}"

timestamp() {
  date '+%Y-%m-%d %H:%M:%S'
}

log() {
  printf '[%s] %s\n' "$(timestamp)" "$*"
}

die() {
  log "ERROR: $*" >&2
  log "Server logs: ${LOG_DIR}" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

SERVER_RELEASE_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAINTENANCE_LOCK_HELPER="${SERVER_RELEASE_SCRIPT_DIR}/sub2api-maintenance-lock.sh"
[ -r "$MAINTENANCE_LOCK_HELPER" ] && [ ! -L "$MAINTENANCE_LOCK_HELPER" ] \
  || die "maintenance lock helper is missing or unsafe: ${MAINTENANCE_LOCK_HELPER}"
# shellcheck disable=SC1090,SC1091 # Installed alongside this root-owned executable.
. "$MAINTENANCE_LOCK_HELPER"
MAINTENANCE_LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE}"
if ! sub2api_maintenance_lock_validate_configured_path "$MAINTENANCE_LOCK_FILE"; then
  die "unsafe maintenance lock: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
fi

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

require_go_memory_limit() {
  case "$2" in
    ''|*[!0-9kKmMgGtTpPeEiIbB]*) die "$1 must be a Go memory quantity" ;;
    *[bB]) ;;
    *) die "$1 must include a memory unit" ;;
  esac
}

run_blue_green() {
  # Pass only the dependency selector and root-owned paths. Runtime secrets
  # remain in the external env file and never enter this coordinator's logs.
  env \
    SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE="$DEPENDENCY_MODE" \
    SUB2API_EXTERNAL_RUNTIME_ENV_FILE="$EXTERNAL_RUNTIME_ENV_FILE" \
    SUB2API_EXTERNAL_CA_FILE="$EXTERNAL_CA_FILE" \
    SUB2API_IMAGE_ROUTE_CONTRACT_VERIFIER="$IMAGE_ROUTE_CONTRACT_VERIFIER" \
    SUB2API_IMAGE_ROUTE_API_HOST="$IMAGE_ROUTE_API_HOST" \
    ALLOW_ISOLATED_OLD_CONTAINER=false \
    SUB2API_RELEASE_ROUTE_CONTRACT_WARN_ONLY=false \
    SUB2API_RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE="$FIXED_EGRESS_COMPATIBILITY_MODE" \
    SUB2API_RELEASE_REAL_REQUEST_PROBE_ENABLED="$REAL_REQUEST_PROBE_ENABLED" \
    SUB2API_RELEASE_REAL_REQUEST_PROBE_SCRIPT="$REAL_REQUEST_PROBE_SCRIPT" \
    SUB2API_RELEASE_REAL_REQUEST_PROBE_KEY_FILE="$REAL_REQUEST_PROBE_KEY_FILE" \
    SUB2API_RELEASE_REAL_REQUEST_PROBE_MODEL="$REAL_REQUEST_PROBE_MODEL" \
    "$@"
}

image_route_verifier_is_safe() {
  [ -f "$IMAGE_ROUTE_CONTRACT_VERIFIER" ] \
    && [ ! -L "$IMAGE_ROUTE_CONTRACT_VERIFIER" ] \
    && [ -x "$IMAGE_ROUTE_CONTRACT_VERIFIER" ] \
    || return 1
  [ "$(id -u)" -ne 0 ] \
    || [ "$(stat -c '%u:%g:%a' "$IMAGE_ROUTE_CONTRACT_VERIFIER")" = '0:0:750' ]
}

require_image_route_verifier() {
  image_route_verifier_is_safe \
    || {
      log "ERROR: image route contract verifier is missing or unsafe: $IMAGE_ROUTE_CONTRACT_VERIFIER" >&2
      return 1
    }
}

verify_image_route_contract_json() {
  local config_json="$1"
  require_image_route_verifier \
    || {
      return 1
    }
  printf '%s\n' "$config_json" \
    | "$IMAGE_ROUTE_CONTRACT_VERIFIER" --host "$IMAGE_ROUTE_API_HOST" >/dev/null \
    || {
      log "ERROR: Caddy image route contract failed for the supplied JSON" >&2
      return 1
    }
}

verify_image_route_contract_startup() {
  require_image_route_verifier || return 1
  docker exec "$CADDY_CONTAINER" caddy adapt \
    --config "$DRAIN_CADDY_CONFIG_PATH" --adapter caddyfile \
    | "$IMAGE_ROUTE_CONTRACT_VERIFIER" --host "$IMAGE_ROUTE_API_HOST" >/dev/null \
    || {
      log "ERROR: startup Caddy image route contract failed" >&2
      return 1
    }
}

verify_image_route_contract_active() {
  require_image_route_verifier || return 1
  docker exec "$CADDY_CONTAINER" sh -c \
    'wget -Y off -qO- http://127.0.0.1:2019/config/ 2>/dev/null || curl --noproxy "*" -fsS http://127.0.0.1:2019/config/' \
    | "$IMAGE_ROUTE_CONTRACT_VERIFIER" --host "$IMAGE_ROUTE_API_HOST" >/dev/null \
    || {
      log "ERROR: active Caddy image route contract failed" >&2
      return 1
    }
}

verify_image_route_contract_views() {
  verify_image_route_contract_startup && verify_image_route_contract_active
}

warn_image_route_contract_views() {
  if ! verify_image_route_contract_views; then
    # Rollback restores the last known serving generation. A route-contract
    # warning must remain visible, but it cannot turn a successful restoration
    # into a reported rollback failure.
    log "WARNING: Caddy rolled back but the image route contract could not be verified; continuing with the restored generation" >&2
  fi
}

for command_name in docker curl flock grep awk perl python3 systemd-run id mkdir stat sleep tr chmod; do
  require_cmd "$command_name"
done
require_positive_integer SUB2API_RELEASE_MIN_FREE_BYTES "$MIN_FREE_BYTES"
if [ "$PREBUILT_MODE" != "true" ] && [ -z "$PREBUILT_IMAGE_PREFIX" ]; then
  require_cmd timeout
  require_positive_integer SUB2API_RELEASE_BUILD_TIMEOUT_SECONDS "$BUILD_TIMEOUT_SECONDS"
  require_positive_integer SUB2API_RELEASE_BUILD_GOMAXPROCS "$BUILD_GOMAXPROCS"
  require_positive_integer SUB2API_RELEASE_BUILD_GO_PARALLELISM "$BUILD_GO_PARALLELISM"
  require_go_memory_limit SUB2API_RELEASE_BUILD_GO_MEMORY_LIMIT "$BUILD_GO_MEMORY_LIMIT"
fi
case "$PREBUILT_IMAGE_PREFIX" in
  '') ;;
  *[!A-Za-z0-9./:_-]*) die "SUB2API_RELEASE_PREBUILT_IMAGE_PREFIX contains unsupported characters" ;;
esac
require_bool SUB2API_RELEASE_ALLOW_PREEXISTING_DRAINING_CONTAINER "$ALLOW_PREEXISTING_DRAINING_CONTAINER"
require_bool SUB2API_DUAL_NODE_RUNTIME_ENABLED "$DUAL_NODE_RUNTIME_ENABLED"
require_bool SUB2API_RELEASE_REAL_REQUEST_PROBE_ENABLED "$REAL_REQUEST_PROBE_ENABLED"
case "$RELEASE_BACKGROUND_MODE" in
  activate|preserve-standby) ;;
  *) die "SUB2API_RELEASE_BACKGROUND_MODE must be activate or preserve-standby" ;;
esac
case "$FIXED_EGRESS_COMPATIBILITY_MODE" in
  preserve|true|false) ;;
  *) die "SUB2API_RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE must be preserve, true, or false" ;;
esac
[ "$RELEASE_BACKGROUND_MODE" != preserve-standby ] || [ "$DUAL_NODE_RUNTIME_ENABLED" = true ] \
  || die "SUB2API_RELEASE_BACKGROUND_MODE=preserve-standby requires SUB2API_DUAL_NODE_RUNTIME_ENABLED=true"
if [ "$REAL_REQUEST_PROBE_ENABLED" = true ]; then
  [ -f "$REAL_REQUEST_PROBE_SCRIPT" ] && [ ! -L "$REAL_REQUEST_PROBE_SCRIPT" ] \
    || die "real request probe script is missing or is a symlink: $REAL_REQUEST_PROBE_SCRIPT"
  [ "$(realpath -e -- "$REAL_REQUEST_PROBE_SCRIPT")" = "$REAL_REQUEST_PROBE_SCRIPT" ] \
    || die "real request probe script must be canonical: $REAL_REQUEST_PROBE_SCRIPT"
  [ -x "$REAL_REQUEST_PROBE_SCRIPT" ] \
    || die "real request probe script is not executable: $REAL_REQUEST_PROBE_SCRIPT"
  [ "$(stat -c '%u:%g:%a' "$REAL_REQUEST_PROBE_SCRIPT")" = '0:0:750' ] \
    || die "real request probe script must be root-owned mode 0750"
  [ -f "$REAL_REQUEST_PROBE_KEY_FILE" ] && [ ! -L "$REAL_REQUEST_PROBE_KEY_FILE" ] \
    || die "real request probe key file is missing or is a symlink: $REAL_REQUEST_PROBE_KEY_FILE"
  [ "$(realpath -e -- "$REAL_REQUEST_PROBE_KEY_FILE")" = "$REAL_REQUEST_PROBE_KEY_FILE" ] \
    || die "real request probe key file must be canonical: $REAL_REQUEST_PROBE_KEY_FILE"
  [ "$(stat -c '%u:%g:%a' "$REAL_REQUEST_PROBE_KEY_FILE")" = '0:0:600' ] \
    || die "real request probe key file must be root-owned mode 0600"
fi
validate_health_resolve "$PUBLIC_HEALTH_RESOLVE" "$PUBLIC_HEALTH_URL"
require_positive_integer SUB2API_RELEASE_DRAIN_INTERVAL_SECONDS "$DRAIN_INTERVAL_SECONDS"
require_positive_integer SUB2API_RELEASE_DRAIN_ACTIVE_WINDOW_SECONDS "$DRAIN_ACTIVE_WINDOW_SECONDS"
require_non_negative_integer SUB2API_RELEASE_DRAIN_RETRY_DELAY_SECONDS "$DRAIN_RETRY_DELAY_SECONDS"
require_non_negative_integer SUB2API_RELEASE_DRAIN_MAX_RUNTIME_SECONDS "$DRAIN_MAX_RUNTIME_SECONDS"
case "$DEPENDENCY_MODE" in
  local|external) ;;
  *) die "SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE must be local or external" ;;
esac
SWITCH_RUN_BACKUP=true
if [ "$DEPENDENCY_MODE" = external ]; then
  # The external data-service owner owns its backup policy. The local backup
  # helper targets the retired Compose-managed PostgreSQL service.
  SWITCH_RUN_BACKUP=false
fi

if [ "$RECOVER_RETAINED_CADDY_EXPOSURE" != true ]; then
  if [ "$PREBUILT_MODE" != "true" ]; then
    case "$SOURCE_DIR" in
      "${WORK_ROOT%/}"/*) ;;
      *) die "refusing source outside automatic-release work root: $SOURCE_DIR" ;;
    esac
  fi
  case "$IMAGE" in
    sub2api:auto-*) ;;
    *) die "refusing unexpected image tag: $IMAGE" ;;
  esac
  case "$COMMIT" in
    *[!0-9a-f]*|'') die "invalid commit: $COMMIT" ;;
  esac
fi
case "$RUN_ID" in
  *[!A-Za-z0-9._-]*|'') die "invalid release run id" ;;
esac

mkdir -p "$LOG_DIR"
exec 9>"$LOCK_FILE"
flock -n 9 || die "another production release is already running"
if ! sub2api_maintenance_lock_open "$MAINTENANCE_LOCK_FILE"; then
  die "unsafe maintenance lock: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
fi
flock -n 8 || die "production maintenance or runtime recovery is already running"
[ ! -e "$CADDY_TRANSACTION_PATH" ] && [ ! -L "$CADDY_TRANSACTION_PATH" ] \
  || die "unfinished GCP Taiwan Caddy listener transaction exists; commit or rollback it before a production release"
[ ! -e "$CADDY_CUSTOMER_HOST_TRANSACTION_PATH" ] && [ ! -L "$CADDY_CUSTOMER_HOST_TRANSACTION_PATH" ] \
  || die "unfinished customer Host Caddy transaction exists; commit or rollback it before a production release"
if [ "$RECOVER_RETAINED_CADDY_EXPOSURE" = true ]; then
  [ -e "$CADDY_SWITCH_TRANSACTION_PATH" ] || [ -L "$CADDY_SWITCH_TRANSACTION_PATH" ] \
    || die "no retained blue-green Caddy upstream transaction exists for guarded recovery"
else
  [ ! -e "$CADDY_SWITCH_TRANSACTION_PATH" ] && [ ! -L "$CADDY_SWITCH_TRANSACTION_PATH" ] \
    || die "unfinished blue-green Caddy upstream transaction exists; recover it before a production release with sub2api-server-release.sh --recover-retained-caddy-exposure"
fi

if [ "$PREBUILT_MODE" != "true" ]; then
  [ -d "$SOURCE_DIR" ] || die "source directory does not exist: $SOURCE_DIR"
  [ -f "$SOURCE_DIR/Dockerfile" ] || die "repository Dockerfile is missing"
fi
[ -x "$BLUE_GREEN_SCRIPT" ] || die "blue-green script is missing or not executable"
if [ "$RECOVER_RETAINED_CADDY_EXPOSURE" != true ]; then
  [ -x "$DRAIN_MONITOR_SCRIPT" ] || die "drain monitor is missing or not executable: $DRAIN_MONITOR_SCRIPT"
  require_image_route_verifier \
    || die "image route contract verifier is missing or unsafe: $IMAGE_ROUTE_CONTRACT_VERIFIER"
fi
case "$IMAGE_ROUTE_API_HOST" in
  ''|*[!A-Za-z0-9.-]*|.*|*..*|*.) die "SUB2API_IMAGE_ROUTE_API_HOST must be a simple DNS name" ;;
esac
[ "$DUAL_NODE_RUNTIME_ENABLED" != true ] || [ -x "$NODE_STATE_SCRIPT" ] \
  || die "node state helper is missing or not executable: $NODE_STATE_SCRIPT"
[ "$RECOVER_RETAINED_CADDY_EXPOSURE" != true ] || [ "$DUAL_NODE_RUNTIME_ENABLED" = true ] \
  || die "guarded retained-Caddy recovery requires SUB2API_DUAL_NODE_RUNTIME_ENABLED=true"

run_node_state() {
  if [ "$DUAL_NODE_RUNTIME_ENABLED" != true ]; then
	case "${1:-}" in
	  status) printf 'traffic=accepting active_container=%s background=active\n' "$OLD_CONTAINER" ;;
	esac
	return 0
  fi
  env SUB2API_NODE_STATE_LOCK_HELD=1 "$NODE_STATE_SCRIPT" "$@"
}

require_no_unfinished_local_transaction_before_stale_target_removal() {
  if [ "$DUAL_NODE_RUNTIME_ENABLED" = true ]; then
    run_node_state preflight >>"${LOG_DIR}/node-state.log" \
      || die "could not verify that no local release transaction is unfinished before removing an external target"
    return
  fi

  # Legacy single-node releases do not invoke node-state, but a retained
  # transaction file may still name the stopped rollback generation.  Treat
  # every residue as unfinished, including a dangling symlink or a non-file.
  if [ -e "$LOCAL_RELEASE_STATE_FILE_HOST" ] || [ -L "$LOCAL_RELEASE_STATE_FILE_HOST" ]; then
    die "an unfinished local release transaction exists at ${LOCAL_RELEASE_STATE_FILE_HOST}; refusing to remove an external target"
  fi
}

if [ "$RECOVER_RETAINED_CADDY_EXPOSURE" != true ]; then
  available_bytes="$(df --output=avail -B1 / | tail -1 | tr -d '[:space:]')"
  [ "$available_bytes" -ge "$MIN_FREE_BYTES" ] || die "less than 8 GiB is free on the server"

  active_upstream="$(grep -oE 'sub2api(-(blue|green))?:8080' "${APP_DIR}/Caddyfile" | sort -u)"
  upstream_count="$(printf '%s\n' "$active_upstream" | sed '/^$/d' | wc -l)"
  [ "$upstream_count" -eq 1 ] || die "Caddy upstream is ambiguous: $active_upstream"

  OLD_CONTAINER="${active_upstream%:8080}"
# Three names remain available so a deliberately approved long-lived drain can
# be retained. By default, however, a release refuses to start while any
# inactive application container is still running: every application container
# also starts background queue consumers, so an old binary could otherwise
# process a new job with stale semantics even though Caddy sends it no traffic.
case "$OLD_CONTAINER" in
  sub2api-blue)
    release_candidates=(sub2api-green sub2api)
    ;;
  sub2api-green)
    release_candidates=(sub2api-blue sub2api)
    ;;
  sub2api)
    release_candidates=(sub2api-green sub2api-blue)
    ;;
  *)
    die "unsupported active container: $OLD_CONTAINER"
    ;;
esac

running_inactive_containers=()
for candidate in "${release_candidates[@]}"; do
  candidate_running="$(docker inspect "$candidate" --format '{{.State.Running}}' 2>/dev/null || true)"
  if [ "$candidate_running" = "true" ]; then
    running_inactive_containers+=("$candidate")
  fi
done
if [ "${#running_inactive_containers[@]}" -gt 0 ] \
  && [ "$ALLOW_PREEXISTING_DRAINING_CONTAINER" != "true" ]; then
  die "pre-existing inactive container(s) are still running: ${running_inactive_containers[*]}; wait for the drain monitor or stop them only after verifying zero active connections, because they can consume shared background queues"
fi

NEW_CONTAINER=""
for candidate in "${release_candidates[@]}"; do
  candidate_running="$(docker inspect "$candidate" --format '{{.State.Running}}' 2>/dev/null || true)"
  if [ "$candidate_running" != "true" ]; then
    NEW_CONTAINER="$candidate"
    break
  fi
done
[ -n "$NEW_CONTAINER" ] || die "no absent or stopped release target; other colors are still draining"

OLD_UPSTREAM="${OLD_CONTAINER}:8080"
NEW_UPSTREAM="${NEW_CONTAINER}:8080"
OLD_IMAGE="$(docker inspect "$OLD_CONTAINER" --format '{{.Config.Image}}' 2>/dev/null || true)"
OLD_RUNNING="$(docker inspect "$OLD_CONTAINER" --format '{{.State.Running}}' 2>/dev/null || true)"
OLD_HEALTH="$(docker inspect "$OLD_CONTAINER" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' 2>/dev/null || true)"
[ "$OLD_RUNNING" = "true" ] || die "active container is not running: $OLD_CONTAINER"
[ "$OLD_HEALTH" = "healthy" ] || die "active container is not healthy: $OLD_CONTAINER ($OLD_HEALTH)"
  if ! verify_image_route_contract_views; then
    die "current Caddy image route contract is invalid; refusing to start a release"
  fi
fi

caddy_config_points_uniquely_to_upstream() {
  local caddy_config="$1"
  local expected_upstream="$2"
  local upstreams
  local upstream_count

  upstreams="$(printf '%s\n' "$caddy_config" | grep -oE 'sub2api(-(blue|green))?:8080' | sort -u || true)"
  upstream_count="$(printf '%s\n' "$upstreams" | sed '/^$/d' | wc -l | tr -d '[:space:]')"
  [ "$upstream_count" -eq 1 ] && [ "$upstreams" = "$expected_upstream" ]
}

caddy_views_point_uniquely_to_upstream() {
  local expected_upstream="$1"
  local active_config
  local caddy_config
  local host_config
  local startup_config

  if ! host_config="$(cat "${APP_DIR}/Caddyfile")"; then
    log "WARNING: could not read host Caddyfile while checking ${expected_upstream}" >&2
    return 1
  fi
  if ! startup_config="$(docker exec \
    -e "CADDY_CHECK_PATH=${DRAIN_CADDY_CONFIG_PATH}" \
    "$CADDY_CONTAINER" sh -c 'cat "$CADDY_CHECK_PATH"')"; then
    log "WARNING: could not read Caddy startup configuration while checking ${expected_upstream}" >&2
    return 1
  fi
  if ! active_config="$(docker exec "$CADDY_CONTAINER" sh -c 'wget -Y off -qO- http://127.0.0.1:2019/config/ 2>/dev/null || curl --noproxy "*" -fsS http://127.0.0.1:2019/config/')"; then
    log "WARNING: could not read active Caddy configuration while checking ${expected_upstream}" >&2
    return 1
  fi

  for caddy_config in "$host_config" "$startup_config" "$active_config"; do
    caddy_config_points_uniquely_to_upstream "$caddy_config" "$expected_upstream" || return 1
  done
  return 0
}

caddy_views_point_uniquely_to_old() {
  caddy_views_point_uniquely_to_upstream "$OLD_UPSTREAM"
}

runtime_traffic_state_file_metadata() {
  stat -c '%u:%g:%a' "$RUNTIME_TRAFFIC_STATE_FILE" 2>/dev/null \
    || stat -f '%u:%g:%Lp' "$RUNTIME_TRAFFIC_STATE_FILE"
}

runtime_traffic_state_file_is_safe() {
  local metadata

  [ "$DUAL_NODE_RUNTIME_ENABLED" = true ] || {
    log "ERROR: post-switch rollback requires dual-node runtime traffic admission" >&2
    return 1
  }
  case "$RUNTIME_TRAFFIC_STATE_FILE" in
    /*) ;;
    *)
      log "ERROR: runtime traffic-state path must be absolute" >&2
      return 1
      ;;
  esac
  [ -f "$RUNTIME_TRAFFIC_STATE_FILE" ] && [ ! -L "$RUNTIME_TRAFFIC_STATE_FILE" ] || {
    log "ERROR: runtime traffic-state file is missing or unsafe: ${RUNTIME_TRAFFIC_STATE_FILE}" >&2
    return 1
  }
  metadata="$(runtime_traffic_state_file_metadata)" || {
    log "ERROR: could not inspect runtime traffic-state file: ${RUNTIME_TRAFFIC_STATE_FILE}" >&2
    return 1
  }
  if [ "$(id -u)" -eq 0 ]; then
    [ "$metadata" = '0:0:644' ] || {
      log "ERROR: runtime traffic-state file has unexpected metadata: ${RUNTIME_TRAFFIC_STATE_FILE}" >&2
      return 1
    }
  else
    # Hermetic deployment tests run as the developer user. Production reaches
    # this branch only through the root-owned release receiver.
    [ "${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-0}" = 1 ] \
      && [ "${metadata##*:}" = 644 ] || {
      log "ERROR: runtime traffic-state file is not available to the release boundary" >&2
      return 1
    }
  fi
  return 0
}

read_runtime_traffic_state() {
  local state

  runtime_traffic_state_file_is_safe || return 1
  state="$(tr -d '\r\n' <"$RUNTIME_TRAFFIC_STATE_FILE")" || return 1
  case "$state" in
    accepting|draining)
      printf '%s\n' "$state"
      ;;
    *)
      log "ERROR: runtime traffic-state file has an invalid value" >&2
      return 1
      ;;
  esac
}

write_runtime_traffic_state() {
  local state="$1"

  case "$state" in
    accepting|draining) ;;
    *)
      log "ERROR: refusing unsupported runtime traffic state: ${state}" >&2
      return 1
      ;;
  esac
  runtime_traffic_state_file_is_safe || return 1
  # Keep the inode stable for the bind-mounted candidate. The maintenance lock
  # held by this coordinator serializes this with node-state transitions.
  printf '%s\n' "$state" >"$RUNTIME_TRAFFIC_STATE_FILE" \
    && chmod 644 "$RUNTIME_TRAFFIC_STATE_FILE"
}

candidate_internal_get_2xx() {
  local path="$1"
  local response

  case "$path" in
    /internal/livez|"$REFUND_ROLLBACK_READINESS_PATH") ;;
    *)
      log "ERROR: refusing unsupported candidate internal probe path" >&2
      return 1
      ;;
  esac
  # The canonical production image is Alpine/BusyBox and does not install
  # curl. BusyBox wget returns non-zero for an HTTP non-2xx response, so this
  # request fails closed for both an unavailable candidate and a blocked
  # readiness check. The token remains inside the candidate namespace.
  if ! response="$(docker exec \
    -e "SUB2API_ROLLBACK_GATE_PATH=${path}" \
    "$NEW_CONTAINER" \
    sh -ceu '
      token="$(cat /run/sub2api-runtime/health-token)"
      [ -n "$token" ]
      wget -Y off -q -T 10 -O - \
        --header="X-Monitor-Token: ${token}" \
        "http://127.0.0.1:8080${SUB2API_ROLLBACK_GATE_PATH}"
    ')"; then
    log "ERROR: candidate internal rollback probe is unreachable or rejected: ${path}" >&2
    return 1
  fi
  printf '%s' "$response"
}

candidate_in_flight_requests() {
  local response
  local in_flight

  response="$(candidate_internal_get_2xx /internal/livez)" || return 1
  in_flight="$(printf '%s' "$response" | perl -ne 'if (/"in_flight_requests"\s*:\s*([0-9]+)/) { print "$1\n"; exit }')"
  case "$in_flight" in
    ''|*[!0-9]*)
      log "ERROR: candidate liveness response omitted a valid in-flight request count" >&2
      return 1
      ;;
  esac
  printf '%s\n' "$in_flight"
}

candidate_refund_rollback_ready() {
  local response

  response="$(candidate_internal_get_2xx "$REFUND_ROLLBACK_READINESS_PATH")" || return 1
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
    log "ERROR: candidate refund rollback readiness response is not valid zero-pending JSON: ${REFUND_ROLLBACK_READINESS_PATH}" >&2
    return 1
  fi
}

wait_for_candidate_requests_to_drain() {
  local started="$SECONDS"
  local in_flight

  while :; do
    in_flight="$(candidate_in_flight_requests)" || return 1
    if [ "$in_flight" -eq 0 ]; then
      return 0
    fi
    if [ $((SECONDS - started)) -ge "$DRAIN_ACTIVE_WINDOW_SECONDS" ]; then
      log "ERROR: candidate requests did not drain before rollback: in_flight=${in_flight}" >&2
      return 1
    fi
    sleep 1
  done
}

restore_runtime_traffic_after_failed_refund_rollback_gate() {
  local prior_state="$1"

  # A retained wrapper-owned transaction means a live Caddy reload was
  # attempted, even when every current Caddy view has since returned to old.
  # Do not re-admit the old generation on a blocked/unreachable readiness
  # probe: the candidate may have persisted a reviewed refund reservation
  # during that exposure. Candidate-only views retain the existing behavior,
  # because the fenced candidate remains the only verified serving generation.
  if [ "$RETAINED_CADDY_EXPOSURE_RECOVERY" = true ] \
    && ! caddy_views_point_uniquely_to_upstream "$NEW_UPSTREAM"; then
    log "Refund rollback gate retained a possible candidate exposure and Caddy no longer conclusively points only at ${NEW_UPSTREAM}; leaving traffic admission draining for canonical recovery" >&2
    return 0
  fi
  if write_runtime_traffic_state "$prior_state"; then
    log "Refund rollback gate left Caddy on ${NEW_CONTAINER}; restored traffic admission to ${prior_state} while retaining the local release transaction"
    return 0
  fi
  log "ERROR: refund rollback gate failed and traffic admission could not be restored; ${NEW_CONTAINER} remains fenced for manual recovery" >&2
  return 1
}

restore_runtime_traffic_after_post_gate_rollback_failure() {
  local failure_context="$1"
  local prior_state="${ROLLBACK_GATE_PRIOR_TRAFFIC_STATE:-}"

  [ -n "$prior_state" ] || return 0
  # The rollback helper can fail after it has already switched Caddy back to
  # the old generation. Reopen admission only when every Caddy view still
  # proves that the candidate is serving; otherwise preserve the fence for the
  # same canonical recovery transaction.
  if ! caddy_views_point_uniquely_to_upstream "$NEW_UPSTREAM"; then
    log "WARNING: ${failure_context}; Caddy no longer conclusively points only at ${NEW_UPSTREAM}, leaving traffic admission draining for canonical recovery" >&2
    return 0
  fi
  if write_runtime_traffic_state "$prior_state"; then
    log "${failure_context}; Caddy remains on ${NEW_CONTAINER}, restored traffic admission to ${prior_state} while retaining the local release transaction"
    return 0
  fi
  log "ERROR: ${failure_context}; Caddy remains on ${NEW_CONTAINER} but traffic admission could not be restored" >&2
  return 1
}

gate_post_switch_rollback_on_refund_readiness() {
  local prior_traffic_state

  ROLLBACK_GATE_PRIOR_TRAFFIC_STATE=""
  prior_traffic_state="$(read_runtime_traffic_state)" || return 1
  if ! write_runtime_traffic_state draining; then
    return 1
  fi
  log "Drained new requests from ${NEW_CONTAINER} before checking reviewed refund reservations"
  if ! wait_for_candidate_requests_to_drain; then
    restore_runtime_traffic_after_failed_refund_rollback_gate "$prior_traffic_state" || true
    return 1
  fi
  if ! candidate_refund_rollback_ready; then
    restore_runtime_traffic_after_failed_refund_rollback_gate "$prior_traffic_state" || true
    return 1
  fi
  ROLLBACK_GATE_PRIOR_TRAFFIC_STATE="$prior_traffic_state"
  log "Refund rollback readiness passed for ${NEW_CONTAINER}; continuing with the old-generation takeover"
  return 0
}

retained_transaction_value() {
  local transaction_file="$1"
  local key="$2"

  awk -F= -v key="$key" '
    $1 == key { count += 1; value = substr($0, length(key) + 2) }
    END {
      if (count != 1 || value == "") exit 1
      print value
    }
  ' "$transaction_file"
}

retained_transaction_file_is_safe() {
  local transaction_file="$1"
  local label="$2"
  local metadata expected_uid

  [ -f "$transaction_file" ] && [ ! -L "$transaction_file" ] || {
    log "ERROR: ${label} is missing or unsafe: ${transaction_file}" >&2
    return 1
  }
  expected_uid="$(id -u)" || return 1
  metadata="$(stat -c '%u:%a' "$transaction_file" 2>/dev/null \
    || stat -f '%u:%Lp' "$transaction_file")" || return 1
  [ "$metadata" = "${expected_uid}:600" ] || {
    log "ERROR: ${label} has unsafe metadata: ${metadata}" >&2
    return 1
  }
}

require_retained_container_name() {
  case "$2" in
    sub2api|sub2api-blue|sub2api-green) ;;
    *)
      log "ERROR: ${1} is not a supported application container" >&2
      return 1
      ;;
  esac
}

load_retained_caddy_switch_identity() {
  retained_transaction_file_is_safe "$CADDY_SWITCH_TRANSACTION_PATH" \
    "retained blue-green Caddy transaction" || return 1
  RETAINED_CADDY_RECOVERY_OWNER="$(retained_transaction_value "$CADDY_SWITCH_TRANSACTION_PATH" RECOVERY_OWNER)" \
    || return 1
  RETAINED_CADDY_LIVE_RELOAD_ATTEMPTED="$(retained_transaction_value "$CADDY_SWITCH_TRANSACTION_PATH" LIVE_RELOAD_ATTEMPTED)" \
    || return 1
  RETAINED_CADDY_UPSTREAM_FROM="$(retained_transaction_value "$CADDY_SWITCH_TRANSACTION_PATH" UPSTREAM_FROM)" \
    || return 1
  RETAINED_CADDY_UPSTREAM_TO="$(retained_transaction_value "$CADDY_SWITCH_TRANSACTION_PATH" UPSTREAM_TO)" \
    || return 1
  [ "$RETAINED_CADDY_RECOVERY_OWNER" = server-wrapper ] \
    && [ "$RETAINED_CADDY_LIVE_RELOAD_ATTEMPTED" = true ] \
    || return 1
  case "$RETAINED_CADDY_UPSTREAM_FROM:$RETAINED_CADDY_UPSTREAM_TO" in
    sub2api:8080:sub2api-green:8080|sub2api:8080:sub2api-blue:8080|\
    sub2api-green:8080:sub2api:8080|sub2api-green:8080:sub2api-blue:8080|\
    sub2api-blue:8080:sub2api:8080|sub2api-blue:8080:sub2api-green:8080) ;;
    *) return 1 ;;
  esac
}

load_retained_local_release_identity() {
  retained_transaction_file_is_safe "$LOCAL_RELEASE_STATE_FILE_HOST" \
    "retained local release transaction" || return 1
  RETAINED_LOCAL_STATE="$(retained_transaction_value "$LOCAL_RELEASE_STATE_FILE_HOST" state)" \
    || return 1
  RETAINED_LOCAL_PREVIOUS="$(retained_transaction_value "$LOCAL_RELEASE_STATE_FILE_HOST" previous)" \
    || return 1
  RETAINED_LOCAL_CANDIDATE="$(retained_transaction_value "$LOCAL_RELEASE_STATE_FILE_HOST" candidate)" \
    || return 1
  [ "$RETAINED_LOCAL_STATE" = local-switching ] || return 1
  require_retained_container_name previous "$RETAINED_LOCAL_PREVIOUS" || return 1
  require_retained_container_name candidate "$RETAINED_LOCAL_CANDIDATE" || return 1
  [ "$RETAINED_LOCAL_PREVIOUS" != "$RETAINED_LOCAL_CANDIDATE" ] || return 1
}

retained_caddy_switch_matches_current_release() {
  load_retained_caddy_switch_identity || return 1
  [ "$RETAINED_CADDY_UPSTREAM_FROM" = "$OLD_UPSTREAM" ] \
    && [ "$RETAINED_CADDY_UPSTREAM_TO" = "$NEW_UPSTREAM" ]
}

load_retained_caddy_exposure_for_recovery() {
  load_retained_caddy_switch_identity || return 1
  load_retained_local_release_identity || return 1
  [ "$RETAINED_CADDY_UPSTREAM_FROM" = "${RETAINED_LOCAL_PREVIOUS}:8080" ] \
    && [ "$RETAINED_CADDY_UPSTREAM_TO" = "${RETAINED_LOCAL_CANDIDATE}:8080" ] \
    || return 1
  OLD_CONTAINER="$RETAINED_LOCAL_PREVIOUS"
  NEW_CONTAINER="$RETAINED_LOCAL_CANDIDATE"
  OLD_UPSTREAM="$RETAINED_CADDY_UPSTREAM_FROM"
  NEW_UPSTREAM="$RETAINED_CADDY_UPSTREAM_TO"
  OLD_IMAGE=""
}

cleanup_retained_caddy_candidate() {
  if ! docker inspect "$NEW_CONTAINER" >/dev/null 2>&1; then
    return 0
  fi
  if ! caddy_views_point_uniquely_to_old; then
    log "ERROR: retained recovery restored admission state but Caddy does not conclusively point at ${OLD_UPSTREAM}; retaining ${NEW_CONTAINER}" >&2
    return 1
  fi
  log "Removing retained candidate ${NEW_CONTAINER} after guarded Caddy restoration"
  docker rm -f "$NEW_CONTAINER" >>"${LOG_DIR}/retained-candidate-cleanup.log" 2>&1
}

recover_retained_caddy_exposure() {
  local recovery_log="${LOG_DIR}/retained-caddy-recovery.log"

  RETAINED_CADDY_EXPOSURE_RECOVERY=true
  log "Retained Caddy transaction matches ${OLD_UPSTREAM} -> ${NEW_UPSTREAM}; draining and checking refund rollback readiness before any old-generation restoration"
  if ! gate_post_switch_rollback_on_refund_readiness; then
    RETAINED_CADDY_EXPOSURE_RECOVERY=false
    log "ERROR: guarded Caddy recovery is blocked by candidate refund readiness; retaining Caddy transaction, ${NEW_CONTAINER}, and the local release transaction" >&2
    return 1
  fi
  if ! run_blue_green \
    SUB2API_SERVER_WRAPPER_OWNS_CADDY_RECOVERY=true \
    SUB2API_CADDY_SWITCH_RECOVERY_ACTION=restore-after-refund-gate \
    OLD_CONTAINER="$OLD_CONTAINER" \
    NEW_CONTAINER="$NEW_CONTAINER" \
    NEW_IMAGE='' \
    CADDY_UPSTREAM_FROM="$OLD_UPSTREAM" \
    CADDY_UPSTREAM_TO="$NEW_UPSTREAM" \
    PULL_IMAGE=false \
    RUN_BACKUP=false \
    PRECREATE_ONLY=false \
    REMOVE_EXISTING_NEW_CONTAINER=false \
    SUB2API_DUAL_NODE_RUNTIME_ENABLED="$DUAL_NODE_RUNTIME_ENABLED" \
    bash "$BLUE_GREEN_SCRIPT" >"$recovery_log" 2>&1; then
    tail -120 "$recovery_log" >&2 || true
    restore_runtime_traffic_after_post_gate_rollback_failure \
      "guarded Caddy restoration helper failed" || true
    RETAINED_CADDY_EXPOSURE_RECOVERY=false
    log "ERROR: guarded Caddy restoration failed; transaction and target remain for canonical recovery" >&2
    return 1
  fi
  if ! caddy_views_point_uniquely_to_old; then
    RETAINED_CADDY_EXPOSURE_RECOVERY=false
    log "ERROR: guarded Caddy restoration returned but Caddy does not conclusively point at ${OLD_UPSTREAM}; leaving traffic admission draining" >&2
    return 1
  fi
  if [ -e "$CADDY_SWITCH_TRANSACTION_PATH" ] || [ -L "$CADDY_SWITCH_TRANSACTION_PATH" ]; then
    RETAINED_CADDY_EXPOSURE_RECOVERY=false
    log "ERROR: guarded Caddy restoration did not clear its transaction; leaving traffic admission draining" >&2
    return 1
  fi
  if ! run_node_state abort-local >>"${LOG_DIR}/node-state.log" 2>&1; then
    RETAINED_CADDY_EXPOSURE_RECOVERY=false
    log "ERROR: Caddy was restored but node runtime state could not be finalized; runtime guard recover-local is the only remaining canonical finalization path" >&2
    return 1
  fi
  RETAINED_CADDY_EXPOSURE_RECOVERY=false
  cleanup_retained_caddy_candidate || return 1
  log "Guarded retained-Caddy recovery completed"
  return 0
}

validate_external_runtime_contract_before_stale_target_removal() {
  local validation_log="${LOG_DIR}/external-runtime-validation.log"

  if ! run_blue_green \
    VALIDATE_EXTERNAL_RUNTIME_ONLY=true \
    OLD_CONTAINER="$OLD_CONTAINER" \
    NEW_CONTAINER="$NEW_CONTAINER" \
    NEW_IMAGE="$IMAGE" \
    RUN_BACKUP=false \
    PULL_IMAGE=false \
    SUB2API_DUAL_NODE_RUNTIME_ENABLED="$DUAL_NODE_RUNTIME_ENABLED" \
    bash "$BLUE_GREEN_SCRIPT" >"$validation_log" 2>&1; then
    tail -100 "$validation_log" >&2 || true
    die "could not validate external runtime contract before removing stopped inactive target ${NEW_CONTAINER}"
  fi
}

remove_stopped_external_inactive_target() {
  local old_health
  local old_running
  local target_running

  if ! docker inspect "$NEW_CONTAINER" >/dev/null 2>&1; then
    return 0
  fi
  [ "$NEW_CONTAINER" != "$OLD_CONTAINER" ] \
    || die "refusing to remove Caddy-active container $OLD_CONTAINER"

  validate_external_runtime_contract_before_stale_target_removal
  if ! caddy_views_point_uniquely_to_old; then
    die "Caddy views do not conclusively point at ${OLD_UPSTREAM}; retaining stopped inactive target ${NEW_CONTAINER}"
  fi
  # Re-check the current old and target states after all release candidates
  # and Caddy views were verified. A target that started meanwhile is never
  # force-removed, and a degraded old generation is retained as rollback.
  if ! old_running="$(docker inspect "$OLD_CONTAINER" --format '{{.State.Running}}')" \
    || ! old_health="$(docker inspect "$OLD_CONTAINER" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}')"; then
    die "could not re-check active container before removing stopped target ${NEW_CONTAINER}"
  fi
  [ "$old_running" = true ] && [ "$old_health" = healthy ] \
    || die "active container is no longer healthy before removing stopped target ${NEW_CONTAINER}"
  if ! docker inspect "$NEW_CONTAINER" >/dev/null 2>&1; then
    log "Stopped inactive target ${NEW_CONTAINER} disappeared before cleanup"
    return 0
  fi
  if ! target_running="$(docker inspect "$NEW_CONTAINER" --format '{{.State.Running}}')"; then
    die "could not re-check inactive target ${NEW_CONTAINER} before cleanup"
  fi
  [ "$target_running" = false ] \
    || die "inactive target ${NEW_CONTAINER} is no longer stopped; retaining it"

  log "Removing stale stopped inactive target ${NEW_CONTAINER}; Caddy still points at ${OLD_CONTAINER}"
  docker rm "$NEW_CONTAINER" >>"${LOG_DIR}/stale-target-cleanup.log" 2>&1 \
    || die "could not remove stale stopped inactive target ${NEW_CONTAINER}"
}

if [ "$RECOVER_RETAINED_CADDY_EXPOSURE" = true ]; then
  load_retained_caddy_exposure_for_recovery \
    || die "retained Caddy/local transactions are invalid or do not describe the same old/candidate pair"
  if ! recover_retained_caddy_exposure; then
    die "guarded retained-Caddy recovery did not complete"
  fi
  log "Retained Caddy exposure recovered: old=${OLD_CONTAINER} candidate=${NEW_CONTAINER}"
  exit 0
fi

run_node_state bootstrap >>"${LOG_DIR}/node-state.log" \
  || die "could not bootstrap node runtime state"
node_state_status="$(run_node_state status)" \
  || die "could not read node runtime state after bootstrap"
printf '%s\n' "$node_state_status" >>"${LOG_DIR}/node-state.log"
EXPECTED_BACKGROUND_STATE=active
LOCAL_STANDBY_COMMAND=local-standby
if [ "$RELEASE_BACKGROUND_MODE" = preserve-standby ]; then
  EXPECTED_BACKGROUND_STATE=standby
  LOCAL_STANDBY_COMMAND=local-preserve-standby
fi
[ "$node_state_status" = "traffic=accepting active_container=${OLD_CONTAINER} background=${EXPECTED_BACKGROUND_STATE}" ] \
  || die "node runtime state is not safe for a ${RELEASE_BACKGROUND_MODE} local release: ${node_state_status}"
if [ "$DEPENDENCY_MODE" = external ]; then
  # External mode cannot safely reuse a stopped target whose runtime contract
  # may belong to a previous data-service configuration. Node-state preflight
  # (or the legacy transaction-file guard) refuses unfinished local recovery
  # before any stale container object is removed.
  require_no_unfinished_local_transaction_before_stale_target_removal
  remove_stopped_external_inactive_target
fi
if docker inspect "$NEW_CONTAINER" >/dev/null 2>&1; then
  target_running="$(docker inspect "$NEW_CONTAINER" --format '{{.State.Running}}')"
  if [ "$target_running" = "true" ]; then
    die "inactive target ${NEW_CONTAINER} is still running, probably draining a previous release; retry later"
  fi
fi

recent_requests="$(docker logs --since 2m "$OLD_CONTAINER" 2>&1 | grep -c '"component": "http.access"' || true)"
log "Preflight: active=${OLD_CONTAINER} target=${NEW_CONTAINER} recent_requests_2m=${recent_requests}"
log "Preflight: disk_free=$(df -h / | awk 'NR==2 {print $4}') load=$(cut -d' ' -f1-3 /proc/loadavg)"

build_started="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
BUILT_ON_SERVER=false
if [ "$PREBUILT_MODE" = "true" ]; then
  log "Using GitHub-built image ${IMAGE}; production-side compilation is disabled"
  docker image inspect "$IMAGE" >/dev/null 2>&1 || \
    die "GitHub-built image is missing: ${IMAGE}"
elif [ -n "$PREBUILT_IMAGE_PREFIX" ]; then
  PREBUILT_IMAGE="${PREBUILT_IMAGE_PREFIX}${COMMIT}"
  log "Using externally built image ${PREBUILT_IMAGE}; server-side compilation is disabled"
  docker image inspect "$PREBUILT_IMAGE" >/dev/null 2>&1 || \
    die "prebuilt image is missing: ${PREBUILT_IMAGE}"
  docker tag "$PREBUILT_IMAGE" "$IMAGE" || die "failed to tag prebuilt image"
else
  BUILT_ON_SERVER=true
  log "Building ${IMAGE} on the server; detailed output is in ${BUILD_LOG}"
  if ! timeout "$BUILD_TIMEOUT_SECONDS" env DOCKER_BUILDKIT=1 docker build \
    --progress=plain \
    --tag "$IMAGE" \
    --build-arg "GOPROXY=https://goproxy.cn,direct" \
    --build-arg "GOSUMDB=sum.golang.google.cn" \
    --build-arg "NPM_CONFIG_REGISTRY=https://registry.npmmirror.com" \
    --build-arg "BUILD_GOMAXPROCS=${BUILD_GOMAXPROCS}" \
    --build-arg "BUILD_GO_PARALLELISM=${BUILD_GO_PARALLELISM}" \
    --build-arg "BUILD_GO_MEMORY_LIMIT=${BUILD_GO_MEMORY_LIMIT}" \
    --build-arg "COMMIT=${COMMIT}" \
    --build-arg "VERSION=${VERSION}" \
    --build-arg "DATE=${build_started}" \
    --file "${SOURCE_DIR}/Dockerfile" \
    "$SOURCE_DIR" >"$BUILD_LOG" 2>&1; then
    tail -100 "$BUILD_LOG" >&2 || true
    die "Docker build failed"
  fi
fi

docker image inspect "$IMAGE" >/dev/null || die "built image is missing"
log "Image ready: $(docker image inspect "$IMAGE" --format '{{.Id}} {{.Size}} bytes')"

rollback() {
  local rollback_source_running
  local rollback_allow_isolated=false

  ROLLBACK_GATE_PRIOR_TRAFFIC_STATE=""
  log "Attempting automatic rollback to ${OLD_CONTAINER}"
  # The candidate may already have persisted a reviewed refund reservation.
  # Older binaries cannot safely reconcile that state, so keep Caddy on the
  # candidate unless its authenticated readiness endpoint confirms that no
  # entitlement reservation remains after request admission has drained.
  if ! gate_post_switch_rollback_on_refund_readiness; then
    log "ERROR: automatic rollback is blocked by candidate refund readiness; Caddy, ${NEW_CONTAINER}, and the local release transaction are retained. Let the candidate finish refund reconciliation, then resume the same canonical release/recovery transaction; do not use direct Caddy or container changes" >&2
    return 1
  fi
  if ! rollback_source_running="$(docker inspect "$NEW_CONTAINER" --format '{{.State.Running}}')"; then
    restore_runtime_traffic_after_post_gate_rollback_failure \
      "could not inspect failed release container before rollback" || true
    log "ERROR: could not inspect failed release container before rollback: ${NEW_CONTAINER}" >&2
    return 1
  fi
  case "$rollback_source_running" in
    true) ;;
    false) rollback_allow_isolated=true ;;
    *)
      restore_runtime_traffic_after_post_gate_rollback_failure \
        "failed release container has an invalid running state" || true
      log "ERROR: failed release container has an invalid running state: ${NEW_CONTAINER}" >&2
      return 1
      ;;
  esac
  # The helper swaps OLD/NEW for rollback. Preserve the exact compatibility
  # setting from the original active generation (including an absent key),
  # rather than applying the mode of the failed new generation to it. If the
  # failed generation has already stopped, use the helper's audited isolated-
  # old contract; a still-running source retains the normal rollback path.
  run_blue_green \
    OLD_CONTAINER="$NEW_CONTAINER" \
    NEW_CONTAINER="$OLD_CONTAINER" \
    NEW_IMAGE="$OLD_IMAGE" \
    CADDY_UPSTREAM_FROM="$NEW_UPSTREAM" \
    CADDY_UPSTREAM_TO="$OLD_UPSTREAM" \
    PULL_IMAGE=false \
    RUN_BACKUP=false \
    REMOVE_EXISTING_NEW_CONTAINER=false \
    ALLOW_ISOLATED_OLD_CONTAINER="$rollback_allow_isolated" \
    SUB2API_RELEASE_ROUTE_CONTRACT_WARN_ONLY=true \
    SUB2API_RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE=preserve \
    SUB2API_RELEASE_FIXED_EGRESS_PRESERVE_SOURCE_CONTAINER="$OLD_CONTAINER" \
    SUB2API_RELEASE_REAL_REQUEST_PROBE_ENABLED=false \
    SUB2API_DUAL_NODE_RUNTIME_ENABLED="$DUAL_NODE_RUNTIME_ENABLED" \
    bash "$BLUE_GREEN_SCRIPT" >>"${LOG_DIR}/rollback.log" 2>&1 || {
      tail -100 "${LOG_DIR}/rollback.log" >&2 || true
      restore_runtime_traffic_after_post_gate_rollback_failure \
        "automatic rollback helper failed" || true
      log "ERROR: automatic rollback failed; manual intervention is required" >&2
      return 1
    }
  if ! run_node_state abort-local >>"${LOG_DIR}/node-state.log" 2>&1; then
    log "ERROR: Caddy rolled back but node runtime state could not be restored" >&2
    return 1
  fi
  ROLLBACK_GATE_PRIOR_TRAFFIC_STATE=""
  warn_image_route_contract_views
  log "Rollback completed"
}

cleanup_failed_inactive_target() {
  if ! docker inspect "$NEW_CONTAINER" >/dev/null 2>&1; then
    return 0
  fi
  if ! caddy_views_point_uniquely_to_old; then
    log "WARNING: Caddy does not conclusively point at ${OLD_UPSTREAM}; retaining ${NEW_CONTAINER}" >&2
    return 0
  fi

  log "Removing failed inactive target ${NEW_CONTAINER}; Caddy still points at ${OLD_CONTAINER}"
  docker rm -f "$NEW_CONTAINER" >>"${LOG_DIR}/failed-target-cleanup.log" 2>&1 \
    || log "WARNING: could not remove failed inactive target ${NEW_CONTAINER}" >&2
}

run_node_state "$LOCAL_STANDBY_COMMAND" "$NEW_CONTAINER" >>"${LOG_DIR}/node-state.log" \
  || die "could not prepare ${NEW_CONTAINER} background admission state"
log "Switching ${OLD_UPSTREAM} to ${NEW_UPSTREAM} through the existing blue-green script"
if ! run_blue_green \
  OLD_CONTAINER="$OLD_CONTAINER" \
  NEW_CONTAINER="$NEW_CONTAINER" \
  NEW_IMAGE="$IMAGE" \
  CADDY_UPSTREAM_FROM="$OLD_UPSTREAM" \
  CADDY_UPSTREAM_TO="$NEW_UPSTREAM" \
  PULL_IMAGE=false \
  RUN_BACKUP="$SWITCH_RUN_BACKUP" \
  SUB2API_SERVER_WRAPPER_OWNS_CADDY_RECOVERY=true \
  SUB2API_DUAL_NODE_RUNTIME_ENABLED="$DUAL_NODE_RUNTIME_ENABLED" \
  bash "$BLUE_GREEN_SCRIPT" >"$SWITCH_LOG" 2>&1; then
  tail -120 "$SWITCH_LOG" >&2 || true
  if [ -e "$CADDY_SWITCH_TRANSACTION_PATH" ] || [ -L "$CADDY_SWITCH_TRANSACTION_PATH" ]; then
    if retained_caddy_switch_matches_current_release; then
      if ! recover_retained_caddy_exposure; then
        log "ERROR: retained Caddy exposure recovery did not complete; do not abort local state or remove ${NEW_CONTAINER}" >&2
      fi
    else
      log "ERROR: helper left an invalid, legacy, or mismatched Caddy transaction; retaining ${NEW_CONTAINER} and local release state for canonical recovery" >&2
    fi
  elif caddy_views_point_uniquely_to_old; then
    log "Blue-green release failed before the Caddy switch; aborting local node state without rollback"
    if run_node_state abort-local >>"${LOG_DIR}/node-state.log" 2>&1; then
      cleanup_failed_inactive_target
    else
      log "ERROR: Caddy still points at ${OLD_CONTAINER}, but node runtime state could not be restored" >&2
    fi
  else
    if rollback; then
      cleanup_failed_inactive_target
    fi
  fi
  die "blue-green release failed"
fi

public_health_curl_args=(-fsS --noproxy '*' --max-time 20 --retry 3 --retry-delay 2)
if [ -n "$PUBLIC_HEALTH_RESOLVE" ]; then
  public_health_curl_args+=(--resolve "$PUBLIC_HEALTH_RESOLVE")
fi
if ! curl "${public_health_curl_args[@]}" "$PUBLIC_HEALTH_URL" >/dev/null; then
  if rollback; then
    cleanup_failed_inactive_target
  fi
  die "public health check failed after switch"
fi

NEW_HEALTH="$(docker inspect "$NEW_CONTAINER" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}')"
[ "$NEW_HEALTH" = "healthy" ] || {
  if rollback; then
    cleanup_failed_inactive_target
  fi
  die "new container lost health after switch: $NEW_HEALTH"
}

if ! active_config="$(docker exec "$CADDY_CONTAINER" sh -c 'wget -Y off -qO- http://127.0.0.1:2019/config/ 2>/dev/null || curl --noproxy "*" -fsS http://127.0.0.1:2019/config/')"; then
  if rollback; then
    cleanup_failed_inactive_target
  fi
  die "could not read the active Caddy configuration after switch"
fi
printf '%s' "$active_config" | grep -qF "$NEW_UPSTREAM" || {
  if rollback; then
    cleanup_failed_inactive_target
  fi
  die "active Caddy config does not contain $NEW_UPSTREAM"
}
if printf '%s' "$active_config" | grep -qF "$OLD_UPSTREAM"; then
  if rollback; then
    cleanup_failed_inactive_target
  fi
  die "active Caddy config still contains old upstream $OLD_UPSTREAM"
fi
if ! verify_image_route_contract_startup \
  || ! verify_image_route_contract_json "$active_config"; then
  if rollback; then
    cleanup_failed_inactive_target
  fi
  die "Caddy image route contract failed after switch"
fi

# Both release modes keep the candidate fenced during verification. Ordinary
# releases transfer background admission only after every rollback-capable
# gate passes; preserve-standby releases finalize without ever admitting it.
if ! run_node_state commit-local >>"${LOG_DIR}/node-state.log" 2>&1; then
  if rollback; then
    cleanup_failed_inactive_target
  fi
  die "node runtime state activation failed after the Caddy switch"
fi

# The blue-green helper starts a nohup monitor, but this release helper itself
# runs inside a Type=oneshot systemd service. systemd kills remaining processes
# in that service's cgroup when the main process exits, regardless of nohup.
# Start a second, independently managed transient service after all rollback
# gates have passed. It uses its own lock/PID files, so the short-lived helper
# monitor and this persistent monitor can overlap safely.
drain_unit="sub2api-drain-${OLD_CONTAINER}-${RUN_ID}"
drain_unit_log="${LOG_DIR}/drain-unit-launch.log"
log "Starting persistent drain monitor unit ${drain_unit} for ${OLD_CONTAINER}"
if ! systemd-run \
  --quiet \
  --unit="$drain_unit" \
  --collect \
  --description="Drain ${OLD_CONTAINER} after Sub2API release ${RUN_ID}" \
  --property=Type=exec \
  --property=Nice=10 \
  --setenv="APP_DIR=${APP_DIR}" \
  --setenv="DRAIN_CONTAINER=${OLD_CONTAINER}" \
  --setenv="ACTIVE_CONTAINER=${NEW_CONTAINER}" \
  --setenv="REQUIRED_CADDY_UPSTREAM=${NEW_UPSTREAM}" \
  --setenv="FORBIDDEN_CADDY_UPSTREAM=${OLD_UPSTREAM}" \
  --setenv="CADDY_CONTAINER=${CADDY_CONTAINER}" \
  --setenv="CADDY_ACTIVE_CONFIG_PATH=${DRAIN_CADDY_CONFIG_PATH}" \
  --setenv="SUB2API_MAINTENANCE_LOCK_FILE=${MAINTENANCE_LOCK_FILE}" \
  --setenv="INTERVAL_SECONDS=${DRAIN_INTERVAL_SECONDS}" \
  --setenv="ACTIVE_WINDOW_SECONDS=${DRAIN_ACTIVE_WINDOW_SECONDS}" \
  --setenv="RETRY_DELAY_SECONDS=${DRAIN_RETRY_DELAY_SECONDS}" \
  --setenv="MAX_RUNTIME_SECONDS=${DRAIN_MAX_RUNTIME_SECONDS}" \
  --setenv="STOP_DRAIN_CONTAINER=true" \
  --setenv="LOG_FILE=${LOG_DIR}/drain-monitor.log" \
  --setenv="LOCK_FILE=/run/${drain_unit}.lock" \
  --setenv="PID_FILE=/run/${drain_unit}.pid" \
  "$DRAIN_MONITOR_SCRIPT" >"$drain_unit_log" 2>&1; then
  die "could not start persistent drain monitor; ${NEW_CONTAINER} remains active and ${OLD_CONTAINER} was retained for safety"
fi
printf '%s\n' "$drain_unit" >"${LOG_DIR}/drain-unit.name"

app_5xx="$(docker logs --since "$build_started" "$NEW_CONTAINER" 2>&1 | grep -Ec '"status_code":[[:space:]]*5[0-9]{2}' || true)"
app_fatal="$(docker logs --since "$build_started" "$NEW_CONTAINER" 2>&1 | grep -Eic 'panic|fatal|redis.*(error|fail|timeout)|database.*(error|fail)' || true)"
caddy_5xx="$(docker logs --since "$build_started" "$CADDY_CONTAINER" 2>&1 | grep -Ec '"status":[[:space:]]*5[0-9]{2}' || true)"

if [ "$app_fatal" -gt 0 ]; then
  docker logs --since "$build_started" "$NEW_CONTAINER" >"${LOG_DIR}/suspicious-app.log" 2>&1 || true
  log "WARNING: found ${app_fatal} suspicious application log lines; review ${LOG_DIR}/suspicious-app.log"
fi

# Retain active and rollback images.  Only old generated release images that
# are no longer referenced by a container are eligible for removal.
generated_index=0
while IFS= read -r old_tag; do
  [ -n "$old_tag" ] || continue
  generated_index=$((generated_index + 1))
  [ "$generated_index" -le 3 ] && continue
  if docker ps -a --format '{{.Image}}' | grep -qxF "$old_tag"; then
    continue
  fi
  docker image rm "$old_tag" >>"${LOG_DIR}/image-cleanup.log" 2>&1 || true
done < <(docker images --format '{{.Repository}}:{{.Tag}}' | grep '^sub2api:auto-' || true)

if [ "$BUILT_ON_SERVER" = "true" ] \
  && docker buildx prune --help 2>&1 | grep -q -- '--max-used-space'; then
  docker buildx prune --force --max-used-space 8GB >"${LOG_DIR}/cache-cleanup.log" 2>&1 || true
fi

log "Release verified: container=${NEW_CONTAINER} image=${IMAGE} health=${NEW_HEALTH} background_mode=${RELEASE_BACKGROUND_MODE}"
log "Audit counts: app_5xx=${app_5xx} app_fatal=${app_fatal} caddy_5xx=${caddy_5xx}"
log "Disk after release: $(df -h / | awk 'NR==2 {print $5 " used, " $4 " free"}')"
log "Server logs: ${LOG_DIR}"
