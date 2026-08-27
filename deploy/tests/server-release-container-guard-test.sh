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
BLUE_GREEN_ENV_LOG="${TEST_ROOT}/blue-green-env.log"
EXTERNAL_RUNTIME_ENV_FILE="${TEST_ROOT}/external-runtime.env"
EXTERNAL_CA_FILE="${TEST_ROOT}/db-ca.crt"

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
if [ -n "${FAKE_BLUE_GREEN_ENV_LOG:-}" ]; then
  printf 'mode=%s runtime=%s ca=%s old=%s new=%s\n' \
    "${SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE:-}" \
    "${SUB2API_EXTERNAL_RUNTIME_ENV_FILE:-}" \
    "${SUB2API_EXTERNAL_CA_FILE:-}" \
    "${OLD_CONTAINER:-}" \
    "${NEW_CONTAINER:-}" >>"$FAKE_BLUE_GREEN_ENV_LOG"
fi
exit 0
EOF
printf '#!/usr/bin/env bash\nexit 0\n' >"${APP_DIR}/scripts/sub2api-drain-monitor.sh"
chmod +x \
  "${APP_DIR}/scripts/sub2api-blue-green-release.sh" \
  "${APP_DIR}/scripts/sub2api-drain-monitor.sh"

printf '%s\n' 'DATABASE_PASSWORD=sentinel-not-for-logs' >"$EXTERNAL_RUNTIME_ENV_FILE"
printf '%s\n' 'test-ca' >"$EXTERNAL_CA_FILE"

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
    printf 'reverse_proxy sub2api-green:8080\n'
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
exit 1
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
    FAKE_FLOCK_COUNT_FILE="${FAKE_FLOCK_COUNT_FILE:-}" \
    FAKE_FLOCK_FAIL_ON_CALL="${FAKE_FLOCK_FAIL_ON_CALL:-}" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_WORK_ROOT="$WORK_ROOT" \
    SUB2API_RELEASE_LOG_DIR="${TEST_ROOT}/logs" \
    SUB2API_RELEASE_LOCK_FILE="${TEST_ROOT}/release.lock" \
    SUB2API_MAINTENANCE_LOCK_FILE="${TEST_ROOT}/maintenance.lock" \
    SUB2API_RELEASE_MIN_FREE_BYTES=1 \
    SUB2API_RELEASE_BUILD_TIMEOUT_SECONDS=30 \
    SUB2API_RELEASE_BUILD_GOMAXPROCS=1 \
    SUB2API_RELEASE_BUILD_GO_PARALLELISM=1 \
    SUB2API_RELEASE_BUILD_GO_MEMORY_LIMIT=768MiB \
    SUB2API_RELEASE_ALLOW_PREEXISTING_DRAINING_CONTAINER="${ALLOW_DRAINING:-false}" \
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
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_WORK_ROOT="$WORK_ROOT" \
    SUB2API_RELEASE_LOG_DIR="${TEST_ROOT}/logs" \
    SUB2API_RELEASE_LOCK_FILE="${TEST_ROOT}/release.lock" \
    SUB2API_MAINTENANCE_LOCK_FILE="${TEST_ROOT}/maintenance.lock" \
    SUB2API_RELEASE_MIN_FREE_BYTES=1 \
    SUB2API_RELEASE_ALLOW_PREEXISTING_DRAINING_CONTAINER="${ALLOW_DRAINING:-false}" \
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
    PATH="$FAKE_BIN:$PATH" \
    FAKE_DOCKER_CALLS="$DOCKER_CALLS" \
    FAKE_BLUE_GREEN_ENV_LOG="$BLUE_GREEN_ENV_LOG" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_WORK_ROOT="$WORK_ROOT" \
    SUB2API_RELEASE_LOG_DIR="$TEST_ROOT/logs" \
    SUB2API_RELEASE_LOCK_FILE="$TEST_ROOT/release.lock" \
    SUB2API_MAINTENANCE_LOCK_FILE="$TEST_ROOT/maintenance.lock" \
    SUB2API_RELEASE_MIN_FREE_BYTES=1 \
    SUB2API_RELEASE_ALLOW_PREEXISTING_DRAINING_CONTAINER=true \
    SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE=external \
    SUB2API_EXTERNAL_RUNTIME_ENV_FILE="$EXTERNAL_RUNTIME_ENV_FILE" \
    SUB2API_EXTERNAL_CA_FILE="$EXTERNAL_CA_FILE" \
    /bin/bash "$SCRIPT" \
      --prebuilt \
      'sub2api:auto-test' \
      'abc123' \
      '0.1.test' \
      'https://example.invalid/health' \
      'external-rollback-test'
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

: >"$DOCKER_CALLS"
: >"$BLUE_GREEN_ENV_LOG"
external_rollback_output="$TEST_ROOT/external-rollback.log"
if run_external_github_prebuilt_release >"$external_rollback_output" 2>&1; then
  fail 'external-mode fake release unexpectedly passed a failing public health check'
fi
assert_contains "$external_rollback_output" 'Rollback completed'
[ "$(wc -l <"$BLUE_GREEN_ENV_LOG" | tr -d '[:space:]')" = 2 ] \
  || fail 'external release did not invoke the helper for both switch and rollback'
assert_contains "$BLUE_GREEN_ENV_LOG" \
  "mode=external runtime=$EXTERNAL_RUNTIME_ENV_FILE ca=$EXTERNAL_CA_FILE old=sub2api-green new=sub2api-blue"
assert_contains "$BLUE_GREEN_ENV_LOG" \
  "mode=external runtime=$EXTERNAL_RUNTIME_ENV_FILE ca=$EXTERNAL_CA_FILE old=sub2api-blue new=sub2api-green"
if grep -Fq 'mode=local' "$BLUE_GREEN_ENV_LOG"; then
  fail 'external rollback was permitted to fall back to local dependencies'
fi
if grep -Fq 'sentinel-not-for-logs' "$external_rollback_output" "$BLUE_GREEN_ENV_LOG"; then
  fail 'external runtime secret reached a release or helper log'
fi

printf 'Server release inactive-container guard tests passed.\n'
