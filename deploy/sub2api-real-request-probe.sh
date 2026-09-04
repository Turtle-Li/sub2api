#!/usr/bin/env bash

# Run one authenticated, non-streaming request against a candidate Sub2API
# container before blue-green traffic is switched. The API key is read from a
# root-only file and is passed to curl through its config stdin so it never
# appears in process arguments or logs.

set -Eeuo pipefail
umask 077

if [ "$#" -ne 3 ]; then
  echo "Usage: sub2api-real-request-probe.sh CONTAINER KEY_FILE MODEL" >&2
  exit 2
fi

CONTAINER="$1"
KEY_FILE="$2"
MODEL="$3"
APP_PORT="${SUB2API_RELEASE_REAL_REQUEST_PROBE_PORT:-8080}"
PATH_VALUE="${SUB2API_RELEASE_REAL_REQUEST_PROBE_PATH:-/responses}"
TIMEOUT_SECONDS="${SUB2API_RELEASE_REAL_REQUEST_PROBE_TIMEOUT_SECONDS:-45}"

die() {
  printf 'real-request-probe: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

case "$CONTAINER" in
  sub2api|sub2api-blue|sub2api-green) ;;
  *) die "unsupported candidate container" ;;
esac

case "$KEY_FILE" in
  /*) ;;
  *) die "key file must be an absolute path" ;;
esac
case "$KEY_FILE" in
  *$'\n'*|*$'\r'*|*'|'*|*,*) die "key file path contains unsupported characters" ;
esac

case "$MODEL" in
  ''|*[!A-Za-z0-9._:-]*) die "model contains unsupported characters" ;
esac
case "$PATH_VALUE" in
  /[A-Za-z0-9._~/-]*) ;;
  *) die "probe path must be an absolute path without a query" ;
esac
case "$PATH_VALUE" in
  *'?'*|*'#'*|*$'\n'*|*$'\r'*) die "probe path must not contain a query, fragment, or line break" ;
esac
case "$APP_PORT" in
  ''|*[!0-9]*) die "probe port must be numeric" ;
esac
[ "$APP_PORT" -ge 1 ] && [ "$APP_PORT" -le 65535 ] || die "probe port is out of range"
case "$TIMEOUT_SECONDS" in
  ''|*[!0-9]*) die "probe timeout must be numeric" ;
esac
[ "$TIMEOUT_SECONDS" -gt 0 ] || die "probe timeout must be positive"

for command_name in curl docker mktemp python3 realpath stat tr wc; do
  require_cmd "$command_name"
done

[ -f "$KEY_FILE" ] && [ ! -L "$KEY_FILE" ] || die "key file is missing or is a symlink"
CANONICAL_KEY_FILE="$(realpath -e -- "$KEY_FILE")" || die "key file cannot be resolved"
[ "$CANONICAL_KEY_FILE" = "$KEY_FILE" ] || die "key file must be canonical"
[ "$(stat -c '%u:%g:%a' "$KEY_FILE")" = '0:0:600' ] \
  || die "key file must be root-owned mode 0600"

key_line_count="$(wc -l <"$KEY_FILE" | tr -d '[:space:]')"
[ "$key_line_count" -eq 1 ] || die "key file must contain exactly one line"
API_KEY="$(tr -d '\r\n' <"$KEY_FILE")"
case "$API_KEY" in
  ''|*[!A-Za-z0-9._~-]*) die "key file contains unsupported characters" ;
esac
[ "${#API_KEY}" -ge 20 ] && [ "${#API_KEY}" -le 512 ] \
  || die "key file contains an invalid key length"

container_ip="$(docker inspect "$CONTAINER" --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' 2>/dev/null || true)"
case "$container_ip" in
  ''|*[!0-9.]*) die "candidate container has no IPv4 address" ;
esac

PROBE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-real-probe.XXXXXX")"
trap 'rm -rf -- "$PROBE_DIR"' EXIT INT TERM
chmod 700 "$PROBE_DIR"
BODY_FILE="$PROBE_DIR/request.json"
MODELS_FILE="$PROBE_DIR/models.json"
RESPONSE_FILE="$PROBE_DIR/response.json"
ERROR_FILE="$PROBE_DIR/curl.error"

cat >"$BODY_FILE" <<EOF
{"model":"$MODEL","input":[{"role":"user","content":[{"type":"input_text","text":"sub2api release probe"}]}],"max_output_tokens":1,"stream":false}
EOF
chmod 600 "$BODY_FILE"

curl_with_key() {
  local output_file="$1"
  shift
  # The API key travels in curl's config stream rather than an argv element.
  {
    printf 'header = "Authorization: Bearer %s"\n' "$API_KEY"
    printf 'header = "Content-Type: application/json"\n'
    printf 'header = "Host: api.turtleligpt.com"\n'
  } | curl --config - --noproxy '*' --silent --show-error \
    --connect-timeout 10 --max-time "$TIMEOUT_SECONDS" \
    --output "$output_file" --write-out '%{http_code}' "$@" \
    2>"$ERROR_FILE"
}

models_url="http://${container_ip}:${APP_PORT}/v1/models"
models_status="$(curl_with_key "$MODELS_FILE" --request GET "$models_url" || true)"
case "$models_status" in
  2??) ;;
  *) die "authenticated models probe returned HTTP ${models_status:-000}" ;
esac
python3 - "$MODELS_FILE" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if not isinstance(payload, dict) or not isinstance(payload.get("data"), list) or not payload["data"]:
    raise SystemExit("models response did not contain a non-empty data list")
PY

responses_url="http://${container_ip}:${APP_PORT}${PATH_VALUE}"
responses_status="$(curl_with_key "$RESPONSE_FILE" --request POST --data-binary "@$BODY_FILE" "$responses_url" || true)"
case "$responses_status" in
  2??) ;;
  *) die "authenticated responses probe returned HTTP ${responses_status:-000}" ;
esac
python3 - "$RESPONSE_FILE" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if not isinstance(payload, dict) or not isinstance(payload.get("id"), str) or not payload["id"]:
    raise SystemExit("responses response did not contain an id")
PY

printf 'real-request-probe: container=%s model=%s models_status=%s responses_status=%s\n' \
  "$CONTAINER" "$MODEL" "$models_status" "$responses_status"
