#!/usr/bin/env bash

set -Eeuo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${DEPLOY_DIR}/sub2api-node-state.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-node-state-test.XXXXXX")"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT
mkdir -p "${TEST_ROOT}/bin"
mkdir -p "${TEST_ROOT}/locks"
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

file_mode() {
  stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1"
}

file_inode() {
  stat -c '%i' "$1" 2>/dev/null || stat -f '%i' "$1"
}

[ "$(run_state status)" = 'traffic=missing active_container=sub2api-blue background=missing' ]
[ "$(run_state bootstrap)" = BOOTSTRAPPED ]
[ "$(file_mode "${TEST_ROOT}/state")" = 755 ]
[ "$(file_mode "${TEST_ROOT}/state/background")" = 755 ]
[ "$(file_mode "${TEST_ROOT}/state/traffic-state")" = 644 ]
[ "$(file_mode "${TEST_ROOT}/state/background/sub2api-blue")" = 644 ]
traffic_inode="$(file_inode "${TEST_ROOT}/state/traffic-state")"
blue_inode="$(file_inode "${TEST_ROOT}/state/background/sub2api-blue")"
# A hard link models a running container's bind-mounted view of the inode. It
# must observe every transition without being recreated.
ln "${TEST_ROOT}/state/traffic-state" "${TEST_ROOT}/container-traffic-state"
ln "${TEST_ROOT}/state/background/sub2api-blue" "${TEST_ROOT}/container-blue-background"
lock_mode="$(file_mode "${TEST_ROOT}/locks")"
[ "$lock_mode" = 700 ] || { printf 'FAIL: maintenance lock parent is not private: %s\n' "$lock_mode" >&2; exit 1; }
[ "$(run_state status)" = 'traffic=accepting active_container=sub2api-blue background=active' ]
[ "$(run_state drain)" = DRAINING ]
[ "$(file_inode "${TEST_ROOT}/state/traffic-state")" = "$traffic_inode" ]
[ "$(file_inode "${TEST_ROOT}/state/background/sub2api-blue")" = "$blue_inode" ]
[ "$(cat "${TEST_ROOT}/container-traffic-state")" = draining ]
[ "$(cat "${TEST_ROOT}/container-blue-background")" = standby ]
[ "$(run_state status)" = 'traffic=draining active_container=sub2api-blue background=standby' ]
[ "$(run_state abort)" = ABORTED ]
[ "$(file_inode "${TEST_ROOT}/state/traffic-state")" = "$traffic_inode" ]
[ "$(file_inode "${TEST_ROOT}/state/background/sub2api-blue")" = "$blue_inode" ]
[ "$(cat "${TEST_ROOT}/container-traffic-state")" = accepting ]
[ "$(cat "${TEST_ROOT}/container-blue-background")" = active ]
[ "$(run_state status)" = 'traffic=accepting active_container=sub2api-blue background=active' ]
if run_state local-preserve-standby sub2api-green >"${TEST_ROOT}/preserve-active.out" 2>"${TEST_ROOT}/preserve-active.err"; then
  printf 'FAIL: standby-preserving release accepted an active background owner\n' >&2
  exit 1
fi
grep -q 'requires the current generation to be standby' "${TEST_ROOT}/preserve-active.err"
[ ! -e "${TEST_ROOT}/state/local-release.env" ]
[ "$(run_state drain)" = DRAINING ]
[ "$(run_state preflight)" = PREFLIGHT ]
[ "$(run_state status)" = 'traffic=accepting active_container=sub2api-blue background=standby' ]
[ "$(file_inode "${TEST_ROOT}/state/traffic-state")" = "$traffic_inode" ]
[ "$(file_inode "${TEST_ROOT}/state/background/sub2api-blue")" = "$blue_inode" ]
[ "$(cat "${TEST_ROOT}/container-traffic-state")" = accepting ]
[ "$(cat "${TEST_ROOT}/container-blue-background")" = standby ]
[ "$(run_state rollback-standby)" = 'ROLLBACK_STANDBY sub2api-blue' ]
[ "$(run_state status)" = 'traffic=accepting active_container=sub2api-blue background=standby' ]
[ "$(cat "${TEST_ROOT}/container-traffic-state")" = accepting ]
[ "$(cat "${TEST_ROOT}/container-blue-background")" = standby ]
[ "$(run_state standby sub2api-green)" = 'STANDBY sub2api-green' ]
green_inode="$(file_inode "${TEST_ROOT}/state/background/sub2api-green")"
ln "${TEST_ROOT}/state/background/sub2api-green" "${TEST_ROOT}/container-green-background"
if run_state standby sub2api-blue >/dev/null 2>&1; then
  printf 'FAIL: active container was accepted as standby\n' >&2
  exit 1
