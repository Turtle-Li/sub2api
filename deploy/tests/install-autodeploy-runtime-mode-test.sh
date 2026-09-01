#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-install-autodeploy-test.XXXXXX")"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
SOURCE_ROOT="${TEST_ROOT}/source"
SCRIPT="${SOURCE_ROOT}/deploy/install-autodeploy.sh"
FAKE_BIN="${TEST_ROOT}/bin"
APP_DIR="${TEST_ROOT}/app"
CONFIG_FILE="${TEST_ROOT}/etc/sub2api-autodeploy.env"
UNIT_DIR="${TEST_ROOT}/units"
SYSTEMCTL_CALLS="${TEST_ROOT}/systemctl-calls.log"
INSTALL_CALLS="${TEST_ROOT}/install-calls.log"
MAINTENANCE_LOCK_FILE="${TEST_ROOT}/runtime/sub2api-maintenance.lock"
REAL_STAT="$(command -v stat)"
FAKE_HELPER_STAGE_PREFIX="${TEST_ROOT}/sub2api-maintenance-helper."
FAKE_SOURCE_ROOT="$SOURCE_ROOT"
LOCK_DESCENDANT_PID=""
SIGNAL_SUPERVISOR_PID=""
SIGNAL_CHILD_PID=""
export REAL_STAT FAKE_HELPER_STAGE_PREFIX FAKE_SOURCE_ROOT
export SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1
export SUB2API_MAINTENANCE_LOCK_HELPER_STAGE_ROOT="$TEST_ROOT"

cleanup() {
  if [ -n "$SIGNAL_SUPERVISOR_PID" ] && kill -0 "$SIGNAL_SUPERVISOR_PID" >/dev/null 2>&1; then
    kill -TERM "$SIGNAL_SUPERVISOR_PID" >/dev/null 2>&1 || true
    sleep 0.02
    kill -TERM "$SIGNAL_SUPERVISOR_PID" >/dev/null 2>&1 || true
    wait "$SIGNAL_SUPERVISOR_PID" >/dev/null 2>&1 || true
  fi
  if [ -n "$SIGNAL_CHILD_PID" ] && kill -0 "$SIGNAL_CHILD_PID" >/dev/null 2>&1; then
    kill -KILL "$SIGNAL_CHILD_PID" >/dev/null 2>&1 || true
  fi
  if [ -n "$LOCK_DESCENDANT_PID" ] && kill -0 "$LOCK_DESCENDANT_PID" >/dev/null 2>&1; then
    kill "$LOCK_DESCENDANT_PID" >/dev/null 2>&1 || true
  fi
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

file_mode() {
  stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1"
}

mkdir -p "$FAKE_BIN" "$APP_DIR" "$UNIT_DIR" "$SOURCE_ROOT"
cp -R "$DEPLOY_DIR" "${SOURCE_ROOT}/deploy"
SOURCE_HELPER="${SOURCE_ROOT}/deploy/sub2api-maintenance-lock.sh"
chmod 644 "$SOURCE_HELPER"
# This test intentionally keeps the invoked installer and helper owned by a
# non-root source-tree owner. sudo may execute exactly that normal checkout.
if [ "$(id -u)" -eq 0 ]; then
  chown 12345 "$SCRIPT" "$SOURCE_HELPER"
fi
SOURCE_HELPER_OWNER="$(stat -c '%u' "$SOURCE_HELPER" 2>/dev/null || stat -f '%u' "$SOURCE_HELPER")"
[ "$SOURCE_HELPER_OWNER" -ne 0 ] || fail 'source helper test fixture is not a normal non-root checkout'
: >"$SYSTEMCTL_CALLS"
: >"$INSTALL_CALLS"

cat >"${FAKE_BIN}/id" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
  -u|-g) printf '0\n' ;;
  *) exit 1 ;;
esac
EOF
chmod +x "${FAKE_BIN}/id"

cat >"${FAKE_BIN}/stat" <<'EOF'
#!/usr/bin/env bash

format="${2:-}"
path="${3:-}"
is_staging_path=false
is_source_path=false
case "$path" in
  "${FAKE_HELPER_STAGE_PREFIX}"*) is_staging_path=true ;;
esac
case "$path" in
  "$FAKE_SOURCE_ROOT"|"${FAKE_SOURCE_ROOT}"/*) is_source_path=true ;;
esac
case "$format" in
  '%u')
    if [ "$is_staging_path" = true ]; then
      printf '0\n'
    elif [ "$is_source_path" = true ]; then
      exec "$REAL_STAT" "$@"
    else
      printf '0\n'
    fi
    ;;
  '%a'|'%Lp')
    if [ "$is_staging_path" = true ]; then
      case "$path" in
        */sub2api-maintenance-lock.sh) printf '600\n' ;;
        *) printf '700\n' ;;
      esac
    elif [ "$is_source_path" = true ]; then
      exec "$REAL_STAT" "$@"
    else
      printf '755\n'
    fi
    ;;
  '%u:%g:%a:%h:%d:%i')
    if [ -n "${FAKE_WRITABLE_PARENT_CONTAINER:-}" ] \
      && [ "$path" = "$FAKE_WRITABLE_PARENT_CONTAINER" ]; then
      printf '0:0:1777:1:1:1\n'
    elif [ "$path" = "${SUB2API_MAINTENANCE_LOCK_FILE:-}" ]; then
      printf '0:0:600:1:1:1\n'
    elif [ "$path" = "${SUB2API_MAINTENANCE_LOCK_FILE%/*}" ]; then
      printf '0:0:700:1:1:1\n'
    elif [[ "$path" == */runtime ]]; then
      printf '0:0:700:1:1:1\n'
    else
      printf '0:0:755:1:1:1\n'
    fi
    ;;
  *) exec "$REAL_STAT" "$@" ;;
esac
EOF
chmod +x "${FAKE_BIN}/stat"

cat >"${FAKE_BIN}/sha256sum" <<'EOF'
#!/usr/bin/env bash

exec shasum -a 256 "$@"
EOF
chmod +x "${FAKE_BIN}/sha256sum"

