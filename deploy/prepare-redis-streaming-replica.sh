#!/usr/bin/env bash

# Configure a stopped candidate application's Redis as a replica. This creates
# pre-sync only and must never promote the candidate.
set -Eeuo pipefail

ORIGINAL_ARGS=("$@")
LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-/run/lock/sub2api-maintenance.lock}"
LOCKED=false
RUNTIME_ENV_FILE="${SUB2API_REDIS_RUNTIME_ENV_FILE:-/etc/sub2api-redis-streaming/runtime.env}"
MASTER_CREDENTIAL_FILE=""
STATUS_FILE="${SUB2API_REDIS_STREAMING_STATUS_FILE:-/run/sub2api-redis-streaming.status}"
WAIT_ATTEMPTS="${SUB2API_REDIS_STREAMING_WAIT_ATTEMPTS:-90}"

usage() {
  cat <<'EOF'
Usage: prepare-redis-streaming-replica.sh \
  --runtime-env-file PATH --master-credential-file PATH

The root-only runtime environment identifies the candidate Redis, candidate app,
internal Docker network, local Redis credential file, container configuration
path, and its mode-0600 host directory-bind source. The host configuration
keeps the Redis container's numeric uid/gid. The root-only master credential is
the source replication ACL password.
EOF
}
die() { echo "ERROR: $*" >&2; exit 1; }
require_cmd() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }

validate_root_secret() {
  local path="$1" mode
  [ -f "$path" ] || die "credential file must be a regular file: $path"
  [ "$(stat -c '%U' "$path")" = root ] || die "credential file must be owned by root: $path"
  mode="$(stat -c '%a' "$path")"
  [ "$((8#$mode & 077))" -eq 0 ] || die "credential file must not grant group/other access: $path"
  awk 'NR > 1 { exit 1 } !/^[[:xdigit:]]{48,}$/ { exit 1 } END { if (NR != 1) exit 1 }' "$path" \
    || die "credential file must contain one 48+ character hexadecimal value"
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
  install -d -o root -g root -m 755 "$status_dir"
  status_temp="$(mktemp "$status_dir/.sub2api-redis-streaming.XXXXXX")"
  printf 'state=%s checked_at=%s %s\n' "$state" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >"$status_temp"
  chmod 644 "$status_temp"
  mv -f "$status_temp" "$STATUS_FILE"
}
fail() { write_status unhealthy "$*"; die "$*"; }

while [ "$#" -gt 0 ]; do
  case "$1" in
    --runtime-env-file) RUNTIME_ENV_FILE="${2:-}"; shift ;;
    --master-credential-file) MASTER_CREDENTIAL_FILE="${2:-}"; shift ;;
    --locked) LOCKED=true ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
  shift
done