fi

# A request-serving rollback node can recreate either color without becoming a
# shared background-work owner, including after an interrupted switch.
[ "$(run_state local-preserve-standby sub2api-green)" = 'LOCAL_PRESERVE_STANDBY sub2api-green' ]
grep -qx 'final_background=standby' "${TEST_ROOT}/state/local-release.env"
printf 'api.turtleligpt.com { reverse_proxy sub2api-green:8080 }\n' >"${TEST_ROOT}/Caddyfile"
[ "$(run_state commit-local)" = 'COMMITTED_LOCAL_STANDBY sub2api-green' ]
[ "$(run_state status)" = 'traffic=accepting active_container=sub2api-green background=standby' ]
[ "$(cat "${TEST_ROOT}/container-green-background")" = standby ]
[ "$(cat "${TEST_ROOT}/container-blue-background")" = standby ]
[ "$(run_state local-preserve-standby sub2api-blue)" = 'LOCAL_PRESERVE_STANDBY sub2api-blue' ]
[ "$(run_state abort-local)" = 'ABORTED_LOCAL_STANDBY sub2api-green' ]
[ "$(run_state status)" = 'traffic=accepting active_container=sub2api-green background=standby' ]
[ "$(run_state local-preserve-standby sub2api-blue)" = 'LOCAL_PRESERVE_STANDBY sub2api-blue' ]
printf 'api.turtleligpt.com { reverse_proxy sub2api-blue:8080 }\n' >"${TEST_ROOT}/Caddyfile"
[ "$(run_state recover-local)" = 'RECOVERED_LOCAL_STANDBY sub2api-blue' ]
[ "$(run_state status)" = 'traffic=accepting active_container=sub2api-blue background=standby' ]
[ "$(cat "${TEST_ROOT}/container-blue-background")" = standby ]
[ "$(cat "${TEST_ROOT}/container-green-background")" = standby ]
[ "$(run_state local-preserve-standby sub2api-green)" = 'LOCAL_PRESERVE_STANDBY sub2api-green' ]
[ "$(run_state recover-local)" = 'ABORTED_LOCAL_STANDBY sub2api-blue' ]
printf 'api.turtleligpt.com { reverse_proxy sub2api-green:8080 }\n' >"${TEST_ROOT}/Caddyfile"
[ "$(run_state activate)" = ACTIVE ]
[ "$(file_inode "${TEST_ROOT}/state/background/sub2api-green")" = "$green_inode" ]
[ "$(cat "${TEST_ROOT}/container-green-background")" = active ]
[ "$(run_state status)" = 'traffic=accepting active_container=sub2api-green background=active' ]
[ "$(cat "${TEST_ROOT}/state/background/sub2api-blue")" = standby ]

# A runtime-guard run repairs a local release killed on either side of the
# Caddy switch without interfering with cluster drain/preflight operations.
[ "$(run_state local-standby sub2api-blue)" = 'LOCAL_STANDBY sub2api-blue' ]
grep -v '^final_background=' "${TEST_ROOT}/state/local-release.env" \
  >"${TEST_ROOT}/state/local-release.env.legacy"
mv -f -- "${TEST_ROOT}/state/local-release.env.legacy" "${TEST_ROOT}/state/local-release.env"
printf 'api.turtleligpt.com { reverse_proxy sub2api-blue:8080 }\n' >"${TEST_ROOT}/Caddyfile"
[ "$(run_state recover-local)" = 'RECOVERED_LOCAL sub2api-blue' ]
[ "$(run_state status)" = 'traffic=accepting active_container=sub2api-blue background=active' ]
[ "$(cat "${TEST_ROOT}/state/background/sub2api-green")" = standby ]
[ "$(file_inode "${TEST_ROOT}/state/traffic-state")" = "$traffic_inode" ]
[ "$(file_inode "${TEST_ROOT}/state/background/sub2api-blue")" = "$blue_inode" ]
[ "$(file_inode "${TEST_ROOT}/state/background/sub2api-green")" = "$green_inode" ]
[ "$(cat "${TEST_ROOT}/container-blue-background")" = active ]
[ "$(cat "${TEST_ROOT}/container-green-background")" = standby ]
[ ! -e "${TEST_ROOT}/state/local-release.env" ]

