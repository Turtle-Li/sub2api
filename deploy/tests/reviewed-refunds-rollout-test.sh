#!/usr/bin/env bash

# Hermetic checks for the reviewed-refunds rollout host guard. The fake Docker
# CLI models metadata and loopback health calls only; it never contacts Docker,
# Caddy, PostgreSQL, or a production endpoint.

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
SCRIPT="${DEPLOY_DIR}/sub2api-reviewed-refunds-rollout.sh"
INSTALLER="${DEPLOY_DIR}/install-autodeploy.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-reviewed-refunds-rollout-test.XXXXXX")"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
FAKE_BIN="${TEST_ROOT}/bin"
CASE_ROOT=''
EXPECTED_COMMIT='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
OTHER_COMMIT='bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'

cleanup() {
  rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local file="$1" expected="$2"

  grep -Fq -- "$expected" "$file" \
    || { [ ! -f "$file" ] || sed -n '1,180p' "$file" >&2; fail "expected '${expected}' in ${file}"; }
}

assert_not_contains() {
  local file="$1" unexpected="$2"

  if [ -f "$file" ] && grep -Fq -- "$unexpected" "$file"; then
    sed -n '1,180p' "$file" >&2
    fail "did not expect '${unexpected}' in ${file}"
  fi
}

mkdir -p "$FAKE_BIN"
cat >"${FAKE_BIN}/flock" <<'EOF'
#!/usr/bin/env bash

if [ "${FAKE_LOCK_BUSY:-false}" = true ] && [ "$*" = '-n 8' ]; then
  exit 1
fi
exit 0
EOF
chmod +x "${FAKE_BIN}/flock"

cat >"${FAKE_BIN}/docker" <<'EOF'
#!/usr/bin/env bash

set -Eeuo pipefail

printf '%s\n' "$*" >>"${FAKE_DOCKER_CALLS:?}"
image_id="sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

container_value() {
  local object="$1" format="$2" name running health source

  case "$object" in
    sub2api-green|target-id)
      name='/sub2api-green'
      running="${FAKE_TARGET_RUNNING:-true}"
      health="${FAKE_TARGET_HEALTH:-healthy}"
      source='https://github.com/Turtle-Li/sub2api'
      ;;
    sub2api-caddy|caddy-id)
      name='/sub2api-caddy'
      running=true
      health=healthy
      source='https://example.invalid/caddy'
      ;;
    renamed-id)
      name='/sub2api-pre-cny-legacy-20260912'
      running="${FAKE_RENAMED_RUNNING:-true}"
      health=healthy
      source='https://github.com/Turtle-Li/sub2api'
      ;;
    sub2api|sub2api-blue)
      name="/${object}"
      running="${FAKE_OLD_RUNNING:-false}"
      health=healthy
      source='https://github.com/Turtle-Li/sub2api'
      ;;
    *) exit 1 ;;
  esac

  case "$format" in
    *'.State.Running'*) printf '%s\n' "$running" ;;
    *'.State.Health'*) printf '%s\n' "$health" ;;
    *'{{.Image}}'*) printf '%s\n' "$image_id" ;;
    *'{{.Name}}'*) printf '%s\n' "$name" ;;
    *'org.opencontainers.image.source'*) printf '%s\n' "$source" ;;
    *) exit 1 ;;
  esac
}

