#!/usr/bin/env bash

# Static and simulated checks for Redis pre-sync artifacts. This never contacts
# Docker, SSH, Redis, UFW, or a production endpoint.
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PRIMARY="$ROOT_DIR/deploy/install-redis-streaming-primary.sh"
TUNNEL_INSTALLER="$ROOT_DIR/deploy/install-redis-streaming-tunnel.sh"
TUNNEL="$ROOT_DIR/deploy/sub2api-redis-streaming-tunnel.sh"
TUNNEL_UNIT="$ROOT_DIR/deploy/sub2api-redis-streaming-tunnel.service"
PREPARE="$ROOT_DIR/deploy/prepare-redis-streaming-replica.sh"
WATCHDOG_INSTALLER="$ROOT_DIR/deploy/install-redis-streaming-watchdog.sh"
WATCHDOG="$ROOT_DIR/deploy/sub2api-redis-streaming-watchdog.sh"
WATCHDOG_UNIT="$ROOT_DIR/deploy/sub2api-redis-streaming-watchdog.service"
WATCHDOG_TIMER="$ROOT_DIR/deploy/sub2api-redis-streaming-watchdog.timer"
TEMP_DIR="$(mktemp -d "$ROOT_DIR/.redis-streaming-test.XXXXXX")"
PREPARE_CONFIG_PARENT=""
trap 'rm -rf "$TEMP_DIR" "$PREPARE_CONFIG_PARENT"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
need() { grep -Fq -- "$2" "$1" || fail "missing $2 in $1"; }

for script in "$PRIMARY" "$TUNNEL_INSTALLER" "$TUNNEL" "$PREPARE" "$WATCHDOG_INSTALLER" "$WATCHDOG"; do
  bash -n "$script"
done

need "$PRIMARY" 'RELAY_PORT='
need "$PRIMARY" '16380'
need "$PRIMARY" 'sub2api_replication'
need "$PRIMARY" 'exec flock -w 30 "$LOCK_FILE"'
need "$PRIMARY" 'docker network create --driver bridge --internal'
need "$PRIMARY" 'ListenStream=127.0.0.1:'
need "$PRIMARY" 'production Redis has a public host port binding'
need "$PRIMARY" 'from="%s",restrict,port-forwarding,permitopen="127.0.0.1:%s"'
need "$PRIMARY" 'PermitOpen 127.0.0.1:'
need "$PRIMARY" 'network.prefixlen != 32'
need "$PRIMARY" '+ping +replconf +psync'
need "$PRIMARY" 'CONFIG SET repl-backlog-size'
need "$PRIMARY" '64mb'
need "$PRIMARY" 'backlog_bytes'
need "$PRIMARY" 'EXPECTED_REPL_BACKLOG_BYTES'
need "$PRIMARY" 'ACL_CREATED'
need "$PRIMARY" '[ "$loaded_backlog" = "$EXPECTED_REPL_BACKLOG_BYTES" ]'
need "$PRIMARY" 'if grep -Fxq ACL_CREATED "$acl_marker_file"; then acl_created=true; fi'
need "$PRIMARY" '[ "$acl_transaction_status" -eq 0 ] || die "replication ACL/backlog transaction failed"'
need "$PRIMARY" 'persistence=runtime-only revalidate_before_cutover=true'
need "$PRIMARY" "relay does not drop all capabilities"
need "$PRIMARY" "relay has added capabilities"
need "$PRIMARY" "relay is missing no-new-privileges"
need "$PRIMARY" "relay uses an unexpected user"
need "$PRIMARY" "relay uses an unexpected restart policy"
need "$PRIMARY" "relay tmpfs differs"
need "$PRIMARY" "relay memory limit differs"
need "$PRIMARY" "relay CPU limit differs"
need "$PRIMARY" "relay configuration mount differs"
need "$PRIMARY" '{{range .Mounts}}{{.Source}}|{{.Destination}}|{{.RW}}|{{.Type}}{{end}}'
need "$PRIMARY" 'sanitize-payload'
need "$PRIMARY" 'sha256sum'
need "$PRIMARY" '--user "$replication_user" PING'
need "$PRIMARY" 'pre-existing replication ACL user does not match the required least-privilege credential contract'
if grep -Fq -- '--publish' "$PRIMARY"; then fail "source relay must not publish a Docker host port"; fi
if grep -Fq 'CONFIG REWRITE' "$PRIMARY"; then fail "command-line source Redis must not gate on CONFIG REWRITE"; fi
if grep -Eq 'redis-cli[^[:cntrl:]]*( -a |--pass)|docker exec[[:space:]]+-e[[:space:]]+[^[:space:]]*REDISCLI_AUTH|^[[:space:]]*set -x' "$PRIMARY" "$PREPARE" "$WATCHDOG"; then fail "credential exposure path found"; fi
if grep -Eq '^[[:space:]]*(echo|logger)[^#]*(source_auth|replication_auth|candidate_auth|master_auth)' "$PRIMARY" "$PREPARE" "$WATCHDOG"; then fail "credential log path found"; fi
if grep -Eq 'docker inspect[^[:cntrl:]]*(Config\.Cmd|Config\.Env|\.Args)' "$PRIMARY" "$TUNNEL_INSTALLER" "$PREPARE" "$WATCHDOG"; then fail "docker inspect must not render Cmd/Env"; fi

