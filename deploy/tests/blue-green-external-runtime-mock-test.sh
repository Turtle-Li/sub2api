#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "$0")" && pwd)"
DEPLOY_DIR="$(cd "$TEST_DIR/.." && pwd)"
SCRIPT="$DEPLOY_DIR/sub2api-blue-green-release.sh"
TEST_ROOT="$(mktemp -d /tmp/sub2api-blue-green-external-mock.XXXXXX)"
FAKE_BIN="$TEST_ROOT/bin"
STATE_ROOT="$TEST_ROOT/state"
APP_DIR="$TEST_ROOT/app"
CALLS="$TEST_ROOT/calls.log"
OUTPUT="$TEST_ROOT/output.log"
RUNTIME_ENV="$TEST_ROOT/external.env"
CA_FILE="$TEST_ROOT/ca.crt"
RUNTIME_STATE_DIR="$TEST_ROOT/runtime"
TRAFFIC_STATE_FILE="$RUNTIME_STATE_DIR/traffic-state"
BACKGROUND_STATE_DIR="$RUNTIME_STATE_DIR/background"
BACKGROUND_STATE_FILE="$BACKGROUND_STATE_DIR/sub2api-green"
HEALTH_TOKEN_FILE="$TEST_ROOT/health-token"
CADDY_STARTUP_FILE="$TEST_ROOT/caddy-startup.Caddyfile"
CADDY_ACTIVE_FILE="$TEST_ROOT/caddy-active.json"
CADDY_CANDIDATE_FILE="$TEST_ROOT/caddy-candidate.Caddyfile"
NSENTER_FAIL_MARKER="$TEST_ROOT/nsenter-fail-once.marker"

cleanup() {
  rm -rf "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  if ! grep -Fq -- "$2" "$1"; then
    sed -n '1,160p' "$1" >&2
    fail "expected required content was absent: $2"
  fi
}

assert_not_contains() {
  if grep -Fq -- "$2" "$1"; then
    fail "forbidden content was present"
  fi
}

assert_not_line() {
  if grep -Fxq -- "$2" "$1"; then
    fail "forbidden exact line was present"
  fi
}

state_path() {
  printf '%s/%s\n' "$STATE_ROOT" "$1"
}

make_state() {
  local name="$1"
  local image="$2"
  local running="$3"
  local restart="$4"
  local env_file="$5"
  local mounts_file="$6"
  local path
  path="$(state_path "$name")"
  mkdir -p "$path"
  {
    printf 'image=%s\n' "$image"
    printf 'running=%s\n' "$running"
    printf 'restart=%s\n' "$restart"
    printf 'network=%s\n' sub2api_default
  } >"$path/meta"
  printf '%s\n' sub2api_default >"$path/networks"
  cp "$env_file" "$path/env"
  cp "$mounts_file" "$path/mounts"
}

mkdir -p "$FAKE_BIN" "$STATE_ROOT" "$APP_DIR/scripts" "$BACKGROUND_STATE_DIR"
printf 'reverse_proxy sub2api-green:8080\n' >"$APP_DIR/Caddyfile"
printf 'reverse_proxy sub2api-green:8080\n' >"$CADDY_STARTUP_FILE"
printf 'reverse_proxy sub2api-green:8080\n' >"$CADDY_ACTIVE_FILE"
printf '#!/usr/bin/env bash\nexit 0\n' >"$APP_DIR/scripts/backup.sh"
printf '#!/usr/bin/env bash\nexit 0\n' >"$APP_DIR/scripts/sub2api-drain-monitor.sh"
chmod +x "$APP_DIR/scripts/backup.sh" "$APP_DIR/scripts/sub2api-drain-monitor.sh"

cat >"$FAKE_BIN/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail

printf '%s\n' "$*" >>"$FAKE_DOCKER_CALLS"
state_root="$FAKE_DOCKER_STATE"

path_for() {
  printf '%s/%s\n' "$state_root" "$1"
}

meta() {
  sed -n "s/^$2=//p" "$(path_for "$1")/meta" | tail -n 1
}

set_meta() {
  local file
  file="$(path_for "$1")/meta"
  sed -i.bak "s|^$2=.*|$2=$3|" "$file"
  rm -f "$file.bak"
}

record_mount() {
  local mount="$1"
  local output="$2"
  local type="" source="" target="" rw=true token key value
  while IFS= read -r token; do
    case "$token" in
      readonly) rw=false ;;
      *=*)
        key="${token%%=*}"
        value="${token#*=}"
        case "$key" in
          type) type="$value" ;;
          source) source="$value" ;;
          target) target="$value" ;;
        esac
        ;;
    esac
  done < <(printf '%s\n' "$mount" | tr ',' '\n')
  printf '%s|%s|%s|%s\n' "$type" "$source" "$target" "$rw" >>"$output"
}

create_container() {
  local running="$1"
  shift
  local name="" network="" env_file="" restart=no image="" argument
  local mount_file
  rm -f "$TEST_ROOT_MOCK/mounts"
  while [ "$#" -gt 0 ]; do
    argument="$1"
    case "$argument" in
      -d) ;;
      --name) name="$2"; shift ;;
      --network) network="$2"; shift ;;
      --env-file) env_file="$2"; shift ;;
      --restart) restart="$2"; shift ;;
      --mount)
        mount_file="$TEST_ROOT_MOCK/mounts"
        printf '%s\n' "$2" >>"$mount_file"
        shift
        ;;
      --*) ;;
      *) image="$argument" ;;
    esac
    shift
  done
  [ -n "$name" ] && [ -n "$network" ] && [ -n "$env_file" ] && [ -n "$image" ] || exit 64
  if [ "${FAKE_DOCKER_FAIL_CREATE:-false}" = true ]; then
    exit 73
  fi
  mkdir -p "$(path_for "$name")"
  {
    printf 'image=%s\n' "$image"
    printf 'running=%s\n' "$running"
    printf 'restart=%s\n' "$restart"
    printf 'network=%s\n' "$network"
  } >"$(path_for "$name")/meta"
  printf '%s\n' "$network" >"$(path_for "$name")/networks"
  cp "$env_file" "$(path_for "$name")/env"
  : >"$(path_for "$name")/mounts"
  if [ -f "$TEST_ROOT_MOCK/mounts" ]; then
    while IFS= read -r argument; do
      record_mount "$argument" "$(path_for "$name")/mounts"
    done <"$TEST_ROOT_MOCK/mounts"
    rm -f "$TEST_ROOT_MOCK/mounts"
  fi
}

