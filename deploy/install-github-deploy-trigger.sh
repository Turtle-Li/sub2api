#!/usr/bin/env bash

# Provision a dedicated, forced-command SSH account for the GitHub Actions
# production deploy workflow. The account can only start the existing
# root-owned sub2api-autodeploy.service through a narrowly scoped sudo rule.

set -Eeuo pipefail

APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
TRIGGER_SCRIPT="${APP_DIR}/scripts/sub2api-github-deploy-trigger.sh"
DEPLOY_USER="${SUB2API_GITHUB_DEPLOY_USER:-sub2api-github-deploy}"
DEPLOY_HOME="${SUB2API_GITHUB_DEPLOY_HOME:-/var/lib/sub2api-github-deploy}"
SERVICE_NAME="sub2api-autodeploy.service"
PUBLIC_KEY_FILE=""

usage() {
  cat <<'EOF'
Usage: install-github-deploy-trigger.sh --public-key-file PATH [options]

Options:
  --public-key-file PATH  Public Ed25519 key used by the GitHub Actions workflow.
  --user NAME             Dedicated system user (default: sub2api-github-deploy).
  --help                  Show this help.
EOF
}

die() {
  echo "ERROR: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --public-key-file)
      [ "$#" -ge 2 ] || die "--public-key-file requires a value"
      PUBLIC_KEY_FILE="$2"
      shift
      ;;
    --user)
      [ "$#" -ge 2 ] || die "--user requires a value"
      DEPLOY_USER="$2"
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      die "Unknown option: $1"
      ;;
  esac
  shift
done

[ "$(id -u)" -eq 0 ] || die "run this installer as root on the Sub2API server"
[ -n "$PUBLIC_KEY_FILE" ] || die "--public-key-file is required"
[ -r "$PUBLIC_KEY_FILE" ] || die "public key file is not readable: $PUBLIC_KEY_FILE"
case "$DEPLOY_USER" in
  ''|-*|*[!a-zA-Z0-9_-]*) die "deploy user contains unsupported characters" ;;
esac

for command_name in install useradd getent id ssh-keygen visudo sudo systemctl; do
  require_cmd "$command_name"
done
[ -x "$TRIGGER_SCRIPT" ] || die "release trigger is missing or not executable: $TRIGGER_SCRIPT"
systemctl cat "$SERVICE_NAME" >/dev/null || die "required systemd service is missing: $SERVICE_NAME"

key_line="$(awk 'NF && $1 !~ /^#/ { print; exit }' "$PUBLIC_KEY_FILE")"
[ -n "$key_line" ] || die "public key file contains no key"
printf '%s\n' "$key_line" | ssh-keygen -lf - >/dev/null \
  || die "public key is invalid"
case "$key_line" in
  ssh-ed25519\ *) ;;
  *) die "only an Ed25519 public key is accepted" ;;
esac

if id "$DEPLOY_USER" >/dev/null 2>&1; then
  existing_home="$(getent passwd "$DEPLOY_USER" | awk -F: '{print $6}')"
  [ "$existing_home" = "$DEPLOY_HOME" ] \
    || die "existing user has unexpected home directory: $existing_home"
else
  useradd --system --create-home --home-dir "$DEPLOY_HOME" \
    --shell /bin/bash --user-group "$DEPLOY_USER"
fi

install -d -o root -g root -m 755 "$DEPLOY_HOME"
# sshd on Ubuntu only accepts this account's authorized_keys when the account
# owns the .ssh directory and key file. The account password remains locked,
# and this sole key is restricted to the forced command below.
install -d -o "$DEPLOY_USER" -g "$DEPLOY_USER" -m 700 "$DEPLOY_HOME/.ssh"
authorized_keys_temp="$(mktemp)"
printf 'command="%s",no-port-forwarding,no-agent-forwarding,no-X11-forwarding,no-pty,no-user-rc %s\n' \
  "$TRIGGER_SCRIPT" "$key_line" \
  >"$authorized_keys_temp"
install -o "$DEPLOY_USER" -g "$DEPLOY_USER" -m 600 "$authorized_keys_temp" \
  "$DEPLOY_HOME/.ssh/authorized_keys"
rm -f "$authorized_keys_temp"

sudoers_temp="$(mktemp)"
printf '%s ALL=(root) NOPASSWD: /usr/bin/systemctl start %s\n' \
  "$DEPLOY_USER" "$SERVICE_NAME" >"$sudoers_temp"
visudo -cf "$sudoers_temp" >/dev/null || {
  rm -f "$sudoers_temp"
  die "generated sudoers policy did not validate"
}
install -o root -g root -m 440 "$sudoers_temp" "/etc/sudoers.d/${DEPLOY_USER}"
rm -f "$sudoers_temp"

echo "Installed restricted GitHub Actions deploy trigger for ${DEPLOY_USER}."
