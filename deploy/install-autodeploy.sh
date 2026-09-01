#!/usr/bin/env bash

# Install the Sub2API release controller on the dedicated Sub2API host. The
# normal path receives a GitHub-built image and only performs blue-green
# release work. The source-based service and polling timer remain available as
# an explicit recovery fallback. This installer does not release an image.

set -Eeuo pipefail

# The fenced re-exec runs this installer from a no-follow descriptor (FD 6),
# so its normal dirname(BASH_SOURCE) calculation would point at /dev. Accept
# the supplied source root only after one trusted Python gate validates the
# private source snapshot on FD 6, unlinked nonce on FD 9, and absence of the
# supervisor-only lock descriptors. An ordinary sudo invocation cannot forge
# those root-owned descriptors.
INSTALLER_FENCE_READY="${SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_READY:-0}"
INSTALLER_FENCE_VERIFIED=false
VERIFIED_INSTALLER_FENCE_LEGACY=""
case "$INSTALLER_FENCE_READY" in
  0|1) ;;
  *) printf 'ERROR: invalid inherited maintenance-lock fence marker\n' >&2; exit 1 ;;
esac
if [ "$INSTALLER_FENCE_READY" = 1 ]; then
  INSTALLER_FENCE_TOKEN="${SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_TOKEN:-}"
  INSTALLER_FENCE_SOURCE_ROOT="${SUB2API_AUTODEPLOY_EXEC_SOURCE_ROOT:-}"
  INSTALLER_FENCE_SUPERVISED="${SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_SUPERVISED:-0}"
  INSTALLER_FENCE_LEGACY="${SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_LEGACY:-}"
  [ -n "$INSTALLER_FENCE_TOKEN" ] && [ -n "$INSTALLER_FENCE_SOURCE_ROOT" ] \
    || { printf 'ERROR: inherited maintenance-lock fence is incomplete\n' >&2; exit 1; }
  [ "$INSTALLER_FENCE_SUPERVISED" = 1 ] \
    || { printf 'ERROR: inherited maintenance-lock fence is not supervised\n' >&2; exit 1; }
  case "${BASH_SOURCE[0]}" in
    /dev/fd/6) ;;
    *) printf 'ERROR: inherited maintenance-lock fence did not pin the installer source\n' >&2; exit 1 ;;
  esac
  python3 - "$INSTALLER_FENCE_TOKEN" <<'PY' || exit 1
import os
import stat
import sys

token = sys.argv[1]
test_mode = os.environ.get("SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS", "0")
if test_mode not in {"0", "1"}:
    raise SystemExit("invalid maintenance-lock test switch")
expected_uid = os.geteuid()
expected_gid = os.getegid()
if test_mode != "1" and expected_uid != 0:
    raise SystemExit("maintenance-lock fenced installer requires root")
try:
    descriptor = os.fstat(9)
    os.lseek(9, 0, os.SEEK_SET)
    contents = os.read(9, 256).decode("ascii")
except OSError as exc:
    raise SystemExit(f"cannot inspect inherited maintenance-lock nonce: {exc.strerror}")
if (
    not stat.S_ISREG(descriptor.st_mode)
    or descriptor.st_uid != expected_uid
    or descriptor.st_gid != expected_gid
    or stat.S_IMODE(descriptor.st_mode) != 0o600
    or descriptor.st_nlink != 0
    or contents != token
):
    raise SystemExit("inherited maintenance-lock nonce is unsafe")
try:
    source = os.fstat(6)
except OSError as exc:
    raise SystemExit(f"cannot inspect inherited installer source snapshot: {exc.strerror}")
if (
    not stat.S_ISREG(source.st_mode)
    or source.st_uid != expected_uid
    or source.st_gid != expected_gid
    or stat.S_IMODE(source.st_mode) != 0o600
    or source.st_nlink != 0
):
    raise SystemExit("inherited installer source snapshot is unsafe")
for lock_descriptor in (7, 8):
    try:
        os.fstat(lock_descriptor)
    except OSError:
        continue
    raise SystemExit(f"supervised child inherited maintenance lock FD {lock_descriptor}")
PY
  SOURCE_ROOT="$INSTALLER_FENCE_SOURCE_ROOT"
  VERIFIED_INSTALLER_FENCE_LEGACY="$INSTALLER_FENCE_LEGACY"
  # Bash continues to read this script from FD 6, so that unlinked snapshot
  # remains open until the interpreter is done. FD 9 and every fence-related
  # environment value are no longer needed after the one trusted check above;
  # close/unset them before any later external command can inherit a usable
  # re-entry capability.
  exec 9<&-
  unset SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_READY
  unset SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_SUPERVISED
  unset SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_TOKEN
  unset SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_LEGACY
  unset SUB2API_AUTODEPLOY_EXEC_SOURCE_ROOT
  unset INSTALLER_FENCE_READY INSTALLER_FENCE_TOKEN INSTALLER_FENCE_SOURCE_ROOT \
    INSTALLER_FENCE_SUPERVISED INSTALLER_FENCE_LEGACY
  INSTALLER_FENCE_VERIFIED=true
else
  SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
fi
ORIGINAL_ARGS=("$@")
APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
SCRIPT_DIR="${APP_DIR}/scripts"
CONFIG_FILE="${SUB2API_AUTODEPLOY_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"
UNIT_DIR="${SUB2API_AUTODEPLOY_UNIT_DIR:-/etc/systemd/system}"
RUNTIME_GUARD_EXECUTABLE="${SUB2API_RUNTIME_GUARD_EXECUTABLE:-/usr/local/libexec/sub2api-runtime-guard.sh}"
MAINTENANCE_LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-/run/sub2api-maintenance/sub2api-maintenance.lock}"
MAINTENANCE_LOCK_DIR=""
MAINTENANCE_LOCK_HELPER="${SOURCE_ROOT}/deploy/sub2api-maintenance-lock.sh"
INSTALLER_SOURCE_FILE="${SOURCE_ROOT}/deploy/install-autodeploy.sh"
MAINTENANCE_LOCK_HELPER_SOURCE_UID=""
STAGED_MAINTENANCE_LOCK_HELPER=""
STAGED_MAINTENANCE_LOCK_HELPER_DIR=""
LEGACY_MAINTENANCE_LOCK_FILE="/run/lock/sub2api-maintenance.lock"

