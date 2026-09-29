#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$(cd "$TEST_DIR/.." && pwd)/sub2api-image-upscale-vault-inject.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-image-upscale-vault-inject.XXXXXX")"
OUTPUT="${TEST_ROOT}/output.log"

cleanup() {
  status=$?
  rm -rf "$TEST_ROOT"
  exit "$status"
}
trap cleanup EXIT

fail() {
  printf 'Image upscale Vault inject test failed: %s\n' "$*" >&2
  exit 1
}

image='sub2api:prebuilt-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
api_secret='must-not-appear-api-secret'
access_secret='must-not-appear-access-secret'
cos_secret='must-not-appear-cos-secret'

if IMAGE_UPSCALE_API_KEY="$api_secret" \
  BATCH_IMAGE_DELIVERY_COS_ACCESS_KEY_ID="$access_secret" \
  BATCH_IMAGE_DELIVERY_COS_SECRET_ACCESS_KEY="$cos_secret" \
  bash "$SCRIPT" invalid-image >"$OUTPUT" 2>&1; then
  fail 'invalid image was accepted'
fi
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_INJECT_REJECTED' "$OUTPUT" || fail 'invalid image rejection drifted'
for secret in "$api_secret" "$access_secret" "$cos_secret"; do
  grep -q "$secret" "$OUTPUT" && fail 'invalid image rejection leaked a secret'
done

if bash "$SCRIPT" "$image" >"$OUTPUT" 2>&1; then
  fail 'missing injection set was accepted'
fi
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_INJECT_REJECTED' "$OUTPUT" || fail 'missing secret rejection drifted'

if IMAGE_UPSCALE_API_KEY='contains whitespace' bash "$SCRIPT" "$image" >"$OUTPUT" 2>&1; then
  fail 'whitespace-bearing token was accepted'
fi
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_INJECT_REJECTED' "$OUTPUT" || fail 'token validation rejection drifted'

if BATCH_IMAGE_DELIVERY_COS_ACCESS_KEY_ID="$access_secret" \
  bash "$SCRIPT" batch-cos "$image" >"$OUTPUT" 2>&1; then
  fail 'partial Batch Image COS credential set was accepted'
fi
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_INJECT_REJECTED' "$OUTPUT" || fail 'partial COS rejection drifted'

if IMAGE_UPSCALE_API_KEY="$api_secret" \
  BATCH_IMAGE_DELIVERY_COS_ACCESS_KEY_ID="$access_secret" \
  bash "$SCRIPT" "$image" >"$OUTPUT" 2>&1; then
  fail 'single-argument partial COS migration was accepted'
fi
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_INJECT_REJECTED' "$OUTPUT" || fail 'single-argument partial COS rejection drifted'

grep -Fq -- '-u IMAGE_UPSCALE_API_KEY' "$SCRIPT" || fail 'SSH child inherits the upscale secret environment'
grep -Fq -- '-u BATCH_IMAGE_DELIVERY_COS_ACCESS_KEY_ID' "$SCRIPT" || fail 'SSH child inherits the COS access-key environment'
grep -Fq -- '-u BATCH_IMAGE_DELIVERY_COS_SECRET_ACCESS_KEY' "$SCRIPT" || fail 'SSH child inherits the COS secret-key environment'
grep -Fq 'action=api' "$SCRIPT" || fail 'legacy single-key injection path is missing'
grep -Fq 'remote_action=load-all' "$SCRIPT" || fail 'combined stdin-only load action is missing'
grep -Fq 'remote_action=load-batch-cos' "$SCRIPT" || fail 'independent COS load action is missing'
grep -Fq 'remote_action=ready' "$SCRIPT" || fail 'post-injection readiness action is missing'
if grep -Fq -- '--ref vault://' "$SCRIPT"; then
  fail 'local injector hardcodes a Vault reference instead of using remote root configuration'
fi

printf '%s\n' 'Image upscale Vault inject tests passed.'