# Redis 8 ACL GETUSER raw fixture: flags include the implicit
# sanitize-payload flag and password digests are plain 64-character hex.
acl_section_fixture() {
  awk -v wanted="$1" '
    $0 == wanted { in_section=1; next }
    $0 ~ /^(flags|passwords|commands|keys|channels|selectors)$/ {
      if (in_section) exit
      next
    }
    in_section { print }
  '
}
validate_redis8_acl_fixture() {
  local acl_info="$1" replication_password="$2"
  local flags flag flag_count=0 have_on=false have_sanitize=false
  local password_hash passwords password_count=0 expected_password_hash
  local commands token token_count=0 have_disable=false have_ping=false have_replconf=false have_psync=false

  flags="$(printf '%s\n' "$acl_info" | acl_section_fixture flags)"
  for flag in $flags; do
    flag_count="$((flag_count + 1))"
    case "$flag" in
      on) [ "$have_on" = false ] || return 1; have_on=true ;;
      sanitize-payload) [ "$have_sanitize" = false ] || return 1; have_sanitize=true ;;
      *) return 1 ;;
    esac
  done
  [ "$flag_count" -eq 2 ] && [ "$have_on" = true ] && [ "$have_sanitize" = true ] || return 1

  expected_password_hash="$(printf '%s' "$replication_password" | sha256sum | awk '{print $1}')"
  passwords="$(printf '%s\n' "$acl_info" | acl_section_fixture passwords)"
  while IFS= read -r password_hash; do
    [ -n "$password_hash" ] || continue
    [ "$password_hash" = "$expected_password_hash" ] || return 1
    password_count="$((password_count + 1))"
  done <<EOF
$passwords
EOF
  [ "$password_count" -eq 1 ] || return 1

  commands="$(printf '%s\n' "$acl_info" | acl_section_fixture commands)"
  for token in $commands; do
    token_count="$((token_count + 1))"
    case "$token" in
      -@all) [ "$have_disable" = false ] || return 1; have_disable=true ;;
      +ping) [ "$have_ping" = false ] || return 1; have_ping=true ;;
      +replconf) [ "$have_replconf" = false ] || return 1; have_replconf=true ;;
      +psync) [ "$have_psync" = false ] || return 1; have_psync=true ;;
      *) return 1 ;;
    esac
  done
  [ "$token_count" -eq 4 ] && [ "$have_disable" = true ] && [ "$have_ping" = true ] \
    && [ "$have_replconf" = true ] && [ "$have_psync" = true ] || return 1
  [ -z "$(printf '%s\n' "$acl_info" | acl_section_fixture keys)" ] || return 1
  [ -z "$(printf '%s\n' "$acl_info" | acl_section_fixture channels)" ] || return 1
  [ -z "$(printf '%s\n' "$acl_info" | acl_section_fixture selectors)" ]
}
fixture_password=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
fixture_hash="$(printf '%s' "$fixture_password" | sha256sum | awk '{print $1}')"
fixture_acl="$(printf 'flags\non\nsanitize-payload\npasswords\n%s\ncommands\n-@all +ping +replconf +psync\nkeys\nchannels\nselectors\n' "$fixture_hash")"
extra_password_acl="$(printf 'flags\non\nsanitize-payload\npasswords\n%s\ndeadbeef\ncommands\n-@all +ping +replconf +psync\nkeys\nchannels\nselectors\n' "$fixture_hash")"
nopass_acl="$(printf 'flags\non\nsanitize-payload\nnopass\npasswords\n%s\ncommands\n-@all +ping +replconf +psync\nkeys\nchannels\nselectors\n' "$fixture_hash")"
broad_acl="$(printf 'flags\non\nsanitize-payload\npasswords\n%s\ncommands\n-@all +ping +replconf +psync +get\nkeys\nchannels\nselectors\n' "$fixture_hash")"
validate_redis8_acl_fixture "$fixture_acl" "$fixture_password" || fail "Redis 8 exact ACL fixture was rejected"
if validate_redis8_acl_fixture "$extra_password_acl" "$fixture_password"; then fail "extra Redis ACL password was accepted"; fi
if validate_redis8_acl_fixture "$nopass_acl" "$fixture_password"; then fail "nopass Redis ACL was accepted"; fi
if validate_redis8_acl_fixture "$broad_acl" "$fixture_password"; then fail "broad Redis ACL command was accepted"; fi