command_name="$1"
shift
case "$command_name" in
  inspect)
    format=""
    if [ "$#" -ge 3 ] && { [ "$1" = -f ] || [ "$1" = --format ]; }; then
      format="$2"
      name="$3"
    else
      name="$1"
      if [ "$#" -ge 3 ] && { [ "$2" = -f ] || [ "$2" = --format ]; }; then
        format="$3"
      fi
    fi
    [ -d "$(path_for "$name")" ] || exit 1
    [ -n "$format" ] || exit 0
    case "$format" in
      *Config.Env*) cat "$(path_for "$name")/env" ;;
      *Config.Image*) meta "$name" image ;;
      *HostConfig.RestartPolicy.Name*) meta "$name" restart ;;
      *State.Running*) meta "$name" running ;;
      *State.Pid*) printf '4242\n' ;;
      *State.Health*)
        if [ "$(meta "$name" running)" = true ]; then printf 'healthy\n'; else printf 'created\n'; fi
        ;;
      *NetworkSettings.Networks*) cat "$(path_for "$name")/networks" ;;
      *Mounts*) cat "$(path_for "$name")/mounts" ;;
      *) exit 65 ;;
    esac
    ;;
  network|volume)
    [ "$1" = inspect ] || exit 66
    ;;
  pull)
    ;;
  create)
    create_container false "$@"
    ;;
  run)
    create_container true "$@"
    ;;
  update)
    [ "$1" = --restart ] || exit 67
    set_meta "$3" restart "$2"
    ;;
  start)
    set_meta "$1" running true
    ;;
  rm)
    [ "$1" != -f ] || shift
    rm -rf "$(path_for "$1")"
    ;;
  exec)
    [ "${FAKE_DOCKER_CADDY_FLOW:-false}" = true ] || exit 1
    while [ "${1:-}" = -e ]; do
      export "$2"
      shift 2
    done
    name="$1"
    shift
    if [ "$name" != sub2api-caddy ]; then
      exit 0
    fi
    case "${1:-}" in
      caddy)
        case "${2:-}" in
          validate) exit 0 ;;
          reload)
            cp "$FAKE_CADDY_CANDIDATE_FILE" "$FAKE_CADDY_ACTIVE_FILE"
            exit 0
            ;;
          *) exit 69 ;;
        esac
        ;;
      sh)
        if [ -n "${CADDY_CHECK_PATH:-}" ]; then
          grep -qF "$CADDY_CHECK_TEXT" "$FAKE_CADDY_STARTUP_FILE"
        else
          grep -qF "$CADDY_CHECK_TEXT" "$FAKE_CADDY_ACTIVE_FILE"
        fi
        ;;
      *) exit 70 ;;
    esac
    ;;
  cp)
    [ "${FAKE_DOCKER_CADDY_FLOW:-false}" = true ] || exit 0
    cp "$1" "$FAKE_CADDY_CANDIDATE_FILE"
    ;;
  logs)
    ;;
  *)
    exit 68
    ;;
esac
EOF
chmod +x "$FAKE_BIN/docker"

cat >"$FAKE_BIN/stat" <<'EOF'
#!/usr/bin/env bash
[ "$1" = -c ] || exit 1
case "$2" in
  %u:%a)
    printf '%s:600\n' "$(id -u)"
    ;;
  %u)
    if [ "$3" = "$FAKE_HEALTH_TOKEN_FILE" ]; then printf '1000\n'; else printf '0\n'; fi
    ;;
  %g)
    if [ "$3" = "$FAKE_HEALTH_TOKEN_FILE" ]; then printf '1000\n'; else printf '0\n'; fi
    ;;
  %a)
    case "$3" in
      "$FAKE_RUNTIME_ENV"|"$FAKE_HEALTH_TOKEN_FILE") printf '600\n' ;;
      "$FAKE_TRAFFIC_STATE_FILE"|"$FAKE_BACKGROUND_STATE_FILE") printf '644\n' ;;
      *) printf '%s\n' "$FAKE_CA_MODE" ;;
    esac
    ;;
  *) exit 1 ;;
esac
EOF
chmod +x "$FAKE_BIN/stat"

cat >"$FAKE_BIN/realpath" <<'EOF'
#!/usr/bin/env bash
[ "$1" = -e ] && [ "$2" = -- ] || exit 1
if [ "$FAKE_REALPATH_DRIFT" = true ] && [ "$3" = "$FAKE_CA_FILE" ]; then
  printf '%s.drift\n' "$3"
else
  printf '%s\n' "$3"
fi
EOF
chmod +x "$FAKE_BIN/realpath"

cat >"$FAKE_BIN/date" <<'EOF'
#!/usr/bin/env bash
printf '2026-08-28T00:00:00+00:00\n'
EOF
chmod +x "$FAKE_BIN/date"

cat >"$FAKE_BIN/id" <<'EOF'
#!/usr/bin/env bash
[ "${1:-}" = -u ] || exit 64
printf '0\n'
EOF
chmod +x "$FAKE_BIN/id"

