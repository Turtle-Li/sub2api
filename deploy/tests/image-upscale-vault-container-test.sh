#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$(cd "$TEST_DIR/.." && pwd)/sub2api-image-upscale-vault-container.sh"
COMPOSE_FILE="$(cd "$TEST_DIR/.." && pwd)/docker-compose.yml"
DEPLOY_DIR="$(cd "$TEST_DIR/.." && pwd)"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-image-upscale-vault-container.XXXXXX")"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
FAKE_BIN="$TEST_ROOT/bin"
LOCK_DIR="$TEST_ROOT/locks"
LOCK_FILE="$LOCK_DIR/maintenance.lock"
STATE="$TEST_ROOT/container-created"
HEALTH="$TEST_ROOT/health"
CALLS="$TEST_ROOT/calls"
OUTPUT="$TEST_ROOT/output"
CONFIG_FILE="$TEST_ROOT/autodeploy.env"
UPSCALE_VAULT_REF='vault://secret/data/infrastructure/office-mini-upscale-api-public-key-20260928#api_key'
COS_ACCESS_VAULT_REF='vault://secret/data/test/sub2api-batch-image#access_key_id'
COS_SECRET_VAULT_REF='vault://secret/data/test/sub2api-batch-image#secret_access_key'
trap 'rm -rf "$TEST_ROOT"' EXIT

fail() {
  printf 'Image upscale Vault container test failed: %s\n' "$*" >&2
  exit 1
}

run_script() {
  PATH="$FAKE_BIN:$PATH" MOCK_STATE="$STATE" MOCK_HEALTH="$HEALTH" MOCK_CALLS="$CALLS" MOCK_IMAGE="$image" \
    MOCK_PLATFORM="${MOCK_PLATFORM:-linux/amd64}" MOCK_REVISION="${MOCK_REVISION:-}" \
    MOCK_SOURCE="${MOCK_SOURCE:-}" MOCK_VERSION="${MOCK_VERSION-0.1.186}" \
    MOCK_NETWORK="${MOCK_NETWORK:-none}" MOCK_USER="${MOCK_USER:-1000:1000}" \
    MOCK_INIT_FAIL="${MOCK_INIT_FAIL:-0}" \
    SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_ALLOW_NON_ROOT_FOR_TESTS=1 \
    SUB2API_MAINTENANCE_LOCK_FILE="$LOCK_FILE" \
    SUB2API_AUTODEPLOY_CONFIG_FILE="$CONFIG_FILE" bash "$SCRIPT" "$@"
}

run_without_non_root_allowance() {
  PATH="$FAKE_BIN:$PATH" MOCK_STATE="$STATE" MOCK_HEALTH="$HEALTH" MOCK_CALLS="$CALLS" MOCK_IMAGE="$image" \
    MOCK_ID_UID=1000 SUB2API_MAINTENANCE_LOCK_FILE="$LOCK_FILE" \
    SUB2API_AUTODEPLOY_CONFIG_FILE="$CONFIG_FILE" bash "$SCRIPT" "$@"
}

run_count() {
  grep -c '^run -d' "$CALLS" || true
}

init_count() {
  grep -c '^run --rm' "$CALLS" || true
}

assert_no_ref_in_output() {
  for reference in "$UPSCALE_VAULT_REF" "$COS_ACCESS_VAULT_REF" "$COS_SECRET_VAULT_REF"; do
    if grep -Fq -- "$reference" "$OUTPUT"; then
      fail 'script output exposed a Vault reference'
    fi
  done
}

assert_no_secret_in_output_or_calls() {
  local secret
  for secret in "$@"; do
    if grep -Fq -- "$secret" "$OUTPUT" || grep -Fq -- "$secret" "$CALLS"; then
      fail 'secret escaped stdin-only injection'
    fi
  done
}

