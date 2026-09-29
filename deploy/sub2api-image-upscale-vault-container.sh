#!/usr/bin/env bash

# Prepare or verify the Image 2.5 upscale memory agent. The long-lived
# container has no network, no secret environment, and exposes only its public
# Unix socket through the named volume. A separate hash-pinned Vault consumer
# is responsible for the later in-memory injection.
#
# An existing sidecar is never replaced here. Any profile migration belongs to
# a separately coordinated, lock-owning maintenance operation.

set -Eeuo pipefail
set +x

CONTAINER=sub2api-upscale-vault
VOLUME=sub2api_image_upscale_vault
PUBLIC_DIR=/run/sub2api-upscale-vault
ADMIN_DIR=/run/sub2api-upscale-vault-admin
UPSCAPE_API_KEY_VAULT_REF='vault://secret/data/infrastructure/office-mini-upscale-api-public-key-20260928#api_key'
CONFIG_FILE="${SUB2API_AUTODEPLOY_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"
COS_ACCESS_KEY_VAULT_REF=''
COS_SECRET_ACCESS_KEY_VAULT_REF=''

die() {
  printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_REJECTED' >&2
  exit 1
}

UPSCALE_VAULT_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAINTENANCE_LOCK_HELPER="${UPSCALE_VAULT_SCRIPT_DIR}/sub2api-maintenance-lock.sh"
[ -r "$MAINTENANCE_LOCK_HELPER" ] && [ ! -L "$MAINTENANCE_LOCK_HELPER" ] || die
# shellcheck disable=SC1090,SC1091 # Installed alongside this root-owned executable.
. "$MAINTENANCE_LOCK_HELPER"

file_owner_mode() {
  stat -c '%u:%a' "$1" 2>/dev/null || stat -f '%u:%Lp' "$1" 2>/dev/null
}

parse_exact_vault_reference() {
  local reference="$1" expected_field="$2" body path field component
  local -a components

  case "$reference" in
    vault://*) ;;
    *) return 1 ;;
  esac
  case "$reference" in
    *[[:space:]]*) return 1 ;;
  esac
  body="${reference#vault://}"
  path="${body%%#*}"
  field="${body#*#}"
  [ "$field" != "$body" ] || return 1
  [ "${field#*#}" = "$field" ] || return 1
  case "$path" in
    ''|/*|*/|*//* ) return 1 ;;
  esac
  IFS=/ read -r -a components <<<"$path"
  for component in "${components[@]}"; do
    case "$component" in
      ''|.|..|*[!A-Za-z0-9_.-]*) return 1 ;;
    esac
    [ "${#component}" -le 128 ] || return 1
  done
  [ "$field" = "$expected_field" ] || return 1
  printf '%s\n' "$path"
}

load_root_runtime_configuration() {
  local expected_owner access_path secret_path key config_directory config_name canonical_config

  case "${SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_ALLOW_NON_ROOT_FOR_TESTS:-0}" in
    1) expected_owner="$(id -u)" ;;
    0) expected_owner=0 ;;
    *) die ;;
  esac
  case "$CONFIG_FILE" in
    /*) ;;
    *) die ;;
  esac
  [ -f "$CONFIG_FILE" ] && [ ! -L "$CONFIG_FILE" ] || die
  config_directory="${CONFIG_FILE%/*}"
  config_name="${CONFIG_FILE##*/}"
  [ -n "$config_directory" ] && [ -n "$config_name" ] || die
  canonical_config="$(cd -P -- "$config_directory" 2>/dev/null && pwd -P)/$config_name" || die
  [ "$canonical_config" = "$CONFIG_FILE" ] || die
  [ "$(file_owner_mode "$CONFIG_FILE")" = "${expected_owner}:600" ] || die

  # shellcheck disable=SC1090 # The checked root-owned production runtime configuration owns these non-secret refs.
  . "$CONFIG_FILE"
  set +x

  for key in IMAGE_UPSCALE_API_KEY BATCH_IMAGE_DELIVERY_COS_ACCESS_KEY_ID BATCH_IMAGE_DELIVERY_COS_SECRET_ACCESS_KEY; do
    [ "${!key+x}" != x ] || die
  done
  [ "${IMAGE_UPSCALE_API_KEY_VAULT_REF:-}" = "$UPSCAPE_API_KEY_VAULT_REF" ] || die
  [ "${BATCH_IMAGE_DELIVERY_COS_VAULT_AGENT_SOCKET:-}" = "$PUBLIC_DIR/public.sock" ] || die
  COS_ACCESS_KEY_VAULT_REF="${BATCH_IMAGE_DELIVERY_COS_ACCESS_KEY_VAULT_REF:-}"
  COS_SECRET_ACCESS_KEY_VAULT_REF="${BATCH_IMAGE_DELIVERY_COS_SECRET_ACCESS_KEY_VAULT_REF:-}"
  access_path="$(parse_exact_vault_reference "$COS_ACCESS_KEY_VAULT_REF" access_key_id)" || die
  secret_path="$(parse_exact_vault_reference "$COS_SECRET_ACCESS_KEY_VAULT_REF" secret_access_key)" || die
  [ "$access_path" = "$secret_path" ] || die
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
  printf '["/app/sub2api-vault-agent","serve","--public-socket","%s/public.sock","--admin-socket","%s/admin.sock","--allowed-ref","%s","--allowed-ref","%s","--allowed-ref","%s"]' \
    "$PUBLIC_DIR" "$ADMIN_DIR" "$UPSCAPE_API_KEY_VAULT_REF" "$COS_ACCESS_KEY_VAULT_REF" "$COS_SECRET_ACCESS_KEY_VAULT_REF"
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