PRODUCTION_BRANCH="${SUB2API_AUTODEPLOY_PRODUCTION_BRANCH:-}"
PRODUCTION_REPO_URL="${SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL:-}"
UPSTREAM_REPO_URL="${SUB2API_AUTODEPLOY_UPSTREAM_REPO_URL:-}"
HEALTH_URL="${SUB2API_PUBLIC_HEALTH_URL:-https://www.turtleligpt.com/health}"
HEALTH_RESOLVE="${SUB2API_PUBLIC_HEALTH_RESOLVE:-}"
GITHUB_IMAGE_SOURCE="${SUB2API_GITHUB_IMAGE_SOURCE:-}"
RECOVERY_MERGE_MAIN=true
REPLACE_CONFIG=false
ENABLE_TIMER=false
INSTALL_BLUE_GREEN_HELPER=false
ENABLE_RUNTIME_GUARD=true
DEPENDENCY_MODE="${SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE:-local}"
RUNTIME_NETWORK="${SUB2API_RUNTIME_GUARD_NETWORK:-sub2api_default}"
RUNTIME_DATA_VOLUME="${SUB2API_RUNTIME_GUARD_DATA_VOLUME:-sub2api_sub2api_data}"
CADDY_CONTAINER="${SUB2API_CADDY_CONTAINER:-sub2api-caddy}"
EXTERNAL_RUNTIME_ENV_FILE="${SUB2API_EXTERNAL_RUNTIME_ENV_FILE:-}"
EXTERNAL_CA_FILE="${SUB2API_EXTERNAL_CA_FILE:-}"
DUAL_NODE_RUNTIME_ENABLED="${SUB2API_DUAL_NODE_RUNTIME_ENABLED:-false}"
RUNTIME_CONFIG_EXPLICIT=false

usage() {
  cat <<'EOF'
Usage: install-autodeploy.sh [options]

Options:
  --production-branch BRANCH  Branch that holds the site's custom production code.
  --production-repo URL       Git URL of the production fork.
  --upstream-repo URL         Git URL of the official upstream.
  --health-url URL            Public URL checked after the blue-green switch.
  --health-resolve VALUE      Pin that URL to this origin as HOST:PORT:IPV4.
  --replace-config            Replace an existing /etc/sub2api-autodeploy.env.
  --enable-timer              Enable the periodic polling fallback (off by default).
  --install-blue-green-helper Replace the externally managed blue-green helper after backing it up.
  --dependency-mode MODE      Application dependency mode: local or external.
  --runtime-network NAME      Exact Docker network for application slots.
  --runtime-data-volume NAME  Exact Docker data volume for application slots.
  --caddy-container NAME      Exact Caddy container name.
  --external-runtime-env-file PATH
                              Root-owned 0600 external PG/Redis environment file.
  --external-ca-file PATH     Root-owned CA file for external PG/Redis TLS.
  --dual-node-runtime-enabled BOOL
                              Enable exact traffic/background/token runtime contract.
  --no-enable-runtime-guard   Install the runtime guard but leave its timer disabled.
  --no-enable                 Do not enable the timer (kept for compatibility).
  --help                      Show this help.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --production-branch)
      [ "$#" -ge 2 ] || { echo "--production-branch requires a value" >&2; exit 2; }
      PRODUCTION_BRANCH="$2"
      shift
      ;;
    --production-repo)
      [ "$#" -ge 2 ] || { echo "--production-repo requires a value" >&2; exit 2; }
      PRODUCTION_REPO_URL="$2"
      shift
      ;;
    --upstream-repo)
      [ "$#" -ge 2 ] || { echo "--upstream-repo requires a value" >&2; exit 2; }
      UPSTREAM_REPO_URL="$2"
      shift
      ;;
    --health-url)
      [ "$#" -ge 2 ] || { echo "--health-url requires a value" >&2; exit 2; }
      HEALTH_URL="$2"
      shift
      ;;
    --health-resolve)
      [ "$#" -ge 2 ] || { echo "--health-resolve requires a value" >&2; exit 2; }
      HEALTH_RESOLVE="$2"
      shift
      ;;
    --replace-config)
      REPLACE_CONFIG=true
      ;;
    --enable-timer)
      ENABLE_TIMER=true
      ;;
    --install-blue-green-helper)
      INSTALL_BLUE_GREEN_HELPER=true
      ;;
    --dependency-mode)
      [ "$#" -ge 2 ] || { echo "--dependency-mode requires a value" >&2; exit 2; }
      DEPENDENCY_MODE="$2"
      RUNTIME_CONFIG_EXPLICIT=true
      shift
      ;;
    --runtime-network)
      [ "$#" -ge 2 ] || { echo "--runtime-network requires a value" >&2; exit 2; }
      RUNTIME_NETWORK="$2"
      RUNTIME_CONFIG_EXPLICIT=true
      shift
      ;;
    --runtime-data-volume)
      [ "$#" -ge 2 ] || { echo "--runtime-data-volume requires a value" >&2; exit 2; }
      RUNTIME_DATA_VOLUME="$2"
      RUNTIME_CONFIG_EXPLICIT=true
      shift
      ;;
    --caddy-container)
      [ "$#" -ge 2 ] || { echo "--caddy-container requires a value" >&2; exit 2; }
      CADDY_CONTAINER="$2"
      RUNTIME_CONFIG_EXPLICIT=true
      shift
      ;;
    --external-runtime-env-file)
      [ "$#" -ge 2 ] || { echo "--external-runtime-env-file requires a value" >&2; exit 2; }
      EXTERNAL_RUNTIME_ENV_FILE="$2"
      RUNTIME_CONFIG_EXPLICIT=true
      shift
      ;;
    --external-ca-file)
      [ "$#" -ge 2 ] || { echo "--external-ca-file requires a value" >&2; exit 2; }
      EXTERNAL_CA_FILE="$2"
      RUNTIME_CONFIG_EXPLICIT=true
      shift
      ;;
    --dual-node-runtime-enabled)
      [ "$#" -ge 2 ] || { echo "--dual-node-runtime-enabled requires a value" >&2; exit 2; }
      DUAL_NODE_RUNTIME_ENABLED="$2"
      RUNTIME_CONFIG_EXPLICIT=true
      shift
      ;;
    --no-enable-runtime-guard)
      ENABLE_RUNTIME_GUARD=false
      ;;
    --no-enable)
      ENABLE_TIMER=false
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

die() {
  echo "ERROR: $*" >&2
  exit 1
}

