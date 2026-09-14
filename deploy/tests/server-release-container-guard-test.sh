#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
SCRIPT="${DEPLOY_DIR}/sub2api-server-release.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-server-release-test.XXXXXX")"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
FAKE_BIN="${TEST_ROOT}/bin"
APP_DIR="${TEST_ROOT}/app"
WORK_ROOT="${TEST_ROOT}/worktrees"
SOURCE_DIR="${WORK_ROOT}/release.case"
DOCKER_CALLS="${TEST_ROOT}/docker-calls.log"
NODE_STATE_CALLS="${TEST_ROOT}/node-state-calls.log"
CURL_CALLS="${TEST_ROOT}/curl-calls.log"
BLUE_GREEN_ENV_LOG="${TEST_ROOT}/blue-green-env.log"
EVENT_LOG="${TEST_ROOT}/events.log"
ROUTE_VERIFIER_CALLS="${TEST_ROOT}/route-verifier-calls.log"
STARTUP_CADDY="${TEST_ROOT}/startup.Caddyfile"
ACTIVE_CADDY="${TEST_ROOT}/active-caddy.json"
LOCAL_TRANSACTION="${TEST_ROOT}/local-release.env"
RETAINED_CADDY_TRANSACTION="${APP_DIR}/.sub2api-blue-green-caddy-transaction.env"
NEW_RUNNING_MARKER="${TEST_ROOT}/new-running.marker"
EXTERNAL_RUNTIME_ENV_FILE="${TEST_ROOT}/external-runtime.env"
EXTERNAL_CA_FILE="${TEST_ROOT}/external-ca.crt"
TRAFFIC_STATE_FILE="${TEST_ROOT}/traffic-state"

cleanup() {
  rm -rf "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local file="$1"
  local expected="$2"
  if ! grep -Fq -- "$expected" "$file"; then
    sed -n '1,160p' "$file" >&2
    fail "expected '${expected}' in ${file}"
  fi
}

assert_not_contains() {
  local file="$1"
  local unexpected="$2"
  if grep -Fq -- "$unexpected" "$file"; then
    sed -n '1,200p' "$file" >&2
    fail "did not expect '${unexpected}' in ${file}"
  fi
}

assert_event_order() {
  local first="$1"
  local second="$2"
  local first_line
  local second_line

  first_line="$(grep -nF -- "$first" "$EVENT_LOG" | head -1 | cut -d: -f1 || true)"
  second_line="$(grep -nF -- "$second" "$EVENT_LOG" | head -1 | cut -d: -f1 || true)"
  [ -n "$first_line" ] && [ -n "$second_line" ] && [ "$first_line" -lt "$second_line" ] \
    || fail "expected '${first}' before '${second}' in ${EVENT_LOG}"
}

file_inode() {
  stat -c '%i' "$1" 2>/dev/null || stat -f '%i' "$1"
}

assert_traffic_state() {
  local expected="$1"
  local actual

  actual="$(tr -d '\r\n' <"$TRAFFIC_STATE_FILE")"
  [ "$actual" = "$expected" ] \
    || fail "expected traffic state ${expected}, got ${actual}"
}

mkdir -p "$FAKE_BIN" "$APP_DIR/scripts" "$SOURCE_DIR"
printf 'FROM scratch\n' >"${SOURCE_DIR}/Dockerfile"
printf 'reverse_proxy sub2api-green:8080\n' >"${APP_DIR}/Caddyfile"
printf 'reverse_proxy sub2api-green:8080\n' >"$STARTUP_CADDY"
printf '{"upstream":"sub2api-green:8080"}\n' >"$ACTIVE_CADDY"
: >"$EXTERNAL_RUNTIME_ENV_FILE"
: >"$EXTERNAL_CA_FILE"
printf 'accepting\n' >"$TRAFFIC_STATE_FILE"
chmod 644 "$TRAFFIC_STATE_FILE"
cat >"${APP_DIR}/scripts/verify_image_route_contract.py" <<'EOF'
#!/usr/bin/env bash
cat >/dev/null
printf '%s\n' "$*" >>"$FAKE_ROUTE_VERIFIER_CALLS"
EOF
chmod +x "${APP_DIR}/scripts/verify_image_route_contract.py"

reset_caddy_views() {
  printf 'reverse_proxy sub2api-green:8080\n' >"${APP_DIR}/Caddyfile"
  printf 'reverse_proxy sub2api-green:8080\n' >"$STARTUP_CADDY"
  printf '{"upstream":"sub2api-green:8080"}\n' >"$ACTIVE_CADDY"
}

cat >"${APP_DIR}/scripts/sub2api-blue-green-release.sh" <<'EOF'
#!/usr/bin/env bash
set -eu
if [ "${VALIDATE_EXTERNAL_RUNTIME_ONLY:-false}" = true ]; then
  if [ -n "${FAKE_EVENT_LOG:-}" ]; then
    printf 'helper-validation old=%s new=%s\n' \
      "${OLD_CONTAINER:-}" "${NEW_CONTAINER:-}" >>"$FAKE_EVENT_LOG"
  fi
  [ "${SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE:-}" = external ] || exit 25
  [ -n "${SUB2API_EXTERNAL_RUNTIME_ENV_FILE:-}" ] || exit 25
  [ -n "${SUB2API_EXTERNAL_CA_FILE:-}" ] || exit 25
  [ "${FAKE_EXTERNAL_VALIDATION_FAIL:-0}" != 1 ] || exit 25
  exit 0
fi
if [ -n "${FAKE_BLUE_GREEN_ENV_LOG:-}" ]; then
  printf 'mode=%s old=%s new=%s backup=%s isolated_old=%s route_contract_warn_only=%s fixed_egress_compatibility=%s preserve_source=%s wrapper_owns_caddy_recovery=%s caddy_recovery_action=%s\n' \
    "${SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE:-}" \
    "${OLD_CONTAINER:-}" \
    "${NEW_CONTAINER:-}" \
    "${RUN_BACKUP:-}" \
    "${ALLOW_ISOLATED_OLD_CONTAINER:-false}" \
    "${SUB2API_RELEASE_ROUTE_CONTRACT_WARN_ONLY:-false}" \
    "${SUB2API_RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE:-}" \
    "${SUB2API_RELEASE_FIXED_EGRESS_PRESERVE_SOURCE_CONTAINER:-}" \
    "${SUB2API_SERVER_WRAPPER_OWNS_CADDY_RECOVERY:-false}" \
    "${SUB2API_CADDY_SWITCH_RECOVERY_ACTION:-normal}" >>"$FAKE_BLUE_GREEN_ENV_LOG"
fi
if [ -n "${FAKE_EVENT_LOG:-}" ]; then
  printf 'helper old=%s new=%s action=%s\n' "${OLD_CONTAINER:-}" "${NEW_CONTAINER:-}" \
    "${SUB2API_CADDY_SWITCH_RECOVERY_ACTION:-normal}" >>"$FAKE_EVENT_LOG"
fi
if [ "${SUB2API_CADDY_SWITCH_RECOVERY_ACTION:-normal}" = restore-after-refund-gate ]; then
  [ "${SUB2API_SERVER_WRAPPER_OWNS_CADDY_RECOVERY:-false}" = true ] || exit 41
  [ -f "${FAKE_CADDY_SWITCH_TRANSACTION:-}" ] || exit 42
  printf 'reverse_proxy %s\n' "$CADDY_UPSTREAM_FROM" >"$FAKE_APP_CADDY"
  printf 'reverse_proxy %s\n' "$CADDY_UPSTREAM_FROM" >"$FAKE_STARTUP_CADDY"
  printf '{"upstream":"%s"}\n' "$CADDY_UPSTREAM_FROM" >"$FAKE_ACTIVE_CADDY"
  rm -f -- "$FAKE_CADDY_SWITCH_TRANSACTION"
  exit 0
fi
if [ "${FAKE_MARK_NEW_RUNNING:-0}" = 1 ] \
  && [ "${OLD_CONTAINER:-}" = sub2api-green ] \
  && [ "${NEW_CONTAINER:-}" = sub2api-blue ]; then
  : >"$FAKE_NEW_RUNNING_MARKER"
fi
if [ "${FAKE_BLUE_GREEN_FAIL_BEFORE_CADDY:-0}" = 1 ]; then
  if [ "${OLD_CONTAINER:-}" = sub2api-blue ]; then
    printf 'old container sub2api-blue is not running; refusing to release\n' >&2
  fi
  exit 23
fi
if [ "${FAKE_ROLLBACK_HELPER_FAIL_WITH_CANDIDATE:-0}" = 1 ] \
  && [ "${OLD_CONTAINER:-}" = sub2api-blue ] \
  && [ "${NEW_CONTAINER:-}" = sub2api-green ]; then
  exit 26
fi
if [ "${FAKE_ROLLBACK_HELPER_FAIL_AFTER_CADDY:-0}" = 1 ] \
  && [ "${OLD_CONTAINER:-}" = sub2api-blue ] \
  && [ "${NEW_CONTAINER:-}" = sub2api-green ]; then
  printf 'reverse_proxy %s\n' "$CADDY_UPSTREAM_TO" >"$FAKE_APP_CADDY"
  printf 'reverse_proxy %s\n' "$CADDY_UPSTREAM_TO" >"$FAKE_STARTUP_CADDY"
  printf '{"upstream":"%s"}\n' "$CADDY_UPSTREAM_TO" >"$FAKE_ACTIVE_CADDY"
  exit 27
fi
if [ "${FAKE_UPDATE_CADDY:-0}" = 1 ] \
  || [ "${FAKE_BLUE_GREEN_FAIL_AFTER_CADDY:-0}" = 1 ]; then
  printf 'reverse_proxy %s\n' "$CADDY_UPSTREAM_TO" >"$FAKE_APP_CADDY"
  printf 'reverse_proxy %s\n' "$CADDY_UPSTREAM_TO" >"$FAKE_STARTUP_CADDY"
  printf '{"upstream":"%s"}\n' "$CADDY_UPSTREAM_TO" >"$FAKE_ACTIVE_CADDY"
