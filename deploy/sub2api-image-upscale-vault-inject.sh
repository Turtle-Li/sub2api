#!/usr/bin/env bash

# Hash-pinned local infra-vault consumer. The bearer exists only in this
# process and SSH stdin; it never enters an argument, file, or remote
# environment.

set -Eeuo pipefail

VAULT_REF='vault://secret/data/infrastructure/office-mini-upscale-api-public-key-20260928#api_key'
SSH_TARGET='sub2api-aws-candidate'
REMOTE_HELPER='/opt/sub2api/scripts/sub2api-image-upscale-vault-container.sh'

die() {
  printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_INJECT_REJECTED' >&2
  exit 1
}

[ "$#" -eq 1 ] || die
image="$1"
case "$image" in sub2api:prebuilt-*) ;; *) die ;; esac
revision="${image#sub2api:prebuilt-}"
[ "${#revision}" -eq 40 ] || die
case "$revision" in *[!0-9a-f]*) die ;; esac

api_key="${IMAGE_UPSCALE_API_KEY:-}"
[ -n "$api_key" ] && [ "${#api_key}" -le 512 ] || die
# Shell environment variables cannot contain NUL. Reject whitespace so the
# bearer remains a single opaque token across the stdin-only transport.
case "$api_key" in *[[:space:]]*) die ;; esac
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
load_command="sudo -n docker exec -i sub2api-upscale-vault /app/sub2api-vault-agent load --admin-socket /run/sub2api-upscale-vault-admin/admin.sock --ref $VAULT_REF"
ready_command="sudo -n $REMOTE_HELPER ready $image"

printf '%s' "$api_key" |
  /usr/bin/env -u IMAGE_UPSCALE_API_KEY /usr/bin/ssh "${ssh_options[@]}" "$SSH_TARGET" "$load_command"
printf -v api_key '%*s' "${#api_key}" ''
unset api_key IMAGE_UPSCALE_API_KEY

# shellcheck disable=SC2029 # The command is assembled only from fixed values
# and the locally validated image revision, then intentionally expanded here.
/usr/bin/ssh "${ssh_options[@]}" "$SSH_TARGET" "$ready_command"
printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_INJECTED'
