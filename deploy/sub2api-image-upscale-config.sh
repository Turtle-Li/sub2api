#!/usr/bin/env bash

# Install or switch the fixed, non-secret Image 2.5 upscale runtime block in
# the existing production release configuration. The first installation must
# be disabled; later calls may change only the enabled flag.

set -Eeuo pipefail

CONFIG_FILE="${SUB2API_AUTODEPLOY_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"
BEGIN_MARKER="# BEGIN SUB2API IMAGE UPSCALE (managed)"
END_MARKER="# END SUB2API IMAGE UPSCALE (managed)"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAINTENANCE_LOCK_HELPER="${SCRIPT_DIR}/sub2api-maintenance-lock.sh"
TEMP_FILE=""

die() {
  printf '%s\n' 'SUB2API_IMAGE_UPSCALE_CONFIG_REJECTED' >&2
  exit 1
}

cleanup() {
  if [ -n "$TEMP_FILE" ]; then
    rm -f -- "$TEMP_FILE"
  fi
}
trap cleanup EXIT

file_owner_mode() {
  stat -c '%u:%a' "$1" 2>/dev/null || stat -f '%u:%Lp' "$1" 2>/dev/null
}

[ "$#" -eq 0 ] || die
[ -r "$MAINTENANCE_LOCK_HELPER" ] && [ ! -L "$MAINTENANCE_LOCK_HELPER" ] || die
# shellcheck disable=SC1090,SC1091 # Installed beside the root-owned helper.
. "$MAINTENANCE_LOCK_HELPER"
LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE}"
if ! sub2api_maintenance_lock_validate_configured_path "$LOCK_FILE"; then
  die
fi

[ -f "$CONFIG_FILE" ] && [ ! -L "$CONFIG_FILE" ] || die
expected_owner=0
if [ "${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-0}" = 1 ]; then
  expected_owner="$(id -u)"
fi
[ "$(file_owner_mode "$CONFIG_FILE")" = "${expected_owner}:600" ] || die

vault_volume=''
enabled=''
base_url=''
vault_ref=''
agent_socket=''
request_timeout=''
job_timeout=''
poll_interval=''
retry_max=''
max_concurrent=''
max_queue=''
max_result_bytes=''
seen_keys='|'
line_count=0

while IFS= read -r line || [ -n "$line" ]; do
  line_count=$((line_count + 1))
  [ "$line_count" -le 12 ] || die
  case "$line" in
    *$'\r'*|*$'\n'*) die ;;
    *=*) ;;
    *) die ;;
  esac
  key="${line%%=*}"
  value="${line#*=}"
  case "$seen_keys" in *"|$key|"*) die ;; esac
  seen_keys="${seen_keys}${key}|"
  [ -n "$value" ] || die
  case "$value" in *"'"*) die ;; esac
  case "$key" in
    SUB2API_IMAGE_UPSCALE_VAULT_VOLUME) vault_volume="$value" ;;
    IMAGE_UPSCALE_ENABLED) enabled="$value" ;;
    IMAGE_UPSCALE_BASE_URL) base_url="$value" ;;
    IMAGE_UPSCALE_API_KEY_VAULT_REF) vault_ref="$value" ;;
    IMAGE_UPSCALE_VAULT_AGENT_SOCKET) agent_socket="$value" ;;
    IMAGE_UPSCALE_REQUEST_TIMEOUT_SECONDS) request_timeout="$value" ;;
    IMAGE_UPSCALE_JOB_TIMEOUT_SECONDS) job_timeout="$value" ;;
    IMAGE_UPSCALE_POLL_INTERVAL_MS) poll_interval="$value" ;;
    IMAGE_UPSCALE_RETRY_MAX) retry_max="$value" ;;
    IMAGE_UPSCALE_MAX_CONCURRENT) max_concurrent="$value" ;;
    IMAGE_UPSCALE_MAX_QUEUE) max_queue="$value" ;;
    IMAGE_UPSCALE_MAX_RESULT_BYTES) max_result_bytes="$value" ;;
    *) die ;;
  esac
done

