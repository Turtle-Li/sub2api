#!/usr/bin/env bash

# This is an SSH forced-command handler for GitHub Actions. The corresponding
# key has no shell, port-forwarding, or agent-forwarding privileges. It accepts
# strictly validated image and AWS Caddy configuration uploads. It cannot start
# the legacy source-build service or execute arbitrary commands.

set -Eeuo pipefail

APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
IMAGE_RELEASE_SCRIPT="${APP_DIR}/scripts/sub2api-github-image-release.sh"
CADDY_RELEASE_SCRIPT="${APP_DIR}/scripts/sub2api-caddy-config-release.sh"
SUDO_BIN="${SUB2API_SUDO_BIN:-/usr/bin/sudo}"
ORIGINAL_COMMAND="${SSH_ORIGINAL_COMMAND:-}"

case "$ORIGINAL_COMMAND" in
  *$'\n'*|*$'\r'*)
    echo "Invalid deploy command." >&2
    exit 2
    ;;
esac

validate_commit() {
  case "$1" in
    *[!0-9a-f]*|'') echo "Invalid commit." >&2; exit 2 ;;
  esac
  [ "${#1}" -eq 40 ] || { echo "Invalid commit length." >&2; exit 2; }
}

validate_digest() {
  local digest="$1" digest_hex
  case "$digest" in
    sha256:*) ;;
    *) echo "Invalid digest." >&2; exit 2 ;;
  esac
  digest_hex="${digest#sha256:}"
  case "$digest_hex" in
    *[!0-9a-f]*|'') echo "Invalid digest." >&2; exit 2 ;;
  esac
  [ "${#digest_hex}" -eq 64 ] || { echo "Invalid digest length." >&2; exit 2; }
}

IFS=' ' read -r action argument_one argument_two argument_three extra <<<"$ORIGINAL_COMMAND"
case "$action" in
  deploy-image)
    commit="$argument_one"
    version="$argument_two"
    archive_digest="$argument_three"
    [ -z "${extra:-}" ] || { echo "Invalid deploy-image command." >&2; exit 2; }
    validate_commit "$commit"
    case "$version" in
      ''|*[!0-9A-Za-z._+-]*) echo "Invalid version." >&2; exit 2 ;;
    esac
    [ "${#version}" -le 64 ] || { echo "Version is too long." >&2; exit 2; }
    validate_digest "$archive_digest"
    # The deploy account intentionally cannot traverse the root-only
    # application directory. sudo resolves this root-owned helper.
    exec "$SUDO_BIN" -n "$IMAGE_RELEASE_SCRIPT" "$commit" "$version" "$archive_digest"
    ;;
  deploy-caddy)
    commit="$argument_one"
    config_digest="$argument_two"
    [ -z "${argument_three:-}" ] && [ -z "${extra:-}" ] \
      || { echo "Invalid deploy-caddy command." >&2; exit 2; }
    validate_commit "$commit"
    validate_digest "$config_digest"
    exec "$SUDO_BIN" -n "$CADDY_RELEASE_SCRIPT" "$commit" "$config_digest"
    ;;
  *)
    echo "Only deploy-image and deploy-caddy are permitted." >&2
    exit 2
    ;;
esac
