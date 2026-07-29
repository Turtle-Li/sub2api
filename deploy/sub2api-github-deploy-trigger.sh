#!/usr/bin/env bash

# This is an SSH forced-command handler for GitHub Actions. The corresponding
# key has no shell, port-forwarding, or agent-forwarding privileges. It accepts
# one strictly validated image upload command whose stdin is a zstd-compressed
# Docker archive. It cannot start the legacy source-build service.

set -Eeuo pipefail

APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
IMAGE_RELEASE_SCRIPT="${APP_DIR}/scripts/sub2api-github-image-release.sh"
SUDO_BIN="${SUB2API_SUDO_BIN:-/usr/bin/sudo}"
ORIGINAL_COMMAND="${SSH_ORIGINAL_COMMAND:-}"

case "$ORIGINAL_COMMAND" in
  *$'\n'*|*$'\r'*)
    echo "Invalid deploy command." >&2
    exit 2
    ;;
esac

IFS=' ' read -r action commit version image_id extra <<<"$ORIGINAL_COMMAND"
if [ "$action" != "deploy-image" ] || [ -n "${extra:-}" ]; then
  echo "Only 'deploy-image COMMIT VERSION IMAGE_ID' is permitted." >&2
  exit 2
fi
case "$commit" in
  *[!0-9a-f]*|'') echo "Invalid commit." >&2; exit 2 ;;
esac
[ "${#commit}" -eq 40 ] || { echo "Invalid commit length." >&2; exit 2; }
case "$version" in
  ''|*[!0-9A-Za-z._+-]*) echo "Invalid version." >&2; exit 2 ;;
esac
[ "${#version}" -le 64 ] || { echo "Version is too long." >&2; exit 2; }
case "$image_id" in
  sha256:*) ;;
  *) echo "Invalid image ID." >&2; exit 2 ;;
esac
image_id_hex="${image_id#sha256:}"
case "$image_id_hex" in
  *[!0-9a-f]*|'') echo "Invalid image ID." >&2; exit 2 ;;
esac
[ "${#image_id_hex}" -eq 64 ] || { echo "Invalid image ID length." >&2; exit 2; }
[ -f "$IMAGE_RELEASE_SCRIPT" ] || {
  echo "GitHub image release helper is unavailable." >&2
  exit 1
}

exec "$SUDO_BIN" -n "$IMAGE_RELEASE_SCRIPT" "$commit" "$version" "$image_id"
