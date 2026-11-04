#!/usr/bin/env bash
# Dedicated memory-only day-pass machine bearer. No payment/Feishu/media refs.
set -Eeuo pipefail
set +x
CONTAINER=sub2api-daypass-vault
VOLUME=sub2api_daypass_vault
PUBLIC_DIR=/run/sub2api-daypass-vault
ADMIN_DIR=/run/sub2api-daypass-vault-admin
BEARER_REF='vault://secret/data/ops/newapi/daypass#bearer'
CONFIG_FILE="${SUB2API_AUTODEPLOY_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"
die() { printf '%s\n' SUB2API_DAYPASS_VAULT_REJECTED >&2; exit 1; }
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[ -r "$SCRIPT_DIR/sub2api-maintenance-lock.sh" ] && [ ! -L "$SCRIPT_DIR/sub2api-maintenance-lock.sh" ] || die
# shellcheck disable=SC1090,SC1091
. "$SCRIPT_DIR/sub2api-maintenance-lock.sh"
file_owner_mode() {
  stat -c '%u:%a' "$1" 2>/dev/null || stat -f '%u:%Lp' "$1" 2>/dev/null
}


load_root_runtime_configuration() {
  local owner=0
  if [ "${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-0}" = 1 ]; then owner="$(id -u)"; fi
  [ -f "$CONFIG_FILE" ] && [ ! -L "$CONFIG_FILE" ] || die
  [ "$(realpath -e "$CONFIG_FILE")" = "$CONFIG_FILE" ] || die
  [ "$(file_owner_mode "$CONFIG_FILE")" = "$owner:600" ] || die
  # shellcheck disable=SC1090
  . "$CONFIG_FILE"
  set +x
  [ "${SHOP_DAYPASS_ENABLED:-false}" = true ] || die
  [[ "${SHOP_DAYPASS_PRODUCT_ID:-}" =~ ^[1-9][0-9]*$ ]] || die
  [ "${SHOP_DAYPASS_BEARER+x}" != x ] || die
  [ "${DAYPASS_SHOP_BEARER+x}" != x ] || die
}
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
  printf '["/app/sub2api-vault-agent","serve","--public-socket","%s/public.sock","--admin-socket","%s/admin.sock","--allowed-ref","%s"]' "$PUBLIC_DIR" "$ADMIN_DIR" "$BEARER_REF"
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
  local image="$1" mounts command health security user tmpfs expected_tmpfs

  [ "$(docker container inspect "$CONTAINER" --format '{{.Config.Image}}')" = "$image" ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{.State.Running}}')" = true ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{json .Config.Entrypoint}}')" = '["/app/docker-entrypoint.sh"]' ] || return 1
  user="$(docker container inspect "$CONTAINER" --format '{{.Config.User}}')" || return 1
  [ "$user" = 1000:1000 ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{.HostConfig.NetworkMode}}')" = none ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{.HostConfig.ReadonlyRootfs}}')" = true ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{.HostConfig.RestartPolicy.Name}}')" = unless-stopped ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{.HostConfig.PidsLimit}}')" = 64 ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{.HostConfig.Init}}')" = true ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{json .HostConfig.CapDrop}}')" = '["ALL"]' ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{json .HostConfig.CapAdd}}')" = null ] || return 1
  [ "$(docker container inspect "$CONTAINER" --format '{{.HostConfig.Privileged}}')" = false ] || return 1
  security="$(docker container inspect "$CONTAINER" --format '{{json .HostConfig.SecurityOpt}}')" || return 1
  [ "$security" = '["no-new-privileges"]' ] || return 1
  expected_tmpfs="$(printf '%s|rw,noexec,nosuid,nodev,size=1m,mode=0700,uid=1000,gid=1000\n/tmp|rw,noexec,nosuid,nodev,size=4m,mode=0700,uid=1000,gid=1000' "$ADMIN_DIR")"
  tmpfs="$(docker container inspect "$CONTAINER" --format '{{range $path, $options := .HostConfig.Tmpfs}}{{printf "%s|%s\n" $path $options}}{{end}}')" || return 1
  [ "$tmpfs" = "$expected_tmpfs" ] || return 1
  mounts="$(docker container inspect "$CONTAINER" --format '{{range .Mounts}}{{printf "%s|%s|%s|%t\n" .Type .Name .Destination .RW}}{{end}}')" || return 1
  [ "$mounts" = "volume|$VOLUME|$PUBLIC_DIR|true" ] || return 1
  command="$(docker container inspect "$CONTAINER" --format '{{json .Config.Cmd}}')" || return 1
  [ "$command" = "$(agent_command_json)" ] || return 1
  health="$(docker container inspect "$CONTAINER" --format '{{json .Config.Healthcheck.Test}}')" || return 1
  [ "$health" = '["CMD-SHELL","/app/sub2api-vault-agent check --public-socket '"$PUBLIC_DIR"'/public.sock"]' ] || return 1
}


