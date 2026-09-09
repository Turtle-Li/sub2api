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

run_script() {
  local mock_profile="$1"
  shift
  PATH="$FAKE_BIN:$PATH" MOCK_STATE="$STATE" MOCK_HEALTH="$HEALTH" MOCK_CALLS="$CALLS" MOCK_IMAGE="$image" MOCK_PROFILE="$mock_profile" \
    MOCK_INIT_FAIL="${MOCK_INIT_FAIL:-0}" MOCK_USER="${MOCK_USER:-1000:1000}" \
    SUB2API_PAYMENT_VAULT_CONTAINER_ALLOW_NON_ROOT_FOR_TESTS=1 \
    SUB2API_MAINTENANCE_LOCK_FILE="$LOCK_FILE" bash "$SCRIPT" "$@"
}

run_count() {
  grep -c '^run -d' "$CALLS" || true
}

init_count() {
  grep -c '^run --rm' "$CALLS" || true
}

mkdir -p "$FAKE_BIN" "$LOCK_DIR"
chmod 700 "$LOCK_DIR"
printf 'starting\n' >"$HEALTH"

cat >"$FAKE_BIN/flock" <<'EOF_FLOCK'
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
EOF_FLOCK
cat >"$FAKE_BIN/docker" <<'EOF_DOCKER'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >>"$MOCK_CALLS"
profile="${MOCK_PROFILE:-sandbox}"
case "$profile" in
  sandbox) request_ref='vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64' ;;
  live) request_ref='vault://secret/data/sub2api/unified-payment/live#request_private_key_base64' ;;
  *) exit 1 ;;
esac
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
      *'.Config.User'*) printf '%s\n' "${MOCK_USER:-1000:1000}" ;;
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
      *'.Config.Cmd'*) printf '["/app/sub2api-vault-agent","serve","--public-socket","/run/sub2api-payment-vault/public.sock","--admin-socket","/run/sub2api-payment-vault-admin/admin.sock","--allowed-ref","%s"]\n' "$request_ref" ;;
      *'.Config.Healthcheck.Test'*) printf '["CMD-SHELL","/app/sub2api-vault-agent check --public-socket /run/sub2api-payment-vault/public.sock"]\n' ;;
      *) exit 1 ;;
    esac
    ;;
  volume:create) printf 'sub2api_unified_payment_vault\n' ;;
  run:--rm)
    [ "${MOCK_INIT_FAIL:-0}" = 0 ] || exit 1
    printf 'init-container-id\n'
    ;;
  run:-d) touch "$MOCK_STATE" ; printf 'container-id\n' ;;
  *) exit 1 ;;
esac
EOF_DOCKER
chmod +x "$FAKE_BIN/flock" "$FAKE_BIN/docker"

image="sub2api:prebuilt-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
run_script sandbox prepare "$image" >"$OUTPUT"
grep -qx 'SUB2API_PAYMENT_VAULT_CONTAINER_WAITING_FOR_INJECTION' "$OUTPUT" || fail 'sandbox prepare classification drifted'
grep -q -- '--network none --read-only --init --restart unless-stopped --cap-drop ALL' "$CALLS" || fail 'container hardening flags missing'
grep -q -- 'run --rm --network none --read-only --cap-drop ALL --cap-add CHOWN --cap-add FOWNER --security-opt no-new-privileges --pids-limit 16 --user 0:0 --mount type=volume,source=sub2api_unified_payment_vault,target=/run/sub2api-payment-vault --entrypoint /bin/sh' "$CALLS" \
  || fail 'volume initializer hardening or mount scope drifted'
grep -q -- 'chown 1000:1000 "$socket_dir"' "$CALLS" || fail 'volume initializer ownership repair drifted'
grep -q -- 'chmod 0700 "$socket_dir"' "$CALLS" || fail 'volume initializer mode repair drifted'
grep -q -- 'run -d --name sub2api-payment-vault --network none --read-only --init --restart unless-stopped --cap-drop ALL --security-opt no-new-privileges --pids-limit 64 --user 1000:1000' "$CALLS" \
  || fail 'main agent non-root hardening drifted'
[ "$(init_count)" = 1 ] || fail 'prepare did not run exactly one volume initializer'
grep -q -- '--allowed-ref vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64' "$CALLS" || fail 'sandbox default request reference missing'

printf 'healthy\n' >"$HEALTH"
run_script sandbox ready "$image" >"$OUTPUT"
grep -qx 'SUB2API_PAYMENT_VAULT_CONTAINER_READY' "$OUTPUT" || fail 'sandbox ready classification drifted'
[ "$(init_count)" = 1 ] || fail 'ready unexpectedly reinitialized the socket volume'

before_runs="$(run_count)"
if run_script sandbox --profile live prepare "$image" >"$OUTPUT" 2>&1; then
  fail 'live profile replaced a running sandbox sidecar'
fi
grep -qx 'SUB2API_PAYMENT_VAULT_CONTAINER_PROFILE_CONFLICT_REQUIRES_MAINTENANCE_MIGRATION' "$OUTPUT" \
  || fail 'profile conflict classification drifted'
[ "$before_runs" = "$(run_count)" ] || fail 'profile conflict started a replacement sidecar'

# This models a separately completed, lock-owned maintenance migration. The
# sidecar script itself is never given a Docker stop/remove operation.
python3 - "$STATE" <<'PY'
from pathlib import Path
import sys

Path(sys.argv[1]).unlink()
PY
run_script live --profile live prepare "$image" >"$OUTPUT"
grep -qx 'SUB2API_PAYMENT_VAULT_CONTAINER_WAITING_FOR_INJECTION' "$OUTPUT" || fail 'live prepare classification drifted'
grep -q -- '--allowed-ref vault://secret/data/sub2api/unified-payment/live#request_private_key_base64' "$CALLS" || fail 'live request reference missing'
run_script live --profile live ready "$image" >"$OUTPUT"
grep -qx 'SUB2API_PAYMENT_VAULT_CONTAINER_READY' "$OUTPUT" || fail 'live ready classification drifted'

if MOCK_USER=0:0 run_script live --profile live ready "$image" >"$OUTPUT" 2>&1; then
  fail 'root-running agent passed verification'
fi

# The init container must fail closed before it can start a long-lived agent.
python3 - "$STATE" <<'PY'
from pathlib import Path
import sys

Path(sys.argv[1]).unlink()
PY
before_runs="$(run_count)"
if MOCK_INIT_FAIL=1 run_script live --profile live prepare "$image" >"$OUTPUT" 2>&1; then
  fail 'failed volume initializer started the agent'
fi
[ "$before_runs" = "$(run_count)" ] || fail 'failed volume initializer started the agent'

before_calls="$(wc -l <"$CALLS")"
if run_script live --profile production prepare "$image" >"$OUTPUT" 2>&1; then
  fail 'unsupported profile was accepted'
fi
[ "$before_calls" = "$(wc -l <"$CALLS")" ] || fail 'unsupported profile reached Docker'

before_calls="$(wc -l <"$CALLS")"
if run_script live --profile live prepare 'sub2api:latest' >"$OUTPUT" 2>&1; then
  fail 'unpinned image was accepted'
fi
[ "$before_calls" = "$(wc -l <"$CALLS")" ] || fail 'unpinned image reached Docker'
grep -q 'vault://secret/data/sub2api/unified-payment/live#request_private_key_base64' "$OUTPUT" && fail 'test output exposed a Vault reference'
printf 'Unified payment Vault container tests passed.\n'