reject_identity() {
  local label="$1" platform="$2" revision="$3" source="$4" version="$5" before_runs

  before_runs="$(run_count)"
  if MOCK_PLATFORM="$platform" MOCK_REVISION="$revision" MOCK_SOURCE="$source" MOCK_VERSION="$version" \
    run_script prepare "$image" >"$OUTPUT" 2>&1; then
    fail "$label was accepted"
  fi
  [ "$before_runs" = "$(run_count)" ] || fail "$label started a sidecar"
  assert_no_ref_in_output
}

mkdir -p "$FAKE_BIN" "$LOCK_DIR"
chmod 700 "$LOCK_DIR"
printf 'starting\n' >"$HEALTH"
cat >"$CONFIG_FILE" <<EOF
IMAGE_UPSCALE_API_KEY_VAULT_REF=$UPSCALE_VAULT_REF
BATCH_IMAGE_DELIVERY_COS_ACCESS_KEY_VAULT_REF=$COS_ACCESS_VAULT_REF
BATCH_IMAGE_DELIVERY_COS_SECRET_ACCESS_KEY_VAULT_REF=$COS_SECRET_VAULT_REF
BATCH_IMAGE_DELIVERY_COS_VAULT_AGENT_SOCKET=/run/sub2api-upscale-vault/public.sock
EOF
chmod 600 "$CONFIG_FILE"

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
cat >"$FAKE_BIN/id" <<'EOF_ID'
#!/usr/bin/env bash
set -eu

if [ "${MOCK_ID_UID:-}" != "" ] && [ "$#" -eq 1 ] && [ "$1" = -u ]; then
  printf '%s\n' "$MOCK_ID_UID"
else
  exec /usr/bin/id "$@"
fi
EOF_ID
cat >"$FAKE_BIN/docker" <<'EOF_DOCKER'
#!/usr/bin/env bash
set -eu

printf '%s\n' "$*" >>"$MOCK_CALLS"
public_dir=/run/sub2api-upscale-vault
admin_dir=/run/sub2api-upscale-vault-admin
volume=sub2api_image_upscale_vault
upscale_ref='vault://secret/data/infrastructure/office-mini-upscale-api-public-key-20260928#api_key'
cos_access_ref='vault://secret/data/test/sub2api-batch-image#access_key_id'
cos_secret_ref='vault://secret/data/test/sub2api-batch-image#secret_access_key'

