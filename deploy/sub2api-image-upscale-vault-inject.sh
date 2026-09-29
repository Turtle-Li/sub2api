#!/usr/bin/env bash

# Hash-pinned local infra-vault consumer. Each value exists only in this
# process and SSH stdin; it never enters an argument, file, Docker metadata,
# or the remote environment. The remote root-owned helper resolves the exact
# non-secret Vault references before passing each value to the private socket.

set -Eeuo pipefail
set +x

SSH_TARGET='sub2api-aws-candidate'
REMOTE_HELPER='/opt/sub2api/scripts/sub2api-image-upscale-vault-container.sh'

die() {
  printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_INJECT_REJECTED' >&2
  exit 1
}

case "$#" in
  1)
    # Preserve the established one-argument API-key injector. If either new
    # COS value is present, use the atomic three-value path so a partial
    # migration fails before any remote command is issued.
    if [ "${BATCH_IMAGE_DELIVERY_COS_ACCESS_KEY_ID+x}" = x ] \
      || [ "${BATCH_IMAGE_DELIVERY_COS_SECRET_ACCESS_KEY+x}" = x ]; then
      action=all
    else
      action=api
    fi
    image="$1"
    ;;
  2)
    action="$1"
    image="$2"
    ;;
  *) die ;;
esac
case "$action" in
  all|api|batch-cos|ready) ;;
  *) die ;;
esac
case "$image" in sub2api:prebuilt-*) ;; *) die ;; esac
revision="${image#sub2api:prebuilt-}"
[ "${#revision}" -eq 40 ] || die
case "$revision" in *[!0-9a-f]*) die ;; esac

clear_secret() {
  local variable="$1" value
  value="${!variable-}"
  if [ -n "$value" ]; then
    printf -v "$variable" '%*s' "${#value}" ''
  fi
  unset "$variable"
}

validate_secret() {
  local variable="$1" maximum="$2" reject_all_whitespace="$3" value trimmed
  value="${!variable-}"
  [ -n "$value" ] && [ "${#value}" -le "$maximum" ] || die
  case "$value" in
    *$'\r'*|*$'\n'*) die ;;
  esac
  trimmed="$(printf '%s' "$value" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
  [ "$trimmed" = "$value" ] || die
  if [ "$reject_all_whitespace" = true ]; then
    case "$value" in *[[:space:]]*) die ;; esac
  fi
}

api_key="${IMAGE_UPSCALE_API_KEY-}"
cos_access_key="${BATCH_IMAGE_DELIVERY_COS_ACCESS_KEY_ID-}"
cos_secret_key="${BATCH_IMAGE_DELIVERY_COS_SECRET_ACCESS_KEY-}"
cleanup() {
  clear_secret api_key
  clear_secret cos_access_key
  clear_secret cos_secret_key
  unset IMAGE_UPSCALE_API_KEY BATCH_IMAGE_DELIVERY_COS_ACCESS_KEY_ID BATCH_IMAGE_DELIVERY_COS_SECRET_ACCESS_KEY
}
trap cleanup EXIT

case "$action" in
  all)
    validate_secret api_key 512 true
    validate_secret cos_access_key 256 false
    validate_secret cos_secret_key 512 false
    remote_action=load-all
    ;;
  api)
    validate_secret api_key 512 true
    remote_action=load-api
    ;;
  batch-cos)
    validate_secret cos_access_key 256 false
    validate_secret cos_secret_key 512 false
    remote_action=load-batch-cos
    ;;
  ready)
    remote_action=ready
    ;;
esac
[ -x /usr/bin/ssh ] && [ -x /usr/bin/env ] || die

ssh_options=(
  -o BatchMode=yes
  -o ConnectTimeout=15
  -o IdentitiesOnly=yes
  -o LogLevel=ERROR
  -o ServerAliveCountMax=3
  -o ServerAliveInterval=10
  -o StrictHostKeyChecking=yes
)
remote_command="sudo -n $REMOTE_HELPER $remote_action $image"
ssh_without_secrets=(
  /usr/bin/env
  -u IMAGE_UPSCALE_API_KEY
  -u BATCH_IMAGE_DELIVERY_COS_ACCESS_KEY_ID
  -u BATCH_IMAGE_DELIVERY_COS_SECRET_ACCESS_KEY
  /usr/bin/ssh
  "${ssh_options[@]}"
  "$SSH_TARGET"
  "$remote_command"
)

case "$action" in
  all)
    printf '%s\n%s\n%s\n' "$api_key" "$cos_access_key" "$cos_secret_key" | "${ssh_without_secrets[@]}"
    printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_INJECTED'
    ;;
  api)
    printf '%s\n' "$api_key" | "${ssh_without_secrets[@]}"
    printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_LOADED'
    ;;
  batch-cos)
    printf '%s\n%s\n' "$cos_access_key" "$cos_secret_key" | "${ssh_without_secrets[@]}"
    printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_LOADED'
    ;;
  ready)
    "${ssh_without_secrets[@]}"
    printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_READY'
    ;;
esac
