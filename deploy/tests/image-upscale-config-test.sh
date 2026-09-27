#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "$0")" && pwd)"
DEPLOY_DIR="$(cd "$TEST_DIR/.." && pwd)"
SCRIPT="${DEPLOY_DIR}/sub2api-image-upscale-config.sh"
ENV_EXAMPLE="${DEPLOY_DIR}/.env.example"
TMP_BASE="${TMPDIR:-/tmp}"
TEST_ROOT="$(mktemp -d "${TMP_BASE%/}/sub2api-image-upscale-config.XXXXXX")"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
CONFIG_FILE="${TEST_ROOT}/autodeploy.env"
LOCK_DIR="${TEST_ROOT}/lock"
LOCK_FILE="${LOCK_DIR}/sub2api-maintenance.lock"
OUTPUT="${TEST_ROOT}/output.log"
FAKE_BIN="${TEST_ROOT}/bin"
FLOCK_LOG="${TEST_ROOT}/flock.log"

cleanup() {
  status=$?
  rm -rf "$TEST_ROOT"
  exit "$status"
}
trap cleanup EXIT

fail() {
  printf 'Image upscale config test failed: %s\n' "$*" >&2
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
  enabled="$1"
  cat <<EOF
SUB2API_IMAGE_UPSCALE_VAULT_VOLUME=sub2api_image_upscale_vault
IMAGE_UPSCALE_ENABLED=${enabled}
IMAGE_UPSCALE_BASE_URL=https://hcmac-mini.tailfc4ed7.ts.net
IMAGE_UPSCALE_API_KEY_VAULT_REF=vault://secret/data/infrastructure/office-mini-upscale-api-public-key-20260928#api_key
IMAGE_UPSCALE_VAULT_AGENT_SOCKET=/run/sub2api-upscale-vault/public.sock
IMAGE_UPSCALE_REQUEST_TIMEOUT_SECONDS=30
IMAGE_UPSCALE_JOB_TIMEOUT_SECONDS=900
IMAGE_UPSCALE_POLL_INTERVAL_MS=500
IMAGE_UPSCALE_RETRY_MAX=3
IMAGE_UPSCALE_MAX_CONCURRENT=1
IMAGE_UPSCALE_MAX_QUEUE=8
IMAGE_UPSCALE_MAX_RESULT_BYTES=134217728
EOF
}

example_configuration="$({
  awk '
    /^SUB2API_IMAGE_UPSCALE_VAULT_VOLUME=/ { capture = 1 }
    capture && /^[A-Z0-9_]+=/ {
      print
      count++
      if (count == 12) exit
    }
  ' "$ENV_EXAMPLE"
})"
[ "$example_configuration" = "$(configuration false)" ] \
  || fail '.env.example image-upscale block drifted from the fixed helper contract'

run_config() {
  SUB2API_AUTODEPLOY_CONFIG_FILE="$CONFIG_FILE" \
    SUB2API_MAINTENANCE_LOCK_FILE="$LOCK_FILE" \
    SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1 \
    FLOCK_LOG="$FLOCK_LOG" \
    PATH="${FAKE_BIN}:${PATH}" \
    bash "$SCRIPT"
}

before="$(cksum "$CONFIG_FILE")"
if configuration true | run_config >"$OUTPUT" 2>&1; then
  fail 'first install enabled the feature'
fi
[ "$before" = "$(cksum "$CONFIG_FILE")" ] || fail 'rejected first enable changed config'

configuration false | run_config >"$OUTPUT" 2>&1
grep -qx 'SUB2API_IMAGE_UPSCALE_CONFIG_READY' "$OUTPUT" || fail 'disabled install failed'
grep -qx 'EXISTING_SECRET=must-not-be-printed-or-rewritten' "$CONFIG_FILE" || fail 'surrounding config changed'
grep -qx "IMAGE_UPSCALE_ENABLED='false'" "$CONFIG_FILE" || fail 'disabled block missing'
[ ! -L "$CONFIG_FILE" ] || fail 'config became a symlink'
grep -qx -- '-n 8' "$FLOCK_LOG" || fail 'maintenance lock was not acquired'

before="$(cksum "$CONFIG_FILE")"
configuration false | run_config >"$OUTPUT" 2>&1
[ "$before" = "$(cksum "$CONFIG_FILE")" ] || fail 'idempotent verification rewrote config'

configuration true | run_config >"$OUTPUT" 2>&1
grep -qx "IMAGE_UPSCALE_ENABLED='true'" "$CONFIG_FILE" || fail 'explicit enable failed'
grep -qx 'EXISTING_SECRET=must-not-be-printed-or-rewritten' "$CONFIG_FILE" || fail 'enable changed surrounding config'

configuration false | run_config >"$OUTPUT" 2>&1
grep -qx "IMAGE_UPSCALE_ENABLED='false'" "$CONFIG_FILE" || fail 'rollback disable failed'

before="$(cksum "$CONFIG_FILE")"
if configuration false | sed 's#hcmac-mini.tailfc4ed7.ts.net#wrong.example.test#' | run_config >"$OUTPUT" 2>&1; then
  fail 'unapproved gateway was accepted'
fi
[ "$before" = "$(cksum "$CONFIG_FILE")" ] || fail 'invalid gateway changed config'
grep -q 'must-not-be-printed-or-rewritten' "$OUTPUT" && fail 'surrounding config leaked'

if { configuration false; printf '%s\n' 'IMAGE_UPSCALE_ENABLED=false'; } | run_config >"$OUTPUT" 2>&1; then
  fail 'duplicate or oversized input was accepted'
fi
[ "$before" = "$(cksum "$CONFIG_FILE")" ] || fail 'invalid duplicate input changed config'

printf '%s\n' 'Image upscale config tests passed.'
