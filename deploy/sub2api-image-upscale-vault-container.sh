#!/usr/bin/env bash

# Prepare or verify the Image 2.5 upscale memory agent. The long-lived
# container has no network, no secret environment, and exposes only its public
# Unix socket through the named volume. A separate hash-pinned Vault consumer
# is responsible for the later in-memory injection.
#
# An existing sidecar is never replaced here. Any profile migration belongs to
# a separately coordinated, lock-owning maintenance operation.

set -Eeuo pipefail

CONTAINER=sub2api-upscale-vault
VOLUME=sub2api_image_upscale_vault
PUBLIC_DIR=/run/sub2api-upscale-vault
ADMIN_DIR=/run/sub2api-upscale-vault-admin
VAULT_REF='vault://secret/data/infrastructure/office-mini-upscale-api-public-key-20260928#api_key'

die() {
  printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_REJECTED' >&2
  exit 1
}

UPSCALE_VAULT_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAINTENANCE_LOCK_HELPER="${UPSCALE_VAULT_SCRIPT_DIR}/sub2api-maintenance-lock.sh"
[ -r "$MAINTENANCE_LOCK_HELPER" ] && [ ! -L "$MAINTENANCE_LOCK_HELPER" ] || die
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
  case "$revision" in
    *[!0-9a-f]*) die ;;
  esac

  platform="$(docker image inspect "$image" --format '{{.Os}}/{{.Architecture}}')" || die
  [ "$platform" = linux/amd64 ] || die
  [ "$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" = "$revision" ] || die
  source="$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.source"}}')" || die
  [ "$source" = https://github.com/Turtle-Li/sub2api ] || die
  version="$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.version"}}')" || die
  [ -n "$version" ] || die
}

agent_command_json() {
  printf '["/app/sub2api-vault-agent","serve","--public-socket","%s/public.sock","--admin-socket","%s/admin.sock","--allowed-ref","%s"]' \
    "$PUBLIC_DIR" "$ADMIN_DIR" "$VAULT_REF"
}

prepare_public_volume() {
  local image="$1"

  # The normal image entrypoint can repair ownership only when it starts as
  # root. The long-lived agent deliberately cannot, so this bounded helper
  # changes only the root of the one named socket volume before it is started.
  docker run --rm \
    --network none \
    --read-only \
    --cap-drop ALL \
    --cap-add CHOWN \
    --cap-add FOWNER \
    --security-opt no-new-privileges \
    --pids-limit 16 \
    --user 0:0 \
    --mount "type=volume,source=$VOLUME,target=$PUBLIC_DIR" \
    --entrypoint /bin/sh \
    "$image" \
    -ec '
      socket_dir="$1"
      test -d "$socket_dir"
      test ! -L "$socket_dir"
      chown 1000:1000 "$socket_dir"
      chmod 0700 "$socket_dir"
    ' -- "$PUBLIC_DIR" >/dev/null
}

verify_container() {
  local image="$1" mounts command health security user

  [ "$(docker container inspect "$CONTAINER" --format '{{.Config.Image}}')" = "$image" ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{.State.Running}}')" = true ] || return 1
  user="$(docker container inspect "$CONTAINER" --format '{{.Config.User}}')" || return 1
  [ "$user" = 1000:1000 ] || return 1
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
  [ "$command" = "$(agent_command_json)" ] || return 1
  health="$(docker container inspect "$CONTAINER" --format '{{json .Config.Healthcheck.Test}}')" || return 1
  [ "$health" = '["CMD-SHELL","/app/sub2api-vault-agent check --public-socket '"$PUBLIC_DIR"'/public.sock"]' ] || return 1
}

case "${SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_ALLOW_NON_ROOT_FOR_TESTS:-0}" in
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
case "$action" in
  prepare|ready|ready-auto) ;;
  *) die ;;
esac

if ! sub2api_maintenance_lock_validate_configured_path "$LOCK_FILE"; then
  die
fi
for command_name in docker flock; do
  command -v "$command_name" >/dev/null 2>&1 || die
done
require_image "$image"

if [ "$action" = ready-auto ]; then
  # Release/recovery callers already hold the canonical maintenance lock and
  # pass its open descriptor across exec. Direct invocations have no matching
  # descriptor, so acquire the lock normally. Either path is non-blocking.
  inherited_identity="$(sub2api_maintenance_lock_descriptor_identity 2>/dev/null || true)"
  path_identity="$(sub2api_maintenance_lock_identity "$LOCK_FILE" 2>/dev/null || true)"
  if [ -z "$inherited_identity" ] || [ "$inherited_identity" != "$path_identity" ]; then
    if ! sub2api_maintenance_lock_open "$LOCK_FILE"; then
      die
    fi
  fi
else
  if ! sub2api_maintenance_lock_open "$LOCK_FILE"; then
    die
  fi
fi
flock -n "$SUB2API_MAINTENANCE_LOCK_FD" || die

if docker container inspect "$CONTAINER" >/dev/null 2>&1; then
  :
else
  [ "$action" = prepare ] || die
  docker volume create "$VOLUME" >/dev/null || die
  prepare_public_volume "$image" || die
  docker run -d \
    --name "$CONTAINER" \
    --network none \
    --read-only \
    --init \
    --restart unless-stopped \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --pids-limit 64 \
    --user 1000:1000 \
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
    --allowed-ref "$VAULT_REF" >/dev/null || die
fi

verify_container "$image" || die
if [ "$action" = ready ] || [ "$action" = ready-auto ]; then
  [ "$(docker container inspect "$CONTAINER" --format '{{.State.Health.Status}}')" = healthy ] || die
  printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_READY'
else
  printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_WAITING_FOR_INJECTION'
fi
