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
secret='must-not-appear-in-output'

if IMAGE_UPSCALE_API_KEY="$secret" bash "$SCRIPT" invalid-image >"$OUTPUT" 2>&1; then
  fail 'invalid image was accepted'
fi
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_INJECT_REJECTED' "$OUTPUT" || fail 'invalid image rejection drifted'
grep -q "$secret" "$OUTPUT" && fail 'invalid image rejection leaked the secret'

if bash "$SCRIPT" "$image" >"$OUTPUT" 2>&1; then
  fail 'missing secret was accepted'
fi
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_INJECT_REJECTED' "$OUTPUT" || fail 'missing secret rejection drifted'

if IMAGE_UPSCALE_API_KEY='contains whitespace' bash "$SCRIPT" "$image" >"$OUTPUT" 2>&1; then
  fail 'whitespace-bearing token was accepted'
fi
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_INJECT_REJECTED' "$OUTPUT" || fail 'token validation rejection drifted'

grep -Fq '/usr/bin/env -u IMAGE_UPSCALE_API_KEY /usr/bin/ssh' "$SCRIPT" || fail 'SSH child inherits the secret environment'
grep -Fq 'docker exec -i sub2api-upscale-vault /app/sub2api-vault-agent load' "$SCRIPT" || fail 'fixed remote load command is missing'
# shellcheck disable=SC2016 # Assert the source uses the fixed variables,
# rather than expanding test-process variables into the expected text.
grep -Fq 'ready_command="sudo -n $REMOTE_HELPER ready $image"' "$SCRIPT" || fail 'post-injection readiness gate is missing'

printf '%s\n' 'Image upscale Vault inject tests passed.'
