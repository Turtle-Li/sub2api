#!/usr/bin/env bash

# Install a dedicated Tailnet-restricted SSH identity for certificate delivery.
# This installer creates no key material; it consumes an operator-provided
# Ed25519 public key and records only non-secret receiver settings.

set -Eeuo pipefail
umask 077

APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
TRIGGER_SCRIPT="${APP_DIR}/scripts/sub2api-cert-deploy-trigger.sh"
RECEIVER_SCRIPT="${APP_DIR}/scripts/sub2api-cert-receiver.sh"
MAINTENANCE_LOCK_HELPER="${APP_DIR}/scripts/sub2api-maintenance-lock.sh"
DEPLOY_USER="${SUB2API_CERT_DEPLOY_USER:-sub2api-cert-deploy}"
DEPLOY_HOME="${SUB2API_CERT_DEPLOY_HOME:-/var/lib/sub2api-cert-deploy}"
CONFIG_FILE="${SUB2API_CERT_RECEIVER_CONFIG_FILE:-/etc/sub2api-cert-receiver.env}"
MAINTENANCE_LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-/run/sub2api-maintenance/sub2api-maintenance.lock}"
MAINTENANCE_LOCK_DIR=""
PUBLIC_KEY_FILE=""
SOURCE_ADDRESS=""
DOMAIN="api.turtleligpt.com"
CADDY_CONTAINER="sub2api-caddy"
CADDYFILE_HOST_PATH="${APP_DIR}/Caddyfile"
CADDYFILE_CONTAINER_PATH="/etc/caddy/Caddyfile"
CADDY_CERT_ROOT="/etc/sub2api-certs"
TLS_VERIFY_IP="127.0.0.1"
TLS_VERIFY_PORT="443"

usage() {
  cat <<'EOF'
Usage: install-sub2api-cert-receiver.sh --public-key-file PATH --source-address TAILNET_IP_OR_CIDR [options]

Required:
  --public-key-file PATH     Existing Ed25519 public key from the certificate controller.
  --source-address ADDRESS   Exact Tailnet IPv4 address (optional /32) allowed by authorized_keys.

Options:
  --user NAME                Dedicated system user (default: sub2api-cert-deploy).
  --domain NAME              Certificate hostname (default: api.turtleligpt.com).
  --caddy-container NAME     Local Caddy container name (default: sub2api-caddy).
  --caddyfile-host PATH      Root-owned host Caddyfile.
  --caddyfile-container PATH Caddyfile path inside the Caddy container.
  --caddy-cert-root PATH     Certificate mount root inside Caddy.
  --tls-verify-ip ADDRESS    Local listener used for post-reload SNI verification.
  --tls-verify-port PORT     Local TLS port (default: 443).
  --help                     Show this help.
EOF
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

# Reject an ambient production override before validating/sourcing installed
# helper files or touching any account/configuration target. The helper repeats
# the full grammar and ancestry validation once it is loaded.
case "${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-0}" in
  1) ;;
  0)
    [ "$MAINTENANCE_LOCK_FILE" = "/run/sub2api-maintenance/sub2api-maintenance.lock" ] \
      || die "maintenance lock path must be the canonical /run/sub2api-maintenance/sub2api-maintenance.lock: ${MAINTENANCE_LOCK_FILE}"
    ;;
  *) die "SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS must be 0 or 1" ;;
esac

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

validate_docker_name() {
  case "$2" in
    ''|[!0-9A-Za-z]*|*[!0-9A-Za-z_.-]*) die "$1 is not a supported Docker container name" ;;
  esac
}

validate_user_name() {
  case "$2" in
    ''|*[!0-9A-Za-z_-]*|[0-9-]*) die "$1 is not a supported system username" ;;
  esac
}