need "$TUNNEL_INSTALLER" 'local bind must equal the candidate network gateway'
need "$TUNNEL_INSTALLER" 'ufw allow in on $probe_bridge from $probe_subnet to $probe_gateway port $LOCAL_PORT proto tcp'
need "$TUNNEL_INSTALLER" "comment 'Sub2API internal Redis streaming tunnel'"
need "$TUNNEL_INSTALLER" 'ufw --force delete allow in on "$probe_bridge" from "$probe_subnet" to "$probe_gateway" port "$LOCAL_PORT" proto tcp'
need "$TUNNEL" '-o StrictHostKeyChecking=yes'
need "$TUNNEL" '-o UserKnownHostsFile="$KNOWN_HOSTS"'
need "$TUNNEL" '-o ExitOnForwardFailure=yes'
need "$TUNNEL" '-o ServerAliveInterval=15'
need "$TUNNEL_UNIT" 'Restart=always'

need "$PREPARE" 'SUB2API_REDIS_RUNTIME_CONFIG_SOURCE'
need "$PREPARE" 'runtime_config=not_exact_readonly_directory_bind_mount'
need "$PREPARE" '{{.Source}}|{{.Destination}}|{{.RW}}|{{.Type}}'
need "$PREPARE" 'os.replace(temporary, path)'
need "$PREPARE" 'os.lstat(source)'
need "$PREPARE" 'source and destination basenames differ'
need "$PREPARE" 'str(source_stat.st_uid)'
need "$PREPARE" 'expected_uid'
need "$PREPARE" 'runtime_config=uid_gid_mode_mismatch'
need "$PREPARE" 'stat.S_IMODE(directory_stat.st_mode) != 0o700'
need "$PREPARE" 'directory_stat.st_uid != source_stat.st_uid'
need "$PREPARE" 'replaced_directory = os.lstat(directory)'
need "$PREPARE" 'masteruser '
need "$PREPARE" 'masterauth '
need "$PREPARE" 'replicaof '
need "$PREPARE" 'CONFIG SET masteruser'
need "$PREPARE" 'CONFIG SET masterauth'
need "$PREPARE" 'resp REPLICAOF'
need "$PREPARE" 'wait_for_queryable_replication'
need "$PREPARE" 'resume_upstream=unexpected'
need "$PREPARE" 'resume_config=not_exact'
need "$PREPARE" 'resume_runtime_config=not_exact'
need "$PREPARE" 'sync_phase=full role=slave link=up sync=0'
if grep -Fq 'CONFIG REWRITE' "$PREPARE"; then fail "readonly candidate config must not use CONFIG REWRITE"; fi
if grep -Fq 'REPLICAOF NO ONE' "$PREPARE"; then fail "prepare must not promote candidate"; fi

need "$WATCHDOG" 'candidate_app=unexpectedly_running'
need "$WATCHDOG" 'docker start "$REDIS_CONTAINER"'
need "$WATCHDOG" 'systemctl restart "$TUNNEL_SERVICE"'
need "$WATCHDOG" 'master_link_status'
need "$WATCHDOG" 'master_sync_in_progress'
need "$WATCHDOG" 'SUB2API_REDIS_TUNNEL_BIND'
need "$WATCHDOG" 'SUB2API_REDIS_TUNNEL_PORT'
need "$WATCHDOG" 'upstream=unexpected'
need "$WATCHDOG" 'tunnel_bind=invalid'
need "$WATCHDOG" 'master_repl_offset'
need "$WATCHDOG" 'slave_repl_offset'
need "$WATCHDOG" 'if [ -z "$replication_info" ]; then'
need "$WATCHDOG" 'write_status unhealthy "reason=$reason"'
need "$WATCHDOG" 'sync_phase=incremental role=slave link=up sync=0'
for forbidden in REPLICAOF SLAVEOF 'NO ONE' FAILOVER; do
  if grep -Fq "$forbidden" "$WATCHDOG"; then fail "watchdog topology command: $forbidden"; fi
