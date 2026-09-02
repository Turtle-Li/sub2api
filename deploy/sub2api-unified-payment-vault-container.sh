#!/usr/bin/env bash

# Prepare or verify the production Sub2 unified-payment memory agent. The
# container has no network, no secret environment, a read-only root filesystem,
# and a private admin socket in tmpfs. The named volume exposes only public.sock
# to application containers; a key value reaches the agent later through the
# separately hash-pinned Vault activation consumer.

set -Eeuo pipefail

CONTAINER=sub2api-payment-vault
VOLUME=sub2api_unified_payment_vault
PUBLIC_DIR=/run/sub2api-payment-vault
ADMIN_DIR=/run/sub2api-payment-vault-admin
REQUEST_REF='vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64'

die() {
  printf '%s\n' 'SUB2API_PAYMENT_VAULT_CONTAINER_REJECTED' >&2
  exit 1
}

PAYMENT_VAULT_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAINTENANCE_LOCK_HELPER="${PAYMENT_VAULT_SCRIPT_DIR}/sub2api-maintenance-lock.sh"
[ -r "$MAINTENANCE_LOCK_HELPER" ] && [ ! -L "$MAINTENANCE_LOCK_HELPER" ] \
  || die
# shellcheck disable=SC1090,SC1091 # Installed alongside this root-owned executable.
. "$MAINTENANCE_LOCK_HELPER"
LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE}"

require_image() {
  local image="$1" revision platform source version
  case "$image" in
    sub2api:prebuilt-*) ;;
    *) die ;;
  esac
  revision="${image#sub2api:prebuilt-}"
  [ "${#revision}" -eq 40 ] || die
  case "$revision" in *[!0-9a-f]*) die ;; esac
  platform="$(docker image inspect "$image" --format '{{.Os}}/{{.Architecture}}')" || die
  [ "$platform" = linux/amd64 ] || die
  [ "$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" = "$revision" ] || die
  source="$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.source"}}')" || die
  [ "$source" = https://github.com/Turtle-Li/sub2api ] || die
  version="$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.version"}}')" || die
  [ -n "$version" ] || die
}

verify_container() {
  local image="$1" mounts command health security
  [ "$(docker container inspect "$CONTAINER" --format '{{.Config.Image}}')" = "$image" ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{.State.Running}}')" = true ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{.HostConfig.NetworkMode}}')" = none ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{.HostConfig.ReadonlyRootfs}}')" = true ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{.HostConfig.RestartPolicy.Name}}')" = unless-stopped ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{.HostConfig.PidsLimit}}')" = 64 ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{.HostConfig.Init}}')" = true ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{json .HostConfig.CapDrop}}')" = '["ALL"]' ] || return 1
  security="$(docker container inspect "$CONTAINER" --format '{{json .HostConfig.SecurityOpt}}')" || return 1
  [ "$security" = '["no-new-privileges"]' ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format "{{index .HostConfig.Tmpfs \"$ADMIN_DIR\"}}")" = 'rw,noexec,nosuid,nodev,size=1m,mode=0700,uid=1000,gid=1000' ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{index .HostConfig.Tmpfs "/tmp"}}')" = 'rw,noexec,nosuid,nodev,size=4m,mode=0700,uid=1000,gid=1000' ] || return 1
  mounts="$(docker container inspect "$CONTAINER" --format '{{range .Mounts}}{{printf "%s|%s|%s|%t\n" .Type .Name .Destination .RW}}{{end}}')" || return 1
  [ "$mounts" = "volume|$VOLUME|$PUBLIC_DIR|true" ] || return 1
  command="$(docker container inspect "$CONTAINER" --format '{{json .Config.Cmd}}')" || return 1
  [ "$command" = '["/app/sub2api-vault-agent","serve","--public-socket","/run/sub2api-payment-vault/public.sock","--admin-socket","/run/sub2api-payment-vault-admin/admin.sock","--allowed-ref","vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64"]' ] || return 1
  health="$(docker container inspect "$CONTAINER" --format '{{json .Config.Healthcheck.Test}}')" || return 1
  [ "$health" = '["CMD-SHELL","/app/sub2api-vault-agent check --public-socket /run/sub2api-payment-vault/public.sock"]' ] || return 1
}

case "${SUB2API_PAYMENT_VAULT_CONTAINER_ALLOW_NON_ROOT_FOR_TESTS:-0}" in
  1)
    # shellcheck disable=SC2034 # Read by the sourced maintenance-lock helper.
    SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1
    ;;
  0) [ "$(id -u)" -eq 0 ] || die ;;
  *) die ;;
esac
[ "$#" -eq 2 ] || die
action="$1"
image="$2"
case "$action" in prepare|ready) ;; *) die ;; esac
if ! sub2api_maintenance_lock_validate_configured_path "$LOCK_FILE"; then
  die
fi
for command_name in docker flock; do command -v "$command_name" >/dev/null 2>&1 || die; done
require_image "$image"

if ! sub2api_maintenance_lock_open "$LOCK_FILE"; then
  die
fi
flock -n "$SUB2API_MAINTENANCE_LOCK_FD" || die

if ! docker container inspect "$CONTAINER" >/dev/null 2>&1; then
  [ "$action" = prepare ] || die
  docker volume create "$VOLUME" >/dev/null || die
  docker run -d \
    --name "$CONTAINER" \
    --network none \
    --read-only \
    --init \
    --restart unless-stopped \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --pids-limit 64 \
    --mount "type=volume,source=$VOLUME,target=$PUBLIC_DIR" \
    --tmpfs "$ADMIN_DIR:rw,noexec,nosuid,nodev,size=1m,mode=0700,uid=1000,gid=1000" \
    --tmpfs '/tmp:rw,noexec,nosuid,nodev,size=4m,mode=0700,uid=1000,gid=1000' \
    --health-cmd "/app/sub2api-vault-agent check --public-socket $PUBLIC_DIR/public.sock" \
    --health-interval 5s \
    --health-timeout 3s \
    --health-retries 6 \
    --health-start-period 2s \
    "$image" \
    /app/sub2api-vault-agent serve \
    --public-socket "$PUBLIC_DIR/public.sock" \
    --admin-socket "$ADMIN_DIR/admin.sock" \
    --allowed-ref "$REQUEST_REF" >/dev/null || die
fi

verify_container "$image" || die
if [ "$action" = ready ]; then
  [ "$(docker container inspect "$CONTAINER" --format '{{.State.Health.Status}}')" = healthy ] || die
  printf '%s\n' 'SUB2API_PAYMENT_VAULT_CONTAINER_READY'
else
  printf '%s\n' 'SUB2API_PAYMENT_VAULT_CONTAINER_WAITING_FOR_INJECTION'
fi