cat >"${FAKE_BIN}/systemctl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_SYSTEMCTL_CALLS"
if [ "${FAKE_SYSTEMCTL_SPAWN_LOCK_DESCENDANT:-0}" = 1 ] \
  && [ ! -e "${FAKE_LOCK_DESCENDANT_READY:?}" ]; then
  # This deliberately long-lived external descendant models a systemctl
  # child that outlives the installer.  It keeps any inherited descriptors
  # open, reports them, then attempts a forged fenced re-entry only after
  # the supervisor has returned to the test.
  (
    for descriptor in 6 7 8 9; do
      if python3 - "$descriptor" <<'PY'
import os
import sys

try:
    os.fstat(int(sys.argv[1]))
except OSError:
    raise SystemExit(1)
PY
      then
        printf 'fd%s=present\n' "$descriptor"
      else
        printf 'fd%s=absent\n' "$descriptor"
      fi
    done
    if [ -n "${SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_READY+x}" ] \
      || [ -n "${SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_SUPERVISED+x}" ] \
      || [ -n "${SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_TOKEN+x}" ] \
      || [ -n "${SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_LEGACY+x}" ] \
      || [ -n "${SUB2API_AUTODEPLOY_EXEC_SOURCE_ROOT+x}" ] \
      || [ -n "${INSTALLER_FENCE_READY+x}" ] \
      || [ -n "${INSTALLER_FENCE_TOKEN+x}" ] \
      || [ -n "${INSTALLER_FENCE_SOURCE_ROOT+x}" ] \
      || [ -n "${INSTALLER_FENCE_SUPERVISED+x}" ] \
      || [ -n "${INSTALLER_FENCE_LEGACY+x}" ] \
      || [ -n "${INSTALLER_FENCE_VERIFIED+x}" ] \
      || [ -n "${VERIFIED_INSTALLER_FENCE_LEGACY+x}" ]; then
      printf 'fence_env=present\n'
    else
      printf 'fence_env=absent\n'
    fi
    while [ ! -e "${FAKE_LOCK_DESCENDANT_REENTRY_GO:?}" ]; do
      sleep 0.01
    done
    if env \
      SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_READY=1 \
      SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_SUPERVISED=1 \
      SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_TOKEN=forged \
      SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_LEGACY="${FAKE_LOCK_DESCENDANT_LEGACY:?}" \
      SUB2API_AUTODEPLOY_EXEC_SOURCE_ROOT="${FAKE_SOURCE_ROOT:?}" \
      /bin/bash /dev/fd/6 >"${FAKE_LOCK_DESCENDANT_REENTRY_OUT:?}" 2>&1; then
      printf 'reentry_status=0\n'
    else
      printf 'reentry_status=%s\n' "$?"
    fi
    # Preserve the original background PID across the long-lived process so
    # the test cleanup kills the actual descendant, not merely its shell.
    exec sleep 30
  ) >"${FAKE_LOCK_DESCENDANT_REPORT:?}" 2>&1 &
  printf '%s\n' "$!" >"$FAKE_LOCK_DESCENDANT_PID_FILE"
  : >"$FAKE_LOCK_DESCENDANT_READY"
fi
EOF
chmod +x "${FAKE_BIN}/systemctl"

cat >"${FAKE_BIN}/install" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
if [ -n "${FAKE_INSTALL_CALLS:-}" ]; then
  printf '%s\n' "$*" >>"$FAKE_INSTALL_CALLS"
fi
if [ "${FAKE_EXPECT_MAINTENANCE_FENCES:-0}" = 1 ] \
  && [ ! -e "${FAKE_MAINTENANCE_FENCES_CHECKED:?}" ]; then
  python3 - "${SUB2API_MAINTENANCE_LOCK_FILE:?}" "${FAKE_LEGACY_MAINTENANCE_LOCK_FILE:-}" <<'PY'
import fcntl
import os
import sys

for path in sys.argv[1:]:
    if not path:
        continue
    descriptor = os.open(path, os.O_RDWR)
    try:
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            continue
        raise SystemExit(f"maintenance fence was not held: {path}")
    finally:
        os.close(descriptor)
PY
  : >"$FAKE_MAINTENANCE_FENCES_CHECKED"
fi
create_parent=false
directory_mode=false
mode=""
args=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    -D) create_parent=true ;;
    -d) directory_mode=true ;;
    -o|-g) shift ;;
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

NORMAL_FENCE_CHECKED="${TEST_ROOT}/normal-install-fence-checked"
rm -f -- "$NORMAL_FENCE_CHECKED"

(
  cd "$SOURCE_ROOT"
  env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_SYSTEMCTL_CALLS="$SYSTEMCTL_CALLS" \
    FAKE_INSTALL_CALLS="$INSTALL_CALLS" \
    FAKE_EXPECT_MAINTENANCE_FENCES=1 \
    FAKE_MAINTENANCE_FENCES_CHECKED="$NORMAL_FENCE_CHECKED" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_CONFIG_FILE="$CONFIG_FILE" \
    SUB2API_AUTODEPLOY_UNIT_DIR="$UNIT_DIR" \
    SUB2API_MAINTENANCE_LOCK_FILE="$MAINTENANCE_LOCK_FILE" \
    SUB2API_RUNTIME_GUARD_EXECUTABLE="${TEST_ROOT}/libexec/sub2api-runtime-guard.sh" \
    /bin/bash deploy/install-autodeploy.sh \
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
      --no-enable
) >"${TEST_ROOT}/output.log"

[ -e "$NORMAL_FENCE_CHECKED" ] \
  || fail 'ordinary installation did not retain the canonical maintenance fence into installation'

assert_contains "$CONFIG_FILE" 'SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE=external'
assert_contains "$CONFIG_FILE" 'SUB2API_RUNTIME_GUARD_NETWORK=candidate-network'
assert_contains "$CONFIG_FILE" 'SUB2API_RUNTIME_GUARD_DATA_VOLUME=candidate-data'
assert_contains "$CONFIG_FILE" 'SUB2API_CADDY_CONTAINER=candidate-caddy'
assert_contains "$CONFIG_FILE" 'SUB2API_EXTERNAL_RUNTIME_ENV_FILE=/etc/sub2api-external-runtime.env'
assert_contains "$CONFIG_FILE" 'SUB2API_EXTERNAL_CA_FILE=/opt/sub2api/db-host-ca/ca.crt'
assert_contains "$CONFIG_FILE" 'SUB2API_DUAL_NODE_RUNTIME_ENABLED=true'
assert_contains "$CONFIG_FILE" 'SUB2API_RELEASE_BACKGROUND_MODE=activate'
assert_contains "$CONFIG_FILE" "SUB2API_MAINTENANCE_LOCK_FILE=${MAINTENANCE_LOCK_FILE}"
[ -x "${APP_DIR}/scripts/sub2api-maintenance-lock.sh" ] \
  || fail 'script-directory maintenance lock helper was not installed'
[ -x "${TEST_ROOT}/libexec/sub2api-maintenance-lock.sh" ] \
  || fail 'runtime-guard sibling maintenance lock helper was not installed'
[ -d "${MAINTENANCE_LOCK_FILE%/*}" ] \
  || fail 'private maintenance lock directory was not installed'
grep -Fqx -- 'disable --now sub2api-runtime-guard.timer' "$SYSTEMCTL_CALLS" \
  || fail 'runtime guard timer was not explicitly left disabled'
if grep -Fq -- 'enable --now sub2api-runtime-guard.timer' "$SYSTEMCTL_CALLS"; then
  fail 'runtime guard timer was enabled during staged external-runtime installation'
fi
grep -Fqx -- 'disable --now sub2api-autodeploy.timer' "$SYSTEMCTL_CALLS" \
  || fail 'polling timer was not left disabled'
if find "$TEST_ROOT" -maxdepth 1 -name 'sub2api-maintenance-helper.*' -print -quit | grep -q .; then
  fail 'root-only staged maintenance helper was not removed after source'
fi

# Explicit runtime-mode options must never be silently ignored when a config
# already exists. The operator must opt into a full, auditable replacement.
config_checksum_before="$(cksum "$CONFIG_FILE")"
: >"$SYSTEMCTL_CALLS"
if env \
  PATH="${FAKE_BIN}:${PATH}" \
  FAKE_SYSTEMCTL_CALLS="$SYSTEMCTL_CALLS" \
  FAKE_INSTALL_CALLS="$INSTALL_CALLS" \
  SUB2API_APP_DIR="$APP_DIR" \
  SUB2API_AUTODEPLOY_CONFIG_FILE="$CONFIG_FILE" \
  SUB2API_AUTODEPLOY_UNIT_DIR="$UNIT_DIR" \
  SUB2API_MAINTENANCE_LOCK_FILE="$MAINTENANCE_LOCK_FILE" \
  SUB2API_RUNTIME_GUARD_EXECUTABLE="${TEST_ROOT}/libexec/sub2api-runtime-guard.sh" \
  /bin/bash "$SCRIPT" \
    --production-branch main \
    --production-repo https://github.com/Turtle-Li/sub2api.git \
    --upstream-repo https://github.com/Wei-Shaw/sub2api.git \
    --dependency-mode external \
    --external-runtime-env-file /etc/sub2api-external-runtime.env \
    --external-ca-file /opt/sub2api/db-host-ca/ca.crt \
    --dual-node-runtime-enabled true \
    --no-enable-runtime-guard \
    --no-enable \
    >"${TEST_ROOT}/existing-config.out" 2>&1; then
  fail 'existing config silently accepted explicit runtime-mode options without --replace-config'
