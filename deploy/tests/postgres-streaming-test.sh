#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
PRIMARY="$ROOT_DIR/deploy/install-postgres-streaming-primary.sh"
TUNNEL_INSTALLER="$ROOT_DIR/deploy/install-postgres-streaming-tunnel.sh"
TUNNEL="$ROOT_DIR/deploy/sub2api-postgres-streaming-tunnel.sh"
UNIT="$ROOT_DIR/deploy/sub2api-postgres-streaming-tunnel.service"
WATCHDOG_INSTALLER="$ROOT_DIR/deploy/install-postgres-streaming-watchdog.sh"
WATCHDOG="$ROOT_DIR/deploy/sub2api-postgres-streaming-watchdog.sh"
WATCHDOG_SERVICE="$ROOT_DIR/deploy/sub2api-postgres-streaming-watchdog.service"
WATCHDOG_TIMER="$ROOT_DIR/deploy/sub2api-postgres-streaming-watchdog.timer"
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT

bash -n "$PRIMARY"
bash -n "$TUNNEL_INSTALLER"
bash -n "$TUNNEL"
bash -n "$WATCHDOG_INSTALLER"
bash -n "$WATCHDOG"

grep -Fq 'exec flock -w 30 "$LOCK_FILE"' "$PRIMARY"
if grep -Fq -- '--publish' "$PRIMARY"; then
  echo "isolated relay must not rely on Docker host publishing" >&2
  exit 1
fi
grep -Fq 'ListenStream=127.0.0.1:${RELAY_PORT}' "$PRIMARY"
grep -Fq 'systemd-socket-proxyd' "$PRIMARY"
grep -Fq 'existing relay must not publish Docker host ports' "$PRIMARY"
grep -Fq 'existing relay has unexpected extra networks' "$PRIMARY"
grep -Fq 'existing relay does not drop all capabilities' "$PRIMARY"
grep -Fq -- '--network "$RELAY_NETWORK"' "$PRIMARY"
grep -Fq -- '--ip "$RELAY_IP"' "$PRIMARY"
grep -Fq 'host replication ${REPLICATION_USER} ${RELAY_IP}/32 scram-sha-256' "$PRIMARY"
grep -Fq 'host all ${REPLICATION_USER} ${RELAY_IP}/32 reject' "$PRIMARY"
grep -Fq "SET password_encryption = 'scram-sha-256'" "$PRIMARY"
grep -Fq 'replication role password is not SCRAM-SHA-256' "$PRIMARY"
[ "$(grep -Fc 'docker exec -i "$POSTGRES_CONTAINER" sh -lc '\''exec psql -XAt' "$PRIMARY")" -eq 2 ] || {
  echo "heredoc PostgreSQL checks must attach docker stdin" >&2
  exit 1
}
reject_line=$(grep -nF 'host all ${REPLICATION_USER} ${RELAY_IP}/32 reject' "$PRIMARY" | cut -d: -f1)
existing_hba_line=$(grep -nF "' \"\$hba\" >>\"\$hba_temp\"" "$PRIMARY" | cut -d: -f1)
[ "$reject_line" -lt "$existing_hba_line" ] || {
  echo "managed HBA rules are not inserted before existing broad rules" >&2
  exit 1
}
grep -Fq "ALTER SYSTEM SET max_slot_wal_keep_size" "$PRIMARY"
if grep -Fq "max_slot_wal_keep_size = '\${SLOT_WAL_CEILING}'; SELECT pg_reload_conf()" "$PRIMARY"; then
  echo "ALTER SYSTEM and pg_reload_conf must use separate psql commands" >&2
  exit 1
fi
grep -Fq '^[1-9][0-9]*(MB|GB)$' "$PRIMARY"
grep -Fq 'systemctl reload ssh' "$PRIMARY"
if grep -Eq 'docker (stop|restart|rm -f) .*sub2api-(blue|green)|docker (stop|restart|rm -f) .*sub2api-postgres' "$PRIMARY"; then
  echo "primary installer contains a forbidden production lifecycle action" >&2
  exit 1
fi

mkdir "$TEMP_DIR/bin"
cat >"$TEMP_DIR/bin/flock" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >"$POSTGRES_STREAMING_FLOCK_LOG"
EOF
chmod +x "$TEMP_DIR/bin/flock"
POSTGRES_STREAMING_FLOCK_LOG="$TEMP_DIR/flock.log" \
  SUB2API_MAINTENANCE_LOCK_FILE="$TEMP_DIR/shared-maintenance.lock" \
  PATH="$TEMP_DIR/bin:$PATH" \
  "$PRIMARY" --public-key-file /not-used --password-file /not-used --source-cidr 192.0.2.1/32