validate_absolute_path() {
  case "$2" in /*) ;; *) die "$1 must be an absolute path" ;; esac
  case "$2" in
    *$'\n'*|*$'\r'*|*'#'*|*'&'*|*'\'*) die "$1 contains unsupported characters" ;;
  esac
}

validate_root_owned_file() {
  local label="$1"
  local path="$2"
  local require_executable="$3"
  local owner mode permissions
  [ -f "$path" ] && [ ! -L "$path" ] || die "$label is not a regular non-symlink file: $path"
  owner="$(stat -c '%u' "$path")" || die "cannot read owner for $label: $path"
  [ "$owner" -eq 0 ] || die "$label must be owned by root: $path"
  mode="$(stat -c '%a' "$path")" || die "cannot read permissions for $label: $path"
  case "$mode" in ''|*[!0-7]*) die "$label has unsupported permissions: $path" ;; esac
  permissions=$((8#$mode))
  [ $((permissions & 0022)) -eq 0 ] || die "$label must not be group/other writable: $path"
  [ "$require_executable" != true ] || [ -x "$path" ] \
    || die "$label is not executable: $path"
}

validate_root_owned_directory_chain() {
  local label="$1" path="$2"
  local remaining current component owner mode permissions

  case "$path" in
    /*) ;;
    *) die "$label has a non-absolute path: $path" ;;
  esac
  remaining="${path#/}"
  current="/"
  while :; do
    [ ! -L "$current" ] || die "$label ancestor is a symlink: $current"
    [ -d "$current" ] || die "$label ancestor is not a directory: $current"
    owner="$(stat -c '%u' "$current")" \
      || die "cannot read owner for $label ancestor: $current"
    [ "$owner" -eq 0 ] || die "$label ancestor must be owned by root: $current"
    mode="$(stat -c '%a' "$current")" \
      || die "cannot read permissions for $label ancestor: $current"
    case "$mode" in ''|*[!0-7]*) die "$label ancestor has unsupported permissions: $current" ;; esac
    permissions=$((8#$mode))
    [ $((permissions & 0022)) -eq 0 ] \
      || die "$label ancestor is group/other writable: $current"
    [ -n "$remaining" ] || break
    component="${remaining%%/*}"
    if [ "$component" = "$remaining" ]; then
      remaining=""
    else
      remaining="${remaining#*/}"
    fi
    [ -n "$component" ] || die "$label ancestor path is malformed: $path"
    if [ "$current" = / ]; then
      current="/${component}"
    else
      current="${current}/${component}"
    fi
  done
}