fi
if [ "${FAKE_BLUE_GREEN_FAIL_AFTER_CADDY:-0}" = 1 ] \
  && [ "${OLD_CONTAINER:-}" = sub2api-green ]; then
  exit 24
fi
if [ "${FAKE_RETAINED_CADDY_EXPOSURE_WITH_OLD_VIEWS:-0}" = 1 ] \
  && [ "${OLD_CONTAINER:-}" = sub2api-green ] \
  && [ "${NEW_CONTAINER:-}" = sub2api-blue ]; then
  {
    printf 'RECOVERY_OWNER=server-wrapper\n'
    printf 'LIVE_RELOAD_ATTEMPTED=true\n'
    printf 'UPSTREAM_FROM=%s\n' "$CADDY_UPSTREAM_FROM"
    printf 'UPSTREAM_TO=%s\n' "$CADDY_UPSTREAM_TO"
  } >"$FAKE_CADDY_SWITCH_TRANSACTION"
  chmod 600 "$FAKE_CADDY_SWITCH_TRANSACTION"
  exit 28
fi
EOF
printf '#!/usr/bin/env bash\nexit 0\n' >"${APP_DIR}/scripts/sub2api-drain-monitor.sh"
cat >"${APP_DIR}/scripts/sub2api-node-state.sh" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >>"$FAKE_NODE_STATE_CALLS"
case "${1:-}" in
  status)
    printf 'traffic=%s active_container=sub2api-green background=%s\n' \
      "$(tr -d '\r\n' <"$FAKE_TRAFFIC_STATE")" "${FAKE_NODE_STATE_BACKGROUND:-active}"
    ;;
  preflight)
    [ ! -e "$FAKE_LOCAL_TRANSACTION" ] \
      || { printf 'ERROR: an unfinished local release transaction exists\n' >&2; exit 64; }
    ;;
  local-standby|local-preserve-standby)
    {
      printf 'state=local-switching\n'
      printf 'previous=sub2api-green\n'
      printf 'candidate=%s\n' "$2"
      printf 'final_background=%s\n' active
    } >"$FAKE_LOCAL_TRANSACTION"
    chmod 600 "$FAKE_LOCAL_TRANSACTION"
    ;;
  commit-local|abort-local)
    rm -f -- "$FAKE_LOCAL_TRANSACTION"
    printf 'accepting\n' >"$FAKE_TRAFFIC_STATE"
    ;;
esac
EOF
chmod +x \
  "${APP_DIR}/scripts/sub2api-blue-green-release.sh" \
  "${APP_DIR}/scripts/sub2api-drain-monitor.sh" \
  "${APP_DIR}/scripts/sub2api-node-state.sh"

cat >"${FAKE_BIN}/docker" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_DOCKER_CALLS"
command_name="${1:-}"
case "$command_name" in
  inspect)
    container_name="${2:-}"
    format=""
    if [ "${3:-}" = "--format" ]; then
      format="${4:-}"
    fi
    case "$container_name" in
      sub2api-green|sub2api-blue|sub2api) ;;
      *) exit 1 ;;
    esac
    case "$format" in
      *State.Running*)
        if [ "$container_name" = sub2api-blue ] \
          && [ "${FAKE_ROLLBACK_SOURCE_INSPECT_FAIL:-0}" = 1 ] \
          && [ -n "${FAKE_EVENT_LOG:-}" ] \
          && grep -Fq 'refund-rollback-readiness status=200' "$FAKE_EVENT_LOG"; then
          exit 67
        fi
        case "$container_name" in
          sub2api-green|sub2api) printf 'true\n' ;;
          sub2api-blue)
            if [ -e "${FAKE_NEW_RUNNING_MARKER:-}" ]; then printf 'true\n'; else printf 'false\n'; fi
            ;;
        esac
        ;;
      *State.Health*) printf 'healthy\n' ;;
      *Config.Image*) printf 'sub2api:auto-old\n' ;;
    esac
    ;;
  image)
    [ "${2:-}" = "inspect" ] || exit 1
    exit 0
    ;;
  tag)
    exit 0
    ;;
  logs)
    exit 0
    ;;
  exec)
    case "$*" in
      *SUB2API_ROLLBACK_GATE_PATH=/internal/livez*)
        case "$*" in
          *curl*) exit 65 ;;
          *wget*) ;;
          *) exit 65 ;;
        esac
        case "$*" in
          *X-Monitor-Token:*) ;;
          *) exit 66 ;;
        esac
        [ -z "${FAKE_EVENT_LOG:-}" ] || printf 'refund-rollback-livez in_flight=%s\n' \
          "${FAKE_REFUND_ROLLBACK_IN_FLIGHT:-0}" >>"$FAKE_EVENT_LOG"
        [ "${FAKE_REFUND_ROLLBACK_LIVEZ_UNREACHABLE:-0}" != 1 ] || exit 63
        printf '{"live":true,"in_flight_requests":%s}\n' \
          "${FAKE_REFUND_ROLLBACK_IN_FLIGHT:-0}"
        ;;
      *SUB2API_ROLLBACK_GATE_PATH=/internal/refund-rollback-readiness*)
        case "$*" in
          *curl*) exit 65 ;;
          *wget*) ;;
          *) exit 65 ;;
        esac
        case "$*" in
          *X-Monitor-Token:*) ;;
          *) exit 66 ;;
        esac
        if [ "${FAKE_REFUND_ROLLBACK_READINESS_UNREACHABLE:-0}" = 1 ]; then
          [ -z "${FAKE_EVENT_LOG:-}" ] || printf 'refund-rollback-readiness status=unreachable\n' \
            >>"$FAKE_EVENT_LOG"
          exit 64
        fi
        [ -z "${FAKE_EVENT_LOG:-}" ] || printf 'refund-rollback-readiness status=%s\n' \
          "${FAKE_REFUND_ROLLBACK_READINESS_STATUS:-200}" >>"$FAKE_EVENT_LOG"
        case "${FAKE_REFUND_ROLLBACK_READINESS_STATUS:-200}" in
          2??)
            if [ "${FAKE_REFUND_ROLLBACK_READINESS_BODY_SET:-false}" = true ]; then
              printf '%s' "${FAKE_REFUND_ROLLBACK_READINESS_BODY:-}"
            else
              printf '%s\n' '{"ready":true,"entitlement_reserved_reviewed_pending_count":0}'
            fi
            ;;
          *) exit 64 ;;
        esac
        ;;
      *caddy\ adapt*)
        printf '{}\n'
        ;;
      *CADDY_CHECK_PATH=*)
        [ "${FAKE_STARTUP_CADDY_FAIL:-0}" != 1 ] || exit 61
        cat "$FAKE_STARTUP_CADDY"
        ;;
      *127.0.0.1:2019/config*)
        [ "${FAKE_ACTIVE_CADDY_FAIL:-0}" != 1 ] || exit 62
        cat "$FAKE_ACTIVE_CADDY"
        ;;
      *) exit 1 ;;
    esac
    ;;
  rm)
    if [ "${2:-}" = "-f" ]; then
      target_name="${3:-}"
    else
      target_name="${2:-}"
    fi
    [ "$target_name" = sub2api-blue ] || exit 1
    if [ -n "${FAKE_EVENT_LOG:-}" ]; then
      printf 'docker-rm %s\n' "$*" >>"$FAKE_EVENT_LOG"
    fi
    exit 0
    ;;
  build)
    exit 71
    ;;
  *)
    exit 1
    ;;
esac
EOF
chmod +x "${FAKE_BIN}/docker"

cat >"${FAKE_BIN}/curl" <<'EOF'
#!/usr/bin/env bash
[ -z "${FAKE_CURL_CALLS:-}" ] || printf '%s\n' "$*" >>"$FAKE_CURL_CALLS"
[ "${FAKE_CURL_SUCCESS:-0}" = 1 ]
EOF
chmod +x "${FAKE_BIN}/curl"

cat >"${FAKE_BIN}/df" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
  --output=avail)
    printf 'Avail\n99999999999\n'
    ;;
  -h)
    printf 'Filesystem Size Used Avail Capacity Mounted\nfake 100G 1G 99G 1%% /\n'
    ;;
  *)
    exit 1
    ;;
esac
EOF
chmod +x "${FAKE_BIN}/df"

cat >"${FAKE_BIN}/cut" <<'EOF'
#!/usr/bin/env bash
printf '0.01 0.01 0.01\n'
EOF
chmod +x "${FAKE_BIN}/cut"

cat >"${FAKE_BIN}/flock" <<'EOF'
#!/usr/bin/env bash
if [ -n "${FAKE_FLOCK_COUNT_FILE:-}" ]; then
  count=0
  if [ -r "$FAKE_FLOCK_COUNT_FILE" ]; then
    count="$(cat "$FAKE_FLOCK_COUNT_FILE")"
  fi
  count=$((count + 1))
  printf '%s\n' "$count" >"$FAKE_FLOCK_COUNT_FILE"
  if [ -n "${FAKE_FLOCK_FAIL_ON_CALL:-}" ] \
    && [ "$count" -eq "$FAKE_FLOCK_FAIL_ON_CALL" ]; then
    exit 1
  fi
fi
exit 0
EOF
chmod +x "${FAKE_BIN}/flock"

cat >"${FAKE_BIN}/timeout" <<'EOF'
#!/usr/bin/env bash
shift
exec "$@"
EOF
chmod +x "${FAKE_BIN}/timeout"

cat >"${FAKE_BIN}/systemd-run" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "${FAKE_BIN}/systemd-run"

