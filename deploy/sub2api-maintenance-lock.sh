#!/usr/bin/env bash

# Sourceable guard for the one host-wide Sub2API maintenance lock.  The lock
# directory is intentionally outside /run/lock: that path is commonly
# world-writable and lets an unprivileged account pre-create a lock inode and
# hold it indefinitely.
#
# Bash has no O_NOFOLLOW equivalent for redirections.  The security boundary is
# therefore a root-owned, mode-0700 parent that no unprivileged account can
# replace entries inside.  We validate the ancestor chain and pathname before
# opening, then validate both again and compare the opened descriptor's device
# and inode with the pathname before any caller invokes flock.  This protects
# against unprivileged races; a privileged/root actor is outside this lock's
# threat model.  The explicit non-root switch exists only for local test
# fixtures that use a caller-owned mode-0700 temporary directory.

# shellcheck disable=SC2034 # Exported to callers that source this library.
SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE="/run/sub2api-maintenance/sub2api-maintenance.lock"
SUB2API_MAINTENANCE_LOCK_FD=8
# shellcheck disable=SC2034 # Read by callers after a failed open.
SUB2API_MAINTENANCE_LOCK_ERROR=""
# shellcheck disable=SC2034 # Set by the side-effect-free installer preflight.
SUB2API_MAINTENANCE_LOCK_PARENT=""

sub2api_maintenance_lock_fail() {
  SUB2API_MAINTENANCE_LOCK_ERROR="$*"
  return 1
}

sub2api_maintenance_lock_parent() {
  local path="$1" parent

  parent="${path%/*}"
  if [ -z "$parent" ]; then
    printf '/\n'
  else
    printf '%s\n' "$parent"
  fi
}

sub2api_maintenance_lock_metadata() {
  local path="$1" metadata

  if metadata="$(stat -c '%u:%g:%a:%h:%d:%i' "$path" 2>/dev/null)"; then
    printf '%s\n' "$metadata"
    return 0
  fi
  if metadata="$(stat -f '%u:%g:%Lp:%l:%d:%i' "$path" 2>/dev/null)"; then
    printf '%s\n' "$metadata"
    return 0
  fi
  return 1
}

sub2api_maintenance_lock_identity() {
  local path="$1" identity

  if identity="$(stat -L -c '%d:%i' "$path" 2>/dev/null)"; then
    printf '%s\n' "$identity"
    return 0
  fi
  if identity="$(stat -L -f '%d:%i' "$path" 2>/dev/null)"; then
    printf '%s\n' "$identity"
    return 0
  fi
  return 1
}

sub2api_maintenance_lock_descriptor_identity() {
  python3 - "$SUB2API_MAINTENANCE_LOCK_FD" <<'PY'
import os
import sys

descriptor = int(sys.argv[1])
metadata = os.fstat(descriptor)
print(f"{metadata.st_dev}:{metadata.st_ino}")
PY
}

sub2api_maintenance_lock_validate_path() {
  local path="$1" base

  case "$path" in
    /*) ;;
    *) sub2api_maintenance_lock_fail "maintenance lock path must be absolute: ${path}"; return 1 ;;
  esac
  case "$path" in
    *$'\n'*|*$'\r'*)
      sub2api_maintenance_lock_fail "maintenance lock path contains unsupported components: ${path}"
      return 1
      ;;
  esac
  case "$path" in
    *'//'*)
      sub2api_maintenance_lock_fail "maintenance lock path contains an empty component: ${path}"
      return 1
      ;;
  esac
  case "/${path}/" in
    */./*|*/../*)
      sub2api_maintenance_lock_fail "maintenance lock path must not contain . or .. components: ${path}"
      return 1
      ;;
  esac
  base="${path##*/}"
  case "$base" in
    ''|.|..)
      sub2api_maintenance_lock_fail "maintenance lock path has no regular-file basename: ${path}"
      return 1
      ;;
  esac
}

# Production has exactly one host-wide lock identity.  A caller-owned path is
# useful for hermetic shell fixtures, but only behind the explicit test switch;
# accepting a second safe-looking production pathname would silently split the
# release, certificate, and Caddy maintenance domains.
sub2api_maintenance_lock_validate_configured_path() {
  local lock_path="$1" test_mode

  SUB2API_MAINTENANCE_LOCK_ERROR=""
  sub2api_maintenance_lock_validate_path "$lock_path" || return 1
  test_mode="${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-0}"
  case "$test_mode" in
    0|1) ;;
    *)
      sub2api_maintenance_lock_fail "SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS must be 0 or 1"
      return 1
      ;;
  esac
  if [ "$test_mode" != 1 ] \
    && [ "$lock_path" != "$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE" ]; then
    sub2api_maintenance_lock_fail \
      "maintenance lock path must be the canonical ${SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE}: ${lock_path}"
    return 1
  fi
}