done
need "$WATCHDOG_TIMER" 'OnUnitActiveSec=1min'
need "$WATCHDOG_TIMER" 'Persistent=true'
need "$WATCHDOG_UNIT" 'ReadWritePaths=/run'
need "$WATCHDOG_INSTALLER" 'SUB2API_REDIS_STREAMING_LOCK_HELD=true "$runner_target"'

mkdir -p "$TEMP_DIR/lock-bin"
cat >"$TEMP_DIR/lock-bin/flock" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >"$REDIS_STREAMING_FLOCK_LOG"
EOF
chmod +x "$TEMP_DIR/lock-bin/flock"
REDIS_STREAMING_FLOCK_LOG="$TEMP_DIR/primary-flock.log" \
  SUB2API_MAINTENANCE_LOCK_FILE="$TEMP_DIR/shared-maintenance.lock" \
  PATH="$TEMP_DIR/lock-bin:$PATH" \
  "$PRIMARY" --public-key-file /not-used --source-auth-file /not-used \
  --replication-auth-file /not-used --source-cidr 192.0.2.41/32
need "$TEMP_DIR/primary-flock.log" "-w 30 $TEMP_DIR/shared-maintenance.lock"

# Candidate preparation simulation covers an exact read-only directory bind,
# numeric (non-root service compatible) ownership preservation, bounded
# queryability startup, exact resume, and fail-closed wrong-upstream handling.
python3 - <<'PY'
import stat

def valid(file_meta, directory_meta):
    file_uid, file_gid, file_mode = file_meta
    dir_uid, dir_gid, dir_mode = directory_meta
    return (file_mode == 0o600 and dir_mode == 0o700
            and file_uid == dir_uid and file_gid == dir_gid)

assert valid((999, 1000, 0o600), (999, 1000, 0o700))
assert not valid((999, 1000, 0o600), (999, 1000, 0o755))
assert not valid((999, 1000, 0o600), (998, 1000, 0o700))
assert not valid((999, 1000, 0o600), (999, 999, 0o700))
PY

PREPARE_CONFIG_PARENT="$(mktemp -d "$(dirname "$(dirname "$ROOT_DIR")")/.redis-streaming-prepare.XXXXXX")"
mkdir -p "$TEMP_DIR/prepare-bin" "$PREPARE_CONFIG_PARENT/runtime"
cat >"$TEMP_DIR/prepare-bin/id" <<'EOF'
#!/usr/bin/env bash
[ "${1:-}" = -u ] && { echo 0; exit 0; }
exit 1
EOF
cat >"$TEMP_DIR/prepare-bin/stat" <<'EOF'
#!/usr/bin/env bash
case "${2:-}" in
  '%a:%U') echo 600:root ;;
  '%a') echo 600 ;;
  '%U') echo root ;;
  *) exit 1 ;;