grep -Fq -- "-w 30 $TEMP_DIR/shared-maintenance.lock" "$TEMP_DIR/flock.log"
printf 'SUB2API_MAINTENANCE_LOCK_FILE=%s\n' "$TEMP_DIR/configured-maintenance.lock" >"$TEMP_DIR/autodeploy.env"
POSTGRES_STREAMING_FLOCK_LOG="$TEMP_DIR/flock.log" \
  SUB2API_AUTODEPLOY_CONFIG_FILE="$TEMP_DIR/autodeploy.env" \
  PATH="$TEMP_DIR/bin:$PATH" \
  "$PRIMARY" --public-key-file /not-used --password-file /not-used --source-cidr 192.0.2.1/32
grep -Fq -- "-w 30 $TEMP_DIR/configured-maintenance.lock" "$TEMP_DIR/flock.log"
if SUB2API_MAINTENANCE_LOCK_FILE=relative.lock PATH="$TEMP_DIR/bin:$PATH" \
  "$PRIMARY" --public-key-file /not-used --password-file /not-used --source-cidr 192.0.2.1/32 \
  >"$TEMP_DIR/relative-lock.out" 2>&1; then
  echo "primary installer accepted a relative maintenance lock path" >&2
  exit 1
fi
grep -Fq 'maintenance lock path must be absolute' "$TEMP_DIR/relative-lock.out"

grep -Fq 'case "$LOCAL_BIND"' "$TUNNEL"
grep -Fq '0.0.0.0|::' "$TUNNEL"
grep -Fq -- '-o StrictHostKeyChecking=yes' "$TUNNEL"
grep -Fq -- '-o ExitOnForwardFailure=yes' "$TUNNEL"
grep -Fq -- '-o ConnectTimeout=10' "$TUNNEL"
grep -Fq -- '-o ServerAliveInterval=15' "$TUNNEL"
grep -Fq 'Restart=always' "$UNIT"
grep -Fq 'NoNewPrivileges=true' "$UNIT"
grep -Fq 'CapabilityBoundingSet=' "$UNIT"
grep -Fq 'ip -o address show' "$TUNNEL_INSTALLER"
grep -Fq 'systemd-analyze verify' "$TUNNEL_INSTALLER"
grep -Fq 'install -d -o root -g root -m 755 /usr/local/libexec' "$TUNNEL_INSTALLER"
grep -Fq 'address.version != 4' "$TUNNEL_INSTALLER"
grep -Fq 'local bind must equal the candidate network gateway' "$TUNNEL_INSTALLER"
grep -Fq "comment 'Sub2API internal PostgreSQL streaming tunnel'" "$TUNNEL_INSTALLER"
grep -Fq 'ufw --force delete allow in on "$probe_bridge"' "$TUNNEL_INSTALLER"
grep -Fq 'systemctl restart "$SERVICE_NAME"' "$TUNNEL_INSTALLER"
grep -Fq '"$PROBE_IMAGE" pg_isready -h "$LOCAL_BIND" -p "$LOCAL_PORT"' "$TUNNEL_INSTALLER"
grep -Fq 'StartLimitIntervalSec=0' "$UNIT"
grep -Fq 'candidate_app=unexpectedly_running' "$WATCHDOG"
grep -Fq 'pg_is_in_recovery()' "$WATCHDOG"
grep -Fq 'pg_stat_wal_receiver' "$WATCHDOG"
grep -Fq 'systemctl restart "$TUNNEL_SERVICE"' "$WATCHDOG"
grep -Fq 'docker start "$POSTGRES_CONTAINER"' "$WATCHDOG"
grep -Fq 'OnUnitActiveSec=1min' "$WATCHDOG_TIMER"
grep -Fq 'Persistent=true' "$WATCHDOG_TIMER"
grep -Fq 'NoNewPrivileges=true' "$WATCHDOG_SERVICE"
grep -Fq 'ProtectSystem=strict' "$WATCHDOG_SERVICE"
grep -Fq 'systemd-analyze verify "$service_target" "$timer_target"' "$WATCHDOG_INSTALLER"
grep -Fq 'systemctl enable --now "$TIMER_NAME"' "$WATCHDOG_INSTALLER"
grep -Fq 'SUB2API_PG_STREAMING_LOCK_HELD=true "$runner_target"' "$WATCHDOG_INSTALLER"
if grep -Fq 'systemctl start "$SERVICE_NAME"' "$WATCHDOG_INSTALLER"; then
  echo "watchdog installer would deadlock by starting the lock-taking service" >&2
  exit 1