fi
grep -Fq -- 'runtime configuration options require --replace-config' "${TEST_ROOT}/existing-config.out" \
  || fail 'existing-config failure did not explain the required replacement gate'
[ "$(cksum "$CONFIG_FILE")" = "$config_checksum_before" ] \
  || fail 'rejected runtime-mode migration changed the existing config'
[ ! -s "$SYSTEMCTL_CALLS" ] \
  || fail 'rejected runtime-mode migration touched systemd'

# A legacy /run/lock value must fail before copying scripts or touching
# systemd.  Operators must explicitly replace the configuration so the new
# root-owned private parent is created with the intended path.
LEGACY_CONFIG_FILE="${TEST_ROOT}/etc/sub2api-autodeploy-legacy-lock.env"
printf 'SUB2API_APP_DIR=%s\nSUB2API_MAINTENANCE_LOCK_FILE=/run/lock/sub2api-maintenance.lock\n' \
  "$APP_DIR" >"$LEGACY_CONFIG_FILE"
: >"$SYSTEMCTL_CALLS"
if env \
  PATH="${FAKE_BIN}:${PATH}" \
  FAKE_SYSTEMCTL_CALLS="$SYSTEMCTL_CALLS" \
  FAKE_INSTALL_CALLS="$INSTALL_CALLS" \
  SUB2API_APP_DIR="$APP_DIR" \
  SUB2API_AUTODEPLOY_CONFIG_FILE="$LEGACY_CONFIG_FILE" \
  SUB2API_AUTODEPLOY_UNIT_DIR="$UNIT_DIR" \
  SUB2API_MAINTENANCE_LOCK_FILE="$MAINTENANCE_LOCK_FILE" \
  SUB2API_RUNTIME_GUARD_EXECUTABLE="${TEST_ROOT}/libexec/sub2api-runtime-guard.sh" \
  /bin/bash "$SCRIPT" \
    --production-branch main \
    --production-repo https://github.com/Turtle-Li/sub2api.git \
    --upstream-repo https://github.com/Wei-Shaw/sub2api.git \
    --no-enable-runtime-guard \
    --no-enable \
    >"${TEST_ROOT}/legacy-lock-config.out" 2>&1; then
  fail 'legacy /run/lock maintenance configuration was accepted'
fi
grep -Fq -- 'existing maintenance lock uses retired /run/lock/sub2api-maintenance.lock' \
  "${TEST_ROOT}/legacy-lock-config.out" \
  || fail 'legacy maintenance-lock migration did not explain the replacement gate'
[ ! -s "$SYSTEMCTL_CALLS" ] \
  || fail 'legacy maintenance-lock migration touched systemd'

# Lock target validation is pure: neither dot components nor a symlink parent
# may reach install -d and alter an unrelated directory's permissions.
SENSITIVE_DIR="${TEST_ROOT}/sensitive"
mkdir -m 755 "$SENSITIVE_DIR"
SENSITIVE_MODE="$(file_mode "$SENSITIVE_DIR")"
assert_unsafe_maintenance_target() {
  local label="$1" lock_path="$2" expected="$3" parent_container="${4:-}" output checksum

  output="${TEST_ROOT}/${label}-maintenance-lock.out"
  checksum="$(cksum "$CONFIG_FILE")"
  : >"$SYSTEMCTL_CALLS"
  : >"$INSTALL_CALLS"
  if env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_SYSTEMCTL_CALLS="$SYSTEMCTL_CALLS" \
    FAKE_INSTALL_CALLS="$INSTALL_CALLS" \
    FAKE_WRITABLE_PARENT_CONTAINER="$parent_container" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_CONFIG_FILE="$CONFIG_FILE" \
    SUB2API_AUTODEPLOY_UNIT_DIR="$UNIT_DIR" \
    SUB2API_MAINTENANCE_LOCK_FILE="$lock_path" \
    SUB2API_RUNTIME_GUARD_EXECUTABLE="${TEST_ROOT}/libexec/sub2api-runtime-guard.sh" \
    /bin/bash "$SCRIPT" \
      --production-branch main \
      --production-repo https://github.com/Turtle-Li/sub2api.git \
      --upstream-repo https://github.com/Wei-Shaw/sub2api.git \
      --no-enable-runtime-guard \
      --no-enable \
      >"$output" 2>&1; then
    fail "${label} maintenance lock target was accepted"
  fi
  grep -Fq -- "$expected" "$output" \
    || { sed -n '1,160p' "$output" >&2; fail "${label} failure did not explain the unsafe target"; }
  [ ! -s "$INSTALL_CALLS" ] || fail "${label} reached install before target validation"
  [ ! -s "$SYSTEMCTL_CALLS" ] || fail "${label} touched systemd before target validation"
  [ "$(cksum "$CONFIG_FILE")" = "$checksum" ] || fail "${label} changed the existing config"
  [ "$(file_mode "$SENSITIVE_DIR")" = "$SENSITIVE_MODE" ] \
    || fail "${label} changed the sensitive directory mode"
}

WRITABLE_CONTAINER="${TEST_ROOT}/writable-container"
mkdir -m 700 "$WRITABLE_CONTAINER"
chmod 1777 "$WRITABLE_CONTAINER"
assert_unsafe_maintenance_target \
  writable-container "${WRITABLE_CONTAINER}/missing-private/maintenance.lock" \
  'maintenance lock parent container is group/world-writable' "$WRITABLE_CONTAINER"
assert_unsafe_maintenance_target \
  doubled-separator "${TEST_ROOT}//doubled/maintenance.lock" \
  'maintenance lock path contains an empty component'
assert_unsafe_maintenance_target \
  dotdot "${TEST_ROOT}/guard-parent/../sensitive/maintenance.lock" \
  'maintenance lock path must not contain . or .. components'
SYMLINK_PARENT="${TEST_ROOT}/maintenance-link"
ln -s "$SENSITIVE_DIR" "$SYMLINK_PARENT"
assert_unsafe_maintenance_target \
  symlink-parent "${SYMLINK_PARENT}/maintenance.lock" \
  'maintenance lock parent is a symlink'

# sudo may execute an installer from a normal user's checkout. The helper
# must share that source-file owner and be non-writable to group/other; it is
# deliberately not required to be root-owned until it is installed.
config_checksum_before="$(cksum "$CONFIG_FILE")"
: >"$SYSTEMCTL_CALLS"
: >"$INSTALL_CALLS"
chmod 664 "$SOURCE_HELPER"
if env \
  PATH="${FAKE_BIN}:${PATH}" \
  FAKE_SYSTEMCTL_CALLS="$SYSTEMCTL_CALLS" \
  FAKE_INSTALL_CALLS="$INSTALL_CALLS" \
  SUB2API_APP_DIR="$APP_DIR" \
  SUB2API_AUTODEPLOY_CONFIG_FILE="$CONFIG_FILE" \
  SUB2API_AUTODEPLOY_UNIT_DIR="$UNIT_DIR" \
  SUB2API_MAINTENANCE_LOCK_FILE="$MAINTENANCE_LOCK_FILE" \
  SUB2API_RUNTIME_GUARD_EXECUTABLE="${TEST_ROOT}/libexec/sub2api-runtime-guard.sh" \
  /bin/bash "$SCRIPT" \
    --production-branch main \
    --production-repo https://github.com/Turtle-Li/sub2api.git \
    --upstream-repo https://github.com/Wei-Shaw/sub2api.git \
    --no-enable-runtime-guard \
    --no-enable \
    >"${TEST_ROOT}/group-writable-helper.out" 2>&1; then
  fail 'group-writable source helper was accepted'