[ "$(run_state local-standby sub2api-green)" = 'LOCAL_STANDBY sub2api-green' ]
[ "$(run_state recover-local)" = 'ABORTED_LOCAL sub2api-blue' ]
[ "$(run_state status)" = 'traffic=accepting active_container=sub2api-blue background=active' ]
[ "$(cat "${TEST_ROOT}/state/background/sub2api-green")" = standby ]
[ "$(file_inode "${TEST_ROOT}/state/background/sub2api-blue")" = "$blue_inode" ]
[ "$(file_inode "${TEST_ROOT}/state/background/sub2api-green")" = "$green_inode" ]
[ "$(cat "${TEST_ROOT}/container-blue-background")" = active ]
[ "$(cat "${TEST_ROOT}/container-green-background")" = standby ]
[ "$(run_state recover-local)" = NO_LOCAL_RECOVERY ]

# Normal single-host blue/green finalization is explicit. Cluster commands must
# never consume the local transaction file.
[ "$(run_state local-standby sub2api-green)" = 'LOCAL_STANDBY sub2api-green' ]
printf 'api.turtleligpt.com { reverse_proxy sub2api-green:8080 }\n' >"${TEST_ROOT}/Caddyfile"
[ "$(run_state commit-local)" = 'COMMITTED_LOCAL sub2api-green' ]
[ "$(file_inode "${TEST_ROOT}/state/traffic-state")" = "$traffic_inode" ]
[ "$(file_inode "${TEST_ROOT}/state/background/sub2api-green")" = "$green_inode" ]
[ "$(cat "${TEST_ROOT}/container-traffic-state")" = accepting ]
[ "$(cat "${TEST_ROOT}/container-green-background")" = active ]
[ "$(cat "${TEST_ROOT}/container-blue-background")" = standby ]
[ ! -e "${TEST_ROOT}/state/local-release.env" ]
[ "$(run_state local-standby sub2api-blue)" = 'LOCAL_STANDBY sub2api-blue' ]
printf 'api.turtleligpt.com { reverse_proxy sub2api-green:8080 }\n' >"${TEST_ROOT}/Caddyfile"
[ "$(run_state abort-local)" = 'ABORTED_LOCAL sub2api-green' ]
[ "$(file_inode "${TEST_ROOT}/state/traffic-state")" = "$traffic_inode" ]
[ "$(file_inode "${TEST_ROOT}/state/background/sub2api-green")" = "$green_inode" ]
[ "$(cat "${TEST_ROOT}/container-green-background")" = active ]
[ "$(cat "${TEST_ROOT}/container-blue-background")" = standby ]
[ ! -e "${TEST_ROOT}/state/local-release.env" ]

# A cluster drain/preflight must not interleave with a pending single-host
# release transaction because the runtime guard could otherwise re-admit it.
[ "$(run_state local-standby sub2api-blue)" = 'LOCAL_STANDBY sub2api-blue' ]
for cluster_action in drain preflight rollback-standby activate abort; do
  if run_state "$cluster_action" >"${TEST_ROOT}/${cluster_action}.out" 2>"${TEST_ROOT}/${cluster_action}.err"; then
    printf 'FAIL: %s accepted an unfinished local release transaction\n' "$cluster_action" >&2
    exit 1
  fi
  grep -q 'run recover-local first' "${TEST_ROOT}/${cluster_action}.err"
done
[ "$(run_state status)" = 'traffic=accepting active_container=sub2api-green background=active' ]
[ "$(run_state recover-local)" = 'ABORTED_LOCAL sub2api-green' ]
[ "$(run_state drain)" = DRAINING ]
[ "$(run_state preflight)" = PREFLIGHT ]

# A kill/ENOSPC during the in-place truncate/write window can leave an empty or
# prefix-only value. Bootstrap repairs only those recognizable interruption
# shapes to fail-closed states; arbitrary content remains a hard failure.
: >"${TEST_ROOT}/state/traffic-state"
printf 'act' >"${TEST_ROOT}/state/background/sub2api-green"
[ "$(run_state bootstrap)" = BOOTSTRAPPED ]
[ "$(run_state status)" = 'traffic=draining active_container=sub2api-green background=standby' ]
[ "$(file_inode "${TEST_ROOT}/state/traffic-state")" = "$traffic_inode" ]
[ "$(file_inode "${TEST_ROOT}/state/background/sub2api-green")" = "$green_inode" ]
[ "$(run_state abort)" = ABORTED ]
printf 'unexpected-owner-value\n' >"${TEST_ROOT}/state/traffic-state"
if run_state bootstrap >"${TEST_ROOT}/invalid-bootstrap.out" 2>"${TEST_ROOT}/invalid-bootstrap.err"; then
  printf 'FAIL: arbitrary invalid runtime state was repaired\n' >&2
  exit 1
fi
grep -q 'runtime state file contains an invalid value' "${TEST_ROOT}/invalid-bootstrap.err"

if run_state traffic accepting >/dev/null 2>&1; then
  printf 'FAIL: unsupported partial state mutation was accepted\n' >&2
  exit 1
fi

printf 'Node traffic/background state tests passed.\n'