esac
EOF
cat >"$TEMP_DIR/prepare-bin/flock" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat >"$TEMP_DIR/prepare-bin/install" <<'EOF'
#!/usr/bin/env bash
for target; do :; done
mkdir -p "$target"
EOF
cat >"$TEMP_DIR/prepare-bin/systemctl" <<'EOF'
#!/usr/bin/env bash
[ "${1:-}" = is-active ] && exit 0
exit 1
EOF
cat >"$TEMP_DIR/prepare-bin/sleep" <<'EOF'
#!/usr/bin/env bash
[ -z "${PREPARE_SLEEP_LOG:-}" ] || printf '%s\n' "${1:-}" >>"$PREPARE_SLEEP_LOG"
EOF
cat >"$TEMP_DIR/prepare-bin/docker" <<'EOF'
#!/usr/bin/env bash
set -eu
operation="${1:-}"
shift || true
case "$operation" in
  inspect)
    if [ "${1:-}" = -f ]; then
      template="${2:-}"
      container="${3:-}"
      case "$template" in
        *State.Running*)
          if [ "$container" = "$PREPARE_APP_CONTAINER" ]; then echo false; else echo true; fi
          ;;
      esac
    else
      all=" $* "
      case "$all" in
        *NetworkSettings.Networks*) echo 172.30.241.5 ;;
        *range\ .Mounts*) printf '%s\n' "$PREPARE_CONFIG_MOUNT" ;;
      esac
    fi
    ;;
  network)
    all=" $* "
    case "$all" in
      *'{{.Internal}}'*) echo true ;;
      *'{{range .IPAM.Config}}'*) echo 172.30.241.1 ;;
    esac
    ;;
  start) exit 0 ;;
  exec)
    all=" $* "
    # The real docker exec -i consumes credentials piped by the runner. The
    # fake must do the same or pipefail can race an upstream awk into SIGPIPE.
    case "$all" in *' -i '*) cat >/dev/null ;; esac
    case "$all" in
      *'%u:%g:%a'*) printf '%s\n' "$PREPARE_CONFIG_META" ;;
      *'INFO replication'*)
        count=0
        [ ! -f "$PREPARE_QUERY_COUNT" ] || count="$(cat "$PREPARE_QUERY_COUNT")"
        count="$((count + 1))"
        printf '%s\n' "$count" >"$PREPARE_QUERY_COUNT"
        if [ "$count" -le "${PREPARE_EMPTY_QUERIES:-0}" ]; then exit 0; fi
        case "$(cat "$PREPARE_REPLICA_STATE")" in
          master)
            printf 'role:master\n'
            ;;
          resume|slave)
            printf 'role:slave\nmaster_host:172.30.241.1\nmaster_port:16380\nmaster_link_status:up\nmaster_sync_in_progress:0\n'
            ;;
          wrong)
            printf 'role:slave\nmaster_host:198.51.100.7\nmaster_port:16380\nmaster_link_status:up\nmaster_sync_in_progress:0\n'
            ;;
          *) exit 1 ;;
        esac
        ;;
      *'REPLICAOF'*)
        printf 'runtime_topology_apply\n' >>"$PREPARE_ACTION_LOG"
        printf 'slave\n' >"$PREPARE_REPLICA_STATE"
        ;;
      *'CONFIG GET masteruser'*)
        [ "${PREPARE_RUNTIME_CONFIG_MATCH:-true}" = true ]
        ;;
    esac
    ;;
  *) exit 1 ;;
esac
EOF
chmod +x "$TEMP_DIR/prepare-bin/"*

prepare_config_dir="$PREPARE_CONFIG_PARENT/runtime"
prepare_config="$prepare_config_dir/redis.conf"
prepare_auth="$TEMP_DIR/prepare-runtime-auth"
prepare_master_auth="$TEMP_DIR/prepare-master-auth"
prepare_env="$TEMP_DIR/prepare-runtime.env"
prepare_status="$TEMP_DIR/prepare.status"
prepare_state="$TEMP_DIR/prepare-replica-state"
prepare_queries="$TEMP_DIR/prepare-query-count"
prepare_actions="$TEMP_DIR/prepare-actions.log"
prepare_sleeps="$TEMP_DIR/prepare-sleeps.log"
printf 'appendonly yes\n' >"$prepare_config"
chmod 700 "$prepare_config_dir"
chmod 600 "$prepare_config"
printf '%048d\n' 0 >"$prepare_auth"
printf '%048d\n' 1 >"$prepare_master_auth"
cat >"$prepare_env" <<EOF
SUB2API_REDIS_STANDBY_CONTAINER=sub2api-migration-redis
SUB2API_REDIS_CANDIDATE_APP_CONTAINER=sub2api-migration-app-candidate
SUB2API_REDIS_PROBE_NETWORK=sub2api-candidate-internal
SUB2API_REDIS_RUNTIME_AUTH_FILE=${prepare_auth}
SUB2API_REDIS_RUNTIME_CONFIG=/run/sub2api-redis/redis.conf
SUB2API_REDIS_RUNTIME_CONFIG_SOURCE=${prepare_config}
SUB2API_REDIS_TUNNEL_BIND=172.30.241.1
SUB2API_REDIS_TUNNEL_PORT=16380
SUB2API_REDIS_REPLICATION_USER=sub2api_replication
EOF
prepare_uid="$(python3 -c 'import os; print(os.stat(__import__("sys").argv[1]).st_uid)' "$prepare_config")"
prepare_gid="$(python3 -c 'import os; print(os.stat(__import__("sys").argv[1]).st_gid)' "$prepare_config")"
prepare_common_env=(
  PATH="$TEMP_DIR/prepare-bin:$PATH"
  PREPARE_APP_CONTAINER=sub2api-migration-app-candidate
  PREPARE_CONFIG_MOUNT="$prepare_config_dir|/run/sub2api-redis|false|bind"
  PREPARE_CONFIG_META="$prepare_uid:$prepare_gid:600"
  PREPARE_REPLICA_STATE="$prepare_state"
  PREPARE_QUERY_COUNT="$prepare_queries"
  PREPARE_ACTION_LOG="$prepare_actions"
  PREPARE_SLEEP_LOG="$prepare_sleeps"
  SUB2API_REDIS_STREAMING_STATUS_FILE="$prepare_status"
  SUB2API_REDIS_STREAMING_WAIT_ATTEMPTS=4
)

