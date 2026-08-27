#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "$0")" && pwd)"
DEPLOY_DIR="$(cd "$TEST_DIR/.." && pwd)"
SCRIPT="$DEPLOY_DIR/sub2api-blue-green-release.sh"
INSTALLER="$DEPLOY_DIR/install-autodeploy.sh"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  grep -Fq -- "$2" "$1" || fail "expected required content was absent"
}

assert_not_contains() {
  if grep -Fq -- "$2" "$1"; then
    fail "forbidden content was present"
  fi
}

assert_contains "$SCRIPT" 'PRECREATE_ONLY'
assert_contains "$SCRIPT" 'PGSSLROOTCERT'
assert_contains "$SCRIPT" '/etc/sub2api-db-ca/ca.crt'
assert_contains "$SCRIPT" '/etc/ssl/certs/sub2api-db-ca.pem'
assert_contains "$SCRIPT" 'SUB2API_EXTERNAL_RUNTIME_ENV_FILE'
assert_contains "$SCRIPT" 'SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE'
assert_contains "$SCRIPT" 'realpath -e --'
assert_contains "$SCRIPT" 'group-writable'
assert_contains "$SCRIPT" 'other-writable'
assert_contains "$SCRIPT" 'network_count'
assert_contains "$SCRIPT" 'mount_count'
assert_contains "$SCRIPT" '.Type .Name .Destination .RW'
assert_contains "$SCRIPT" '.Type .Source .Destination .RW'
assert_not_contains "$SCRIPT" 'println .Type "|" .Source'
assert_not_contains "$SCRIPT" '/etc/ssl/cert.pem'
assert_contains "$INSTALLER" 'deploy/sub2api-blue-green-release.sh'
assert_contains "$INSTALLER" 'sub2api-blue-green-release.sh'

printf 'Blue-green external runtime static checks passed.\n'
