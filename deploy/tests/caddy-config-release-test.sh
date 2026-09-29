#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
RECEIVER="${DEPLOY_DIR}/sub2api-caddy-config-release.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-caddy-config-release-test.XXXXXX")"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
FAKE_BIN="${TEST_ROOT}/bin"
FAKE_CONTAINER_ROOT="${TEST_ROOT}/container"
FAKE_JSON="${TEST_ROOT}/fake-caddy-json.py"
APP_DIR="${TEST_ROOT}/app"
BACKUP_DIR="${APP_DIR}/backups"
HOST_CADDY="${TEST_ROOT}/host.Caddyfile"
STARTUP_CADDY="${TEST_ROOT}/startup.Caddyfile"
ACTIVE_JSON="${TEST_ROOT}/active.json"
CONFIG_FILE="${TEST_ROOT}/autodeploy.env"
LOCK_DIR="${TEST_ROOT}/locks"
LOCK_FILE="${LOCK_DIR}/maintenance.lock"
DOCKER_LOG="${TEST_ROOT}/docker.log"
NSENTER_LOG="${TEST_ROOT}/nsenter.log"
CURL_LOG="${TEST_ROOT}/curl.log"
FLOCK_LOG="${TEST_ROOT}/flock.log"
FAIL_VALIDATE="${TEST_ROOT}/fail-validate"
FAIL_RELOAD="${TEST_ROOT}/fail-reload"
LOCK_BUSY="${TEST_ROOT}/lock-busy"
BAD_ACTIVE_ENV="${TEST_ROOT}/bad-active-env"
BAD_ACTIVE_WS_ENV="${TEST_ROOT}/bad-active-ws-env"
BAD_ACTIVE_REVISION="${TEST_ROOT}/bad-active-revision"
FAIL_TRANSACTION_CLEAR="${TEST_ROOT}/fail-transaction-clear"
TRANSACTION_PATH="${APP_DIR}/.sub2api-caddy-config-release-transaction.env"
COMMIT='0123456789abcdef0123456789abcdef01234567'

cleanup() {
  rm -rf "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local path="$1" expected="$2"

  grep -Fq -- "$expected" "$path" || {
    sed -n '1,220p' "$path" >&2 || true
    fail "expected '${expected}' in ${path}"
  }
}

assert_not_contains() {
  local path="$1" unexpected="$2"

  if grep -Fq -- "$unexpected" "$path"; then
    sed -n '1,220p' "$path" >&2 || true
    fail "did not expect '${unexpected}' in ${path}"
  fi
}

assert_equal() {
  local actual="$1" expected="$2" label="$3"

  [ "$actual" = "$expected" ] || fail "${label}: expected ${expected}, got ${actual}"
}

file_sha() {
  sha256sum "$1" | awk '{print $1}'
}

file_inode() {
  stat -c '%i' "$1" 2>/dev/null || stat -f '%i' "$1"
}

json_sha() {
  python3 - "$1" <<'PY'
import hashlib
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as source:
    config = json.load(source)
print(hashlib.sha256(json.dumps(config, sort_keys=True, separators=(",", ":")).encode("utf-8")).hexdigest())
PY
}

assert_source_body_contract() {
  python3 - "$1" <<'PY'
import re
import sys

text = open(sys.argv[1], "r", encoding="utf-8").read()
max_sizes = re.findall(r"(?m)^\s*max_size\s+([^\s#]+)", text)
thresholds = [line for line in text.splitlines() if "Content-Length" in line]
messages = re.findall(r"(?m)^\s*respond\s+\"[^\"\n]*超过 128MiB[^\"\n]*\"\s+413(?:\s+#.*)?$", text)
if len(max_sizes) != 4 or any(value not in {"128MiB", "134217728"} for value in max_sizes):
    raise SystemExit(1)
if len(thresholds) != 4:
    raise SystemExit(1)
for line in thresholds:
    if re.findall(r"(?<![<>=])>\s*([0-9]+)", line) != ["134217728"]:
        raise SystemExit(1)
if len(messages) != 4:
    raise SystemExit(1)
PY
}

assert_json_body_contract() {
  python3 - "$1" <<'PY'
import json
import re
import sys

config = json.load(open(sys.argv[1], "r", encoding="utf-8"))
sizes = []
expressions = []
def walk(value):
    if isinstance(value, dict):
        if value.get("handler") == "request_body":
            sizes.append(value.get("max_size"))
        for child in value.values():
            if isinstance(child, str) and "Content-Length" in child:
                expressions.append(child)
            walk(child)
    elif isinstance(value, list):
        for child in value:
            walk(child)
walk(config)
if sizes != [134217728] * 4 or len(expressions) != 4:
    raise SystemExit(1)
if any(re.findall(r"(?<![<>=])>\s*([0-9]+)", value) != ["134217728"] for value in expressions):
    raise SystemExit(1)
PY
}

