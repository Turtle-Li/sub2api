#!/usr/bin/env bash

# SSH forced-command parser for the centralized certificate deploy identity.
# It intentionally exposes only the frozen certificate receiver protocol.

set -Eeuo pipefail

RECEIVER="${SUB2API_CERT_RECEIVER:-/opt/sub2api/scripts/sub2api-cert-receiver.sh}"
SUDO_BIN="${SUB2API_SUDO_BIN:-/usr/bin/sudo}"
ORIGINAL_COMMAND="${SSH_ORIGINAL_COMMAND:-}"

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 2
}

case "$ORIGINAL_COMMAND" in
  *$'\n'*|*$'\r'*) die "invalid certificate deploy command" ;;
esac

validate_generation() {
  [[ "$1" =~ ^[0-9a-f]{20}$ ]] || die "invalid generation"
}

validate_hash() {
  [[ "$1" =~ ^[0-9a-f]{64}$ ]] || die "invalid hash"
}

validate_domain() {
  [[ "$1" =~ ^[0-9A-Za-z.-]+$ ]] && [[ "$1" == *.* ]] || die "invalid domain"
}

IFS=' ' read -r action arg1 arg2 arg3 arg4 arg5 extra <<<"$ORIGINAL_COMMAND"
case "$action" in
  prepare)
    [ -n "${arg1:-}" ] && [ -n "${arg2:-}" ] && [ -n "${arg3:-}" ] \
      && [ -n "${arg4:-}" ] && [ -n "${arg5:-}" ] && [ -z "${extra:-}" ] \
      || die "prepare requires GENERATION CERT_SHA KEY_SHA MIN_SECONDS DOMAIN"
    validate_generation "$arg1"
    validate_hash "$arg2"
    validate_hash "$arg3"
    case "$arg4" in ''|*[!0-9]*) die "invalid minimum lifetime" ;; esac
    [ "$arg4" -gt 0 ] || die "invalid minimum lifetime"
    validate_domain "$arg5"
    exec "$SUDO_BIN" -n "$RECEIVER" prepare "$arg1" "$arg2" "$arg3" "$arg4" "$arg5"
    ;;
  activate|rollback|commit|discard)
    [ -n "${arg1:-}" ] && [ -n "${arg2:-}" ] \
      && [ -z "${arg3:-}${arg4:-}${arg5:-}${extra:-}" ] \
      || die "${action} requires GENERATION DOMAIN"
    validate_generation "$arg1"
    validate_domain "$arg2"
    exec "$SUDO_BIN" -n "$RECEIVER" "$action" "$arg1" "$arg2"
    ;;
  status)
    [ -n "${arg1:-}" ] && [ -z "${arg2:-}${arg3:-}${arg4:-}${arg5:-}${extra:-}" ] \
      || die "status requires DOMAIN"
    validate_domain "$arg1"
    exec "$SUDO_BIN" -n "$RECEIVER" status "$arg1"
    ;;
  *)
    die "only prepare, activate, status, rollback, commit, and discard are permitted"
    ;;
esac
