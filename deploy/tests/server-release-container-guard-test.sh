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
printf '#!/usr/bin/env bash\nexit 0\n' >"${APP_DIR}/scripts/sub2api-blue-green-release.sh"
printf '#!/usr/bin/env bash\nexit 0\n' >"${APP_DIR}/scripts/sub2api-drain-monitor.sh"
chmod +x \
  "${APP_DIR}/scripts/sub2api-blue-green-release.sh" \
  "${APP_DIR}/scripts/sub2api-drain-monitor.sh"

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
  logs)
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
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_WORK_ROOT="$WORK_ROOT" \
    SUB2API_RELEASE_LOG_DIR="${TEST_ROOT}/logs" \
    SUB2API_RELEASE_LOCK_FILE="${TEST_ROOT}/release.lock" \
    SUB2API_RELEASE_MIN_FREE_BYTES=1 \
    SUB2API_RELEASE_BUILD_TIMEOUT_SECONDS=30 \
    SUB2API_RELEASE_ALLOW_PREEXISTING_DRAINING_CONTAINER="${ALLOW_DRAINING:-false}" \
    /bin/bash "$SCRIPT" \
      "$SOURCE_DIR" \
      'sub2api:auto-test' \
      'abc123' \
      '0.1.test' \
      'https://example.invalid/health' \
      'guard-test'
}

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

printf 'Server release inactive-container guard tests passed.\n'