case "${1:-}" in
  inspect)
    [ "${3:-}" = '--format' ] || exit 1
    container_value "${2:-}" "${4:-}"
    ;;
  image)
    [ "${2:-}" = inspect ] && [ "${4:-}" = '--format' ] || exit 1
    case "${5:-}" in
      *'{{.Id}}'*) printf '%s\n' "$image_id" ;;
      *'org.opencontainers.image.source'*) printf '%s\n' 'https://github.com/Turtle-Li/sub2api' ;;
      *'org.opencontainers.image.revision'*) printf '%s\n' "${FAKE_TARGET_COMMIT:?}" ;;
      *) exit 1 ;;
    esac
    ;;
  ps)
    [ "${2:-}" = '-aq' ] \
      && [ "${3:-}" = '--filter' ] \
      && [ "${4:-}" = 'label=org.opencontainers.image.source=https://github.com/Turtle-Li/sub2api' ] \
      || exit 1
    printf '%s\n' target-id caddy-id
    if [ "${FAKE_RENAMED_WRITER:-false}" = true ]; then
      printf '%s\n' renamed-id
    fi
    ;;
  exec)
    args="$*"
    case "$args" in
      *sub2api-caddy*)
        if [[ "$args" == *'2019/config'* ]]; then
          printf '%s\n' "${FAKE_ADMIN_CADDY:?}"
        elif [[ "$args" == 'exec -i '* ]]; then
          printf '%s\n' "${FAKE_HOST_CADDY_JSON:?}"
        elif [[ "$args" == *'caddy adapt'* ]]; then
          printf '%s\n' "${FAKE_STARTUP_CADDY_JSON:?}"
        else
          exit 1
        fi
        ;;
      *'/internal/reviewed-refunds-rollout'*)
        [ "${FAKE_CAS_CONFLICT:-false}" != true ] || exit 1
        expected=''
        case "$args" in
          *'SUB2API_REVIEWED_REFUNDS_ROLLOUT_EXPECTED=false'*) expected=false ;;
          *'SUB2API_REVIEWED_REFUNDS_ROLLOUT_EXPECTED=true'*) expected=true ;;
        esac
        case "$args" in
          *'SUB2API_REVIEWED_REFUNDS_ROLLOUT_ENABLED=true'*) enabled=true ;;
          *'SUB2API_REVIEWED_REFUNDS_ROLLOUT_ENABLED=false'*) enabled=false ;;
          *) exit 1 ;;
        esac
        printf 'POST expected=%s enabled=%s\n' "$expected" "$enabled" >>"${FAKE_CAS_CALLS:?}"
        printf '{"changed":true,"enabled":%s}\n' "$enabled"
        ;;
      *'/internal/livez'*)
        printf '{"live":true,"in_flight_requests":%s}\n' "${FAKE_IN_FLIGHT:?}"
        ;;
      *'/internal/readyz'*)
        if [ "${FAKE_READYZ_FAIL_WHILE_DRAINING:-false}" = true ] \
          && grep -qx 'traffic=draining active_container=sub2api-green background=standby' \
            "${FAKE_NODE_STATE_FILE:?}"; then
          exit 1
        fi
        printf '%s\n' "${FAKE_READYZ_JSON:?}"
        ;;
      *'/internal/refund-rollback-readiness'*)
        printf '{"ready":true,"entitlement_reserved_reviewed_pending_count":%s}\n' "${FAKE_REFUND_PENDING_COUNT:?}"
        ;;
      *) exit 1 ;;
    esac
    ;;
  *) exit 1 ;;
esac
EOF
chmod +x "${FAKE_BIN}/docker"

grep -Fq 'deploy/sub2api-reviewed-refunds-rollout.sh' "$INSTALLER" \
  || fail 'installer preflight does not require the reviewed-refunds rollout helper'
# shellcheck disable=SC2016 # This is literal source text asserted in the installer.
grep -Fq 'bash -n "${SOURCE_ROOT}/deploy/sub2api-reviewed-refunds-rollout.sh"' "$INSTALLER" \
  || fail 'installer does not syntax-check the reviewed-refunds rollout helper'
# shellcheck disable=SC2016 # This is literal source text asserted in the installer.
grep -Fq 'install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-reviewed-refunds-rollout.sh"' "$INSTALLER" \
  || fail 'installer does not install the reviewed-refunds rollout helper as root-only executable'

new_case() {
  CASE_ROOT="$(mktemp -d "${TEST_ROOT}/case.XXXXXX")"
  mkdir -p "${CASE_ROOT}/app/scripts" "${CASE_ROOT}/runtime" "${CASE_ROOT}/locks"
  chmod 700 "${CASE_ROOT}/locks"
  printf 'reverse_proxy sub2api-green:8080\n' >"${CASE_ROOT}/app/Caddyfile"
  printf 'traffic=accepting active_container=sub2api-green background=active\n' >"${CASE_ROOT}/runtime/node-state"
  cat >"${CASE_ROOT}/app/scripts/sub2api-node-state.sh" <<'EOF'
#!/usr/bin/env bash

set -Eeuo pipefail

state_file="${FAKE_NODE_STATE_FILE:?}"
printf '%s\n' "$*" >>"${FAKE_NODE_STATE_CALLS:?}"
case "${1:-}" in
  status) cat "$state_file" ;;
  drain)
    printf 'traffic=draining active_container=sub2api-green background=standby\n' >"$state_file"
    printf 'DRAINED\n'
    ;;
  *) exit 1 ;;