run_release() {
  env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_DOCKER_CALLS="$DOCKER_CALLS" \
    FAKE_NODE_STATE_CALLS="$NODE_STATE_CALLS" \
    FAKE_CURL_CALLS="$CURL_CALLS" \
    FAKE_APP_CADDY="${APP_DIR}/Caddyfile" \
    FAKE_STARTUP_CADDY="$STARTUP_CADDY" \
    FAKE_ACTIVE_CADDY="$ACTIVE_CADDY" \
    FAKE_LOCAL_TRANSACTION="$LOCAL_TRANSACTION" \
    FAKE_CADDY_SWITCH_TRANSACTION="$RETAINED_CADDY_TRANSACTION" \
    FAKE_TRAFFIC_STATE="$TRAFFIC_STATE_FILE" \
    FAKE_BLUE_GREEN_ENV_LOG="$BLUE_GREEN_ENV_LOG" \
    FAKE_EVENT_LOG="$EVENT_LOG" \
    FAKE_ROUTE_VERIFIER_CALLS="$ROUTE_VERIFIER_CALLS" \
    FAKE_MARK_NEW_RUNNING="${FAKE_MARK_NEW_RUNNING:-0}" \
    FAKE_NEW_RUNNING_MARKER="$NEW_RUNNING_MARKER" \
    FAKE_NODE_STATE_BACKGROUND="${FAKE_NODE_STATE_BACKGROUND:-active}" \
    FAKE_FLOCK_COUNT_FILE="${FAKE_FLOCK_COUNT_FILE:-}" \
    FAKE_FLOCK_FAIL_ON_CALL="${FAKE_FLOCK_FAIL_ON_CALL:-}" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_WORK_ROOT="$WORK_ROOT" \
    SUB2API_RELEASE_LOG_DIR="${TEST_ROOT}/logs" \
    SUB2API_RELEASE_LOCK_FILE="${TEST_ROOT}/release.lock" \
    SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS="${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-1}" \
    SUB2API_MAINTENANCE_LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-${TEST_ROOT}/maintenance.lock}" \
    SUB2API_TRAFFIC_STATE_FILE_HOST="$TRAFFIC_STATE_FILE" \
    SUB2API_LOCAL_RELEASE_STATE_FILE_HOST="$LOCAL_TRANSACTION" \
    SUB2API_PUBLIC_HEALTH_RESOLVE="${SUB2API_PUBLIC_HEALTH_RESOLVE:-example.invalid:443:192.0.2.10}" \
    SUB2API_RELEASE_MIN_FREE_BYTES=1 \
    SUB2API_RELEASE_BUILD_TIMEOUT_SECONDS=30 \
    SUB2API_RELEASE_BUILD_GOMAXPROCS=1 \
    SUB2API_RELEASE_BUILD_GO_PARALLELISM=1 \
    SUB2API_RELEASE_BUILD_GO_MEMORY_LIMIT=768MiB \
    SUB2API_RELEASE_ALLOW_PREEXISTING_DRAINING_CONTAINER="${ALLOW_DRAINING:-false}" \
    SUB2API_DUAL_NODE_RUNTIME_ENABLED=true \
    SUB2API_RELEASE_BACKGROUND_MODE="${RELEASE_BACKGROUND_MODE:-activate}" \
    SUB2API_RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE="${RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE:-preserve}" \
    /bin/bash "$SCRIPT" \
      "$SOURCE_DIR" \
      'sub2api:auto-test' \
      'abc123' \
      '0.1.test' \
      'https://example.invalid/health' \
      'guard-test'
}

run_github_prebuilt_release() {
  env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_DOCKER_CALLS="$DOCKER_CALLS" \
    FAKE_NODE_STATE_CALLS="$NODE_STATE_CALLS" \
    FAKE_CURL_CALLS="$CURL_CALLS" \
    FAKE_APP_CADDY="${APP_DIR}/Caddyfile" \
    FAKE_STARTUP_CADDY="$STARTUP_CADDY" \
    FAKE_ACTIVE_CADDY="$ACTIVE_CADDY" \
    FAKE_LOCAL_TRANSACTION="$LOCAL_TRANSACTION" \
    FAKE_CADDY_SWITCH_TRANSACTION="$RETAINED_CADDY_TRANSACTION" \
    FAKE_TRAFFIC_STATE="$TRAFFIC_STATE_FILE" \
    FAKE_BLUE_GREEN_ENV_LOG="$BLUE_GREEN_ENV_LOG" \
    FAKE_EVENT_LOG="$EVENT_LOG" \
    FAKE_ROUTE_VERIFIER_CALLS="$ROUTE_VERIFIER_CALLS" \
    FAKE_MARK_NEW_RUNNING="${FAKE_MARK_NEW_RUNNING:-0}" \
    FAKE_NEW_RUNNING_MARKER="$NEW_RUNNING_MARKER" \
    FAKE_NODE_STATE_BACKGROUND="${FAKE_NODE_STATE_BACKGROUND:-active}" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_WORK_ROOT="$WORK_ROOT" \
    SUB2API_RELEASE_LOG_DIR="${TEST_ROOT}/logs" \
    SUB2API_RELEASE_LOCK_FILE="${TEST_ROOT}/release.lock" \
    SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS="${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-1}" \
    SUB2API_MAINTENANCE_LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-${TEST_ROOT}/maintenance.lock}" \
    SUB2API_TRAFFIC_STATE_FILE_HOST="$TRAFFIC_STATE_FILE" \
    SUB2API_LOCAL_RELEASE_STATE_FILE_HOST="$LOCAL_TRANSACTION" \
    SUB2API_PUBLIC_HEALTH_RESOLVE="${SUB2API_PUBLIC_HEALTH_RESOLVE:-example.invalid:443:192.0.2.10}" \
    SUB2API_RELEASE_MIN_FREE_BYTES=1 \
    SUB2API_RELEASE_ALLOW_PREEXISTING_DRAINING_CONTAINER="${ALLOW_DRAINING:-false}" \
    SUB2API_DUAL_NODE_RUNTIME_ENABLED=true \
    SUB2API_RELEASE_BACKGROUND_MODE="${RELEASE_BACKGROUND_MODE:-activate}" \
    SUB2API_RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE="${RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE:-preserve}" \
    /bin/bash "$SCRIPT" \
      --prebuilt \
      'sub2api:auto-test' \
      'abc123' \
      '0.1.test' \
      'https://example.invalid/health' \
      'github-prebuilt-test'
}

run_external_github_prebuilt_release() {
  env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_DOCKER_CALLS="$DOCKER_CALLS" \
    FAKE_NODE_STATE_CALLS="$NODE_STATE_CALLS" \
    FAKE_CURL_CALLS="$CURL_CALLS" \
    FAKE_APP_CADDY="${APP_DIR}/Caddyfile" \
    FAKE_STARTUP_CADDY="$STARTUP_CADDY" \
    FAKE_ACTIVE_CADDY="$ACTIVE_CADDY" \
    FAKE_LOCAL_TRANSACTION="$LOCAL_TRANSACTION" \
    FAKE_CADDY_SWITCH_TRANSACTION="$RETAINED_CADDY_TRANSACTION" \
    FAKE_TRAFFIC_STATE="$TRAFFIC_STATE_FILE" \
    FAKE_BLUE_GREEN_ENV_LOG="$BLUE_GREEN_ENV_LOG" \
    FAKE_EVENT_LOG="$EVENT_LOG" \
    FAKE_ROUTE_VERIFIER_CALLS="$ROUTE_VERIFIER_CALLS" \
    FAKE_MARK_NEW_RUNNING="${FAKE_MARK_NEW_RUNNING:-0}" \
    FAKE_NEW_RUNNING_MARKER="$NEW_RUNNING_MARKER" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_WORK_ROOT="$WORK_ROOT" \
    SUB2API_RELEASE_LOG_DIR="${TEST_ROOT}/logs" \
    SUB2API_RELEASE_LOCK_FILE="${TEST_ROOT}/release.lock" \
    SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1 \
    SUB2API_MAINTENANCE_LOCK_FILE="${TEST_ROOT}/maintenance.lock" \
    SUB2API_TRAFFIC_STATE_FILE_HOST="$TRAFFIC_STATE_FILE" \
    SUB2API_PUBLIC_HEALTH_RESOLVE="${SUB2API_PUBLIC_HEALTH_RESOLVE:-example.invalid:443:192.0.2.10}" \
    SUB2API_RELEASE_MIN_FREE_BYTES=1 \
    SUB2API_RELEASE_ALLOW_PREEXISTING_DRAINING_CONTAINER="${ALLOW_DRAINING:-false}" \
    SUB2API_DUAL_NODE_RUNTIME_ENABLED="${DUAL_NODE_RUNTIME_ENABLED:-true}" \
    SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE=external \
    SUB2API_EXTERNAL_RUNTIME_ENV_FILE="$EXTERNAL_RUNTIME_ENV_FILE" \
    SUB2API_EXTERNAL_CA_FILE="$EXTERNAL_CA_FILE" \
    SUB2API_LOCAL_RELEASE_STATE_FILE_HOST="$LOCAL_TRANSACTION" \
    SUB2API_RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE="${RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE:-preserve}" \
    /bin/bash "$SCRIPT" \
      --prebuilt \
      'sub2api:auto-test' \
      'abc123' \
      '0.1.test' \
      'https://example.invalid/health' \
      'external-runtime-test'
}

