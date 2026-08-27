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

cleanup() {
  rm -rf "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  grep -Fq -- "$2" "$1" || fail "expected required content was absent"
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

mkdir -p "$FAKE_BIN" "$STATE_ROOT" "$APP_DIR/scripts"
printf 'reverse_proxy sub2api-green:8080\n' >"$APP_DIR/Caddyfile"
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
    # The health probe intentionally fails after start. This reaches the
    # reuse/start boundary without entering Caddy in this focused mock.
    exit 1
    ;;
  logs|cp)
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
  %u) printf '0\n' ;;
  %a)
    if [ "$3" = "$FAKE_RUNTIME_ENV" ]; then printf '600\n'; else printf '%s\n' "$FAKE_CA_MODE"; fi
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

cat >"$FAKE_BIN/nsenter" <<'EOF'
#!/usr/bin/env bash
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
    TEST_ROOT_MOCK="$TEST_ROOT" \
    FAKE_DOCKER_FAIL_CREATE="${FAKE_DOCKER_FAIL_CREATE:-false}" \
    FAKE_CA_MODE="${FAKE_CA_MODE:-644}" \
    FAKE_REALPATH_DRIFT="${FAKE_REALPATH_DRIFT:-false}" \
    APP_DIR="$APP_DIR" \
    OLD_CONTAINER=sub2api \
    NEW_CONTAINER=sub2api-green \
    NEW_IMAGE=sub2api:new \
    NETWORK=sub2api_default \
    DATA_VOLUME=sub2api_sub2api_data \
    CADDY_CONTAINER=sub2api-caddy \
    CADDYFILE="$APP_DIR/Caddyfile" \
    DRAIN_LOG_FILE="$TEST_ROOT/drain.log" \
    DRAIN_NOHUP_FILE="$TEST_ROOT/drain.nohup" \
    DRAIN_PID_FILE="$TEST_ROOT/drain.pid" \
    SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE="${MODE:-external}" \
    SUB2API_EXTERNAL_RUNTIME_ENV_FILE="$RUNTIME_ENV" \
    SUB2API_EXTERNAL_CA_FILE="$CA_FILE" \
    RUN_BACKUP=false \
    PULL_IMAGE=false \
    PRECREATE_ONLY="${PRECREATE_ONLY:-false}" \
    HEALTH_ATTEMPTS=1 \
    HEALTH_INTERVAL_SECONDS=1 \
    /bin/bash "$SCRIPT"
}

: >"$CALLS"
if FAKE_CA_MODE=664 PRECREATE_ONLY=true run_helper >"$OUTPUT" 2>&1; then
  fail 'group-writable external CA was accepted'
fi
assert_contains "$OUTPUT" 'must not be group-writable'
[ ! -s "$CALLS" ] || fail 'Docker was touched before CA permission validation'

: >"$CALLS"
if FAKE_REALPATH_DRIFT=true PRECREATE_ONLY=true run_helper >"$OUTPUT" 2>&1; then
  fail 'external CA realpath drift was accepted'
fi
assert_contains "$OUTPUT" 'must not traverse a symlink'
[ ! -s "$CALLS" ] || fail 'Docker was touched before canonical-path validation'

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
assert_contains "$(state_path sub2api-green)/env" 'UNRELATED_SETTING=preserved'
assert_not_line "$(state_path sub2api-green)/env" 'DATABASE_HOST=postgres'
assert_not_line "$(state_path sub2api-green)/env" 'REDIS_HOST=redis'
assert_contains "$(state_path sub2api-green)/mounts" "bind|$CA_FILE|/etc/sub2api-db-ca/ca.crt|false"
assert_contains "$(state_path sub2api-green)/mounts" "bind|$CA_FILE|/etc/ssl/certs/sub2api-db-ca.pem|false"
assert_not_contains "$OUTPUT" 'external-secret-not-for-logs'
assert_not_contains "$OUTPUT" 'redis-secret-not-for-logs'
assert_not_contains "$CALLS" 'external-secret-not-for-logs'

# External prepared targets must be attached to exactly one network and carry
# exactly the data volume plus the two read-only CA mounts.
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

printf 'Blue-green external runtime mock tests passed.\n'