case "$1:$2" in
  image:inspect)
    case "$5" in
      *'.Os}}/{{.Architecture}}'*) printf '%s\n' "${MOCK_PLATFORM:-linux/amd64}" ;;
      *'image.revision'*)
        if [ "${MOCK_REVISION:-}" != "" ]; then
          printf '%s\n' "$MOCK_REVISION"
        else
          printf '%s\n' "${3#sub2api:prebuilt-}"
        fi
        ;;
      *'image.source'*)
        if [ "${MOCK_SOURCE:-}" != "" ]; then
          printf '%s\n' "$MOCK_SOURCE"
        else
          printf '%s\n' 'https://github.com/Turtle-Li/sub2api'
        fi
        ;;
      *'image.version'*) printf '%s\n' "${MOCK_VERSION-0.1.186}" ;;
      *) exit 1 ;;
    esac
    ;;
  container:inspect)
    [ -f "$MOCK_STATE" ] || exit 1
    [ "${4:-}" = --format ] || exit 0
    case "$5" in
      *'.Config.Image'*) printf '%s\n' "${MOCK_CONTAINER_IMAGE:-$MOCK_IMAGE}" ;;
      *'.State.Running'*) printf '%s\n' "${MOCK_RUNNING:-true}" ;;
      *'.Config.User'*) printf '%s\n' "${MOCK_USER:-1000:1000}" ;;
      *'.State.Health.Status'*) cat "$MOCK_HEALTH" ;;
      *'.HostConfig.NetworkMode'*) printf '%s\n' "${MOCK_NETWORK:-none}" ;;
      *'.HostConfig.ReadonlyRootfs'*) printf '%s\n' "${MOCK_READ_ONLY:-true}" ;;
      *'.HostConfig.RestartPolicy.Name'*) printf '%s\n' "${MOCK_RESTART:-unless-stopped}" ;;
      *'.HostConfig.PidsLimit'*) printf '%s\n' "${MOCK_PIDS_LIMIT:-64}" ;;
      *'.HostConfig.Init'*) printf '%s\n' "${MOCK_INIT:-true}" ;;
      *'.HostConfig.CapDrop'*) printf '%s\n' "${MOCK_CAP_DROP:-[\"ALL\"]}" ;;
      *'.HostConfig.SecurityOpt'*) printf '%s\n' "${MOCK_SECURITY:-[\"no-new-privileges\"]}" ;;
      *"$admin_dir"*) printf '%s\n' "${MOCK_ADMIN_TMPFS:-rw,noexec,nosuid,nodev,size=1m,mode=0700,uid=1000,gid=1000}" ;;
      *'.HostConfig.Tmpfs'*'/tmp'*) printf '%s\n' "${MOCK_TMP_TMPFS:-rw,noexec,nosuid,nodev,size=4m,mode=0700,uid=1000,gid=1000}" ;;
      *'range .Mounts'*) printf 'volume|%s|%s|true\n' "$volume" "$public_dir" ;;
      *'.Config.Cmd'*) printf '["/app/sub2api-vault-agent","serve","--public-socket","%s/public.sock","--admin-socket","%s/admin.sock","--allowed-ref","%s","--allowed-ref","%s","--allowed-ref","%s"]\n' "$public_dir" "$admin_dir" "$upscale_ref" "$cos_access_ref" "$cos_secret_ref" ;;
      *'.Config.Healthcheck.Test'*) printf '["CMD-SHELL","/app/sub2api-vault-agent check --public-socket %s/public.sock"]\n' "$public_dir" ;;
      *) exit 1 ;;
    esac
    ;;
  volume:create) printf '%s\n' "$volume" ;;
  run:--rm)
    [ "${MOCK_INIT_FAIL:-0}" = 0 ] || exit 1
    printf 'init-container-id\n'
    ;;
  run:-d)
    touch "$MOCK_STATE"
    printf 'container-id\n'
    ;;
  exec:*)
    if [ "${2:-}" = -i ]; then
      cat >/dev/null
    fi
    ;;
  *) exit 1 ;;
esac
EOF_DOCKER
chmod +x "$FAKE_BIN/flock" "$FAKE_BIN/id" "$FAKE_BIN/docker"

image='sub2api:prebuilt-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
grep -A4 '^  image_upscale_vault:$' "$COMPOSE_FILE" | grep -qx '    driver: local' \
  || fail 'Compose must provision the disabled-by-default socket volume'
for unsupported_compose in docker-compose.dev.yml docker-compose.local.yml docker-compose.standalone.yml; do
  [ "$(grep -c '^      - IMAGE_UPSCALE_ENABLED=false$' "$DEPLOY_DIR/$unsupported_compose")" -eq 1 ] \
    || fail "$unsupported_compose must force image upscale off"
  [ "$(grep -c '^      - IMAGE_UPSCALE_' "$DEPLOY_DIR/$unsupported_compose")" -eq 1 ] \
    || fail "$unsupported_compose exposes unsupported image upscale settings"
done
run_script prepare "$image" >"$OUTPUT"
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_WAITING_FOR_INJECTION' "$OUTPUT" || fail 'prepare classification drifted'
assert_no_ref_in_output
grep -q -- '--network none --read-only --init --restart unless-stopped --cap-drop ALL --security-opt no-new-privileges --pids-limit 64 --user 1000:1000' "$CALLS" \
  || fail 'long-lived agent hardening flags missing'
grep -q -- '--tmpfs /run/sub2api-upscale-vault-admin:rw,noexec,nosuid,nodev,size=1m,mode=0700,uid=1000,gid=1000 --tmpfs /tmp:rw,noexec,nosuid,nodev,size=4m,mode=0700,uid=1000,gid=1000' "$CALLS" \
  || fail 'long-lived agent tmpfs hardening drifted'
