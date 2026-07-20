#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
SCRIPT="${DEPLOY_DIR}/sub2api-autodeploy.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-autodeploy-test.XXXXXX")"
SOURCE_REPO="${TEST_ROOT}/source"
STATE_DIR="${TEST_ROOT}/state"
FAKE_BIN="${TEST_ROOT}/bin"
RELEASE_HELPER="${TEST_ROOT}/release-helper.sh"
RELEASE_CALLS="${TEST_ROOT}/release-calls.log"
FLOCK_LOG="${TEST_ROOT}/flock.log"

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
    sed -n '1,120p' "$file" >&2
    fail "expected '${expected}' in ${file}"
  fi
}

mkdir -p "$SOURCE_REPO/backend/cmd/server" "$FAKE_BIN"
printf '0.1.test\n' >"$SOURCE_REPO/backend/cmd/server/VERSION"
git -C "$SOURCE_REPO" init -q -b main
git -C "$SOURCE_REPO" add backend/cmd/server/VERSION
git -C "$SOURCE_REPO" \
  -c user.name='Sub2API Test' \
  -c user.email='sub2api-test@localhost' \
  commit -q -m 'test source'

cat >"$FAKE_BIN/flock" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_FLOCK_LOG"
[ "${FAKE_FLOCK_MODE:-success}" != 'fail' ]
EOF
chmod +x "$FAKE_BIN/flock"

cat >"$FAKE_BIN/sha256sum" <<'EOF'
#!/usr/bin/env bash
if [ -x /usr/bin/sha256sum ]; then
  exec /usr/bin/sha256sum "$@"
fi
exec /usr/bin/shasum -a 256 "$@"
EOF
chmod +x "$FAKE_BIN/sha256sum"

cat >"$RELEASE_HELPER" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_RELEASE_CALLS"
[ "${FAKE_RELEASE_RESULT:-failure}" = 'success' ]
EOF
chmod +x "$RELEASE_HELPER"

run_autodeploy() {
  env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_FLOCK_LOG="$FLOCK_LOG" \
    FAKE_FLOCK_MODE="${FAKE_FLOCK_MODE:-success}" \
    FAKE_RELEASE_CALLS="$RELEASE_CALLS" \
    FAKE_RELEASE_RESULT="${FAKE_RELEASE_RESULT:-failure}" \
    SUB2API_AUTODEPLOY_CONFIG_FILE="${TEST_ROOT}/missing.env" \
    SUB2API_APP_DIR="${TEST_ROOT}/app" \
    SUB2API_AUTODEPLOY_STATE_DIR="$STATE_DIR" \
    SUB2API_AUTODEPLOY_REPO_DIR="${STATE_DIR}/repo" \
    SUB2API_AUTODEPLOY_WORK_ROOT="${STATE_DIR}/worktrees" \
    SUB2API_AUTODEPLOY_LOCK_FILE="${TEST_ROOT}/autodeploy.lock" \
    SUB2API_SERVER_RELEASE_HELPER="$RELEASE_HELPER" \
    SUB2API_AUTODEPLOY_PRODUCTION_REMOTE=fork \
    SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL="$SOURCE_REPO" \
    SUB2API_AUTODEPLOY_PRODUCTION_BRANCH=main \
    SUB2API_AUTODEPLOY_MERGE_MAIN=false \
    SUB2API_AUTODEPLOY_MERGE_UPSTREAM=false \
    SUB2API_AUTODEPLOY_LOCK_WAIT_SECONDS=2 \
    SUB2API_AUTODEPLOY_FAILURE_RETRY_SECONDS=1800 \
    SUB2API_RELEASE_LOG_DIR="${TEST_ROOT}/logs" \
    /bin/bash "$SCRIPT" "$@"
}

lock_output="${TEST_ROOT}/lock-output.log"
if FAKE_FLOCK_MODE=fail run_autodeploy >"$lock_output" 2>&1; then
  fail 'lock contention was reported as success'
fi
assert_contains "$lock_output" 'still running after 2s; no release was attempted'
assert_contains "$FLOCK_LOG" '-w 2 9'

first_failure_output="${TEST_ROOT}/first-failure-output.log"
if run_autodeploy >"$first_failure_output" 2>&1; then
  fail 'release-helper failure was reported as success'
fi
[[ -s "${STATE_DIR}/last-failed.env" ]] || fail 'release failure state was not recorded'

cooldown_output="${TEST_ROOT}/cooldown-output.log"
if run_autodeploy >"$cooldown_output" 2>&1; then
  fail 'same-candidate cooldown was reported as success'
fi
assert_contains "$cooldown_output" 'the same candidate failed recently'
[[ "$(wc -l <"$RELEASE_CALLS" | tr -d ' ')" == '1' ]] \
  || fail 'release helper ran during the failure cooldown'

success_output="${TEST_ROOT}/success-output.log"
FAKE_RELEASE_RESULT=success run_autodeploy --force >"$success_output" 2>&1
assert_contains "$success_output" 'Automatic release completed'
[[ -s "${STATE_DIR}/last-successful.env" ]] || fail 'release success state was not recorded'
[[ ! -e "${STATE_DIR}/last-failed.env" ]] || fail 'failure state survived a successful release'

no_change_output="${TEST_ROOT}/no-change-output.log"
run_autodeploy >"$no_change_output" 2>&1
assert_contains "$no_change_output" 'No source change since the successful release'
[[ "$(wc -l <"$RELEASE_CALLS" | tr -d ' ')" == '2' ]] \
  || fail 'release helper ran for an already-successful source fingerprint'

printf 'Automatic release control-flow tests passed.\n'