fi
grep -Fq -- 'maintenance lock helper must not be group/other writable' \
  "${TEST_ROOT}/group-writable-helper.out" \
  || fail 'group-writable source helper failure did not explain the source-trust boundary'
[ ! -s "$INSTALL_CALLS" ] || fail 'group-writable source helper reached install'
[ ! -s "$SYSTEMCTL_CALLS" ] || fail 'group-writable source helper touched systemd'
[ "$(cksum "$CONFIG_FILE")" = "$config_checksum_before" ] \
  || fail 'group-writable source helper changed the existing config'

chmod 644 "$SOURCE_HELPER"
SOURCE_DEPLOY_DIR="${SOURCE_ROOT}/deploy"
SOURCE_DEPLOY_MODE="$(file_mode "$SOURCE_DEPLOY_DIR")"
config_checksum_before="$(cksum "$CONFIG_FILE")"
: >"$SYSTEMCTL_CALLS"
: >"$INSTALL_CALLS"
chmod g+w "$SOURCE_DEPLOY_DIR"
if env \
  PATH="${FAKE_BIN}:${PATH}" \
  FAKE_SYSTEMCTL_CALLS="$SYSTEMCTL_CALLS" \
  FAKE_INSTALL_CALLS="$INSTALL_CALLS" \
  SUB2API_APP_DIR="$APP_DIR" \
  SUB2API_AUTODEPLOY_CONFIG_FILE="$CONFIG_FILE" \
  SUB2API_AUTODEPLOY_UNIT_DIR="$UNIT_DIR" \
  SUB2API_MAINTENANCE_LOCK_FILE="$MAINTENANCE_LOCK_FILE" \
  SUB2API_RUNTIME_GUARD_EXECUTABLE="${TEST_ROOT}/libexec/sub2api-runtime-guard.sh" \
  /bin/bash "$SCRIPT" \
    --production-branch main \
    --production-repo https://github.com/Turtle-Li/sub2api.git \
    --upstream-repo https://github.com/Wei-Shaw/sub2api.git \
    --no-enable-runtime-guard \
    --no-enable \
    >"${TEST_ROOT}/group-writable-helper-parent.out" 2>&1; then
  chmod "$SOURCE_DEPLOY_MODE" "$SOURCE_DEPLOY_DIR"
  fail 'group-writable source helper ancestor was accepted'
fi
chmod "$SOURCE_DEPLOY_MODE" "$SOURCE_DEPLOY_DIR"
grep -Fq -- 'ancestor is group/other writable' "${TEST_ROOT}/group-writable-helper-parent.out" \
  || fail 'group-writable source helper ancestor failure did not explain the source-trust boundary'
[ ! -s "$INSTALL_CALLS" ] || fail 'group-writable source helper ancestor reached install'
[ ! -s "$SYSTEMCTL_CALLS" ] || fail 'group-writable source helper ancestor touched systemd'
[ "$(cksum "$CONFIG_FILE")" = "$config_checksum_before" ] \
  || fail 'group-writable source helper ancestor changed the existing config'

# A root process must never source the checkout pathname after initial source
# metadata validation. The test-only barrier fires only after Python opened the
# source FD with O_NOFOLLOW; both an atomic regular-file replacement and an
# atomic symlink replacement must fail the final lstat-to-FD binding before
# install, systemd, or configuration mutation.
restore_source_helper() {
  rm -f -- "$SOURCE_HELPER"
  cp "${DEPLOY_DIR}/sub2api-maintenance-lock.sh" "$SOURCE_HELPER"
  chmod 644 "$SOURCE_HELPER"
  if [ "$(id -u)" -eq 0 ]; then
    chown 12345 "$SOURCE_HELPER"
  fi
}

assert_source_replacement_barrier() {
  local label="$1" replacement_kind="$2"
  local barrier="${TEST_ROOT}/${label}-source-open"
  local output="${TEST_ROOT}/${label}-source-replacement.out"
  local replacement_path="${SOURCE_HELPER}.${label}.replacement"
  local malicious_target="${TEST_ROOT}/${label}-malicious-helper"
  local checksum installer_status replacement_pid

  restore_source_helper
  rm -f -- "$barrier" "${barrier}.continue" "$replacement_path" "$malicious_target"
  checksum="$(cksum "$CONFIG_FILE")"
  : >"$SYSTEMCTL_CALLS"
  : >"$INSTALL_CALLS"
  (
    local attempt
    for ((attempt = 0; attempt < 1000; attempt += 1)); do
      [ -e "$barrier" ] && break
      sleep 0.01
    done
    [ -e "$barrier" ] || exit 1
    case "$replacement_kind" in
      regular)
        printf '#!/usr/bin/env bash\nexit 99\n' >"$replacement_path"
        chmod 644 "$replacement_path"
        ;;
      symlink)
        printf '#!/usr/bin/env bash\nexit 99\n' >"$malicious_target"
        chmod 644 "$malicious_target"
        ln -s "$malicious_target" "$replacement_path"
        ;;
      *) exit 2 ;;
    esac
    mv -f "$replacement_path" "$SOURCE_HELPER"
    : >"${barrier}.continue"
  ) &
  replacement_pid=$!

  if env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_SYSTEMCTL_CALLS="$SYSTEMCTL_CALLS" \
    FAKE_INSTALL_CALLS="$INSTALL_CALLS" \
    SUB2API_MAINTENANCE_LOCK_TEST_AFTER_SOURCE_OPEN_BARRIER="$barrier" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_CONFIG_FILE="$CONFIG_FILE" \
    SUB2API_AUTODEPLOY_UNIT_DIR="$UNIT_DIR" \
    SUB2API_MAINTENANCE_LOCK_FILE="$MAINTENANCE_LOCK_FILE" \
    SUB2API_RUNTIME_GUARD_EXECUTABLE="${TEST_ROOT}/libexec/sub2api-runtime-guard.sh" \
    /bin/bash "$SCRIPT" \
      --production-branch main \
      --production-repo https://github.com/Turtle-Li/sub2api.git \
      --upstream-repo https://github.com/Wei-Shaw/sub2api.git \
      --no-enable-runtime-guard \
      --no-enable \
      >"$output" 2>&1; then
    installer_status=0
  else
    installer_status=$?
  fi
  if ! wait "$replacement_pid"; then
    fail "${label} source replacement did not reach the post-open barrier"
  fi
  rm -f -- "$barrier" "${barrier}.continue"
  [ "$installer_status" -ne 0 ] || fail "${label} source replacement during staging was accepted"
  grep -Fq -- 'source path changed while staging' "$output" \
    || { sed -n '1,160p' "$output" >&2; fail "${label} source replacement did not fail its FD/path binding"; }
  [ ! -s "$INSTALL_CALLS" ] || fail "${label} source replacement reached install"
  [ ! -s "$SYSTEMCTL_CALLS" ] || fail "${label} source replacement touched systemd"
  [ "$(cksum "$CONFIG_FILE")" = "$checksum" ] \
    || fail "${label} source replacement changed the existing config"
  if find "$TEST_ROOT" -maxdepth 1 -name 'sub2api-maintenance-helper.*' -print -quit | grep -q .; then
    fail "${label} failed helper staging left a private helper directory behind"
  fi
}

