#!/usr/bin/env bash

# All runtime-guard behavior is exercised against a stateful fake Docker CLI.
# The test never connects to Docker, Caddy, or a public endpoint.

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
SCRIPT="${DEPLOY_DIR}/sub2api-runtime-guard.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-runtime-guard-test.XXXXXX")"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
FAKE_BIN="${TEST_ROOT}/bin"
CASE_ROOT=""
REAL_STAT="$(command -v stat)"

cleanup() {
  rm -rf "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local file="$1"
  local expected="$2"
  if ! grep -Fq -- "$expected" "$file"; then
    [ -f "$file" ] && sed -n '1,180p' "$file" >&2
    fail "expected '${expected}' in ${file}"
  fi
}

assert_not_contains() {
  local file="$1"
  local unexpected="$2"
  if [ -f "$file" ] && grep -Fq -- "$unexpected" "$file"; then
    sed -n '1,180p' "$file" >&2
    fail "did not expect '${unexpected}' in ${file}"
  fi
}

assert_before() {
  local file="$1"
  local first="$2"
  local second="$3"
  local first_line
  local second_line

  first_line="$(grep -nF -- "$first" "$file" | head -n 1 | cut -d: -f1 || true)"
  second_line="$(grep -nF -- "$second" "$file" | head -n 1 | cut -d: -f1 || true)"
  [ -n "$first_line" ] && [ -n "$second_line" ] && [ "$first_line" -lt "$second_line" ] \
    || fail "expected '${first}' before '${second}' in ${file}"
}

SERVICE_UNIT="${DEPLOY_DIR}/sub2api-runtime-guard.service"
assert_contains "$SERVICE_UNIT" 'ConditionFileIsExecutable=/usr/local/libexec/sub2api-runtime-guard.sh'
assert_not_contains "$SERVICE_UNIT" 'ConditionPathIsExecutable='

mkdir -p "$FAKE_BIN"

cat >"${FAKE_BIN}/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail

printf '%s\n' "$*" >>"$FAKE_DOCKER_CALLS"

state_file() {
  printf '%s/%s.env\n' "$FAKE_CONTAINER_STATE_DIR" "$1"
}

load_state() {
  local file
  file="$(state_file "$1")"
  [ -r "$file" ] || return 1
  # Test-generated values only.
  # shellcheck disable=SC1090
  . "$file"
  restart_policy="${restart_policy:-unless-stopped}"
  networks="${networks:-}"
  mounts="${mounts:-}"
  environment="${environment:-}"
}

runtime_drift_enabled() {
  local direct_flag="$1" after_release_flag="$2"
  [ "$direct_flag" = true ] \
    || { [ "$after_release_flag" = true ] && [ -s "$FAKE_RELEASE_CALLS" ]; }
}

save_state() {
  local file
  file="$(state_file "$1")"
  printf 'running=%q\nhealth=%q\noom=%q\nexit_code=%q\nimage=%q\nstart_health=%q\nrestart_health=%q\nrestart_policy=%q\nnetworks=%q\nmounts=%q\nenvironment=%q\n' \
    "$running" "$health" "$oom" "$exit_code" "$image" "$start_health" "$restart_health" \
    "$restart_policy" "$networks" "$mounts" "$environment" >"$file"
}

case "${1:-}" in
  inspect)
    container_name="${2:-}"
    load_state "$container_name" || exit 1
    format=""
    if [ "${3:-}" = "--format" ]; then
      format="${4:-}"
    fi
    case "$format" in
      *State.Running*) printf '%s\n' "$running" ;;
      *State.Health*) printf '%s\n' "$health" ;;
      *State.OOMKilled*) printf '%s\n' "$oom" ;;
      *State.ExitCode*) printf '%s\n' "$exit_code" ;;
      *Config.Image*) printf '%s\n' "$image" ;;
      *HostConfig.RestartPolicy*) printf '%s\n' "$restart_policy" ;;
      *NetworkSettings.Networks*) printf '%s\n' "$networks" ;;
      *Mounts*) printf '%s\n' "$mounts" ;;
      *Config.Env*) printf '%s\n' "$environment" ;;
      *) : ;;
    esac
    ;;
  start)
    container_name="${2:-}"
    load_state "$container_name" || exit 1
    running=true
    health="$start_health"
    save_state "$container_name"
    ;;
  restart)
    container_name="${2:-}"
    load_state "$container_name" || exit 1
    running=true
    health="$restart_health"
    save_state "$container_name"
    ;;
  stop)
    container_name="${2:-}"
    load_state "$container_name" || exit 1
    running=false
    health=exited
    save_state "$container_name"
    ;;
  exec)
    shift
    container_name=""
    runtime_state_path=""
    while [ "$#" -gt 0 ]; do
      case "$1" in
        -e)
          case "${2:-}" in
            SUB2API_RUNTIME_STATE_PATH=*) runtime_state_path="${2#*=}" ;;
          esac
          shift 2
          ;;
        *)
          container_name="$1"
          shift
          break
          ;;
      esac
    done
    command_text="$*"
    if [ "$container_name" = "sub2api-caddy" ]; then
      case "$command_text" in
        *2019/config*)
          if [ "${FAKE_CADDY_ADMIN_MODE:-ok}" = fail-until-restart ] \
            && ! grep -Fq -- 'restart sub2api-caddy' "$FAKE_DOCKER_CALLS"; then
            exit 1
          fi
          cat "$FAKE_ACTIVE_CONFIG_FILE"
          ;;
        *cat*) cat "$FAKE_STARTUP_CONFIG_FILE" ;;
        *) exit 0 ;;
      esac
      exit 0
    fi
    load_state "$container_name" || exit 1
    [ "$running" = true ] && [ "$health" = healthy ] || exit 1
    if [ -n "$runtime_state_path" ]; then
      case "$runtime_state_path" in
        /run/sub2api-runtime/traffic-state)
          runtime_host_path="$FAKE_RUNTIME_ROOT/traffic-state"
          runtime_kind=traffic
          ;;
        /run/sub2api-runtime/background-state)
          runtime_host_path="$FAKE_RUNTIME_ROOT/background/$container_name"
          runtime_kind=background
          ;;
        *) exit 1 ;;
      esac
      runtime_identity="1:$(printf '%s' "$runtime_host_path" | cksum | awk '{print $1}')"
      case "$command_text" in
        *'stat -c'*)
          if runtime_drift_enabled \
            "${FAKE_RUNTIME_INODE_DRIFT:-false}" \
            "${FAKE_RUNTIME_INODE_DRIFT_AFTER_RELEASE:-false}" \
            && { [ -z "${FAKE_RUNTIME_DRIFT_CONTAINER:-}" ] || [ "$FAKE_RUNTIME_DRIFT_CONTAINER" = "$container_name" ]; } \
            && [ "${FAKE_RUNTIME_DRIFT_TARGET:-traffic}" = "$runtime_kind" ]; then
            printf '1:999\n'
          else
            printf '%s\n' "$runtime_identity"
          fi
          ;;
        *'tr -d'*)
          if runtime_drift_enabled \
            "${FAKE_RUNTIME_CONTENT_DRIFT:-false}" \
            "${FAKE_RUNTIME_CONTENT_DRIFT_AFTER_RELEASE:-false}" \
            && { [ -z "${FAKE_RUNTIME_DRIFT_CONTAINER:-}" ] || [ "$FAKE_RUNTIME_DRIFT_CONTAINER" = "$container_name" ]; } \
            && [ "${FAKE_RUNTIME_DRIFT_TARGET:-traffic}" = "$runtime_kind" ]; then
            printf 'stale-container-view\n'
          else
            tr -d '\r\n' <"$runtime_host_path"
          fi
          ;;
        *) exit 1 ;;
      esac
    fi
    ;;
  *)
    exit 1
    ;;