[ "$line_count" -eq 12 ] || die
[ "$vault_volume" = sub2api_image_upscale_vault ] || die
case "$enabled" in true|false) ;; *) die ;; esac
[ "$base_url" = https://hcmac-mini.tailfc4ed7.ts.net ] || die
[ "$vault_ref" = 'vault://secret/data/infrastructure/office-mini-upscale-api-public-key-20260928#api_key' ] || die
[ "$agent_socket" = /run/sub2api-upscale-vault/public.sock ] || die
[ "$request_timeout" = 30 ] || die
[ "$job_timeout" = 900 ] || die
[ "$poll_interval" = 500 ] || die
[ "$retry_max" = 3 ] || die
[ "$max_concurrent" = 1 ] || die
[ "$max_queue" = 8 ] || die
[ "$max_result_bytes" = 134217728 ] || die

desired="$BEGIN_MARKER
SUB2API_IMAGE_UPSCALE_VAULT_VOLUME='${vault_volume}'
IMAGE_UPSCALE_ENABLED='${enabled}'
IMAGE_UPSCALE_BASE_URL='${base_url}'
IMAGE_UPSCALE_API_KEY_VAULT_REF='${vault_ref}'
IMAGE_UPSCALE_VAULT_AGENT_SOCKET='${agent_socket}'
IMAGE_UPSCALE_REQUEST_TIMEOUT_SECONDS='${request_timeout}'
IMAGE_UPSCALE_JOB_TIMEOUT_SECONDS='${job_timeout}'
IMAGE_UPSCALE_POLL_INTERVAL_MS='${poll_interval}'
IMAGE_UPSCALE_RETRY_MAX='${retry_max}'
IMAGE_UPSCALE_MAX_CONCURRENT='${max_concurrent}'
IMAGE_UPSCALE_MAX_QUEUE='${max_queue}'
IMAGE_UPSCALE_MAX_RESULT_BYTES='${max_result_bytes}'
$END_MARKER"
if [ "$enabled" = true ]; then
  alternate="$(printf '%s\n' "$desired" | sed "s/^IMAGE_UPSCALE_ENABLED='true'$/IMAGE_UPSCALE_ENABLED='false'/")"
else
  alternate="$(printf '%s\n' "$desired" | sed "s/^IMAGE_UPSCALE_ENABLED='false'$/IMAGE_UPSCALE_ENABLED='true'/")"
fi

if ! sub2api_maintenance_lock_open "$LOCK_FILE"; then
  die
fi
flock -n "$SUB2API_MAINTENANCE_LOCK_FD" || die

begin_count="$(grep -cF "$BEGIN_MARKER" "$CONFIG_FILE" || true)"
end_count="$(grep -cF "$END_MARKER" "$CONFIG_FILE" || true)"
case "$begin_count:$end_count" in
  0:0)
    [ "$enabled" = false ] || die
    TEMP_FILE="$(mktemp "${CONFIG_FILE}.tmp.XXXXXX")" || die
    chmod 600 "$TEMP_FILE" || die
    chown "$expected_owner" "$TEMP_FILE" || die
    {
      cat "$CONFIG_FILE"
      printf '\n%s\n' "$desired"
    } >"$TEMP_FILE" || die
    mv -f -- "$TEMP_FILE" "$CONFIG_FILE" || die
    TEMP_FILE=""
    ;;
  1:1)
    existing="$(sed -n "/^${BEGIN_MARKER}$/,/^${END_MARKER}$/p" "$CONFIG_FILE")"
    if [ "$existing" = "$desired" ]; then
      :
    elif [ "$existing" = "$alternate" ]; then
      TEMP_FILE="$(mktemp "${CONFIG_FILE}.tmp.XXXXXX")" || die
      chmod 600 "$TEMP_FILE" || die
      chown "$expected_owner" "$TEMP_FILE" || die
      if ! awk -v begin="$BEGIN_MARKER" -v end="$END_MARKER" -v enabled="$enabled" '
        $0 == begin { in_block = 1 }
        in_block && /^IMAGE_UPSCALE_ENABLED=/ {
          print "IMAGE_UPSCALE_ENABLED=\047" enabled "\047"
          next
        }
        { print }
        in_block && $0 == end { in_block = 0 }
      ' "$CONFIG_FILE" >"$TEMP_FILE"; then
        die
      fi
      mv -f -- "$TEMP_FILE" "$CONFIG_FILE" || die
      TEMP_FILE=""
    else
      die
    fi
    ;;
  *) die ;;
esac

[ "$(file_owner_mode "$CONFIG_FILE")" = "${expected_owner}:600" ] || die
printf '%s\n' 'SUB2API_IMAGE_UPSCALE_CONFIG_READY'
