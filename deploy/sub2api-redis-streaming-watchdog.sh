#!/usr/bin/env bash

# Monitor an already-configured Redis replica. This watchdog can only start the
# target Redis, restart its SSH tunnel, and report health. It does not change
# replication topology or perform any promotion action.
set -Eeuo pipefail

LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-/run/lock/sub2api-maintenance.lock}"
LOCK_HELD="${SUB2API_REDIS_STREAMING_LOCK_HELD:-false}"
RUNTIME_ENV_FILE="${SUB2API_REDIS_RUNTIME_ENV_FILE:-/etc/sub2api-redis-streaming/runtime.env}"
STATUS_FILE="${SUB2API_REDIS_STREAMING_STATUS_FILE:-/run/sub2api-redis-streaming.status}"
WAIT_ATTEMPTS="${SUB2API_REDIS_STREAMING_WAIT_ATTEMPTS:-30}"
MAX_OFFSET_LAG="${SUB2API_REDIS_STREAMING_MAX_OFFSET_LAG:-268435456}"
TUNNEL_SERVICE="${SUB2API_REDIS_TUNNEL_SERVICE:-sub2api-redis-streaming-tunnel.service}"

die() { echo "ERROR: $*" >&2; exit 1; }

validate_root_secret() {
  local path="$1" mode
  [ -f "$path" ] || fail "runtime_auth=not_regular_file"
  [ "$(stat -c '%U' "$path")" = root ] || fail "runtime_auth=not_root_owned"
  mode="$(stat -c '%a' "$path")"
  [ "$((8#$mode & 077))" -eq 0 ] || fail "runtime_auth=not_root_only"
  awk 'NR > 1 { exit 1 } !/^[[:xdigit:]]{48,}$/ { exit 1 } END { if (NR != 1) exit 1 }' "$path" || fail "runtime_auth=invalid_format"
}
read_env_value() {
  local key="$1"
  awk -v expected="$key" '
    index($0, expected "=") == 1 { value = substr($0, length(expected) + 2); found++ }
    END { if (found != 1 || value == "") exit 1; print value }
  ' "$RUNTIME_ENV_FILE"
}
write_status() {
  local state="$1"
  shift
  local status_dir status_temp
  status_dir="$(dirname "$STATUS_FILE")"
  [ -d "$status_dir" ] || mkdir -p -m 755 "$status_dir"
  status_temp="$(mktemp "$status_dir/.sub2api-redis-streaming.XXXXXX")"
  printf 'state=%s checked_at=%s %s\n' "$state" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >"$status_temp"
  chmod 644 "$status_temp"
  mv -f "$status_temp" "$STATUS_FILE"
}
fail() {
  local reason="$*"
  write_status unhealthy "reason=$reason" >/dev/null 2>&1 || true
  trap - ERR
  die "$reason"
}
require_cmd() { command -v "$1" >/dev/null 2>&1 || fail "command_missing=$1"; }
unexpected_failure() {
  local status=$?
  trap - ERR
  [ "$status" -eq 0 ] || write_status unhealthy "reason=unexpected_error" >/dev/null 2>&1 || true
  exit "$status"
}
candidate_app_stopped() {
  [ "$(docker inspect -f '{{.State.Running}}' "$APP_CONTAINER")" = false ]
}