grep -q -- 'run --rm --network none --read-only --cap-drop ALL --cap-add CHOWN --cap-add FOWNER --security-opt no-new-privileges --pids-limit 16 --user 0:0 --mount type=volume,source=sub2api_image_upscale_vault,target=/run/sub2api-upscale-vault --entrypoint /bin/sh' "$CALLS" \
  || fail 'volume initializer hardening or mount scope drifted'
grep -q -- "chown 1000:1000 \"\$socket_dir\"" "$CALLS" || fail 'volume initializer ownership repair drifted'
grep -q -- "chmod 0700 \"\$socket_dir\"" "$CALLS" || fail 'volume initializer mode repair drifted'
grep -q -- 'run -d --name sub2api-upscale-vault --network none --read-only --init --restart unless-stopped --cap-drop ALL --security-opt no-new-privileges --pids-limit 64 --user 1000:1000' "$CALLS" \
  || fail 'main agent non-root hardening drifted'
grep -q -- "--allowed-ref $UPSCALE_VAULT_REF --allowed-ref $COS_ACCESS_VAULT_REF --allowed-ref $COS_SECRET_VAULT_REF" "$CALLS" \
  || fail 'three exact allowed references are missing'
[ "$(init_count)" = 1 ] || fail 'prepare did not run exactly one volume initializer'

before_runs="$(run_count)"
before_inits="$(init_count)"
run_script prepare "$image" >"$OUTPUT"
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_WAITING_FOR_INJECTION' "$OUTPUT" || fail 'repeated prepare classification drifted'
[ "$before_runs" = "$(run_count)" ] || fail 'repeated prepare replaced the sidecar'
[ "$before_inits" = "$(init_count)" ] || fail 'repeated prepare reinitialized the socket volume'

printf 'healthy\n' >"$HEALTH"
run_script ready "$image" >"$OUTPUT"
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_READY' "$OUTPUT" || fail 'ready classification drifted'
[ "$before_runs" = "$(run_count)" ] || fail 'ready replaced the sidecar'

run_script ready-auto "$image" >"$OUTPUT"
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_READY' "$OUTPUT" || fail 'ready-auto classification drifted'
[ "$before_runs" = "$(run_count)" ] || fail 'ready-auto replaced the sidecar'

# The historical single-key operation remains available, while the two COS
# values use independent stdin-only loads. Neither secret may become a Docker
# argument, command log, or helper output.
api_secret='api-secret-must-not-appear'
access_secret='access-secret-must-not-appear'
cos_secret='cos-secret-must-not-appear'
printf '%s\n' "$api_secret" | run_script load-api "$image" >"$OUTPUT"
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_LOADED' "$OUTPUT" || fail 'single-key load classification drifted'
assert_no_secret_in_output_or_calls "$api_secret"
printf '%s\n%s\n' "$access_secret" "$cos_secret" | run_script load-batch-cos "$image" >"$OUTPUT"
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_LOADED' "$OUTPUT" || fail 'COS load classification drifted'
assert_no_secret_in_output_or_calls "$access_secret" "$cos_secret"
printf '%s\n%s\n%s\n' "$api_secret" "$access_secret" "$cos_secret" | run_script load-all "$image" >"$OUTPUT"
grep -qx 'SUB2API_IMAGE_UPSCALE_VAULT_CONTAINER_READY' "$OUTPUT" || fail 'combined load readiness drifted'
assert_no_secret_in_output_or_calls "$api_secret" "$access_secret" "$cos_secret"
grep -q -- "exec -i sub2api-upscale-vault /app/sub2api-vault-agent load --admin-socket /run/sub2api-upscale-vault-admin/admin.sock --ref $UPSCALE_VAULT_REF" "$CALLS" \
  || fail 'single-key load did not target the exact approved reference'
grep -q -- "exec -i sub2api-upscale-vault /app/sub2api-vault-agent load --admin-socket /run/sub2api-upscale-vault-admin/admin.sock --ref $COS_ACCESS_VAULT_REF" "$CALLS" \
  || fail 'COS access-key load did not target the exact configured reference'