run_retained_caddy_recovery() {
  env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_DOCKER_CALLS="$DOCKER_CALLS" \
    FAKE_NODE_STATE_CALLS="$NODE_STATE_CALLS" \
    FAKE_CURL_CALLS="$CURL_CALLS" \
    FAKE_APP_CADDY="${APP_DIR}/Caddyfile" \
    FAKE_STARTUP_CADDY="$STARTUP_CADDY" \
    FAKE_ACTIVE_CADDY="$ACTIVE_CADDY" \
    FAKE_LOCAL_TRANSACTION="$LOCAL_TRANSACTION" \
    FAKE_CADDY_SWITCH_TRANSACTION="$RETAINED_CADDY_TRANSACTION" \
    FAKE_TRAFFIC_STATE="$TRAFFIC_STATE_FILE" \
    FAKE_BLUE_GREEN_ENV_LOG="$BLUE_GREEN_ENV_LOG" \
    FAKE_EVENT_LOG="$EVENT_LOG" \
    FAKE_ROUTE_VERIFIER_CALLS="$ROUTE_VERIFIER_CALLS" \
    FAKE_MARK_NEW_RUNNING="${FAKE_MARK_NEW_RUNNING:-0}" \
    FAKE_NEW_RUNNING_MARKER="$NEW_RUNNING_MARKER" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_WORK_ROOT="$WORK_ROOT" \
    SUB2API_RELEASE_LOG_DIR="${TEST_ROOT}/logs" \
    SUB2API_RELEASE_LOCK_FILE="${TEST_ROOT}/release.lock" \
    SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1 \
    SUB2API_MAINTENANCE_LOCK_FILE="${TEST_ROOT}/maintenance.lock" \
    SUB2API_TRAFFIC_STATE_FILE_HOST="$TRAFFIC_STATE_FILE" \
    SUB2API_LOCAL_RELEASE_STATE_FILE_HOST="$LOCAL_TRANSACTION" \
    SUB2API_RELEASE_MIN_FREE_BYTES=1 \
    SUB2API_DUAL_NODE_RUNTIME_ENABLED=true \
    SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE=external \
    SUB2API_EXTERNAL_RUNTIME_ENV_FILE="$EXTERNAL_RUNTIME_ENV_FILE" \
    SUB2API_EXTERNAL_CA_FILE="$EXTERNAL_CA_FILE" \
    /bin/bash "$SCRIPT" --recover-retained-caddy-exposure
}

reset_release_case() {
  reset_caddy_views
  rm -f -- "$LOCAL_TRANSACTION" "$RETAINED_CADDY_TRANSACTION" "$NEW_RUNNING_MARKER"
  printf 'accepting\n' >"$TRAFFIC_STATE_FILE"
  : >"$DOCKER_CALLS"
  : >"$NODE_STATE_CALLS"
  : >"$CURL_CALLS"
  : >"$BLUE_GREEN_ENV_LOG"
  : >"$EVENT_LOG"
  : >"$ROUTE_VERIFIER_CALLS"
}

maintenance_output="${TEST_ROOT}/maintenance-lock.log"
flock_count_file="${TEST_ROOT}/flock-count"
if FAKE_FLOCK_COUNT_FILE="$flock_count_file" \
  FAKE_FLOCK_FAIL_ON_CALL=2 \
  run_release >"$maintenance_output" 2>&1; then
  fail 'maintenance lock contention was accepted'
fi
assert_contains "$maintenance_output" \
  'production maintenance or runtime recovery is already running'
if [ -s "$DOCKER_CALLS" ]; then
  fail 'Docker was inspected before the maintenance lock was acquired'
fi

# A safe-looking second private lock must not let the release path split away
# from the certificate/runtime maintenance domain in production mode.
SECOND_SAFE_LOCK="${TEST_ROOT}/second-safe/private/maintenance.lock"
: >"$DOCKER_CALLS"
if SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=0 \
  SUB2API_MAINTENANCE_LOCK_FILE="$SECOND_SAFE_LOCK" \
  run_github_prebuilt_release >"${TEST_ROOT}/production-noncanonical-lock.out" 2>&1; then
  fail 'server release accepted a production noncanonical maintenance lock path'
fi
assert_contains "${TEST_ROOT}/production-noncanonical-lock.out" \
  'maintenance lock path must be the canonical /run/sub2api-maintenance/sub2api-maintenance.lock'
[ ! -e "${SECOND_SAFE_LOCK%/*}" ] \
  || fail 'server release created a noncanonical lock parent before rejection'
if [ -s "$DOCKER_CALLS" ]; then
  fail 'server release inspected Docker before rejecting a noncanonical lock path'
fi

: >"$DOCKER_CALLS"
printf 'STATUS=staged\n' >"${APP_DIR}/.gcp-tw-caddy-transaction.env"
if run_github_prebuilt_release >"${TEST_ROOT}/caddy-transaction.log" 2>&1; then
  fail 'server release accepted an unfinished Caddy listener transaction'
fi
assert_contains "${TEST_ROOT}/caddy-transaction.log" \
  'commit or rollback it before a production release'
[ ! -s "$DOCKER_CALLS" ] || fail 'Docker was touched before the Caddy transaction guard'
rm -f "${APP_DIR}/.gcp-tw-caddy-transaction.env"

: >"$DOCKER_CALLS"
printf 'BEFORE_SHA=test\n' >"${APP_DIR}/.cf-opt-totools-caddy.env"
if run_github_prebuilt_release >"${TEST_ROOT}/customer-host-transaction.log" 2>&1; then
  fail 'server release accepted an unfinished customer Host transaction'
fi
assert_contains "${TEST_ROOT}/customer-host-transaction.log" \
  'commit or rollback it before a production release'
[ ! -s "$DOCKER_CALLS" ] || fail 'Docker was touched before the customer Host transaction guard'
rm -f "${APP_DIR}/.cf-opt-totools-caddy.env"

: >"$DOCKER_CALLS"
printf 'BEFORE_SHA=test\n' >"${APP_DIR}/.sub2api-blue-green-caddy-transaction.env"
if run_github_prebuilt_release >"${TEST_ROOT}/blue-green-caddy-transaction.log" 2>&1; then
  fail 'server release accepted an unfinished blue-green Caddy transaction'
fi
assert_contains "${TEST_ROOT}/blue-green-caddy-transaction.log" \
  'recover it before a production release'
[ ! -s "$DOCKER_CALLS" ] || fail 'Docker was touched before the blue-green Caddy transaction guard'
rm -f "${APP_DIR}/.sub2api-blue-green-caddy-transaction.env"

: >"$DOCKER_CALLS"
resolve_mismatch_output="${TEST_ROOT}/resolve-mismatch.log"
if SUB2API_PUBLIC_HEALTH_RESOLVE='peer.invalid:443:192.0.2.10' \
  run_github_prebuilt_release >"$resolve_mismatch_output" 2>&1; then
  fail 'server release accepted a health resolve override for a peer hostname'
fi

: >"$DOCKER_CALLS"
invalid_background_mode_output="${TEST_ROOT}/invalid-background-mode.log"
if RELEASE_BACKGROUND_MODE=unexpected \
  run_github_prebuilt_release >"$invalid_background_mode_output" 2>&1; then
  fail 'server release accepted an unsupported background mode'
fi
assert_contains "$invalid_background_mode_output" \
  'SUB2API_RELEASE_BACKGROUND_MODE must be activate or preserve-standby'
if [ -s "$DOCKER_CALLS" ]; then
  fail 'Docker was inspected before background-mode validation'
fi

: >"$DOCKER_CALLS"
invalid_fixed_egress_mode_output="${TEST_ROOT}/invalid-fixed-egress-mode.log"
if RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE=unexpected \
  run_github_prebuilt_release >"$invalid_fixed_egress_mode_output" 2>&1; then
  fail 'server release accepted an unsupported fixed-egress compatibility mode'
fi
assert_contains "$invalid_fixed_egress_mode_output" \
  'SUB2API_RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE must be preserve, true, or false'
if [ -s "$DOCKER_CALLS" ]; then
  fail 'Docker was inspected before fixed-egress compatibility-mode validation'
fi
assert_contains "$resolve_mismatch_output" 'host/port must match SUB2API_PUBLIC_HEALTH_URL'
if [ -s "$DOCKER_CALLS" ]; then
  fail 'Docker was inspected before health resolve validation'
fi

reset_release_case
invalid_mode_output="${TEST_ROOT}/invalid-dependency-mode.log"
if ALLOW_DRAINING=true SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE=unexpected \
  run_github_prebuilt_release >"$invalid_mode_output" 2>&1; then
  fail 'server release accepted an unsupported dependency mode'
fi
assert_contains "$invalid_mode_output" \
  'SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE must be local or external'
if [ -s "$DOCKER_CALLS" ]; then
  fail 'Docker was inspected before dependency-mode validation'
fi

strict_output="${TEST_ROOT}/strict.log"
if run_release >"$strict_output" 2>&1; then
  fail 'running inactive container was accepted by default'
fi
assert_contains "$strict_output" 'pre-existing inactive container(s) are still running: sub2api'
assert_contains "$strict_output" 'they can consume shared background queues'
if grep -Fq -- 'build ' "$DOCKER_CALLS"; then
  fail 'image build started before the inactive-container guard'
fi

: >"$DOCKER_CALLS"
override_output="${TEST_ROOT}/override.log"
if ALLOW_DRAINING=true run_release >"$override_output" 2>&1; then
  fail 'fake image build unexpectedly succeeded'
fi
assert_contains "$override_output" 'Building sub2api:auto-test'
assert_contains "$DOCKER_CALLS" 'build --progress=plain'
assert_contains "$DOCKER_CALLS" '--build-arg BUILD_GOMAXPROCS=1'
assert_contains "$DOCKER_CALLS" '--build-arg BUILD_GO_PARALLELISM=1'
assert_contains "$DOCKER_CALLS" '--build-arg BUILD_GO_MEMORY_LIMIT=768MiB'

: >"$DOCKER_CALLS"
prebuilt_output="${TEST_ROOT}/prebuilt.log"
if ALLOW_DRAINING=true SUB2API_RELEASE_PREBUILT_IMAGE_PREFIX='sub2api:prebuilt-' run_release >"$prebuilt_output" 2>&1; then
  fail 'fake prebuilt release unexpectedly succeeded'
fi
assert_contains "$prebuilt_output" 'Using externally built image sub2api:prebuilt-abc123'
assert_contains "$DOCKER_CALLS" 'image inspect sub2api:prebuilt-abc123'
assert_contains "$DOCKER_CALLS" 'tag sub2api:prebuilt-abc123 sub2api:auto-test'
if grep -Fq -- 'build ' "$DOCKER_CALLS"; then
  fail 'server-side image build ran despite a prebuilt image'
fi

: >"$DOCKER_CALLS"
github_prebuilt_output="${TEST_ROOT}/github-prebuilt.log"
if ALLOW_DRAINING=true run_github_prebuilt_release >"$github_prebuilt_output" 2>&1; then
  fail 'fake GitHub-prebuilt release unexpectedly succeeded'
fi
assert_contains "$github_prebuilt_output" \
  'Using GitHub-built image sub2api:auto-test; production-side compilation is disabled'
