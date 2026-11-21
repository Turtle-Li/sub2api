#!/usr/bin/env bash
# Start and seed the agent in the same systemd credential mount namespace.
# The plaintext is never an environment value, argument, or Docker metadata.
set -Eeuo pipefail
set +x

agent=/usr/local/libexec/sub2api-image-upscale-aliyun/vault-agent
directory=/run/sub2api-aliyun-agent
credential="${CREDENTIALS_DIRECTORY:?}/sub2api-aliyun-image-upscale"
fail() { printf 'Aliyun host agent initialization failed: %s\n' "$1" >&2; exit 1; }
[ -r "$credential" ] || fail systemd_credential

reference='vault://secret/data/sub2api/aliyun-image-upscale#credentials'
"$agent" serve --public-socket "$directory/public/public.sock" \
  --admin-socket "$directory/admin.sock" --allowed-ref "$reference" &
child=$!
stop_agent() {
  kill -TERM "$child" 2>/dev/null || true
  wait "$child" 2>/dev/null || true
}
trap stop_agent EXIT
trap 'exit 0' TERM INT
for _ in {1..50}; do
  kill -0 "$child" 2>/dev/null || fail agent_exit
  [ ! -S "$directory/admin.sock" ] || break
  sleep 0.2
done
[ -S "$directory/admin.sock" ] || fail admin_socket
"$agent" load --admin-socket "$directory/admin.sock" \
  --ref "$reference" <"$credential" >/dev/null || fail injection
"$agent" check --public-socket "$directory/public/public.sock" || fail readiness
systemd-notify --ready || fail notify
wait "$child" || fail agent_exit
fail agent_exit