case "$LOCK_FILE" in /*) ;; *) die "maintenance lock path must be absolute" ;; esac
case "$STATUS_FILE" in /*) ;; *) die "status path must be absolute" ;; esac
if [ "$LOCKED" != true ]; then
  command -v flock >/dev/null 2>&1 || die "flock is required"
  exec flock -w 30 "$LOCK_FILE" "$0" --locked "${ORIGINAL_ARGS[@]}"
fi

[ "$(id -u)" -eq 0 ] || die "run as root on the Tokyo candidate host"
for command_name in awk date dirname docker flock install mktemp mv python3 sed seq sleep stat systemctl tr; do require_cmd "$command_name"; done
[ -f "$RUNTIME_ENV_FILE" ] || die "runtime environment file is missing"
validate_root_secret "$MASTER_CREDENTIAL_FILE"
[ "$(stat -c '%a:%U' "$RUNTIME_ENV_FILE")" = 600:root ] || die "runtime environment must be root-owned mode 0600"

REDIS_CONTAINER="$(read_env_value SUB2API_REDIS_STANDBY_CONTAINER)" || die "runtime environment is missing SUB2API_REDIS_STANDBY_CONTAINER"
APP_CONTAINER="$(read_env_value SUB2API_REDIS_CANDIDATE_APP_CONTAINER)" || die "runtime environment is missing SUB2API_REDIS_CANDIDATE_APP_CONTAINER"
PROBE_NETWORK="$(read_env_value SUB2API_REDIS_PROBE_NETWORK)" || die "runtime environment is missing SUB2API_REDIS_PROBE_NETWORK"
RUNTIME_AUTH_FILE="$(read_env_value SUB2API_REDIS_RUNTIME_AUTH_FILE)" || die "runtime environment is missing SUB2API_REDIS_RUNTIME_AUTH_FILE"
RUNTIME_CONFIG="$(read_env_value SUB2API_REDIS_RUNTIME_CONFIG)" || die "runtime environment is missing SUB2API_REDIS_RUNTIME_CONFIG"
RUNTIME_CONFIG_SOURCE="$(read_env_value SUB2API_REDIS_RUNTIME_CONFIG_SOURCE)" || die "runtime environment is missing SUB2API_REDIS_RUNTIME_CONFIG_SOURCE"
TUNNEL_BIND="$(read_env_value SUB2API_REDIS_TUNNEL_BIND)" || die "runtime environment is missing SUB2API_REDIS_TUNNEL_BIND"
TUNNEL_PORT="$(read_env_value SUB2API_REDIS_TUNNEL_PORT)" || die "runtime environment is missing SUB2API_REDIS_TUNNEL_PORT"
REPLICATION_USER="$(read_env_value SUB2API_REDIS_REPLICATION_USER)" || die "runtime environment is missing SUB2API_REDIS_REPLICATION_USER"
TUNNEL_SERVICE="${SUB2API_REDIS_TUNNEL_SERVICE:-sub2api-redis-streaming-tunnel.service}"

for value in "$REDIS_CONTAINER" "$APP_CONTAINER" "$PROBE_NETWORK" "$REPLICATION_USER"; do
  case "$value" in ''|-*|*[!a-zA-Z0-9_.-]*) die "runtime value contains unsupported characters" ;; esac
done
case "$RUNTIME_CONFIG" in /*) ;; *) die "runtime Redis configuration path must be absolute" ;; esac
case "$RUNTIME_CONFIG_SOURCE" in /*) ;; *) die "runtime Redis configuration source must be absolute" ;; esac
case "$TUNNEL_PORT" in ''|*[!0-9]*) die "tunnel port must be numeric" ;; esac
[ "$TUNNEL_PORT" -ge 1 ] && [ "$TUNNEL_PORT" -le 65535 ] || die "tunnel port is outside 1-65535"
case "$WAIT_ATTEMPTS" in ''|*[!0-9]*) die "wait attempts must be numeric" ;; esac
[ "$WAIT_ATTEMPTS" -gt 0 ] || die "wait attempts must be positive"
python3 - "$TUNNEL_BIND" <<'PY' || die "tunnel bind must be a non-wildcard IPv4 address"
import ipaddress
import sys
address = ipaddress.ip_address(sys.argv[1])
if address.version != 4 or address.is_unspecified:
    raise SystemExit(1)
PY
validate_root_secret "$RUNTIME_AUTH_FILE"
config_contract="$(python3 - "$RUNTIME_CONFIG_SOURCE" "$RUNTIME_CONFIG" <<'PY'
import os
import posixpath
import stat
import sys

source, destination = sys.argv[1:]
allowed = set("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._/-")
for label, path in (("source", source), ("destination", destination)):
    if not path.startswith("/") or path == "/" or posixpath.normpath(path) != path:
        raise SystemExit(f"{label} path is not normalized absolute")
    if any(character not in allowed for character in path):
        raise SystemExit(f"{label} path has unsupported characters")
if os.path.basename(source) != os.path.basename(destination):
    raise SystemExit("source and destination basenames differ")
source_directory = os.path.dirname(source)
destination_directory = os.path.dirname(destination)
source_stat = os.lstat(source)
directory_stat = os.lstat(source_directory)
if not stat.S_ISREG(source_stat.st_mode) or stat.S_ISLNK(source_stat.st_mode):
    raise SystemExit("source configuration is not a regular non-symlink file")
if not stat.S_ISDIR(directory_stat.st_mode) or stat.S_ISLNK(directory_stat.st_mode):
    raise SystemExit("source configuration directory is not a real directory")
if os.path.realpath(source) != source or os.path.realpath(source_directory) != source_directory:
    raise SystemExit("source configuration path traverses a symlink")
if stat.S_IMODE(source_stat.st_mode) != 0o600:
    raise SystemExit("source configuration mode is not 0600")
if (stat.S_IMODE(directory_stat.st_mode) != 0o700
        or directory_stat.st_uid != source_stat.st_uid
        or directory_stat.st_gid != source_stat.st_gid):
    raise SystemExit("source configuration directory ownership or mode differs")
print("|".join((
    source_directory,
    destination_directory,
    str(source_stat.st_uid),
    str(source_stat.st_gid),
    oct(stat.S_IMODE(source_stat.st_mode)),
)))
PY
)" || die "runtime Redis configuration source must be a normalized, non-symlink mode-0600 file"
IFS='|' read -r RUNTIME_CONFIG_SOURCE_DIR RUNTIME_CONFIG_DIR HOST_CONFIG_UID HOST_CONFIG_GID HOST_CONFIG_MODE <<<"$config_contract"
[ "$HOST_CONFIG_MODE" = 0o600 ] || die "runtime Redis configuration source mode contract is invalid"

docker inspect "$REDIS_CONTAINER" >/dev/null 2>&1 || fail "standby_container=missing"
docker inspect "$APP_CONTAINER" >/dev/null 2>&1 || fail "candidate_app_container=missing"
[ "$(docker inspect -f '{{.State.Running}}' "$APP_CONTAINER")" = false ] || fail "candidate_app=unexpectedly_running"
docker network inspect "$PROBE_NETWORK" >/dev/null 2>&1 || fail "candidate_network=missing"
[ "$(docker network inspect "$PROBE_NETWORK" --format '{{.Internal}}')" = true ] || fail "candidate_network=not_internal"
gateway="$(docker network inspect "$PROBE_NETWORK" --format '{{range .IPAM.Config}}{{.Gateway}}{{end}}')"
[ "$TUNNEL_BIND" = "$gateway" ] || fail "tunnel_bind=not_candidate_gateway"
redis_network_ip="$(docker inspect "$REDIS_CONTAINER" --format "{{with index .NetworkSettings.Networks \"$PROBE_NETWORK\"}}{{.IPAddress}}{{end}}")"
[ -n "$redis_network_ip" ] || fail "standby_container=not_on_candidate_network"
config_mount="$(docker inspect "$REDIS_CONTAINER" --format "{{range .Mounts}}{{if eq .Destination \"$RUNTIME_CONFIG_DIR\"}}{{.Source}}|{{.Destination}}|{{.RW}}|{{.Type}}{{end}}{{end}}")"
[ "$config_mount" = "$RUNTIME_CONFIG_SOURCE_DIR|$RUNTIME_CONFIG_DIR|false|bind" ] || fail "runtime_config=not_exact_readonly_directory_bind_mount"
systemctl is-active --quiet "$TUNNEL_SERVICE" || fail "tunnel=inactive"
if [ "$(docker inspect -f '{{.State.Running}}' "$REDIS_CONTAINER")" != true ]; then
  docker start "$REDIS_CONTAINER" >/dev/null || fail "standby_container=start_failed"
fi
container_config_metadata="$(docker exec "$REDIS_CONTAINER" stat -c '%u:%g:%a' "$RUNTIME_CONFIG")" \
 || fail "runtime_config=container_stat_failed"
[ "$container_config_metadata" = "$HOST_CONFIG_UID:$HOST_CONFIG_GID:600" ] \
 || fail "runtime_config=uid_gid_mode_mismatch"

query_replication() {
  awk 'NR == 1 { print; exit }' "$RUNTIME_AUTH_FILE" | docker exec -i "$REDIS_CONTAINER" sh -ceu '
    IFS= read -r candidate_auth
    REDISCLI_AUTH="$candidate_auth" redis-cli --no-auth-warning INFO replication
  '
}
replication_field() {
  local field="$1"
  awk -F: -v expected="$field" '$1 == expected { gsub(/\r/, "", $2); print $2; exit }'
}
wait_for_queryable_replication() {
  local replication_info
  for _ in $(seq 1 "$WAIT_ATTEMPTS"); do
    [ "$(docker inspect -f '{{.State.Running}}' "$APP_CONTAINER")" = false ] || fail "candidate_app=unexpectedly_running"
    replication_info="$(query_replication 2>/dev/null || true)"
    if [ -n "$replication_info" ]; then
      printf '%s\n' "$replication_info"
      return 0
    fi
    sleep 1
  done
  return 1
}

initial_replication_info="$(wait_for_queryable_replication)" || fail "standby=not_queryable"
candidate_role="$(printf '%s\n' "$initial_replication_info" | replication_field role)"

if [ "$candidate_role" = master ]; then
candidate_app_running=false
[ "$(docker inspect -f '{{.State.Running}}' "$APP_CONTAINER")" = false ] || candidate_app_running=true
[ "$candidate_app_running" = false ] || fail "candidate_app=unexpectedly_running"
awk 'NR == 1 { print; exit }' "$MASTER_CREDENTIAL_FILE" | python3 -c '
import os
import stat
import sys
import tempfile

path, replication_user, tunnel_bind, tunnel_port, expected_uid, expected_gid = sys.argv[1:]
expected_uid = int(expected_uid)
expected_gid = int(expected_gid)
master_auth = sys.stdin.readline().rstrip("\r\n")
st = os.lstat(path)
if (not stat.S_ISREG(st.st_mode) or stat.S_ISLNK(st.st_mode)
        or stat.S_IMODE(st.st_mode) != 0o600
        or st.st_uid != expected_uid or st.st_gid != expected_gid):
    raise SystemExit("runtime configuration source metadata changed")
directory = os.path.dirname(path)
directory_stat = os.lstat(directory)
if (not stat.S_ISDIR(directory_stat.st_mode) or stat.S_ISLNK(directory_stat.st_mode)
        or stat.S_IMODE(directory_stat.st_mode) != 0o700
        or directory_stat.st_uid != expected_uid or directory_stat.st_gid != expected_gid
        or os.path.realpath(path) != path or os.path.realpath(directory) != directory):
    raise SystemExit("runtime configuration source path is unsafe")
with open(path, "r", encoding="utf-8") as handle:
    existing = handle.readlines()
managed = {"masteruser", "masterauth", "replicaof"}
kept = []
for line in existing:
    fields = line.lstrip().split(None, 1)
    if fields and fields[0] in managed:
        continue
    kept.append(line)
if kept and not kept[-1].endswith("\n"):
    kept[-1] += "\n"
kept.extend((
    "masteruser " + replication_user + "\n",
    "masterauth " + master_auth + "\n",
    "replicaof " + tunnel_bind + " " + tunnel_port + "\n",
))
fd, temporary = tempfile.mkstemp(prefix=".sub2api-redis.", dir=directory, text=True)
try:
    os.fchmod(fd, 0o600)
    os.fchown(fd, expected_uid, expected_gid)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        handle.writelines(kept)
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, path)
    replaced = os.lstat(path)
    if (not stat.S_ISREG(replaced.st_mode) or stat.S_ISLNK(replaced.st_mode)
            or stat.S_IMODE(replaced.st_mode) != 0o600
            or replaced.st_uid != expected_uid or replaced.st_gid != expected_gid):
        raise SystemExit("runtime configuration replacement metadata changed")
    replaced_directory = os.lstat(directory)
    if (not stat.S_ISDIR(replaced_directory.st_mode) or stat.S_ISLNK(replaced_directory.st_mode)
            or stat.S_IMODE(replaced_directory.st_mode) != 0o700
            or replaced_directory.st_uid != expected_uid or replaced_directory.st_gid != expected_gid):
        raise SystemExit("runtime configuration directory metadata changed")
finally:
    if os.path.exists(temporary):
        os.unlink(temporary)
' "$RUNTIME_CONFIG_SOURCE" "$REPLICATION_USER" "$TUNNEL_BIND" "$TUNNEL_PORT" "$HOST_CONFIG_UID" "$HOST_CONFIG_GID" \
  || fail "replica_configuration=persistent_config_write_failed"

[ "$(docker inspect -f '{{.State.Running}}' "$APP_CONTAINER")" = false ] || fail "candidate_app=unexpectedly_running"
container_config_metadata="$(docker exec "$REDIS_CONTAINER" stat -c '%u:%g:%a' "$RUNTIME_CONFIG")" \
  || fail "runtime_config=container_stat_after_replace_failed"
[ "$container_config_metadata" = "$HOST_CONFIG_UID:$HOST_CONFIG_GID:600" ] \
  || fail "runtime_config=uid_gid_mode_mismatch_after_replace"
{
  awk 'NR == 1 { print; exit }' "$RUNTIME_AUTH_FILE"
  awk 'NR == 1 { print; exit }' "$MASTER_CREDENTIAL_FILE"
} | docker exec -i "$REDIS_CONTAINER" sh -ceu '
  replication_user="$1"; tunnel_bind="$2"; tunnel_port="$3"
  IFS= read -r candidate_auth; IFS= read -r master_auth
  resp() {
    printf "*%s\r\n" "$#"
    for argument; do printf "$%s\r\n" "${#argument}"; printf "%s\r\n" "$argument"; done
  }
  {
    resp CONFIG SET masteruser "$replication_user"
    resp CONFIG SET masterauth "$master_auth"
    resp REPLICAOF "$tunnel_bind" "$tunnel_port"
  } | REDISCLI_AUTH="$candidate_auth" redis-cli --no-auth-warning --pipe >/dev/null
' sh "$REPLICATION_USER" "$TUNNEL_BIND" "$TUNNEL_PORT" || fail "replica_configuration=runtime_apply_failed"
elif [ "$candidate_role" = slave ]; then
  resume_master_host="$(printf '%s\n' "$initial_replication_info" | replication_field master_host)"
  resume_master_port="$(printf '%s\n' "$initial_replication_info" | replication_field master_port)"
  [ "$resume_master_host" = "$TUNNEL_BIND" ] && [ "$resume_master_port" = "$TUNNEL_PORT" ] \
    || fail "resume_upstream=unexpected"

  awk 'NR == 1 { print; exit }' "$MASTER_CREDENTIAL_FILE" | python3 -c '
import os
import stat
import sys

path, replication_user, tunnel_bind, tunnel_port, expected_uid, expected_gid = sys.argv[1:]
expected_uid = int(expected_uid)
expected_gid = int(expected_gid)
master_auth = sys.stdin.readline().rstrip("\r\n")
st = os.lstat(path)
directory = os.path.dirname(path)
directory_st = os.lstat(directory)
if (not stat.S_ISREG(st.st_mode) or stat.S_ISLNK(st.st_mode)
        or stat.S_IMODE(st.st_mode) != 0o600
        or st.st_uid != expected_uid or st.st_gid != expected_gid
        or not stat.S_ISDIR(directory_st.st_mode) or stat.S_ISLNK(directory_st.st_mode)
        or stat.S_IMODE(directory_st.st_mode) != 0o700
        or directory_st.st_uid != expected_uid or directory_st.st_gid != expected_gid
        or os.path.realpath(path) != path or os.path.realpath(directory) != directory):
    raise SystemExit("persisted configuration metadata is unsafe")
expected = {
    "masteruser": replication_user,
    "masterauth": master_auth,
    "replicaof": tunnel_bind + " " + tunnel_port,
}
seen = {key: [] for key in expected}
with open(path, "r", encoding="utf-8") as handle:
    for raw_line in handle:
        fields = raw_line.lstrip().split(None, 1)
        if fields and fields[0] in seen:
            seen[fields[0]].append(fields[1].rstrip("\r\n") if len(fields) == 2 else "")
for key, value in expected.items():
    if seen[key] != [value]:
        raise SystemExit("persisted replication configuration is not exact")
' "$RUNTIME_CONFIG_SOURCE" "$REPLICATION_USER" "$TUNNEL_BIND" "$TUNNEL_PORT" "$HOST_CONFIG_UID" "$HOST_CONFIG_GID" \
    || fail "resume_config=not_exact"

  {
    awk 'NR == 1 { print; exit }' "$RUNTIME_AUTH_FILE"
    awk 'NR == 1 { print; exit }' "$MASTER_CREDENTIAL_FILE"
  } | docker exec -i "$REDIS_CONTAINER" sh -ceu '
    replication_user="$1"
    IFS= read -r candidate_auth
    IFS= read -r master_auth
    runtime_master_user="$(REDISCLI_AUTH="$candidate_auth" redis-cli --no-auth-warning --raw CONFIG GET masteruser | sed -n "2p")"
    runtime_master_auth="$(REDISCLI_AUTH="$candidate_auth" redis-cli --no-auth-warning --raw CONFIG GET masterauth | sed -n "2p")"
    [ "$runtime_master_user" = "$replication_user" ] && [ "$runtime_master_auth" = "$master_auth" ]
  ' sh "$REPLICATION_USER" || fail "resume_runtime_config=not_exact"
else
  fail "standby_role=$candidate_role"
fi

for _ in $(seq 1 "$WAIT_ATTEMPTS"); do
  [ "$(docker inspect -f '{{.State.Running}}' "$APP_CONTAINER")" = false ] || fail "candidate_app=unexpectedly_running"
  replication_info="$(query_replication 2>/dev/null || true)"
  if [ -z "$replication_info" ]; then
    sleep 1
    continue
  fi
  role="$(printf '%s\n' "$replication_info" | replication_field role)"
  [ "$role" = slave ] || fail "standby_role=$role"
  link="$(printf '%s\n' "$replication_info" | replication_field master_link_status)"
  sync="$(printf '%s\n' "$replication_info" | replication_field master_sync_in_progress)"
  if [ "$link" = up ] && [ "$sync" = 0 ]; then
    write_status healthy "sync_phase=full role=slave link=up sync=0"
    echo "Redis full pre-sync is ready."
    exit 0
  fi
  sleep 1
done
fail "full_sync=not_ready"