assert_contains "$DOCKER_CALLS" 'image inspect sub2api:auto-test'
if grep -Fq -- 'build ' "$DOCKER_CALLS"; then
  fail 'production-side image build ran in explicit --prebuilt mode'
fi

reset_release_case
rollback_cleanup_output="${TEST_ROOT}/rollback-cleanup.log"
if ALLOW_DRAINING=true FAKE_UPDATE_CADDY=1 \
  run_github_prebuilt_release >"$rollback_cleanup_output" 2>&1; then
  fail 'fake release unexpectedly passed a failing public health check'
fi
assert_contains "$rollback_cleanup_output" 'Rollback completed'
assert_contains "$rollback_cleanup_output" 'Removing failed inactive target sub2api-blue'
assert_contains "$DOCKER_CALLS" 'rm -f sub2api-blue'
assert_contains "$NODE_STATE_CALLS" 'bootstrap'
assert_contains "$NODE_STATE_CALLS" 'local-standby sub2api-blue'
assert_contains "$NODE_STATE_CALLS" 'abort-local'
assert_contains "$BLUE_GREEN_ENV_LOG" \
  'mode=local old=sub2api-green new=sub2api-blue backup=true'
assert_contains "$BLUE_GREEN_ENV_LOG" \
  'mode=local old=sub2api-blue new=sub2api-green backup=false'
assert_contains "$BLUE_GREEN_ENV_LOG" \
  'mode=local old=sub2api-blue new=sub2api-green backup=false isolated_old=true route_contract_warn_only=true fixed_egress_compatibility=preserve preserve_source=sub2api-green'
assert_contains "$BLUE_GREEN_ENV_LOG" \
  'route_contract_warn_only=true fixed_egress_compatibility=preserve preserve_source=sub2api-green'

# If the failed new generation is still running, rollback must retain the
# normal blue-green source contract rather than forcing isolated-old mode.
reset_release_case
running_source_rollback_output="${TEST_ROOT}/running-source-rollback.log"
if ALLOW_DRAINING=true FAKE_UPDATE_CADDY=1 FAKE_MARK_NEW_RUNNING=1 \
  run_github_prebuilt_release >"$running_source_rollback_output" 2>&1; then
  fail 'fake release unexpectedly passed a failing public health check'
fi
assert_contains "$running_source_rollback_output" 'Rollback completed'
assert_contains "$BLUE_GREEN_ENV_LOG" \
  'mode=local old=sub2api-blue new=sub2api-green backup=false isolated_old=false route_contract_warn_only=true fixed_egress_compatibility=preserve preserve_source=sub2api-green'
assert_contains "$BLUE_GREEN_ENV_LOG" \
  'isolated_old=false route_contract_warn_only=true fixed_egress_compatibility=preserve preserve_source=sub2api-green'

reset_release_case
successful_release_output="${TEST_ROOT}/successful-release.log"
if ! ALLOW_DRAINING=true FAKE_CURL_SUCCESS=1 FAKE_UPDATE_CADDY=1 \
  RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE=true \
  run_github_prebuilt_release >"$successful_release_output" 2>&1; then
  sed -n '1,200p' "$successful_release_output" >&2
  fail 'fake verified release did not complete'
fi
assert_contains "$NODE_STATE_CALLS" 'local-standby sub2api-blue'
assert_contains "$NODE_STATE_CALLS" 'commit-local'
assert_contains "$CURL_CALLS" '--resolve example.invalid:443:192.0.2.10'
assert_contains "$CURL_CALLS" '--noproxy *'
assert_contains "$BLUE_GREEN_ENV_LOG" 'fixed_egress_compatibility=true'
if grep -Fq -- 'abort-local' "$NODE_STATE_CALLS"; then
  fail 'successful release invoked node-state abort'
fi
route_verifier_calls="$(wc -l <"$ROUTE_VERIFIER_CALLS" | tr -d '[:space:]')"
[ "$route_verifier_calls" -ge 4 ] \
  || fail "successful release did not verify startup and active Caddy image routes (calls=$route_verifier_calls)"

# The release coordinator owns the local/external backup choice, rather than
# allowing the blue-green helper to infer it from an ambient environment.
reset_release_case
local_backup_output="${TEST_ROOT}/local-backup.log"
if ! ALLOW_DRAINING=true FAKE_CURL_SUCCESS=1 FAKE_UPDATE_CADDY=1 \
  run_github_prebuilt_release >"$local_backup_output" 2>&1; then
  sed -n '1,200p' "$local_backup_output" >&2
  fail 'local dependency release did not complete'
fi
assert_contains "$BLUE_GREEN_ENV_LOG" \
  'mode=local old=sub2api-green new=sub2api-blue backup=true'
assert_not_contains "$DOCKER_CALLS" 'rm sub2api-blue'
assert_not_contains "$NODE_STATE_CALLS" 'preflight'

# A stopped target from an external runtime must be discarded only after every
# Caddy view still proves that traffic belongs to the healthy old generation.
reset_release_case
external_stale_output="${TEST_ROOT}/external-stale.log"
if ! ALLOW_DRAINING=true FAKE_CURL_SUCCESS=1 FAKE_UPDATE_CADDY=1 \
  run_external_github_prebuilt_release >"$external_stale_output" 2>&1; then
  sed -n '1,200p' "$external_stale_output" >&2
  fail 'external stale-target release did not complete'
fi
assert_contains "$BLUE_GREEN_ENV_LOG" \
  'mode=external old=sub2api-green new=sub2api-blue backup=false'
assert_contains "$DOCKER_CALLS" 'rm sub2api-blue'
assert_not_contains "$DOCKER_CALLS" 'rm -f sub2api-blue'
assert_event_order 'helper-validation old=sub2api-green new=sub2api-blue' 'docker-rm rm sub2api-blue'
assert_event_order 'docker-rm rm sub2api-blue' 'helper old=sub2api-green new=sub2api-blue'

# The real helper's external runtime validation runs before an external stale
# target is removed. A contract failure must stop before a local transaction
# is created or the normal helper is invoked.
reset_release_case
external_validation_failure_output="${TEST_ROOT}/external-validation-failure.log"
if ALLOW_DRAINING=true FAKE_EXTERNAL_VALIDATION_FAIL=1 \
  run_external_github_prebuilt_release >"$external_validation_failure_output" 2>&1; then
  fail 'external stale cleanup accepted an invalid external runtime contract'
fi
assert_contains "$external_validation_failure_output" \
  'could not validate external runtime contract before removing stopped inactive target sub2api-blue'
assert_contains "$EVENT_LOG" 'helper-validation old=sub2api-green new=sub2api-blue'
assert_not_contains "$DOCKER_CALLS" 'rm sub2api-blue'
assert_not_contains "$EVENT_LOG" 'helper old='
assert_not_contains "$NODE_STATE_CALLS" 'local-standby'
[ ! -e "$LOCAL_TRANSACTION" ] || fail 'external validation failure created a local release transaction'

# Any disagreement, including an unreadable view, prevents target removal and
# keeps the helper/node transaction untouched.
reset_release_case
printf 'reverse_proxy sub2api-green:8080\n# sub2api-blue:8080\n' >"${APP_DIR}/Caddyfile"
host_drift_output="${TEST_ROOT}/host-drift.log"
if ALLOW_DRAINING=true run_external_github_prebuilt_release >"$host_drift_output" 2>&1; then
  fail 'host Caddy drift was accepted'
fi
assert_not_contains "$DOCKER_CALLS" 'rm sub2api-blue'
assert_not_contains "$EVENT_LOG" 'helper old='
assert_not_contains "$NODE_STATE_CALLS" 'local-standby'

reset_release_case
printf 'reverse_proxy sub2api-green:8080\nreverse_proxy sub2api:8080\n' >"$STARTUP_CADDY"
legacy_upstream_output="${TEST_ROOT}/legacy-upstream-ambiguity.log"
if ALLOW_DRAINING=true run_external_github_prebuilt_release >"$legacy_upstream_output" 2>&1; then
  fail 'a Caddy view with a third legacy upstream was accepted'
fi
assert_contains "$legacy_upstream_output" \
  'Caddy views do not conclusively point at sub2api-green:8080'
assert_not_contains "$DOCKER_CALLS" 'rm sub2api-blue'
assert_not_contains "$EVENT_LOG" 'helper old='
assert_not_contains "$NODE_STATE_CALLS" 'local-standby'

reset_release_case
printf 'reverse_proxy sub2api-blue:8080\n' >"$STARTUP_CADDY"
startup_drift_output="${TEST_ROOT}/startup-drift.log"
if ALLOW_DRAINING=true run_external_github_prebuilt_release >"$startup_drift_output" 2>&1; then
  fail 'startup Caddy drift was accepted'
fi
assert_contains "$startup_drift_output" 'Caddy views do not conclusively point at sub2api-green:8080'
assert_not_contains "$DOCKER_CALLS" 'rm sub2api-blue'
assert_not_contains "$EVENT_LOG" 'helper old='
assert_not_contains "$NODE_STATE_CALLS" 'local-standby'

reset_release_case
printf '{"upstream":"sub2api-blue:8080"}\n' >"$ACTIVE_CADDY"
active_drift_output="${TEST_ROOT}/active-drift.log"
if ALLOW_DRAINING=true run_external_github_prebuilt_release >"$active_drift_output" 2>&1; then
  fail 'active Caddy drift was accepted'
fi
assert_contains "$active_drift_output" 'Caddy views do not conclusively point at sub2api-green:8080'
assert_not_contains "$DOCKER_CALLS" 'rm sub2api-blue'
assert_not_contains "$EVENT_LOG" 'helper old='
assert_not_contains "$NODE_STATE_CALLS" 'local-standby'

reset_release_case
startup_read_failure_output="${TEST_ROOT}/startup-read-failure.log"
if ALLOW_DRAINING=true FAKE_STARTUP_CADDY_FAIL=1 \
  run_external_github_prebuilt_release >"$startup_read_failure_output" 2>&1; then
  fail 'unreadable startup Caddy view was accepted'