# Production recognizes only the historical /run/lock pathname.  A separate
# fixture-only spelling lets the shell tests model its sticky-parent fence
# without creating /run on the developer machine.
if [ -n "${SUB2API_MAINTENANCE_LEGACY_LOCK_FILE_FOR_TESTS:-}" ]; then
  [ "${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-0}" = 1 ] \
    || die "SUB2API_MAINTENANCE_LEGACY_LOCK_FILE_FOR_TESTS is only available to maintenance-lock tests"
  LEGACY_MAINTENANCE_LOCK_FILE="$SUB2API_MAINTENANCE_LEGACY_LOCK_FILE_FOR_TESTS"
fi

# This literal production gate runs before the normal-checkout helper staging
# directory is created. The sourced helper repeats the complete path grammar
# and ownership preflight below, while this early check guarantees a hostile
# ambient override cannot cause even a private staging or lock-parent write.
case "${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-0}" in
  1) ;;
  0)
    [ "$MAINTENANCE_LOCK_FILE" = "/run/sub2api-maintenance/sub2api-maintenance.lock" ] \
      || die "maintenance lock path must be the canonical /run/sub2api-maintenance/sub2api-maintenance.lock: ${MAINTENANCE_LOCK_FILE}"
    ;;
  *) die "SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS must be 0 or 1" ;;
esac

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

validate_source_helper_file() {
  local label="$1" helper_path="$2" installer_path="$3"
  local helper_owner installer_owner

  validate_source_tree_file "executed installer source" "$installer_path"
  installer_owner="$(source_tree_owner "$installer_path")" \
    || die "cannot read owner for installer source: $installer_path"
  validate_source_tree_file "$label" "$helper_path"
  helper_owner="$(source_tree_owner "$helper_path")" \
    || die "cannot read owner for $label: $helper_path"
  [ "$helper_owner" = "$installer_owner" ] \
    || die "$label owner must match the executed installer source: $helper_path"
  validate_source_tree_ancestors "executed installer source" "$installer_path" "$installer_owner"
  validate_source_tree_ancestors "$label" "$helper_path" "$installer_owner"
  MAINTENANCE_LOCK_HELPER_SOURCE_UID="$helper_owner"
}

source_tree_owner() {
  stat -c '%u' "$1" 2>/dev/null || stat -f '%u' "$1"
}

source_tree_mode() {
  stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1"
}

staged_helper_digest() {
  local digest

  # This function is called only after the helper is in the private staging
  # directory; never hash the mutable checkout pathname here.
  digest="$(sha256sum "$1")" || return 1
  digest="${digest%%[[:space:]]*}"
  [ -n "$digest" ] || return 1
  printf '%s\n' "$digest"
}

validate_source_tree_file() {
  local label="$1" path="$2" mode permissions

  [ -f "$path" ] && [ ! -L "$path" ] \
    || die "$label is not a regular non-symlink file: $path"
  source_tree_owner "$path" >/dev/null \
    || die "cannot read owner for $label: $path"
  mode="$(source_tree_mode "$path")" \
    || die "cannot read permissions for $label: $path"
  case "$mode" in ''|*[!0-7]*) die "$label has unsupported permissions: $path" ;; esac
  permissions=$((8#$mode))
  [ $((permissions & 0022)) -eq 0 ] \
    || die "$label must not be group/other writable: $path"
}

validate_source_tree_directory() {
  local label="$1" directory="$2" expected_owner="$3"
  local owner mode permissions

  [ ! -L "$directory" ] || die "$label ancestor is a symlink: $directory"
  [ -d "$directory" ] || die "$label ancestor is not a directory: $directory"
  owner="$(source_tree_owner "$directory")" \
    || die "cannot read owner for $label ancestor: $directory"
  [ "$owner" = 0 ] || [ "$owner" = "$expected_owner" ] \
    || die "$label ancestor has an unexpected owner: $directory"
  mode="$(source_tree_mode "$directory")" \
    || die "cannot read permissions for $label ancestor: $directory"
  case "$mode" in ''|*[!0-7]*) die "$label ancestor has unsupported permissions: $directory" ;; esac
  permissions=$((8#$mode))
  [ $((permissions & 0022)) -eq 0 ] \
    || die "$label ancestor is group/other writable: $directory"
}

validate_source_tree_ancestors() {
  local label="$1" path="$2" expected_owner="$3"
  local parent remaining current component

  case "$path" in
    /*) ;;
    *) die "$label has a non-absolute path: $path" ;;
  esac
  parent="${path%/*}"
  [ -n "$parent" ] || parent="/"
  remaining="${parent#/}"
  current="/"
  validate_source_tree_directory "$label" "$current" "$expected_owner"
  while [ -n "$remaining" ]; do
    component="${remaining%%/*}"
    if [ "$component" = "$remaining" ]; then
      remaining=""
    else
      remaining="${remaining#*/}"
    fi
    [ -n "$component" ] || die "$label ancestor path is malformed: $path"
    if [ "$current" = / ]; then
      current="/${component}"
    else
      current="${current}/${component}"
    fi
    validate_source_tree_directory "$label" "$current" "$expected_owner"
  done
}

validate_root_owned_directory_chain() {
  local label="$1" path="$2"
  local remaining current component owner mode permissions

  case "$path" in
    /*) ;;
    *) die "$label has a non-absolute path: $path" ;;
  esac
  remaining="${path#/}"
  current="/"
  while :; do
    [ ! -L "$current" ] || die "$label ancestor is a symlink: $current"
    [ -d "$current" ] || die "$label ancestor is not a directory: $current"
    owner="$(source_tree_owner "$current")" \
      || die "cannot read owner for $label ancestor: $current"
    [ "$owner" = 0 ] || die "$label ancestor must be root-owned: $current"
    mode="$(source_tree_mode "$current")" \
      || die "cannot read permissions for $label ancestor: $current"
    case "$mode" in ''|*[!0-7]*) die "$label ancestor has unsupported permissions: $current" ;; esac
    permissions=$((8#$mode))
    [ $((permissions & 0022)) -eq 0 ] \
      || die "$label ancestor is group/other writable: $current"
    [ -n "$remaining" ] || break
    component="${remaining%%/*}"
    if [ "$component" = "$remaining" ]; then
      remaining=""
    else
      remaining="${remaining#*/}"
    fi
    [ -n "$component" ] || die "$label ancestor path is malformed: $path"
    if [ "$current" = / ]; then
      current="/${component}"
    else
      current="${current}/${component}"
    fi
  done
}