printf 'master\n' >"$prepare_state"
: >"$prepare_actions"
: >"$prepare_sleeps"
rm -f "$prepare_queries"
env "${prepare_common_env[@]}" PREPARE_EMPTY_QUERIES=1 \
  "$PREPARE" --locked --runtime-env-file "$prepare_env" --master-credential-file "$prepare_master_auth" >/dev/null
need "$prepare_config" 'masteruser sub2api_replication'
need "$prepare_config" 'masterauth '
need "$prepare_config" 'replicaof 172.30.241.1 16380'
need "$prepare_actions" 'runtime_topology_apply'
need "$prepare_sleeps" '1'
need "$prepare_status" 'state=healthy '

# A timed-out full sync can resume only the same tunnel upstream; it does not
# issue a second topology mutation. A different upstream is rejected.
printf 'resume\n' >"$prepare_state"
: >"$prepare_actions"
rm -f "$prepare_queries"
env "${prepare_common_env[@]}" \
  "$PREPARE" --locked --runtime-env-file "$prepare_env" --master-credential-file "$prepare_master_auth" >/dev/null
[ ! -s "$prepare_actions" ] || fail "exact replica resume rewrote topology"
need "$prepare_status" 'state=healthy '

printf 'wrong\n' >"$prepare_state"
: >"$prepare_actions"
rm -f "$prepare_queries"
if env "${prepare_common_env[@]}" \
  "$PREPARE" --locked --runtime-env-file "$prepare_env" --master-credential-file "$prepare_master_auth" >/dev/null 2>&1; then
  fail "prepare accepted a replica with a different upstream"
fi
need "$prepare_status" 'resume_upstream=unexpected'
[ ! -s "$prepare_actions" ] || fail "wrong-upstream resume rewrote topology"

chmod 755 "$prepare_config_dir"
printf 'master\n' >"$prepare_state"
rm -f "$prepare_queries"
if env "${prepare_common_env[@]}" \
  "$PREPARE" --locked --runtime-env-file "$prepare_env" --master-credential-file "$prepare_master_auth" >/dev/null 2>&1; then
  fail "prepare accepted a broad configuration directory"
fi
chmod 700 "$prepare_config_dir"

# Stateful fake CLI coverage of incremental readiness, tunnel repair, and the
# candidate-app fail-closed boundary.
mkdir -p "$TEMP_DIR/watchdog-bin"
cat >"$TEMP_DIR/watchdog-bin/id" <<'EOF'
#!/usr/bin/env bash
[ "${1:-}" = -u ] && { echo 0; exit 0; }
exec /usr/bin/id "$@"
EOF
cat >"$TEMP_DIR/watchdog-bin/stat" <<'EOF'
#!/usr/bin/env bash
case "${2:-}" in
  '%a:%U') echo 600:root ;;
  '%a') echo 600 ;;
  '%U') echo root ;;
  *) /usr/bin/stat "$@" ;;
esac
EOF
cat >"$TEMP_DIR/watchdog-bin/flock" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat >"$TEMP_DIR/watchdog-bin/install" <<'EOF'
#!/usr/bin/env bash
for last; do :; done
mkdir -p "$last"
EOF
cat >"$TEMP_DIR/watchdog-bin/systemctl" <<'EOF'
#!/usr/bin/env bash
if [ "${1:-}" = is-active ]; then
  [ "${REDIS_TUNNEL_ACTIVE:-true}" = true ]
  exit