fi
assert_contains "$startup_read_failure_output" 'could not read Caddy startup configuration'
assert_not_contains "$DOCKER_CALLS" 'rm sub2api-blue'
assert_not_contains "$EVENT_LOG" 'helper old='
assert_not_contains "$NODE_STATE_CALLS" 'local-standby'

# A stale external target can be a previous local transaction's rollback
# generation. The coordinator must stop before touching it and leave recovery
# to the node-state helper.
reset_release_case
: >"$LOCAL_TRANSACTION"
unfinished_transaction_output="${TEST_ROOT}/unfinished-local-transaction.log"
if ALLOW_DRAINING=true run_external_github_prebuilt_release >"$unfinished_transaction_output" 2>&1; then
  fail 'external stale cleanup accepted an unfinished local transaction'
fi
assert_contains "$unfinished_transaction_output" \
  'could not verify that no local release transaction is unfinished before removing an external target'
[ -e "$LOCAL_TRANSACTION" ] || fail 'external stale cleanup removed the unfinished local transaction'
assert_contains "$NODE_STATE_CALLS" 'preflight'
assert_not_contains "$DOCKER_CALLS" 'rm sub2api-blue'
assert_not_contains "$EVENT_LOG" 'helper old='

# Single-node compatibility mode has no node-state preflight. It must still
# reject every retained local-transaction path before stale external cleanup.
reset_release_case
: >"$LOCAL_TRANSACTION"
legacy_transaction_output="${TEST_ROOT}/legacy-local-transaction.log"
if ALLOW_DRAINING=true DUAL_NODE_RUNTIME_ENABLED=false \
  run_external_github_prebuilt_release >"$legacy_transaction_output" 2>&1; then
  fail 'legacy external stale cleanup accepted a local transaction file'
fi
assert_contains "$legacy_transaction_output" 'an unfinished local release transaction exists at'
assert_not_contains "$NODE_STATE_CALLS" 'preflight'
assert_not_contains "$DOCKER_CALLS" 'rm sub2api-blue'
assert_not_contains "$EVENT_LOG" 'helper-validation'
assert_not_contains "$EVENT_LOG" 'helper old='

reset_release_case
ln -s "${TEST_ROOT}/missing-local-release.env" "$LOCAL_TRANSACTION"
legacy_transaction_symlink_output="${TEST_ROOT}/legacy-local-transaction-symlink.log"
if ALLOW_DRAINING=true DUAL_NODE_RUNTIME_ENABLED=false \
  run_external_github_prebuilt_release >"$legacy_transaction_symlink_output" 2>&1; then
  fail 'legacy external stale cleanup accepted a dangling local transaction symlink'
fi
assert_contains "$legacy_transaction_symlink_output" 'an unfinished local release transaction exists at'
[ -L "$LOCAL_TRANSACTION" ] || fail 'legacy transaction symlink was changed'
assert_not_contains "$DOCKER_CALLS" 'rm sub2api-blue'
assert_not_contains "$EVENT_LOG" 'helper-validation'

reset_release_case
mkdir "$LOCAL_TRANSACTION"
legacy_transaction_directory_output="${TEST_ROOT}/legacy-local-transaction-directory.log"
if ALLOW_DRAINING=true DUAL_NODE_RUNTIME_ENABLED=false \
  run_external_github_prebuilt_release >"$legacy_transaction_directory_output" 2>&1; then
  fail 'legacy external stale cleanup accepted a non-file local transaction residue'
fi
assert_contains "$legacy_transaction_directory_output" 'an unfinished local release transaction exists at'
[ -d "$LOCAL_TRANSACTION" ] || fail 'legacy transaction directory was changed'
assert_not_contains "$DOCKER_CALLS" 'rm sub2api-blue'
assert_not_contains "$EVENT_LOG" 'helper-validation'
rmdir "$LOCAL_TRANSACTION"

# A helper failure before Caddy changes is not a rollback event. Abort the
# local transaction directly, clean the failed inactive target safely, and do
# not reinterpret the stopped target as the old active generation.
reset_release_case
pre_caddy_failure_output="${TEST_ROOT}/pre-caddy-failure.log"
if ALLOW_DRAINING=true FAKE_BLUE_GREEN_FAIL_BEFORE_CADDY=1 \
  run_external_github_prebuilt_release >"$pre_caddy_failure_output" 2>&1; then
  fail 'pre-Caddy helper failure was accepted'
fi
assert_contains "$pre_caddy_failure_output" \
  'Blue-green release failed before the Caddy switch; aborting local node state without rollback'
assert_not_contains "$pre_caddy_failure_output" 'Attempting automatic rollback'
assert_not_contains "$pre_caddy_failure_output" 'old container sub2api-green is not running'
assert_not_contains "$pre_caddy_failure_output" 'old container sub2api-blue is not running'
assert_contains "$NODE_STATE_CALLS" 'local-standby sub2api-blue'
assert_contains "$NODE_STATE_CALLS" 'abort-local'
[ ! -e "$LOCAL_TRANSACTION" ] || fail 'pre-Caddy failure left a local release transaction'
[ "$(wc -l <"$BLUE_GREEN_ENV_LOG" | tr -d '[:space:]')" = 1 ] \
  || fail 'pre-Caddy failure attempted a rollback helper invocation'
assert_contains "$DOCKER_CALLS" 'rm -f sub2api-blue'
assert_not_contains "$EVENT_LOG" 'refund-rollback-'
assert_traffic_state accepting

# A Caddy reload can have applied the candidate and still failed. The helper
# retains an owner+live-reload transaction, so the wrapper must gate the
# candidate even when every Caddy view has already returned to old. A 409
# leaves both durable transactions and admission draining; the explicit resume
# command repeats the gate and is the only path that can restore old state.
reset_release_case
retained_old_views_409_output="${TEST_ROOT}/retained-old-views-409.log"
retained_old_views_409_inode="$(file_inode "$TRAFFIC_STATE_FILE")"
if ALLOW_DRAINING=true FAKE_RETAINED_CADDY_EXPOSURE_WITH_OLD_VIEWS=1 \
  FAKE_REFUND_ROLLBACK_READINESS_STATUS=409 \
  run_external_github_prebuilt_release >"$retained_old_views_409_output" 2>&1; then
  fail 'retained old-view exposure accepted a pending refund readiness response'
fi
assert_contains "$retained_old_views_409_output" \
  'Retained Caddy transaction matches sub2api-green:8080 -> sub2api-blue:8080; draining and checking refund rollback readiness'
assert_contains "$retained_old_views_409_output" \
  'guarded Caddy recovery is blocked by candidate refund readiness'
assert_contains "$EVENT_LOG" 'refund-rollback-livez in_flight=0'
assert_contains "$EVENT_LOG" 'refund-rollback-readiness status=409'
assert_not_contains "$NODE_STATE_CALLS" 'abort-local'
assert_not_contains "$DOCKER_CALLS" 'rm -f sub2api-blue'
[ -e "$RETAINED_CADDY_TRANSACTION" ] \
  || fail '409 retained old-view exposure discarded the Caddy transaction'
[ -e "$LOCAL_TRANSACTION" ] \
  || fail '409 retained old-view exposure discarded the local transaction'
assert_contains "${APP_DIR}/Caddyfile" 'sub2api-green:8080'
assert_contains "$STARTUP_CADDY" 'sub2api-green:8080'
assert_contains "$ACTIVE_CADDY" 'sub2api-green:8080'
[ "$(file_inode "$TRAFFIC_STATE_FILE")" = "$retained_old_views_409_inode" ] \
  || fail '409 retained old-view exposure replaced the traffic-state bind-mount inode'
assert_traffic_state draining

# A route-miss SPA can still return HTTP 200. A retained exposure must not
# mistake that HTML response for a zero-pending refund readiness attestation.
reset_release_case
retained_old_views_html_output="${TEST_ROOT}/retained-old-views-html.log"
retained_old_views_html_inode="$(file_inode "$TRAFFIC_STATE_FILE")"
if ALLOW_DRAINING=true FAKE_RETAINED_CADDY_EXPOSURE_WITH_OLD_VIEWS=1 \
  FAKE_REFUND_ROLLBACK_READINESS_BODY_SET=true \
  FAKE_REFUND_ROLLBACK_READINESS_BODY='<!doctype html><html><body>SPA fallback</body></html>' \
  run_external_github_prebuilt_release >"$retained_old_views_html_output" 2>&1; then
  fail 'retained old-view exposure accepted an HTTP 200 HTML readiness response'
fi
assert_contains "$retained_old_views_html_output" \
  'candidate refund rollback readiness response is not valid zero-pending JSON: /internal/refund-rollback-readiness'
assert_contains "$retained_old_views_html_output" \
  'guarded Caddy recovery is blocked by candidate refund readiness'
assert_contains "$EVENT_LOG" 'refund-rollback-readiness status=200'
assert_not_contains "$NODE_STATE_CALLS" 'abort-local'
assert_not_contains "$DOCKER_CALLS" 'rm -f sub2api-blue'
[ -e "$RETAINED_CADDY_TRANSACTION" ] \
  || fail 'HTML retained old-view exposure discarded the Caddy transaction'
[ -e "$LOCAL_TRANSACTION" ] \
  || fail 'HTML retained old-view exposure discarded the local transaction'
assert_contains "${APP_DIR}/Caddyfile" 'sub2api-green:8080'
assert_contains "$STARTUP_CADDY" 'sub2api-green:8080'
assert_contains "$ACTIVE_CADDY" 'sub2api-green:8080'
[ "$(file_inode "$TRAFFIC_STATE_FILE")" = "$retained_old_views_html_inode" ] \
  || fail 'HTML retained old-view exposure replaced the traffic-state bind-mount inode'
assert_traffic_state draining

