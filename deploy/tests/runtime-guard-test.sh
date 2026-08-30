#!/usr/bin/env bash

# All runtime-guard behavior is exercised against a stateful fake Docker CLI.
# The test never connects to Docker, Caddy, or a public endpoint.

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
SCRIPT="${DEPLOY_DIR}/sub2api-runtime-guard.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-runtime-guard-test.XXXXXX")"
FAKE_BIN="${TEST_ROOT}/bin"
CASE_ROOT=""

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
}

save_state() {
  local file
  file="$(state_file "$1")"
  printf 'running=%s\nhealth=%s\noom=%s\nexit_code=%s\nimage=%s\nstart_health=%s\nrestart_health=%s\n' \
    "$running" "$health" "$oom" "$exit_code" "$image" "$start_health" "$restart_health" >"$file"
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
    while [ "$#" -gt 0 ]; do
      case "$1" in
        -e)
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
    [ "$running" = true ] && [ "$health" = healthy ]
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

new_case() {
  local name="$1"

  CASE_ROOT="${TEST_ROOT}/${name}"
  mkdir -p "${CASE_ROOT}/app/scripts" "${CASE_ROOT}/containers"
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
printf 'old=%s\nnew=%s\nimage=%s\nfrom=%s\nto=%s\nbackup=%s\npull=%s\nremove=%s\n' \
  "$OLD_CONTAINER" "$NEW_CONTAINER" "$NEW_IMAGE" "$CADDY_UPSTREAM_FROM" "$CADDY_UPSTREAM_TO" \
  "$RUN_BACKUP" "$PULL_IMAGE" "$REMOVE_EXISTING_NEW_CONTAINER" >>"$FAKE_RELEASE_CALLS"
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
    FAKE_NODE_STATE_CALLS="${CASE_ROOT}/node-state-calls.log" \
    FAKE_RELEASE_CALLS="${CASE_ROOT}/release-calls.log" \
    FAKE_STARTUP_CONFIG_FILE="${CASE_ROOT}/startup-Caddyfile" \
    SUB2API_APP_DIR="${CASE_ROOT}/app" \
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

printf 'Runtime guard fake-Docker tests passed.\n'
