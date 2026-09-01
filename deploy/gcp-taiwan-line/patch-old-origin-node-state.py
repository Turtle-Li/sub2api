#!/usr/bin/env python3
"""Build the exact inode-preserving node-state hotfix for the old origin.

This is a deliberately narrow one-time transformer.  It accepts only the
known legacy production helper and writes a new candidate without installing
it.  The operator must syntax-check and install the candidate while holding
the legacy production maintenance lock.
"""

from __future__ import annotations

import argparse
import hashlib
import os
import stat
import sys
from pathlib import Path


LEGACY_SHA256 = "421082e420a9e18a87246c4edaf9cd64e6d40e9976fa8e95de294cffd355f8b2"
MAX_SOURCE_BYTES = 256 * 1024

LEGACY_WRITE_STATE = """write_state() {
  local path="$1"
  local value="$2"
  local temporary="${path}.tmp.$$"
  printf '%s\\n' "$value" >"$temporary"
  chmod 644 "$temporary"
  mv -f -- "$temporary" "$path"
}
"""

INODE_PRESERVING_WRITE_STATE = """write_state() {
  local path="$1"
  local value="$2"
  local temporary="${path}.tmp.$$"

  if [ -L "$path" ] || { [ -e "$path" ] && [ ! -f "$path" ]; }; then
    die "runtime state path is not a regular file: $path"
  fi
  if [ -f "$path" ]; then
    if [ "$(read_state "$path")" = "$value" ]; then
      chmod 644 "$path" \\
        || die "could not set runtime state file permissions: $path"
      return
    fi
    # Docker single-file bind mounts pin the current inode. Replacing this path
    # with rename(2) leaves already-running containers on a stale state file.
    # The legacy host maintenance lock serializes writers, so update an existing
    # state file in place and retain atomic rename only for first creation.
    printf '%s\\n' "$value" >"$path" \\
      || die "could not update runtime state file: $path"
    chmod 644 "$path" \\
      || die "could not set runtime state file permissions: $path"
    return
  fi

  printf '%s\\n' "$value" >"$temporary"
  chmod 644 "$temporary"
  mv -f -- "$temporary" "$path"
}
"""


class HotfixError(RuntimeError):
    """The source or candidate boundary is not safe for this migration."""


def sha256(payload: bytes) -> str:
    return hashlib.sha256(payload).hexdigest()


def transform(payload: bytes, *, verify_digest: bool = True) -> bytes:
    if verify_digest and sha256(payload) != LEGACY_SHA256:
        raise HotfixError("source digest is not the reviewed legacy helper")
    try:
        text = payload.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise HotfixError("source is not valid UTF-8") from exc
    if text.count(LEGACY_WRITE_STATE) != 1:
        raise HotfixError("legacy write_state block is not present exactly once")
    return text.replace(LEGACY_WRITE_STATE, INODE_PRESERVING_WRITE_STATE, 1).encode()


def read_regular_file(path: Path) -> bytes:
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open(path, flags)
    try:
        metadata = os.fstat(descriptor)
        if not stat.S_ISREG(metadata.st_mode):
            raise HotfixError("source is not a regular file")
        if metadata.st_size > MAX_SOURCE_BYTES:
            raise HotfixError("source exceeds the safety size limit")
        payload = bytearray()
        while True:
            chunk = os.read(descriptor, 65536)
            if not chunk:
                break
            payload.extend(chunk)
        return bytes(payload)
    finally:
        os.close(descriptor)


def write_exclusive_file(path: Path, payload: bytes) -> None:
    parent = path.parent
    parent_metadata = os.lstat(parent)
    if not stat.S_ISDIR(parent_metadata.st_mode) or stat.S_ISLNK(parent_metadata.st_mode):
        raise HotfixError("candidate parent is not a real directory")
    if parent_metadata.st_mode & 0o022:
        raise HotfixError("candidate parent must not be group/world writable")

    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open(path, flags, 0o600)
    try:
        view = memoryview(payload)
        while view:
            written = os.write(descriptor, view)
            view = view[written:]
        os.fsync(descriptor)
    except BaseException:
        os.close(descriptor)
        path.unlink(missing_ok=True)
        raise
    else:
        os.close(descriptor)


def self_test() -> None:
    fixture = ("#!/usr/bin/env bash\n" + LEGACY_WRITE_STATE + "main \"$@\"\n").encode()
    patched = transform(fixture, verify_digest=False).decode()
    if LEGACY_WRITE_STATE in patched:
        raise HotfixError("self-test retained the legacy write_state block")
    if patched.count(INODE_PRESERVING_WRITE_STATE) != 1:
        raise HotfixError("self-test did not produce one reviewed replacement")
    try:
        transform(fixture.replace(b"mv -f", b"mv --"), verify_digest=False)
    except HotfixError:
        pass
    else:
        raise HotfixError("self-test accepted a changed legacy block")
    print("OLD_ORIGIN_NODE_STATE_HOTFIX_SELF_TEST_PASS")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("source", nargs="?")
    parser.add_argument("candidate", nargs="?")
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()

    if args.self_test:
        if args.source or args.candidate:
            parser.error("--self-test accepts no paths")
        self_test()
        return 0
    if not args.source or not args.candidate:
        parser.error("SOURCE and CANDIDATE are required")

    source = Path(args.source)
    candidate = Path(args.candidate)
    payload = read_regular_file(source)
    patched = transform(payload)
    write_exclusive_file(candidate, patched)
    print(
        "OLD_ORIGIN_NODE_STATE_HOTFIX_CANDIDATE "
        f"source_sha256={sha256(payload)} candidate_sha256={sha256(patched)}"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (HotfixError, OSError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1) from exc
