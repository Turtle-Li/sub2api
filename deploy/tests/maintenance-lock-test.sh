#!/usr/bin/env bash

# Exercise the shared maintenance-lock boundary without Docker, a server, or
# production paths. The explicit non-root flag models the caller-owned 0700
# temporary directories used by deployment tests; production callers remain
# root-only.

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
HELPER="${DEPLOY_DIR}/sub2api-maintenance-lock.sh"
PAYMENT_VAULT_SCRIPT="${DEPLOY_DIR}/sub2api-unified-payment-vault-container.sh"
INSTALLER_SCRIPT="${DEPLOY_DIR}/install-autodeploy.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-maintenance-lock-test.XXXXXX")"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
FAKE_BIN="${TEST_ROOT}/bin"
REAL_STAT="$(command -v stat)"
HOLDER_PID=""

cleanup() {
  if [ -n "$HOLDER_PID" ] && kill -0 "$HOLDER_PID" >/dev/null 2>&1; then
    if [ -n "${RELEASE_FILE:-}" ]; then
      : >"$RELEASE_FILE"
    fi
    kill "$HOLDER_PID" >/dev/null 2>&1 || true
    wait "$HOLDER_PID" >/dev/null 2>&1 || true
  fi
  rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

file_mode() {
  stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1"
}

file_links() {
  stat -c '%h' "$1" 2>/dev/null || stat -f '%l' "$1"
}

open_lock() {
  local lock_path="$1"

  # shellcheck disable=SC2016 # Inner shell intentionally expands its own arguments.
  env SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1 \
    /bin/bash -c '
      set -Eeuo pipefail
      . "$1"
      if ! sub2api_maintenance_lock_open "$2"; then
        printf "%s\n" "$SUB2API_MAINTENANCE_LOCK_ERROR" >&2
        exit 1
      fi
    ' bash "$HELPER" "$lock_path"
}

assert_unsafe() {
  local label lock_path expected output

  label="$1"
  lock_path="$2"
  expected="$3"
  output="${TEST_ROOT}/${label}.log"

  if open_lock "$lock_path" >"$output" 2>&1; then
    fail "${label} was accepted as a maintenance lock"
  fi
  grep -Fq -- "$expected" "$output" \
    || { sed -n '1,120p' "$output" >&2; fail "${label} did not report ${expected}"; }
}

[ -r "$HELPER" ] && [ ! -L "$HELPER" ] || fail 'maintenance lock helper is missing or unsafe'
# shellcheck disable=SC1090,SC1091 # The helper is checked independently.
. "$HELPER"
[ "$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE" = '/run/sub2api-maintenance/sub2api-maintenance.lock' ] \
  || fail 'maintenance lock default is not the private runtime path'

for consumer in \
  "${DEPLOY_DIR}/sub2api-runtime-guard.sh" \
  "${DEPLOY_DIR}/sub2api-drain-monitor.sh" \
  "${DEPLOY_DIR}/sub2api-server-release.sh" \
  "${DEPLOY_DIR}/sub2api-node-state.sh" \
  "${DEPLOY_DIR}/sub2api-cert-receiver.sh" \
  "$PAYMENT_VAULT_SCRIPT" \
  "${DEPLOY_DIR}/gcp-taiwan-line/azure-caddy-listeners.sh"; do
  grep -Fq -- 'sub2api_maintenance_lock_open' "$consumer" \
    || fail "shared maintenance lock helper is not used by ${consumer}"
done
grep -Fq -- 'sub2api-maintenance-lock.sh' "$PAYMENT_VAULT_SCRIPT" \
  || fail 'payment Vault container does not source the shared maintenance lock helper'
if grep -Fq -- '/run/lock/sub2api-maintenance.lock' "$PAYMENT_VAULT_SCRIPT"; then
  fail 'payment Vault container retained the retired public maintenance lock path'
fi
# The installer owns both fences only during the documented legacy migration;
# individual runtime consumers must use the canonical private helper above.
# shellcheck disable=SC2016 # The search text intentionally names shell variables literally.
grep -Fq -- '"$MAINTENANCE_LOCK_FILE" "$legacy_fence_path"' "$INSTALLER_SCRIPT" \
  || fail 'legacy maintenance-lock migration no longer retains both fences'

PRIVATE_DIR="${TEST_ROOT}/private"
mkdir -m 700 "$PRIVATE_DIR"
SAFE_LOCK="${PRIVATE_DIR}/maintenance.lock"
open_lock "$SAFE_LOCK"
[ "$(file_mode "$PRIVATE_DIR")" = 700 ] || fail 'new maintenance lock parent is not mode 0700'
[ "$(file_mode "$SAFE_LOCK")" = 600 ] || fail 'new maintenance lock is not mode 0600'
[ "$(file_links "$SAFE_LOCK")" = 1 ] || fail 'new maintenance lock has an unexpected hard link'

assert_unsafe doubled-separator "${PRIVATE_DIR}//doubled.lock" 'contains an empty component'

ln -s "$SAFE_LOCK" "${PRIVATE_DIR}/symlink.lock"
assert_unsafe symlink "${PRIVATE_DIR}/symlink.lock" 'is a symlink'

ln -s "$PRIVATE_DIR" "${TEST_ROOT}/symlink-parent"
assert_unsafe parent-symlink "${TEST_ROOT}/symlink-parent/maintenance.lock" 'parent is a symlink'

mkfifo "${PRIVATE_DIR}/fifo.lock"
assert_unsafe fifo "${PRIVATE_DIR}/fifo.lock" 'not a regular file'

WORLD_PARENT="${TEST_ROOT}/world-writable"
mkdir -m 700 "$WORLD_PARENT"
chmod 777 "$WORLD_PARENT"
assert_unsafe world-writable-parent "${WORLD_PARENT}/maintenance.lock" 'parent must be mode 0700'

WRONG_MODE="${PRIVATE_DIR}/wrong-mode.lock"
: >"$WRONG_MODE"
chmod 644 "$WRONG_MODE"
assert_unsafe wrong-mode "$WRONG_MODE" 'must be mode 0600'

HARD_LINK="${PRIVATE_DIR}/hard-link.lock"
: >"$HARD_LINK"
chmod 600 "$HARD_LINK"
ln "$HARD_LINK" "${PRIVATE_DIR}/hard-link-second"
assert_unsafe hard-link "$HARD_LINK" 'exactly one hard link'

mkdir -p "$FAKE_BIN"
cat >"${FAKE_BIN}/stat" <<'EOF'
#!/usr/bin/env bash

last_argument=""
for argument in "$@"; do
  last_argument="$argument"
done
result="$("$REAL_STAT" "$@")" || exit $?
if [ "$last_argument" = "$FAKE_STAT_BAD_OWNER_PATH" ]; then
  printf '%s\n' "$result" | sed 's/^[^:]*/99999/'
else
  printf '%s\n' "$result"
fi
EOF
chmod +x "${FAKE_BIN}/stat"
cat >"${FAKE_BIN}/flock" <<'EOF'
#!/usr/bin/env python3

import fcntl
import sys

arguments = sys.argv[1:]
nonblocking = False
while arguments and arguments[0].startswith("-"):
    option = arguments.pop(0)
    if option == "-n":
        nonblocking = True
    elif option not in ("-x", "-e"):
        sys.exit(64)

if len(arguments) != 1:
    sys.exit(64)

descriptor = int(arguments[0])
operation = fcntl.LOCK_EX | (fcntl.LOCK_NB if nonblocking else 0)
try:
    fcntl.flock(descriptor, operation)
except BlockingIOError:
    sys.exit(1)
EOF
chmod +x "${FAKE_BIN}/flock"
WRONG_OWNER="${PRIVATE_DIR}/wrong-owner.lock"
: >"$WRONG_OWNER"
chmod 600 "$WRONG_OWNER"
WRONG_OWNER_OUTPUT="${TEST_ROOT}/wrong-owner.log"
# shellcheck disable=SC2016 # Inner shell intentionally expands its own arguments.
if env \
  PATH="${FAKE_BIN}:${PATH}" \
  REAL_STAT="$REAL_STAT" \
  FAKE_STAT_BAD_OWNER_PATH="$WRONG_OWNER" \
  SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1 \
  /bin/bash -c '
    set -Eeuo pipefail
    . "$1"
    if ! sub2api_maintenance_lock_open "$2"; then
      printf "%s\n" "$SUB2API_MAINTENANCE_LOCK_ERROR" >&2
      exit 1
    fi
  ' bash "$HELPER" "$WRONG_OWNER" >"$WRONG_OWNER_OUTPUT" 2>&1; then
  fail 'wrong-owner maintenance lock was accepted'
fi
grep -Fq -- 'unexpected owner' "$WRONG_OWNER_OUTPUT" \
  || { sed -n '1,120p' "$WRONG_OWNER_OUTPUT" >&2; fail 'wrong-owner lock did not fail closed'; }

READY_FILE="${TEST_ROOT}/holder-ready"
RELEASE_FILE="${TEST_ROOT}/holder-release"
python3 - "$SAFE_LOCK" "$READY_FILE" "$RELEASE_FILE" <<'PY' &
import fcntl
from pathlib import Path
import sys
import time

lock_path, ready_path, release_path = sys.argv[1:]
with open(lock_path, "a", encoding="utf-8") as handle:
    fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
    Path(ready_path).touch()
    while not Path(release_path).exists():
        time.sleep(0.05)
PY
HOLDER_PID=$!
for _ in $(seq 1 100); do
  [ -e "$READY_FILE" ] && break
  sleep 0.05
done
[ -e "$READY_FILE" ] || { kill "$HOLDER_PID" 2>/dev/null || true; fail 'lock holder did not become ready'; }
# shellcheck disable=SC2016 # Inner shell intentionally expands its own arguments.
if env PATH="${FAKE_BIN}:${PATH}" \
  REAL_STAT="$REAL_STAT" \
  SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1 \
  /bin/bash -c '
    set -u
    . "$1"
    sub2api_maintenance_lock_open "$2" || exit 70
    flock -n 8
  ' bash "$HELPER" "$SAFE_LOCK"; then
  : >"$RELEASE_FILE"
  wait "$HOLDER_PID"
  fail 'a legitimately pre-held maintenance lock was not reported busy'
else
  busy_status=$?
fi
: >"$RELEASE_FILE"
wait "$HOLDER_PID"
[ "$busy_status" -eq 1 ] || fail "legitimate busy lock returned ${busy_status}, not flock contention"
HOLDER_PID=""

# The payment Vault container must contend with the exact same secure inode as
# release, runtime, certificate, and Caddy maintenance consumers. Its image
# inspection remains read-only, but it must not inspect/create the shared
# container or volume once the shared lock is held.
PAYMENT_DOCKER_CALLS="${TEST_ROOT}/payment-docker-calls.log"
PAYMENT_OUTPUT="${TEST_ROOT}/payment-lock-contention.out"
PAYMENT_IMAGE='sub2api:prebuilt-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
cat >"${FAKE_BIN}/docker" <<'EOF'
#!/usr/bin/env bash

set -eu

printf '%s\n' "$*" >>"$PAYMENT_DOCKER_CALLS"
case "$1:$2" in
  image:inspect)
    case "$5" in
      *'.Os}}/{{.Architecture}}'*) printf 'linux/amd64\n' ;;
      *'image.revision'*) printf '%s\n' "${3#sub2api:prebuilt-}" ;;
      *'image.source'*) printf 'https://github.com/Turtle-Li/sub2api\n' ;;
      *'image.version'*) printf '0.1.186\n' ;;
      *) exit 97 ;;
    esac
    ;;
  *) exit 97 ;;
