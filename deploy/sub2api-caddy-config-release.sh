#!/usr/bin/env bash

# Root-owned receiver for an audited Caddyfile release. The sender supplies a
# complete Caddyfile plus its immutable source commit and digest; this helper
# projects the template onto the currently serving blue/green slot without
# changing application lifecycle state.

set -Eeuo pipefail
umask 077

usage() {
  cat >&2 <<'EOF'
Usage: sub2api-caddy-config-release.sh COMMIT CONFIG_DIGEST < Caddyfile

COMMIT         Full 40-character lowercase Git commit.
CONFIG_DIGEST  sha256:<64 lowercase hexadecimal characters> for stdin.
EOF
  exit 2
}

log() {
  printf '[%s] %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"
}

die() {
  log "ERROR: $*" >&2
  exit 1
}

[ "$#" -eq 2 ] || usage
COMMIT="$1"
REQUESTED_DIGEST="$2"

[[ "$COMMIT" =~ ^[0-9a-f]{40}$ ]] \
  || die 'COMMIT must be a full 40-character lowercase Git commit'
[[ "$REQUESTED_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]] \
  || die 'CONFIG_DIGEST must be sha256:<64 lowercase hexadecimal characters>'

TEST_MODE="${SUB2API_CADDY_CONFIG_RELEASE_ALLOW_NON_ROOT_FOR_TESTS:-0}"
case "$TEST_MODE" in
  0|1) ;;
  *) die 'SUB2API_CADDY_CONFIG_RELEASE_ALLOW_NON_ROOT_FOR_TESTS must be 0 or 1' ;;
esac

CONFIG_FILE="${SUB2API_CADDY_CONFIG_RELEASE_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"
INPUT_PATH=""
CONTAINER_CANDIDATE_PATH=""
CONTAINER_ROLLBACK_PATH=""
TRANSACTION_CREATED=false
CADDY_RW_PID=""
BACKUP_PATH=""
CANDIDATE_PATH=""
RECORD_PATH=""
BEFORE_SHA=""
AFTER_SHA=""
BEFORE_SLOT=""
BEFORE_CONFIG_SHA=""
AFTER_CONFIG_SHA=""
COMPLETION_RECORD_CREATED=false

file_metadata() {
  stat -c '%u:%g:%a' "$1" 2>/dev/null || stat -f '%u:%g:%Lp' "$1"
}

file_identity() {
  stat -Lc '%d:%i' "$1" 2>/dev/null || stat -Lf '%d:%i' "$1"
}

