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

live_configuration() {
  enabled="$1"
  cat <<EOF
SUB2API_UNIFIED_PAYMENT_VAULT_VOLUME=sub2api_unified_payment_vault
UNIFIED_PAYMENT_ENABLED=${enabled}
UNIFIED_PAYMENT_BASE_URL=https://pay.totools.cn
UNIFIED_PAYMENT_ENVIRONMENT=live
UNIFIED_PAYMENT_ORGANIZATION_ID=84fc3e66-e959-4bc8-8d78-6f8c3d3483fb
UNIFIED_PAYMENT_PRODUCT_ID=00da03c5-bc5c-4edb-9d4c-c77da0e969d5
UNIFIED_PAYMENT_APP_ID=app.sub2.live
UNIFIED_PAYMENT_REQUEST_KEY_ID=sub2.request.live.v1
UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF=vault://secret/data/sub2api/unified-payment/live#request_private_key_base64
UNIFIED_PAYMENT_VAULT_AGENT_SOCKET=/run/sub2api-payment-vault/public.sock
UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON={"sub2.webhook.live.v1":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}
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
grep -qx "UNIFIED_PAYMENT_WEBHOOK_URL='https://api.turtleligpt.com/api/v1/payment/webhook/unified'" "$CONFIG_FILE" \
  || fail 'approved Webhook URL config missing'

before="$(cksum "$CONFIG_FILE")"
configuration | run_config >"$OUTPUT" 2>&1
after="$(cksum "$CONFIG_FILE")"
[ "$before" = "$after" ] || fail 'idempotent verification rewrote the file'

# A managed block written before the optional Webhook URL existed remains
# accepted as-is; the release helper supplies the pinned endpoint to the
# candidate container for this legacy input.
sed -i.bak '/^UNIFIED_PAYMENT_WEBHOOK_URL=/d' "$CONFIG_FILE"
rm -f "$CONFIG_FILE.bak"
before="$(cksum "$CONFIG_FILE")"
configuration | run_config >"$OUTPUT" 2>&1
after="$(cksum "$CONFIG_FILE")"
[ "$before" = "$after" ] || fail 'legacy managed block was rewritten'

webhook_key='BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB='
if configuration | run_config >"$OUTPUT" 2>&1; then
  fail 'conflicting managed block was accepted'
fi
[ "$before" = "$(cksum "$CONFIG_FILE")" ] || fail 'rejected update changed the file'
grep -q 'must-not-be-printed-or-rewritten' "$OUTPUT" && fail 'surrounding config leaked to output'

# A 13-line input may supply the optional endpoint without changing the
# existing 12 required fields.
printf '%s\n' 'EXISTING_SECRET=must-not-be-printed-or-rewritten' >"$CONFIG_FILE"
webhook_key='AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA='
{ configuration; printf '%s\n' 'UNIFIED_PAYMENT_WEBHOOK_URL=https://api.turtleligpt.com/api/v1/payment/webhook/unified'; } | run_config >"$OUTPUT" 2>&1
grep -qx "UNIFIED_PAYMENT_WEBHOOK_URL='https://api.turtleligpt.com/api/v1/payment/webhook/unified'" "$CONFIG_FILE" \
  || fail 'optional endpoint was not accepted'

# Both independent optional fields are accepted together (14 lines).
printf '%s\n' 'EXISTING_SECRET=must-not-be-printed-or-rewritten' >"$CONFIG_FILE"
{ configuration; printf '%s\n' 'UNIFIED_PAYMENT_PAYMENT_METHODS=alipay,wechat_pay'; printf '%s\n' 'UNIFIED_PAYMENT_WEBHOOK_URL=https://api.turtleligpt.com/api/v1/payment/webhook/unified'; } | run_config >"$OUTPUT" 2>&1
grep -qx "UNIFIED_PAYMENT_PAYMENT_METHODS='alipay,wechat_pay'" "$CONFIG_FILE" || fail 'method allow-list missing'
grep -qx "UNIFIED_PAYMENT_WEBHOOK_URL='https://api.turtleligpt.com/api/v1/payment/webhook/unified'" "$CONFIG_FILE" \
  || fail 'approved endpoint missing from 14-line input'
before="$(cksum "$CONFIG_FILE")"
if { configuration; printf '%s\n' 'UNIFIED_PAYMENT_PAYMENT_METHODS=stripe'; } | run_config >"$OUTPUT" 2>&1; then
  fail 'unsupported method accepted'
fi
[ "$before" = "$(cksum "$CONFIG_FILE")" ] || fail 'invalid methods changed config'
if { configuration; printf '%s\n' 'UNIFIED_PAYMENT_WEBHOOK_URL=https://wrong.example.com/api/v1/payment/webhook/unified'; } | run_config >"$OUTPUT" 2>&1; then
  fail 'unapproved webhook endpoint accepted'
fi
[ "$before" = "$(cksum "$CONFIG_FILE")" ] || fail 'invalid webhook endpoint changed config'

# A live public profile can be staged with its runtime gateway disabled while
# registration is completed. The later explicit false->true transition keeps
# every public identity value unchanged and never rewrites global payment UI
# settings that live outside the managed block.
printf '%s\n' 'EXISTING_SECRET=must-not-be-printed-or-rewritten' 'PAYMENT_ENABLED=false' >"$CONFIG_FILE"
before="$(cksum "$CONFIG_FILE")"
if live_configuration true | run_config >"$OUTPUT" 2>&1; then
  fail 'first-ever live runtime enable was accepted'
fi
grep -qx 'SUB2API_UNIFIED_PAYMENT_CONFIG_REJECTED' "$OUTPUT" || fail 'first-ever live runtime enable had wrong rejection'
[ "$before" = "$(cksum "$CONFIG_FILE")" ] || fail 'rejected first-ever live enable changed config'

live_configuration false | run_config >"$OUTPUT" 2>&1
grep -qx "UNIFIED_PAYMENT_ENABLED='false'" "$CONFIG_FILE" || fail 'disabled live profile was not written'
grep -qx "UNIFIED_PAYMENT_APP_ID='app.sub2.live'" "$CONFIG_FILE" || fail 'live app scope missing'
grep -qx "UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF='vault://secret/data/sub2api/unified-payment/live#request_private_key_base64'" "$CONFIG_FILE" \
  || fail 'live Vault reference missing'
grep -Fqx "UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON='{"'"'sub2.webhook.live.v1'"'":"'"'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA='"'"}'" "$CONFIG_FILE" \
  || fail 'live Webhook key metadata missing'
grep -qx 'PAYMENT_ENABLED=false' "$CONFIG_FILE" || fail 'runtime staging changed global payment setting'

live_configuration true | run_config >"$OUTPUT" 2>&1
grep -qx "UNIFIED_PAYMENT_ENABLED='true'" "$CONFIG_FILE" || fail 'explicit live runtime enable was not applied'
grep -qx 'PAYMENT_ENABLED=false' "$CONFIG_FILE" || fail 'runtime enable changed global payment setting'
before="$(cksum "$CONFIG_FILE")"
if live_configuration true | sed 's/app\.sub2\.live/app.sub2.sandbox/' | run_config >"$OUTPUT" 2>&1; then
  fail 'sandbox app identity was accepted for live profile'
fi
[ "$before" = "$(cksum "$CONFIG_FILE")" ] || fail 'invalid live profile changed config'

printf 'Unified payment config tests passed.\n'
