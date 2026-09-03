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
STARTUP_CADDY="${TEST_ROOT}/startup.Caddyfile"
ACTIVE_CADDY="${TEST_ROOT}/active-caddy.json"
LOCAL_TRANSACTION="${TEST_ROOT}/local-release.env"
NEW_RUNNING_MARKER="${TEST_ROOT}/new-running.marker"
EXTERNAL_RUNTIME_ENV_FILE="${TEST_ROOT}/external-runtime.env"
EXTERNAL_CA_FILE="${TEST_ROOT}/external-ca.crt"

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

mkdir -p "$FAKE_BIN" "$APP_DIR/scripts" "$SOURCE_DIR"
printf 'FROM scratch\n' >"${SOURCE_DIR}/Dockerfile"
printf 'reverse_proxy sub2api-green:8080\n' >"${APP_DIR}/Caddyfile"
printf 'reverse_proxy sub2api-green:8080\n' >"$STARTUP_CADDY"
printf '{"upstream":"sub2api-green:8080"}\n' >"$ACTIVE_CADDY"
: >"$EXTERNAL_RUNTIME_ENV_FILE"
: >"$EXTERNAL_CA_FILE"

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
  printf 'mode=%s old=%s new=%s backup=%s isolated_old=%s fixed_egress_compatibility=%s preserve_source=%s\n' \
    "${SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE:-}" \
    "${OLD_CONTAINER:-}" \
    "${NEW_CONTAINER:-}" \
    "${RUN_BACKUP:-}" \
    "${ALLOW_ISOLATED_OLD_CONTAINER:-false}" \
    "${SUB2API_RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE:-}" \
    "${SUB2API_RELEASE_FIXED_EGRESS_PRESERVE_SOURCE_CONTAINER:-}" >>"$FAKE_BLUE_GREEN_ENV_LOG"
fi
if [ -n "${FAKE_EVENT_LOG:-}" ]; then
  printf 'helper old=%s new=%s\n' "${OLD_CONTAINER:-}" "${NEW_CONTAINER:-}" >>"$FAKE_EVENT_LOG"
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
EOF
printf '#!/usr/bin/env bash\nexit 0\n' >"${APP_DIR}/scripts/sub2api-drain-monitor.sh"
cat >"${APP_DIR}/scripts/sub2api-node-state.sh" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >>"$FAKE_NODE_STATE_CALLS"
case "${1:-}" in
  status)
    printf 'traffic=accepting active_container=sub2api-green background=%s\n' \
      "${FAKE_NODE_STATE_BACKGROUND:-active}"
    ;;
  preflight)
    [ ! -e "$FAKE_LOCAL_TRANSACTION" ] \
      || { printf 'ERROR: an unfinished local release transaction exists\n' >&2; exit 64; }
    ;;
  local-standby|local-preserve-standby) : >"$FAKE_LOCAL_TRANSACTION" ;;
  abort-local) rm -f -- "$FAKE_LOCAL_TRANSACTION" ;;
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
    FAKE_BLUE_GREEN_ENV_LOG="$BLUE_GREEN_ENV_LOG" \
    FAKE_EVENT_LOG="$EVENT_LOG" \
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
    FAKE_BLUE_GREEN_ENV_LOG="$BLUE_GREEN_ENV_LOG" \
    FAKE_EVENT_LOG="$EVENT_LOG" \
    FAKE_MARK_NEW_RUNNING="${FAKE_MARK_NEW_RUNNING:-0}" \
    FAKE_NEW_RUNNING_MARKER="$NEW_RUNNING_MARKER" \
    FAKE_NODE_STATE_BACKGROUND="${FAKE_NODE_STATE_BACKGROUND:-active}" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_WORK_ROOT="$WORK_ROOT" \
    SUB2API_RELEASE_LOG_DIR="${TEST_ROOT}/logs" \
    SUB2API_RELEASE_LOCK_FILE="${TEST_ROOT}/release.lock" \
    SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS="${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-1}" \
    SUB2API_MAINTENANCE_LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-${TEST_ROOT}/maintenance.lock}" \
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
    FAKE_BLUE_GREEN_ENV_LOG="$BLUE_GREEN_ENV_LOG" \
    FAKE_EVENT_LOG="$EVENT_LOG" \
    FAKE_MARK_NEW_RUNNING="${FAKE_MARK_NEW_RUNNING:-0}" \
    FAKE_NEW_RUNNING_MARKER="$NEW_RUNNING_MARKER" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_WORK_ROOT="$WORK_ROOT" \
    SUB2API_RELEASE_LOG_DIR="${TEST_ROOT}/logs" \
    SUB2API_RELEASE_LOCK_FILE="${TEST_ROOT}/release.lock" \
    SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1 \
    SUB2API_MAINTENANCE_LOCK_FILE="${TEST_ROOT}/maintenance.lock" \
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

reset_release_case() {
  reset_caddy_views
  rm -f -- "$LOCAL_TRANSACTION" "$NEW_RUNNING_MARKER"
  : >"$DOCKER_CALLS"
  : >"$NODE_STATE_CALLS"
  : >"$CURL_CALLS"
  : >"$BLUE_GREEN_ENV_LOG"
  : >"$EVENT_LOG"
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
  'mode=local old=sub2api-blue new=sub2api-green backup=false isolated_old=true fixed_egress_compatibility=preserve preserve_source=sub2api-green'

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
  'mode=local old=sub2api-blue new=sub2api-green backup=false isolated_old=false fixed_egress_compatibility=preserve preserve_source=sub2api-green'

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

# If the helper reports failure after it has changed Caddy, the old-only proof
# is unavailable and the existing rollback path remains mandatory.
reset_release_case
post_caddy_helper_failure_output="${TEST_ROOT}/post-caddy-helper-failure.log"
if ALLOW_DRAINING=true FAKE_BLUE_GREEN_FAIL_AFTER_CADDY=1 \
  run_external_github_prebuilt_release >"$post_caddy_helper_failure_output" 2>&1; then
  fail 'post-Caddy helper failure was accepted'
fi
assert_contains "$post_caddy_helper_failure_output" \
  'Attempting automatic rollback to sub2api-green'
assert_contains "$post_caddy_helper_failure_output" 'Rollback completed'
assert_contains "$BLUE_GREEN_ENV_LOG" \
  'mode=external old=sub2api-green new=sub2api-blue backup=false'
assert_contains "$BLUE_GREEN_ENV_LOG" \
  'mode=external old=sub2api-blue new=sub2api-green backup=false'
assert_contains "$NODE_STATE_CALLS" 'abort-local'
[ ! -e "$LOCAL_TRANSACTION" ] || fail 'post-Caddy helper failure left a local release transaction'

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