assert_source_replacement_barrier regular-replacement regular
assert_source_replacement_barrier symlink-replacement symlink

# The installer itself is also read from a normal-user checkout. Once the
# wrapper opens that source FD, a replacement or in-place write must never
# affect the root re-exec: it copies the FD to an unlinked private snapshot and
# binds the checkout pathname again before executing it.
restore_installer_source() {
  rm -f -- "$SCRIPT"
  cp "${DEPLOY_DIR}/install-autodeploy.sh" "$SCRIPT"
  chmod 755 "$SCRIPT"
  if [ "$(id -u)" -eq 0 ]; then
    chown 12345 "$SCRIPT"
  fi
}

assert_installer_source_barrier() {
  local label="$1" mutation_kind="$2"
  local barrier="${TEST_ROOT}/${label}-installer-source-open"
  local output="${TEST_ROOT}/${label}-installer-source.out"
  local replacement="${SCRIPT}.${label}.replacement"
  local config_file="${TEST_ROOT}/etc/${label}-installer-source.env"
  local lock_file="${TEST_ROOT}/${label}-locks/maintenance.lock"
  local checksum installer_status mutation_pid

  restore_source_helper
  restore_installer_source
  mkdir -m 700 "${lock_file%/*}"
  printf 'SUB2API_APP_DIR=%s\nSUB2API_MAINTENANCE_LOCK_FILE=%s\n' "$APP_DIR" "$lock_file" >"$config_file"
  rm -f -- "$barrier" "${barrier}.continue" "$replacement"
  checksum="$(cksum "$config_file")"
  : >"$SYSTEMCTL_CALLS"
  : >"$INSTALL_CALLS"
  (
    local attempt
    for ((attempt = 0; attempt < 1000; attempt += 1)); do
      [ -e "$barrier" ] && break
      sleep 0.01
    done
    [ -e "$barrier" ] || exit 1
    case "$mutation_kind" in
      regular)
        printf '#!/usr/bin/env bash\nexit 99\n' >"$replacement"
        chmod 755 "$replacement"
        if [ "$(id -u)" -eq 0 ]; then
          chown 12345 "$replacement"
        fi
        mv -f "$replacement" "$SCRIPT"
        ;;
      inplace)
        printf '\n# deterministic installer-source mutation\n' >>"$SCRIPT"
        ;;
      *) exit 2 ;;
    esac
    : >"${barrier}.continue"
  ) &
  mutation_pid=$!

  if env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_SYSTEMCTL_CALLS="$SYSTEMCTL_CALLS" \
    FAKE_INSTALL_CALLS="$INSTALL_CALLS" \
    SUB2API_MAINTENANCE_LOCK_TEST_AFTER_INSTALLER_SOURCE_OPEN_BARRIER="$barrier" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_CONFIG_FILE="$config_file" \
    SUB2API_AUTODEPLOY_UNIT_DIR="$UNIT_DIR" \
    SUB2API_MAINTENANCE_LOCK_FILE="$lock_file" \
    SUB2API_RUNTIME_GUARD_EXECUTABLE="${TEST_ROOT}/libexec/sub2api-runtime-guard.sh" \
    /bin/bash "$SCRIPT" \
      --production-branch main \
      --production-repo https://github.com/Turtle-Li/sub2api.git \
      --upstream-repo https://github.com/Wei-Shaw/sub2api.git \
      --no-enable-runtime-guard \
      --no-enable \
      >"$output" 2>&1; then
    installer_status=0
  else
    installer_status=$?
  fi
  if ! wait "$mutation_pid"; then
    restore_installer_source
    fail "${label} installer-source mutation did not reach the post-open barrier"
  fi
  rm -f -- "$barrier" "${barrier}.continue"
  [ "$installer_status" -ne 0 ] || { restore_installer_source; fail "${label} installer-source mutation was accepted"; }
  case "$mutation_kind" in
    regular) expected='installer source path changed while staging' ;;
    inplace) expected='installer source changed while staging' ;;
  esac
  grep -Fq -- "$expected" "$output" \
    || { sed -n '1,180p' "$output" >&2; restore_installer_source; fail "${label} did not fail its installer FD/path binding"; }
  [ ! -s "$INSTALL_CALLS" ] || { restore_installer_source; fail "${label} reached install"; }
  [ ! -s "$SYSTEMCTL_CALLS" ] || { restore_installer_source; fail "${label} touched systemd"; }
  [ "$(cksum "$config_file")" = "$checksum" ] \
    || { restore_installer_source; fail "${label} changed the configuration"; }
  if find "${lock_file%/*}" -name '.installer-source-*' -print -quit | grep -q .; then
    restore_installer_source
    fail "${label} left a private staged installer source behind"
  fi
  restore_installer_source
}

assert_installer_source_barrier installer-regular-replacement regular
assert_installer_source_barrier installer-inplace-mutation inplace

# Two independently safe test paths must still never become two maintenance
# domains. Retained-config drift is rejected before the fence can create the
# caller's private parent or lock; outside the explicit test switch, either
# path is rejected because production accepts only the canonical pathname.
CROSS_LOCK_A="${TEST_ROOT}/cross-lock-a/private/maintenance.lock"
CROSS_LOCK_B="${TEST_ROOT}/cross-lock-b/private/maintenance.lock"
CROSS_CONFIG="${TEST_ROOT}/etc/cross-lock-config.env"
mkdir -m 700 "${TEST_ROOT}/cross-lock-a" "${TEST_ROOT}/cross-lock-b"
printf 'SUB2API_APP_DIR=%s\nSUB2API_MAINTENANCE_LOCK_FILE=%s\n' "$APP_DIR" "$CROSS_LOCK_B" >"$CROSS_CONFIG"
cross_checksum="$(cksum "$CROSS_CONFIG")"
: >"$SYSTEMCTL_CALLS"
: >"$INSTALL_CALLS"
if env \
  PATH="${FAKE_BIN}:${PATH}" \
  FAKE_SYSTEMCTL_CALLS="$SYSTEMCTL_CALLS" \
  FAKE_INSTALL_CALLS="$INSTALL_CALLS" \
  SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1 \
  SUB2API_APP_DIR="$APP_DIR" \
  SUB2API_AUTODEPLOY_CONFIG_FILE="$CROSS_CONFIG" \
  SUB2API_AUTODEPLOY_UNIT_DIR="$UNIT_DIR" \
  SUB2API_MAINTENANCE_LOCK_FILE="$CROSS_LOCK_A" \
  SUB2API_RUNTIME_GUARD_EXECUTABLE="${TEST_ROOT}/libexec/sub2api-runtime-guard.sh" \
  /bin/bash "$SCRIPT" \
    --production-branch main \
    --production-repo https://github.com/Turtle-Li/sub2api.git \
    --upstream-repo https://github.com/Wei-Shaw/sub2api.git \
    --no-enable-runtime-guard \
    --no-enable \
    >"${TEST_ROOT}/cross-lock-drift.out" 2>&1; then
  fail 'two safe retained maintenance-lock paths were accepted'
fi
grep -Fq -- "existing SUB2API_MAINTENANCE_LOCK_FILE must equal ${CROSS_LOCK_A}" \
  "${TEST_ROOT}/cross-lock-drift.out" \
  || { sed -n '1,160p' "${TEST_ROOT}/cross-lock-drift.out" >&2; fail 'cross-lock drift did not explain the binding'; }
