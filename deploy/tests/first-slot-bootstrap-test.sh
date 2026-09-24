#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${TEST_DIR}/../.." && pwd)"

if [ "$(uname -s)" = Darwin ] && [ "${SUB2API_FIRST_SLOT_TEST_IN_LINUX:-0}" != 1 ]; then
  docker run --rm \
    -e SUB2API_FIRST_SLOT_TEST_IN_LINUX=1 \
    -v "${REPO_ROOT}:/repo:ro" \
    python:3.12-bookworm \
    bash /repo/deploy/tests/first-slot-bootstrap-test.sh
  exit $?
fi

SCRIPT="${REPO_ROOT}/deploy/sub2api-first-slot-bootstrap.sh"
LOCK_HELPER="${REPO_ROOT}/deploy/sub2api-maintenance-lock.sh"
TEST_ROOT="$(mktemp -d /root/sub2api-first-slot-test.XXXXXX)"
trap 'rm -rf "$TEST_ROOT"' EXIT
BIN="${TEST_ROOT}/bin"
STATE="${TEST_ROOT}/docker-state"
APP_DIR="${TEST_ROOT}/app"
LOCK_DIR="${TEST_ROOT}/lock"
mkdir -p "$BIN" "$STATE/containers" "$STATE/volumes" "$STATE/networks" "$APP_DIR/secrets" "$APP_DIR/db-host-ca" "$LOCK_DIR"
chmod 0700 "$LOCK_DIR"

cp "$SCRIPT" "${TEST_ROOT}/sub2api-first-slot-bootstrap.sh"
cp "$LOCK_HELPER" "${TEST_ROOT}/sub2api-maintenance-lock.sh"
chmod +x "${TEST_ROOT}/sub2api-first-slot-bootstrap.sh"

cat >"$BIN/docker" <<'SH'
#!/usr/bin/env bash
set -Eeuo pipefail
state="${FAKE_DOCKER_STATE:?}"
printf '%q ' "$@" >>"$state/calls"
printf '\n' >>"$state/calls"
kind="${1:-}"
shift || true

inspect_value() {
  local name="$1" format="$2" root="$state/containers/$name"
  case "$name:$format" in
    sub2api-payment-vault:'{{.State.Running}}'|sub2api-feishu-vault:'{{.State.Running}}') printf 'true\n' ;;
    sub2api-payment-vault:'{{.State.Health.Status}}'|sub2api-feishu-vault:'{{.State.Health.Status}}') printf 'healthy\n' ;;
    *:'{{.Config.Image}}') cat "$root/image" ;;
    *:'{{.State.Running}}') cat "$root/running" ;;
    *:'{{.State.Health.Status}}') cat "$root/health" ;;
    *) exit 1 ;;
  esac
}