write_caddyfile() {
  local destination="$1" slot="$2" max_size="$3" threshold="$4"

  cat >"$destination" <<EOF
{
  admin 127.0.0.1:2019
}

(sub2_proxy) {
  reverse_proxy sub2api-${slot}:8080
}

api.turtleligpt.com {
  @responses_request_body path /v1/responses /v1/responses/* /responses /responses/* /backend-api/codex/responses /backend-api/codex/responses/* /v1/images/batches
  @too_large_responses_body {
    path /v1/responses /v1/responses/* /responses /responses/* /backend-api/codex/responses /backend-api/codex/responses/* /v1/images/batches
    expression \`{http.request.header.Content-Length} != '' && int({http.request.header.Content-Length}) > ${threshold}\`
  }
  handle @too_large_responses_body {
    respond "请求体过大：本次请求超过 128MiB，服务器已提前拦截。" 413
  }
  handle @responses_request_body {
    request_body {
      max_size ${max_size}
    }
    import sub2_proxy
  }

  @too_large_default_body expression \`{http.request.header.Content-Length} != '' && int({http.request.header.Content-Length}) > ${threshold}\`
  handle @too_large_default_body {
    respond "请求体过大：本次请求超过 128MiB，服务器已提前拦截。" 413
  }
  handle {
    request_body {
      max_size ${max_size}
    }
    import sub2_proxy
  }
}

aws-test.turtleligpt.com {
  @responses_request_body path /v1/responses /v1/responses/* /responses /responses/* /backend-api/codex/responses /backend-api/codex/responses/* /v1/images/batches
  @too_large_responses_body {
    path /v1/responses /v1/responses/* /responses /responses/* /backend-api/codex/responses /backend-api/codex/responses/* /v1/images/batches
    expression \`{http.request.header.Content-Length} != '' && int({http.request.header.Content-Length}) > ${threshold}\`
  }
  handle @too_large_responses_body {
    respond "请求体过大：本次请求超过 128MiB，服务器已提前拦截。" 413
  }
  handle @responses_request_body {
    request_body {
      max_size ${max_size}
    }
    import sub2_proxy
  }

  @too_large_default_body expression \`{http.request.header.Content-Length} != '' && int({http.request.header.Content-Length}) > ${threshold}\`
  handle @too_large_default_body {
    respond "请求体过大：本次请求超过 128MiB，服务器已提前拦截。" 413
  }
  handle {
    request_body {
      max_size ${max_size}
    }
    import sub2_proxy
  }
}
EOF
  chmod 0600 "$destination"
}

make_template() {
  local max_size="$1" threshold="$2" output="$3"

  write_caddyfile "$output" blue "$max_size" "$threshold"
}

reset_fixture() {
  local slot="$1" max_size="$2" threshold="$3"

  rm -rf "$BACKUP_DIR"
  rm -f "$TRANSACTION_PATH" "$FAIL_VALIDATE" "$FAIL_RELOAD" "$LOCK_BUSY" "$BAD_ACTIVE_ENV" "$BAD_ACTIVE_WS_ENV" \
    "$BAD_ACTIVE_REVISION" "$FAIL_TRANSACTION_CLEAR"
  : >"$DOCKER_LOG"
  : >"$NSENTER_LOG"
  : >"$CURL_LOG"
  : >"$FLOCK_LOG"
  write_caddyfile "$HOST_CADDY" "$slot" "$max_size" "$threshold"
  cp "$HOST_CADDY" "$STARTUP_CADDY"
  chmod 0600 "$STARTUP_CADDY"
  "$FAKE_JSON" "$HOST_CADDY" >"$ACTIVE_JSON"
}

run_receiver() {
  local candidate="$1" digest output config_file

  digest="${2:-sha256:$(file_sha "$candidate")}"
  output="$3"
  config_file="${4:-$CONFIG_FILE}"

  env \
    PATH="${FAKE_BIN}:${PATH}" \
    TMPDIR="$TEST_ROOT" \
    FAKE_ACTIVE_JSON="$ACTIVE_JSON" \
    FAKE_BAD_ACTIVE_ENV="$BAD_ACTIVE_ENV" \
    FAKE_BAD_ACTIVE_WS_ENV="$BAD_ACTIVE_WS_ENV" \
    FAKE_BAD_ACTIVE_REVISION="$BAD_ACTIVE_REVISION" \
    FAKE_CONTAINER_ROOT="$FAKE_CONTAINER_ROOT" \
    FAKE_COMMIT="$COMMIT" \
    FAKE_CURL_LOG="$CURL_LOG" \
    FAKE_DOCKER_LOG="$DOCKER_LOG" \
    FAKE_FAIL_TRANSACTION_CLEAR="$FAIL_TRANSACTION_CLEAR" \
    FAKE_FAIL_RELOAD="$FAIL_RELOAD" \
    FAKE_FAIL_VALIDATE="$FAIL_VALIDATE" \
    FAKE_FLOCK_LOG="$FLOCK_LOG" \
    FAKE_JSON="$FAKE_JSON" \
    FAKE_LOCK_BUSY="$LOCK_BUSY" \
    FAKE_NSENTER_LOG="$NSENTER_LOG" \
    FAKE_STARTUP_CADDY="$STARTUP_CADDY" \
    FAKE_TRANSACTION_PATH="$TRANSACTION_PATH" \
    SUB2API_CADDY_CONFIG_RELEASE_ALLOW_NON_ROOT_FOR_TESTS=1 \
    SUB2API_CADDY_CONFIG_RELEASE_CONFIG_FILE="$config_file" \
    /bin/bash "$RECEIVER" "$COMMIT" "$digest" <"$candidate" >"$output" 2>&1
}

mkdir -p "$FAKE_BIN" "$FAKE_CONTAINER_ROOT" "$APP_DIR" "$LOCK_DIR"
chmod 0700 "$LOCK_DIR"

cat >"$FAKE_JSON" <<'PY'
#!/usr/bin/env python3
import json
import re
import sys

text = open(sys.argv[1], "r", encoding="utf-8").read()
upstreams = sorted(set(re.findall(r"\bsub2api(?:-(?:blue|green))?:8080\b", text)))
hosts = [
    host for host in ("api.turtleligpt.com", "aws-test.turtleligpt.com")
    if re.search(r"(?m)^" + re.escape(host) + r"\s+\{$", text)
]
sizes = []
for value in re.findall(r"(?m)^\s*max_size\s+([^\s#]+)", text):
    if value in {"128MiB", "134217728"}:
        sizes.append(134217728)
    elif value == "16MB":
        sizes.append(16000000)
    elif value == "100MB":
        sizes.append(100000000)
    else:
        sizes.append(-1)
thresholds = []
for line in text.splitlines():
    if "Content-Length" in line:
        thresholds.extend(re.findall(r"(?<![<>=])>\s*([0-9]+)", line))
if len(hosts) != 2 or len(sizes) != 4 or len(thresholds) != 4:
    raise SystemExit(2)
routes = []
for index, host in enumerate(hosts):
    for guard_index in range(2):
        guard = index * 2 + guard_index
        routes.append({
            "match": [{"host": [host]}],
            "handle": [{"handler": "subroute", "routes": [{
                "match": [{"expression": "Content-Length > " + thresholds[guard]}],
                "handle": [
                    {"handler": "request_body", "max_size": sizes[guard]},
                    {"handler": "reverse_proxy", "upstreams": [{"dial": value} for value in upstreams]},
                ],
            }]}],
        })
print(json.dumps({"apps": {"http": {"servers": {"srv0": {"routes": routes}}}}}, separators=(",", ":")))
PY
chmod +x "$FAKE_JSON"

cat >"${FAKE_BIN}/docker" <<'EOF'
#!/usr/bin/env bash
set -eu

printf '%s\n' "$*" >>"$FAKE_DOCKER_LOG"

config_arg() {
  local value=''
  while [ "$#" -gt 0 ]; do
    if [ "$1" = --config ]; then
      value="$2"
      break
    fi
    shift
  done
  [ -n "$value" ] || exit 90
  printf '%s\n' "$value"
}

container_config_path() {
  local path="$1"
  if [ "$path" = /etc/caddy/Caddyfile ]; then
    printf '%s\n' "$FAKE_STARTUP_CADDY"
  else
    printf '%s%s\n' "$FAKE_CONTAINER_ROOT" "$path"
  fi
}

case "${1:-}" in
  inspect)
    target="${2:-}"
    case "$target" in
      sub2api-caddy|sub2api-blue|sub2api-green) ;;
      *) exit 2 ;;
    esac
    if [ "${3:-}" = --format ]; then
      case "${4:-}" in
        '{{.State.Running}}') printf 'true\n' ;;
        '{{.State.Pid}}') printf '4242\n' ;;
        '{{range .Config.Env}}{{println .}}{{end}}')
          case "$target" in
            sub2api-blue|sub2api-green)
              if [ -e "$FAKE_BAD_ACTIVE_ENV" ]; then
                printf '%s\n' 'SERVER_MAX_REQUEST_BODY_SIZE=134217728' 'GATEWAY_MAX_BODY_SIZE=100000000' 'GATEWAY_OPENAI_WS_CLIENT_READ_LIMIT_BYTES=134217728'
              elif [ -e "$FAKE_BAD_ACTIVE_WS_ENV" ]; then
                printf '%s\n' 'SERVER_MAX_REQUEST_BODY_SIZE=134217728' 'GATEWAY_MAX_BODY_SIZE=134217728' 'GATEWAY_OPENAI_WS_CLIENT_READ_LIMIT_BYTES=100000000'
              else
                printf '%s\n' 'SERVER_MAX_REQUEST_BODY_SIZE=134217728' 'GATEWAY_MAX_BODY_SIZE=134217728' 'GATEWAY_OPENAI_WS_CLIENT_READ_LIMIT_BYTES=134217728'
              fi
              ;;
            *) exit 3 ;;
          esac
          ;;
        '{{index .Config.Labels "org.opencontainers.image.revision"}}')
          case "$target" in
            sub2api-blue|sub2api-green)
              if [ -e "$FAKE_BAD_ACTIVE_REVISION" ]; then
                printf '%s\n' 'ffffffffffffffffffffffffffffffffffffffff'
              else
                printf '%s\n' "$FAKE_COMMIT"
              fi
              ;;
            *) exit 3 ;;
          esac
          ;;
        *) exit 4 ;;
      esac
    fi
    ;;
  cp)
    source_path="${2:-}"
    destination="${3:-}"
    case "$destination" in sub2api-caddy:/*) ;; *) exit 5 ;; esac
    container_path="${destination#sub2api-caddy:}"
    mkdir -p "${FAKE_CONTAINER_ROOT}$(dirname "$container_path")"
    cp "$source_path" "${FAKE_CONTAINER_ROOT}${container_path}"
    ;;
  exec)
    shift
    while [ "${1:-}" = -i ] || [ "${1:-}" = -e ]; do
      if [ "$1" = -e ]; then
        shift 2
      else
        shift
      fi
    done
    [ "${1:-}" = sub2api-caddy ] || exit 6
    shift
    case "${1:-}" in
      caddy)
        action="${2:-}"
        shift 2
        config_path="$(config_arg "$@")"
        if [ "$config_path" = /dev/stdin ]; then
          temporary="$(mktemp "${FAKE_CONTAINER_ROOT}/stdin.XXXXXX")"
          cat >"$temporary"
          source_path="$temporary"
        else
          source_path="$(container_config_path "$config_path")"
        fi
        [ -f "$source_path" ] || exit 7
        case "$action" in
          validate)
            [ ! -e "$FAKE_FAIL_VALIDATE" ] || exit 41
            ;;
          adapt)
            "$FAKE_JSON" "$source_path"
            ;;
          reload)
            case " $* " in *' --force '*) ;; *) exit 42 ;; esac
            if [ -e "$FAKE_FAIL_RELOAD" ]; then
              rm -f "$FAKE_FAIL_RELOAD"
              exit 43
            fi
            "$FAKE_JSON" "$source_path" >"$FAKE_ACTIVE_JSON"
            ;;
          *) exit 8 ;;
        esac
        ;;
      sha256sum)
        [ "${2:-}" = /etc/caddy/Caddyfile ] || exit 9
        sha256sum "$FAKE_STARTUP_CADDY"
        ;;
      sh)
        args="$*"
        case "$args" in
          *'2019/config'*) cat "$FAKE_ACTIVE_JSON" ;;
          *'caddy adapt'*) "$FAKE_JSON" "$FAKE_STARTUP_CADDY" ;;
          *'/health'*) [ ! -e "${FAKE_FAIL_HEALTH:-/nonexistent}" ] ;;
          *) exit 10 ;;
        esac
        ;;
      rm)
        [ "${2:-}" = -f ] || exit 11
        rm -f "${FAKE_CONTAINER_ROOT}${3:-}"
        ;;
      *) exit 12 ;;
    esac
    ;;
  *) exit 13 ;;
esac
EOF
chmod +x "${FAKE_BIN}/docker"

cat >"${FAKE_BIN}/flock" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >>"$FAKE_FLOCK_LOG"
[ ! -e "$FAKE_LOCK_BUSY" ]
EOF
chmod +x "${FAKE_BIN}/flock"

cat >"${FAKE_BIN}/curl" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$@" >>"$FAKE_CURL_LOG"
[ ! -e "${FAKE_FAIL_PUBLIC_HEALTH:-/nonexistent}" ]
EOF
chmod +x "${FAKE_BIN}/curl"

cat >"${FAKE_BIN}/rm" <<'EOF'
#!/usr/bin/env bash
set -eu

for argument in "$@"; do
  if [ "$argument" = "$FAKE_TRANSACTION_PATH" ] && [ -e "$FAKE_FAIL_TRANSACTION_CLEAR" ]; then
    exit 1
  fi
done
exec /bin/rm "$@"
EOF
chmod +x "${FAKE_BIN}/rm"

cat >"${FAKE_BIN}/nsenter" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >>"$FAKE_NSENTER_LOG"
EOF
chmod +x "${FAKE_BIN}/nsenter"

cat >"$CONFIG_FILE" <<EOF
SUB2API_APP_DIR=${APP_DIR}
SUB2API_CADDY_CONTAINER=sub2api-caddy
SUB2API_CADDY_CONFIG_RELEASE_CADDYFILE=${HOST_CADDY}
SUB2API_CADDY_CONFIG_RELEASE_STARTUP_HOST_PATH=${STARTUP_CADDY}
SUB2API_CADDY_CONFIG_RELEASE_CADDY_CONFIG_PATH=/etc/caddy/Caddyfile
SUB2API_CADDY_CONFIG_RELEASE_BACKUP_DIR=${BACKUP_DIR}
SUB2API_PUBLIC_HEALTH_URL=https://api.turtleligpt.com/health
SUB2API_PUBLIC_HEALTH_RESOLVE=api.turtleligpt.com:443:127.0.0.1
SUB2API_MAINTENANCE_LOCK_FILE=${LOCK_FILE}
EOF
chmod 0600 "$CONFIG_FILE"

[ -x "$RECEIVER" ] || chmod +x "$RECEIVER"

GOOD_TEMPLATE="${TEST_ROOT}/good-template.Caddyfile"
make_template 128MiB 134217728 "$GOOD_TEMPLATE"

# The receiver accepts only the reviewed public-health origin pin before it
# reads stdin or touches the maintenance lock/Caddy runtime.
BAD_HEALTH_CONFIG="${TEST_ROOT}/bad-health-autodeploy.env"
awk '
  /^SUB2API_PUBLIC_HEALTH_RESOLVE=/ {
    print "SUB2API_PUBLIC_HEALTH_RESOLVE=api.turtleligpt.com:443:127.0.0.2"
    next
  }
  { print }
' "$CONFIG_FILE" >"$BAD_HEALTH_CONFIG"
chmod 0600 "$BAD_HEALTH_CONFIG"
reset_fixture blue 100MB 100000000
before_sha="$(file_sha "$HOST_CADDY")"
if run_receiver "$GOOD_TEMPLATE" "sha256:$(file_sha "$GOOD_TEMPLATE")" "${TEST_ROOT}/bad-health-config.log" "$BAD_HEALTH_CONFIG"; then
  fail 'receiver accepted an inexact public-health resolve pin'
fi
assert_contains "${TEST_ROOT}/bad-health-config.log" 'SUB2API_PUBLIC_HEALTH_RESOLVE must be exactly api.turtleligpt.com:443:127.0.0.1'
assert_equal "$(file_sha "$HOST_CADDY")" "$before_sha" 'health configuration failure host hash'
assert_not_contains "$DOCKER_LOG" 'inspect'

# A bad digest is rejected before the script opens the maintenance lock or
# inspects Caddy, so it cannot mutate the host or startup configuration.
reset_fixture blue 100MB 100000000
before_sha="$(file_sha "$HOST_CADDY")"
before_inode="$(file_inode "$HOST_CADDY")"
if run_receiver "$GOOD_TEMPLATE" "sha256:$(printf '%064d' 0)" "${TEST_ROOT}/bad-digest.log"; then
  fail 'receiver accepted a mismatched digest'
fi
assert_contains "${TEST_ROOT}/bad-digest.log" 'Caddyfile digest does not match CONFIG_DIGEST'
assert_equal "$(file_sha "$HOST_CADDY")" "$before_sha" 'digest failure host hash'
assert_equal "$(file_inode "$HOST_CADDY")" "$before_inode" 'digest failure host inode'
cmp -s "$HOST_CADDY" "$STARTUP_CADDY" || fail 'digest failure changed startup Caddyfile'
assert_not_contains "$DOCKER_LOG" 'caddy reload'

# A held global FD-8 lock is rejected before any Caddy mutation.
reset_fixture blue 100MB 100000000
before_sha="$(file_sha "$HOST_CADDY")"
touch "$LOCK_BUSY"
if run_receiver "$GOOD_TEMPLATE" "sha256:$(file_sha "$GOOD_TEMPLATE")" "${TEST_ROOT}/busy-lock.log"; then
  fail 'receiver accepted a busy maintenance lock'
fi
assert_contains "${TEST_ROOT}/busy-lock.log" 'timed out waiting for the maintenance lock'
assert_contains "$FLOCK_LOG" '-w 120 -x 8'
assert_equal "$(file_sha "$HOST_CADDY")" "$before_sha" 'busy lock host hash'
cmp -s "$HOST_CADDY" "$STARTUP_CADDY" || fail 'busy lock changed startup Caddyfile'

# The active Admin API may not carry an unrelated configuration delta while
# still selecting the same upstream/body-limit fields.
reset_fixture blue 100MB 100000000
before_sha="$(file_sha "$HOST_CADDY")"
python3 - "$ACTIVE_JSON" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, "r", encoding="utf-8") as source:
    config = json.load(source)
config["apps"]["http"]["servers"]["srv0"]["unrelated"] = {"must_not": "converge"}
with open(path, "w", encoding="utf-8") as destination:
    json.dump(config, destination, separators=(",", ":"))
PY
if run_receiver "$GOOD_TEMPLATE" "sha256:$(file_sha "$GOOD_TEMPLATE")" "${TEST_ROOT}/divergent-active-json.log"; then
  fail 'receiver accepted a divergent full active Caddy JSON configuration'
fi
assert_contains "${TEST_ROOT}/divergent-active-json.log" 'configuration SHA-256 values diverge'
assert_equal "$(file_sha "$HOST_CADDY")" "$before_sha" 'divergent active JSON host hash'
[ ! -e "$TRANSACTION_PATH" ] && [ ! -L "$TRANSACTION_PATH" ] \
  || fail 'divergent active JSON created a transaction'

# The active green slot must be projected into the blue-only template, and a
# successful release must preserve the host bind inode while all views converge.
reset_fixture green 100MB 100000000
before_inode="$(file_inode "$HOST_CADDY")"
if ! run_receiver "$GOOD_TEMPLATE" "sha256:$(file_sha "$GOOD_TEMPLATE")" "${TEST_ROOT}/green-success.log"; then
  sed -n '1,260p' "${TEST_ROOT}/green-success.log" >&2 || true
  fail 'receiver did not complete the green-slot release'
fi
assert_equal "$(file_inode "$HOST_CADDY")" "$before_inode" 'successful release host inode'
cmp -s "$HOST_CADDY" "$STARTUP_CADDY" || fail 'successful release did not converge host/startup files'
assert_contains "$HOST_CADDY" 'sub2api-green:8080'
assert_not_contains "$HOST_CADDY" 'sub2api-blue:8080'
assert_contains "$HOST_CADDY" 'max_size 128MiB'
assert_contains "$HOST_CADDY" '> 134217728`'
assert_not_contains "$HOST_CADDY" '100MB'
assert_not_contains "$HOST_CADDY" '100000000'
assert_contains "$ACTIVE_JSON" 'sub2api-green:8080'
assert_contains "$ACTIVE_JSON" '134217728'
assert_source_body_contract "$HOST_CADDY"
assert_source_body_contract "$STARTUP_CADDY"
assert_json_body_contract "$ACTIVE_JSON"
"$FAKE_JSON" "$HOST_CADDY" >"${TEST_ROOT}/host-after.json"
"$FAKE_JSON" "$STARTUP_CADDY" >"${TEST_ROOT}/startup-after.json"
assert_equal "$(json_sha "${TEST_ROOT}/host-after.json")" "$(json_sha "${TEST_ROOT}/startup-after.json")" 'successful release host/startup normalized JSON'
assert_equal "$(json_sha "${TEST_ROOT}/host-after.json")" "$(json_sha "$ACTIVE_JSON")" 'successful release host/active normalized JSON'
[ ! -e "$TRANSACTION_PATH" ] && [ ! -L "$TRANSACTION_PATH" ] \
  || fail 'successful release retained its transaction'
assert_contains "${TEST_ROOT}/green-success.log" 'CADDY_CONFIG_RELEASED'
backup_path="$(sed -n 's/.* backup=\([^ ]*\).*/\1/p' "${TEST_ROOT}/green-success.log")"
record_path="$(sed -n 's/.* record=\([^ ]*\).*/\1/p' "${TEST_ROOT}/green-success.log")"
[ -f "$backup_path" ] || fail 'successful release did not retain its rollback backup'
[ -f "$record_path" ] || fail 'successful release did not retain its completion record'
assert_contains "$record_path" "COMMIT=${COMMIT}"
assert_contains "$record_path" "FINAL_SHA=$(file_sha "$HOST_CADDY")"
assert_contains "$NSENTER_LOG" 'remount,rw,bind'
assert_contains "$NSENTER_LOG" 'remount,ro,bind'
assert_contains "$CURL_LOG" '--fail'
assert_contains "$CURL_LOG" '--silent'
assert_contains "$CURL_LOG" '--show-error'
assert_contains "$CURL_LOG" '--noproxy'
assert_contains "$CURL_LOG" '*'
assert_contains "$CURL_LOG" '--connect-timeout'
assert_contains "$CURL_LOG" '20'
assert_contains "$CURL_LOG" '--resolve'
assert_contains "$CURL_LOG" 'api.turtleligpt.com:443:127.0.0.1'
assert_contains "$CURL_LOG" 'https://api.turtleligpt.com/health'

# Candidate validation failure occurs before the transaction/write boundary.
reset_fixture blue 100MB 100000000
before_sha="$(file_sha "$HOST_CADDY")"
cp "$ACTIVE_JSON" "${TEST_ROOT}/validate-before-active.json"
touch "$FAIL_VALIDATE"
if run_receiver "$GOOD_TEMPLATE" "sha256:$(file_sha "$GOOD_TEMPLATE")" "${TEST_ROOT}/validate-failure.log"; then
  fail 'receiver accepted a Caddy validation failure'
fi
assert_contains "${TEST_ROOT}/validate-failure.log" 'Caddy candidate validation failed before any live mutation'
assert_equal "$(file_sha "$HOST_CADDY")" "$before_sha" 'validation failure host hash'
cmp -s "$HOST_CADDY" "$STARTUP_CADDY" || fail 'validation failure changed startup Caddyfile'
cmp -s "$ACTIVE_JSON" "${TEST_ROOT}/validate-before-active.json" || fail 'validation failure changed active Caddy state'
[ ! -e "$TRANSACTION_PATH" ] && [ ! -L "$TRANSACTION_PATH" ] \
  || fail 'validation failure published a transaction before mutation'

# A reload failure after both in-place writes is rolled back with a forced
# reload; the retained transaction and backup make an interruption auditable.
reset_fixture blue 100MB 100000000
before_sha="$(file_sha "$HOST_CADDY")"
cp "$ACTIVE_JSON" "${TEST_ROOT}/reload-before-active.json"
touch "$FAIL_RELOAD"
if run_receiver "$GOOD_TEMPLATE" "sha256:$(file_sha "$GOOD_TEMPLATE")" "${TEST_ROOT}/reload-failure.log"; then
  fail 'receiver accepted a Caddy reload failure'
fi
assert_contains "${TEST_ROOT}/reload-failure.log" 'previous Caddy configuration was restored'
assert_equal "$(file_sha "$HOST_CADDY")" "$before_sha" 'reload rollback host hash'
cmp -s "$HOST_CADDY" "$STARTUP_CADDY" || fail 'reload rollback did not restore startup Caddyfile'
cmp -s "$ACTIVE_JSON" "${TEST_ROOT}/reload-before-active.json" || fail 'reload rollback did not restore active Caddy state'
[ -f "$TRANSACTION_PATH" ] || fail 'reload rollback did not retain its transaction'
rollback_backup="$(sed -n 's/^BACKUP_PATH=//p' "$TRANSACTION_PATH")"
[ -f "$rollback_backup" ] || fail 'reload rollback transaction does not retain a backup'
assert_contains "$DOCKER_LOG" 'caddy reload --force --config /etc/caddy/Caddyfile --adapter caddyfile'
assert_contains "$DOCKER_LOG" 'caddy reload --force --config /tmp/sub2api-caddy-config-rollback-'

# If completion persistence succeeds but the transaction cannot be cleared,
# cleanup rolls back and must remove the record that would otherwise claim a
# successful release.
reset_fixture blue 100MB 100000000
before_sha="$(file_sha "$HOST_CADDY")"
cp "$ACTIVE_JSON" "${TEST_ROOT}/transaction-clear-before-active.json"
touch "$FAIL_TRANSACTION_CLEAR"
if run_receiver "$GOOD_TEMPLATE" "sha256:$(file_sha "$GOOD_TEMPLATE")" "${TEST_ROOT}/transaction-clear-failure.log"; then
  fail 'receiver accepted a transaction-clear failure'
fi
assert_contains "${TEST_ROOT}/transaction-clear-failure.log" 'could not clear the completed Caddy configuration transaction'
assert_contains "${TEST_ROOT}/transaction-clear-failure.log" 'previous Caddy configuration was restored'
assert_equal "$(file_sha "$HOST_CADDY")" "$before_sha" 'transaction-clear rollback host hash'
cmp -s "$HOST_CADDY" "$STARTUP_CADDY" || fail 'transaction-clear rollback did not restore startup Caddyfile'
cmp -s "$ACTIVE_JSON" "${TEST_ROOT}/transaction-clear-before-active.json" || fail 'transaction-clear rollback did not restore active Caddy state'
[ -f "$TRANSACTION_PATH" ] || fail 'transaction-clear rollback did not retain its transaction'
if find "$BACKUP_DIR" -maxdepth 1 -name '*.release.env' -print -quit | grep -q .; then
  fail 'transaction-clear rollback retained a completion record after reverting Caddy'
fi

# Both stale decimal policies are rejected by the source contract before any
# transaction or in-place write is created.
for stale_policy in 16MB:16000000 100MB:100000000; do
  max_size="${stale_policy%%:*}"
  threshold="${stale_policy#*:}"
  stale_template="${TEST_ROOT}/stale-${max_size}.Caddyfile"
  make_template "$max_size" "$threshold" "$stale_template"
  reset_fixture green 100MB 100000000
  before_sha="$(file_sha "$HOST_CADDY")"
  if run_receiver "$stale_template" "sha256:$(file_sha "$stale_template")" "${TEST_ROOT}/stale-${max_size}.log"; then
    fail "receiver accepted stale ${max_size} candidate"
  fi
  assert_contains "${TEST_ROOT}/stale-${max_size}.log" 'candidate template does not have the required blue-only 128 MiB Caddy contract'
  assert_equal "$(file_sha "$HOST_CADDY")" "$before_sha" "${max_size} rejection host hash"
  cmp -s "$HOST_CADDY" "$STARTUP_CADDY" || fail "${max_size} rejection changed startup Caddyfile"
  [ ! -e "$TRANSACTION_PATH" ] && [ ! -L "$TRANSACTION_PATH" ] \
    || fail "${max_size} rejection left a transaction"
done

# The active application HTTP limits are checked under the same lock before
# the receiver captures a backup or alters the Caddy bind inode.
reset_fixture blue 100MB 100000000
before_sha="$(file_sha "$HOST_CADDY")"
touch "$BAD_ACTIVE_ENV"
if run_receiver "$GOOD_TEMPLATE" "sha256:$(file_sha "$GOOD_TEMPLATE")" "${TEST_ROOT}/active-env.log"; then
  fail 'receiver accepted an active application with an invalid body limit'
fi
assert_contains "${TEST_ROOT}/active-env.log" 'does not expose the required 128 MiB HTTP/WS ingress limits'
assert_equal "$(file_sha "$HOST_CADDY")" "$before_sha" 'active environment gate host hash'
cmp -s "$HOST_CADDY" "$STARTUP_CADDY" || fail 'active environment gate changed startup Caddyfile'

# Client Responses WebSocket ingress is the third managed layer; a stale
# decimal 100 MB value must block Caddy activation even when both HTTP values
# already match.
reset_fixture blue 100MB 100000000
before_sha="$(file_sha "$HOST_CADDY")"
touch "$BAD_ACTIVE_WS_ENV"
if run_receiver "$GOOD_TEMPLATE" "sha256:$(file_sha "$GOOD_TEMPLATE")" "${TEST_ROOT}/active-ws-env.log"; then
  fail 'receiver accepted an active application with a stale client WebSocket ingress limit'
fi
assert_contains "${TEST_ROOT}/active-ws-env.log" 'does not expose the required 128 MiB HTTP/WS ingress limits'
assert_equal "$(file_sha "$HOST_CADDY")" "$before_sha" 'active WebSocket environment gate host hash'
cmp -s "$HOST_CADDY" "$STARTUP_CADDY" || fail 'active WebSocket environment gate changed startup Caddyfile'

# The active slot must be the exact image commit supplied by the restricted
# receiver protocol, in addition to carrying all three ingress limits.
reset_fixture green 100MB 100000000
before_sha="$(file_sha "$HOST_CADDY")"
touch "$BAD_ACTIVE_REVISION"
if run_receiver "$GOOD_TEMPLATE" "sha256:$(file_sha "$GOOD_TEMPLATE")" "${TEST_ROOT}/active-revision.log"; then
  fail 'receiver accepted an active application with a mismatched OCI revision'
fi
assert_contains "${TEST_ROOT}/active-revision.log" 'matching OCI revision'
assert_equal "$(file_sha "$HOST_CADDY")" "$before_sha" 'active revision gate host hash'
cmp -s "$HOST_CADDY" "$STARTUP_CADDY" || fail 'active revision gate changed startup Caddyfile'

printf 'caddy config release receiver tests passed\n'
