#!/usr/bin/env bash

# Exercise the standalone certificate-receiver installer without creating a
# system user, sudoers policy, or files below /etc. The installer must reject a
# missing maintenance-lock helper before any such mutation.

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
INSTALLER="${DEPLOY_DIR}/install-sub2api-cert-receiver.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-cert-installer-test.XXXXXX")"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
FAKE_BIN="${TEST_ROOT}/bin"
APP_DIR="${TEST_ROOT}/app"
DEPLOY_HOME="${TEST_ROOT}/deploy-home"
CONFIG_FILE="${TEST_ROOT}/etc/sub2api-cert-receiver.env"
MAINTENANCE_LOCK_FILE="${TEST_ROOT}/runtime/sub2api-maintenance.lock"
PUBLIC_KEY_FILE="${TEST_ROOT}/controller.pub"
MUTATION_CALLS="${TEST_ROOT}/mutations.log"
FAKE_WRITABLE_PARENT_CONTAINER=""
FAKE_UNSAFE_HELPER_ANCESTOR=""
export FAKE_WRITABLE_PARENT_CONTAINER FAKE_UNSAFE_HELPER_ANCESTOR

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
    || { sed -n '1,160p' "$file" >&2 || true; fail "expected '${expected}' in ${file}"; }
}

file_mode() {
  stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1"
}

mkdir -p "$FAKE_BIN" "${APP_DIR}/scripts" "${CONFIG_FILE%/*}"
: >"$MUTATION_CALLS"
printf 'ssh-ed25519 test-controller-key certificate-controller\n' >"$PUBLIC_KEY_FILE"
for script in sub2api-cert-deploy-trigger.sh sub2api-cert-receiver.sh; do
  printf '#!/usr/bin/env bash\nexit 0\n' >"${APP_DIR}/scripts/${script}"
  chmod 755 "${APP_DIR}/scripts/${script}"
done
printf 'example.invalid { respond 200 }\n' >"${APP_DIR}/Caddyfile"
chmod 644 "${APP_DIR}/Caddyfile"

cat >"${FAKE_BIN}/id" <<'EOF'
#!/usr/bin/env bash

if [ "${1:-}" = -u ]; then
  printf '0\n'
  exit 0
fi
if [ "${1:-}" = -g ]; then
  printf '0\n'
  exit 0
fi
exit 1
EOF
chmod +x "${FAKE_BIN}/id"

cat >"${FAKE_BIN}/stat" <<'EOF'
#!/usr/bin/env bash

format="${1:-}:${2:-}"
path="${3:-}"
case "$format" in
  -c:%u) printf '0\n' ;;
  -c:%a)
    if [ -n "${FAKE_WRITABLE_PARENT_CONTAINER:-}" ] \
      && [ "$path" = "$FAKE_WRITABLE_PARENT_CONTAINER" ]; then
      printf '1777\n'
    elif [ -n "${FAKE_UNSAFE_HELPER_ANCESTOR:-}" ] \
      && [ "$path" = "$FAKE_UNSAFE_HELPER_ANCESTOR" ]; then
      printf '775\n'
    else
      case "$path" in
      */Caddyfile) printf '644\n' ;;
      *) printf '755\n' ;;
      esac
    fi
    ;;
  -c:%u:%g:%a:%h:%d:%i)
    if [ -n "${FAKE_WRITABLE_PARENT_CONTAINER:-}" ] \
      && [ "$path" = "$FAKE_WRITABLE_PARENT_CONTAINER" ]; then
      printf '0:0:1777:1:1:1\n'
    else
      printf '0:0:755:1:1:1\n'
    fi
    ;;
  *) exit 1 ;;
esac
EOF
chmod +x "${FAKE_BIN}/stat"

cat >"${FAKE_BIN}/install" <<'EOF'
#!/usr/bin/env bash

set -Eeuo pipefail

printf 'install:%s\n' "$*" >>"$FAKE_MUTATION_CALLS"
directory_mode=false
mode=""
arguments=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    -d) directory_mode=true ;;
    -o|-g) shift ;;
    -m)
      shift
      mode="${1:-}"
      ;;
    *) arguments+=("$1") ;;
  esac
  shift
done

if [ "$directory_mode" = true ]; then
  for destination in "${arguments[@]}"; do
    mkdir -p "$destination"
    [ -z "$mode" ] || chmod "$mode" "$destination"
  done
  exit 0
fi