sub2api_maintenance_lock_validate_ancestor_chain() {
  local directory="$1" expected_uid="$2" test_mode="$3"
  local remaining current component metadata uid gid mode links mode_value

  remaining="${directory#/}"
  current=""
  while [ -n "$remaining" ]; do
    component="${remaining%%/*}"
    if [ "$component" = "$remaining" ]; then
      remaining=""
    else
      remaining="${remaining#*/}"
    fi
    [ -n "$component" ] || {
      sub2api_maintenance_lock_fail "maintenance lock ancestor path is malformed: ${directory}"
      return 1
    }
    if [ -z "$current" ]; then
      current="/${component}"
    else
      current="${current}/${component}"
    fi
    if [ -L "$current" ]; then
      sub2api_maintenance_lock_fail "maintenance lock ancestor is a symlink: ${current}"
      return 1
    fi
    if [ ! -d "$current" ]; then
      sub2api_maintenance_lock_fail "maintenance lock ancestor is not a directory: ${current}"
      return 1
    fi
    if ! metadata="$(sub2api_maintenance_lock_metadata "$current")"; then
      sub2api_maintenance_lock_fail "cannot read maintenance lock ancestor metadata: ${current}"
      return 1
    fi
    IFS=: read -r uid gid mode links _ _ <<EOF
$metadata
EOF
    case "$mode" in
      ''|*[!0-7]*)
        sub2api_maintenance_lock_fail "maintenance lock ancestor has unsupported mode: ${current}"
        return 1
        ;;
    esac
    if [ "$test_mode" = 1 ]; then
      if [ "$uid" != "$expected_uid" ] && [ "$uid" != 0 ]; then
        sub2api_maintenance_lock_fail "maintenance lock ancestor has an unexpected owner: ${current}"
        return 1
      fi
    elif [ "$uid" != "$expected_uid" ]; then
      sub2api_maintenance_lock_fail "maintenance lock ancestor is not root-owned: ${current}"
      return 1
    fi
    mode_value=$((8#$mode))
    if [ $((mode_value & 0022)) -ne 0 ] \
      && { [ "$uid" != 0 ] || [ $((mode_value & 01000)) -eq 0 ]; }; then
      sub2api_maintenance_lock_fail "maintenance lock ancestor is group/world-writable: ${current}"
      return 1
    fi
  done
}

sub2api_maintenance_lock_validate_private_parent() {
  local parent="$1" expected_uid="$2" expected_gid="$3"
  local metadata uid gid mode links

  if [ -L "$parent" ]; then
    sub2api_maintenance_lock_fail "maintenance lock parent is a symlink: ${parent}"
    return 1
  fi
  if [ ! -d "$parent" ]; then
    sub2api_maintenance_lock_fail "maintenance lock parent is not a directory: ${parent}"
    return 1
  fi
  if ! metadata="$(sub2api_maintenance_lock_metadata "$parent")"; then
    sub2api_maintenance_lock_fail "cannot read maintenance lock parent metadata: ${parent}"
    return 1
  fi
  IFS=: read -r uid gid mode links _ _ <<EOF
$metadata
EOF
  if [ "$uid" != "$expected_uid" ] || [ "$gid" != "$expected_gid" ]; then
    sub2api_maintenance_lock_fail "maintenance lock parent has an unexpected owner: ${parent}"
    return 1
  fi
  if [ "$mode" != 700 ]; then
    sub2api_maintenance_lock_fail "maintenance lock parent must be mode 0700: ${parent}"
    return 1
  fi
}

sub2api_maintenance_lock_validate_install_parent_container() {
  local container="$1" expected_uid="$2" test_mode="$3"
  local metadata uid gid mode links mode_value

  if [ -L "$container" ]; then
    sub2api_maintenance_lock_fail "maintenance lock parent container is a symlink: ${container}"
    return 1
  fi
  if [ ! -d "$container" ]; then
    sub2api_maintenance_lock_fail "maintenance lock parent container is not a directory: ${container}"
    return 1
  fi
  if ! metadata="$(sub2api_maintenance_lock_metadata "$container")"; then
    sub2api_maintenance_lock_fail "cannot read maintenance lock parent container metadata: ${container}"
    return 1
  fi
  IFS=: read -r uid gid mode links _ _ <<EOF
$metadata
EOF
  case "$mode" in
    ''|*[!0-7]*)
      sub2api_maintenance_lock_fail "maintenance lock parent container has unsupported mode: ${container}"
      return 1
      ;;
  esac
  if [ "$test_mode" = 1 ]; then
    if [ "$uid" != "$expected_uid" ] && [ "$uid" != 0 ]; then
      sub2api_maintenance_lock_fail "maintenance lock parent container has an unexpected owner: ${container}"
      return 1
    fi
  elif [ "$uid" != "$expected_uid" ]; then
    sub2api_maintenance_lock_fail "maintenance lock parent container is not root-owned: ${container}"
    return 1
  fi
  mode_value=$((8#$mode))
  if [ $((mode_value & 0022)) -ne 0 ]; then
    sub2api_maintenance_lock_fail "maintenance lock parent container is group/world-writable: ${container}"
    return 1
  fi
}

sub2api_maintenance_lock_validate_file() {
  local path="$1" expected_uid="$2" expected_gid="$3"
  local metadata uid gid mode links

  if [ -L "$path" ]; then
    sub2api_maintenance_lock_fail "maintenance lock path is a symlink: ${path}"
    return 1
  fi
  if [ ! -f "$path" ]; then
    sub2api_maintenance_lock_fail "maintenance lock path is not a regular file: ${path}"
    return 1
  fi
  if ! metadata="$(sub2api_maintenance_lock_metadata "$path")"; then
    sub2api_maintenance_lock_fail "cannot read maintenance lock metadata: ${path}"
    return 1
  fi
  IFS=: read -r uid gid mode links _ _ <<EOF
$metadata
EOF
  if [ "$uid" != "$expected_uid" ] || [ "$gid" != "$expected_gid" ]; then
    sub2api_maintenance_lock_fail "maintenance lock has an unexpected owner: ${path}"
    return 1
  fi
  if [ "$mode" != 600 ]; then
    sub2api_maintenance_lock_fail "maintenance lock must be mode 0600: ${path}"
    return 1
  fi
  if [ "$links" != 1 ]; then
    sub2api_maintenance_lock_fail "maintenance lock must have exactly one hard link: ${path}"
    return 1
  fi
}

# Validate an installer-selected target without creating or opening anything.
# The direct private parent may be absent (the installer creates it after this
# returns), but every existing ancestor must already be a safe non-symlink
# directory. An existing parent or lock must meet the same strict contract as
# a runtime lock acquisition so install -d can never chmod an arbitrary path.
sub2api_maintenance_lock_validate_install_target() {
  local lock_path="$1" test_mode expected_uid expected_gid parent parent_container

  SUB2API_MAINTENANCE_LOCK_ERROR=""
  SUB2API_MAINTENANCE_LOCK_PARENT=""
  for command_name in id stat; do
    command -v "$command_name" >/dev/null 2>&1 || {
      sub2api_maintenance_lock_fail "${command_name} is required for the maintenance lock"
      return 1
    }
  done
  sub2api_maintenance_lock_validate_configured_path "$lock_path" || return 1

  test_mode="${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-0}"
  case "$test_mode" in
    0|1) ;;
    *)
      sub2api_maintenance_lock_fail "SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS must be 0 or 1"
      return 1
      ;;
  esac
  expected_uid="$(id -u)"
  expected_gid="$(id -g)"
  if [ "$test_mode" != 1 ] && [ "$expected_uid" -ne 0 ]; then
    sub2api_maintenance_lock_fail "maintenance lock operations require root"
    return 1
  fi

  parent="$(sub2api_maintenance_lock_parent "$lock_path")"
  parent_container="$(sub2api_maintenance_lock_parent "$parent")"
  sub2api_maintenance_lock_validate_ancestor_chain "$parent_container" "$expected_uid" "$test_mode" \
    || return 1
  sub2api_maintenance_lock_validate_install_parent_container \
    "$parent_container" "$expected_uid" "$test_mode" || return 1
  if [ -e "$parent" ] || [ -L "$parent" ]; then
    sub2api_maintenance_lock_validate_private_parent "$parent" "$expected_uid" "$expected_gid" \
      || return 1
  fi
  if [ -e "$lock_path" ] || [ -L "$lock_path" ]; then
    sub2api_maintenance_lock_validate_file "$lock_path" "$expected_uid" "$expected_gid" \
      || return 1
  fi
  SUB2API_MAINTENANCE_LOCK_PARENT="$parent"
}