clear_secret() {
  local variable="$1" value
  value="${!variable-}"
  if [ -n "$value" ]; then
    printf -v "$variable" '%*s' "${#value}" ''
  fi
  unset "$variable"
}

read_secret_line() {
  local variable="$1" maximum="$2" reject_all_whitespace="$3" value

  IFS= read -r "$variable" || die
  value="${!variable}"
  [ -n "$value" ] && [ "${#value}" -le "$maximum" ] || die
  case "$value" in
    *$'\r'*|*$'\n'*) die ;;
  esac
  [ "$(printf '%s' "$value" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')" = "$value" ] || die
  if [ "$reject_all_whitespace" = true ]; then
    case "$value" in *[[:space:]]*) die ;; esac
  fi
}

require_no_additional_stdin() {
  local extra
  if IFS= read -r extra; then
    die
  fi
}

load_secret() {
  local reference="$1" variable="$2"

  printf '%s' "${!variable}" \
    | docker exec -i "$CONTAINER" /app/sub2api-vault-agent load \
      --admin-socket "$ADMIN_DIR/admin.sock" --ref "$reference" >/dev/null 2>&1 || die
}

check_agent_ready() {
  docker exec "$CONTAINER" /app/sub2api-vault-agent check \
    --public-socket "$PUBLIC_DIR/public.sock" >/dev/null 2>&1 || die
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
  prepare|ready|ready-auto|load-api|load-batch-cos|load-all) ;;
  *) die ;;
esac

for command_name in docker flock id stat sed; do
  command -v "$command_name" >/dev/null 2>&1 || die
done
load_root_runtime_configuration
LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE}"
if ! sub2api_maintenance_lock_validate_configured_path "$LOCK_FILE"; then
  die
fi
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
    --allowed-ref "$UPSCAPE_API_KEY_VAULT_REF" \
    --allowed-ref "$COS_ACCESS_KEY_VAULT_REF" \
    --allowed-ref "$COS_SECRET_ACCESS_KEY_VAULT_REF" >/dev/null || die
fi

verify_container "$image" || die
case "$action" in
  ready|ready-auto)
    [ "$(docker container inspect "$CONTAINER" --format '{{.State.Health.Status}}')" = healthy ] || die
    printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_READY'
    ;;
  load-api|load-batch-cos|load-all)
    api_key=''
    cos_access_key=''
    cos_secret_key=''
    cleanup_injected_values() {
      clear_secret api_key
      clear_secret cos_access_key
      clear_secret cos_secret_key
    }
    trap cleanup_injected_values EXIT
    case "$action" in
      load-api)
        read_secret_line api_key 512 true
        require_no_additional_stdin
        load_secret "$UPSCAPE_API_KEY_VAULT_REF" api_key
        ;;
      load-batch-cos)
        read_secret_line cos_access_key 256 false
        read_secret_line cos_secret_key 512 false
        require_no_additional_stdin
        load_secret "$COS_ACCESS_KEY_VAULT_REF" cos_access_key
        load_secret "$COS_SECRET_ACCESS_KEY_VAULT_REF" cos_secret_key
        ;;
      load-all)
        read_secret_line api_key 512 true
        read_secret_line cos_access_key 256 false
        read_secret_line cos_secret_key 512 false
        require_no_additional_stdin
        load_secret "$UPSCAPE_API_KEY_VAULT_REF" api_key
        load_secret "$COS_ACCESS_KEY_VAULT_REF" cos_access_key
        load_secret "$COS_SECRET_ACCESS_KEY_VAULT_REF" cos_secret_key
        ;;
    esac
    if [ "$action" = load-all ]; then
      check_agent_ready
      printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_READY'
    else
      printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_LOADED'
    fi
    ;;
  prepare)
    printf '%s\n' 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_WAITING_FOR_INJECTION'
    ;;
esac