[ ! -e "${CROSS_LOCK_A%/*}" ] || fail 'cross-lock drift created the requested lock parent before rejection'
[ ! -e "${CROSS_LOCK_B%/*}" ] || fail 'cross-lock drift created the retained lock parent before rejection'
[ ! -s "$INSTALL_CALLS" ] || fail 'cross-lock drift reached installation'
[ ! -s "$SYSTEMCTL_CALLS" ] || fail 'cross-lock drift touched systemd'
[ "$(cksum "$CROSS_CONFIG")" = "$cross_checksum" ] || fail 'cross-lock drift changed the configuration'

: >"$SYSTEMCTL_CALLS"
: >"$INSTALL_CALLS"
if env \
  PATH="${FAKE_BIN}:${PATH}" \
  FAKE_SYSTEMCTL_CALLS="$SYSTEMCTL_CALLS" \
  FAKE_INSTALL_CALLS="$INSTALL_CALLS" \
  SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=0 \
  SUB2API_APP_DIR="$APP_DIR" \
  SUB2API_AUTODEPLOY_CONFIG_FILE="$CROSS_CONFIG" \
  SUB2API_AUTODEPLOY_UNIT_DIR="$UNIT_DIR" \
  SUB2API_MAINTENANCE_LOCK_FILE="$CROSS_LOCK_A" \
  SUB2API_RUNTIME_GUARD_EXECUTABLE="${TEST_ROOT}/libexec/sub2api-runtime-guard.sh" \
  /bin/bash "$SCRIPT" \
    --production-branch main \
    --production-repo https://github.com/Turtle-Li/sub2api.git \
    --upstream-repo https://github.com/Wei-Shaw/sub2api.git \
    --no-enable-runtime-guard \
    --no-enable \
    >"${TEST_ROOT}/production-noncanonical-lock.out" 2>&1; then
  fail 'production noncanonical maintenance-lock path was accepted'
fi
grep -Fq -- 'maintenance lock path must be the canonical /run/sub2api-maintenance/sub2api-maintenance.lock' \
  "${TEST_ROOT}/production-noncanonical-lock.out" \
  || { sed -n '1,160p' "${TEST_ROOT}/production-noncanonical-lock.out" >&2; fail 'production noncanonical lock failure was unclear'; }
[ ! -e "${CROSS_LOCK_A%/*}" ] || fail 'production noncanonical lock created a parent before rejection'
[ ! -e "${CROSS_LOCK_B%/*}" ] || fail 'production noncanonical retained path created a parent before rejection'
[ ! -s "$INSTALL_CALLS" ] || fail 'production noncanonical lock reached installation'
[ ! -s "$SYSTEMCTL_CALLS" ] || fail 'production noncanonical lock touched systemd'

# A --replace-config migration from the exact retired lock path must fence
# both the old sticky-parent lock and the canonical private lock before any
# configuration, script, or systemd mutation. A legitimate old lock holder is
# contention (not an unsafe-path error), and therefore leaves all deployment
# state untouched.
LEGACY_MIGRATION_LOCK="${TEST_ROOT}/legacy/sub2api-maintenance.lock"
LEGACY_MIGRATION_CONFIG="${TEST_ROOT}/etc/sub2api-autodeploy-legacy-migration.env"
mkdir -m 700 "${LEGACY_MIGRATION_LOCK%/*}"
printf 'SUB2API_APP_DIR=%s\nSUB2API_MAINTENANCE_LOCK_FILE=%s\n' \
  "$APP_DIR" "$LEGACY_MIGRATION_LOCK" >"$LEGACY_MIGRATION_CONFIG"
LEGACY_HOLDER_READY="${TEST_ROOT}/legacy-holder-ready"
LEGACY_HOLDER_RELEASE="${TEST_ROOT}/legacy-holder-release"
python3 - "$LEGACY_MIGRATION_LOCK" "$LEGACY_HOLDER_READY" "$LEGACY_HOLDER_RELEASE" <<'PY' &
import fcntl
from pathlib import Path
import sys
import time

lock_path, ready_path, release_path = sys.argv[1:]
with open(lock_path, "a", encoding="utf-8") as handle:
    fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
    Path(ready_path).touch()
    while not Path(release_path).exists():
        time.sleep(0.01)
PY
LEGACY_HOLDER_PID=$!
for _ in $(seq 1 200); do
  [ -e "$LEGACY_HOLDER_READY" ] && break
  sleep 0.01
done
[ -e "$LEGACY_HOLDER_READY" ] || fail 'legacy migration lock holder did not become ready'
legacy_checksum_before="$(cksum "$LEGACY_MIGRATION_CONFIG")"
: >"$SYSTEMCTL_CALLS"
: >"$INSTALL_CALLS"
if env \
  PATH="${FAKE_BIN}:${PATH}" \
  FAKE_SYSTEMCTL_CALLS="$SYSTEMCTL_CALLS" \
  FAKE_INSTALL_CALLS="$INSTALL_CALLS" \
  SUB2API_APP_DIR="$APP_DIR" \
  SUB2API_AUTODEPLOY_CONFIG_FILE="$LEGACY_MIGRATION_CONFIG" \
  SUB2API_AUTODEPLOY_UNIT_DIR="$UNIT_DIR" \
  SUB2API_MAINTENANCE_LOCK_FILE="$MAINTENANCE_LOCK_FILE" \
  SUB2API_MAINTENANCE_LEGACY_LOCK_FILE_FOR_TESTS="$LEGACY_MIGRATION_LOCK" \
  SUB2API_RUNTIME_GUARD_EXECUTABLE="${TEST_ROOT}/libexec/sub2api-runtime-guard.sh" \
  /bin/bash "$SCRIPT" \
    --production-branch main \
    --production-repo https://github.com/Turtle-Li/sub2api.git \
    --upstream-repo https://github.com/Wei-Shaw/sub2api.git \
    --replace-config \
    --no-enable-runtime-guard \
    --no-enable \
    >"${TEST_ROOT}/legacy-holder.out" 2>&1; then
  : >"$LEGACY_HOLDER_RELEASE"
  wait "$LEGACY_HOLDER_PID"
  fail 'active legacy lock holder was accepted during --replace-config migration'
fi
: >"$LEGACY_HOLDER_RELEASE"
wait "$LEGACY_HOLDER_PID"
grep -Fq -- "maintenance lock install fence is busy: ${LEGACY_MIGRATION_LOCK}" \
  "${TEST_ROOT}/legacy-holder.out" \
  || { sed -n '1,160p' "${TEST_ROOT}/legacy-holder.out" >&2; fail 'legacy holder did not report lock contention'; }
[ ! -s "$INSTALL_CALLS" ] || fail 'legacy holder reached script installation'
[ ! -s "$SYSTEMCTL_CALLS" ] || fail 'legacy holder touched systemd'
[ "$(cksum "$LEGACY_MIGRATION_CONFIG")" = "$legacy_checksum_before" ] \
  || fail 'legacy holder changed the autodeploy configuration'

