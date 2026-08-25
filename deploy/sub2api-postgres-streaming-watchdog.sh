#!/usr/bin/env bash

set -Eeuo pipefail

LOCK_FILE="${SUB2API_PG_STREAMING_LOCK_FILE:-/run/lock/sub2api-db-streaming.lock}"
LOCK_HELD="${SUB2API_PG_STREAMING_LOCK_HELD:-false}"
POSTGRES_CONTAINER="${SUB2API_PG_STANDBY_CONTAINER:-sub2api-migration-postgres}"
APP_CONTAINER="${SUB2API_PG_CANDIDATE_APP_CONTAINER:-sub2api-migration-app-candidate}"
TUNNEL_SERVICE="${SUB2API_PG_TUNNEL_SERVICE:-sub2api-postgres-streaming-tunnel.service}"
STATUS_FILE="${SUB2API_PG_STREAMING_STATUS_FILE:-/run/sub2api-postgres-streaming-watchdog.status}"
WAIT_ATTEMPTS="${SUB2API_PG_STREAMING_WAIT_ATTEMPTS:-30}"
MAX_REPLAY_BYTE_LAG="${SUB2API_PG_STREAMING_MAX_REPLAY_BYTE_LAG:-268435456}"

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "ERROR: $1 is required" >&2
    exit 1
  }
}

write_status() {
  local state="$1"
  shift
  local status_dir status_temp
  status_dir="$(dirname "$STATUS_FILE")"
  status_temp="$(mktemp "$status_dir/.sub2api-postgres-streaming-watchdog.XXXXXX")"
  printf 'state=%s checked_at=%s %s\n' "$state" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >"$status_temp"
  chmod 644 "$status_temp"
  mv -f "$status_temp" "$STATUS_FILE"
}

fail() {
  write_status unhealthy "$*"
  echo "ERROR: $*" >&2
  exit 1
}

case "$LOCK_FILE" in /*) ;; *) echo "ERROR: streaming lock path must be absolute" >&2; exit 1 ;; esac
case "$STATUS_FILE" in /*) ;; *) echo "ERROR: status path must be absolute" >&2; exit 1 ;; esac
case "$WAIT_ATTEMPTS" in ''|*[!0-9]*) echo "ERROR: wait attempts must be numeric" >&2; exit 1 ;; esac
case "$MAX_REPLAY_BYTE_LAG" in ''|*[!0-9]*) echo "ERROR: byte lag threshold must be numeric" >&2; exit 1 ;; esac
[ "$WAIT_ATTEMPTS" -gt 0 ] || { echo "ERROR: wait attempts must be positive" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || { echo "ERROR: run as root" >&2; exit 1; }
for command_name in chmod date dirname docker flock mktemp mv seq sleep systemctl; do
  require_cmd "$command_name"
done

if [ "$LOCK_HELD" != true ]; then
  exec 9>"$LOCK_FILE"
  flock -w 30 9 || fail "streaming lock was not available"
fi

docker inspect "$POSTGRES_CONTAINER" >/dev/null 2>&1 || fail "standby_container=missing"
docker inspect "$APP_CONTAINER" >/dev/null 2>&1 || fail "candidate_app_container=missing"
[ "$(docker inspect -f '{{.State.Running}}' "$APP_CONTAINER")" = false ] \
  || fail "candidate_app=unexpectedly_running"

healed_postgres=false
healed_tunnel=false
if [ "$(docker inspect -f '{{.State.Running}}' "$POSTGRES_CONTAINER")" != true ]; then
  docker start "$POSTGRES_CONTAINER" >/dev/null || fail "standby_container=start_failed"
  healed_postgres=true
fi
if ! systemctl is-active --quiet "$TUNNEL_SERVICE"; then
  systemctl restart "$TUNNEL_SERVICE" || fail "tunnel=restart_failed"
  healed_tunnel=true
fi

query_standby() {
  docker exec "$POSTGRES_CONTAINER" sh -c '
    exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -AtF "|" -c \
      "select pg_is_in_recovery(),
              coalesce((select status from pg_stat_wal_receiver limit 1), '\''none'\''),
              coalesce(pg_wal_lsn_diff(
                (select latest_end_lsn from pg_stat_wal_receiver limit 1),
                pg_last_wal_replay_lsn()
              )::bigint, 0)"'
}

standby_state=
for _ in $(seq 1 "$WAIT_ATTEMPTS"); do
  standby_state="$(query_standby 2>/dev/null || true)"
  [ -n "$standby_state" ] && break
  sleep 1
done
[ -n "$standby_state" ] || fail "standby=not_queryable"

IFS='|' read -r in_recovery receiver_status replay_byte_lag <<<"$standby_state"
[ "$in_recovery" = t ] || fail "standby=not_in_recovery"
if [ "$receiver_status" != streaming ]; then
  systemctl restart "$TUNNEL_SERVICE" || fail "receiver=$receiver_status tunnel_restart=failed"
  healed_tunnel=true
  for _ in $(seq 1 "$WAIT_ATTEMPTS"); do
    standby_state="$(query_standby 2>/dev/null || true)"
    IFS='|' read -r in_recovery receiver_status replay_byte_lag <<<"$standby_state"
    [ "$in_recovery" = t ] || fail "standby=not_in_recovery"
    [ "$receiver_status" = streaming ] && break
    sleep 1
  done
fi
[ "$receiver_status" = streaming ] || fail "receiver=$receiver_status"
case "$replay_byte_lag" in ''|*[!0-9]*) fail "replay_byte_lag=invalid" ;; esac
[ "$replay_byte_lag" -le "$MAX_REPLAY_BYTE_LAG" ] \
  || fail "replay_byte_lag=$replay_byte_lag threshold=$MAX_REPLAY_BYTE_LAG"

write_status healthy \
  "standby=in_recovery receiver=streaming replay_byte_lag=$replay_byte_lag healed_postgres=$healed_postgres healed_tunnel=$healed_tunnel"
echo "PostgreSQL streaming watchdog passed: replay_byte_lag=$replay_byte_lag"