cat >"$FAKE_BIN/nsenter" <<'EOF'
#!/usr/bin/env bash
if [ "${FAKE_DOCKER_CADDY_FLOW:-false}" = true ] \
  && [ "${FAKE_NSENTER_FAIL_RO_ALWAYS:-false}" = true ] \
  && printf '%s\n' "$*" | grep -qF 'remount,ro,bind'; then
  exit 72
fi
if [ "${FAKE_DOCKER_CADDY_FLOW:-false}" = true ] \
  && [ "${FAKE_NSENTER_FAIL_RO_ONCE:-false}" = true ] \
  && printf '%s\n' "$*" | grep -qF 'remount,ro,bind' \
  && [ ! -e "$FAKE_NSENTER_FAIL_MARKER" ]; then
  : >"$FAKE_NSENTER_FAIL_MARKER"
  exit 71
fi
exit 0
EOF
chmod +x "$FAKE_BIN/nsenter"

printf '%s\n' \
  'DATABASE_HOST=db.example.test' \
  'DATABASE_PORT=5432' \
  'DATABASE_USER=sub2api' \
  'DATABASE_PASSWORD=external-secret-not-for-logs' \
  'DATABASE_DBNAME=sub2api' \
  'DATABASE_SSLMODE=verify-full' \
  'REDIS_HOST=redis.example.test' \
  'REDIS_PORT=6380' \
  'REDIS_USERNAME=sub2api' \
  'REDIS_PASSWORD=redis-secret-not-for-logs' \
  'REDIS_DB=0' \
  'REDIS_ENABLE_TLS=true' >"$RUNTIME_ENV"
chmod 600 "$RUNTIME_ENV"
printf 'test-ca\n' >"$CA_FILE"
chmod 644 "$CA_FILE"
printf 'accepting\n' >"$TRAFFIC_STATE_FILE"
printf 'standby\n' >"$BACKGROUND_STATE_FILE"
printf 'test-health-token\n' >"$HEALTH_TOKEN_FILE"
chmod 644 "$TRAFFIC_STATE_FILE" "$BACKGROUND_STATE_FILE"
chmod 600 "$HEALTH_TOKEN_FILE"

old_env="$TEST_ROOT/old.env"
old_mounts="$TEST_ROOT/old.mounts"
printf '%s\n' \
  'DATABASE_HOST=postgres' \
  'DATABASE_PORT=5432' \
  'DATABASE_USER=sub2api' \
  'DATABASE_PASSWORD=legacy-secret-not-for-logs' \
  'DATABASE_DBNAME=sub2api' \
  'DATABASE_SSLMODE=disable' \
  'REDIS_HOST=redis' \
  'REDIS_PORT=6379' \
  'REDIS_USERNAME=' \
  'REDIS_PASSWORD=legacy-redis-secret-not-for-logs' \
  'REDIS_DB=0' \
  'REDIS_ENABLE_TLS=false' \
  'SUB2API_TRAFFIC_STATE_FILE=/run/sub2api-runtime/traffic-state' \
  'SUB2API_BACKGROUND_STATE_FILE=/run/sub2api-runtime/background-state' \
  'SUB2API_INTERNAL_HEALTH_TOKEN_FILE=/run/sub2api-runtime/health-token' \
  'UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_BASE64=legacy-private-key-must-be-removed' \
  'UNRELATED_SETTING=preserved' >"$old_env"
printf '%s\n' 'volume|sub2api_sub2api_data|/app/data|true' >"$old_mounts"
make_state sub2api sub2api:old true unless-stopped "$old_env" "$old_mounts"
make_state sub2api-caddy caddy:old true unless-stopped "$old_env" "$old_mounts"