cleanup_staged_maintenance_lock_helper() {
  [ -z "$STAGED_MAINTENANCE_LOCK_HELPER" ] || rm -f -- "$STAGED_MAINTENANCE_LOCK_HELPER"
  [ -z "$STAGED_MAINTENANCE_LOCK_HELPER_DIR" ] || rmdir -- "$STAGED_MAINTENANCE_LOCK_HELPER_DIR" 2>/dev/null || true
  STAGED_MAINTENANCE_LOCK_HELPER=""
  STAGED_MAINTENANCE_LOCK_HELPER_DIR=""
}

stage_maintenance_lock_helper_from_fd() {
  local source_path="$1" stage_path="$2" expected_source_uid="$3" result

  if ! result="$(python3 - "$source_path" "$stage_path" "$expected_source_uid" <<'PY'
import hashlib
import os
import stat
import sys
import time

source_path, stage_path, expected_uid_text = sys.argv[1:]


def fail(message):
    print(f"maintenance lock helper staging failed: {message}", file=sys.stderr)
    raise SystemExit(1)


try:
    expected_uid = int(expected_uid_text, 10)
except ValueError:
    fail("maintenance lock helper has an invalid expected owner")

no_follow = getattr(os, "O_NOFOLLOW", None)
if no_follow is None:
    fail("Python O_NOFOLLOW support is required")

source_fd = -1
stage_fd = -1
try:
    try:
        source_fd = os.open(source_path, os.O_RDONLY | no_follow)
    except OSError as exc:
        fail(f"could not open source without following symlinks: {exc.strerror}")

    source_before = os.fstat(source_fd)
    if not stat.S_ISREG(source_before.st_mode):
        fail("source descriptor is not a regular file")
    if source_before.st_uid != expected_uid:
        fail("source descriptor owner changed while staging")
    if stat.S_IMODE(source_before.st_mode) & 0o022:
        fail("source descriptor is group/other writable")
    source_size = source_before.st_size

    barrier = os.environ.get("SUB2API_MAINTENANCE_LOCK_TEST_AFTER_SOURCE_OPEN_BARRIER")
    if barrier:
        if os.environ.get("SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS") != "1":
            fail("source-open barrier is only available to maintenance-lock tests")
        try:
            barrier_fd = os.open(
                barrier,
                os.O_WRONLY | os.O_CREAT | os.O_EXCL | no_follow,
                0o600,
            )
        except OSError as exc:
            fail(f"could not create source-open test barrier: {exc.strerror}")
        try:
            os.write(barrier_fd, b"source-open\\n")
        finally:
            os.close(barrier_fd)
        deadline = time.monotonic() + 10
        while True:
            try:
                os.lstat(f"{barrier}.continue")
                break
            except FileNotFoundError:
                if time.monotonic() >= deadline:
                    fail("source-open test barrier timed out")
                time.sleep(0.01)

    try:
        stage_fd = os.open(
            stage_path,
            os.O_WRONLY | os.O_CREAT | os.O_EXCL | no_follow,
            0o600,
        )
    except OSError as exc:
        fail(f"could not create private staged helper: {exc.strerror}")
    stage_before = os.fstat(stage_fd)
    if not stat.S_ISREG(stage_before.st_mode):
        fail("staged helper descriptor is not a regular file")
    if stage_before.st_uid != os.geteuid():
        fail("staged helper descriptor has an unexpected owner")

    copied_hash = hashlib.sha256()
    copied_size = 0
    while True:
        chunk = os.read(source_fd, 1024 * 1024)
        if not chunk:
            break
        copied_hash.update(chunk)
        copied_size += len(chunk)
        view = memoryview(chunk)
        while view:
            written = os.write(stage_fd, view)
            if written <= 0:
                fail("could not write private staged helper")
            view = view[written:]
    os.fchmod(stage_fd, 0o600)
    os.fsync(stage_fd)
    stage_after = os.fstat(stage_fd)
    if (
        not stat.S_ISREG(stage_after.st_mode)
        or stat.S_IMODE(stage_after.st_mode) != 0o600
        or stage_after.st_nlink != 1
    ):
        fail("staged helper must be a regular mode-0600 file")
    if stage_after.st_uid != os.geteuid():
        fail("staged helper owner changed while staging")

    source_after = os.fstat(source_fd)
    if not stat.S_ISREG(source_after.st_mode) or source_after.st_uid != expected_uid:
        fail("source descriptor changed while staging")
    if stat.S_IMODE(source_after.st_mode) & 0o022:
        fail("source descriptor became group/other writable")
    if source_after.st_size != source_size or copied_size != source_size:
        fail("source size changed while staging")

    os.lseek(source_fd, 0, os.SEEK_SET)
    source_after_hash = hashlib.sha256()
    while True:
        chunk = os.read(source_fd, 1024 * 1024)
        if not chunk:
            break
        source_after_hash.update(chunk)
    if source_after_hash.digest() != copied_hash.digest():
        fail("source content changed while staging")

    try:
        source_path_after = os.lstat(source_path)
    except OSError:
        fail("source path changed while staging")
    if (
        not stat.S_ISREG(source_path_after.st_mode)
        or source_path_after.st_dev != source_before.st_dev
        or source_path_after.st_ino != source_before.st_ino
    ):
        fail("source path changed while staging")

    print(copied_hash.hexdigest())
finally:
    if stage_fd >= 0:
        os.close(stage_fd)
    if source_fd >= 0:
        os.close(source_fd)
PY
)"; then
    printf '%s\n' "$result" >&2
    return 1
  fi
  printf '%s\n' "$result"
}

