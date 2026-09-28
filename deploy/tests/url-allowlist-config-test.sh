#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "$0")" && pwd)"
DEPLOY_DIR="$(cd "$TEST_DIR/.." && pwd)"
SCRIPT="${DEPLOY_DIR}/sub2api-url-allowlist-config.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-url-allowlist-config.XXXXXX")"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
CONFIG_FILE="${TEST_ROOT}/autodeploy.env"
LOCK_DIR="${TEST_ROOT}/lock"
LOCK_FILE="${LOCK_DIR}/sub2api-maintenance.lock"
OUTPUT="${TEST_ROOT}/output.log"
FAKE_BIN="${TEST_ROOT}/bin"
FLOCK_LOG="${TEST_ROOT}/flock.log"
APPROVED_HOSTS='api.openai.com,api.anthropic.com,api.kimi.com,api.moonshot.ai,api.moonshot.cn,open.bigmodel.cn,api.minimaxi.com,api.minimax.io,opencode.ai,generativelanguage.googleapis.com,cloudcode-pa.googleapis.com,*.openai.azure.com,100.121.157.55:18000'

cleanup() {
  status=$?
  rm -rf "$TEST_ROOT"
  exit "$status"
}
trap cleanup EXIT

fail() {
  printf 'URL allowlist config test failed: %s\n' "$*" >&2
  exit 1
}

mkdir -m 700 "$LOCK_DIR" "$FAKE_BIN"
cat >"${FAKE_BIN}/flock" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FLOCK_LOG"
exit 0
EOF
chmod 755 "${FAKE_BIN}/flock"
printf '%s\n' 'EXISTING_SECRET=must-not-be-printed-or-rewritten' >"$CONFIG_FILE"
chmod 600 "$CONFIG_FILE"

configuration() {
  cat <<EOF
SECURITY_URL_ALLOWLIST_ENABLED=false
SECURITY_URL_ALLOWLIST_ALLOW_INSECURE_HTTP=true
SECURITY_URL_ALLOWLIST_ALLOW_PRIVATE_HOSTS=true
SECURITY_URL_ALLOWLIST_UPSTREAM_HOSTS=${APPROVED_HOSTS}
EOF
}

run_config() {
  SUB2API_AUTODEPLOY_CONFIG_FILE="$CONFIG_FILE" \
    SUB2API_MAINTENANCE_LOCK_FILE="$LOCK_FILE" \
    SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1 \
    FLOCK_LOG="$FLOCK_LOG" \
    PATH="${FAKE_BIN}:${PATH}" \
    bash "$SCRIPT"
}

configuration | run_config >"$OUTPUT" 2>&1
grep -qx 'SUB2API_URL_ALLOWLIST_CONFIG_READY' "$OUTPUT" || fail 'approved config was rejected'
grep -qx 'EXISTING_SECRET=must-not-be-printed-or-rewritten' "$CONFIG_FILE" || fail 'surrounding config changed'
grep -Fqx "SECURITY_URL_ALLOWLIST_UPSTREAM_HOSTS='${APPROVED_HOSTS}'" "$CONFIG_FILE" || fail 'approved host list missing'
grep -qx -- '-n 8' "$FLOCK_LOG" || fail 'maintenance lock was not acquired'
[ ! -L "$CONFIG_FILE" ] || fail 'config became a symlink'

before="$(cksum "$CONFIG_FILE")"
configuration | run_config >"$OUTPUT" 2>&1
[ "$before" = "$(cksum "$CONFIG_FILE")" ] || fail 'idempotent verification rewrote config'

if configuration | sed 's/100.121.157.55:18000/100.121.157.55:18001/' | run_config >"$OUTPUT" 2>&1; then
  fail 'unapproved relay port was accepted'
fi
[ "$before" = "$(cksum "$CONFIG_FILE")" ] || fail 'rejected relay change modified config'
grep -q 'must-not-be-printed-or-rewritten' "$OUTPUT" && fail 'surrounding config leaked'

if configuration | sed '/ALLOW_PRIVATE_HOSTS/d' | run_config >"$OUTPUT" 2>&1; then
  fail 'incomplete config was accepted'
fi
[ "$before" = "$(cksum "$CONFIG_FILE")" ] || fail 'incomplete config modified file'

printf '%s\n' 'URL allowlist config tests passed.'