run_helper() {
  env \
    PATH="$FAKE_BIN:$PATH" \
    FAKE_DOCKER_CALLS="$CALLS" \
    FAKE_DOCKER_STATE="$STATE_ROOT" \
    FAKE_RUNTIME_ENV="$RUNTIME_ENV" \
    FAKE_CA_FILE="$CA_FILE" \
    FAKE_TRAFFIC_STATE_FILE="$TRAFFIC_STATE_FILE" \
    FAKE_BACKGROUND_STATE_FILE="$BACKGROUND_STATE_FILE" \
    FAKE_HEALTH_TOKEN_FILE="$HEALTH_TOKEN_FILE" \
    TEST_ROOT_MOCK="$TEST_ROOT" \
    FAKE_DOCKER_FAIL_CREATE="${FAKE_DOCKER_FAIL_CREATE:-false}" \
    FAKE_DOCKER_CADDY_FLOW="${FAKE_DOCKER_CADDY_FLOW:-false}" \
    FAKE_CADDY_STARTUP_FILE="$CADDY_STARTUP_FILE" \
    FAKE_CADDY_ACTIVE_FILE="$CADDY_ACTIVE_FILE" \
    FAKE_CADDY_CANDIDATE_FILE="$CADDY_CANDIDATE_FILE" \
    FAKE_NSENTER_FAIL_RO_ONCE="${FAKE_NSENTER_FAIL_RO_ONCE:-false}" \
    FAKE_NSENTER_FAIL_MARKER="$NSENTER_FAIL_MARKER" \
    FAKE_CA_MODE="${FAKE_CA_MODE:-644}" \
    FAKE_REALPATH_DRIFT="${FAKE_REALPATH_DRIFT:-false}" \
    APP_DIR="$APP_DIR" \
    OLD_CONTAINER=sub2api \
    NEW_CONTAINER=sub2api-green \
    NEW_IMAGE=sub2api:new \
    SUB2API_RUNTIME_GUARD_NETWORK=sub2api_default \
    SUB2API_RUNTIME_GUARD_DATA_VOLUME=sub2api_sub2api_data \
    SUB2API_CADDY_CONTAINER=sub2api-caddy \
    CADDYFILE="$APP_DIR/Caddyfile" \
    CADDY_CONFIG_PATH="$CADDY_STARTUP_FILE" \
    SUB2API_CADDY_STARTUP_HOST_PATH="$CADDY_STARTUP_FILE" \
    DRAIN_LOG_FILE="$TEST_ROOT/drain.log" \
    DRAIN_NOHUP_FILE="$TEST_ROOT/drain.nohup" \
    DRAIN_PID_FILE="$TEST_ROOT/drain.pid" \
    SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE="${MODE:-external}" \
    SUB2API_EXTERNAL_RUNTIME_ENV_FILE="$RUNTIME_ENV" \
    SUB2API_EXTERNAL_CA_FILE="$CA_FILE" \
    SUB2API_TRAFFIC_STATE_FILE_HOST="$TRAFFIC_STATE_FILE" \
    SUB2API_BACKGROUND_STATE_DIR_HOST="$BACKGROUND_STATE_DIR" \
    SUB2API_INTERNAL_HEALTH_TOKEN_FILE="$HEALTH_TOKEN_FILE" \
    SUB2API_DUAL_NODE_RUNTIME_ENABLED="${DUAL_NODE_RUNTIME_ENABLED:-true}" \
    ALLOW_ISOLATED_OLD_CONTAINER="${ALLOW_ISOLATED_OLD_CONTAINER:-false}" \
    REMOVE_EXISTING_NEW_CONTAINER="${REMOVE_EXISTING_NEW_CONTAINER:-true}" \
    RUN_BACKUP=false \
    PULL_IMAGE=false \
    PRECREATE_ONLY="${PRECREATE_ONLY:-false}" \
    VALIDATE_EXTERNAL_RUNTIME_ONLY="${VALIDATE_EXTERNAL_RUNTIME_ONLY:-false}" \
    HEALTH_ATTEMPTS=1 \
    HEALTH_INTERVAL_SECONDS=1 \
    /bin/bash "$SCRIPT"
}

# A retained listener transaction owns the complete Caddyfile until it is
# explicitly committed or rolled back. A release must fail before Docker or
# either application generation is touched.
: >"$CALLS"
printf 'STATUS=staged\n' >"$APP_DIR/.gcp-tw-caddy-transaction.env"
if PRECREATE_ONLY=true run_helper >"$OUTPUT" 2>&1; then
  fail 'blue-green release accepted an unfinished Caddy listener transaction'
fi
assert_contains "$OUTPUT" 'commit or rollback it before a blue-green release'
[ ! -s "$CALLS" ] || fail 'Docker was touched before the Caddy transaction guard'
rm -f "$APP_DIR/.gcp-tw-caddy-transaction.env"

: >"$CALLS"
printf 'BEFORE_SHA=test\n' >"$APP_DIR/.cf-opt-totools-caddy.env"
if PRECREATE_ONLY=true run_helper >"$OUTPUT" 2>&1; then
  fail 'blue-green release accepted an unfinished customer Host transaction'
fi
assert_contains "$OUTPUT" 'commit or rollback it before a blue-green release'
[ ! -s "$CALLS" ] || fail 'Docker was touched before the customer Host transaction guard'
rm -f "$APP_DIR/.cf-opt-totools-caddy.env"

: >"$CALLS"
if FAKE_CA_MODE=664 VALIDATE_EXTERNAL_RUNTIME_ONLY=true run_helper >"$OUTPUT" 2>&1; then
  fail 'group-writable external CA was accepted'
fi
assert_contains "$OUTPUT" 'must not be group-writable'
[ ! -s "$CALLS" ] || fail 'Docker was touched before CA permission validation'

: >"$CALLS"
if FAKE_REALPATH_DRIFT=true VALIDATE_EXTERNAL_RUNTIME_ONLY=true run_helper >"$OUTPUT" 2>&1; then
  fail 'external CA realpath drift was accepted'
fi
assert_contains "$OUTPUT" 'must not traverse a symlink'
[ ! -s "$CALLS" ] || fail 'Docker was touched before canonical-path validation'

: >"$CALLS"
sed -i.bak 's/^DATABASE_SSLMODE=.*/DATABASE_SSLMODE=disable/' "$RUNTIME_ENV"
rm -f "$RUNTIME_ENV.bak"
invalid_runtime_env_accepted=false
if VALIDATE_EXTERNAL_RUNTIME_ONLY=true run_helper >"$OUTPUT" 2>&1; then
  invalid_runtime_env_accepted=true
fi
sed -i.bak 's/^DATABASE_SSLMODE=.*/DATABASE_SSLMODE=verify-full/' "$RUNTIME_ENV"
rm -f "$RUNTIME_ENV.bak"
[ "$invalid_runtime_env_accepted" = false ] \
  || fail 'runtime-only validation accepted an invalid external runtime env'
assert_contains "$OUTPUT" 'DATABASE_SSLMODE must be verify-full in external dependency mode'
[ ! -s "$CALLS" ] || fail 'Docker was touched before external runtime env validation'

: >"$CALLS"
if ! VALIDATE_EXTERNAL_RUNTIME_ONLY=true run_helper >"$OUTPUT" 2>&1; then
  sed -n '1,160p' "$OUTPUT" >&2
  fail 'valid external runtime-only validation was rejected'
fi
assert_contains "$OUTPUT" 'external runtime contract validated; exiting before Docker or Caddy lifecycle actions'
[ ! -s "$CALLS" ] || fail 'runtime-only validation invoked Docker'