stage_maintenance_lock_helper() {
  local source_path="$1"
  local source_digest staged_digest stage_mode stage_owner stage_root stage_template

  # /run is an existing root-owned non-writable container on supported hosts.
  # The generated directory is checked again before it ever receives source
  # content, so root never sources directly from the user-owned checkout.
  stage_root="${SUB2API_MAINTENANCE_LOCK_HELPER_STAGE_ROOT:-/run}"
  if [ "$stage_root" != /run ] \
    && [ "${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-0}" != 1 ]; then
    die "SUB2API_MAINTENANCE_LOCK_HELPER_STAGE_ROOT is only available to maintenance-lock tests"
  fi
  case "$stage_root" in
    /*) ;;
    *) die "maintenance lock helper staging directory must be absolute: $stage_root" ;;
  esac
  case "$stage_root" in
    *$'\n'*|*$'\r'*|*'//'*|*/./*|*/../*|*/) die "maintenance lock helper staging directory is malformed: $stage_root" ;;
  esac
  validate_root_owned_directory_chain "maintenance lock helper staging directory" "$stage_root"
  stage_template="${stage_root}/sub2api-maintenance-helper.XXXXXX"
  STAGED_MAINTENANCE_LOCK_HELPER_DIR="$(umask 077; mktemp -d "$stage_template")" \
    || die "could not create a private maintenance lock helper staging directory"
  validate_root_owned_directory_chain \
    "maintenance lock helper staging directory" "$STAGED_MAINTENANCE_LOCK_HELPER_DIR"
  stage_owner="$(source_tree_owner "$STAGED_MAINTENANCE_LOCK_HELPER_DIR")" \
    || { cleanup_staged_maintenance_lock_helper; die "cannot read maintenance lock helper staging directory owner"; }
  stage_mode="$(source_tree_mode "$STAGED_MAINTENANCE_LOCK_HELPER_DIR")" \
    || { cleanup_staged_maintenance_lock_helper; die "cannot read maintenance lock helper staging directory permissions"; }
  [ "$stage_owner" = 0 ] && [ "$stage_mode" = 700 ] \
    || { cleanup_staged_maintenance_lock_helper; die "maintenance lock helper staging directory must be root-owned mode 0700"; }

  STAGED_MAINTENANCE_LOCK_HELPER="${STAGED_MAINTENANCE_LOCK_HELPER_DIR}/sub2api-maintenance-lock.sh"
  if ! source_digest="$(stage_maintenance_lock_helper_from_fd \
    "$source_path" "$STAGED_MAINTENANCE_LOCK_HELPER" "$MAINTENANCE_LOCK_HELPER_SOURCE_UID")"; then
    cleanup_staged_maintenance_lock_helper
    die "could not stage maintenance lock helper safely"
  fi
  staged_digest="$(staged_helper_digest "$STAGED_MAINTENANCE_LOCK_HELPER")" \
    || { cleanup_staged_maintenance_lock_helper; die "cannot hash staged maintenance lock helper"; }
  if [ "$source_digest" != "$staged_digest" ]; then
    cleanup_staged_maintenance_lock_helper
    die "staged maintenance lock helper digest mismatch"
  fi
  validate_root_owned_directory_chain \
    "staged maintenance lock helper" "$STAGED_MAINTENANCE_LOCK_HELPER_DIR"
  validate_source_tree_file "staged maintenance lock helper" "$STAGED_MAINTENANCE_LOCK_HELPER"
  stage_owner="$(source_tree_owner "$STAGED_MAINTENANCE_LOCK_HELPER")" \
    || { cleanup_staged_maintenance_lock_helper; die "cannot read staged maintenance lock helper owner"; }
  stage_mode="$(source_tree_mode "$STAGED_MAINTENANCE_LOCK_HELPER")" \
    || { cleanup_staged_maintenance_lock_helper; die "cannot read staged maintenance lock helper permissions"; }
  [ "$stage_owner" = 0 ] && [ "$stage_mode" = 600 ] \
    || { cleanup_staged_maintenance_lock_helper; die "staged maintenance lock helper must be root-owned mode 0600"; }
}

require_simple_value() {
  local name="$1"
  local value="$2"
  [ -n "$value" ] || die "$name must not be empty"
  case "$value" in
    *$'\n'*|*$'\r'*|*' '*) die "$name must not contain whitespace" ;;
  esac
}

require_docker_name() {
  local name="$1" value="$2"
  case "$value" in
    ''|[!A-Za-z0-9]*|*[!A-Za-z0-9_.-]*) die "$name must be a valid Docker object name" ;;
  esac
}

require_absolute_path() {
  local name="$1" value="$2"
  case "$value" in
    /*) ;;
    *) die "$name must be an absolute path" ;;
  esac
  case "$value" in
    *$'\n'*|*$'\r'*|*' '*) die "$name must not contain whitespace" ;;
  esac
}

validate_health_resolve() {
  [ -n "$1" ] || return 0
  python3 - "$1" "$2" <<'PY'
import ipaddress
import re
import sys
import urllib.parse

value = sys.argv[1]
url = sys.argv[2]
parts = value.split(":")
if len(parts) != 3:
    raise SystemExit("SUB2API_PUBLIC_HEALTH_RESOLVE must be HOST:PORT:IPV4")
host, port, address = parts
if not re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?", host) or ".." in host:
    raise SystemExit("SUB2API_PUBLIC_HEALTH_RESOLVE has an invalid host")
if not port.isdigit() or not 1 <= int(port) <= 65535:
    raise SystemExit("SUB2API_PUBLIC_HEALTH_RESOLVE has an invalid port")
if not isinstance(ipaddress.ip_address(address), ipaddress.IPv4Address):
    raise SystemExit("SUB2API_PUBLIC_HEALTH_RESOLVE must use IPv4")
parsed = urllib.parse.urlsplit(url)
if parsed.scheme not in {"http", "https"} or not parsed.hostname or parsed.username or parsed.password:
    raise SystemExit("SUB2API_PUBLIC_HEALTH_URL has an invalid authority")
url_port = parsed.port or (443 if parsed.scheme == "https" else 80)
if parsed.hostname != host or url_port != int(port):
    raise SystemExit("SUB2API_PUBLIC_HEALTH_RESOLVE host/port must match SUB2API_PUBLIC_HEALTH_URL")
PY
}

derive_remote_url() {
  local preferred_remote="$1"
  local fallback_remote="$2"
  git -C "$SOURCE_ROOT" remote get-url "$preferred_remote" 2>/dev/null \
    || git -C "$SOURCE_ROOT" remote get-url "$fallback_remote" 2>/dev/null \
    || true
}

derive_github_image_source() {
  local repository_url="$1"
  case "$repository_url" in
    https://github.com/*)
      printf '%s\n' "${repository_url%.git}"
      ;;
    git@github.com:*)
      printf 'https://github.com/%s\n' "${repository_url#git@github.com:}" \
        | sed 's/\.git$//'
      ;;
    ssh://git@github.com/*)
      printf 'https://github.com/%s\n' "${repository_url#ssh://git@github.com/}" \
        | sed 's/\.git$//'
      ;;
    *)
      return 1
      ;;
  esac
}

[ "$(id -u)" -eq 0 ] || die "run this installer as root on the Sub2API server"
case "$MAINTENANCE_LOCK_FILE" in
  /*/*) ;;
  *) die "SUB2API_MAINTENANCE_LOCK_FILE must name a file below an absolute private directory" ;;