case "$kind" in
  image)
    [ "${1:-}" = inspect ] || exit 1
    image="$2"; shift 2
    [ "$image" = "${FAKE_IMAGE:?}" ] || exit 1
    [ "${1:-}" = --format ] || exit 1
    case "$2" in
      '{{.Os}}/{{.Architecture}}') printf 'linux/amd64\n' ;;
      '{{index .Config.Labels "org.opencontainers.image.source"}}') printf 'https://github.com/Turtle-Li/sub2api\n' ;;
      '{{index .Config.Labels "org.opencontainers.image.revision"}}') printf '%s\n' "${FAKE_REVISION:?}" ;;
      '{{index .Config.Labels "org.opencontainers.image.version"}}') printf '0.2.8\n' ;;
      *) exit 1 ;;
    esac
    ;;
  container)
    [ "${1:-}" = inspect ] || exit 1
    name="$2"
    case "$name" in
      sub2api-payment-vault|sub2api-feishu-vault) exit 0 ;;
    esac
    [ -d "$state/containers/$name" ]
    ;;
  inspect)
    name="$1"; shift
    case "$name" in
      sub2api-payment-vault|sub2api-feishu-vault) ;;
      *) [ -d "$state/containers/$name" ] || exit 1 ;;
    esac
    [ "${1:-}" = --format ] || exit 1
    inspect_value "$name" "$2"
    ;;
  network)
    action="$1"; name="${@: -1}"
    case "$action" in
      inspect) [ -e "$state/networks/$name" ] ;;
      create) : >"$state/networks/$name"; printf '%s\n' "$name" ;;
      *) exit 1 ;;
    esac
    ;;
  volume)
    action="$1"; name="${@: -1}"
    case "$action" in
      inspect) [ -e "$state/volumes/$name" ] ;;
      create) : >"$state/volumes/$name"; printf '%s\n' "$name" ;;
      *) exit 1 ;;
    esac
    ;;
  create)
    name= env_file= image=
    mounts="$state/create-mounts"
    : >"$mounts"
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --name) name="$2"; shift 2 ;;
        --network|--env-file|--mount|--restart)
          key="$1"; value="$2"; shift 2
          [ "$key" != --env-file ] || env_file="$value"
          [ "$key" != --mount ] || printf '%s\n' "$value" >>"$mounts"
          ;;
        --*) exit 1 ;;
        *) image="$1"; shift ;;
      esac
    done
    [ -n "$name" ] && [ -n "$env_file" ] && [ "$image" = "${FAKE_IMAGE:?}" ]
    root="$state/containers/$name"
    mkdir "$root"
    cp "$env_file" "$root/env"
    printf '%s\n' "$image" >"$root/image"
    printf 'false\n' >"$root/running"
    printf 'created\n' >"$root/health"
    printf '%s\n' "$name"
    ;;
  update) exit 0 ;;
  start)
    root="$state/containers/$1"
    printf 'true\n' >"$root/running"
    printf 'healthy\n' >"$root/health"
    printf '%s\n' "$1"
    ;;
  stop)
    name="${@: -1}"
    printf 'false\n' >"$state/containers/$name/running"
    ;;
  exec) exit 0 ;;
  *) exit 1 ;;
esac
SH
chmod +x "$BIN/docker"

REVISION=47c28fe46956fbac22d8bfb4c0b42a261f6ef4f9
IMAGE=sub2api:auto-20260924-220350-47c28fe4
APP_ENV="${TEST_ROOT}/candidate-app.env"
EXTERNAL_ENV="${TEST_ROOT}/external.env"
CA_FILE="${APP_DIR}/db-host-ca/ca.crt"
HEALTH_FILE="${APP_DIR}/secrets/internal-health-token"
TRAFFIC_FILE="${TEST_ROOT}/runtime/traffic-state"
BACKGROUND_DIR="${TEST_ROOT}/runtime/background"

cat >"$APP_ENV" <<'EOF'
DATABASE_HOST=old-db.invalid
DATABASE_PORT=1
DATABASE_USER=old
DATABASE_PASSWORD=old-secret
DATABASE_DBNAME=old
DATABASE_SSLMODE=disable
REDIS_HOST=old-redis.invalid
REDIS_PORT=2
REDIS_USERNAME=old
REDIS_PASSWORD=old-secret
REDIS_DB=0
REDIS_ENABLE_TLS=false
PGSSLROOTCERT=/old/ca
UNIFIED_PAYMENT_ENABLED=true
UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_BASE64=must-not-survive
UNIFIED_PAYMENT_VAULT_AGENT_SOCKET=/run/sub2api-payment-vault/public.sock
SUB2API_FEISHU_ENABLED=true
SUB2API_FEISHU_WEBHOOK_URL=must-not-survive
SUB2API_TRAFFIC_STATE_FILE=/old/traffic
SUB2API_BACKGROUND_STATE_FILE=/old/background
SUB2API_INTERNAL_HEALTH_TOKEN_FILE=/old/health
SUB2API_IMAGE=old:image
RUN_MODE=production
DOMAIN=
EOF
cat >"$EXTERNAL_ENV" <<'EOF'
DATABASE_HOST=db.example.internal
DATABASE_PORT=5432
DATABASE_USER=sub2api
DATABASE_PASSWORD=new-db-secret
DATABASE_DBNAME=sub2api
DATABASE_SSLMODE=verify-full
REDIS_HOST=redis.example.internal
REDIS_PORT=6379
REDIS_USERNAME=default
REDIS_PASSWORD=new-redis-secret
REDIS_DB=0
REDIS_ENABLE_TLS=true
EOF
printf 'test-ca\n' >"$CA_FILE"
printf 'health-token\n' >"$HEALTH_FILE"
chmod 0600 "$APP_ENV" "$EXTERNAL_ENV" "$HEALTH_FILE"
chmod 0644 "$CA_FILE"
chown 1000:1000 "$HEALTH_FILE"
: >"$STATE/volumes/sub2api_unified_payment_vault"
: >"$STATE/volumes/sub2api_feishu_vault"