grep -q -- "exec -i sub2api-upscale-vault /app/sub2api-vault-agent load --admin-socket /run/sub2api-upscale-vault-admin/admin.sock --ref $COS_SECRET_VAULT_REF" "$CALLS" \
  || fail 'COS secret-key load did not target the exact configured reference'

before_runs="$(run_count)"
if MOCK_NETWORK=bridge run_script ready "$image" >"$OUTPUT" 2>&1; then
  fail 'network configuration drift was accepted'
fi
[ "$before_runs" = "$(run_count)" ] || fail 'configuration drift replaced the sidecar'
assert_no_ref_in_output
if grep -Eq '^(rm|stop|container rm|container stop)' "$CALLS"; then
  fail 'sidecar lifecycle command was issued'
fi

before_runs="$(run_count)"
if MOCK_USER=0:0 run_script ready "$image" >"$OUTPUT" 2>&1; then
  fail 'root-running agent was accepted'
fi
[ "$before_runs" = "$(run_count)" ] || fail 'root-running agent was replaced'
assert_no_ref_in_output

before_calls="$(wc -l <"$CALLS")"
if run_without_non_root_allowance prepare "$image" >"$OUTPUT" 2>&1; then
  fail 'non-root caller was accepted'
fi
[ "$before_calls" = "$(wc -l <"$CALLS")" ] || fail 'non-root rejection reached Docker'
assert_no_ref_in_output

# A root initializer failure must not result in a long-lived agent.
rm -f "$STATE"
before_runs="$(run_count)"
if MOCK_INIT_FAIL=1 run_script prepare "$image" >"$OUTPUT" 2>&1; then
  fail 'failed volume initializer started the agent'
fi
[ "$before_runs" = "$(run_count)" ] || fail 'failed volume initializer started the agent'
assert_no_ref_in_output

before_calls="$(wc -l <"$CALLS")"
if run_script load "$image" >"$OUTPUT" 2>&1; then
  fail 'unsupported action was accepted'
fi
[ "$before_calls" = "$(wc -l <"$CALLS")" ] || fail 'unsupported action reached Docker'
assert_no_ref_in_output

before_calls="$(wc -l <"$CALLS")"
if run_script prepare 'sub2api:latest' >"$OUTPUT" 2>&1; then
  fail 'unpinned image was accepted'
fi
[ "$before_calls" = "$(wc -l <"$CALLS")" ] || fail 'unpinned image reached Docker'
assert_no_ref_in_output

reject_identity 'non-amd64 image' linux/arm64 '' '' '0.1.186'
reject_identity 'revision label mismatch' linux/amd64 bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb '' '0.1.186'
reject_identity 'source label mismatch' linux/amd64 '' https://example.invalid/sub2api '0.1.186'
reject_identity 'empty version label' linux/amd64 '' '' ''

# The two COS refs are an atomic same-item profile. A root-owned config that
# points them at different paths fails before Docker receives any command.
cat >"$CONFIG_FILE" <<EOF
IMAGE_UPSCALE_API_KEY_VAULT_REF=$UPSCALE_VAULT_REF
BATCH_IMAGE_DELIVERY_COS_ACCESS_KEY_VAULT_REF=$COS_ACCESS_VAULT_REF
BATCH_IMAGE_DELIVERY_COS_SECRET_ACCESS_KEY_VAULT_REF=vault://secret/data/test/other-batch-image#secret_access_key
BATCH_IMAGE_DELIVERY_COS_VAULT_AGENT_SOCKET=/run/sub2api-upscale-vault/public.sock
EOF
chmod 600 "$CONFIG_FILE"
before_calls="$(wc -l <"$CALLS")"
if run_script prepare "$image" >"$OUTPUT" 2>&1; then
  fail 'mismatched Batch Image COS Vault paths were accepted'
fi
[ "$before_calls" = "$(wc -l <"$CALLS")" ] || fail 'mismatched COS refs reached Docker'
assert_no_ref_in_output

printf 'Image upscale Vault container tests passed.\n'