esac
for command_name in git id install mktemp rm rmdir sha256sum systemctl docker curl flock grep head python3 sed stat tar wc zstd; do
  require_cmd "$command_name"
done
[ -d "$APP_DIR" ] || die "Sub2API application directory does not exist: $APP_DIR"

if [ -z "$PRODUCTION_BRANCH" ]; then
  PRODUCTION_BRANCH="$(git -C "$SOURCE_ROOT" branch --show-current 2>/dev/null || true)"
fi
if [ "$PRODUCTION_BRANCH" = "main" ]; then
  RECOVERY_MERGE_MAIN=false
fi
if [ -z "$PRODUCTION_REPO_URL" ]; then
  PRODUCTION_REPO_URL="$(derive_remote_url fork origin)"
fi
if [ -z "$UPSTREAM_REPO_URL" ]; then
  UPSTREAM_REPO_URL="$(derive_remote_url origin fork)"
fi
if [ -z "$GITHUB_IMAGE_SOURCE" ]; then
  GITHUB_IMAGE_SOURCE="$(derive_github_image_source "$PRODUCTION_REPO_URL")" \
    || die "set SUB2API_GITHUB_IMAGE_SOURCE to the canonical https://github.com/OWNER/REPO URL"
fi

require_simple_value SUB2API_AUTODEPLOY_PRODUCTION_BRANCH "$PRODUCTION_BRANCH"
require_simple_value SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL "$PRODUCTION_REPO_URL"
if [ -n "$UPSTREAM_REPO_URL" ]; then
  require_simple_value SUB2API_AUTODEPLOY_UPSTREAM_REPO_URL "$UPSTREAM_REPO_URL"
fi
require_simple_value SUB2API_PUBLIC_HEALTH_URL "$HEALTH_URL"
validate_health_resolve "$HEALTH_RESOLVE" "$HEALTH_URL"
require_simple_value SUB2API_GITHUB_IMAGE_SOURCE "$GITHUB_IMAGE_SOURCE"
require_simple_value SUB2API_APP_DIR "$APP_DIR"
case "$DEPENDENCY_MODE" in
  local|external) ;;
  *) die "SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE must be local or external" ;;
esac
case "$DUAL_NODE_RUNTIME_ENABLED" in
  true|false) ;;
  *) die "SUB2API_DUAL_NODE_RUNTIME_ENABLED must be true or false" ;;
esac
require_docker_name SUB2API_RUNTIME_GUARD_NETWORK "$RUNTIME_NETWORK"
require_docker_name SUB2API_RUNTIME_GUARD_DATA_VOLUME "$RUNTIME_DATA_VOLUME"
require_docker_name SUB2API_CADDY_CONTAINER "$CADDY_CONTAINER"
if [ "$DEPENDENCY_MODE" = external ]; then
  require_absolute_path SUB2API_EXTERNAL_RUNTIME_ENV_FILE "$EXTERNAL_RUNTIME_ENV_FILE"
  require_absolute_path SUB2API_EXTERNAL_CA_FILE "$EXTERNAL_CA_FILE"
fi
case "$GITHUB_IMAGE_SOURCE" in
  https://github.com/*) ;;
  *) die "SUB2API_GITHUB_IMAGE_SOURCE must be an https://github.com URL" ;;
esac
git -C "$SOURCE_ROOT" check-ref-format --branch "$PRODUCTION_BRANCH" >/dev/null

for file in \
  deploy/sub2api-autodeploy.sh \
  deploy/sub2api-github-image-release.sh \
  deploy/sub2api-server-release.sh \
  deploy/sub2api-drain-monitor.sh \
  deploy/sub2api-maintenance-lock.sh \
  deploy/sub2api-runtime-guard.sh \
  deploy/sub2api-github-deploy-trigger.sh \
  deploy/sub2api-cert-receiver.sh \
  deploy/sub2api-cert-deploy-trigger.sh \
  deploy/install-sub2api-cert-receiver.sh \
  deploy/sub2api-node-state.sh \
  deploy/sub2api-autodeploy.service \
  deploy/sub2api-autodeploy.timer \
  deploy/sub2api-runtime-guard.service \
  deploy/sub2api-runtime-guard.timer; do
  [ -r "${SOURCE_ROOT}/${file}" ] || die "installer source is incomplete: ${file}"
done
[ "$INSTALL_BLUE_GREEN_HELPER" != true ] \
  || [ -r "${SOURCE_ROOT}/deploy/sub2api-blue-green-release.sh" ] \
  || die "installer source is incomplete: deploy/sub2api-blue-green-release.sh"

bash -n "${SOURCE_ROOT}/deploy/sub2api-autodeploy.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-github-image-release.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-server-release.sh"
[ "$INSTALL_BLUE_GREEN_HELPER" != true ] \
  || bash -n "${SOURCE_ROOT}/deploy/sub2api-blue-green-release.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-drain-monitor.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-runtime-guard.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-github-deploy-trigger.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-cert-receiver.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-cert-deploy-trigger.sh"
bash -n "${SOURCE_ROOT}/deploy/install-sub2api-cert-receiver.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-node-state.sh"

# This source helper shares the ownership trust boundary of the installer the
# operator explicitly invoked through sudo: both source files and every
# ancestor are non-symlink and non-writable to group/other, while a normal
# checkout owner remains valid. Python opens the checked helper with
# O_NOFOLLOW, validates its descriptor, copies that descriptor directly into
# a new root-only /run file with O_EXCL|O_NOFOLLOW, and binds the final lstat
# pathname back to that descriptor before source. Only the private staging
# file is hashed, syntax-checked, and sourced. This prevents a leaf
# replacement between validation and source without falsely requiring a
# root-owned checkout. Its pure preflight validates the target/ancestor chain
# without creating a directory, so the later install -d cannot chmod a path
# selected through .. or a symlink component.
validate_source_helper_file "maintenance lock helper" "$MAINTENANCE_LOCK_HELPER" "$INSTALLER_SOURCE_FILE"
trap cleanup_staged_maintenance_lock_helper EXIT
stage_maintenance_lock_helper "$MAINTENANCE_LOCK_HELPER"
# The copied file is below the private staging parent; never syntax-check the
# mutable checkout pathname after its source-trust validation.
bash -n "$STAGED_MAINTENANCE_LOCK_HELPER"
# shellcheck disable=SC1090,SC1091 # The staged copy has a private root-only parent.
if ! . "$STAGED_MAINTENANCE_LOCK_HELPER"; then
  cleanup_staged_maintenance_lock_helper
  die "could not load staged maintenance lock helper"
