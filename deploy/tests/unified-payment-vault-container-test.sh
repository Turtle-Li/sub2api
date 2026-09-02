#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$(cd "$TEST_DIR/.." && pwd)/sub2api-unified-payment-vault-container.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-payment-vault-container.XXXXXX")"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
FAKE_BIN="$TEST_ROOT/bin"
LOCK_DIR="$TEST_ROOT/locks"
LOCK_FILE="$LOCK_DIR/maintenance.lock"
STATE="$TEST_ROOT/container-created"
HEALTH="$TEST_ROOT/health"
CALLS="$TEST_ROOT/calls"
OUTPUT="$TEST_ROOT/output"
trap 'rm -rf "$TEST_ROOT"' EXIT

fail() {
  printf 'Unified payment Vault container test failed: %s\n' "$*" >&2
  exit 1
}

mkdir -p "$FAKE_BIN" "$LOCK_DIR"
chmod 700 "$LOCK_DIR"
printf 'starting\n' >"$HEALTH"

cat >"$FAKE_BIN/flock" <<'EOF'
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
cat >"$FAKE_BIN/docker" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >>"$MOCK_CALLS"
case "$1:$2" in
  image:inspect)
    case "$5" in
      *'.Os}}/{{.Architecture}}'*) printf 'linux/amd64\n' ;;
      *'image.revision'*) printf '%s\n' "${3#sub2api:prebuilt-}" ;;
      *'image.source'*) printf 'https://github.com/Turtle-Li/sub2api\n' ;;
      *'image.version'*) printf '0.1.186\n' ;;
      *) exit 1 ;;
    esac
    ;;
  container:inspect)
    [ -f "$MOCK_STATE" ] || exit 1
    [ "${4:-}" = --format ] || exit 0
    case "$5" in
      *'.Config.Image'*) printf '%s\n' "$MOCK_IMAGE" ;;
      *'.State.Running'*) printf 'true\n' ;;
      *'.State.Health.Status'*) cat "$MOCK_HEALTH" ;;
      *'.HostConfig.NetworkMode'*) printf 'none\n' ;;
      *'.HostConfig.ReadonlyRootfs'*) printf 'true\n' ;;
      *'.HostConfig.RestartPolicy.Name'*) printf 'unless-stopped\n' ;;
      *'.HostConfig.PidsLimit'*) printf '64\n' ;;
      *'.HostConfig.Init'*) printf 'true\n' ;;
      *'.HostConfig.CapDrop'*) printf '["ALL"]\n' ;;
      *'.HostConfig.SecurityOpt'*) printf '["no-new-privileges"]\n' ;;
      *'/run/sub2api-payment-vault-admin'*) printf 'rw,noexec,nosuid,nodev,size=1m,mode=0700,uid=1000,gid=1000\n' ;;
      *'.HostConfig.Tmpfs'*'/tmp'*) printf 'rw,noexec,nosuid,nodev,size=4m,mode=0700,uid=1000,gid=1000\n' ;;
      *'range .Mounts'*) printf 'volume|sub2api_unified_payment_vault|/run/sub2api-payment-vault|true\n' ;;
      *'.Config.Cmd'*) printf '["/app/sub2api-vault-agent","serve","--public-socket","/run/sub2api-payment-vault/public.sock","--admin-socket","/run/sub2api-payment-vault-admin/admin.sock","--allowed-ref","vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64"]\n' ;;
      *'.Config.Healthcheck.Test'*) printf '["CMD-SHELL","/app/sub2api-vault-agent check --public-socket /run/sub2api-payment-vault/public.sock"]\n' ;;
      *) exit 1 ;;
    esac
    ;;
  volume:create) printf 'sub2api_unified_payment_vault\n' ;;
  run:-d) touch "$MOCK_STATE" ; printf 'container-id\n' ;;
  *) exit 1 ;;
esac
EOF
chmod +x "$FAKE_BIN/flock" "$FAKE_BIN/docker"

image="sub2api:prebuilt-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
PATH="$FAKE_BIN:$PATH" MOCK_STATE="$STATE" MOCK_HEALTH="$HEALTH" MOCK_CALLS="$CALLS" MOCK_IMAGE="$image" \
  SUB2API_PAYMENT_VAULT_CONTAINER_ALLOW_NON_ROOT_FOR_TESTS=1 \
  SUB2API_MAINTENANCE_LOCK_FILE="$LOCK_FILE" bash "$SCRIPT" prepare "$image" >"$OUTPUT"
grep -qx 'SUB2API_PAYMENT_VAULT_CONTAINER_WAITING_FOR_INJECTION' "$OUTPUT" || fail 'prepare classification drifted'
grep -q -- '--network none --read-only --init --restart unless-stopped --cap-drop ALL' "$CALLS" || fail 'container hardening flags missing'
grep -q -- '--allowed-ref vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64' "$CALLS" || fail 'exact request key reference missing'

printf 'healthy\n' >"$HEALTH"
PATH="$FAKE_BIN:$PATH" MOCK_STATE="$STATE" MOCK_HEALTH="$HEALTH" MOCK_CALLS="$CALLS" MOCK_IMAGE="$image" \
  SUB2API_PAYMENT_VAULT_CONTAINER_ALLOW_NON_ROOT_FOR_TESTS=1 \
  SUB2API_MAINTENANCE_LOCK_FILE="$LOCK_FILE" bash "$SCRIPT" ready "$image" >"$OUTPUT"
grep -qx 'SUB2API_PAYMENT_VAULT_CONTAINER_READY' "$OUTPUT" || fail 'ready classification drifted'

before="$(wc -l <"$CALLS")"
if PATH="$FAKE_BIN:$PATH" MOCK_STATE="$STATE" MOCK_HEALTH="$HEALTH" MOCK_CALLS="$CALLS" MOCK_IMAGE="$image" \
  SUB2API_PAYMENT_VAULT_CONTAINER_ALLOW_NON_ROOT_FOR_TESTS=1 \
  SUB2API_MAINTENANCE_LOCK_FILE="$LOCK_FILE" bash "$SCRIPT" prepare 'sub2api:latest' >"$OUTPUT" 2>&1; then
  fail 'unpinned image was accepted'
fi
[ "$before" = "$(wc -l <"$CALLS")" ] || fail 'rejected image reached Docker'

printf 'Unified payment Vault container tests passed.\n'