# A process interruption after the failed helper exits must use the same
# coordinator gate on retry. Once readiness becomes 2xx it restores verified
# old Caddy, finalizes the local transaction, re-admits traffic, and removes
# the retained candidate.
: >"$DOCKER_CALLS"
: >"$NODE_STATE_CALLS"
if ! FAKE_REFUND_ROLLBACK_READINESS_STATUS=200 \
  run_retained_caddy_recovery >"${TEST_ROOT}/retained-canonical-recovery.log" 2>&1; then
  sed -n '1,200p' "${TEST_ROOT}/retained-canonical-recovery.log" >&2
  fail 'canonical retained-Caddy recovery was rejected after readiness passed'
fi
assert_contains "${TEST_ROOT}/retained-canonical-recovery.log" 'Guarded retained-Caddy recovery completed'
assert_event_order 'refund-rollback-readiness status=200' \
  'helper old=sub2api-green new=sub2api-blue action=restore-after-refund-gate'
assert_contains "$NODE_STATE_CALLS" 'abort-local'
assert_contains "$DOCKER_CALLS" 'rm -f sub2api-blue'
[ ! -e "$RETAINED_CADDY_TRANSACTION" ] \
  || fail 'canonical retained-Caddy recovery retained its completed transaction'
[ ! -e "$LOCAL_TRANSACTION" ] \
  || fail 'canonical retained-Caddy recovery retained its completed local state'
assert_traffic_state accepting

# An unreachable readiness endpoint has the same retained, draining recovery
# shape. No helper restoration, local abort, or candidate removal is allowed.
reset_release_case
retained_old_views_unreachable_output="${TEST_ROOT}/retained-old-views-unreachable.log"
if ALLOW_DRAINING=true FAKE_RETAINED_CADDY_EXPOSURE_WITH_OLD_VIEWS=1 \
  FAKE_REFUND_ROLLBACK_READINESS_UNREACHABLE=1 \
  run_external_github_prebuilt_release >"$retained_old_views_unreachable_output" 2>&1; then
  fail 'retained old-view exposure accepted an unreachable readiness endpoint'
fi
assert_contains "$retained_old_views_unreachable_output" \
  'candidate internal rollback probe is unreachable or rejected: /internal/refund-rollback-readiness'
assert_contains "$retained_old_views_unreachable_output" \
  'guarded Caddy recovery is blocked by candidate refund readiness'
assert_contains "$EVENT_LOG" 'refund-rollback-readiness status=unreachable'
assert_not_contains "$NODE_STATE_CALLS" 'abort-local'
assert_not_contains "$DOCKER_CALLS" 'rm -f sub2api-blue'
[ -e "$RETAINED_CADDY_TRANSACTION" ] \
  || fail 'unreachable retained old-view exposure discarded the Caddy transaction'
[ -e "$LOCAL_TRANSACTION" ] \
  || fail 'unreachable retained old-view exposure discarded the local transaction'
assert_traffic_state draining

# A 2xx readiness result on the initial wrapper failure must take the explicit
# retained-transaction restoration path even though all Caddy views were old
# before the gate ran.
reset_release_case
retained_old_views_200_output="${TEST_ROOT}/retained-old-views-200.log"
if ALLOW_DRAINING=true FAKE_RETAINED_CADDY_EXPOSURE_WITH_OLD_VIEWS=1 \
  run_external_github_prebuilt_release >"$retained_old_views_200_output" 2>&1; then
  fail 'retained old-view helper failure unexpectedly completed the release'
fi
assert_contains "$retained_old_views_200_output" 'Guarded retained-Caddy recovery completed'
assert_contains "$EVENT_LOG" 'refund-rollback-readiness status=200'
assert_event_order 'refund-rollback-readiness status=200' \
  'helper old=sub2api-green new=sub2api-blue action=restore-after-refund-gate'
assert_contains "$NODE_STATE_CALLS" 'abort-local'
assert_contains "$DOCKER_CALLS" 'rm -f sub2api-blue'
[ ! -e "$RETAINED_CADDY_TRANSACTION" ] \
  || fail '2xx retained old-view recovery retained the Caddy transaction'
[ ! -e "$LOCAL_TRANSACTION" ] \
  || fail '2xx retained old-view recovery retained the local transaction'
assert_contains "${APP_DIR}/Caddyfile" 'sub2api-green:8080'
assert_contains "$STARTUP_CADDY" 'sub2api-green:8080'
assert_contains "$ACTIVE_CADDY" 'sub2api-green:8080'
assert_traffic_state accepting

# Once Caddy may have sent traffic to the candidate, a reviewed pending refund
# reservation blocks old-generation takeover. The candidate, Caddy direction,
# and durable local transaction must remain available for reconciliation; the
# traffic-state file returns to its original inode and value rather than leaving
# a deterministic draining outage.
reset_release_case
pending_refund_rollback_output="${TEST_ROOT}/pending-refund-rollback.log"
pending_traffic_inode="$(file_inode "$TRAFFIC_STATE_FILE")"
if ALLOW_DRAINING=true FAKE_BLUE_GREEN_FAIL_AFTER_CADDY=1 \
  FAKE_REFUND_ROLLBACK_READINESS_STATUS=409 \
  run_external_github_prebuilt_release >"$pending_refund_rollback_output" 2>&1; then
  fail 'pending refund readiness allowed old-generation rollback'
fi
assert_contains "$pending_refund_rollback_output" \
  'candidate internal rollback probe is unreachable or rejected: /internal/refund-rollback-readiness'
assert_contains "$pending_refund_rollback_output" \
  'automatic rollback is blocked by candidate refund readiness'
assert_contains "$pending_refund_rollback_output" \
  'Let the candidate finish refund reconciliation, then resume the same canonical release/recovery transaction'
assert_contains "$EVENT_LOG" 'refund-rollback-livez in_flight=0'
assert_contains "$EVENT_LOG" 'refund-rollback-readiness status=409'
assert_not_contains "$EVENT_LOG" 'helper old=sub2api-blue new=sub2api-green'
assert_contains "$NODE_STATE_CALLS" 'local-standby sub2api-blue'
assert_not_contains "$NODE_STATE_CALLS" 'abort-local'
[ -e "$LOCAL_TRANSACTION" ] || fail 'pending refund rollback removed the local release transaction'
assert_contains "${APP_DIR}/Caddyfile" 'sub2api-blue:8080'
assert_contains "$STARTUP_CADDY" 'sub2api-blue:8080'
assert_contains "$ACTIVE_CADDY" 'sub2api-blue:8080'
assert_not_contains "$DOCKER_CALLS" 'rm -f sub2api-blue'
[ "$(file_inode "$TRAFFIC_STATE_FILE")" = "$pending_traffic_inode" ] \
  || fail 'pending refund rollback replaced the traffic-state bind-mount inode'
assert_traffic_state accepting

# A successful status still cannot permit old-generation takeover when the
# candidate reports a nonzero reviewed reservation count.
reset_release_case
nonzero_refund_rollback_output="${TEST_ROOT}/nonzero-refund-rollback.log"
nonzero_traffic_inode="$(file_inode "$TRAFFIC_STATE_FILE")"
if ALLOW_DRAINING=true FAKE_BLUE_GREEN_FAIL_AFTER_CADDY=1 \
  FAKE_REFUND_ROLLBACK_READINESS_BODY_SET=true \
  FAKE_REFUND_ROLLBACK_READINESS_BODY='{"ready":true,"entitlement_reserved_reviewed_pending_count":1}' \
  run_external_github_prebuilt_release >"$nonzero_refund_rollback_output" 2>&1; then
  fail 'nonzero JSON refund readiness allowed old-generation rollback'
fi
assert_contains "$nonzero_refund_rollback_output" \
  'candidate refund rollback readiness response is not valid zero-pending JSON: /internal/refund-rollback-readiness'
assert_contains "$nonzero_refund_rollback_output" \
  'automatic rollback is blocked by candidate refund readiness'
assert_contains "$EVENT_LOG" 'refund-rollback-livez in_flight=0'
assert_contains "$EVENT_LOG" 'refund-rollback-readiness status=200'
assert_not_contains "$EVENT_LOG" 'helper old=sub2api-blue new=sub2api-green'
assert_contains "$NODE_STATE_CALLS" 'local-standby sub2api-blue'
assert_not_contains "$NODE_STATE_CALLS" 'abort-local'
[ -e "$LOCAL_TRANSACTION" ] || fail 'nonzero refund rollback removed the local release transaction'
assert_contains "${APP_DIR}/Caddyfile" 'sub2api-blue:8080'
assert_contains "$STARTUP_CADDY" 'sub2api-blue:8080'
assert_contains "$ACTIVE_CADDY" 'sub2api-blue:8080'
assert_not_contains "$DOCKER_CALLS" 'rm -f sub2api-blue'
[ "$(file_inode "$TRAFFIC_STATE_FILE")" = "$nonzero_traffic_inode" ] \
  || fail 'nonzero refund rollback replaced the traffic-state bind-mount inode'
assert_traffic_state accepting

# An unavailable readiness endpoint has the same fail-closed recovery shape.
reset_release_case
unreachable_refund_rollback_output="${TEST_ROOT}/unreachable-refund-rollback.log"
unreachable_traffic_inode="$(file_inode "$TRAFFIC_STATE_FILE")"
if ALLOW_DRAINING=true FAKE_BLUE_GREEN_FAIL_AFTER_CADDY=1 \
  FAKE_REFUND_ROLLBACK_READINESS_UNREACHABLE=1 \
  run_external_github_prebuilt_release >"$unreachable_refund_rollback_output" 2>&1; then
  fail 'unreachable refund readiness allowed old-generation rollback'
fi
assert_contains "$unreachable_refund_rollback_output" \
  'candidate internal rollback probe is unreachable or rejected: /internal/refund-rollback-readiness'
assert_contains "$unreachable_refund_rollback_output" \
  'automatic rollback is blocked by candidate refund readiness'
assert_contains "$unreachable_refund_rollback_output" \
  'Let the candidate finish refund reconciliation, then resume the same canonical release/recovery transaction'
