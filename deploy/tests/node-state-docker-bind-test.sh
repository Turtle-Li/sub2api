#!/usr/bin/env bash

# Linux/macOS Docker integration for the single-file bind-mount invariant.
# It uses only the pinned local Alpine image digest and never contacts a registry.

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
SCRIPT="${DEPLOY_DIR}/sub2api-node-state.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-node-state-docker.XXXXXX")"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
CONTAINER_NAME="sub2api-node-state-bind-test-$$"
TEST_IMAGE="alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc"

cleanup() {
  docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
  rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

if ! docker version >/dev/null 2>&1; then
  printf 'SKIP: Docker is not available for the bind-mount integration test.\n'
  exit 0
fi
if ! docker image inspect "$TEST_IMAGE" >/dev/null 2>&1; then
  printf 'SKIP: pinned Alpine image is not local; this test never pulls images.\n'
  exit 0
fi

mkdir -p "${TEST_ROOT}/bin" "${TEST_ROOT}/locks"
chmod 700 "${TEST_ROOT}/locks"
printf '#!/usr/bin/env bash\nexit 0\n' >"${TEST_ROOT}/bin/flock"
chmod +x "${TEST_ROOT}/bin/flock"
printf 'api.turtleligpt.com { reverse_proxy sub2api-blue:8080 }\n' >"${TEST_ROOT}/Caddyfile"

run_state() {
  SUB2API_NODE_STATE_ALLOW_NON_ROOT_FOR_TESTS=1 \
    SUB2API_NODE_STATE_DIR="${TEST_ROOT}/state" \
    SUB2API_NODE_STATE_CADDYFILE="${TEST_ROOT}/Caddyfile" \
    SUB2API_MAINTENANCE_LOCK_FILE="${TEST_ROOT}/locks/maintenance.lock" \
    PATH="${TEST_ROOT}/bin:${PATH}" \
    /bin/bash "$SCRIPT" "$@"
}

[ "$(run_state bootstrap)" = BOOTSTRAPPED ]
docker run -d \
  --name "$CONTAINER_NAME" \
  --pull never \
  --network none \
  --restart no \
  --read-only \
  --mount "type=bind,source=${TEST_ROOT}/state/traffic-state,target=/run/traffic-state,readonly" \
  --mount "type=bind,source=${TEST_ROOT}/state/background/sub2api-blue,target=/run/background-state,readonly" \
  --entrypoint sh \
  "$TEST_IMAGE" \
  -c 'while :; do sleep 3600; done' >/dev/null

[ "$(docker exec "$CONTAINER_NAME" cat /run/traffic-state)" = accepting ]
[ "$(docker exec "$CONTAINER_NAME" cat /run/background-state)" = active ]

[ "$(run_state drain)" = DRAINING ]
[ "$(docker exec "$CONTAINER_NAME" cat /run/traffic-state)" = draining ] \
  || fail 'container retained stale traffic state after drain'
[ "$(docker exec "$CONTAINER_NAME" cat /run/background-state)" = standby ] \
  || fail 'container retained stale background state after drain'

[ "$(run_state abort)" = ABORTED ]
[ "$(docker exec "$CONTAINER_NAME" cat /run/traffic-state)" = accepting ] \
  || fail 'container retained stale traffic state after abort'
[ "$(docker exec "$CONTAINER_NAME" cat /run/background-state)" = active ] \
  || fail 'container retained stale background state after abort'

printf 'Node state Docker bind-mount integration test passed.\n'