esac
EOF
chmod +x "${FAKE_BIN}/docker"

PAYMENT_READY_FILE="${TEST_ROOT}/payment-holder-ready"
RELEASE_FILE="${TEST_ROOT}/payment-holder-release"
python3 - "$SAFE_LOCK" "$PAYMENT_READY_FILE" "$RELEASE_FILE" <<'PY' &
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
HOLDER_PID=$!
for _ in $(seq 1 100); do
  [ -e "$PAYMENT_READY_FILE" ] && break
  sleep 0.05
done
[ -e "$PAYMENT_READY_FILE" ] \
  || { kill "$HOLDER_PID" 2>/dev/null || true; fail 'payment lock holder did not become ready'; }
if env \
  PATH="${FAKE_BIN}:${PATH}" \
  REAL_STAT="$REAL_STAT" \
  PAYMENT_DOCKER_CALLS="$PAYMENT_DOCKER_CALLS" \
  SUB2API_PAYMENT_VAULT_CONTAINER_ALLOW_NON_ROOT_FOR_TESTS=1 \
  SUB2API_MAINTENANCE_LOCK_FILE="$SAFE_LOCK" \
  /bin/bash "$PAYMENT_VAULT_SCRIPT" prepare "$PAYMENT_IMAGE" >"$PAYMENT_OUTPUT" 2>&1; then
  : >"$RELEASE_FILE"
  wait "$HOLDER_PID"
  fail 'payment Vault container bypassed a held shared maintenance lock'
fi
: >"$RELEASE_FILE"
wait "$HOLDER_PID"
HOLDER_PID=""
grep -Fxq 'SUB2API_PAYMENT_VAULT_CONTAINER_REJECTED' "$PAYMENT_OUTPUT" \
  || { sed -n '1,120p' "$PAYMENT_OUTPUT" >&2; fail 'payment lock contention did not fail closed'; }
[ "$(wc -l <"$PAYMENT_DOCKER_CALLS")" -eq 4 ] \
  || { cat "$PAYMENT_DOCKER_CALLS" >&2; fail 'payment contention reached a container or volume mutation'; }

printf 'Maintenance lock safety tests passed.\n'