fi
cleanup_staged_maintenance_lock_helper
if ! sub2api_maintenance_lock_validate_install_target "$MAINTENANCE_LOCK_FILE"; then
  die "unsafe maintenance lock target: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
fi
MAINTENANCE_LOCK_DIR="$SUB2API_MAINTENANCE_LOCK_PARENT"

configured_maintenance_lock=""
if [ -e "$CONFIG_FILE" ]; then
  configured_maintenance_lock="$(sed -n 's/^SUB2API_MAINTENANCE_LOCK_FILE=//p' "$CONFIG_FILE" | tail -n 1)"
fi

# A retained configuration participates in the same single-lock contract as
# the caller's environment.  Reject drift before the fence shim creates even
# the canonical parent/file; an ordinary non-replace run must be pure when an
# existing config points at another inode.
if [ -e "$CONFIG_FILE" ] && [ "$REPLACE_CONFIG" != "true" ]; then
  if [ "$configured_maintenance_lock" = "$LEGACY_MAINTENANCE_LOCK_FILE" ]; then
    die "existing maintenance lock uses retired ${LEGACY_MAINTENANCE_LOCK_FILE}; set a private root-owned path or rerun with --replace-config"
  fi
  [ "$configured_maintenance_lock" = "$MAINTENANCE_LOCK_FILE" ] \
    || die "existing SUB2API_MAINTENANCE_LOCK_FILE must equal ${MAINTENANCE_LOCK_FILE}; rerun with --replace-config"
fi

# Every installer run holds the canonical fence before it writes a config,
# installs a script, or asks systemd to reload.  The only supported migration
# additionally holds the exact retired /run/lock inode through the whole run,
# closing the split-lock window between legacy release workers and the new
# private lock. The helper's Python supervisor holds FD 7/8 while it waits for
# this pinned-FD Bash child, so later install/systemctl descendants cannot
# retain either maintenance lock after the installer exits.
legacy_fence_path=""
if [ "$REPLACE_CONFIG" = true ] \
  && [ "$configured_maintenance_lock" = "$LEGACY_MAINTENANCE_LOCK_FILE" ]; then
  legacy_fence_path="$LEGACY_MAINTENANCE_LOCK_FILE"
fi
if [ "$INSTALLER_FENCE_VERIFIED" = true ]; then
  [ "$VERIFIED_INSTALLER_FENCE_LEGACY" = "$legacy_fence_path" ] \
    || die "inherited maintenance-lock fence does not match the configuration migration"
else
  # This replaces the current shell on success; a failure is printed by the
  # no-follow fence shim and exits before any deployment mutation.
  sub2api_maintenance_lock_exec_installer_with_fences \
    "$INSTALLER_SOURCE_FILE" "$SOURCE_ROOT" "$MAINTENANCE_LOCK_HELPER_SOURCE_UID" \
    "$MAINTENANCE_LOCK_FILE" "$legacy_fence_path" "${ORIGINAL_ARGS[@]}"
  die "could not establish maintenance lock installation fence"
fi

if [ -e "$CONFIG_FILE" ] && [ "$REPLACE_CONFIG" != "true" ]; then
  [ "$RUNTIME_CONFIG_EXPLICIT" != true ] \
    || die "runtime configuration options require --replace-config when ${CONFIG_FILE} already exists"
  configured_app_dir="$(sed -n 's/^SUB2API_APP_DIR=//p' "$CONFIG_FILE" | tail -n 1)"
  if [ -n "$configured_app_dir" ] && [ "$configured_app_dir" != "$APP_DIR" ]; then
    die "existing SUB2API_APP_DIR=${configured_app_dir} does not match installer APP_DIR=${APP_DIR}; use the matching directory or --replace-config"
  fi
  if [ -z "$configured_app_dir" ]; then
    printf 'SUB2API_APP_DIR=%s\n' "$APP_DIR" >>"$CONFIG_FILE"
  fi
  echo "Keeping existing automatic-release configuration: ${CONFIG_FILE}"
else
  config_temp="$(mktemp)"
  umask 077
  {
    printf '%s\n' '# Managed by deploy/install-autodeploy.sh'
    printf 'SUB2API_APP_DIR=%s\n' "$APP_DIR"
    printf 'SUB2API_AUTODEPLOY_PRODUCTION_REMOTE=%s\n' 'fork'
    printf 'SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL=%s\n' "$PRODUCTION_REPO_URL"
    printf 'SUB2API_AUTODEPLOY_PRODUCTION_BRANCH=%s\n' "$PRODUCTION_BRANCH"
    printf 'SUB2API_AUTODEPLOY_MERGE_MAIN=%s\n' "$RECOVERY_MERGE_MAIN"
    printf 'SUB2API_AUTODEPLOY_MAIN_REMOTE=%s\n' 'fork'
    printf 'SUB2API_AUTODEPLOY_MAIN_REPO_URL=%s\n' "$PRODUCTION_REPO_URL"
    printf 'SUB2API_AUTODEPLOY_MAIN_BRANCH=%s\n' 'main'
    # Official upstream updates are merged into fork/main deliberately before
    # this service is triggered; never merge them from the production server.
    printf 'SUB2API_AUTODEPLOY_MERGE_UPSTREAM=%s\n' 'false'
    printf 'SUB2API_AUTODEPLOY_UPSTREAM_REMOTE=%s\n' 'origin'
    printf 'SUB2API_AUTODEPLOY_UPSTREAM_REPO_URL=%s\n' "$UPSTREAM_REPO_URL"
    printf 'SUB2API_AUTODEPLOY_UPSTREAM_BRANCH=%s\n' 'main'
    printf 'SUB2API_GITHUB_IMAGE_SOURCE=%s\n' "$GITHUB_IMAGE_SOURCE"
    printf 'SUB2API_GITHUB_IMAGE_MAX_BYTES=%s\n' '1073741824'
    printf 'SUB2API_PUBLIC_HEALTH_URL=%s\n' "$HEALTH_URL"
    if [ -n "$HEALTH_RESOLVE" ]; then
      printf 'SUB2API_PUBLIC_HEALTH_RESOLVE=%s\n' "$HEALTH_RESOLVE"
    fi
    printf 'SUB2API_MAINTENANCE_LOCK_FILE=%s\n' "$MAINTENANCE_LOCK_FILE"
    printf 'SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE=%s\n' "$DEPENDENCY_MODE"
    printf 'SUB2API_RUNTIME_GUARD_NETWORK=%s\n' "$RUNTIME_NETWORK"
    printf 'SUB2API_RUNTIME_GUARD_DATA_VOLUME=%s\n' "$RUNTIME_DATA_VOLUME"
    printf 'SUB2API_CADDY_CONTAINER=%s\n' "$CADDY_CONTAINER"
    if [ "$DEPENDENCY_MODE" = external ]; then
      printf 'SUB2API_EXTERNAL_RUNTIME_ENV_FILE=%s\n' "$EXTERNAL_RUNTIME_ENV_FILE"
      printf 'SUB2API_EXTERNAL_CA_FILE=%s\n' "$EXTERNAL_CA_FILE"
    fi
    printf 'SUB2API_RUNTIME_GUARD_RETRY_ATTEMPTS=%s\n' '20'
    printf 'SUB2API_RUNTIME_GUARD_RETRY_INTERVAL_SECONDS=%s\n' '3'
    printf 'SUB2API_RUNTIME_GUARD_COOLDOWN_SECONDS=%s\n' '300'
    printf 'SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_ATTEMPTS=%s\n' '3'
    printf 'SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_INTERVAL_SECONDS=%s\n' '3'
    printf 'SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_MAX_TIME_SECONDS=%s\n' '20'
    printf 'SUB2API_AUTODEPLOY_LOCK_WAIT_SECONDS=%s\n' '900'
    printf 'SUB2API_AUTODEPLOY_FAILURE_RETRY_SECONDS=%s\n' '1800'
    printf 'SUB2API_RELEASE_ALLOW_PREEXISTING_DRAINING_CONTAINER=%s\n' 'false'
    printf 'SUB2API_RELEASE_BACKGROUND_MODE=%s\n' 'activate'
    printf 'SUB2API_DUAL_NODE_RUNTIME_ENABLED=%s\n' "$DUAL_NODE_RUNTIME_ENABLED"
  } >"$config_temp"
  install -D -m 600 "$config_temp" "$CONFIG_FILE"
  rm -f "$config_temp"
  echo "Installed automatic-release configuration: ${CONFIG_FILE}"