: >"$CALLS"
if MODE=local VALIDATE_EXTERNAL_RUNTIME_ONLY=true run_helper >"$OUTPUT" 2>&1; then
  fail 'runtime-only validation accepted local dependency mode'
fi
assert_contains "$OUTPUT" 'VALIDATE_EXTERNAL_RUNTIME_ONLY requires external dependency mode'
[ ! -s "$CALLS" ] || fail 'invalid runtime-only validation invoked Docker'

: >"$CALLS"
before_caddy="$(cksum "$APP_DIR/Caddyfile")"
PRECREATE_ONLY=true run_helper >"$OUTPUT" 2>&1
after_caddy="$(cksum "$APP_DIR/Caddyfile")"
[ "$before_caddy" = "$after_caddy" ] || fail 'precreate changed Caddy'
assert_contains "$CALLS" 'create --name sub2api-green'
assert_contains "$CALLS" '--restart no'
assert_not_contains "$CALLS" 'start sub2api-green'
assert_not_contains "$CALLS" 'update --restart'
assert_not_contains "$CALLS" 'exec '
assert_contains "$(state_path sub2api-green)/env" 'DATABASE_HOST=db.example.test'
assert_contains "$(state_path sub2api-green)/env" 'DATABASE_SSLMODE=verify-full'
assert_contains "$(state_path sub2api-green)/env" 'REDIS_ENABLE_TLS=true'
assert_contains "$(state_path sub2api-green)/env" 'PGSSLROOTCERT=/etc/sub2api-db-ca/ca.crt'
assert_contains "$(state_path sub2api-green)/env" 'SUB2API_TRAFFIC_STATE_FILE=/run/sub2api-runtime/traffic-state'
assert_contains "$(state_path sub2api-green)/env" 'SUB2API_BACKGROUND_STATE_FILE=/run/sub2api-runtime/background-state'
assert_contains "$(state_path sub2api-green)/env" 'SUB2API_INTERNAL_HEALTH_TOKEN_FILE=/run/sub2api-runtime/health-token'
assert_contains "$(state_path sub2api-green)/env" 'UNRELATED_SETTING=preserved'
assert_not_line "$(state_path sub2api-green)/env" 'DATABASE_HOST=postgres'
assert_not_line "$(state_path sub2api-green)/env" 'REDIS_HOST=redis'
assert_contains "$(state_path sub2api-green)/mounts" "bind|$CA_FILE|/etc/sub2api-db-ca/ca.crt|false"
assert_contains "$(state_path sub2api-green)/mounts" "bind|$CA_FILE|/etc/ssl/certs/sub2api-db-ca.pem|false"
assert_contains "$(state_path sub2api-green)/mounts" "bind|$TRAFFIC_STATE_FILE|/run/sub2api-runtime/traffic-state|false"
assert_contains "$(state_path sub2api-green)/mounts" "bind|$BACKGROUND_STATE_FILE|/run/sub2api-runtime/background-state|false"
assert_contains "$(state_path sub2api-green)/mounts" "bind|$HEALTH_TOKEN_FILE|/run/sub2api-runtime/health-token|false"
assert_not_contains "$OUTPUT" 'external-secret-not-for-logs'

assert_not_contains "$OUTPUT" 'redis-secret-not-for-logs'
assert_not_contains "$CALLS" 'external-secret-not-for-logs'

# External prepared targets must be attached to exactly one network and carry
# exactly the data volume, two read-only CA mounts, and three runtime mounts.
printf '%s\n' sub2api_default unexpected-network >"$(state_path sub2api-green)/networks"
: >"$CALLS"
if PRECREATE_ONLY=true run_helper >"$OUTPUT" 2>&1; then
  fail 'external target with an extra network was accepted'
fi
assert_contains "$OUTPUT" 'does not match'
assert_not_contains "$CALLS" 'rm '
printf '%s\n' sub2api_default >"$(state_path sub2api-green)/networks"

expected_mounts="$TEST_ROOT/expected-mounts"
cp "$(state_path sub2api-green)/mounts" "$expected_mounts"
printf '%s\n' 'bind|/tmp/unexpected|/etc/unexpected|false' >>"$(state_path sub2api-green)/mounts"
: >"$CALLS"
if PRECREATE_ONLY=true run_helper >"$OUTPUT" 2>&1; then
  fail 'external target with an extra mount was accepted'
fi
assert_contains "$OUTPUT" 'does not match'
assert_not_contains "$CALLS" 'rm '
cp "$expected_mounts" "$(state_path sub2api-green)/mounts"

# The regular path must validate then reuse the precreated object. The mock
# intentionally fails the later app-health probe, after update/start but before
# Caddy, to isolate this boundary.
: >"$CALLS"
if run_helper >"$OUTPUT" 2>&1; then
  fail 'focused mock unexpectedly completed beyond health probe'
fi
assert_contains "$CALLS" 'update --restart unless-stopped sub2api-green'
assert_contains "$CALLS" 'start sub2api-green'
assert_not_contains "$CALLS" 'create --name sub2api-green'
assert_not_contains "$CALLS" 'rm '
assert_not_contains "$OUTPUT" 'external-secret-not-for-logs'

# Runtime recovery is the only caller allowed to switch away from an old slot
# that has already been isolated. The default release contract still rejects
# it, while the explicit narrow mode reaches target validation/health.
sed -i.bak 's/^running=.*/running=false/' "$(state_path sub2api)/meta"
rm -f "$(state_path sub2api)/meta.bak"
: >"$CALLS"
if run_helper >"$OUTPUT" 2>&1; then
  fail 'normal release accepted a stopped old container'