[ "$(id -u)" = 0 ] || [ "${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-0}" = 1 ] || die
[ "$#" = 2 ] || die
action="$1"
image="$2"
case "$action" in prepare|ready|ready-auto|load) ;; *) die ;; esac
load_root_runtime_configuration
require_image "$image"
LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE}"
sub2api_maintenance_lock_validate_configured_path "$LOCK_FILE" || die
inherited_identity="$(sub2api_maintenance_lock_descriptor_identity 2>/dev/null || true)"
path_identity="$(sub2api_maintenance_lock_identity "$LOCK_FILE" 2>/dev/null || true)"
if [ "$action" != ready-auto ] || [ -z "$inherited_identity" ] || [ "$inherited_identity" != "$path_identity" ]; then
  sub2api_maintenance_lock_open "$LOCK_FILE" || die
fi
flock -n "$SUB2API_MAINTENANCE_LOCK_FD" || die
if ! docker container inspect "$CONTAINER" >/dev/null 2>&1; then
  [ "$action" = prepare ] || die
  docker volume create "$VOLUME" >/dev/null || die
  prepare_public_volume "$image" || die
  docker run -d --name "$CONTAINER" --network none --read-only --init --restart unless-stopped \
    --cap-drop ALL --security-opt no-new-privileges --pids-limit 64 --user 1000:1000 \
    --mount "type=volume,source=$VOLUME,target=$PUBLIC_DIR" \
    --tmpfs "$ADMIN_DIR:rw,noexec,nosuid,nodev,size=1m,mode=0700,uid=1000,gid=1000" \
    --tmpfs '/tmp:rw,noexec,nosuid,nodev,size=4m,mode=0700,uid=1000,gid=1000' \
    --health-cmd "/app/sub2api-vault-agent check --public-socket $PUBLIC_DIR/public.sock" \
    --health-interval 5s --health-timeout 3s --health-retries 6 --health-start-period 2s \
    "$image" /app/sub2api-vault-agent serve --public-socket "$PUBLIC_DIR/public.sock" \
    --admin-socket "$ADMIN_DIR/admin.sock" --allowed-ref "$BEARER_REF" >/dev/null || die
fi
verify_container "$image" || die
case "$action" in
  load)
    bearer=''
    trap 'bearer=""; unset bearer' EXIT
    IFS= read -r bearer || die
    [[ "$bearer" =~ ^[a-f0-9]{64}$ ]] || die
    if IFS= read -r extra || [ -n "$extra" ]; then die; fi
    printf '%s' "$bearer" | docker exec -i "$CONTAINER" /app/sub2api-vault-agent load \
      --admin-socket "$ADMIN_DIR/admin.sock" --ref "$BEARER_REF" >/dev/null 2>&1 || die
    docker exec "$CONTAINER" /app/sub2api-vault-agent check --public-socket "$PUBLIC_DIR/public.sock" >/dev/null 2>&1 || die
    printf '%s\n' SUB2API_DAYPASS_VAULT_READY
    ;;
  ready|ready-auto)
    [ "$(docker inspect "$CONTAINER" --format '{{.State.Health.Status}}')" = healthy ] || die
    printf '%s\n' SUB2API_DAYPASS_VAULT_READY
    ;;
  prepare) printf '%s\n' SUB2API_DAYPASS_VAULT_WAITING_FOR_INJECTION ;;
esac