fi

install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-autodeploy.sh" \
  "${SCRIPT_DIR}/sub2api-autodeploy.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-github-image-release.sh" \
  "${SCRIPT_DIR}/sub2api-github-image-release.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-server-release.sh" \
  "${SCRIPT_DIR}/sub2api-server-release.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-maintenance-lock.sh" \
  "${SCRIPT_DIR}/sub2api-maintenance-lock.sh"
if [ "$INSTALL_BLUE_GREEN_HELPER" = true ]; then
  helper_backup_dir="${APP_DIR}/backups/blue-green-helper-$(date -u +%Y%m%dT%H%M%SZ)"
  install -d -m 700 "$helper_backup_dir"
  if [ -f "${SCRIPT_DIR}/sub2api-blue-green-release.sh" ] \
    && [ ! -L "${SCRIPT_DIR}/sub2api-blue-green-release.sh" ]; then
    install -m 600 "${SCRIPT_DIR}/sub2api-blue-green-release.sh" \
      "${helper_backup_dir}/sub2api-blue-green-release.sh"
  fi
  install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-blue-green-release.sh" \
    "${SCRIPT_DIR}/sub2api-blue-green-release.sh"
  printf 'Installed blue-green helper; rollback backup: %s\n' "$helper_backup_dir"
fi
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-drain-monitor.sh" \
  "${SCRIPT_DIR}/sub2api-drain-monitor.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-runtime-guard.sh" \
  "${SCRIPT_DIR}/sub2api-runtime-guard.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-runtime-guard.sh" \
  "$RUNTIME_GUARD_EXECUTABLE"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-maintenance-lock.sh" \
  "${RUNTIME_GUARD_EXECUTABLE%/*}/sub2api-maintenance-lock.sh"
install -D -m 755 "${SOURCE_ROOT}/deploy/sub2api-github-deploy-trigger.sh" \
  "${SCRIPT_DIR}/sub2api-github-deploy-trigger.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-cert-receiver.sh" \
  "${SCRIPT_DIR}/sub2api-cert-receiver.sh"
install -D -m 755 "${SOURCE_ROOT}/deploy/sub2api-cert-deploy-trigger.sh" \
  "${SCRIPT_DIR}/sub2api-cert-deploy-trigger.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/install-sub2api-cert-receiver.sh" \
  "${SCRIPT_DIR}/install-sub2api-cert-receiver.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-node-state.sh" \
  "${SCRIPT_DIR}/sub2api-node-state.sh"
install -d -o root -g root -m 700 "$MAINTENANCE_LOCK_DIR"
install -D -m 644 "${SOURCE_ROOT}/deploy/sub2api-autodeploy.service" \
  "${UNIT_DIR}/sub2api-autodeploy.service"
install -D -m 644 "${SOURCE_ROOT}/deploy/sub2api-autodeploy.timer" \
  "${UNIT_DIR}/sub2api-autodeploy.timer"
install -D -m 644 "${SOURCE_ROOT}/deploy/sub2api-runtime-guard.service" \
  "${UNIT_DIR}/sub2api-runtime-guard.service"
install -D -m 644 "${SOURCE_ROOT}/deploy/sub2api-runtime-guard.timer" \
  "${UNIT_DIR}/sub2api-runtime-guard.timer"

systemctl daemon-reload
if [ "$ENABLE_RUNTIME_GUARD" = "true" ]; then
  systemctl enable --now sub2api-runtime-guard.timer
  echo "Enabled sub2api-runtime-guard.timer (repairs the active production slot)."
else
  systemctl disable --now sub2api-runtime-guard.timer >/dev/null 2>&1 || true
  echo "Installed sub2api-runtime-guard.timer; it remains disabled until runtime state and mounts are verified."
fi
if [ "$ENABLE_TIMER" = "true" ]; then
  systemctl enable --now sub2api-autodeploy.timer
  echo "Enabled sub2api-autodeploy.timer (checks every five minutes)."
else
  systemctl disable --now sub2api-autodeploy.timer >/dev/null 2>&1 || true
  echo "Installed GitHub image receiver and recovery service; polling timer is disabled."
fi

echo "Validate without releasing: ${SCRIPT_DIR}/sub2api-autodeploy.sh --check"
echo "Show timer: systemctl list-timers sub2api-autodeploy.timer"
echo "Show runtime recovery: systemctl status sub2api-runtime-guard.timer"