fi
assert_contains "$OUTPUT" 'old container sub2api is not running; refusing to release'
assert_not_contains "$CALLS" 'exec sub2api-green'
: >"$CALLS"
if ALLOW_ISOLATED_OLD_CONTAINER=true REMOVE_EXISTING_NEW_CONTAINER=false run_helper >"$OUTPUT" 2>&1; then
  fail 'focused isolated-old recovery unexpectedly completed beyond health probe'
fi
assert_not_contains "$OUTPUT" 'old container sub2api is not running; refusing to release'
assert_contains "$CALLS" 'exec sub2api-green'
rm -rf "$(state_path sub2api)"
: >"$CALLS"
if ALLOW_ISOLATED_OLD_CONTAINER=true REMOVE_EXISTING_NEW_CONTAINER=false run_helper >"$OUTPUT" 2>&1; then
  fail 'focused absent-old recovery unexpectedly completed beyond health probe'
fi
assert_not_contains "$OUTPUT" 'old container sub2api does not exist'
assert_contains "$CALLS" 'exec sub2api-green'
make_state sub2api sub2api:old true unless-stopped "$old_env" "$old_mounts"

# Drift on a stopped prepared target must fail closed with no direct reuse,
# deletion, or start.
sed -i.bak 's/^DATABASE_HOST=.*/DATABASE_HOST=drift.example.test/' "$(state_path sub2api-green)/env"
rm -f "$(state_path sub2api-green)/env.bak"
sed -i.bak 's/^running=.*/running=false/' "$(state_path sub2api-green)/meta"
sed -i.bak 's/^restart=.*/restart=no/' "$(state_path sub2api-green)/meta"
rm -f "$(state_path sub2api-green)/meta.bak"
: >"$CALLS"
if PRECREATE_ONLY=true run_helper >"$OUTPUT" 2>&1; then
  fail 'drifted prepared external target was accepted'
fi
assert_contains "$OUTPUT" 'does not match'
assert_not_contains "$CALLS" 'rm '
assert_not_contains "$CALLS" 'start sub2api-green'
assert_not_contains "$OUTPUT" 'external-secret-not-for-logs'

# A failed create happens after the temporary root-only env file has been
# assembled. Its path must no longer exist after the helper's EXIT trap.
rm -rf "$(state_path sub2api-green)"
: >"$CALLS"
if FAKE_DOCKER_FAIL_CREATE=true PRECREATE_ONLY=true run_helper >"$OUTPUT" 2>&1; then
  fail 'forced Docker create failure was accepted'
fi
temporary_env="$(awk '{for (i = 1; i <= NF; i++) if ($i == "--env-file") {print $(i + 1); exit}}' "$CALLS")"
[ -n "$temporary_env" ] || fail 'mock did not observe a runtime env file'
[ ! -e "$temporary_env" ] || fail 'temporary runtime env file remained after failure'
assert_not_contains "$OUTPUT" 'external-secret-not-for-logs'
assert_not_contains "$OUTPUT" 'redis-secret-not-for-logs'

# A direct external release without a precreated target still creates it
# stopped first, verifies restart=no, and only then updates and starts it.
: >"$CALLS"
if PRECREATE_ONLY=false run_helper >"$OUTPUT" 2>&1; then
  fail 'focused direct external mock unexpectedly completed beyond health probe'
fi
assert_contains "$CALLS" 'create --name sub2api-green'
assert_contains "$CALLS" '--restart no'
assert_contains "$CALLS" 'update --restart unless-stopped sub2api-green'
assert_contains "$CALLS" 'start sub2api-green'
if grep -Eq '^create .*--restart unless-stopped' "$CALLS"; then
  fail 'direct external target was created with an active restart policy'
fi

# Installing the repository helper alone must preserve the legacy container
# contract until the node config explicitly enables dual-node runtime gating.
rm -rf "$(state_path sub2api-green)"
: >"$CALLS"
DUAL_NODE_RUNTIME_ENABLED=false PRECREATE_ONLY=true run_helper >"$OUTPUT" 2>&1
if ! grep -Fq -- 'create --name sub2api-green' "$CALLS"; then
  sed -n '1,120p' "$OUTPUT" >&2
  sed -n '1,120p' "$CALLS" >&2
  fail 'legacy precreate did not create the target'
fi
assert_not_contains "$(state_path sub2api-green)/mounts" '/run/sub2api-runtime/'
assert_not_contains "$(state_path sub2api-green)/env" 'SUB2API_TRAFFIC_STATE_FILE='
assert_not_contains "$(state_path sub2api-green)/env" 'SUB2API_INTERNAL_HEALTH_TOKEN_FILE='

# Local mode retains the original run-from-old-env behavior and does not mount
# the external CA. The later app probe is intentionally the only failure.
rm -rf "$(state_path sub2api-green)"
: >"$CALLS"
if MODE=local PRECREATE_ONLY=false run_helper >"$OUTPUT" 2>&1; then
  fail 'focused local mock unexpectedly completed beyond health probe'
fi
assert_contains "$CALLS" 'run -d --name sub2api-green'
assert_contains "$(state_path sub2api-green)/env" 'DATABASE_HOST=postgres'
assert_contains "$(state_path sub2api-green)/env" 'REDIS_HOST=redis'
assert_not_contains "$(state_path sub2api-green)/mounts" '/etc/sub2api-db-ca/ca.crt'

# A running historical local color is reusable in dual mode only when its
# image, network, runtime env, and all three read-only mounts still match.
sed -i.bak '/\/run\/sub2api-runtime\/health-token/d' "$(state_path sub2api-green)/mounts"
rm -f "$(state_path sub2api-green)/mounts.bak"
: >"$CALLS"
if MODE=local PRECREATE_ONLY=false run_helper >"$OUTPUT" 2>&1; then
  fail 'running local target with a missing runtime mount was reused'
fi
assert_contains "$OUTPUT" 'does not match the requested image or dual-node runtime contract'
assert_not_contains "$CALLS" 'exec '