esac
EOF
chmod +x "${FAKE_BIN}/docker"

cat >"${FAKE_BIN}/curl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_CURL_CALLS"
exit "${FAKE_CURL_RESULT:-0}"
EOF
chmod +x "${FAKE_BIN}/curl"

cat >"${FAKE_BIN}/flock" <<'EOF'
#!/usr/bin/env bash
[ "${FAKE_FLOCK_MODE:-success}" != locked ]
EOF
chmod +x "${FAKE_BIN}/flock"

cat >"${FAKE_BIN}/sleep" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "${FAKE_BIN}/sleep"

cat >"${FAKE_BIN}/realpath" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
path="${!#}"
[ -e "$path" ] || exit 1
printf '%s\n' "$path"
EOF
chmod +x "${FAKE_BIN}/realpath"

cat >"${FAKE_BIN}/stat" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
[ "${1:-}" = -c ] || exec "$REAL_STAT" "$@"
format="${2:-}"
path="${3:-}"
case "$path" in
  */external-runtime.env)
    uid=0; gid=0; mode=600
    ;;
  */ca.crt)
    uid=0; gid=0; mode=644
    ;;
  */traffic-state|*/background/sub2api|*/background/sub2api-blue|*/background/sub2api-green)
    uid=0; gid=0; mode=644
    ;;
  */internal-health-token)
    uid=1000; gid=1000; mode=600
    ;;
  *) exec "$REAL_STAT" "$@" ;;
