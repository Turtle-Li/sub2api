#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
SCRIPT="${DEPLOY_DIR}/install-autodeploy.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-install-autodeploy-test.XXXXXX")"
FAKE_BIN="${TEST_ROOT}/bin"
APP_DIR="${TEST_ROOT}/app"
CONFIG_FILE="${TEST_ROOT}/etc/sub2api-autodeploy.env"
UNIT_DIR="${TEST_ROOT}/units"
SYSTEMCTL_CALLS="${TEST_ROOT}/systemctl-calls.log"

cleanup() {
  rm -rf "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local file="$1" expected="$2"
  grep -Fqx -- "$expected" "$file" || fail "expected exact line '${expected}' in ${file}"
}

mkdir -p "$FAKE_BIN" "$APP_DIR" "$UNIT_DIR"
: >"$SYSTEMCTL_CALLS"

cat >"${FAKE_BIN}/id" <<'EOF'
#!/usr/bin/env bash
[ "${1:-}" = -u ] || exit 1
printf '0\n'
EOF
chmod +x "${FAKE_BIN}/id"

cat >"${FAKE_BIN}/systemctl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_SYSTEMCTL_CALLS"
EOF
chmod +x "${FAKE_BIN}/systemctl"

cat >"${FAKE_BIN}/install" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
create_parent=false
directory_mode=false
mode=""
args=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    -D) create_parent=true ;;
    -d) directory_mode=true ;;
    -m)
      shift
      mode="${1:-}"
      ;;
    *) args+=("$1") ;;
  esac
  shift
done
if [ "$directory_mode" = true ]; then
  mkdir -p "${args[@]}"
  [ -z "$mode" ] || chmod "$mode" "${args[@]}"
  exit 0
fi
[ "${#args[@]}" -eq 2 ] || exit 2
source_path="${args[0]}"
destination_path="${args[1]}"
if [ "$create_parent" = true ]; then
  mkdir -p -- "${destination_path%/*}"
fi
cp "$source_path" "$destination_path"
[ -z "$mode" ] || chmod "$mode" "$destination_path"
EOF
chmod +x "${FAKE_BIN}/install"

for command_name in docker flock zstd; do
  cat >"${FAKE_BIN}/${command_name}" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod +x "${FAKE_BIN}/${command_name}"
done

env \
  PATH="${FAKE_BIN}:${PATH}" \
  FAKE_SYSTEMCTL_CALLS="$SYSTEMCTL_CALLS" \
  SUB2API_APP_DIR="$APP_DIR" \
  SUB2API_AUTODEPLOY_CONFIG_FILE="$CONFIG_FILE" \
  SUB2API_AUTODEPLOY_UNIT_DIR="$UNIT_DIR" \
  SUB2API_RUNTIME_GUARD_EXECUTABLE="${TEST_ROOT}/libexec/sub2api-runtime-guard.sh" \
  /bin/bash "$SCRIPT" \
    --production-branch main \
    --production-repo https://github.com/Turtle-Li/sub2api.git \
    --upstream-repo https://github.com/Wei-Shaw/sub2api.git \
    --health-url https://api.turtleligpt.com/health \
    --health-resolve api.turtleligpt.com:443:192.0.2.10 \
    --dependency-mode external \
    --runtime-network candidate-network \
    --runtime-data-volume candidate-data \
    --caddy-container candidate-caddy \
    --external-runtime-env-file /etc/sub2api-external-runtime.env \
    --external-ca-file /opt/sub2api/db-host-ca/ca.crt \
    --dual-node-runtime-enabled true \
    --replace-config \
    --install-blue-green-helper \
    --no-enable-runtime-guard \
    --no-enable \
    >"${TEST_ROOT}/output.log"

assert_contains "$CONFIG_FILE" 'SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE=external'
assert_contains "$CONFIG_FILE" 'SUB2API_RUNTIME_GUARD_NETWORK=candidate-network'
assert_contains "$CONFIG_FILE" 'SUB2API_RUNTIME_GUARD_DATA_VOLUME=candidate-data'
assert_contains "$CONFIG_FILE" 'SUB2API_CADDY_CONTAINER=candidate-caddy'
assert_contains "$CONFIG_FILE" 'SUB2API_EXTERNAL_RUNTIME_ENV_FILE=/etc/sub2api-external-runtime.env'
assert_contains "$CONFIG_FILE" 'SUB2API_EXTERNAL_CA_FILE=/opt/sub2api/db-host-ca/ca.crt'
assert_contains "$CONFIG_FILE" 'SUB2API_DUAL_NODE_RUNTIME_ENABLED=true'
grep -Fqx -- 'disable --now sub2api-runtime-guard.timer' "$SYSTEMCTL_CALLS" \
  || fail 'runtime guard timer was not explicitly left disabled'
if grep -Fq -- 'enable --now sub2api-runtime-guard.timer' "$SYSTEMCTL_CALLS"; then
  fail 'runtime guard timer was enabled during staged external-runtime installation'
fi
grep -Fqx -- 'disable --now sub2api-autodeploy.timer' "$SYSTEMCTL_CALLS" \
  || fail 'polling timer was not left disabled'

printf 'Autodeploy staged external-runtime installation test passed.\n'