esac
EOF
  chmod +x "${CASE_ROOT}/app/scripts/sub2api-node-state.sh"
  cat >"${CASE_ROOT}/config.env" <<EOF
SUB2API_APP_DIR=${CASE_ROOT}/app
SUB2API_CADDY_CONTAINER=sub2api-caddy
SUB2API_MAINTENANCE_LOCK_FILE=${CASE_ROOT}/locks/maintenance.lock
SUB2API_NODE_STATE_SCRIPT=${CASE_ROOT}/app/scripts/sub2api-node-state.sh
SUB2API_NODE_STATE_DIR=${CASE_ROOT}/runtime
SUB2API_GITHUB_IMAGE_SOURCE=https://github.com/Turtle-Li/sub2api
SUB2API_RUNTIME_GUARD_CADDY_CONFIG_PATH=/etc/caddy/Caddyfile
EOF
  chmod 600 "${CASE_ROOT}/config.env"

  FAKE_TARGET_RUNNING=true
  FAKE_TARGET_HEALTH=healthy
  FAKE_TARGET_COMMIT="$EXPECTED_COMMIT"
  FAKE_OLD_RUNNING=false
  FAKE_RENAMED_WRITER=false
  FAKE_RENAMED_RUNNING=true
  FAKE_LOCK_BUSY=false
  FAKE_CAS_CONFLICT=false
  FAKE_IN_FLIGHT=0
  FAKE_REFUND_PENDING_COUNT=0
  FAKE_READYZ_JSON='{"ready":true}'
  FAKE_READYZ_FAIL_WHILE_DRAINING=true
  FAKE_HOST_CADDY_JSON='{"dial":"sub2api-green:8080"}'
  FAKE_STARTUP_CADDY_JSON='{"dial":"sub2api-green:8080"}'
  FAKE_ADMIN_CADDY='{"dial":"sub2api-green:8080"}'
  FAKE_DOCKER_CALLS="${CASE_ROOT}/docker-calls.log"
  FAKE_CAS_CALLS="${CASE_ROOT}/cas-calls.log"
  FAKE_NODE_STATE_CALLS="${CASE_ROOT}/node-state-calls.log"
  FAKE_NODE_STATE_FILE="${CASE_ROOT}/runtime/node-state"
}

run_helper() {
  local expected_state="$1"

  env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_TARGET_RUNNING="$FAKE_TARGET_RUNNING" \
    FAKE_TARGET_HEALTH="$FAKE_TARGET_HEALTH" \
    FAKE_TARGET_COMMIT="$FAKE_TARGET_COMMIT" \
    FAKE_OLD_RUNNING="$FAKE_OLD_RUNNING" \
    FAKE_RENAMED_WRITER="$FAKE_RENAMED_WRITER" \
    FAKE_RENAMED_RUNNING="$FAKE_RENAMED_RUNNING" \
    FAKE_LOCK_BUSY="$FAKE_LOCK_BUSY" \
    FAKE_CAS_CONFLICT="$FAKE_CAS_CONFLICT" \
    FAKE_IN_FLIGHT="$FAKE_IN_FLIGHT" \
    FAKE_REFUND_PENDING_COUNT="$FAKE_REFUND_PENDING_COUNT" \
    FAKE_READYZ_JSON="$FAKE_READYZ_JSON" \
    FAKE_READYZ_FAIL_WHILE_DRAINING="$FAKE_READYZ_FAIL_WHILE_DRAINING" \
    FAKE_HOST_CADDY_JSON="$FAKE_HOST_CADDY_JSON" \
    FAKE_STARTUP_CADDY_JSON="$FAKE_STARTUP_CADDY_JSON" \
    FAKE_ADMIN_CADDY="$FAKE_ADMIN_CADDY" \
    FAKE_DOCKER_CALLS="$FAKE_DOCKER_CALLS" \
    FAKE_CAS_CALLS="$FAKE_CAS_CALLS" \
    FAKE_NODE_STATE_CALLS="$FAKE_NODE_STATE_CALLS" \
    FAKE_NODE_STATE_FILE="$FAKE_NODE_STATE_FILE" \
    SUB2API_REVIEWED_REFUNDS_ROLLOUT_ALLOW_NON_ROOT_FOR_TESTS=1 \
    SUB2API_REVIEWED_REFUNDS_ROLLOUT_CONFIG_FILE="${CASE_ROOT}/config.env" \
    SUB2API_REVIEWED_REFUNDS_ROLLOUT_DRAIN_ATTEMPTS=2 \
    SUB2API_REVIEWED_REFUNDS_ROLLOUT_DRAIN_INTERVAL_SECONDS=0 \
    /bin/bash "$SCRIPT" "$EXPECTED_COMMIT" sub2api-green "$expected_state"
}

