#!/usr/bin/env bash

set -Eeuo pipefail

TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-real-probe-test.XXXXXX")"
FAKE_BIN="$TEST_ROOT/bin"
KEY_FILE="$TEST_ROOT/release-probe-api-key"
CONFIG_LOG="$TEST_ROOT/curl-config.log"
mkdir -p "$FAKE_BIN"
trap 'rm -rf -- "$TEST_ROOT"' EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

cat >"$FAKE_BIN/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
case "${1:-}" in
  inspect)
    printf '172.30.0.2\n'
    ;;
  *)
    exit 1
    ;;
esac
EOF

cat >"$FAKE_BIN/realpath" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "${@: -1}"
EOF

cat >"$FAKE_BIN/stat" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
case "${2:-}" in
  '%u:%g:%a') printf '0:0:600\n' ;;
  *) printf '0\n' ;;
esac
EOF

cat >"$FAKE_BIN/curl" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
cat >"$FAKE_CURL_CONFIG_LOG"
output_file=""
for ((index = 1; index <= $#; index++)); do
  if [ "${!index}" = --output ]; then
    next=$((index + 1))
    output_file="${!next}"
  fi
done
[ -n "$output_file" ]
case "${FAKE_CURL_STATUS:-200}" in
  200)
    if [[ "$*" == *'/v1/models' ]]; then
      printf '{"data":[{"id":"gpt-5.6-sol"}]}\n' >"$output_file"
    else
      printf '{"id":"resp_probe"}\n' >"$output_file"
    fi
    printf '200\n'
    ;;
  *)
    printf '{"error":"upstream"}\n' >"$output_file"
    printf '%s\n' "$FAKE_CURL_STATUS"
    ;;
esac
EOF

chmod +x "$FAKE_BIN/docker" "$FAKE_BIN/realpath" "$FAKE_BIN/stat" "$FAKE_BIN/curl"
printf 'sk-test-release-probe-0123456789\n' >"$KEY_FILE"
chmod 600 "$KEY_FILE"

SCRIPT="$(cd "$(dirname "$0")/.." && pwd)/sub2api-real-request-probe.sh"
PATH="$FAKE_BIN:$PATH" \
  FAKE_CURL_CONFIG_LOG="$CONFIG_LOG" \
  "$SCRIPT" sub2api-green "$KEY_FILE" gpt-5.6-sol >"$TEST_ROOT/success.out"
grep -Fq 'models_status=200 responses_status=200' "$TEST_ROOT/success.out" \
  || fail 'successful real-request probe did not report both requests'
grep -Fq 'Authorization: Bearer sk-test-release-probe-0123456789' "$CONFIG_LOG" \
  || fail 'curl did not receive the API key through config stdin'

if PATH="$FAKE_BIN:$PATH" FAKE_CURL_CONFIG_LOG="$CONFIG_LOG" FAKE_CURL_STATUS=502 \
  "$SCRIPT" sub2api-green "$KEY_FILE" gpt-5.6-sol >"$TEST_ROOT/failure.out" 2>&1; then
  fail 'upstream failure was accepted by the real-request probe'
fi
grep -Fq 'authenticated models probe returned HTTP 502' "$TEST_ROOT/failure.out" \
  || fail 'upstream failure did not identify the failed request stage'

printf 'real-request-probe-test: PASS\n'