file_sha() {
  sha256sum "$1" | awk '{print $1}'
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

require_absolute_path() {
  local label="$1" path="$2"

  case "$path" in
    /*) ;;
    *) die "$label must be an absolute path" ;;
  esac
  case "$path" in
    *$'\n'*|*$'\r'*|*'//'*) die "$label contains unsupported path components" ;;
  esac
  case "/${path}/" in
    */./*|*/../*) die "$label contains unsupported path components" ;;
  esac
}

require_simple_name() {
  local label="$1" value="$2"

  case "$value" in
    ''|.*|*..*|*.|-*|*[!A-Za-z0-9_.-]*) die "$label is not a safe container name" ;;
  esac
}

validate_protected_config() {
  local metadata uid gid mode

  [ -f "$CONFIG_FILE" ] && [ ! -L "$CONFIG_FILE" ] \
    || die "automatic-release configuration is missing or unsafe: ${CONFIG_FILE}"
  metadata="$(file_metadata "$CONFIG_FILE")" \
    || die "could not inspect automatic-release configuration: ${CONFIG_FILE}"
  IFS=: read -r uid gid mode <<EOF
$metadata
EOF
  [ "$mode" = 600 ] \
    || die "automatic-release configuration must be mode 0600: ${CONFIG_FILE}"
  if [ "$TEST_MODE" != 1 ]; then
    [ "$uid" = 0 ] && [ "$gid" = 0 ] \
      || die "automatic-release configuration must be root-owned: ${CONFIG_FILE}"
  fi
}

validate_root_owned_readonly_file() {
  local label="$1" path="$2" metadata uid gid mode mode_value

  [ -f "$path" ] && [ ! -L "$path" ] && [ -r "$path" ] \
    || die "$label is missing or unsafe: ${path}"
  metadata="$(file_metadata "$path")" \
    || die "could not inspect ${label}: ${path}"
  IFS=: read -r uid gid mode <<EOF
$metadata
EOF
  case "$mode" in
    ''|*[!0-7]*) die "$label has an unsupported mode: ${path}" ;;
  esac
  mode_value=$((8#$mode))
  [ $((mode_value & 0022)) -eq 0 ] \
    || die "$label must not be group/world writable: ${path}"
  if [ "$TEST_MODE" != 1 ]; then
    [ "$uid" = 0 ] && [ "$gid" = 0 ] \
      || die "$label must be root-owned: ${path}"
  fi
}

validate_root_owned_directory() {
  local label="$1" path="$2" metadata uid gid mode mode_value

  [ -d "$path" ] && [ ! -L "$path" ] \
    || die "$label is missing or unsafe: ${path}"
  metadata="$(file_metadata "$path")" \
    || die "could not inspect ${label}: ${path}"
  IFS=: read -r uid gid mode <<EOF
$metadata
EOF
  case "$mode" in
    ''|*[!0-7]*) die "$label has an unsupported mode: ${path}" ;;
  esac
  mode_value=$((8#$mode))
  [ $((mode_value & 0022)) -eq 0 ] \
    || die "$label must not be group/world writable: ${path}"
  if [ "$TEST_MODE" != 1 ]; then
    [ "$uid" = 0 ] && [ "$gid" = 0 ] \
      || die "$label must be root-owned: ${path}"
  fi
}

test_path_is_bounded() {
  local label="$1" path="$2"

  case "$path" in
    "$TEST_TMP_ROOT"/*) ;;
    *) die "$label must be inside the bounded test temporary directory" ;;
  esac
}

if [ "$TEST_MODE" = 1 ]; then
  TEST_TMP_ROOT="${TMPDIR:-/tmp}"
  TEST_TMP_ROOT="${TEST_TMP_ROOT%/}"
  [ -n "$TEST_TMP_ROOT" ] && [ "$TEST_TMP_ROOT" != / ] \
    || die 'test mode requires a bounded temporary directory'
  [ -d "$TEST_TMP_ROOT" ] \
    || die "test mode temporary directory does not exist: ${TEST_TMP_ROOT}"
  TEST_TMP_ROOT="$(cd "$TEST_TMP_ROOT" && pwd -P)"
  [ "$CONFIG_FILE" != /etc/sub2api-autodeploy.env ] \
    || die 'test mode requires an explicit non-production config file'
  test_path_is_bounded 'test configuration' "$CONFIG_FILE"
else
  [ "$(id -u)" -eq 0 ] || die 'Caddy configuration receiver must run as root'
fi

validate_protected_config
set -a
# shellcheck disable=SC1090 # Checked root-owned deployment authority.
. "$CONFIG_FILE"
set +a

APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
CADDYFILE="${SUB2API_CADDY_CONFIG_RELEASE_CADDYFILE:-${APP_DIR}/Caddyfile}"
CADDY_CONTAINER="${SUB2API_CADDY_CONTAINER:-sub2api-caddy}"
CADDY_CONFIG_PATH="${SUB2API_CADDY_CONFIG_RELEASE_CADDY_CONFIG_PATH:-${SUB2API_RUNTIME_GUARD_CADDY_CONFIG_PATH:-/etc/caddy/Caddyfile}}"
CADDY_STARTUP_HOST_PATH="${SUB2API_CADDY_CONFIG_RELEASE_STARTUP_HOST_PATH:-${SUB2API_CADDY_STARTUP_HOST_PATH:-}}"
CADDY_API_HOST="${SUB2API_CADDY_CONFIG_RELEASE_API_HOST:-api.turtleligpt.com}"
PUBLIC_HEALTH_URL="${SUB2API_PUBLIC_HEALTH_URL:-}"
PUBLIC_HEALTH_RESOLVE="${SUB2API_PUBLIC_HEALTH_RESOLVE:-}"
BACKUP_DIR="${SUB2API_CADDY_CONFIG_RELEASE_BACKUP_DIR:-${APP_DIR}/backups}"
TRANSACTION_PATH="${SUB2API_CADDY_CONFIG_RELEASE_TRANSACTION_PATH:-${APP_DIR}/.sub2api-caddy-config-release-transaction.env}"
GCP_TRANSACTION_PATH="${APP_DIR}/.gcp-tw-caddy-transaction.env"
CUSTOMER_TRANSACTION_PATH="${APP_DIR}/.cf-opt-totools-caddy.env"
BLUE_GREEN_TRANSACTION_PATH="${APP_DIR}/.sub2api-blue-green-caddy-transaction.env"
MAX_INPUT_BYTES=262144

require_absolute_path SUB2API_APP_DIR "$APP_DIR"
require_absolute_path SUB2API_CADDY_CONFIG_RELEASE_CADDYFILE "$CADDYFILE"
require_absolute_path SUB2API_CADDY_CONFIG_RELEASE_CADDY_CONFIG_PATH "$CADDY_CONFIG_PATH"
require_absolute_path SUB2API_CADDY_CONFIG_RELEASE_BACKUP_DIR "$BACKUP_DIR"
require_absolute_path SUB2API_CADDY_CONFIG_RELEASE_TRANSACTION_PATH "$TRANSACTION_PATH"
require_simple_name SUB2API_CADDY_CONTAINER "$CADDY_CONTAINER"
case "$CADDY_API_HOST" in
  ''|.*|*..*|*.|*[!A-Za-z0-9.-]*) die 'SUB2API_CADDY_CONFIG_RELEASE_API_HOST is invalid' ;;
esac
[ "$PUBLIC_HEALTH_URL" = 'https://api.turtleligpt.com/health' ] \
  || die 'SUB2API_PUBLIC_HEALTH_URL must be exactly https://api.turtleligpt.com/health'
[ "$PUBLIC_HEALTH_RESOLVE" = 'api.turtleligpt.com:443:127.0.0.1' ] \
  || die 'SUB2API_PUBLIC_HEALTH_RESOLVE must be exactly api.turtleligpt.com:443:127.0.0.1'
if [ -n "$CADDY_STARTUP_HOST_PATH" ]; then
  require_absolute_path SUB2API_CADDY_CONFIG_RELEASE_STARTUP_HOST_PATH "$CADDY_STARTUP_HOST_PATH"
fi
if [ "$TEST_MODE" != 1 ] \
  && [ "$TRANSACTION_PATH" != "${APP_DIR}/.sub2api-caddy-config-release-transaction.env" ]; then
  die 'production Caddy configuration transaction path must use the canonical application path'
fi
if [ "$TEST_MODE" = 1 ]; then
  test_path_is_bounded SUB2API_APP_DIR "$APP_DIR"
  test_path_is_bounded SUB2API_CADDY_CONFIG_RELEASE_CADDYFILE "$CADDYFILE"
  test_path_is_bounded SUB2API_CADDY_CONFIG_RELEASE_BACKUP_DIR "$BACKUP_DIR"
  test_path_is_bounded SUB2API_CADDY_CONFIG_RELEASE_TRANSACTION_PATH "$TRANSACTION_PATH"
  [ -z "$CADDY_STARTUP_HOST_PATH" ] \
    || test_path_is_bounded SUB2API_CADDY_CONFIG_RELEASE_STARTUP_HOST_PATH "$CADDY_STARTUP_HOST_PATH"
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAINTENANCE_LOCK_HELPER="${SCRIPT_DIR}/sub2api-maintenance-lock.sh"
[ -f "$MAINTENANCE_LOCK_HELPER" ] && [ ! -L "$MAINTENANCE_LOCK_HELPER" ] \
  || die "maintenance lock helper is missing or unsafe: ${MAINTENANCE_LOCK_HELPER}"
if [ "$TEST_MODE" != 1 ]; then
  [ "$(file_metadata "$MAINTENANCE_LOCK_HELPER")" = '0:0:750' ] \
    || die "maintenance lock helper must be root-owned mode 0750: ${MAINTENANCE_LOCK_HELPER}"
fi
# shellcheck disable=SC1090,SC1091 # Installed alongside this root-owned executable.
. "$MAINTENANCE_LOCK_HELPER"
if [ "$TEST_MODE" = 1 ]; then
  # shellcheck disable=SC2034 # Read by the sourced maintenance lock helper.
  SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1
fi
MAINTENANCE_LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE}"
if ! sub2api_maintenance_lock_validate_configured_path "$MAINTENANCE_LOCK_FILE"; then
  die "unsafe maintenance lock: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
fi
if [ "$TEST_MODE" = 1 ]; then
  test_path_is_bounded SUB2API_MAINTENANCE_LOCK_FILE "$MAINTENANCE_LOCK_FILE"
fi

for command_name in awk chmod cp curl date docker flock id mkdir mktemp mv nsenter python3 rm sha256sum stat; do
  require_cmd "$command_name"
done

remount_caddy_startup_read_only() {
  local container_pid="${CADDY_RW_PID:-}"

  [ -n "$container_pid" ] || return 0
  if caddy_namespace_remount "$container_pid" ro >/dev/null 2>&1; then
    CADDY_RW_PID=""
    return 0
  fi
  return 1
}

caddy_namespace_remount() {
  local container_pid="$1" access_mode="$2"

  case "$CADDY_CONFIG_PATH" in
    /*) ;;
    *) return 1 ;;
  esac
  case "$access_mode" in
    rw|ro) ;;
    *) return 1 ;;
  esac
  nsenter -t "$container_pid" -m -- \
    /bin/mount -n -o "remount,${access_mode},bind" "$CADDY_CONFIG_PATH" "$CADDY_CONFIG_PATH"
}

write_file_preserving_inode() {
  python3 - "$1" "$2" <<'PY'
import os
import sys

source_path, destination_path = sys.argv[1:]
with open(source_path, "rb") as source:
    intended = source.read()
with open(destination_path, "rb") as destination:
    original = destination.read()

def write_all(payload):
    descriptor = os.open(destination_path, os.O_WRONLY | os.O_TRUNC | os.O_CLOEXEC)
    try:
        view = memoryview(payload)
        written = 0
        while written < len(view):
            written += os.write(descriptor, view[written:])
        os.fsync(descriptor)
    finally:
        os.close(descriptor)

try:
    write_all(intended)
except BaseException:
    write_all(original)
    raise
PY
}

sync_caddy_startup_file() {
  local container_pid target_path

  container_pid="$(docker inspect "$CADDY_CONTAINER" --format '{{.State.Pid}}')" || return 1
  case "$container_pid" in
    ''|*[!0-9]*|0|1) return 1 ;;
  esac
  if [ -n "$CADDY_STARTUP_HOST_PATH" ]; then
    target_path="$CADDY_STARTUP_HOST_PATH"
  else
    target_path="/proc/${container_pid}/root${CADDY_CONFIG_PATH}"
  fi
  [ -f "$target_path" ] && [ ! -L "$target_path" ] || return 1

  caddy_namespace_remount "$container_pid" rw || return 1
  CADDY_RW_PID="$container_pid"
  if ! write_file_preserving_inode "$CADDYFILE" "$target_path"; then
    caddy_namespace_remount "$container_pid" ro >/dev/null 2>&1 || true
    return 1
  fi
  caddy_namespace_remount "$container_pid" ro || return 1
  CADDY_RW_PID=""
}

adapt_host_config() {
  docker exec -i "$CADDY_CONTAINER" caddy adapt --config /dev/stdin --adapter caddyfile <"$CADDYFILE"
}

adapt_startup_config() {
  docker exec \
    -e "SUB2API_CADDY_CONFIG_RELEASE_PATH=${CADDY_CONFIG_PATH}" \
    "$CADDY_CONTAINER" sh -ceu \
    'caddy adapt --config "$SUB2API_CADDY_CONFIG_RELEASE_PATH" --adapter caddyfile'
}

read_active_config() {
  docker exec "$CADDY_CONTAINER" sh -ceu \
    'wget -Y off -qO- http://127.0.0.1:2019/config/ 2>/dev/null || curl --noproxy "*" -fsS http://127.0.0.1:2019/config/'
}

startup_file_sha() {
  docker exec "$CADDY_CONTAINER" sha256sum "$CADDY_CONFIG_PATH" | awk '{print $1}'
}

caddy_json_slot() {
  printf '%s' "$1" | python3 -c '
import json
import sys

config = json.load(sys.stdin)
if type(config) is not dict:
    raise SystemExit(1)
dials = []
def walk(value):
    if type(value) is dict:
        for key, child in value.items():
            if key == "dial":
                if type(child) is not str:
                    raise SystemExit(1)
                dials.append(child)
            walk(child)
    elif type(value) is list:
        for child in value:
            walk(child)
walk(config)
if set(dials) == {"sub2api-blue:8080"}:
    print("blue")
elif set(dials) == {"sub2api-green:8080"}:
    print("green")
else:
    raise SystemExit(1)
'
}

verify_caddy_json_body_contract() {
  printf '%s' "$1" | python3 -c '
import json
import re
import sys

expected_host = sys.argv[1]
expected_bytes = int(sys.argv[2])
config = json.load(sys.stdin)
if type(config) is not dict:
    raise SystemExit(1)
request_sizes = []
expressions = []
hosts = []
def walk(value):
    if type(value) is dict:
        if value.get("handler") == "request_body":
            request_sizes.append(value.get("max_size"))
        for key, child in value.items():
            if key == "host":
                if type(child) is not list or not all(type(item) is str for item in child):
                    raise SystemExit(1)
                hosts.extend(child)
            if type(child) is str and "Content-Length" in child:
                expressions.append(child)
            walk(child)
    elif type(value) is list:
        for child in value:
            walk(child)
walk(config)
if expected_host not in hosts or len(request_sizes) != 4:
    raise SystemExit(1)
if any(type(size) is not int or type(size) is bool or size != expected_bytes for size in request_sizes):
    raise SystemExit(1)
if len(expressions) != 4:
    raise SystemExit(1)
for expression in expressions:
    values = re.findall(r"(?<![<>=])>\s*([0-9]+)", expression)
    if values != [str(expected_bytes)]:
        raise SystemExit(1)
' "$CADDY_API_HOST" 134217728
}

caddy_json_sha() {
  printf '%s' "$1" | python3 -c '
import hashlib
import json
import sys

config = json.load(sys.stdin)
if type(config) is not dict:
    raise SystemExit(1)
encoded = json.dumps(config, separators=(",", ":"), sort_keys=True).encode("utf-8")
print(hashlib.sha256(encoded).hexdigest())
'
}

verify_source_contract() {
  local source_path="$1" expected_slot="$2" template_mode="$3"

  python3 - "$source_path" "$expected_slot" "$template_mode" <<'PY'
import re
import sys

path, expected_slot, template_mode = sys.argv[1:]
data = open(path, "rb").read()
try:
    text = data.decode("utf-8")
except UnicodeDecodeError:
    raise SystemExit(1)

if "\x00" in text:
    raise SystemExit(1)
if re.search(r"(?i)\b(?:16|100)\s*(?:mb|mib)\b|\b100000000\b", text):
    raise SystemExit(1)

upstreams = re.findall(r"\bsub2api(?:-(?:blue|green))?:8080\b", text)
if template_mode == "template":
    if not upstreams or set(upstreams) != {"sub2api-blue:8080"} or "sub2api-green" in text:
        raise SystemExit(1)
else:
    expected = "sub2api-{}:8080".format(expected_slot)
    if not upstreams or set(upstreams) != {expected}:
        raise SystemExit(1)

max_sizes = re.findall(r"(?m)^\s*max_size\s+([^\s#]+)", text)
if len(max_sizes) != 4 or any(value not in {"128MiB", "134217728"} for value in max_sizes):
    raise SystemExit(1)

content_length_lines = [line for line in text.splitlines() if "Content-Length" in line]
if len(content_length_lines) != 4:
    raise SystemExit(1)
for line in content_length_lines:
    values = re.findall(r"(?<![<>=])>\s*([0-9]+)", line)
    if values != ["134217728"]:
        raise SystemExit(1)

messages = re.findall(r"(?m)^\s*respond\s+\"[^\"\n]*超过 128MiB[^\"\n]*\"\s+413(?:\s+#.*)?$", text)
if len(messages) != 4:
    raise SystemExit(1)
PY
}

render_candidate() {
  python3 - "$INPUT_PATH" "$CANDIDATE_PATH" "$BEFORE_SLOT" <<'PY'
import sys

source_path, candidate_path, slot = sys.argv[1:]
source = open(source_path, "rb").read()
expected = b"sub2api-blue:8080"
replacement = ("sub2api-{}:8080".format(slot)).encode("ascii")
if expected not in source or b"sub2api-green" in source:
    raise SystemExit(1)
with open(candidate_path, "wb") as destination:
    destination.write(source.replace(expected, replacement))
PY
}

check_active_runtime_contract() {
  local active_container="sub2api-${BEFORE_SLOT}" active_revision

  docker inspect "$active_container" --format '{{range .Config.Env}}{{println .}}{{end}}' \
    | python3 -c '
import sys

expected = {
    "SERVER_MAX_REQUEST_BODY_SIZE": "134217728",
    "GATEWAY_MAX_BODY_SIZE": "134217728",
    "GATEWAY_OPENAI_WS_CLIENT_READ_LIMIT_BYTES": "134217728",
}
found = {key: [] for key in expected}
for line in sys.stdin.read().splitlines():
    for key in expected:
        prefix = key + "="
        if line.startswith(prefix):
            found[key].append(line[len(prefix):])
if any(values != [expected[key]] for key, values in found.items()):
    raise SystemExit(1)
' \
    || return 1
  active_revision="$(docker inspect "$active_container" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" \
    || return 1
  [ "$active_revision" = "$COMMIT" ]
}

assert_current_views() {
  local host_json startup_json active_json host_slot startup_slot active_slot host_config startup_config active_config

  host_json="$(adapt_host_config)" || die 'could not adapt host Caddyfile'
  startup_json="$(adapt_startup_config)" || die 'could not adapt Caddy startup configuration'
  active_json="$(read_active_config)" || die 'could not read active Caddy Admin configuration'
  host_slot="$(caddy_json_slot "$host_json")" || die 'host Caddyfile does not uniquely select blue or green'
  startup_slot="$(caddy_json_slot "$startup_json")" || die 'startup Caddyfile does not uniquely select blue or green'
  active_slot="$(caddy_json_slot "$active_json")" || die 'active Caddy configuration does not uniquely select blue or green'
  [ "$host_slot" = "$startup_slot" ] && [ "$host_slot" = "$active_slot" ] \
    || die 'host, startup, and active Caddy views do not select the same slot'
  host_config="$(caddy_json_sha "$host_json")" || die 'host Caddy JSON is invalid'
  startup_config="$(caddy_json_sha "$startup_json")" || die 'startup Caddy JSON is invalid'
  active_config="$(caddy_json_sha "$active_json")" || die 'active Caddy JSON is invalid'
  [ "$host_config" = "$startup_config" ] && [ "$host_config" = "$active_config" ] \
    || die 'host, startup, and active Caddy configuration SHA-256 values diverge'
  BEFORE_SLOT="$host_slot"
  BEFORE_CONFIG_SHA="$host_config"
}

verify_converged_views() {
  local expected_sha="$1" expected_slot="$2" expected_config="$3" require_body_contract="$4"
  local host_json startup_json active_json host_config startup_config active_config

  [ "$(file_sha "$CADDYFILE")" = "$expected_sha" ] || return 1
  [ "$(startup_file_sha)" = "$expected_sha" ] || return 1
  host_json="$(adapt_host_config)" || return 1
  startup_json="$(adapt_startup_config)" || return 1
  active_json="$(read_active_config)" || return 1
  [ "$(caddy_json_slot "$host_json")" = "$expected_slot" ] || return 1
  [ "$(caddy_json_slot "$startup_json")" = "$expected_slot" ] || return 1
  [ "$(caddy_json_slot "$active_json")" = "$expected_slot" ] || return 1
  if [ "$require_body_contract" = true ]; then
    verify_caddy_json_body_contract "$host_json" \
      && verify_caddy_json_body_contract "$startup_json" \
      && verify_caddy_json_body_contract "$active_json" || return 1
  fi
  host_config="$(caddy_json_sha "$host_json")" || return 1
  startup_config="$(caddy_json_sha "$startup_json")" || return 1
  active_config="$(caddy_json_sha "$active_json")" || return 1
  [ "$host_config" = "$expected_config" ] \
    && [ "$startup_config" = "$expected_config" ] \
    && [ "$active_config" = "$expected_config" ] || return 1
  curl --fail --silent --show-error --noproxy '*' --connect-timeout 5 --max-time 20 \
    --location --max-redirs 0 --resolve "$PUBLIC_HEALTH_RESOLVE" "$PUBLIC_HEALTH_URL" >/dev/null
}

write_transaction() {
  local phase="$1" temporary

  temporary="$(mktemp "${TRANSACTION_PATH}.tmp.XXXXXX")" || return 1
  {
    printf 'VERSION=1\n'
    printf 'CADDYFILE=%s\n' "$CADDYFILE"
    printf 'BACKUP_PATH=%s\n' "$BACKUP_PATH"
    printf 'CANDIDATE_PATH=%s\n' "$CANDIDATE_PATH"
    printf 'BEFORE_SHA=%s\n' "$BEFORE_SHA"
    printf 'AFTER_SHA=%s\n' "$AFTER_SHA"
    printf 'COMMIT=%s\n' "$COMMIT"
    printf 'REQUESTED_DIGEST=%s\n' "$REQUESTED_DIGEST"
    printf 'ACTIVE_SLOT=%s\n' "$BEFORE_SLOT"
    printf 'PHASE=%s\n' "$phase"
  } >"$temporary"
  chmod 0600 "$temporary" || {
    rm -f "$temporary"
    return 1
  }
  mv -f "$temporary" "$TRANSACTION_PATH" || {
    rm -f "$temporary"
    return 1
  }
  TRANSACTION_CREATED=true
}

write_completion_record() {
  local temporary

  temporary="$(mktemp "${RECORD_PATH}.tmp.XXXXXX")" || return 1
  {
    printf 'COMMIT=%s\n' "$COMMIT"
    printf 'REQUESTED_DIGEST=%s\n' "$REQUESTED_DIGEST"
    printf 'FINAL_SHA=%s\n' "$AFTER_SHA"
    printf 'ACTIVE_SLOT=%s\n' "$BEFORE_SLOT"
    printf 'BACKUP_PATH=%s\n' "$BACKUP_PATH"
    printf 'COMPLETED_AT=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  } >"$temporary"
  chmod 0600 "$temporary" || {
    rm -f "$temporary"
    return 1
  }
  mv -f "$temporary" "$RECORD_PATH" || {
    rm -f "$temporary"
    return 1
  }
  COMPLETION_RECORD_CREATED=true
}

rollback_previous_configuration() {
  local result=0

  [ -n "$BACKUP_PATH" ] && [ -f "$BACKUP_PATH" ] && [ -n "$BEFORE_SHA" ] \
    || return 1
  if ! write_file_preserving_inode "$BACKUP_PATH" "$CADDYFILE"; then
    log 'ERROR: could not restore the host Caddyfile inode from its backup' >&2
    result=1
  fi
  if ! sync_caddy_startup_file; then
    log 'ERROR: could not restore the Caddy startup bind from its backup' >&2
    result=1
  fi
  docker cp "$BACKUP_PATH" "${CADDY_CONTAINER}:${CONTAINER_ROLLBACK_PATH}" >/dev/null || result=1
  docker exec "$CADDY_CONTAINER" caddy validate --config "$CONTAINER_ROLLBACK_PATH" --adapter caddyfile >/dev/null \
    || result=1
  docker exec "$CADDY_CONTAINER" caddy reload --force --config "$CONTAINER_ROLLBACK_PATH" --adapter caddyfile >/dev/null \
    || result=1
  verify_converged_views "$BEFORE_SHA" "$BEFORE_SLOT" "$BEFORE_CONFIG_SHA" false \
    || result=1
  [ "$result" -eq 0 ]
}

cleanup() {
  local exit_status=$? rollback_status=0

  trap - EXIT
  set +e
  if ! remount_caddy_startup_read_only; then
    log 'ERROR: Caddy startup bind may still be read-write' >&2
    exit_status=1
  fi
  if [ "$TRANSACTION_CREATED" = true ] \
    && { [ -e "$TRANSACTION_PATH" ] || [ -L "$TRANSACTION_PATH" ]; }; then
    log 'release failed after transaction creation; attempting forced Caddy rollback' >&2
    if [ "$COMPLETION_RECORD_CREATED" = true ] && [ -n "$RECORD_PATH" ]; then
      if rm -f "$RECORD_PATH"; then
        COMPLETION_RECORD_CREATED=false
      else
        log "ERROR: could not remove completion record after failed release: ${RECORD_PATH}" >&2
        exit_status=1
      fi
    fi
    if rollback_previous_configuration; then
      log 'previous Caddy configuration was restored; transaction and backup were retained' >&2
    else
      rollback_status=1
      log "ERROR: rollback verification failed; transaction retained at ${TRANSACTION_PATH}" >&2
    fi
  fi
  [ -z "$INPUT_PATH" ] || rm -f "$INPUT_PATH"
  if [ -n "$CONTAINER_CANDIDATE_PATH" ]; then
    docker exec "$CADDY_CONTAINER" rm -f "$CONTAINER_CANDIDATE_PATH" >/dev/null 2>&1 || true
  fi
  if [ -n "$CONTAINER_ROLLBACK_PATH" ]; then
    docker exec "$CADDY_CONTAINER" rm -f "$CONTAINER_ROLLBACK_PATH" >/dev/null 2>&1 || true
  fi
  if [ "$exit_status" -eq 0 ] && [ "$rollback_status" -ne 0 ]; then
    exit_status=1
  fi
  exit "$exit_status"
}
trap cleanup EXIT

read_input() {
  local input_dir

  input_dir="${TMPDIR:-/tmp}"
  INPUT_PATH="$(mktemp "${input_dir%/}/sub2api-caddy-config-release.XXXXXX")" \
    || die 'could not create a private Caddyfile input file'
  if ! python3 -c '
import sys

path = sys.argv[1]
limit = int(sys.argv[2])
payload = sys.stdin.buffer.read(limit + 1)
if len(payload) > limit:
    raise SystemExit(2)
if not payload.strip() or b"\x00" in payload:
    raise SystemExit(3)
try:
    payload.decode("utf-8")
except UnicodeDecodeError:
    raise SystemExit(4)
with open(path, "wb") as destination:
    destination.write(payload)
' "$INPUT_PATH" "$MAX_INPUT_BYTES"; then
    die "Caddyfile stdin must be non-empty UTF-8 text no larger than ${MAX_INPUT_BYTES} bytes"
  fi
}

ensure_backup_directory() {
  if [ -e "$BACKUP_DIR" ] || [ -L "$BACKUP_DIR" ]; then
    [ -d "$BACKUP_DIR" ] && [ ! -L "$BACKUP_DIR" ] \
      || die "Caddy backup directory is unsafe: ${BACKUP_DIR}"
  else
    mkdir -p "$BACKUP_DIR" || die "could not create Caddy backup directory: ${BACKUP_DIR}"
  fi
  chmod 0700 "$BACKUP_DIR" || die "could not protect Caddy backup directory: ${BACKUP_DIR}"
  if [ "$TEST_MODE" != 1 ]; then
    [ "$(file_metadata "$BACKUP_DIR")" = '0:0:700' ] \
      || die "Caddy backup directory must be root-owned mode 0700: ${BACKUP_DIR}"
  fi
}

require_no_pending_transactions() {
  local path

  for path in \
    "$GCP_TRANSACTION_PATH" \
    "$CUSTOMER_TRANSACTION_PATH" \
    "$BLUE_GREEN_TRANSACTION_PATH" \
    "$TRANSACTION_PATH"; do
    [ ! -e "$path" ] && [ ! -L "$path" ] \
      || die "unfinished Caddy or release transaction exists: ${path}"
  done
}

read_input
ACTUAL_SHA="$(file_sha "$INPUT_PATH")" || die 'could not calculate Caddyfile digest'
[ "$REQUESTED_DIGEST" = "sha256:${ACTUAL_SHA}" ] \
  || die 'Caddyfile digest does not match CONFIG_DIGEST'

validate_root_owned_directory 'application directory' "$APP_DIR"
validate_root_owned_readonly_file 'host Caddyfile' "$CADDYFILE"
if ! sub2api_maintenance_lock_open "$MAINTENANCE_LOCK_FILE"; then
  die "unsafe maintenance lock: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
fi
flock -w 120 -x 8 || die 'timed out waiting for the maintenance lock'
require_no_pending_transactions
docker inspect "$CADDY_CONTAINER" >/dev/null 2>&1 \
  || die "Caddy container is missing: ${CADDY_CONTAINER}"
[ "$(docker inspect "$CADDY_CONTAINER" --format '{{.State.Running}}')" = true ] \
  || die "Caddy container is not running: ${CADDY_CONTAINER}"

HOST_INODE="$(file_identity "$CADDYFILE")" || die 'could not record host Caddyfile inode'
BEFORE_SHA="$(file_sha "$CADDYFILE")" || die 'could not calculate current host Caddyfile digest'
assert_current_views
check_active_runtime_contract \
  || die "active sub2api-${BEFORE_SLOT} does not expose the required 128 MiB HTTP/WS ingress limits and matching OCI revision"
verify_source_contract "$INPUT_PATH" "$BEFORE_SLOT" template \
  || die 'candidate template does not have the required blue-only 128 MiB Caddy contract'

ensure_backup_directory
release_stamp="$(date -u '+%Y%m%dT%H%M%SZ')-${COMMIT:0:12}"
BACKUP_PATH="$(mktemp "${BACKUP_DIR}/Caddyfile.before-caddy-config-release-${release_stamp}.XXXXXX")" \
  || die 'could not allocate a Caddy rollback backup'
CANDIDATE_PATH="$(mktemp "${BACKUP_DIR}/Caddyfile.candidate-caddy-config-release-${release_stamp}.XXXXXX")" \
  || die 'could not allocate a Caddy candidate file'
RECORD_PATH="${BACKUP_PATH}.release.env"
cp "$CADDYFILE" "$BACKUP_PATH" || die 'could not capture the Caddy rollback backup'
chmod 0600 "$BACKUP_PATH" "$CANDIDATE_PATH" || die 'could not protect Caddy release artifacts'
[ "$(file_sha "$BACKUP_PATH")" = "$BEFORE_SHA" ] \
  || die 'host Caddyfile changed while its backup was captured'
render_candidate || die 'could not project the Caddy template onto the active slot'
verify_source_contract "$CANDIDATE_PATH" "$BEFORE_SLOT" projected \
  || die 'projected Caddyfile does not have the required 128 MiB Caddy contract'
AFTER_SHA="$(file_sha "$CANDIDATE_PATH")" || die 'could not calculate projected Caddyfile digest'
[ "$(file_sha "$CADDYFILE")" = "$BEFORE_SHA" ] \
  || die 'host Caddyfile changed while the candidate was prepared'

CONTAINER_CANDIDATE_PATH="/tmp/sub2api-caddy-config-release-${COMMIT}-${$}.Caddyfile"
CONTAINER_ROLLBACK_PATH="/tmp/sub2api-caddy-config-rollback-${COMMIT}-${$}.Caddyfile"
docker cp "$CANDIDATE_PATH" "${CADDY_CONTAINER}:${CONTAINER_CANDIDATE_PATH}" >/dev/null \
  || die 'could not copy candidate Caddyfile into the Caddy container'
docker exec "$CADDY_CONTAINER" caddy validate --config "$CONTAINER_CANDIDATE_PATH" --adapter caddyfile >/dev/null \
  || die 'Caddy candidate validation failed before any live mutation'
candidate_json="$(docker exec "$CADDY_CONTAINER" caddy adapt --config "$CONTAINER_CANDIDATE_PATH" --adapter caddyfile)" \
  || die 'could not adapt candidate Caddyfile'
[ "$(caddy_json_slot "$candidate_json")" = "$BEFORE_SLOT" ] \
  || die 'candidate Caddyfile does not uniquely select the active slot'
verify_caddy_json_body_contract "$candidate_json" \
  || die 'candidate Caddy JSON does not enforce the exact 128 MiB body contract'
AFTER_CONFIG_SHA="$(caddy_json_sha "$candidate_json")" \
  || die 'could not normalize candidate Caddy JSON'

write_transaction prepared || die 'could not publish the Caddy configuration transaction'
write_file_preserving_inode "$CANDIDATE_PATH" "$CADDYFILE" \
  || die 'could not update the host Caddyfile inode'
[ "$(file_identity "$CADDYFILE")" = "$HOST_INODE" ] \
  || die 'host Caddyfile inode changed during the release'
sync_caddy_startup_file \
  || die 'could not synchronize the Caddy startup bind'
write_transaction reload-attempted || die 'could not record the Caddy reload attempt'
docker exec "$CADDY_CONTAINER" caddy reload --force --config "$CADDY_CONFIG_PATH" --adapter caddyfile >/dev/null \
  || die 'Caddy reload failed'
verify_converged_views "$AFTER_SHA" "$BEFORE_SLOT" "$AFTER_CONFIG_SHA" true \
  || die 'host, startup, and active Caddy views did not converge after reload'
write_completion_record || die 'could not record the completed Caddy configuration release'
rm -f "$TRANSACTION_PATH" || die 'could not clear the completed Caddy configuration transaction'
TRANSACTION_CREATED=false
rm -f "$CANDIDATE_PATH" || true
log "CADDY_CONFIG_RELEASED commit=${COMMIT} requested_digest=${REQUESTED_DIGEST} final_sha=${AFTER_SHA} active_slot=${BEFORE_SLOT} backup=${BACKUP_PATH} record=${RECORD_PATH}"