# Once the legacy holder is gone, the first installation operation checks from
# a separate process that FD 7 and FD 8 are both still exclusively held.
: >"$SYSTEMCTL_CALLS"
: >"$INSTALL_CALLS"
FENCE_CHECKED="${TEST_ROOT}/legacy-migration-fences-checked"
LOCK_DESCENDANT_READY="${TEST_ROOT}/legacy-migration-lock-descendant-ready"
LOCK_DESCENDANT_PID_FILE="${TEST_ROOT}/legacy-migration-lock-descendant.pid"
LOCK_DESCENDANT_REPORT="${TEST_ROOT}/legacy-migration-lock-descendant.report"
LOCK_DESCENDANT_REENTRY_GO="${TEST_ROOT}/legacy-migration-lock-descendant-reentry-go"
LOCK_DESCENDANT_REENTRY_OUT="${TEST_ROOT}/legacy-migration-lock-descendant-reentry.out"
rm -f -- "$FENCE_CHECKED"
rm -f -- "$LOCK_DESCENDANT_READY" "$LOCK_DESCENDANT_PID_FILE" \
  "$LOCK_DESCENDANT_REPORT" "$LOCK_DESCENDANT_REENTRY_GO" "$LOCK_DESCENDANT_REENTRY_OUT"
env \
  PATH="${FAKE_BIN}:${PATH}" \
  FAKE_SYSTEMCTL_CALLS="$SYSTEMCTL_CALLS" \
  FAKE_INSTALL_CALLS="$INSTALL_CALLS" \
  FAKE_SYSTEMCTL_SPAWN_LOCK_DESCENDANT=1 \
  FAKE_LOCK_DESCENDANT_READY="$LOCK_DESCENDANT_READY" \
  FAKE_LOCK_DESCENDANT_PID_FILE="$LOCK_DESCENDANT_PID_FILE" \
  FAKE_LOCK_DESCENDANT_REPORT="$LOCK_DESCENDANT_REPORT" \
  FAKE_LOCK_DESCENDANT_REENTRY_GO="$LOCK_DESCENDANT_REENTRY_GO" \
  FAKE_LOCK_DESCENDANT_REENTRY_OUT="$LOCK_DESCENDANT_REENTRY_OUT" \
  FAKE_LOCK_DESCENDANT_LEGACY="$LEGACY_MIGRATION_LOCK" \
  FAKE_EXPECT_MAINTENANCE_FENCES=1 \
  FAKE_MAINTENANCE_FENCES_CHECKED="$FENCE_CHECKED" \
  FAKE_LEGACY_MAINTENANCE_LOCK_FILE="$LEGACY_MIGRATION_LOCK" \
  SUB2API_APP_DIR="$APP_DIR" \
  SUB2API_AUTODEPLOY_CONFIG_FILE="$LEGACY_MIGRATION_CONFIG" \
  SUB2API_AUTODEPLOY_UNIT_DIR="$UNIT_DIR" \
  SUB2API_MAINTENANCE_LOCK_FILE="$MAINTENANCE_LOCK_FILE" \
  SUB2API_MAINTENANCE_LEGACY_LOCK_FILE_FOR_TESTS="$LEGACY_MIGRATION_LOCK" \
  SUB2API_RUNTIME_GUARD_EXECUTABLE="${TEST_ROOT}/libexec/sub2api-runtime-guard.sh" \
  /bin/bash "$SCRIPT" \
    --production-branch main \
    --production-repo https://github.com/Turtle-Li/sub2api.git \
    --upstream-repo https://github.com/Wei-Shaw/sub2api.git \
    --replace-config \
    --no-enable-runtime-guard \
    --no-enable \
    >"${TEST_ROOT}/legacy-migration-success.out"
[ -e "$FENCE_CHECKED" ] || fail 'legacy migration did not retain both maintenance fences into installation'
assert_contains "$LEGACY_MIGRATION_CONFIG" "SUB2API_MAINTENANCE_LOCK_FILE=${MAINTENANCE_LOCK_FILE}"

# systemctl is an external child of the fenced installer. It deliberately
# leaves a live sleep descendant after the installer exits. The supervisor
# alone owns FD 7/8, so neither canonical nor legacy flock may remain busy.
[ -e "$LOCK_DESCENDANT_READY" ] || fail 'migration did not start the external lock-descendant fixture'
[ -s "$LOCK_DESCENDANT_PID_FILE" ] || fail 'external lock-descendant did not report a PID'
LOCK_DESCENDANT_PID="$(cat "$LOCK_DESCENDANT_PID_FILE")"
kill -0 "$LOCK_DESCENDANT_PID" >/dev/null 2>&1 \
  || fail 'external lock-descendant did not remain alive after installer exit'
: >"$LOCK_DESCENDANT_REENTRY_GO"
for _ in $(seq 1 200); do
  grep -q '^reentry_status=' "$LOCK_DESCENDANT_REPORT" 2>/dev/null && break
  sleep 0.01
done
[ -s "$LOCK_DESCENDANT_REPORT" ] || fail 'external lock-descendant did not report inherited descriptors'
assert_contains "$LOCK_DESCENDANT_REPORT" 'fd6=present'
assert_contains "$LOCK_DESCENDANT_REPORT" 'fd7=absent'
assert_contains "$LOCK_DESCENDANT_REPORT" 'fd8=absent'
assert_contains "$LOCK_DESCENDANT_REPORT" 'fd9=absent'
assert_contains "$LOCK_DESCENDANT_REPORT" 'fence_env=absent'
grep -Eq '^reentry_status=[1-9][0-9]*$' "$LOCK_DESCENDANT_REPORT" \
  || { cat "$LOCK_DESCENDANT_REPORT" >&2; fail 'external descendant forged a fenced installer re-entry'; }
grep -Fq 'cannot inspect inherited maintenance-lock nonce' "$LOCK_DESCENDANT_REENTRY_OUT" \
  || { sed -n '1,160p' "$LOCK_DESCENDANT_REENTRY_OUT" >&2; fail 'forged re-entry did not fail at the nonce boundary'; }
python3 - "$MAINTENANCE_LOCK_FILE" "$LEGACY_MIGRATION_LOCK" <<'PY'
import fcntl
import os
import sys

for path in sys.argv[1:]:
    descriptor = os.open(path, os.O_RDWR)
    try:
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise SystemExit(f"maintenance lock remained busy after installer exit: {path}")
    finally:
        os.close(descriptor)
PY
kill "$LOCK_DESCENDANT_PID" >/dev/null 2>&1 || true
LOCK_DESCENDANT_PID=""

# Signal delivery must not make the supervisor relinquish either fence while
# its Bash child is still alive. Inject a test-only TERM/INT/HUP ignore trap
# into the normal-user checkout after the real fence verification point. The
# outer invocation skips it; only the pinned-FD child enters this window.
inject_signal_hold_fixture() {
  local temporary="${SCRIPT}.signal-hold"

  restore_installer_source
  awk '
    { print }
    $0 == "ORIGINAL_ARGS=(\"$@\")" {
      print ""
      print "# Test fixture: ignore the first TERM/INT/HUP in the fenced child."
      print "if [ \"${SUB2API_MAINTENANCE_LOCK_TEST_SIGNAL_HOLD:-0}\" = 1 ] && [ \"$INSTALLER_FENCE_VERIFIED\" = true ]; then"
      print "  [ \"${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-0}\" = 1 ] || exit 1"
      print "  test_signal_hold() {"
      print "    local signal_name=\"$1\""
      print "    printf '\''%s\\n'\'' \"$signal_name\" >\"${SUB2API_MAINTENANCE_LOCK_TEST_SIGNAL_SEEN:?}\""
      print "  }"
      print "  trap '\''test_signal_hold TERM'\'' TERM"
      print "  trap '\''test_signal_hold INT'\'' INT"
      print "  trap '\''test_signal_hold HUP'\'' HUP"
      print "  printf '\''%s\\n'\'' \"$$\" >\"${SUB2API_MAINTENANCE_LOCK_TEST_SIGNAL_CHILD_PID:?}\""
      print "  : >\"${SUB2API_MAINTENANCE_LOCK_TEST_SIGNAL_READY:?}\""
      print "  while :; do sleep 0.01 || true; done"
      print "fi"
    }
  ' "$SCRIPT" >"$temporary"
  grep -Fq -- 'Test fixture: ignore the first TERM/INT/HUP' "$temporary" \
    || fail 'could not inject the fenced-child signal fixture'
  mv -f -- "$temporary" "$SCRIPT"
  chmod 755 "$SCRIPT"
  if [ "$(id -u)" -eq 0 ]; then
    chown 12345 "$SCRIPT"
  fi
}