validate_tailnet_source() {
  local source="$1"
  local ip octet1 octet2 octet3 octet4 extra
  case "$source" in
    */32) ip="${source%/32}" ;;
    */*) die "source address must be one exact Tailnet IPv4 address, not a broad CIDR" ;;
    *) ip="$source" ;;
  esac
  [[ "$ip" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] \
    || die "source address must be an exact Tailnet IPv4 address"
  IFS=. read -r octet1 octet2 octet3 octet4 extra <<<"$ip"
  [ -z "${extra:-}" ] || die "source address must be an exact Tailnet IPv4 address"
  for octet in "$octet1" "$octet2" "$octet3" "$octet4"; do
    ((10#$octet <= 255)) || die "source address contains an invalid IPv4 octet"
  done
  ((10#$octet1 == 100 && 10#$octet2 >= 64 && 10#$octet2 <= 127)) \
    || die "source address must be inside the Tailscale CGNAT range 100.64.0.0/10"
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --public-key-file|--source-address|--user|--domain|--caddy-container|--caddyfile-host|--caddyfile-container|--caddy-cert-root|--tls-verify-ip|--tls-verify-port)
      [ "$#" -ge 2 ] || die "$1 requires a value"
      case "$1" in
        --public-key-file) PUBLIC_KEY_FILE="$2" ;;
        --source-address) SOURCE_ADDRESS="$2" ;;
        --user) DEPLOY_USER="$2" ;;
        --domain) DOMAIN="$2" ;;
        --caddy-container) CADDY_CONTAINER="$2" ;;
        --caddyfile-host) CADDYFILE_HOST_PATH="$2" ;;
        --caddyfile-container) CADDYFILE_CONTAINER_PATH="$2" ;;
        --caddy-cert-root) CADDY_CERT_ROOT="$2" ;;
        --tls-verify-ip) TLS_VERIFY_IP="$2" ;;
        --tls-verify-port) TLS_VERIFY_PORT="$2" ;;
      esac
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *) die "unknown option: $1" ;;
  esac
  shift
done

[ "$(id -u)" -eq 0 ] || die "run this installer as root on the Sub2API node"
[ -n "$PUBLIC_KEY_FILE" ] && [ -r "$PUBLIC_KEY_FILE" ] || die "--public-key-file is required and must be readable"
[ -n "$SOURCE_ADDRESS" ] || die "--source-address is required"
validate_tailnet_source "$SOURCE_ADDRESS"
case "$DOMAIN" in ''|.*|*..*|*[!0-9A-Za-z.-]*) die "invalid domain" ;; esac
case "$TLS_VERIFY_IP" in ''|*[!0-9A-Fa-f:.]*) die "invalid TLS verify IP" ;; esac
case "$TLS_VERIFY_PORT" in ''|*[!0-9]*) die "invalid TLS verify port" ;; esac
[ "$TLS_VERIFY_PORT" -ge 1 ] && [ "$TLS_VERIFY_PORT" -le 65535 ] || die "invalid TLS verify port"
validate_user_name deploy_user "$DEPLOY_USER"
validate_docker_name caddy_container "$CADDY_CONTAINER"
validate_absolute_path app_dir "$APP_DIR"
validate_absolute_path caddyfile_host "$CADDYFILE_HOST_PATH"
validate_absolute_path caddyfile_container "$CADDYFILE_CONTAINER_PATH"
validate_absolute_path caddy_cert_root "$CADDY_CERT_ROOT"
validate_absolute_path config_file "$CONFIG_FILE"
validate_absolute_path maintenance_lock_file "$MAINTENANCE_LOCK_FILE"

for command_name in flock getent id install ssh-keygen stat sudo useradd visudo; do
  require_cmd "$command_name"
done
validate_root_owned_file "certificate deploy trigger" "$TRIGGER_SCRIPT" true
validate_root_owned_file "certificate receiver" "$RECEIVER_SCRIPT" true
# Unlike the source-tree installer, this standalone installer has no trusted
# ordinary-user checkout boundary. Its installed helper and every directory
# used to reach it must already be root-owned and non-writable, which pins the
# pathname between the leaf metadata check and source for non-root attackers.
validate_root_owned_directory_chain "maintenance lock helper" "${MAINTENANCE_LOCK_HELPER%/*}"
validate_root_owned_file "maintenance lock helper" "$MAINTENANCE_LOCK_HELPER" true
validate_root_owned_file "Caddyfile" "$CADDYFILE_HOST_PATH" false
[ -r "$CADDYFILE_HOST_PATH" ] || die "Caddyfile is not readable: $CADDYFILE_HOST_PATH"
# The helper was just checked as a root-owned non-writable regular file. Its
# preflight is read-only and prevents the later install -d from resolving an
# unsafe target through .. or a symlink ancestor.
# shellcheck disable=SC1090,SC1091 # The preceding metadata check pins this source.
. "$MAINTENANCE_LOCK_HELPER"
if ! sub2api_maintenance_lock_validate_install_target "$MAINTENANCE_LOCK_FILE"; then
  die "unsafe maintenance lock target: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
fi
MAINTENANCE_LOCK_DIR="$SUB2API_MAINTENANCE_LOCK_PARENT"
if ! sub2api_maintenance_lock_open "$MAINTENANCE_LOCK_FILE"; then
  die "unsafe maintenance lock: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
fi
flock -n 8 || die "maintenance lock is held"

key_line="$(awk 'NF && $1 !~ /^#/ {print; exit}' "$PUBLIC_KEY_FILE")"
[ -n "$key_line" ] || die "public key file contains no key"
printf '%s\n' "$key_line" | ssh-keygen -lf - >/dev/null || die "public key is invalid"
case "$key_line" in ssh-ed25519\ *) ;; *) die "only an Ed25519 public key is accepted" ;; esac

if id "$DEPLOY_USER" >/dev/null 2>&1; then
  existing_home="$(getent passwd "$DEPLOY_USER" | awk -F: '{print $6}')"
  [ "$existing_home" = "$DEPLOY_HOME" ] || die "existing user has unexpected home directory: $existing_home"
else
  useradd --system --create-home --home-dir "$DEPLOY_HOME" --shell /bin/bash --user-group "$DEPLOY_USER"
fi

install -d -o root -g root -m 755 "$DEPLOY_HOME"
install -d -o "$DEPLOY_USER" -g "$DEPLOY_USER" -m 700 "$DEPLOY_HOME/.ssh"
authorized_keys_temp="$(mktemp)"
printf 'restrict,from="%s",command="%s" %s\n' \
  "$SOURCE_ADDRESS" "$TRIGGER_SCRIPT" "$key_line" >"$authorized_keys_temp"
install -o "$DEPLOY_USER" -g "$DEPLOY_USER" -m 600 "$authorized_keys_temp" "$DEPLOY_HOME/.ssh/authorized_keys"
rm -f -- "$authorized_keys_temp"

config_temp="$(mktemp)"
{
  printf 'SUB2API_CERT_ROOT=%q\n' "${APP_DIR}/certs/${DOMAIN}"
  printf 'SUB2API_CERT_DOMAIN=%q\n' "$DOMAIN"
  printf 'SUB2API_CERT_CADDY_CONTAINER=%q\n' "$CADDY_CONTAINER"
  printf 'SUB2API_CERT_CADDYFILE_HOST_PATH=%q\n' "$CADDYFILE_HOST_PATH"
  printf 'SUB2API_CERT_CADDYFILE_CONTAINER_PATH=%q\n' "$CADDYFILE_CONTAINER_PATH"
  printf 'SUB2API_CERT_CADDY_CERT_ROOT=%q\n' "$CADDY_CERT_ROOT"
  printf 'SUB2API_CERT_TLS_VERIFY_IP=%q\n' "$TLS_VERIFY_IP"
  printf 'SUB2API_CERT_TLS_VERIFY_PORT=%q\n' "$TLS_VERIFY_PORT"
  printf 'SUB2API_MAINTENANCE_LOCK_FILE=%q\n' "$MAINTENANCE_LOCK_FILE"
} >"$config_temp"
install -o root -g root -m 600 "$config_temp" "$CONFIG_FILE"
rm -f -- "$config_temp"

sudoers_temp="$(mktemp)"
{
  printf 'Defaults:%s !use_pty,env_reset\n' "$DEPLOY_USER"
  printf '%s ALL=(root) NOPASSWD: %s *\n' "$DEPLOY_USER" "$RECEIVER_SCRIPT"
} >"$sudoers_temp"
visudo -cf "$sudoers_temp" >/dev/null || {
  rm -f -- "$sudoers_temp"
  die "generated sudoers policy did not validate"
}
install -o root -g root -m 440 "$sudoers_temp" "/etc/sudoers.d/${DEPLOY_USER}"
rm -f -- "$sudoers_temp"

install -d -o root -g root -m 700 "${APP_DIR}/certs/${DOMAIN}" "${APP_DIR}/certs/${DOMAIN}/generations"
install -d -o root -g root -m 700 "$MAINTENANCE_LOCK_DIR"
printf 'Installed restricted certificate receiver for %s from %s.\n' "$DEPLOY_USER" "$SOURCE_ADDRESS"