# Unified payment adds only non-secret overrides and a read-only Unix-socket
# volume. A historical private-key environment field is always stripped.
rm -rf "$(state_path sub2api-green)"
: >"$CALLS"
(
  export SUB2API_UNIFIED_PAYMENT_VAULT_VOLUME=sub2api_unified_payment_vault
  export UNIFIED_PAYMENT_ENABLED=true
  export UNIFIED_PAYMENT_BASE_URL=https://pay.totools.cn
  export UNIFIED_PAYMENT_ENVIRONMENT=sandbox
  export UNIFIED_PAYMENT_ORGANIZATION_ID=84fc3e66-e959-4bc8-8d78-6f8c3d3483fb
  export UNIFIED_PAYMENT_PRODUCT_ID=00da03c5-bc5c-4edb-9d4c-c77da0e969d5
  export UNIFIED_PAYMENT_APP_ID=app.sub2.sandbox
  export UNIFIED_PAYMENT_REQUEST_KEY_ID=sub2.request.sandbox.v1
  export UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF='vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64'
  export UNIFIED_PAYMENT_VAULT_AGENT_SOCKET=/run/sub2api-payment-vault/public.sock
  export UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON='{"sub2.webhook.sandbox.v1":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}'
  export UNIFIED_PAYMENT_RETURN_URL=https://www.turtleligpt.com/payment/result
  PRECREATE_ONLY=true run_helper >"$OUTPUT" 2>&1
)
assert_contains "$(state_path sub2api-green)/env" 'UNIFIED_PAYMENT_ENABLED=true'
assert_contains "$(state_path sub2api-green)/env" 'UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON={"sub2.webhook.sandbox.v1":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}'
assert_not_contains "$(state_path sub2api-green)/env" 'UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_BASE64='
assert_contains "$(state_path sub2api-green)/mounts" 'volume|sub2api_unified_payment_vault|/run/sub2api-payment-vault|false'

: >"$CALLS"
if UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_BASE64=forbidden PRECREATE_ONLY=true run_helper >"$OUTPUT" 2>&1; then
  fail 'legacy unified payment private-key environment was accepted'
fi
assert_contains "$OUTPUT" 'UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_BASE64 is forbidden'
assert_not_contains "$OUTPUT" 'legacy-private-key-must-be-removed'

: >"$CALLS"
if (
  export SUB2API_UNIFIED_PAYMENT_VAULT_VOLUME=sub2api_unified_payment_vault
  export UNIFIED_PAYMENT_ENABLED=true
  export UNIFIED_PAYMENT_BASE_URL=https://pay.totools.cn
  export UNIFIED_PAYMENT_ENVIRONMENT=sandbox
  export UNIFIED_PAYMENT_ORGANIZATION_ID=aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa
  export UNIFIED_PAYMENT_PRODUCT_ID=00da03c5-bc5c-4edb-9d4c-c77da0e969d5
  export UNIFIED_PAYMENT_APP_ID=app.sub2.sandbox
  export UNIFIED_PAYMENT_REQUEST_KEY_ID=sub2.request.sandbox.v1
  export UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF='vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64'
  export UNIFIED_PAYMENT_VAULT_AGENT_SOCKET=/run/sub2api-payment-vault/public.sock
  export UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON='{"sub2.webhook.sandbox.v1":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}'
  export UNIFIED_PAYMENT_RETURN_URL=https://www.turtleligpt.com/payment/result
  PRECREATE_ONLY=true run_helper >"$OUTPUT" 2>&1
); then
  fail 'foreign unified payment organization scope was accepted'
fi
assert_contains "$OUTPUT" 'UNIFIED_PAYMENT_ORGANIZATION_ID does not match the approved Sub2 sandbox scope'

: >"$CALLS"
if (
  export SUB2API_UNIFIED_PAYMENT_VAULT_VOLUME=sub2api_unified_payment_vault
  export UNIFIED_PAYMENT_ENABLED=true
  export UNIFIED_PAYMENT_BASE_URL=https://pay.totools.cn
  export UNIFIED_PAYMENT_ENVIRONMENT=sandbox
  export UNIFIED_PAYMENT_ORGANIZATION_ID=84fc3e66-e959-4bc8-8d78-6f8c3d3483fb
  export UNIFIED_PAYMENT_PRODUCT_ID=00da03c5-bc5c-4edb-9d4c-c77da0e969d5
  export UNIFIED_PAYMENT_APP_ID=app.sub2.sandbox
  export UNIFIED_PAYMENT_REQUEST_KEY_ID=sub2.request.sandbox.v1
  export UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF='vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64'
  export UNIFIED_PAYMENT_VAULT_AGENT_SOCKET=/run/sub2api-payment-vault/public.sock
  export UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON='{"sub2.webhook.sandbox.v1":"not-base64"}'
  export UNIFIED_PAYMENT_RETURN_URL=https://www.turtleligpt.com/payment/result
  PRECREATE_ONLY=true run_helper >"$OUTPUT" 2>&1
); then
  fail 'malformed unified payment webhook public key was accepted'
fi
assert_contains "$OUTPUT" 'UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON is invalid'

# The Caddy switch publishes recovery authority before its first in-place
# write. A remount failure after the startup file write must restore host,
# startup, and active views and remove only the completed transaction.
rm -rf "$(state_path sub2api-green)"
printf 'reverse_proxy sub2api:8080\n' >"$APP_DIR/Caddyfile"
printf 'reverse_proxy sub2api:8080\n' >"$CADDY_STARTUP_FILE"
printf 'reverse_proxy sub2api:8080\n' >"$CADDY_ACTIVE_FILE"
rm -f "$NSENTER_FAIL_MARKER" "$APP_DIR/.sub2api-blue-green-caddy-transaction.env"
: >"$CALLS"
if FAKE_DOCKER_CADDY_FLOW=true FAKE_NSENTER_FAIL_RO_ONCE=true \
  run_helper >"$OUTPUT" 2>&1; then
  fail 'forced Caddy startup remount failure was accepted'
