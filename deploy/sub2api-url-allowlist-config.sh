#!/usr/bin/env bash

# Install the reviewed non-secret URL allowlist runtime block used by the
# production Grok relay. The exact Tailnet host is intentionally frozen so a
# config-only rollout cannot broaden outbound access.

set -Eeuo pipefail

CONFIG_FILE="${SUB2API_AUTODEPLOY_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"
BEGIN_MARKER="# BEGIN SUB2API URL ALLOWLIST (managed)"
END_MARKER="# END SUB2API URL ALLOWLIST (managed)"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAINTENANCE_LOCK_HELPER="${SCRIPT_DIR}/sub2api-maintenance-lock.sh"
APPROVED_UPSTREAM_HOSTS='api.openai.com,api.anthropic.com,api.kimi.com,api.moonshot.ai,api.moonshot.cn,open.bigmodel.cn,api.minimaxi.com,api.minimax.io,opencode.ai,generativelanguage.googleapis.com,cloudcode-pa.googleapis.com,*.openai.azure.com,100.121.157.55:18000'
TEMP_FILE=""

die() {
  printf '%s\n' 'SUB2API_URL_ALLOWLIST_CONFIG_REJECTED' >&2
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
sub2api_maintenance_lock_validate_configured_path "$LOCK_FILE" || die

[ -f "$CONFIG_FILE" ] && [ ! -L "$CONFIG_FILE" ] || die
expected_owner=0
if [ "${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-0}" = 1 ]; then
  expected_owner="$(id -u)"
fi
[ "$(file_owner_mode "$CONFIG_FILE")" = "${expected_owner}:600" ] || die

enabled=""
allow_insecure_http=""
allow_private_hosts=""
upstream_hosts=""
seen_keys='|'
line_count=0
while IFS= read -r line || [ -n "$line" ]; do
  line_count=$((line_count + 1))
  [ "$line_count" -le 4 ] || die
  case "$line" in *$'\r'*|*$'\n'*) die ;; *=*) ;; *) die ;; esac
  key="${line%%=*}"
  value="${line#*=}"
  case "$seen_keys" in *"|$key|"*) die ;; esac
  seen_keys="${seen_keys}${key}|"
  [ -n "$value" ] || die
  case "$value" in *"'"*) die ;; esac
  case "$key" in
    SECURITY_URL_ALLOWLIST_ENABLED) enabled="$value" ;;
    SECURITY_URL_ALLOWLIST_ALLOW_INSECURE_HTTP) allow_insecure_http="$value" ;;
    SECURITY_URL_ALLOWLIST_ALLOW_PRIVATE_HOSTS) allow_private_hosts="$value" ;;
    SECURITY_URL_ALLOWLIST_UPSTREAM_HOSTS) upstream_hosts="$value" ;;
    *) die ;;
  esac
done

[ "$line_count" -eq 4 ] || die
[ "$enabled" = false ] || die
[ "$allow_insecure_http" = true ] || die
[ "$allow_private_hosts" = true ] || die
[ "$upstream_hosts" = "$APPROVED_UPSTREAM_HOSTS" ] || die

desired="$BEGIN_MARKER
SECURITY_URL_ALLOWLIST_ENABLED='false'
SECURITY_URL_ALLOWLIST_ALLOW_INSECURE_HTTP='true'
SECURITY_URL_ALLOWLIST_ALLOW_PRIVATE_HOSTS='true'
SECURITY_URL_ALLOWLIST_UPSTREAM_HOSTS='${APPROVED_UPSTREAM_HOSTS}'
$END_MARKER"

sub2api_maintenance_lock_open "$LOCK_FILE" || die
flock -n "$SUB2API_MAINTENANCE_LOCK_FD" || die
begin_count="$(grep -cF "$BEGIN_MARKER" "$CONFIG_FILE" || true)"
end_count="$(grep -cF "$END_MARKER" "$CONFIG_FILE" || true)"
case "$begin_count:$end_count" in
  0:0)
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
    [ "$existing" = "$desired" ] || die
    ;;
  *) die ;;
esac

[ "$(file_owner_mode "$CONFIG_FILE")" = "${expected_owner}:600" ] || die
printf '%s\n' 'SUB2API_URL_ALLOWLIST_CONFIG_READY'
