#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$(cd "$TEST_DIR/.." && pwd)/sub2api-unified-payment-config.sh"
TEST_ROOT="$(mktemp -d /tmp/sub2api-unified-config.XXXXXX)"
CONFIG_FILE="$TEST_ROOT/autodeploy.env"
LOCK_FILE="$TEST_ROOT/config.lock"
FAKE_BIN="$TEST_ROOT/bin"
OUTPUT="$TEST_ROOT/output.log"
cleanup() {
  status=$?
  if [ "$status" -ne 0 ] && [ -f "$OUTPUT" ]; then
    sed -n '1,120p' "$OUTPUT" >&2
  fi
  rm -rf "$TEST_ROOT"
  exit "$status"
}
trap cleanup EXIT

fail() {
  printf 'Unified payment config test failed: %s\n' "$*" >&2
  exit 1
}

mkdir -p "$FAKE_BIN"
cat >"$FAKE_BIN/stat" <<'EOF'
#!/usr/bin/env bash
[ "$1" = -c ] || exit 1
case "$2" in
  %u) printf '0\n' ;;
  %a) printf '600\n' ;;
  *) exit 1 ;;
esac
EOF
cat >"$FAKE_BIN/flock" <<'EOF'
#!/usr/bin/env bash
[ "$1" = -n ] && [ "$2" = 9 ]
EOF
chmod +x "$FAKE_BIN/stat"
chmod +x "$FAKE_BIN/flock"
printf '%s\n' 'EXISTING_SECRET=must-not-be-printed-or-rewritten' >"$CONFIG_FILE"
chmod 600 "$CONFIG_FILE"

webhook_key='AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA='
configuration() {
  cat <<EOF
SUB2API_UNIFIED_PAYMENT_VAULT_VOLUME=sub2api_unified_payment_vault
UNIFIED_PAYMENT_ENABLED=true
UNIFIED_PAYMENT_BASE_URL=https://pay.totools.cn
UNIFIED_PAYMENT_ENVIRONMENT=sandbox
UNIFIED_PAYMENT_ORGANIZATION_ID=84fc3e66-e959-4bc8-8d78-6f8c3d3483fb
UNIFIED_PAYMENT_PRODUCT_ID=00da03c5-bc5c-4edb-9d4c-c77da0e969d5
UNIFIED_PAYMENT_APP_ID=app.sub2.sandbox
UNIFIED_PAYMENT_REQUEST_KEY_ID=sub2.request.sandbox.v1
UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF=vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64
UNIFIED_PAYMENT_VAULT_AGENT_SOCKET=/run/sub2api-payment-vault/public.sock
UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON={"sub2.webhook.sandbox.v1":"${webhook_key}"}
UNIFIED_PAYMENT_RETURN_URL=https://www.turtleligpt.com/payment/result
EOF
}

run_config() {
  PATH="$FAKE_BIN:$PATH" \
    SUB2API_AUTODEPLOY_CONFIG_FILE="$CONFIG_FILE" \
    SUB2API_UNIFIED_PAYMENT_CONFIG_LOCK_FILE="$LOCK_FILE" \
    bash "$SCRIPT"
}

configuration | run_config >"$OUTPUT" 2>&1
grep -qx 'SUB2API_UNIFIED_PAYMENT_CONFIG_READY' "$OUTPUT" || fail 'first install failed'
grep -qx "EXISTING_SECRET=must-not-be-printed-or-rewritten" "$CONFIG_FILE" || fail 'existing config changed'
grep -qx "UNIFIED_PAYMENT_ENABLED='true'" "$CONFIG_FILE" || fail 'managed config missing'
grep -Fqx "UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON='{\"sub2.webhook.sandbox.v1\":\"${webhook_key}\"}'" "$CONFIG_FILE" \
  || fail 'Webhook public key config missing'

before="$(cksum "$CONFIG_FILE")"
configuration | run_config >"$OUTPUT" 2>&1
after="$(cksum "$CONFIG_FILE")"
[ "$before" = "$after" ] || fail 'idempotent verification rewrote the file'

webhook_key='BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB='
if configuration | run_config >"$OUTPUT" 2>&1; then
  fail 'conflicting managed block was accepted'
fi
[ "$before" = "$(cksum "$CONFIG_FILE")" ] || fail 'rejected update changed the file'
grep -q 'must-not-be-printed-or-rewritten' "$OUTPUT" && fail 'surrounding config leaked to output'

printf 'Unified payment config tests passed.\n'