expect_failure() {
  local expected_state="$1" label="$2" expected_output="$3"

  if run_helper "$expected_state" >"${CASE_ROOT}/${label}.out" 2>"${CASE_ROOT}/${label}.err"; then
    fail "${label} unexpectedly succeeded"
  fi
  assert_contains "${CASE_ROOT}/${label}.err" "$expected_output"
}

expect_success() {
  local expected_state="$1" label="$2"

  if ! run_helper "$expected_state" >"${CASE_ROOT}/${label}.out" 2>"${CASE_ROOT}/${label}.err"; then
    sed -n '1,180p' "${CASE_ROOT}/${label}.err" >&2
    fail "${label} unexpectedly failed"
  fi
}

new_case
expect_success absent absent
assert_contains "$FAKE_CAS_CALLS" 'POST expected= enabled=true'
assert_not_contains "$FAKE_DOCKER_CALLS" 'test-monitor-token'

new_case
expect_success false false
assert_contains "$FAKE_CAS_CALLS" 'POST expected=false enabled=true'

new_case
expect_success true true
assert_contains "$FAKE_CAS_CALLS" 'POST expected=true enabled=false'
assert_contains "$FAKE_NODE_STATE_CALLS" 'drain'
grep -qx 'traffic=draining active_container=sub2api-green background=standby' "$FAKE_NODE_STATE_FILE" \
  || fail 'successful disable did not retain the expected drained runtime state'

new_case
FAKE_CAS_CONFLICT=true
expect_failure false cas-conflict 'reviewed-refunds rollout CAS was rejected'

new_case
FAKE_OLD_RUNNING=true
expect_failure false old-writer 'another canonical app writer is running: sub2api'

new_case
FAKE_RENAMED_WRITER=true
expect_failure false renamed-writer 'another Sub2API writer is running: sub2api-pre-cny-legacy-20260912'

new_case
FAKE_TARGET_COMMIT="$OTHER_COMMIT"
expect_failure false wrong-commit "target image revision label does not match ${EXPECTED_COMMIT}"

new_case
FAKE_ADMIN_CADDY='{"dial":"sub2api-blue:8080"}'
expect_failure false caddy-disagreement 'Caddy host/startup/Admin views do not uniquely select sub2api-green:8080'

new_case
FAKE_ADMIN_CADDY='{"dial":"sub2api-green:8080","unexpected":{"dial":"unknown-writer:8080"}}'
expect_failure false unknown-upstream 'Caddy host/startup/Admin views do not uniquely select sub2api-green:8080'

new_case
: >"${CASE_ROOT}/app/.sub2api-blue-green-caddy-transaction.env"
expect_failure false retained-transaction 'unfinished release transaction exists'

new_case
FAKE_LOCK_BUSY=true
expect_failure false busy-lock 'production maintenance is already running'
[ ! -s "$FAKE_DOCKER_CALLS" ] || fail 'busy maintenance lock reached Docker topology checks'

new_case
FAKE_TARGET_HEALTH=unhealthy
expect_failure false unhealthy 'target container is not healthy: sub2api-green (unhealthy)'

new_case
FAKE_REFUND_PENDING_COUNT=1
expect_failure true pending 'leaving traffic drained'
grep -qx 'traffic=draining active_container=sub2api-green background=standby' "$FAKE_NODE_STATE_FILE" \
  || fail 'pending reconciliation failure did not leave the runtime drained'
[ ! -e "$FAKE_CAS_CALLS" ] || fail 'pending reconciliation reached the CAS endpoint'

new_case
FAKE_IN_FLIGHT=2
expect_failure true in-flight 'leaving traffic drained'
grep -qx 'traffic=draining active_container=sub2api-green background=standby' "$FAKE_NODE_STATE_FILE" \
  || fail 'in-flight failure did not leave the runtime drained'
[ ! -e "$FAKE_CAS_CALLS" ] || fail 'in-flight drain failure reached the CAS endpoint'

printf 'Reviewed-refunds rollout helper tests passed.\n'
