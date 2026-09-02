#!/usr/bin/env bash

# Append or verify the non-secret unified-payment runtime block in the existing
# root-owned production config. The input contains public scope/key metadata
# only. This script never rewrites or prints the surrounding runtime file.

set -Eeuo pipefail

CONFIG_FILE="${SUB2API_AUTODEPLOY_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"
LOCK_FILE="${SUB2API_UNIFIED_PAYMENT_CONFIG_LOCK_FILE:-/var/lock/sub2api-unified-payment-config.lock}"
BEGIN_MARKER="# BEGIN SUB2API UNIFIED PAYMENT (managed)"
END_MARKER="# END SUB2API UNIFIED PAYMENT (managed)"

die() {
  printf '%s\n' 'SUB2API_UNIFIED_PAYMENT_CONFIG_REJECTED' >&2
  exit 1
}

[ "$#" -eq 0 ] || die
[ -f "$CONFIG_FILE" ] && [ ! -L "$CONFIG_FILE" ] || die
[ "$(stat -c '%u' "$CONFIG_FILE")" = 0 ] || die
[ "$(stat -c '%a' "$CONFIG_FILE")" = 600 ] || die

vault_volume=''
enabled=''
base_url=''
environment=''
organization_id=''
product_id=''
app_id=''
request_key_id=''
request_vault_ref=''
agent_socket=''
webhook_public_keys=''
return_url=''
seen_keys='|'

line_count=0
while IFS= read -r line || [ -n "$line" ]; do
  line_count=$((line_count + 1))
  [ "$line_count" -le 12 ] || die
  case "$line" in
    *$'\r'*) die ;;
    *=*) ;;
    *) die ;;
  esac
  key="${line%%=*}"
  value="${line#*=}"
  case "$seen_keys" in *"|$key|"*) die ;; esac
  seen_keys="${seen_keys}${key}|"
  [ -n "$value" ] || die
  case "$value" in *"'"*|*$'\r'*|*$'\n'*) die ;; esac
  case "$key" in
    SUB2API_UNIFIED_PAYMENT_VAULT_VOLUME) vault_volume="$value" ;;
    UNIFIED_PAYMENT_ENABLED) enabled="$value" ;;
    UNIFIED_PAYMENT_BASE_URL) base_url="$value" ;;
    UNIFIED_PAYMENT_ENVIRONMENT) environment="$value" ;;
    UNIFIED_PAYMENT_ORGANIZATION_ID) organization_id="$value" ;;
    UNIFIED_PAYMENT_PRODUCT_ID) product_id="$value" ;;
    UNIFIED_PAYMENT_APP_ID) app_id="$value" ;;
    UNIFIED_PAYMENT_REQUEST_KEY_ID) request_key_id="$value" ;;
    UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF) request_vault_ref="$value" ;;
    UNIFIED_PAYMENT_VAULT_AGENT_SOCKET) agent_socket="$value" ;;
    UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON) webhook_public_keys="$value" ;;
    UNIFIED_PAYMENT_RETURN_URL) return_url="$value" ;;
    *) die ;;
  esac
done
[ "$line_count" -eq 12 ] || die

[ "$vault_volume" = sub2api_unified_payment_vault ] || die
[ "$enabled" = true ] || die
[ "$base_url" = https://pay.totools.cn ] || die
[ "$environment" = sandbox ] || die
[ "$organization_id" = 84fc3e66-e959-4bc8-8d78-6f8c3d3483fb ] || die
[ "$product_id" = 00da03c5-bc5c-4edb-9d4c-c77da0e969d5 ] || die
[ "$app_id" = app.sub2.sandbox ] || die
[ "$request_key_id" = sub2.request.sandbox.v1 ] || die
[ "$request_vault_ref" = 'vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64' ] || die
[ "$agent_socket" = /run/sub2api-payment-vault/public.sock ] || die
[ "$return_url" = https://www.turtleligpt.com/payment/result ] || die
webhook_prefix='{"sub2.webhook.sandbox.v1":"'
case "$webhook_public_keys" in
  "$webhook_prefix"*'"}') ;;
  *) die ;;
esac
webhook_key="${webhook_public_keys#"$webhook_prefix"}"
webhook_key="${webhook_key%\"\}}"
[ "${#webhook_key}" -eq 44 ] || die
case "$webhook_key" in
  *[!A-Za-z0-9+/=]*|*=*=*|*==*) die ;;
esac
[ "${webhook_key#???????????????????????????????????????????}" = = ] || die

desired="$BEGIN_MARKER
SUB2API_UNIFIED_PAYMENT_VAULT_VOLUME='${vault_volume}'
UNIFIED_PAYMENT_ENABLED='${enabled}'
UNIFIED_PAYMENT_BASE_URL='${base_url}'
UNIFIED_PAYMENT_ENVIRONMENT='${environment}'
UNIFIED_PAYMENT_ORGANIZATION_ID='${organization_id}'
UNIFIED_PAYMENT_PRODUCT_ID='${product_id}'
UNIFIED_PAYMENT_APP_ID='${app_id}'
UNIFIED_PAYMENT_REQUEST_KEY_ID='${request_key_id}'
UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF='${request_vault_ref}'
UNIFIED_PAYMENT_VAULT_AGENT_SOCKET='${agent_socket}'
UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON='${webhook_public_keys}'
UNIFIED_PAYMENT_RETURN_URL='${return_url}'
$END_MARKER"

exec 9>"$LOCK_FILE"
flock -n 9 || die
marker_count="$(grep -cF "$BEGIN_MARKER" "$CONFIG_FILE" || true)"
end_count="$(grep -cF "$END_MARKER" "$CONFIG_FILE" || true)"
case "$marker_count:$end_count" in
  0:0)
    printf '\n%s\n' "$desired" >>"$CONFIG_FILE"
    ;;
  1:1)
    existing="$(sed -n "/^${BEGIN_MARKER}$/,/^${END_MARKER}$/p" "$CONFIG_FILE")"
    [ "$existing" = "$desired" ] || die
    ;;
  *) die ;;
esac

printf '%s\n' 'SUB2API_UNIFIED_PAYMENT_CONFIG_READY'