fi
if [ "${1:-}" = restart ]; then
  printf 'restart=%s\n' "${2:-}" >>"$REDIS_WATCHDOG_ACTION_LOG"
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
        [ "${REDIS_APP_RUNNING:-false}" = true ] && echo true || echo false
      else
        [ "${REDIS_TARGET_RUNNING:-true}" = true ] && echo true || echo false
      fi
    fi
    ;;
  start)
    printf 'start=%s\n' "${2:-}" >>"$REDIS_WATCHDOG_ACTION_LOG"
    ;;
  exec)
    if [ -n "${REDIS_INFO_EMPTY_MARKER:-}" ] && [ ! -e "$REDIS_INFO_EMPTY_MARKER" ]; then
      : >"$REDIS_INFO_EMPTY_MARKER"
      exit 0
    fi
    if [ "${REDIS_WRONG_UPSTREAM:-false}" = true ]; then
      master_host=198.51.100.7
    else
      master_host=172.30.241.1
    fi
    cat <<INFO
role:slave
master_host:${master_host}
master_port:16380
master_link_status:up
master_sync_in_progress:0
master_repl_offset:200
slave_repl_offset:199
INFO
    ;;
  *) exit 1 ;;
esac
EOF
cat >"$TEMP_DIR/watchdog-bin/sleep" <<'EOF'
#!/usr/bin/env bash
[ -z "${REDIS_SLEEP_LOG:-}" ] || printf '%s\n' "${1:-}" >>"$REDIS_SLEEP_LOG"
EOF
chmod +x "$TEMP_DIR/watchdog-bin/"*

runtime_auth="$TEMP_DIR/runtime-auth"
runtime_env="$TEMP_DIR/runtime.env"
printf '%048d\n' 0 >"$runtime_auth"
cat >"$runtime_env" <<EOF
SUB2API_REDIS_STANDBY_CONTAINER=sub2api-migration-redis
SUB2API_REDIS_CANDIDATE_APP_CONTAINER=sub2api-migration-app-candidate
SUB2API_REDIS_RUNTIME_AUTH_FILE=${runtime_auth}
SUB2API_REDIS_TUNNEL_BIND=172.30.241.1
SUB2API_REDIS_TUNNEL_PORT=16380
EOF
status_file="$TEMP_DIR/watchdog.status"
action_log="$TEMP_DIR/watchdog-actions.log"
: >"$action_log"

PATH="$TEMP_DIR/watchdog-bin:$PATH" \
  REDIS_WATCHDOG_ACTION_LOG="$action_log" \
  SUB2API_MAINTENANCE_LOCK_FILE="$TEMP_DIR/watchdog.lock" \
  SUB2API_REDIS_RUNTIME_ENV_FILE="$runtime_env" \
  SUB2API_REDIS_STREAMING_STATUS_FILE="$status_file" \
  "$WATCHDOG" >/dev/null
need "$status_file" 'state=healthy '
need "$status_file" 'sync_phase=incremental role=slave link=up sync=0 offset_lag=1'
[ ! -s "$action_log" ] || fail "healthy watchdog performed a repair"

# Redis may still be loading AOF after docker start. An empty INFO response
# consumes the bounded retry budget; a later queryable slave is accepted.
startup_marker="$TEMP_DIR/watchdog-startup-marker"
startup_sleep_log="$TEMP_DIR/watchdog-startup-sleeps.log"
rm -f "$startup_marker" "$startup_sleep_log"
PATH="$TEMP_DIR/watchdog-bin:$PATH" \
  REDIS_INFO_EMPTY_MARKER="$startup_marker" REDIS_SLEEP_LOG="$startup_sleep_log" \
  REDIS_WATCHDOG_ACTION_LOG="$action_log" \
  SUB2API_MAINTENANCE_LOCK_FILE="$TEMP_DIR/watchdog.lock" \
  SUB2API_REDIS_RUNTIME_ENV_FILE="$runtime_env" \
  SUB2API_REDIS_STREAMING_STATUS_FILE="$status_file" \
  SUB2API_REDIS_STREAMING_WAIT_ATTEMPTS=2 \
  "$WATCHDOG" >/dev/null
need "$status_file" 'state=healthy '
[ -f "$startup_marker" ] || fail "watchdog did not retry an initially unqueryable Redis"
need "$startup_sleep_log" '1'

: >"$action_log"
PATH="$TEMP_DIR/watchdog-bin:$PATH" \
  REDIS_TUNNEL_ACTIVE=false REDIS_WATCHDOG_ACTION_LOG="$action_log" \
  SUB2API_MAINTENANCE_LOCK_FILE="$TEMP_DIR/watchdog.lock" \
  SUB2API_REDIS_RUNTIME_ENV_FILE="$runtime_env" \
  SUB2API_REDIS_STREAMING_STATUS_FILE="$status_file" \
  "$WATCHDOG" >/dev/null