wait_for_fixture_file() {
  local path="$1" label="$2"

  for _ in $(seq 1 500); do
    [ -e "$path" ] && return 0
    sleep 0.01
  done
  fail "${label} did not become ready"
}

assert_signal_fences_busy() {
  if python3 - "$MAINTENANCE_LOCK_FILE" "$LEGACY_MIGRATION_LOCK" <<'PY'
import fcntl
import os
import sys

for path in sys.argv[1:]:
    descriptor = os.open(path, os.O_RDWR)
    try:
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            continue
        raise SystemExit(f"maintenance fence was released while installer child lived: {path}")
    finally:
        os.close(descriptor)
PY
  then
    return 0
  fi
  fail 'supervisor released a maintenance fence before its installer child stopped'
}

assert_signal_fences_available() {
  if python3 - "$MAINTENANCE_LOCK_FILE" "$LEGACY_MIGRATION_LOCK" <<'PY'
import fcntl
import os
import sys

for path in sys.argv[1:]:
    descriptor = os.open(path, os.O_RDWR)
    try:
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise SystemExit(f"maintenance fence remained busy after installer child stopped: {path}")
    finally:
        os.close(descriptor)
PY
  then
    return 0
  fi
  fail 'supervisor did not release maintenance fences after reaping its child'
}

assert_supervisor_signal_fence() {
  local signal_name="$1" expected_status="$2" prefix
  local ready_file seen_file child_pid_file output_file config_checksum status

  prefix="${TEST_ROOT}/supervisor-${signal_name}"
  ready_file="${prefix}.ready"
  seen_file="${prefix}.seen"
  child_pid_file="${prefix}.child.pid"
  output_file="${prefix}.out"
  rm -f -- "$ready_file" "$seen_file" "$child_pid_file" "$output_file"
  printf 'SUB2API_APP_DIR=%s\nSUB2API_MAINTENANCE_LOCK_FILE=%s\n' \
    "$APP_DIR" "$LEGACY_MIGRATION_LOCK" >"$LEGACY_MIGRATION_CONFIG"
  config_checksum="$(cksum "$LEGACY_MIGRATION_CONFIG")"
  : >"$SYSTEMCTL_CALLS"
  : >"$INSTALL_CALLS"

  env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_SYSTEMCTL_CALLS="$SYSTEMCTL_CALLS" \
    FAKE_INSTALL_CALLS="$INSTALL_CALLS" \
    SUB2API_MAINTENANCE_LOCK_TEST_SIGNAL_HOLD=1 \
    SUB2API_MAINTENANCE_LOCK_TEST_SIGNAL_READY="$ready_file" \
    SUB2API_MAINTENANCE_LOCK_TEST_SIGNAL_SEEN="$seen_file" \
    SUB2API_MAINTENANCE_LOCK_TEST_SIGNAL_CHILD_PID="$child_pid_file" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_CONFIG_FILE="$LEGACY_MIGRATION_CONFIG" \
    SUB2API_AUTODEPLOY_UNIT_DIR="$UNIT_DIR" \
    SUB2API_MAINTENANCE_LOCK_FILE="$MAINTENANCE_LOCK_FILE" \
    SUB2API_MAINTENANCE_LEGACY_LOCK_FILE_FOR_TESTS="$LEGACY_MIGRATION_LOCK" \
    SUB2API_RUNTIME_GUARD_EXECUTABLE="${TEST_ROOT}/libexec/sub2api-runtime-guard.sh" \
    /bin/bash "$SCRIPT" \
      --production-branch main \
      --production-repo https://github.com/Turtle-Li/sub2api.git \
      --upstream-repo https://github.com/Wei-Shaw/sub2api.git \
      --replace-config \
      --no-enable-runtime-guard \
      --no-enable \
      >"$output_file" 2>&1 &
  SIGNAL_SUPERVISOR_PID=$!
  wait_for_fixture_file "$ready_file" "${signal_name} fenced installer child"
  wait_for_fixture_file "$child_pid_file" "${signal_name} fenced installer child PID"
  SIGNAL_CHILD_PID="$(cat "$child_pid_file")"
  kill -0 "$SIGNAL_SUPERVISOR_PID" >/dev/null 2>&1 \
    || { sed -n '1,180p' "$output_file" >&2; fail "${signal_name} supervisor exited before signal delivery"; }
  kill -0 "$SIGNAL_CHILD_PID" >/dev/null 2>&1 \
    || { sed -n '1,180p' "$output_file" >&2; fail "${signal_name} installer child exited before signal delivery"; }

  kill -"$signal_name" "$SIGNAL_SUPERVISOR_PID"
  wait_for_fixture_file "$seen_file" "${signal_name} delivery to fenced installer child"
  assert_contains "$seen_file" "$signal_name"
  kill -0 "$SIGNAL_SUPERVISOR_PID" >/dev/null 2>&1 \
    || { sed -n '1,180p' "$output_file" >&2; fail "${signal_name} supervisor exited after its child ignored the first signal"; }
  kill -0 "$SIGNAL_CHILD_PID" >/dev/null 2>&1 \
    || fail "${signal_name} installer child did not ignore the first signal"
  assert_signal_fences_busy

  # A second termination request escalates the isolated child group to
  # SIGKILL. The supervisor must still wait/reap before releasing either lock.
  kill -"$signal_name" "$SIGNAL_SUPERVISOR_PID"
  if wait "$SIGNAL_SUPERVISOR_PID"; then
    status=0
  else
    status=$?
  fi
  SIGNAL_SUPERVISOR_PID=""
  [ "$status" -eq "$expected_status" ] \
    || { sed -n '1,180p' "$output_file" >&2; fail "${signal_name} supervisor returned ${status}, expected ${expected_status}"; }
  for _ in $(seq 1 500); do
    kill -0 "$SIGNAL_CHILD_PID" >/dev/null 2>&1 || break
    sleep 0.01
  done
  if kill -0 "$SIGNAL_CHILD_PID" >/dev/null 2>&1; then
    fail "${signal_name} installer child remained after its supervisor returned"
  fi
  SIGNAL_CHILD_PID=""
  assert_signal_fences_available
  [ ! -s "$INSTALL_CALLS" ] || fail "${signal_name} signal fixture reached script installation"
  [ ! -s "$SYSTEMCTL_CALLS" ] || fail "${signal_name} signal fixture touched systemd"
  [ "$(cksum "$LEGACY_MIGRATION_CONFIG")" = "$config_checksum" ] \
    || fail "${signal_name} signal fixture changed its configuration"
}

inject_signal_hold_fixture
assert_supervisor_signal_fence TERM 143
assert_supervisor_signal_fence INT 130
assert_supervisor_signal_fence HUP 129
restore_installer_source

printf 'Autodeploy staged external-runtime installation test passed.\n'