sub2api_maintenance_lock_close_fd() {
  exec 8>&-
}

sub2api_maintenance_lock_open() {
  local lock_path="$1" test_mode expected_uid expected_gid parent parent_container
  local previous_umask path_identity descriptor_identity

  SUB2API_MAINTENANCE_LOCK_ERROR=""
  for command_name in id mkdir python3 stat; do
    command -v "$command_name" >/dev/null 2>&1 || {
      sub2api_maintenance_lock_fail "${command_name} is required for the maintenance lock"
      return 1
    }
  done
  sub2api_maintenance_lock_validate_configured_path "$lock_path" || return 1

  test_mode="${SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-0}"
  case "$test_mode" in
    0|1) ;;
    *)
      sub2api_maintenance_lock_fail "SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS must be 0 or 1"
      return 1
      ;;
  esac
  expected_uid="$(id -u)"
  expected_gid="$(id -g)"
  if [ "$test_mode" != 1 ] && [ "$expected_uid" -ne 0 ]; then
    sub2api_maintenance_lock_fail "maintenance lock operations require root"
    return 1
  fi

  parent="$(sub2api_maintenance_lock_parent "$lock_path")"
  parent_container="$(sub2api_maintenance_lock_parent "$parent")"
  sub2api_maintenance_lock_validate_ancestor_chain "$parent_container" "$expected_uid" "$test_mode" \
    || return 1
  if [ -e "$parent" ] || [ -L "$parent" ]; then
    sub2api_maintenance_lock_validate_private_parent "$parent" "$expected_uid" "$expected_gid" \
      || return 1
  else
    if ! mkdir -m 700 "$parent"; then
      sub2api_maintenance_lock_fail "could not create private maintenance lock parent: ${parent}"
      return 1
    fi
    sub2api_maintenance_lock_validate_private_parent "$parent" "$expected_uid" "$expected_gid" \
      || return 1
  fi
  if [ -e "$lock_path" ] || [ -L "$lock_path" ]; then
    sub2api_maintenance_lock_validate_file "$lock_path" "$expected_uid" "$expected_gid" \
      || return 1
  fi

  previous_umask="$(umask)"
  umask 077
  if ! exec 8>>"$lock_path"; then
    umask "$previous_umask"
    sub2api_maintenance_lock_fail "could not open maintenance lock: ${lock_path}"
    return 1
  fi
  umask "$previous_umask"

  # The parent remains private, so an unprivileged account cannot replace the
  # pathname after the first validation. Re-check anyway and bind the open FD
  # to the same inode before flock distinguishes an unsafe path from a busy one.
  if ! sub2api_maintenance_lock_validate_ancestor_chain "$parent_container" "$expected_uid" "$test_mode" \
    || ! sub2api_maintenance_lock_validate_private_parent "$parent" "$expected_uid" "$expected_gid" \
    || ! sub2api_maintenance_lock_validate_file "$lock_path" "$expected_uid" "$expected_gid"; then
    sub2api_maintenance_lock_close_fd
    return 1
  fi
  if ! path_identity="$(sub2api_maintenance_lock_identity "$lock_path")"; then
    sub2api_maintenance_lock_close_fd
    sub2api_maintenance_lock_fail "cannot read post-open maintenance lock identity: ${lock_path}"
    return 1
  fi
  if ! descriptor_identity="$(sub2api_maintenance_lock_descriptor_identity)"; then
    sub2api_maintenance_lock_close_fd
    sub2api_maintenance_lock_fail "cannot inspect the opened maintenance lock descriptor"
    return 1
  fi
  if [ "$path_identity" != "$descriptor_identity" ]; then
    sub2api_maintenance_lock_close_fd
    sub2api_maintenance_lock_fail "maintenance lock path changed while opening: ${lock_path}"
    return 1
  fi
}