[ "${#arguments[@]}" -eq 2 ] || exit 2
source_path="${arguments[0]}"
destination_path="${arguments[1]}"
case "$destination_path" in
  /etc/sudoers.d/*) exit 0 ;;
esac
mkdir -p "${destination_path%/*}"
cp "$source_path" "$destination_path"
[ -z "$mode" ] || chmod "$mode" "$destination_path"
EOF
chmod +x "${FAKE_BIN}/install"

for command_name in useradd visudo; do
  cat >"${FAKE_BIN}/${command_name}" <<'EOF'
#!/usr/bin/env bash
printf '%s:%s\n' "$(basename "$0")" "$*" >>"$FAKE_MUTATION_CALLS"
exit 0
EOF
  chmod +x "${FAKE_BIN}/${command_name}"
done

cat >"${FAKE_BIN}/ssh-keygen" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "${FAKE_BIN}/ssh-keygen"

for command_name in getent sudo; do
  cat >"${FAKE_BIN}/${command_name}" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
  chmod +x "${FAKE_BIN}/${command_name}"
done

run_installer() {
  local lock_file="${1:-$MAINTENANCE_LOCK_FILE}"

  env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_MUTATION_CALLS="$MUTATION_CALLS" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_CERT_DEPLOY_HOME="$DEPLOY_HOME" \
    SUB2API_CERT_RECEIVER_CONFIG_FILE="$CONFIG_FILE" \
    SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1 \
    SUB2API_MAINTENANCE_LOCK_FILE="$lock_file" \
    /bin/bash "$INSTALLER" \
      --public-key-file "$PUBLIC_KEY_FILE" \
      --source-address 100.64.0.10
}

MISSING_HELPER_OUTPUT="${TEST_ROOT}/missing-helper.log"
if run_installer >"$MISSING_HELPER_OUTPUT" 2>&1; then
  fail 'standalone certificate installer accepted a missing maintenance-lock helper'
fi
assert_contains "$MISSING_HELPER_OUTPUT" 'maintenance lock helper is not a regular non-symlink file'
[ ! -e "$DEPLOY_HOME" ] || fail 'missing helper created the deploy-user home directory'
[ ! -e "$CONFIG_FILE" ] || fail 'missing helper wrote the receiver configuration'
[ ! -s "$MUTATION_CALLS" ] || fail 'missing helper reached a mutating installer command'

cp "${DEPLOY_DIR}/sub2api-maintenance-lock.sh" "${APP_DIR}/scripts/sub2api-maintenance-lock.sh"
chmod 755 "${APP_DIR}/scripts/sub2api-maintenance-lock.sh"
: >"$MUTATION_CALLS"
run_installer >"${TEST_ROOT}/normal-install.log"
assert_contains "$CONFIG_FILE" "SUB2API_MAINTENANCE_LOCK_FILE=${MAINTENANCE_LOCK_FILE}"
[ -s "${DEPLOY_HOME}/.ssh/authorized_keys" ] \
  || fail 'normal standalone certificate installation did not write authorized_keys'
[ -d "${MAINTENANCE_LOCK_FILE%/*}" ] \
  || fail 'normal standalone certificate installation did not create the private lock directory'
grep -Fq -- 'useradd:' "$MUTATION_CALLS" \
  || fail 'normal standalone certificate installation did not create the deploy user'

# The standalone installer may use a temporary private path only under the
# explicit test switch. A production invocation cannot install a certificate
# receiver configuration that would split the host-wide maintenance domain.
SECOND_SAFE_LOCK="${TEST_ROOT}/second-safe/private/maintenance.lock"
config_checksum_before="$(cksum "$CONFIG_FILE")"
: >"$MUTATION_CALLS"
if env \
  PATH="${FAKE_BIN}:${PATH}" \
  FAKE_MUTATION_CALLS="$MUTATION_CALLS" \
  SUB2API_APP_DIR="$APP_DIR" \
  SUB2API_CERT_DEPLOY_HOME="$DEPLOY_HOME" \
  SUB2API_CERT_RECEIVER_CONFIG_FILE="$CONFIG_FILE" \
  SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=0 \
  SUB2API_MAINTENANCE_LOCK_FILE="$SECOND_SAFE_LOCK" \
  /bin/bash "$INSTALLER" \
    --public-key-file "$PUBLIC_KEY_FILE" \
    --source-address 100.64.0.10 \
    >"${TEST_ROOT}/production-noncanonical-lock.out" 2>&1; then
  fail 'standalone certificate installer accepted a production noncanonical lock path'
fi
assert_contains "${TEST_ROOT}/production-noncanonical-lock.out" \
  'maintenance lock path must be the canonical /run/sub2api-maintenance/sub2api-maintenance.lock'
[ ! -e "${SECOND_SAFE_LOCK%/*}" ] \
  || fail 'standalone production noncanonical lock created a parent before rejection'
[ ! -s "$MUTATION_CALLS" ] \
  || fail 'standalone production noncanonical lock reached a mutating installer command'
[ "$(cksum "$CONFIG_FILE")" = "$config_checksum_before" ] \
  || fail 'standalone production noncanonical lock changed the receiver config'

# The helper's read-only target preflight must reject traversal and a symlink
# parent before any installer mutation can chmod the sensitive target.
SENSITIVE_DIR="${TEST_ROOT}/sensitive"
mkdir -m 755 "$SENSITIVE_DIR"
SENSITIVE_MODE="$(file_mode "$SENSITIVE_DIR")"
assert_unsafe_maintenance_target() {
  local label="$1" lock_path="$2" expected="$3" parent_container="${4:-}" output checksum

  output="${TEST_ROOT}/${label}-maintenance-lock.out"
  checksum="$(cksum "$CONFIG_FILE")"
  : >"$MUTATION_CALLS"
  if FAKE_WRITABLE_PARENT_CONTAINER="$parent_container" run_installer "$lock_path" >"$output" 2>&1; then
    fail "${label} maintenance lock target was accepted"
  fi
  assert_contains "$output" "$expected"
  [ ! -s "$MUTATION_CALLS" ] || fail "${label} reached a mutating installer command"
  [ "$(cksum "$CONFIG_FILE")" = "$checksum" ] || fail "${label} changed the receiver config"
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

# The installed helper's directory chain must reject a root-owned-looking but
# group-writable component before any standalone-installer mutation.
HELPER_MODE_OUTPUT="${TEST_ROOT}/helper-ancestor-mode.out"
config_checksum_before="$(cksum "$CONFIG_FILE")"
: >"$MUTATION_CALLS"
if FAKE_UNSAFE_HELPER_ANCESTOR="${APP_DIR}/scripts" run_installer >"$HELPER_MODE_OUTPUT" 2>&1; then
  fail 'standalone helper under a group-writable ancestor was accepted'
fi
assert_contains "$HELPER_MODE_OUTPUT" 'maintenance lock helper ancestor is group/other writable'
[ ! -s "$MUTATION_CALLS" ] || fail 'standalone helper group-writable ancestor reached a mutating installer command'
[ "$(cksum "$CONFIG_FILE")" = "$config_checksum_before" ] \
  || fail 'standalone helper group-writable ancestor changed the receiver config'

# A standalone installed helper has no ordinary-checkout exception: every
# ancestor must remain root-owned, non-writable, and non-symlink until source.
# Replacing its app-directory component with a symlink must fail before user,
# authorized_keys, config, or sudoers mutation.
REAL_APP_DIR="${TEST_ROOT}/app-real"
mv "$APP_DIR" "$REAL_APP_DIR"
ln -s "$REAL_APP_DIR" "$APP_DIR"
HELPER_ANCESTOR_OUTPUT="${TEST_ROOT}/helper-ancestor-symlink.out"
config_checksum_before="$(cksum "$CONFIG_FILE")"
: >"$MUTATION_CALLS"
if run_installer >"$HELPER_ANCESTOR_OUTPUT" 2>&1; then
  rm "$APP_DIR"
  mv "$REAL_APP_DIR" "$APP_DIR"
  fail 'standalone helper under a symlink ancestor was accepted'
fi
assert_contains "$HELPER_ANCESTOR_OUTPUT" 'maintenance lock helper ancestor is a symlink'
[ ! -s "$MUTATION_CALLS" ] || {
  rm "$APP_DIR"
  mv "$REAL_APP_DIR" "$APP_DIR"
  fail 'standalone helper symlink ancestor reached a mutating installer command'
}
[ "$(cksum "$CONFIG_FILE")" = "$config_checksum_before" ] || {
  rm "$APP_DIR"
  mv "$REAL_APP_DIR" "$APP_DIR"
  fail 'standalone helper symlink ancestor changed the receiver config'
}
rm "$APP_DIR"
mv "$REAL_APP_DIR" "$APP_DIR"

printf 'Standalone certificate receiver installer tests passed.\n'
