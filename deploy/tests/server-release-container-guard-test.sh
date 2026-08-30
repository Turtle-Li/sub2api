#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
SCRIPT="${DEPLOY_DIR}/sub2api-server-release.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-server-release-test.XXXXXX")"
FAKE_BIN="${TEST_ROOT}/bin"
APP_DIR="${TEST_ROOT}/app"
WORK_ROOT="${TEST_ROOT}/worktrees"
SOURCE_DIR="${WORK_ROOT}/release.case"
DOCKER_CALLS="${TEST_ROOT}/docker-calls.log"
NODE_STATE_CALLS="${TEST_ROOT}/node-state-calls.log"
CURL_CALLS="${TEST_ROOT}/curl-calls.log"

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

mkdir -p "$FAKE_BIN" "$APP_DIR/scripts" "$SOURCE_DIR"
printf 'FROM scratch\n' >"${SOURCE_DIR}/Dockerfile"
printf 'reverse_proxy sub2api-green:8080\n' >"${APP_DIR}/Caddyfile"
cat >"${APP_DIR}/scripts/sub2api-blue-green-release.sh" <<'EOF'
#!/usr/bin/env bash
set -eu
if [ "${FAKE_UPDATE_CADDY:-0}" = 1 ]; then
  printf 'reverse_proxy %s\n' "$CADDY_UPSTREAM_TO" >"$FAKE_APP_CADDY"
fi
EOF
printf '#!/usr/bin/env bash\nexit 0\n' >"${APP_DIR}/scripts/sub2api-drain-monitor.sh"
cat >"${APP_DIR}/scripts/sub2api-node-state.sh" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >>"$FAKE_NODE_STATE_CALLS"
if [ "${1:-}" = status ]; then
  printf 'traffic=accepting active_container=sub2api-green background=active\n'
fi
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
          sub2api-blue) printf 'false\n' ;;
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
    cat "$FAKE_APP_CADDY"
    ;;
  rm)
    [ "${2:-}" = "-f" ] || exit 1
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
    FAKE_FLOCK_COUNT_FILE="${FAKE_FLOCK_COUNT_FILE:-}" \
    FAKE_FLOCK_FAIL_ON_CALL="${FAKE_FLOCK_FAIL_ON_CALL:-}" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_WORK_ROOT="$WORK_ROOT" \
    SUB2API_RELEASE_LOG_DIR="${TEST_ROOT}/logs" \
    SUB2API_RELEASE_LOCK_FILE="${TEST_ROOT}/release.lock" \
    SUB2API_MAINTENANCE_LOCK_FILE="${TEST_ROOT}/maintenance.lock" \
    SUB2API_PUBLIC_HEALTH_RESOLVE="${SUB2API_PUBLIC_HEALTH_RESOLVE:-example.invalid:443:192.0.2.10}" \
    SUB2API_RELEASE_MIN_FREE_BYTES=1 \
    SUB2API_RELEASE_BUILD_TIMEOUT_SECONDS=30 \
    SUB2API_RELEASE_BUILD_GOMAXPROCS=1 \
    SUB2API_RELEASE_BUILD_GO_PARALLELISM=1 \
    SUB2API_RELEASE_BUILD_GO_MEMORY_LIMIT=768MiB \
    SUB2API_RELEASE_ALLOW_PREEXISTING_DRAINING_CONTAINER="${ALLOW_DRAINING:-false}" \
    SUB2API_DUAL_NODE_RUNTIME_ENABLED=true \
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
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_WORK_ROOT="$WORK_ROOT" \
    SUB2API_RELEASE_LOG_DIR="${TEST_ROOT}/logs" \
    SUB2API_RELEASE_LOCK_FILE="${TEST_ROOT}/release.lock" \
    SUB2API_MAINTENANCE_LOCK_FILE="${TEST_ROOT}/maintenance.lock" \
    SUB2API_PUBLIC_HEALTH_RESOLVE="${SUB2API_PUBLIC_HEALTH_RESOLVE:-example.invalid:443:192.0.2.10}" \
    SUB2API_RELEASE_MIN_FREE_BYTES=1 \
    SUB2API_RELEASE_ALLOW_PREEXISTING_DRAINING_CONTAINER="${ALLOW_DRAINING:-false}" \
    SUB2API_DUAL_NODE_RUNTIME_ENABLED=true \
    /bin/bash "$SCRIPT" \
      --prebuilt \
      'sub2api:auto-test' \
      'abc123' \
      '0.1.test' \
      'https://example.invalid/health' \
      'github-prebuilt-test'
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

: >"$DOCKER_CALLS"
resolve_mismatch_output="${TEST_ROOT}/resolve-mismatch.log"
if SUB2API_PUBLIC_HEALTH_RESOLVE='peer.invalid:443:192.0.2.10' \
  run_github_prebuilt_release >"$resolve_mismatch_output" 2>&1; then
  fail 'server release accepted a health resolve override for a peer hostname'
fi
assert_contains "$resolve_mismatch_output" 'host/port must match SUB2API_PUBLIC_HEALTH_URL'
if [ -s "$DOCKER_CALLS" ]; then
  fail 'Docker was inspected before health resolve validation'
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

: >"$DOCKER_CALLS"
rollback_cleanup_output="${TEST_ROOT}/rollback-cleanup.log"
if ALLOW_DRAINING=true run_github_prebuilt_release >"$rollback_cleanup_output" 2>&1; then
  fail 'fake release unexpectedly passed a failing public health check'
fi
assert_contains "$rollback_cleanup_output" 'Rollback completed'
assert_contains "$rollback_cleanup_output" 'Removing failed inactive target sub2api-blue'
assert_contains "$DOCKER_CALLS" 'rm -f sub2api-blue'
assert_contains "$NODE_STATE_CALLS" 'bootstrap'
assert_contains "$NODE_STATE_CALLS" 'local-standby sub2api-blue'
assert_contains "$NODE_STATE_CALLS" 'abort-local'

: >"$NODE_STATE_CALLS"
successful_release_output="${TEST_ROOT}/successful-release.log"
if ! ALLOW_DRAINING=true FAKE_CURL_SUCCESS=1 FAKE_UPDATE_CADDY=1 \
  run_github_prebuilt_release >"$successful_release_output" 2>&1; then
  sed -n '1,200p' "$successful_release_output" >&2
  fail 'fake verified release did not complete'
fi
assert_contains "$NODE_STATE_CALLS" 'local-standby sub2api-blue'
assert_contains "$NODE_STATE_CALLS" 'commit-local'
assert_contains "$CURL_CALLS" '--resolve example.invalid:443:192.0.2.10'
if grep -Fq -- 'abort-local' "$NODE_STATE_CALLS"; then
  fail 'successful release invoked node-state abort'
fi

printf 'Server release inactive-container guard tests passed.\n'