# Re-exec an installer under a supervising parent that retains the canonical
# maintenance lock on FD 8 and, during the one supported legacy migration, the
# retired /run/lock inode on FD 7. Bash cannot open a pathname with O_NOFOLLOW
# or pass an FD opened by a child back to its parent. The Python supervisor
# therefore validates and locks both files, pins the installer source through
# a no-follow private snapshot on FD 6, then waits for Bash without passing
# FD 7/8 to it. This prevents install/systemctl descendants from extending a
# lock after the installer exits. FD 9 is an unlinked root-owned nonce used by
# the child to distinguish this supervised fence from user-supplied state.
# TERM/INT/HUP is first forwarded to Bash's isolated process group; a second
# termination request escalates that group to SIGKILL, but both fences survive
# until the child is reaped.
sub2api_maintenance_lock_exec_installer_with_fences() {
  local installer_path="$1" source_root="$2" source_uid="$3" canonical_path="$4" legacy_path="$5"
  shift 5

  exec python3 - "$installer_path" "$source_root" "$source_uid" "$canonical_path" "$legacy_path" "$@" <<'PY'
import fcntl
import hashlib
import os
import secrets
import signal
import stat
import subprocess
import sys
import time

installer_path, source_root, source_uid_text, canonical_path, legacy_path = sys.argv[1:6]
installer_args = sys.argv[6:]
test_mode = os.environ.get("SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS", "0")


def fail(message, status=1):
    print(f"unsafe maintenance lock install fence: {message}", file=sys.stderr)
    raise SystemExit(status)


def busy(path):
    print(f"maintenance lock install fence is busy: {path}", file=sys.stderr)
    raise SystemExit(75)


if test_mode not in {"0", "1"}:
    fail("SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS must be 0 or 1")
if test_mode != "1" and os.geteuid() != 0:
    fail("maintenance lock operations require root")
try:
    source_uid = int(source_uid_text, 10)
except ValueError:
    fail("installer source has an invalid expected owner")
if not source_root.startswith("/") or "//" in source_root:
    fail("installer source root is malformed")
if getattr(os, "O_NOFOLLOW", None) is None:
    fail("Python O_NOFOLLOW support is required")

expected_uid = os.geteuid()
expected_gid = os.getegid()
no_follow = os.O_NOFOLLOW


def validate_path(path, label):
    if not path.startswith("/"):
        fail(f"{label} must be absolute: {path}")
    if "\n" in path or "\r" in path or "//" in path:
        fail(f"{label} has an empty or unsupported component: {path}")
    parts = path.split("/")[1:]
    if not parts or any(part in {"", ".", ".."} for part in parts):
        fail(f"{label} has an invalid component: {path}")


def lstat(path, label):
    try:
        return os.lstat(path)
    except OSError as exc:
        fail(f"cannot inspect {label}: {path}: {exc.strerror}")


def require_owner(st, label, *, private=False):
    if test_mode == "1":
        if st.st_uid not in {expected_uid, 0}:
            fail(f"{label} has an unexpected owner")
    elif st.st_uid != 0:
        fail(f"{label} is not root-owned")
    if private and (st.st_uid != expected_uid or st.st_gid != expected_gid):
        fail(f"{label} has an unexpected owner")


def require_directory(st, label):
    if stat.S_ISLNK(st.st_mode):
        fail(f"{label} is a symlink")
    if not stat.S_ISDIR(st.st_mode):
        fail(f"{label} is not a directory")


def validate_ancestor_chain(directory):
    current = ""
    for component in directory.split("/")[1:]:
        current = f"{current}/{component}" if current else f"/{component}"
        st = lstat(current, "maintenance lock ancestor")
        require_directory(st, f"maintenance lock ancestor {current}")
        require_owner(st, f"maintenance lock ancestor {current}")
        mode = stat.S_IMODE(st.st_mode)
        if mode & 0o022 and not (st.st_uid == 0 and mode & stat.S_ISVTX):
            fail(f"maintenance lock ancestor is group/world-writable: {current}")


def validate_private_parent(path):
    st = lstat(path, "maintenance lock parent")
    require_directory(st, f"maintenance lock parent {path}")
    require_owner(st, f"maintenance lock parent {path}", private=True)
    if stat.S_IMODE(st.st_mode) != 0o700:
        fail(f"maintenance lock parent must be mode 0700: {path}")


def validate_parent_container(path):
    st = lstat(path, "maintenance lock parent container")
    require_directory(st, f"maintenance lock parent container {path}")
    require_owner(st, f"maintenance lock parent container {path}")
    if stat.S_IMODE(st.st_mode) & 0o022:
        fail(f"maintenance lock parent container is group/world-writable: {path}")


def validate_regular(st, path, *, legacy=False):
    if stat.S_ISLNK(st.st_mode):
        fail(f"maintenance lock path is a symlink: {path}")
    if not stat.S_ISREG(st.st_mode):
        fail(f"maintenance lock path is not a regular file: {path}")
    require_owner(st, f"maintenance lock {path}", private=True)
    allowed_modes = {0o600, 0o644} if legacy else {0o600}
    if stat.S_IMODE(st.st_mode) not in allowed_modes:
        expected = "0600 or 0644" if legacy else "0600"
        fail(f"maintenance lock must be mode {expected}: {path}")
    if st.st_nlink != 1:
        fail(f"maintenance lock must have exactly one hard link: {path}")


def bind_path(fd, path, *, legacy=False):
    descriptor = os.fstat(fd)
    validate_regular(descriptor, path, legacy=legacy)
    pathname = lstat(path, "maintenance lock path")
    validate_regular(pathname, path, legacy=legacy)
    if (descriptor.st_dev, descriptor.st_ino) != (pathname.st_dev, pathname.st_ino):
        fail(f"maintenance lock path changed while opening: {path}")


def open_canonical(path):
    validate_path(path, "maintenance lock path")
    parent = os.path.dirname(path)
    container = os.path.dirname(parent)
    validate_ancestor_chain(container)
    validate_parent_container(container)
    if os.path.lexists(parent):
        validate_private_parent(parent)
    else:
        try:
            os.mkdir(parent, 0o700)
        except OSError as exc:
            fail(f"could not create private maintenance lock parent: {parent}: {exc.strerror}")
        validate_private_parent(parent)
    if os.path.lexists(path):
        existing = lstat(path, "maintenance lock path")
        validate_regular(existing, path)
        try:
            fd = os.open(path, os.O_RDWR | no_follow)
        except OSError as exc:
            fail(f"could not open maintenance lock: {path}: {exc.strerror}")
    else:
        try:
            fd = os.open(path, os.O_RDWR | os.O_CREAT | os.O_EXCL | no_follow, 0o600)
        except FileExistsError:
            return open_canonical(path)
        except OSError as exc:
            fail(f"could not create maintenance lock: {path}: {exc.strerror}")
    bind_path(fd, path)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        os.close(fd)
        busy(path)
    bind_path(fd, path)
    return fd, parent


def validate_legacy_parent(parent):
    validate_ancestor_chain(os.path.dirname(parent))
    st = lstat(parent, "legacy maintenance lock parent")
    require_directory(st, f"legacy maintenance lock parent {parent}")
    require_owner(st, f"legacy maintenance lock parent {parent}")
    mode = stat.S_IMODE(st.st_mode)
    if mode & 0o022 and not (st.st_uid == 0 and mode & stat.S_ISVTX):
        fail(f"legacy maintenance lock parent is group/world-writable without root sticky protection: {parent}")


def open_legacy(path):
    validate_path(path, "legacy maintenance lock path")
    parent = os.path.dirname(path)
    validate_legacy_parent(parent)
    if os.path.lexists(path):
        existing = lstat(path, "legacy maintenance lock path")
        validate_regular(existing, path, legacy=True)
        try:
            fd = os.open(path, os.O_RDWR | no_follow)
        except OSError as exc:
            fail(f"could not open legacy maintenance lock: {path}: {exc.strerror}")
    else:
        try:
            fd = os.open(path, os.O_RDWR | os.O_CREAT | os.O_EXCL | no_follow, 0o600)
        except FileExistsError:
            return open_legacy(path)
        except OSError as exc:
            fail(f"could not create legacy maintenance lock: {path}: {exc.strerror}")
    bind_path(fd, path, legacy=True)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        os.close(fd)
        busy(path)
    bind_path(fd, path, legacy=True)
    return fd


def stage_installer_source(path, private_parent):
    """Return an unlinked, root-owned FD containing a pinned source snapshot."""
    source_fd = -1
    stage_fd = -1
    stage_path = ""
    try:
        source_fd = os.open(path, os.O_RDONLY | no_follow)
    except OSError as exc:
        fail(f"could not open installer source without following symlinks: {exc.strerror}")
    try:
        source_before = os.fstat(source_fd)
        if not stat.S_ISREG(source_before.st_mode):
            fail("installer source descriptor is not a regular file")
        if source_before.st_uid != source_uid:
            fail("installer source owner changed before fenced re-exec")
        if stat.S_IMODE(source_before.st_mode) & 0o022:
            fail("installer source became group/other writable before fenced re-exec")
        source_size = source_before.st_size

        barrier = os.environ.get("SUB2API_MAINTENANCE_LOCK_TEST_AFTER_INSTALLER_SOURCE_OPEN_BARRIER")
        if barrier:
            if test_mode != "1":
                fail("installer-source barrier is only available to maintenance-lock tests")
            try:
                barrier_fd = os.open(barrier, os.O_WRONLY | os.O_CREAT | os.O_EXCL | no_follow, 0o600)
            except OSError as exc:
                fail(f"could not create installer-source test barrier: {exc.strerror}")
            try:
                os.write(barrier_fd, b"installer-source-open\n")
            finally:
                os.close(barrier_fd)
            deadline = time.monotonic() + 10
            while not os.path.lexists(f"{barrier}.continue"):
                if time.monotonic() >= deadline:
                    fail("installer-source test barrier timed out")
                time.sleep(0.01)

        stage_path = os.path.join(private_parent, f".installer-source-{secrets.token_hex(32)}")
        try:
            stage_fd = os.open(
                stage_path,
                os.O_RDWR | os.O_CREAT | os.O_EXCL | no_follow,
                0o600,
            )
        except OSError as exc:
            fail(f"could not create private staged installer source: {exc.strerror}")
        stage_before = os.fstat(stage_fd)
        if (
            not stat.S_ISREG(stage_before.st_mode)
            or stage_before.st_uid != expected_uid
            or stage_before.st_gid != expected_gid
            or stat.S_IMODE(stage_before.st_mode) != 0o600
            or stage_before.st_nlink != 1
        ):
            fail("private staged installer source has unsafe metadata")

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
                    fail("could not write private staged installer source")
                view = view[written:]
        os.fchmod(stage_fd, 0o600)
        os.fsync(stage_fd)
        stage_after = os.fstat(stage_fd)
        if (
            not stat.S_ISREG(stage_after.st_mode)
            or stage_after.st_uid != expected_uid
            or stage_after.st_gid != expected_gid
            or stat.S_IMODE(stage_after.st_mode) != 0o600
            or stage_after.st_nlink != 1
        ):
            fail("private staged installer source changed while staging")

        source_after = os.fstat(source_fd)
        if (
            not stat.S_ISREG(source_after.st_mode)
            or source_after.st_uid != source_uid
            or stat.S_IMODE(source_after.st_mode) & 0o022
            or source_after.st_size != source_size
            or copied_size != source_size
        ):
            fail("installer source changed while staging")
        os.lseek(source_fd, 0, os.SEEK_SET)
        source_after_hash = hashlib.sha256()
        while True:
            chunk = os.read(source_fd, 1024 * 1024)
            if not chunk:
                break
            source_after_hash.update(chunk)
        if source_after_hash.digest() != copied_hash.digest():
            fail("installer source content changed while staging")
        pathname = lstat(path, "installer source")
        if (
            not stat.S_ISREG(pathname.st_mode)
            or (source_before.st_dev, source_before.st_ino)
            != (pathname.st_dev, pathname.st_ino)
        ):
            fail("installer source path changed while staging")

        os.lseek(stage_fd, 0, os.SEEK_SET)
        os.unlink(stage_path)
        stage_path = ""
        result = stage_fd
        stage_fd = -1
        return result
    finally:
        if source_fd >= 0:
            os.close(source_fd)
        if stage_fd >= 0:
            os.close(stage_fd)
            if stage_path:
                try:
                    os.unlink(stage_path)
                except FileNotFoundError:
                    pass


legacy_fd = -1
canonical_fd = -1
source_fd = -1
token_fd = -1
child = None
termination_signal = None
initial_termination_forwarded = False
termination_escalated = False
escalation_kill_sent = False
previous_signal_handlers = {}


def retain_high(descriptor):
    retained = fcntl.fcntl(descriptor, fcntl.F_DUPFD, 20)
    os.close(descriptor)
    return retained


def close_descriptor(descriptor):
    try:
        os.close(descriptor)
    except OSError:
        pass


def forward_signal_to_child(signum):
    """Forward one supervisor signal to the isolated installer group."""
    global initial_termination_forwarded

    if child is None or child.poll() is not None:
        return False
    try:
        # start_new_session=True gives the Bash child a distinct process group,
        # so this can never signal the lock-owning supervisor or its caller.
        os.killpg(child.pid, signum)
    except ProcessLookupError:
        return False
    except OSError as exc:
        # Never let a signal-handler error release a fence while the child
        # might still be running. The wait path below keeps both locks until
        # the child is reaped; its retry path covers transient failures and
        # the only expected setup race (the process group not existing yet).
        return False
    if termination_signal == signum:
        initial_termination_forwarded = True
    return True


def kill_child_group():
    """Escalate a repeated termination request without abandoning fences."""
    global escalation_kill_sent, termination_escalated

    termination_escalated = True
    if child is None or child.poll() is not None:
        return False
    try:
        os.killpg(child.pid, signal.SIGKILL)
    except (ProcessLookupError, OSError):
        return False
    escalation_kill_sent = True
    return True


def request_termination(signum, _frame):
    """Keep fences held while the installer group is asked to stop."""
    global termination_signal

    if termination_signal is None:
        termination_signal = signum
        # The first request is graceful. Locks remain held until Bash exits
        # and is reaped, even when it has a TERM/INT/HUP trap.
        forward_signal_to_child(signum)
        return
    # A child that ignores the graceful request must not pin this supervisor
    # forever. The next termination request kills only its isolated process
    # group; the wait path still reaps it before either fence is released.
    kill_child_group()


def install_termination_handlers():
    for signum in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        previous_signal_handlers[signum] = signal.signal(signum, request_termination)


def restore_termination_handlers():
    for signum, handler in previous_signal_handlers.items():
        signal.signal(signum, handler)


def supervisor_exit_status(returncode):
    if termination_signal is not None:
        # Report the operator's first cancellation request even when a second
        # request had to SIGKILL an unresponsive child (which returns -9).
        return 128 + termination_signal
    if returncode < 0:
        return 128 + (-returncode)
    return returncode


try:
    if legacy_path:
        legacy_fd = open_legacy(legacy_path)
    canonical_fd, canonical_parent = open_canonical(canonical_path)
    source_fd = stage_installer_source(installer_path, canonical_parent)

    token = secrets.token_hex(32).encode("ascii")
    token_path = os.path.join(canonical_parent, f".installer-fence-{token.decode('ascii')}")
    try:
        token_fd = os.open(token_path, os.O_RDWR | os.O_CREAT | os.O_EXCL | no_follow, 0o600)
    except OSError as exc:
        fail(f"could not create installer fence nonce: {exc.strerror}")
    token_stat = os.fstat(token_fd)
    if (
        not stat.S_ISREG(token_stat.st_mode)
        or token_stat.st_uid != expected_uid
        or token_stat.st_gid != expected_gid
        or stat.S_IMODE(token_stat.st_mode) != 0o600
        or token_stat.st_nlink != 1
    ):
        fail("installer fence nonce has unsafe metadata")
    os.write(token_fd, token)
    os.fsync(token_fd)
    os.unlink(token_path)

    # Preserve every open description above the fixed descriptor range before
    # assigning FD 6/7/8/9. A kernel-selected source/token FD must never
    # overwrite an already-held lock while this mapping is established.
    source_fd = retain_high(source_fd)
    token_fd = retain_high(token_fd)
    canonical_fd = retain_high(canonical_fd)
    if legacy_fd >= 0:
        legacy_fd = retain_high(legacy_fd)
    for descriptor in (6, 7, 8, 9):
        close_descriptor(descriptor)
    os.dup2(source_fd, 6)
    os.dup2(canonical_fd, 8)
    os.dup2(token_fd, 9)
    if legacy_fd >= 0:
        os.dup2(legacy_fd, 7)
    for descriptor in (source_fd, token_fd, canonical_fd, legacy_fd):
        if descriptor >= 0:
            os.close(descriptor)
    source_fd = 6
    canonical_fd = 8
    token_fd = 9
    legacy_fd = 7 if legacy_path else -1
    # Only Bash's source snapshot and nonce cross the child boundary. Popen
    # closes every other descriptor, including the supervisor's lock FDs.
    os.set_inheritable(source_fd, True)
    os.set_inheritable(token_fd, True)
    os.set_inheritable(canonical_fd, False)
    if legacy_fd >= 0:
        os.set_inheritable(legacy_fd, False)

    environment = os.environ.copy()
    environment["SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_READY"] = "1"
    environment["SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_SUPERVISED"] = "1"
    environment["SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_TOKEN"] = token.decode("ascii")
    environment["SUB2API_AUTODEPLOY_MAINTENANCE_FENCE_LEGACY"] = legacy_path
    environment["SUB2API_AUTODEPLOY_EXEC_SOURCE_ROOT"] = source_root
    install_termination_handlers()
    # If a termination request arrived in the narrow setup window, do not
    # spawn a fresh installer. The finally block releases both locks only
    # after this supervisor has no child to reap.
    if termination_signal is not None:
        raise SystemExit(128 + termination_signal)
    child = subprocess.Popen(
        ["/bin/bash", "/dev/fd/6", *installer_args],
        close_fds=True,
        env=environment,
        pass_fds=(source_fd, token_fd),
        start_new_session=True,
    )
    # A signal can be delivered after the pre-spawn check but before Popen
    # assigns child. Retry the first forward once the dedicated group exists.
    if termination_escalated:
        # Two signals may both arrive while Popen is creating the new session.
        # Retry escalation now that child is definitely assigned.
        kill_child_group()
    elif (
        termination_signal is not None
        and not initial_termination_forwarded
    ):
        forward_signal_to_child(termination_signal)
    # The child owns its copies of FD 6/9. The supervisor retains only the
    # actual locks while it waits, so a persistent external descendant cannot
    # keep either maintenance lock alive after this process exits.
    close_descriptor(source_fd)
    close_descriptor(token_fd)
    source_fd = token_fd = -1
    while True:
        retry_first_signal = (
            termination_signal is not None
            and not initial_termination_forwarded
            and not termination_escalated
        )
        retry_escalation = termination_escalated and not escalation_kill_sent
        if retry_first_signal:
            forward_signal_to_child(termination_signal)
        if retry_escalation:
            kill_child_group()
        try:
            if retry_first_signal or retry_escalation:
                child_returncode = child.wait(timeout=0.1)
            else:
                child_returncode = child.wait()
            break
        except InterruptedError:
            continue
        except subprocess.TimeoutExpired:
            continue
    raise SystemExit(supervisor_exit_status(child_returncode))
finally:
    restore_termination_handlers()
    for descriptor in (source_fd, token_fd, canonical_fd, legacy_fd):
        if descriptor >= 0:
            close_descriptor(descriptor)
PY
}