case "$STATUS_FILE" in /*) ;; *) die "status path must be absolute" ;; esac
trap unexpected_failure ERR
write_status checking "phase=preflight" || die "cannot write Redis streaming status"
case "$LOCK_FILE" in /*) ;; *) fail "maintenance_lock=invalid_path" ;; esac
case "$WAIT_ATTEMPTS" in ''|*[!0-9]*) fail "wait_attempts=invalid" ;; esac
case "$MAX_OFFSET_LAG" in ''|*[!0-9]*) fail "offset_lag_threshold=invalid" ;; esac
[ "$WAIT_ATTEMPTS" -gt 0 ] || fail "wait_attempts=nonpositive"
[ "$(id -u)" -eq 0 ] || fail "execution=not_root"
for command_name in awk date dirname docker flock mkdir mktemp mv python3 seq sleep stat systemctl; do require_cmd "$command_name"; done
[ -f "$RUNTIME_ENV_FILE" ] || fail "runtime_env=missing"
[ "$(stat -c '%a:%U' "$RUNTIME_ENV_FILE")" = 600:root ] || fail "runtime_env=not_root_owned_mode_0600"

REDIS_CONTAINER="$(read_env_value SUB2API_REDIS_STANDBY_CONTAINER)" || fail "runtime_env=missing_redis_container"
APP_CONTAINER="$(read_env_value SUB2API_REDIS_CANDIDATE_APP_CONTAINER)" || fail "runtime_env=missing_app_container"
RUNTIME_AUTH_FILE="$(read_env_value SUB2API_REDIS_RUNTIME_AUTH_FILE)" || fail "runtime_env=missing_auth_file"
TUNNEL_BIND="$(read_env_value SUB2API_REDIS_TUNNEL_BIND)" || fail "runtime_env=missing_tunnel_bind"
TUNNEL_PORT="$(read_env_value SUB2API_REDIS_TUNNEL_PORT)" || fail "runtime_env=missing_tunnel_port"
for value in "$REDIS_CONTAINER" "$APP_CONTAINER"; do
  case "$value" in ''|-*|*[!a-zA-Z0-9_.-]*) fail "runtime_env=invalid_container_name" ;; esac
done
case "$TUNNEL_PORT" in ''|*[!0-9]*) fail "tunnel_port=invalid" ;; esac
[ "$TUNNEL_PORT" -ge 1 ] && [ "$TUNNEL_PORT" -le 65535 ] || fail "tunnel_port=out_of_range"
python3 - "$TUNNEL_BIND" <<'PY' || fail "tunnel_bind=invalid"
import ipaddress
import sys

address = ipaddress.ip_address(sys.argv[1])
if address.version != 4 or address.is_unspecified:
    raise SystemExit(1)
PY
validate_root_secret "$RUNTIME_AUTH_FILE"

if [ "$LOCK_HELD" != true ]; then
  exec 9>"$LOCK_FILE"
  flock -w 30 9 || fail "maintenance_lock=unavailable"
fi

docker inspect "$REDIS_CONTAINER" >/dev/null 2>&1 || fail "standby_container=missing"
docker inspect "$APP_CONTAINER" >/dev/null 2>&1 || fail "candidate_app_container=missing"
candidate_app_stopped || fail "candidate_app=unexpectedly_running"

healed_redis=false
healed_tunnel=false
if [ "$(docker inspect -f '{{.State.Running}}' "$REDIS_CONTAINER")" != true ]; then
  candidate_app_stopped || fail "candidate_app=unexpectedly_running"
  docker start "$REDIS_CONTAINER" >/dev/null || fail "standby_container=start_failed"
  healed_redis=true
fi
if ! systemctl is-active --quiet "$TUNNEL_SERVICE"; then
  candidate_app_stopped || fail "candidate_app=unexpectedly_running"
  systemctl restart "$TUNNEL_SERVICE" || fail "tunnel=restart_failed"
  healed_tunnel=true
fi

query_replication() {
  awk 'NR == 1 { print; exit }' "$RUNTIME_AUTH_FILE" | docker exec -i "$REDIS_CONTAINER" sh -ceu '
    IFS= read -r candidate_auth
    REDISCLI_AUTH="$candidate_auth" redis-cli --no-auth-warning INFO replication
  '
}
parse_info() {
  local field="$1"
  awk -F: -v expected="$field" '$1 == expected { gsub(/\r/, "", $2); print $2; exit }'
}

replication_info=""
role=""
link=""
sync=""
master_host=""
master_port=""
for _ in $(seq 1 "$WAIT_ATTEMPTS"); do
  candidate_app_stopped || fail "candidate_app=unexpectedly_running"
  replication_info="$(query_replication 2>/dev/null || true)"
  if [ -z "$replication_info" ]; then
    sleep 1
    continue
  fi
  role="$(printf '%s\n' "$replication_info" | parse_info role)"
  master_host="$(printf '%s\n' "$replication_info" | parse_info master_host)"
  master_port="$(printf '%s\n' "$replication_info" | parse_info master_port)"
  link="$(printf '%s\n' "$replication_info" | parse_info master_link_status)"
  sync="$(printf '%s\n' "$replication_info" | parse_info master_sync_in_progress)"
  [ "$master_host" = "$TUNNEL_BIND" ] && [ "$master_port" = "$TUNNEL_PORT" ] \
    || fail "upstream=unexpected"
  [ "$role" = slave ] || fail "standby_role=$role"
  if [ "$link" = up ] && [ "$sync" = 0 ]; then break; fi
  if [ "$healed_tunnel" = false ]; then
    candidate_app_stopped || fail "candidate_app=unexpectedly_running"
    systemctl restart "$TUNNEL_SERVICE" || fail "tunnel=restart_failed"
    healed_tunnel=true
  fi
  sleep 1
done
[ "$role" = slave ] || fail "standby_role=$role"
[ "$link" = up ] || fail "link=$link"
[ "$sync" = 0 ] || fail "sync=$sync"

master_offset="$(printf '%s\n' "$replication_info" | parse_info master_repl_offset)"
replica_offset="$(printf '%s\n' "$replication_info" | parse_info slave_repl_offset)"
case "$master_offset" in ''|*[!0-9]*) fail "master_offset=invalid" ;; esac
case "$replica_offset" in ''|*[!0-9]*) fail "replica_offset=invalid" ;; esac
[ "$master_offset" -ge "$replica_offset" ] || fail "offset_lag=negative"
offset_lag="$((master_offset - replica_offset))"
[ "$offset_lag" -le "$MAX_OFFSET_LAG" ] || fail "offset_lag=$offset_lag threshold=$MAX_OFFSET_LAG"

write_status healthy "sync_phase=incremental role=slave link=up sync=0 offset_lag=$offset_lag healed_redis=$healed_redis healed_tunnel=$healed_tunnel"
echo "Redis streaming watchdog passed: offset_lag=$offset_lag"
