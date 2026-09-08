#!/usr/bin/env bash

# Append or verify the non-secret unified-payment runtime block in the existing
# root-owned production config. The input contains public scope/key metadata
# only. This script never rewrites or prints the surrounding runtime file.

set -Eeuo pipefail

CONFIG_FILE="${SUB2API_AUTODEPLOY_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"
LOCK_FILE="${SUB2API_UNIFIED_PAYMENT_CONFIG_LOCK_FILE:-/var/lock/sub2api-unified-payment-config.lock}"
BEGIN_MARKER="# BEGIN SUB2API UNIFIED PAYMENT (managed)"
END_MARKER="# END SUB2API UNIFIED PAYMENT (managed)"
APPROVED_UNIFIED_PAYMENT_WEBHOOK_URL='https://api.turtleligpt.com/api/v1/payment/webhook/unified'

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
payment_methods='alipay'
payment_methods_supplied=false
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
webhook_url="$APPROVED_UNIFIED_PAYMENT_WEBHOOK_URL"
seen_keys='|'

line_count=0
while IFS= read -r line || [ -n "$line" ]; do
  line_count=$((line_count + 1))
  [ "$line_count" -le 14 ] || die
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
    UNIFIED_PAYMENT_PAYMENT_METHODS) payment_methods="$value"; payment_methods_supplied=true ;;
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
    UNIFIED_PAYMENT_WEBHOOK_URL) webhook_url="$value" ;;
    *) die ;;
  esac
done
[ "$line_count" -eq 12 ] || [ "$line_count" -eq 13 ] || [ "$line_count" -eq 14 ] || die
case "$payment_methods" in
  alipay|wechat_pay|alipay,wechat_pay|wechat_pay,alipay) ;;
  *) die ;;
esac

[ "$vault_volume" = sub2api_unified_payment_vault ] || die
case "$enabled" in
  true|false) ;;
  *) die ;;
esac
[ "$base_url" = https://pay.totools.cn ] || die
[ "$organization_id" = 84fc3e66-e959-4bc8-8d78-6f8c3d3483fb ] || die
[ "$product_id" = 00da03c5-bc5c-4edb-9d4c-c77da0e969d5 ] || die
webhook_key_id=''
case "$environment" in
  sandbox)
    [ "$app_id" = app.sub2.sandbox ] || die
    [ "$request_key_id" = sub2.request.sandbox.v1 ] || die
    [ "$request_vault_ref" = 'vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64' ] || die
    webhook_key_id='sub2.webhook.sandbox.v1'
    ;;
  live)
    [ "$app_id" = app.sub2.live ] || die
    [ "$request_key_id" = sub2.request.live.v1 ] || die
    [ "$request_vault_ref" = 'vault://secret/data/sub2api/unified-payment/live#request_private_key_base64' ] || die
    webhook_key_id='sub2.webhook.live.v1'
    ;;
  *) die ;;
esac
[ "$agent_socket" = /run/sub2api-payment-vault/public.sock ] || die
[ "$return_url" = https://www.turtleligpt.com/payment/result ] || die
[ "$webhook_url" = "$APPROVED_UNIFIED_PAYMENT_WEBHOOK_URL" ] || die
webhook_prefix="{\"${webhook_key_id}\":\""
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

payment_methods_line=''
if [ "$payment_methods_supplied" = true ]; then
  payment_methods_line="UNIFIED_PAYMENT_PAYMENT_METHODS='${payment_methods}'"$'\n'
fi
binding_block="$BEGIN_MARKER
SUB2API_UNIFIED_PAYMENT_VAULT_VOLUME='${vault_volume}'
UNIFIED_PAYMENT_ENABLED='${enabled}'
${payment_methods_line}UNIFIED_PAYMENT_BASE_URL='${base_url}'
UNIFIED_PAYMENT_ENVIRONMENT='${environment}'
UNIFIED_PAYMENT_ORGANIZATION_ID='${organization_id}'
UNIFIED_PAYMENT_PRODUCT_ID='${product_id}'
UNIFIED_PAYMENT_APP_ID='${app_id}'
UNIFIED_PAYMENT_REQUEST_KEY_ID='${request_key_id}'
UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF='${request_vault_ref}'
UNIFIED_PAYMENT_VAULT_AGENT_SOCKET='${agent_socket}'
UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON='${webhook_public_keys}'
UNIFIED_PAYMENT_RETURN_URL='${return_url}'
"
desired="${binding_block}UNIFIED_PAYMENT_WEBHOOK_URL='${webhook_url}'
$END_MARKER"
legacy_desired="${binding_block}$END_MARKER"
legacy_blank_webhook_desired="${binding_block}UNIFIED_PAYMENT_WEBHOOK_URL=''
$END_MARKER"
live_disabled_desired=''
if [ "$environment" = live ] && [ "$enabled" = true ]; then
  # Runtime enablement is a distinct explicit action after live registration.
  # The exact same public profile must already have been installed disabled;
  # this limited transition never changes the application purchase setting.
  live_disabled_desired="$(printf '%s\n' "$desired" | sed "s/^UNIFIED_PAYMENT_ENABLED='true'$/UNIFIED_PAYMENT_ENABLED='false'/")"
fi

exec 9>"$LOCK_FILE"
flock -n 9 || die
marker_count="$(grep -cF "$BEGIN_MARKER" "$CONFIG_FILE" || true)"
end_count="$(grep -cF "$END_MARKER" "$CONFIG_FILE" || true)"
case "$marker_count:$end_count" in
  0:0)
    # A live runtime may only be enabled after this exact public profile was
    # durably staged disabled. A first-install live=true request has no
    # registration/identity continuity to fence it, so fail closed.
    if [ "$environment" = live ] && [ "$enabled" = true ]; then
      die
    fi
    printf '\n%s\n' "$desired" >>"$CONFIG_FILE"
    ;;
  1:1)
    existing="$(sed -n "/^${BEGIN_MARKER}$/,/^${END_MARKER}$/p" "$CONFIG_FILE")"
    if [ "$existing" = "$desired" ] || [ "$existing" = "$legacy_desired" ] || [ "$existing" = "$legacy_blank_webhook_desired" ]; then
      :
    elif [ -n "$live_disabled_desired" ] && [ "$existing" = "$live_disabled_desired" ]; then
      tmp_file="$(mktemp "${CONFIG_FILE}.tmp.XXXXXX")" || die
      replacement_file="$(mktemp "${CONFIG_FILE}.replacement.XXXXXX")" || { rm -f -- "$tmp_file"; die; }
      chmod 600 "$tmp_file" "$replacement_file" || { rm -f -- "$tmp_file" "$replacement_file"; die; }
      printf '%s\n' "$desired" >"$replacement_file" || { rm -f -- "$tmp_file" "$replacement_file"; die; }
      if ! awk -v begin="$BEGIN_MARKER" -v end="$END_MARKER" -v replacement_file="$replacement_file" '
        $0 == begin {
          while ((getline line < replacement_file) > 0) print line
          close(replacement_file)
          in_block = 1
          next
        }
        in_block && $0 == end { in_block = 0; next }
        !in_block { print }
      ' "$CONFIG_FILE" >"$tmp_file"; then
        rm -f -- "$tmp_file" "$replacement_file"
        die
      fi
      rm -f -- "$replacement_file" || { rm -f -- "$tmp_file"; die; }
      mv -f -- "$tmp_file" "$CONFIG_FILE" || { rm -f -- "$tmp_file"; die; }
    else
      die
    fi
    ;;
  *) die ;;
esac

printf '%s\n' 'SUB2API_UNIFIED_PAYMENT_CONFIG_READY'