fi
assert_contains "$OUTPUT" 'restored interrupted Caddy upstream switch before exit'
assert_contains "$APP_DIR/Caddyfile" 'reverse_proxy sub2api:8080'
assert_contains "$CADDY_STARTUP_FILE" 'reverse_proxy sub2api:8080'
assert_contains "$CADDY_ACTIVE_FILE" 'reverse_proxy sub2api:8080'
[ ! -e "$APP_DIR/.sub2api-blue-green-caddy-transaction.env" ] \
  || fail 'completed automatic Caddy restoration retained its transaction'

# If every read-only remount fails, automatic recovery must retain authority
# and the EXIT trap must report that its final safety remount also failed. A
# later clean invocation recovers all three views and stops before a new switch.
printf 'reverse_proxy sub2api:8080\n' >"$APP_DIR/Caddyfile"
printf 'reverse_proxy sub2api:8080\n' >"$CADDY_STARTUP_FILE"
printf 'reverse_proxy sub2api:8080\n' >"$CADDY_ACTIVE_FILE"
: >"$CALLS"
if FAKE_DOCKER_CADDY_FLOW=true FAKE_NSENTER_FAIL_RO_ALWAYS=true \
  run_helper >"$OUTPUT" 2>&1; then
  fail 'persistent Caddy startup remount failure was accepted'
fi
assert_contains "$OUTPUT" 'Caddy startup bind may still be read-write after recovery'
[ -e "$APP_DIR/.sub2api-blue-green-caddy-transaction.env" ] \
  || fail 'failed final safety remount discarded Caddy recovery authority'
if FAKE_DOCKER_CADDY_FLOW=true run_helper >"$OUTPUT" 2>&1; then
  fail 'clean retry after a persistent remount failure continued as a new release'
fi
assert_contains "$OUTPUT" 'recovered the interrupted Caddy upstream switch; rerun the release'
[ ! -e "$APP_DIR/.sub2api-blue-green-caddy-transaction.env" ] \
  || fail 'clean retry did not clear the retained remount-failure transaction'

# A process killed after publication cannot run EXIT cleanup. Simulate its
# durable state and require the next release invocation to recover and stop,
# rather than continuing a new release on an ambiguous Caddy view.
caddy_backup="$APP_DIR/Caddyfile.bak-blue-green-20260902-000000.recovery"
caddy_candidate="$APP_DIR/Caddyfile.after-blue-green-20260902-000000.recovery"
printf 'reverse_proxy sub2api:8080\n' >"$caddy_backup"
printf 'reverse_proxy sub2api-green:8080\n' >"$caddy_candidate"
printf 'reverse_proxy sub2api-green:8080\n' >"$APP_DIR/Caddyfile"
printf 'reverse_proxy sub2api-green:8080\n' >"$CADDY_STARTUP_FILE"
printf 'reverse_proxy sub2api-green:8080\n' >"$CADDY_ACTIVE_FILE"
before_sha="$(sha256sum "$caddy_backup" | awk '{print $1}')"
after_sha="$(sha256sum "$caddy_candidate" | awk '{print $1}')"
{
  printf 'CADDYFILE=%s\n' "$APP_DIR/Caddyfile"
  printf 'BACKUP_PATH=%s\n' "$caddy_backup"
  printf 'CANDIDATE_PATH=%s\n' "$caddy_candidate"
  printf 'BEFORE_SHA=%s\n' "$before_sha"
  printf 'AFTER_SHA=%s\n' "$after_sha"
  printf 'UPSTREAM_FROM=sub2api:8080\n'
  printf 'UPSTREAM_TO=sub2api-green:8080\n'
} >"$APP_DIR/.sub2api-blue-green-caddy-transaction.env"
chmod 600 "$APP_DIR/.sub2api-blue-green-caddy-transaction.env"
: >"$CALLS"
if FAKE_DOCKER_CADDY_FLOW=true run_helper >"$OUTPUT" 2>&1; then
  fail 'retained Caddy switch recovery continued as a new release'
fi
assert_contains "$OUTPUT" 'recovered the interrupted Caddy upstream switch; rerun the release'
assert_contains "$APP_DIR/Caddyfile" 'reverse_proxy sub2api:8080'
assert_contains "$CADDY_STARTUP_FILE" 'reverse_proxy sub2api:8080'
assert_contains "$CADDY_ACTIVE_FILE" 'reverse_proxy sub2api:8080'
[ ! -e "$APP_DIR/.sub2api-blue-green-caddy-transaction.env" ] \
  || fail 'successful retained-transaction recovery did not clear its state'

# The clean retry may now complete and must commit the transaction only after
# all three Caddy views converge on the target generation.
: >"$CALLS"
FAKE_DOCKER_CADDY_FLOW=true run_helper >"$OUTPUT" 2>&1
assert_contains "$OUTPUT" 'Caddy upstream switch committed; rollback backup retained'
assert_contains "$APP_DIR/Caddyfile" 'reverse_proxy sub2api-green:8080'
assert_contains "$CADDY_STARTUP_FILE" 'reverse_proxy sub2api-green:8080'
assert_contains "$CADDY_ACTIVE_FILE" 'reverse_proxy sub2api-green:8080'
[ ! -e "$APP_DIR/.sub2api-blue-green-caddy-transaction.env" ] \
  || fail 'successful Caddy switch retained its recovery transaction'

printf 'Blue-green external runtime mock tests passed.\n'