assert_contains "$EVENT_LOG" 'refund-rollback-livez in_flight=0'
assert_contains "$EVENT_LOG" 'refund-rollback-readiness status=unreachable'
assert_not_contains "$EVENT_LOG" 'helper old=sub2api-blue new=sub2api-green'
assert_not_contains "$NODE_STATE_CALLS" 'abort-local'
[ -e "$LOCAL_TRANSACTION" ] || fail 'unreachable refund rollback removed the local release transaction'
assert_contains "${APP_DIR}/Caddyfile" 'sub2api-blue:8080'
assert_contains "$STARTUP_CADDY" 'sub2api-blue:8080'
assert_contains "$ACTIVE_CADDY" 'sub2api-blue:8080'
assert_not_contains "$DOCKER_CALLS" 'rm -f sub2api-blue'
[ "$(file_inode "$TRAFFIC_STATE_FILE")" = "$unreachable_traffic_inode" ] \
  || fail 'unreachable refund rollback replaced the traffic-state bind-mount inode'
assert_traffic_state accepting

# The readiness gate leaves traffic draining until the old generation really
# takes over. If source inspection fails before that helper starts, restore the
# original state only because every Caddy view remains on the candidate.
reset_release_case
post_gate_inspect_failure_output="${TEST_ROOT}/post-gate-inspect-failure.log"
post_gate_inspect_traffic_inode="$(file_inode "$TRAFFIC_STATE_FILE")"
if ALLOW_DRAINING=true FAKE_BLUE_GREEN_FAIL_AFTER_CADDY=1 \
  FAKE_ROLLBACK_SOURCE_INSPECT_FAIL=1 \
  run_external_github_prebuilt_release >"$post_gate_inspect_failure_output" 2>&1; then
  fail 'post-gate source inspection failure was accepted'
fi
assert_contains "$post_gate_inspect_failure_output" \
  'Refund rollback readiness passed for sub2api-blue'
assert_contains "$post_gate_inspect_failure_output" \
  'could not inspect failed release container before rollback; Caddy remains on sub2api-blue, restored traffic admission to accepting'
assert_contains "$post_gate_inspect_failure_output" \
  'ERROR: could not inspect failed release container before rollback: sub2api-blue'
assert_contains "$EVENT_LOG" 'refund-rollback-readiness status=200'
assert_not_contains "$EVENT_LOG" 'helper old=sub2api-blue new=sub2api-green'
assert_not_contains "$NODE_STATE_CALLS" 'abort-local'
[ -e "$LOCAL_TRANSACTION" ] || fail 'post-gate source inspection failure removed the local release transaction'
assert_contains "${APP_DIR}/Caddyfile" 'sub2api-blue:8080'
assert_contains "$STARTUP_CADDY" 'sub2api-blue:8080'
assert_contains "$ACTIVE_CADDY" 'sub2api-blue:8080'
[ "$(file_inode "$TRAFFIC_STATE_FILE")" = "$post_gate_inspect_traffic_inode" ] \
  || fail 'post-gate source inspection failure replaced the traffic-state bind-mount inode'
assert_traffic_state accepting

# A rollback-helper failure before its Caddy mutation has the same candidate
# recovery shape, including the original traffic admission state.
reset_release_case
rollback_helper_candidate_failure_output="${TEST_ROOT}/rollback-helper-candidate-failure.log"
rollback_helper_candidate_traffic_inode="$(file_inode "$TRAFFIC_STATE_FILE")"
if ALLOW_DRAINING=true FAKE_BLUE_GREEN_FAIL_AFTER_CADDY=1 \
  FAKE_ROLLBACK_HELPER_FAIL_WITH_CANDIDATE=1 \
  run_external_github_prebuilt_release >"$rollback_helper_candidate_failure_output" 2>&1; then
  fail 'candidate-side rollback-helper failure was accepted'
fi
assert_contains "$rollback_helper_candidate_failure_output" \
  'automatic rollback helper failed; Caddy remains on sub2api-blue, restored traffic admission to accepting'
assert_contains "$rollback_helper_candidate_failure_output" \
  'ERROR: automatic rollback failed; manual intervention is required'
assert_contains "$EVENT_LOG" 'helper old=sub2api-blue new=sub2api-green'
assert_not_contains "$NODE_STATE_CALLS" 'abort-local'
[ -e "$LOCAL_TRANSACTION" ] || fail 'candidate-side rollback-helper failure removed the local release transaction'
assert_contains "${APP_DIR}/Caddyfile" 'sub2api-blue:8080'
assert_contains "$STARTUP_CADDY" 'sub2api-blue:8080'
assert_contains "$ACTIVE_CADDY" 'sub2api-blue:8080'
[ "$(file_inode "$TRAFFIC_STATE_FILE")" = "$rollback_helper_candidate_traffic_inode" ] \
  || fail 'candidate-side rollback-helper failure replaced the traffic-state bind-mount inode'
assert_traffic_state accepting

# A rollback helper may fail after it has switched Caddy to the old generation.
# Do not overwrite that successful Caddy direction by reopening admission from
# the candidate-side recovery path.
reset_release_case
rollback_helper_old_failure_output="${TEST_ROOT}/rollback-helper-old-failure.log"
rollback_helper_old_traffic_inode="$(file_inode "$TRAFFIC_STATE_FILE")"
if ALLOW_DRAINING=true FAKE_BLUE_GREEN_FAIL_AFTER_CADDY=1 \
  FAKE_ROLLBACK_HELPER_FAIL_AFTER_CADDY=1 \
  run_external_github_prebuilt_release >"$rollback_helper_old_failure_output" 2>&1; then
  fail 'old-side rollback-helper failure was accepted'
fi
assert_contains "$rollback_helper_old_failure_output" \
  'automatic rollback helper failed; Caddy no longer conclusively points only at sub2api-blue:8080, leaving traffic admission draining for canonical recovery'
assert_contains "$rollback_helper_old_failure_output" \
  'ERROR: automatic rollback failed; manual intervention is required'
assert_not_contains "$NODE_STATE_CALLS" 'abort-local'
[ -e "$LOCAL_TRANSACTION" ] || fail 'old-side rollback-helper failure removed the local release transaction'
assert_contains "${APP_DIR}/Caddyfile" 'sub2api-green:8080'
assert_contains "$STARTUP_CADDY" 'sub2api-green:8080'
assert_contains "$ACTIVE_CADDY" 'sub2api-green:8080'
[ "$(file_inode "$TRAFFIC_STATE_FILE")" = "$rollback_helper_old_traffic_inode" ] \
  || fail 'old-side rollback-helper failure replaced the traffic-state bind-mount inode'
assert_traffic_state draining

# A zero-pending readiness response permits the existing post-switch rollback.
reset_release_case
post_caddy_helper_failure_output="${TEST_ROOT}/post-caddy-helper-failure.log"
if ALLOW_DRAINING=true FAKE_BLUE_GREEN_FAIL_AFTER_CADDY=1 \
  run_external_github_prebuilt_release >"$post_caddy_helper_failure_output" 2>&1; then
  fail 'post-Caddy helper failure was accepted'
fi
assert_contains "$post_caddy_helper_failure_output" \
  'Attempting automatic rollback to sub2api-green'
assert_contains "$post_caddy_helper_failure_output" 'Rollback completed'
assert_contains "$post_caddy_helper_failure_output" \
  'Refund rollback readiness passed for sub2api-blue'
assert_contains "$EVENT_LOG" 'refund-rollback-livez in_flight=0'
assert_contains "$EVENT_LOG" 'refund-rollback-readiness status=200'
assert_event_order 'refund-rollback-livez in_flight=0' 'refund-rollback-readiness status=200'
assert_event_order 'refund-rollback-readiness status=200' 'helper old=sub2api-blue new=sub2api-green'
assert_contains "$BLUE_GREEN_ENV_LOG" \
  'mode=external old=sub2api-green new=sub2api-blue backup=false'
assert_contains "$BLUE_GREEN_ENV_LOG" \
  'mode=external old=sub2api-blue new=sub2api-green backup=false'
assert_contains "$NODE_STATE_CALLS" 'abort-local'
[ ! -e "$LOCAL_TRANSACTION" ] || fail 'post-Caddy helper failure left a local release transaction'
assert_traffic_state accepting

# A request-serving rollback node must stay background-fenced before, during,
# and after its local blue-green recreation.
printf 'reverse_proxy sub2api-green:8080\n' >"${APP_DIR}/Caddyfile"
: >"$DOCKER_CALLS"
: >"$NODE_STATE_CALLS"
: >"$CURL_CALLS"
preserve_mismatch_output="${TEST_ROOT}/preserve-mismatch.log"
if ALLOW_DRAINING=true RELEASE_BACKGROUND_MODE=preserve-standby \
  run_github_prebuilt_release >"$preserve_mismatch_output" 2>&1; then
  fail 'standby-preserving release accepted an active current generation'
fi
assert_contains "$preserve_mismatch_output" \
  'node runtime state is not safe for a preserve-standby local release'
assert_not_contains "$NODE_STATE_CALLS" 'local-preserve-standby'
assert_not_contains "$DOCKER_CALLS" 'build '

printf 'reverse_proxy sub2api-green:8080\n' >"${APP_DIR}/Caddyfile"
: >"$DOCKER_CALLS"
: >"$NODE_STATE_CALLS"
: >"$CURL_CALLS"
preserve_success_output="${TEST_ROOT}/preserve-success.log"
if ! ALLOW_DRAINING=true RELEASE_BACKGROUND_MODE=preserve-standby \
  FAKE_NODE_STATE_BACKGROUND=standby FAKE_CURL_SUCCESS=1 FAKE_UPDATE_CADDY=1 \
  run_github_prebuilt_release >"$preserve_success_output" 2>&1; then
  sed -n '1,200p' "$preserve_success_output" >&2
  fail 'standby-preserving release did not complete'
fi
assert_contains "$NODE_STATE_CALLS" 'local-preserve-standby sub2api-blue'
assert_contains "$NODE_STATE_CALLS" 'commit-local'
assert_not_contains "$NODE_STATE_CALLS" 'local-standby sub2api-blue'
assert_contains "$preserve_success_output" 'background_mode=preserve-standby'

printf 'Server release inactive-container guard tests passed.\n'