fi

mkdir "$TEMP_DIR/watchdog-bin"
cat >"$TEMP_DIR/watchdog-bin/id" <<'EOF'
#!/usr/bin/env bash
[ "${1:-}" = -u ] && { echo 0; exit 0; }
exec /usr/bin/id "$@"
EOF
cat >"$TEMP_DIR/watchdog-bin/flock" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat >"$TEMP_DIR/watchdog-bin/systemctl" <<'EOF'
#!/usr/bin/env bash
if [ "${1:-}" = is-active ]; then
  [ "${WATCHDOG_TUNNEL_ACTIVE:-true}" = true ]
  exit
fi
if [ "${1:-}" = restart ]; then
  printf 'restart=%s\n' "${2:-}" >>"$WATCHDOG_ACTION_LOG"
  exit 0
fi
exit 1
EOF
cat >"$TEMP_DIR/watchdog-bin/docker" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
  inspect)
    if [ "${2:-}" = -f ]; then
      container="${4:-}"
      if [ "$container" = sub2api-migration-app-candidate ]; then
        [ "${WATCHDOG_APP_RUNNING:-false}" = true ] && echo true || echo false
      else
        [ "${WATCHDOG_PG_RUNNING:-true}" = true ] && echo true || echo false
      fi
    fi
    ;;
  start)
    printf 'start=%s\n' "${2:-}" >>"$WATCHDOG_ACTION_LOG"
    ;;
  exec)
    printf '%s\n' "${WATCHDOG_QUERY_STATE:-t|streaming|0}"
    ;;
  *) exit 1 ;;
esac
EOF
chmod +x "$TEMP_DIR/watchdog-bin/"*

watchdog_status="$TEMP_DIR/watchdog.status"
watchdog_log="$TEMP_DIR/watchdog-actions.log"
: >"$watchdog_log"
PATH="$TEMP_DIR/watchdog-bin:$PATH" \
  WATCHDOG_ACTION_LOG="$watchdog_log" \
  SUB2API_PG_STREAMING_LOCK_FILE="$TEMP_DIR/watchdog.lock" \
  SUB2API_PG_STREAMING_STATUS_FILE="$watchdog_status" \
  "$WATCHDOG" >/dev/null
grep -Fq 'state=healthy ' "$watchdog_status"
grep -Fq 'healed_postgres=false healed_tunnel=false' "$watchdog_status"
[ ! -s "$watchdog_log" ]

PATH="$TEMP_DIR/watchdog-bin:$PATH" \
  WATCHDOG_TUNNEL_ACTIVE=false \
  WATCHDOG_ACTION_LOG="$watchdog_log" \
  SUB2API_PG_STREAMING_LOCK_FILE="$TEMP_DIR/watchdog.lock" \
  SUB2API_PG_STREAMING_STATUS_FILE="$watchdog_status" \
  "$WATCHDOG" >/dev/null
grep -Fq 'restart=sub2api-postgres-streaming-tunnel.service' "$watchdog_log"
grep -Fq 'healed_postgres=false healed_tunnel=true' "$watchdog_status"

if PATH="$TEMP_DIR/watchdog-bin:$PATH" \
  WATCHDOG_APP_RUNNING=true \
  WATCHDOG_ACTION_LOG="$watchdog_log" \
  SUB2API_PG_STREAMING_LOCK_FILE="$TEMP_DIR/watchdog.lock" \
  SUB2API_PG_STREAMING_STATUS_FILE="$watchdog_status" \
  "$WATCHDOG" >/dev/null 2>&1; then
  echo "watchdog accepted an unexpectedly running candidate app" >&2
  exit 1
fi
grep -Fq 'state=unhealthy ' "$watchdog_status"
grep -Fq 'candidate_app=unexpectedly_running' "$watchdog_status"

POSTGRES_STREAMING_FLOCK_LOG="$TEMP_DIR/tunnel-flock.log" \
  SUB2API_PG_TUNNEL_LOCK_FILE="$TEMP_DIR/tunnel-config.lock" \
  PATH="$TEMP_DIR/bin:$PATH" \
  "$TUNNEL_INSTALLER" --ssh-host 192.0.2.1 --ssh-port 22 \
  --key-file /not-used --known-hosts-file /not-used --local-bind 172.18.0.1
grep -Fq -- "-w 30 $TEMP_DIR/tunnel-config.lock" "$TEMP_DIR/tunnel-flock.log"

echo "postgres streaming static checks passed"