esac
case "$format" in
  %u) printf '%s\n' "$uid" ;;
  %g) printf '%s\n' "$gid" ;;
  %a) printf '%s\n' "$mode" ;;
  %d:%i)
    case "$path" in
      */traffic-state|*/background/*)
        printf '1:%s\n' "$(printf '%s' "$path" | cksum | awk '{print $1}')"
        ;;
      *) exit 1 ;;
    esac
    ;;
  *) exit 1 ;;
esac
EOF
chmod +x "${FAKE_BIN}/stat"

write_container() {
  local name="$1"
  local running="$2"
  local health="$3"
  local oom="$4"
  local exit_code="$5"
  local image="$6"
  local start_health="${7:-healthy}"
  local restart_health="${8:-healthy}"

  mkdir -p "${CASE_ROOT}/containers"
  printf 'running=%s\nhealth=%s\noom=%s\nexit_code=%s\nimage=%s\nstart_health=%s\nrestart_health=%s\n' \
    "$running" "$health" "$oom" "$exit_code" "$image" "$start_health" "$restart_health" \
    >"${CASE_ROOT}/containers/${name}.env"
}

write_runtime_metadata() {
  local name="$1"
  local restart_policy="$2"
  local networks="$3"
  local mounts="$4"
  local environment="$5"
  local file="${CASE_ROOT}/containers/${name}.env"
  [ -f "$file" ] || fail "missing container fixture: ${name}"
  printf 'restart_policy=%q\nnetworks=%q\nmounts=%q\nenvironment=%q\n' \
    "$restart_policy" "$networks" "$mounts" "$environment" >>"$file"
}

write_external_runtime_files() {
  mkdir -p "${CASE_ROOT}/external" "${CASE_ROOT}/runtime/background" "${CASE_ROOT}/app/secrets"
  cat >"${CASE_ROOT}/external/external-runtime.env" <<'EOF'
DATABASE_HOST=postgres.invalid
DATABASE_PORT=5432
DATABASE_USER=sub2api
DATABASE_PASSWORD=test-only-password
DATABASE_DBNAME=sub2api
DATABASE_SSLMODE=verify-full
REDIS_HOST=redis.invalid
REDIS_PORT=6380
REDIS_USERNAME=sub2api
REDIS_PASSWORD=test-only-password
REDIS_DB=0
REDIS_ENABLE_TLS=true
EOF
  printf 'test-ca\n' >"${CASE_ROOT}/external/ca.crt"
  printf 'accepting\n' >"${CASE_ROOT}/runtime/traffic-state"
  printf 'standby generation-test\n' >"${CASE_ROOT}/runtime/background/sub2api-green"
  printf 'standby generation-test\n' >"${CASE_ROOT}/runtime/background/sub2api-blue"
  printf 'standby generation-test\n' >"${CASE_ROOT}/runtime/background/sub2api"
  printf 'test-only-health-token\n' >"${CASE_ROOT}/app/secrets/internal-health-token"
}

external_environment() {
  cat <<'EOF'
DATABASE_HOST=postgres.invalid
DATABASE_PORT=5432
DATABASE_USER=sub2api
DATABASE_PASSWORD=test-only-password
DATABASE_DBNAME=sub2api
DATABASE_SSLMODE=verify-full
REDIS_HOST=redis.invalid
REDIS_PORT=6380
REDIS_USERNAME=sub2api
REDIS_PASSWORD=test-only-password
REDIS_DB=0
REDIS_ENABLE_TLS=true
PGSSLROOTCERT=/etc/sub2api-db-ca/ca.crt
EOF
}

dual_environment() {
  external_environment
  cat <<'EOF'
SUB2API_TRAFFIC_STATE_FILE=/run/sub2api-runtime/traffic-state
SUB2API_BACKGROUND_STATE_FILE=/run/sub2api-runtime/background-state
SUB2API_INTERNAL_HEALTH_TOKEN_FILE=/run/sub2api-runtime/health-token
EOF
}

external_mounts() {
  local container_name="$1"
  cat <<EOF
volume|candidate-data|/app/data|true
bind|${CASE_ROOT}/external/ca.crt|/etc/sub2api-db-ca/ca.crt|false
bind|${CASE_ROOT}/external/ca.crt|/etc/ssl/certs/sub2api-db-ca.pem|false
bind|${CASE_ROOT}/runtime/traffic-state|/run/sub2api-runtime/traffic-state|false
bind|${CASE_ROOT}/runtime/background/${container_name}|/run/sub2api-runtime/background-state|false
bind|${CASE_ROOT}/app/secrets/internal-health-token|/run/sub2api-runtime/health-token|false
EOF
}

new_case() {
  local name="$1"

  CASE_ROOT="${TEST_ROOT}/${name}"
  mkdir -p "${CASE_ROOT}/app/scripts" "${CASE_ROOT}/containers"
  chmod 700 "$CASE_ROOT"
  : >"${CASE_ROOT}/docker-calls.log"
  : >"${CASE_ROOT}/release-calls.log"
  : >"${CASE_ROOT}/curl-calls.log"
  : >"${CASE_ROOT}/node-state-calls.log"
  printf 'reverse_proxy sub2api-green:8080\n' >"${CASE_ROOT}/app/Caddyfile"
  printf '{"upstream":"sub2api-green:8080"}\n' >"${CASE_ROOT}/active-config.json"
  printf 'reverse_proxy sub2api-green:8080\n' >"${CASE_ROOT}/startup-Caddyfile"

  cat >"${CASE_ROOT}/app/scripts/sub2api-blue-green-release.sh" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'old=%s\nnew=%s\nimage=%s\nfrom=%s\nto=%s\nbackup=%s\npull=%s\nremove=%s\nisolated_old=%s\n' \
  "$OLD_CONTAINER" "$NEW_CONTAINER" "$NEW_IMAGE" "$CADDY_UPSTREAM_FROM" "$CADDY_UPSTREAM_TO" \
  "$RUN_BACKUP" "$PULL_IMAGE" "$REMOVE_EXISTING_NEW_CONTAINER" \
  "${ALLOW_ISOLATED_OLD_CONTAINER:-false}" >>"$FAKE_RELEASE_CALLS"
printf 'reverse_proxy %s\n' "$CADDY_UPSTREAM_TO" >"$CADDYFILE"
printf '{"upstream":"%s"}\n' "$CADDY_UPSTREAM_TO" >"$FAKE_ACTIVE_CONFIG_FILE"
printf 'reverse_proxy %s\n' "$CADDY_UPSTREAM_TO" >"$FAKE_STARTUP_CONFIG_FILE"
if [ "${FAKE_RELEASE_RESULT:-0}" -ne 0 ] && [ "${FAKE_RELEASE_ROLLBACK:-false}" = true ]; then
  printf 'reverse_proxy %s\n' "$CADDY_UPSTREAM_FROM" >"$CADDYFILE"
  printf '{"upstream":"%s"}\n' "$CADDY_UPSTREAM_FROM" >"$FAKE_ACTIVE_CONFIG_FILE"
  printf 'reverse_proxy %s\n' "$CADDY_UPSTREAM_FROM" >"$FAKE_STARTUP_CONFIG_FILE"
fi
exit "${FAKE_RELEASE_RESULT:-0}"
EOF
  chmod +x "${CASE_ROOT}/app/scripts/sub2api-blue-green-release.sh"
  cat >"${CASE_ROOT}/app/scripts/sub2api-node-state.sh" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >>"$FAKE_NODE_STATE_CALLS"
[ "${FAKE_NODE_STATE_RESULT:-0}" -eq 0 ] || exit "${FAKE_NODE_STATE_RESULT}"
printf 'NO_LOCAL_RECOVERY\n'
EOF
  chmod +x "${CASE_ROOT}/app/scripts/sub2api-node-state.sh"
}

write_standard_dependencies() {
  write_container sub2api-postgres true healthy false 0 postgres:18
  write_container sub2api-redis true healthy false 0 redis:8
  write_container sub2api-caddy true healthy false 0 caddy:2
}

run_guard() {
  env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_ACTIVE_CONFIG_FILE="${CASE_ROOT}/active-config.json" \
    FAKE_CADDY_ADMIN_MODE="${FAKE_CADDY_ADMIN_MODE:-ok}" \
    FAKE_CONTAINER_STATE_DIR="${CASE_ROOT}/containers" \
    FAKE_CURL_CALLS="${CASE_ROOT}/curl-calls.log" \
    FAKE_DOCKER_CALLS="${CASE_ROOT}/docker-calls.log" \
    FAKE_FLOCK_MODE="${FAKE_FLOCK_MODE:-success}" \
    FAKE_RUNTIME_CONTENT_DRIFT="${FAKE_RUNTIME_CONTENT_DRIFT:-false}" \
    FAKE_RUNTIME_CONTENT_DRIFT_AFTER_RELEASE="${FAKE_RUNTIME_CONTENT_DRIFT_AFTER_RELEASE:-false}" \
    FAKE_RUNTIME_DRIFT_CONTAINER="${FAKE_RUNTIME_DRIFT_CONTAINER:-}" \
    FAKE_RUNTIME_DRIFT_TARGET="${FAKE_RUNTIME_DRIFT_TARGET:-traffic}" \
    FAKE_RUNTIME_INODE_DRIFT="${FAKE_RUNTIME_INODE_DRIFT:-false}" \
    FAKE_RUNTIME_INODE_DRIFT_AFTER_RELEASE="${FAKE_RUNTIME_INODE_DRIFT_AFTER_RELEASE:-false}" \
    FAKE_RUNTIME_ROOT="${CASE_ROOT}/runtime" \
    FAKE_NODE_STATE_CALLS="${CASE_ROOT}/node-state-calls.log" \
    FAKE_RELEASE_CALLS="${CASE_ROOT}/release-calls.log" \
    FAKE_STARTUP_CONFIG_FILE="${CASE_ROOT}/startup-Caddyfile" \
    REAL_STAT="$REAL_STAT" \
    SUB2API_APP_DIR="${CASE_ROOT}/app" \
    SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1 \
    SUB2API_MAINTENANCE_LOCK_FILE="${CASE_ROOT}/maintenance.lock" \
    SUB2API_PUBLIC_HEALTH_URL='https://example.invalid/health' \
    SUB2API_PUBLIC_HEALTH_RESOLVE="${SUB2API_PUBLIC_HEALTH_RESOLVE:-example.invalid:443:192.0.2.10}" \
    SUB2API_RUNTIME_GUARD_CONFIG_FILE="${CASE_ROOT}/missing.env" \
    SUB2API_RUNTIME_GUARD_CADDYFILE="${CASE_ROOT}/app/Caddyfile" \
    SUB2API_RUNTIME_GUARD_COOLDOWN_SECONDS="${SUB2API_RUNTIME_GUARD_COOLDOWN_SECONDS:-0}" \
    SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_ATTEMPTS=1 \
    SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_INTERVAL_SECONDS=0 \
    SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_MAX_TIME_SECONDS=1 \
    SUB2API_RUNTIME_GUARD_RETRY_ATTEMPTS=1 \
    SUB2API_RUNTIME_GUARD_RETRY_INTERVAL_SECONDS=0 \
    SUB2API_RUNTIME_GUARD_STATE_DIR="${CASE_ROOT}/runtime-state" \
    /bin/bash "$SCRIPT"
}

run_external_guard() {
  SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE=external \
  SUB2API_EXTERNAL_RUNTIME_ENV_FILE="${CASE_ROOT}/external/external-runtime.env" \
  SUB2API_EXTERNAL_CA_FILE="${CASE_ROOT}/external/ca.crt" \
  SUB2API_RUNTIME_GUARD_NETWORK=candidate-network \
  SUB2API_RUNTIME_GUARD_DATA_VOLUME=candidate-data \
  SUB2API_DUAL_NODE_RUNTIME_ENABLED=true \
  SUB2API_TRAFFIC_STATE_FILE_HOST="${CASE_ROOT}/runtime/traffic-state" \
  SUB2API_BACKGROUND_STATE_DIR_HOST="${CASE_ROOT}/runtime/background" \
  SUB2API_INTERNAL_HEALTH_TOKEN_FILE="${CASE_ROOT}/app/secrets/internal-health-token" \
    run_guard
}

# Lock contention must be a successful no-op: no Docker inspection or state
# rewrite is allowed while another maintenance operation owns the global lock.
new_case lock-contention
write_standard_dependencies
write_container sub2api-green true healthy false 0 sub2api:current
mkdir -p "${CASE_ROOT}/runtime-state"
printf 'sentinel=true\n' >"${CASE_ROOT}/runtime-state/last-failure.env"
FAKE_FLOCK_MODE=locked run_guard >"${CASE_ROOT}/output.log" 2>&1
assert_contains "${CASE_ROOT}/output.log" 'maintenance lock is held; exiting without runtime changes'
[ ! -s "${CASE_ROOT}/docker-calls.log" ] || fail 'Docker was called while maintenance lock was held'
assert_contains "${CASE_ROOT}/runtime-state/last-failure.env" 'sentinel=true'

# Runtime recovery shares the maintenance fence but must also respect a
# listener transaction which remains authoritative after its staging command
# releases that fence.
new_case caddy-listener-transaction
write_standard_dependencies
write_container sub2api-green true healthy false 0 sub2api:current
printf 'STATUS=staged\n' >"${CASE_ROOT}/app/.gcp-tw-caddy-transaction.env"
if run_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted an unfinished Caddy listener transaction'
fi
assert_contains "${CASE_ROOT}/output.log" 'runtime recovery is fenced until commit or rollback'
[ ! -s "${CASE_ROOT}/docker-calls.log" ] || fail 'Docker was called before the Caddy transaction guard'

new_case customer-host-transaction
write_standard_dependencies
write_container sub2api-green true healthy false 0 sub2api:current
printf 'BEFORE_SHA=test\n' >"${CASE_ROOT}/app/.cf-opt-totools-caddy.env"
if run_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted an unfinished customer Host transaction'
fi
assert_contains "${CASE_ROOT}/output.log" 'runtime recovery is fenced until commit or rollback'
[ ! -s "${CASE_ROOT}/docker-calls.log" ] || fail 'Docker was called before the customer Host transaction guard'

new_case blue-green-caddy-transaction
write_standard_dependencies
write_container sub2api-green true healthy false 0 sub2api:current
printf 'BEFORE_SHA=test\n' >"${CASE_ROOT}/app/.sub2api-blue-green-caddy-transaction.env"
if run_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted an unfinished blue-green Caddy transaction'
fi
assert_contains "${CASE_ROOT}/output.log" 'runtime recovery is fenced until the release helper recovers it'
[ ! -s "${CASE_ROOT}/docker-calls.log" ] || fail 'Docker was called before the blue-green Caddy transaction guard'

# A resolve override for a different hostname must fail before it can turn the
# peer origin into false node-local recovery evidence.
new_case health-resolve-host-mismatch
write_standard_dependencies
write_container sub2api-green true healthy false 0 sub2api:current
if SUB2API_PUBLIC_HEALTH_RESOLVE='peer.invalid:443:192.0.2.10' run_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted a health resolve override for a peer hostname'
fi
assert_contains "${CASE_ROOT}/output.log" 'host/port must match SUB2API_PUBLIC_HEALTH_URL'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'inspect '

# A healthy active slot only verifies Caddy consistency and exits unchanged.
new_case active-healthy
write_standard_dependencies
write_container sub2api-green true healthy false 0 sub2api:current
run_guard >"${CASE_ROOT}/output.log" 2>&1
assert_contains "${CASE_ROOT}/output.log" 'active container is already healthy: sub2api-green'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'restart sub2api-green'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'
[ ! -s "${CASE_ROOT}/release-calls.log" ] || fail 'blue-green helper ran for a healthy active slot'
assert_contains "${CASE_ROOT}/node-state-calls.log" 'recover-local'

# External shared dependencies are never treated as local Docker containers.
# A healthy active slot is accepted only when its exact external credentials,
# CA, network, data volume, and dual-node runtime mounts/env match.
new_case external-active-healthy
write_standard_dependencies
write_external_runtime_files
write_container sub2api-green true healthy false 0 sub2api:current
write_runtime_metadata sub2api-green unless-stopped candidate-network \
  "$(external_mounts sub2api-green)" "$(dual_environment)"
run_external_guard >"${CASE_ROOT}/output.log" 2>&1
assert_contains "${CASE_ROOT}/output.log" 'runtime dependency mode=external; skipping local PostgreSQL and Redis container inspection and lifecycle actions'
assert_contains "${CASE_ROOT}/output.log" 'active container is already healthy: sub2api-green'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'inspect sub2api-postgres'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'inspect sub2api-redis'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-postgres'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-redis'

# A correct Mount.Source path is insufficient: a single-file bind mount can
# remain pinned to an inode that node-state already replaced.
new_case external-active-runtime-inode-drift
write_standard_dependencies
write_external_runtime_files
write_container sub2api-green true healthy false 0 sub2api:current
write_runtime_metadata sub2api-green unless-stopped candidate-network \
  "$(external_mounts sub2api-green)" "$(dual_environment)"
if FAKE_RUNTIME_INODE_DRIFT=true run_external_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted a stale runtime-state bind inode'
fi
assert_contains "${CASE_ROOT}/output.log" 'traffic-state bind mount inode is stale'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'restart sub2api-green'
assert_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'
assert_contains "${CASE_ROOT}/runtime-state/last-failure.env" 'reason=active-runtime-state-drift-no-known-good-fallback'

new_case external-active-background-inode-drift
write_standard_dependencies
write_external_runtime_files
write_container sub2api-green true healthy false 0 sub2api:current
write_runtime_metadata sub2api-green unless-stopped candidate-network \
  "$(external_mounts sub2api-green)" "$(dual_environment)"
if FAKE_RUNTIME_INODE_DRIFT=true FAKE_RUNTIME_DRIFT_TARGET=background \
  run_external_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted a stale background-state bind inode'
fi
assert_contains "${CASE_ROOT}/output.log" 'background-state bind mount inode is stale'
assert_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'

new_case external-active-runtime-content-drift
write_standard_dependencies
write_external_runtime_files
write_container sub2api-green true healthy false 0 sub2api:current
write_runtime_metadata sub2api-green unless-stopped candidate-network \
  "$(external_mounts sub2api-green)" "$(dual_environment)"
if FAKE_RUNTIME_CONTENT_DRIFT=true run_external_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted stale runtime-state content'
fi
assert_contains "${CASE_ROOT}/output.log" 'traffic-state bind mount content is stale'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'restart sub2api-green'
assert_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'

new_case external-active-background-content-drift
write_standard_dependencies
write_external_runtime_files
write_container sub2api-green true healthy false 0 sub2api:current
write_runtime_metadata sub2api-green unless-stopped candidate-network \
  "$(external_mounts sub2api-green)" "$(dual_environment)"
if FAKE_RUNTIME_CONTENT_DRIFT=true FAKE_RUNTIME_DRIFT_TARGET=background \
  run_external_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted stale background-state content'
fi
assert_contains "${CASE_ROOT}/output.log" 'background-state bind mount content is stale'
assert_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'

# A healthy slot with stale bind state is removed from admission, then a clean
# historical slot may recover traffic. The original drift evidence survives
# both that promotion and a later healthy guard run on the new Caddy target.
new_case external-active-runtime-drift-recovers-clean-fallback
write_standard_dependencies
write_external_runtime_files
write_container sub2api-green true healthy false 0 sub2api:current
write_runtime_metadata sub2api-green unless-stopped candidate-network "$(external_mounts sub2api-green)" "$(dual_environment)"
write_container sub2api-blue false exited false 0 sub2api:old-blue healthy healthy
write_runtime_metadata sub2api-blue unless-stopped candidate-network "$(external_mounts sub2api-blue)" "$(dual_environment)"
FAKE_RUNTIME_INODE_DRIFT=true FAKE_RUNTIME_DRIFT_CONTAINER=sub2api-green run_external_guard >"${CASE_ROOT}/output.log" 2>&1
assert_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'
assert_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-blue'
assert_contains "${CASE_ROOT}/release-calls.log" 'new=sub2api-blue'
assert_contains "${CASE_ROOT}/runtime-state/last-failure.env" 'active_container=sub2api-green'
assert_contains "${CASE_ROOT}/runtime-state/last-failure.env" 'reason=active-runtime-state-drift'
assert_contains "${CASE_ROOT}/output.log" 'preserving active runtime-state drift evidence'
: >"${CASE_ROOT}/docker-calls.log"
run_external_guard >"${CASE_ROOT}/second-run.log" 2>&1
assert_contains "${CASE_ROOT}/second-run.log" 'active container is already healthy: sub2api-blue'
assert_contains "${CASE_ROOT}/second-run.log" 'preserving failure evidence for previously isolated container: sub2api-green'
assert_contains "${CASE_ROOT}/runtime-state/last-failure.env" 'reason=active-runtime-state-drift'

# A missing runtime mount fails before any application lifecycle action.
new_case external-active-runtime-mismatch
write_standard_dependencies
write_external_runtime_files
write_container sub2api-green false exited false 0 sub2api:current
incomplete_mounts="$(external_mounts sub2api-green | grep -v '/run/sub2api-runtime/health-token')"
write_runtime_metadata sub2api-green unless-stopped candidate-network \
  "$incomplete_mounts" "$(dual_environment)"
if run_external_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted an external active container with a missing runtime mount'
fi
assert_contains "${CASE_ROOT}/output.log" 'active application runtime does not match the configured dependency and dual-node contract'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-green'

# An already-running historical slot is also checked before the failed active
# slot is stopped. This closes the drain/fallback path that never calls start.
new_case external-running-fallback-runtime-mismatch
write_standard_dependencies
write_external_runtime_files
write_container sub2api-green true unhealthy false 1 sub2api:broken healthy unhealthy
write_runtime_metadata sub2api-green unless-stopped candidate-network \
  "$(external_mounts sub2api-green)" "$(dual_environment)"
write_container sub2api-blue true healthy false 0 sub2api:old-blue
incomplete_fallback_mounts="$(external_mounts sub2api-blue | grep -v '/run/sub2api-runtime/background-state')"
write_runtime_metadata sub2api-blue unless-stopped candidate-network \
  "$incomplete_fallback_mounts" "$(dual_environment)"
if run_external_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted a running fallback with a missing runtime mount'
fi
assert_contains "${CASE_ROOT}/output.log" 'running inactive fallback does not match the configured dependency and dual-node contract: sub2api-blue'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'
assert_not_contains "${CASE_ROOT}/release-calls.log" 'new=sub2api-blue'

# A running fallback can have structurally correct mount Sources while still
# reading replaced single-file inodes. Fence it before the failed active is
# stopped or Caddy is changed.
new_case external-running-fallback-runtime-state-drift
write_standard_dependencies
write_external_runtime_files
write_container sub2api-green true unhealthy false 1 sub2api:broken healthy unhealthy
write_runtime_metadata sub2api-green unless-stopped candidate-network "$(external_mounts sub2api-green)" "$(dual_environment)"
write_container sub2api-blue true healthy false 0 sub2api:old-blue
write_runtime_metadata sub2api-blue unless-stopped candidate-network "$(external_mounts sub2api-blue)" "$(dual_environment)"
if FAKE_RUNTIME_INODE_DRIFT=true run_external_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted a running fallback with a stale runtime-state inode'
fi
assert_contains "${CASE_ROOT}/output.log" 'running inactive fallback has stale runtime-state binds: sub2api-blue'
assert_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-blue'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'
assert_not_contains "${CASE_ROOT}/release-calls.log" 'new=sub2api-blue'
assert_contains "${CASE_ROOT}/app/Caddyfile" 'sub2api-green:8080'
assert_contains "${CASE_ROOT}/runtime-state/last-failure.env" 'reason=running-fallback-runtime-state-drift'

# A stopped fallback with missing per-container runtime state must fail softly
# after isolation so the outer recovery transaction records a cooldown fence.
new_case external-stopped-fallback-missing-state
write_standard_dependencies
write_external_runtime_files
rm -f "${CASE_ROOT}/runtime/background/sub2api-blue"
write_container sub2api-green true unhealthy false 1 sub2api:broken healthy unhealthy
write_runtime_metadata sub2api-green unless-stopped candidate-network \
  "$(external_mounts sub2api-green)" "$(dual_environment)"
write_container sub2api-blue false exited false 0 sub2api:old-blue healthy healthy
write_runtime_metadata sub2api-blue unless-stopped candidate-network \
  "$(external_mounts sub2api-blue)" "$(dual_environment)"
if SUB2API_RUNTIME_GUARD_COOLDOWN_SECONDS=300 run_external_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted a fallback whose background state was absent'
fi
assert_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-blue'
assert_contains "${CASE_ROOT}/output.log" 'historical fallback did not become healthy'
assert_contains "${CASE_ROOT}/runtime-state/last-failure.env" 'reason=fallback-did-not-become-healthy'
: >"${CASE_ROOT}/docker-calls.log"
if SUB2API_RUNTIME_GUARD_COOLDOWN_SECONDS=300 run_external_guard >"${CASE_ROOT}/cooldown.log" 2>&1; then
  fail 'runtime guard ignored cooldown after fallback runtime validation failed'
fi
assert_contains "${CASE_ROOT}/cooldown.log" 'runtime recovery is cooling down'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'restart sub2api-green'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-blue'

# A Caddy-selected active container can be absent after an interrupted cleanup.
# External/dual recovery must still validate and promote a conforming fallback.
new_case external-active-absent-fallback
write_standard_dependencies
write_external_runtime_files
write_container sub2api-blue false exited false 0 sub2api:old-blue healthy healthy
write_runtime_metadata sub2api-blue unless-stopped candidate-network \
  "$(external_mounts sub2api-blue)" "$(dual_environment)"
run_external_guard >"${CASE_ROOT}/output.log" 2>&1
assert_contains "${CASE_ROOT}/output.log" 'active container is absent: sub2api-green'
assert_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-blue'
assert_contains "${CASE_ROOT}/release-calls.log" 'new=sub2api-blue'
assert_contains "${CASE_ROOT}/release-calls.log" 'isolated_old=true'
assert_contains "${CASE_ROOT}/app/Caddyfile" 'sub2api-blue:8080'

# A fallback that becomes stale only after the Caddy helper runs must not be
# stopped while Caddy still points at it.  The next timer run can re-check the
# target (and an operator can inspect the retained failure evidence) without
# turning a transient probe failure into a guaranteed outage.
new_case external-post-switch-fallback-runtime-state-drift
write_standard_dependencies
write_external_runtime_files
write_container sub2api-green true unhealthy false 1 sub2api:broken healthy unhealthy
write_runtime_metadata sub2api-green unless-stopped candidate-network "$(external_mounts sub2api-green)" "$(dual_environment)"
write_container sub2api-blue false exited false 0 sub2api:old-blue healthy healthy
write_runtime_metadata sub2api-blue unless-stopped candidate-network "$(external_mounts sub2api-blue)" "$(dual_environment)"
if FAKE_RUNTIME_INODE_DRIFT_AFTER_RELEASE=true run_external_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted a fallback whose runtime-state inode drifted after Caddy switch'
fi
assert_contains "${CASE_ROOT}/release-calls.log" 'new=sub2api-blue'
assert_contains "${CASE_ROOT}/output.log" 'fallback runtime-state bind is stale after Caddy switch: sub2api-blue'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-blue'
assert_contains "${CASE_ROOT}/runtime-state/last-failure.env" 'active_container=sub2api-blue'
assert_contains "${CASE_ROOT}/runtime-state/last-failure.env" 'reason=fallback-verification-failed'
assert_contains "${CASE_ROOT}/app/Caddyfile" 'sub2api-blue:8080'

# A transient public-health failure after a successful Caddy switch follows
# the same rule: retain the only target Caddy can currently serve and record a
# retryable failure instead of stopping it unconditionally.
new_case external-post-switch-public-health-failure
write_standard_dependencies
write_external_runtime_files
write_container sub2api-green true unhealthy false 1 sub2api:broken healthy unhealthy
write_runtime_metadata sub2api-green unless-stopped candidate-network "$(external_mounts sub2api-green)" "$(dual_environment)"
write_container sub2api-blue false exited false 0 sub2api:old-blue healthy healthy
write_runtime_metadata sub2api-blue unless-stopped candidate-network "$(external_mounts sub2api-blue)" "$(dual_environment)"
if FAKE_CURL_RESULT=1 run_external_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted a failed post-switch public health probe'
fi
assert_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-blue'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-blue'
assert_contains "${CASE_ROOT}/app/Caddyfile" 'sub2api-blue:8080'
assert_contains "${CASE_ROOT}/runtime-state/last-failure.env" 'reason=fallback-verification-failed'

# A node-state reconciliation failure after the switch also retains the
# serving fallback; isolation is only allowed after Caddy conclusively points
# back to the failed active slot.
new_case external-post-switch-reconcile-failure
write_standard_dependencies
write_external_runtime_files
write_container sub2api-green true unhealthy false 1 sub2api:broken healthy unhealthy
write_runtime_metadata sub2api-green unless-stopped candidate-network "$(external_mounts sub2api-green)" "$(dual_environment)"
write_container sub2api-blue false exited false 0 sub2api:old-blue healthy healthy
write_runtime_metadata sub2api-blue unless-stopped candidate-network "$(external_mounts sub2api-blue)" "$(dual_environment)"
if FAKE_NODE_STATE_RESULT=1 run_external_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted a failed post-switch reconciliation'
fi
assert_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-blue'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-blue'
assert_contains "${CASE_ROOT}/app/Caddyfile" 'sub2api-blue:8080'
assert_contains "${CASE_ROOT}/runtime-state/last-failure.env" 'reason=fallback-reconciliation-failed'

# A normal-final active generation must never fall back to a Phase-A legacy
# writer.  The mode is checked before the active slot is isolated.
new_case external-reject-phase-a-fallback
write_standard_dependencies
write_external_runtime_files
write_container sub2api-green true unhealthy false 1 sub2api:normal-final healthy unhealthy
write_runtime_metadata sub2api-green unless-stopped candidate-network "$(external_mounts sub2api-green)" "$(dual_environment)"
write_container sub2api-blue false exited false 0 sub2api:phase-a healthy healthy
write_runtime_metadata sub2api-blue unless-stopped candidate-network "$(external_mounts sub2api-blue)" $'SUB2API_FIXED_EGRESS_COMPATIBILITY_MODE=true\n'"$(dual_environment)"
if run_external_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard promoted a Phase-A fallback into normal-final traffic'
fi
assert_contains "${CASE_ROOT}/output.log" 'incompatible fixed-egress mode'
assert_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-blue'
assert_contains "${CASE_ROOT}/runtime-state/last-failure.env" 'reason=no-known-good-fallback'

# Invalid or duplicate compatibility entries on the Caddy-selected generation
# fail closed before any lifecycle action.
new_case external-invalid-active-egress-mode
write_standard_dependencies
write_external_runtime_files
write_container sub2api-green true unhealthy false 1 sub2api:invalid healthy unhealthy
write_runtime_metadata sub2api-green unless-stopped candidate-network \
  "$(external_mounts sub2api-green)" $'SUB2API_FIXED_EGRESS_COMPATIBILITY_MODE=garbage\n'"$(dual_environment)"
write_container sub2api-blue false exited false 0 sub2api:old-blue healthy healthy
write_runtime_metadata sub2api-blue unless-stopped candidate-network \
  "$(external_mounts sub2api-blue)" "$(dual_environment)"
if run_external_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted an invalid active fixed-egress mode'
fi
assert_contains "${CASE_ROOT}/output.log" 'invalid fixed-egress compatibility mode'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-blue'

# A stopped active slot is started and proven through Docker health plus its
# internal health endpoint before any historical candidate is considered.
new_case same-slot-recovery
write_standard_dependencies
write_container sub2api-green false exited false 0 sub2api:current healthy healthy
run_guard >"${CASE_ROOT}/output.log" 2>&1
assert_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-green'
assert_contains "${CASE_ROOT}/output.log" 'active container recovered in place: sub2api-green'
[ ! -s "${CASE_ROOT}/release-calls.log" ] || fail 'blue-green helper ran after same-slot recovery'

# If a prior release still has one healthy old color draining, a failed active
# slot is isolated and traffic is returned to that already-running history.
new_case running-historic-fallback
write_standard_dependencies
write_container sub2api-green true unhealthy false 1 sub2api:broken healthy unhealthy
write_container sub2api-blue true healthy false 0 sub2api:old-blue healthy healthy
run_guard >"${CASE_ROOT}/output.log" 2>&1
assert_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-blue'
assert_contains "${CASE_ROOT}/docker-calls.log" 'restart sub2api-green'
assert_before "${CASE_ROOT}/docker-calls.log" 'restart sub2api-green' 'stop sub2api-green'
assert_contains "${CASE_ROOT}/release-calls.log" 'new=sub2api-blue'
assert_contains "${CASE_ROOT}/output.log" 'promoting already-running healthy historical fallback: sub2api-blue'

# If the active image cannot recover, it is stopped before the last known good
# historic slot starts.  The helper receives the exact image/upstream and its
# post-switch host/Admin/startup/public checks must all complete.
new_case historic-fallback
write_standard_dependencies
write_container sub2api-green true unhealthy false 1 sub2api:broken healthy unhealthy
write_container sub2api-blue false exited false 0 sub2api:old-blue healthy healthy
run_guard >"${CASE_ROOT}/output.log" 2>&1
assert_contains "${CASE_ROOT}/docker-calls.log" 'restart sub2api-green'
assert_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'
assert_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-blue'
assert_before "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green' 'start sub2api-blue'
assert_contains "${CASE_ROOT}/release-calls.log" 'old=sub2api-green'
assert_contains "${CASE_ROOT}/release-calls.log" 'new=sub2api-blue'
assert_contains "${CASE_ROOT}/release-calls.log" 'image=sub2api:old-blue'
assert_contains "${CASE_ROOT}/release-calls.log" 'from=sub2api-green:8080'
assert_contains "${CASE_ROOT}/release-calls.log" 'to=sub2api-blue:8080'
assert_contains "${CASE_ROOT}/release-calls.log" 'backup=false'
assert_contains "${CASE_ROOT}/release-calls.log" 'pull=false'
assert_contains "${CASE_ROOT}/release-calls.log" 'remove=false'
assert_contains "${CASE_ROOT}/release-calls.log" 'isolated_old=true'
assert_contains "${CASE_ROOT}/app/Caddyfile" 'sub2api-blue:8080'
assert_contains "${CASE_ROOT}/active-config.json" 'sub2api-blue:8080'
assert_contains "${CASE_ROOT}/startup-Caddyfile" 'sub2api-blue:8080'
[ -s "${CASE_ROOT}/curl-calls.log" ] || fail 'public health was not checked after fallback switch'

# If the switch helper reports failure after conclusively restoring all Caddy
# views to the failed active upstream, the started fallback is fenced again so
# a later timer run cannot create two application consumers.
new_case failed-switch-fences-fallback
write_standard_dependencies
write_container sub2api-green true unhealthy false 1 sub2api:broken healthy unhealthy
write_container sub2api-blue false exited false 0 sub2api:old-blue healthy healthy
if FAKE_RELEASE_RESULT=1 FAKE_RELEASE_ROLLBACK=true run_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted a failed Caddy switch'
fi
assert_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-blue'
assert_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-blue'
assert_contains "${CASE_ROOT}/output.log" 'stopping inactive fallback sub2api-blue'

# No viable old slot is a hard failure; the bad active remains stopped and no
# release helper is allowed to synthesize or pull a new application image.
new_case no-candidate
write_standard_dependencies
write_container sub2api-green true unhealthy false 1 sub2api:broken healthy unhealthy
if run_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard accepted an unavailable historical fallback'
fi
assert_contains "${CASE_ROOT}/output.log" 'no stopped, non-OOM, zero-exit historical fallback is available'
assert_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'
[ ! -s "${CASE_ROOT}/release-calls.log" ] || fail 'blue-green helper ran without a viable fallback'

# A recorded failure for the same active slot prevents the 30-second timer
# from repeatedly restarting a known-bad container. If an external actor has
# put it back in an unhealthy running state, the guard re-isolates it once.
new_case recovery-cooldown
write_standard_dependencies
write_container sub2api-green true unhealthy false 1 sub2api:broken healthy healthy
mkdir -p "${CASE_ROOT}/runtime-state"
printf 'failed_at_epoch=%s\nactive_container=sub2api-green\nreason=no-known-good-fallback\n' \
  "$(date +%s)" >"${CASE_ROOT}/runtime-state/last-failure.env"
if SUB2API_RUNTIME_GUARD_COOLDOWN_SECONDS=300 run_guard >"${CASE_ROOT}/output.log" 2>&1; then
  fail 'runtime guard ignored its recovery cooldown'
fi
assert_contains "${CASE_ROOT}/output.log" 'runtime recovery is cooling down'
assert_contains "${CASE_ROOT}/docker-calls.log" 'stop sub2api-green'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'restart sub2api-green'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-green'

# Dependencies are separately restarted in dependency order, then a healthy
# active app remains untouched.
new_case dependencies-start
write_container sub2api-postgres false exited false 0 postgres:18
write_container sub2api-redis false exited false 0 redis:8
write_container sub2api-caddy false exited false 0 caddy:2
write_container sub2api-green true healthy false 0 sub2api:current
run_guard >"${CASE_ROOT}/output.log" 2>&1
assert_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-postgres'
assert_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-redis'
assert_contains "${CASE_ROOT}/docker-calls.log" 'start sub2api-caddy'
assert_before "${CASE_ROOT}/docker-calls.log" 'start sub2api-postgres' 'start sub2api-redis'
assert_before "${CASE_ROOT}/docker-calls.log" 'start sub2api-redis' 'start sub2api-caddy'
assert_not_contains "${CASE_ROOT}/docker-calls.log" 'restart sub2api-green'

# A running but persistently unhealthy dependency receives one explicit
# restart before the guard gives up on application recovery.
new_case dependency-restart
write_container sub2api-postgres true unhealthy false 1 postgres:18 healthy healthy
write_container sub2api-redis true healthy false 0 redis:8
write_container sub2api-caddy true healthy false 0 caddy:2
write_container sub2api-green true healthy false 0 sub2api:current
run_guard >"${CASE_ROOT}/output.log" 2>&1
assert_contains "${CASE_ROOT}/docker-calls.log" 'restart sub2api-postgres'
assert_contains "${CASE_ROOT}/output.log" 'restarting unhealthy PostgreSQL container sub2api-postgres'

# A running Caddy with an unreadable Admin API is restarted once, then all
# three Caddy views and public health are verified before success.
new_case caddy-admin-restart
write_standard_dependencies
write_container sub2api-green true healthy false 0 sub2api:current
FAKE_CADDY_ADMIN_MODE=fail-until-restart run_guard >"${CASE_ROOT}/output.log" 2>&1
assert_contains "${CASE_ROOT}/docker-calls.log" 'restart sub2api-caddy'
assert_contains "${CASE_ROOT}/output.log" 'restarting Caddy container after its admin API remained unavailable'
assert_contains "${CASE_ROOT}/curl-calls.log" '--resolve example.invalid:443:192.0.2.10'
assert_contains "${CASE_ROOT}/curl-calls.log" '--noproxy *'

printf 'Runtime guard fake-Docker tests passed.\n'