need "$action_log" 'restart=sub2api-redis-streaming-tunnel.service'
need "$status_file" 'healed_redis=false healed_tunnel=true'

: >"$action_log"
if PATH="$TEMP_DIR/watchdog-bin:$PATH" \
  REDIS_WRONG_UPSTREAM=true REDIS_WATCHDOG_ACTION_LOG="$action_log" \
  SUB2API_MAINTENANCE_LOCK_FILE="$TEMP_DIR/watchdog.lock" \
  SUB2API_REDIS_RUNTIME_ENV_FILE="$runtime_env" \
  SUB2API_REDIS_STREAMING_STATUS_FILE="$status_file" \
  "$WATCHDOG" >/dev/null 2>&1; then
  fail "watchdog accepted a replica pointed at another upstream"
fi
need "$status_file" 'state=unhealthy '
need "$status_file" 'upstream=unexpected'
[ ! -s "$action_log" ] || fail "wrong-upstream watchdog performed a repair"

: >"$action_log"
if PATH="$TEMP_DIR/watchdog-bin:$PATH" \
  REDIS_APP_RUNNING=true REDIS_TARGET_RUNNING=false REDIS_WATCHDOG_ACTION_LOG="$action_log" \
  SUB2API_MAINTENANCE_LOCK_FILE="$TEMP_DIR/watchdog.lock" \
  SUB2API_REDIS_RUNTIME_ENV_FILE="$runtime_env" \
  SUB2API_REDIS_STREAMING_STATUS_FILE="$status_file" \
  "$WATCHDOG" >/dev/null 2>&1; then
  fail "watchdog accepted a running candidate app"
fi
need "$status_file" 'state=unhealthy '
need "$status_file" 'candidate_app=unexpectedly_running'
[ ! -s "$action_log" ] || fail "watchdog changed target state while app was running"

# A stale healthy status must never survive an early runtime/auth failure.
missing_auth_env="$TEMP_DIR/runtime-missing-auth.env"
cat >"$missing_auth_env" <<EOF
SUB2API_REDIS_STANDBY_CONTAINER=sub2api-migration-redis
SUB2API_REDIS_CANDIDATE_APP_CONTAINER=sub2api-migration-app-candidate
SUB2API_REDIS_RUNTIME_AUTH_FILE=${TEMP_DIR}/missing-runtime-auth
SUB2API_REDIS_TUNNEL_BIND=172.30.241.1
SUB2API_REDIS_TUNNEL_PORT=16380
EOF
printf 'state=healthy checked_at=2000-01-01T00:00:00Z sync_phase=incremental\n' >"$status_file"
if PATH="$TEMP_DIR/watchdog-bin:$PATH" \
  REDIS_WATCHDOG_ACTION_LOG="$action_log" \
  SUB2API_MAINTENANCE_LOCK_FILE="$TEMP_DIR/watchdog.lock" \
  SUB2API_REDIS_RUNTIME_ENV_FILE="$missing_auth_env" \
  SUB2API_REDIS_STREAMING_STATUS_FILE="$status_file" \
  "$WATCHDOG" >/dev/null 2>&1; then
  fail "watchdog accepted a missing runtime credential"
fi
need "$status_file" 'state=unhealthy '
need "$status_file" 'reason=runtime_auth=not_regular_file'
if grep -Fq 'state=healthy' "$status_file"; then fail "watchdog retained stale healthy state"; fi

# A malformed lock path is still post-status validation: it must replace an
# earlier healthy result rather than leave a stale cutover-green status.
printf 'state=healthy checked_at=2000-01-01T00:00:00Z sync_phase=incremental\n' >"$status_file"
if PATH="$TEMP_DIR/watchdog-bin:$PATH" \
  REDIS_WATCHDOG_ACTION_LOG="$action_log" \
  SUB2API_MAINTENANCE_LOCK_FILE=relative-lock-path \
  SUB2API_REDIS_RUNTIME_ENV_FILE="$runtime_env" \
  SUB2API_REDIS_STREAMING_STATUS_FILE="$status_file" \
  "$WATCHDOG" >/dev/null 2>&1; then
  fail "watchdog accepted a relative maintenance lock path"
fi
need "$status_file" 'state=unhealthy '
need "$status_file" 'reason=maintenance_lock=invalid_path'
if grep -Fq 'state=healthy' "$status_file"; then fail "watchdog retained healthy status after lock validation failed"; fi

echo "redis streaming checks passed"