run_bootstrap() {
  PATH="$BIN:$PATH" \
  FAKE_DOCKER_STATE="$STATE" \
  FAKE_IMAGE="$IMAGE" \
  FAKE_REVISION="$REVISION" \
  SUB2API_APP_DIR="$APP_DIR" \
  SUB2API_FIRST_SLOT_IMAGE="$IMAGE" \
  SUB2API_FIRST_SLOT_EXPECTED_REVISION="$REVISION" \
  SUB2API_FIRST_SLOT_APP_ENV_FILE="$APP_ENV" \
  SUB2API_EXTERNAL_RUNTIME_ENV_FILE="$EXTERNAL_ENV" \
  SUB2API_EXTERNAL_CA_FILE="$CA_FILE" \
  SUB2API_TRAFFIC_STATE_FILE_HOST="$TRAFFIC_FILE" \
  SUB2API_BACKGROUND_STATE_DIR="$BACKGROUND_DIR" \
  SUB2API_INTERNAL_HEALTH_TOKEN_FILE_HOST="$HEALTH_FILE" \
  SUB2API_MAINTENANCE_LOCK_FILE="$LOCK_DIR/maintenance.lock" \
  SUB2API_FIRST_SLOT_ALLOW_NON_ROOT_FOR_TESTS=1 \
  SUB2API_FIRST_SLOT_HEALTH_ATTEMPTS=1 \
  SUB2API_FIRST_SLOT_HEALTH_INTERVAL_SECONDS=1 \
  bash "${TEST_ROOT}/sub2api-first-slot-bootstrap.sh" "$@"
}

output="$(run_bootstrap bootstrap)"
[ "$output" = 'SUB2API_FIRST_SLOT_BOOTSTRAPPED traffic=draining background=standby' ]
[ "$(cat "$TRAFFIC_FILE")" = draining ]
[ "$(cat "$BACKGROUND_DIR/sub2api-blue")" = standby ]
RUNTIME_ENV="$STATE/containers/sub2api-blue/env"
grep -qx 'DATABASE_HOST=db.example.internal' "$RUNTIME_ENV"
grep -qx 'DATABASE_SSLMODE=verify-full' "$RUNTIME_ENV"
grep -qx 'REDIS_ENABLE_TLS=true' "$RUNTIME_ENV"
grep -qx 'SUB2API_TRAFFIC_STATE_FILE=/run/sub2api-runtime/traffic-state' "$RUNTIME_ENV"
grep -qx "SUB2API_IMAGE=$IMAGE" "$RUNTIME_ENV"
grep -qx 'DOMAIN=' "$RUNTIME_ENV"
if grep -q 'old-secret\|must-not-survive\|/old/' "$RUNTIME_ENV"; then
  echo 'stale or forbidden runtime value survived environment rebuild' >&2
  exit 1
fi
grep -qx 'type=volume,source=sub2api_unified_payment_vault,target=/run/sub2api-payment-vault,readonly' "$STATE/create-mounts"
grep -qx 'type=volume,source=sub2api_feishu_vault,target=/run/sub2api-feishu-vault,readonly' "$STATE/create-mounts"
grep -qx "type=bind,source=$TRAFFIC_FILE,target=/run/sub2api-runtime/traffic-state,readonly" "$STATE/create-mounts"

verify_output="$(run_bootstrap verify)"
[ "$verify_output" = SUB2API_FIRST_SLOT_VERIFIED ]
if run_bootstrap bootstrap >"$TEST_ROOT/repeat.log" 2>&1; then
  echo 'repeated bootstrap unexpectedly succeeded' >&2
  exit 1
fi
grep -q 'application slot already exists: sub2api-blue' "$TEST_ROOT/repeat.log"

printf 'First-slot bootstrap tests passed.\n'
