#!/usr/bin/env bash

set -Eeuo pipefail

CONFIG_FILE="${SUB2API_PG_TUNNEL_CONFIG:-/etc/sub2api-pg-tunnel.conf}"
[ -r "$CONFIG_FILE" ] || { echo "ERROR: missing $CONFIG_FILE" >&2; exit 1; }
# shellcheck source=/dev/null
source "$CONFIG_FILE"

required=(SSH_HOST SSH_PORT SSH_USER SSH_KEY KNOWN_HOSTS LOCAL_BIND LOCAL_PORT REMOTE_HOST REMOTE_PORT)
for name in "${required[@]}"; do
  [ -n "${!name:-}" ] || { echo "ERROR: $name is required" >&2; exit 1; }
done

case "$LOCAL_BIND" in
  0.0.0.0|::|'') echo "ERROR: wildcard local bind is forbidden" >&2; exit 1 ;;
esac
[ "$REMOTE_HOST" = 127.0.0.1 ] || { echo "ERROR: remote endpoint must be loopback" >&2; exit 1; }
for value in "$SSH_PORT" "$LOCAL_PORT" "$REMOTE_PORT"; do
  case "$value" in ''|*[!0-9]*) echo "ERROR: ports must be numeric" >&2; exit 1 ;; esac
done
[ -r "$SSH_KEY" ] || { echo "ERROR: SSH key is unreadable" >&2; exit 1; }
[ -r "$KNOWN_HOSTS" ] || { echo "ERROR: known_hosts is unreadable" >&2; exit 1; }

exec /usr/bin/ssh -NT \
  -i "$SSH_KEY" \
  -p "$SSH_PORT" \
  -o BatchMode=yes \
  -o IdentitiesOnly=yes \
  -o StrictHostKeyChecking=yes \
  -o UserKnownHostsFile="$KNOWN_HOSTS" \
  -o ExitOnForwardFailure=yes \
  -o ConnectTimeout=10 \
  -o ServerAliveInterval=15 \
  -o ServerAliveCountMax=3 \
  -o TCPKeepAlive=yes \
  -L "${LOCAL_BIND}:${LOCAL_PORT}:${REMOTE_HOST}:${REMOTE_PORT}" \
  "${SSH_USER}@${SSH_HOST}"
