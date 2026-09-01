#!/usr/bin/env bash
set -Eeuo pipefail

# Transactionally stages the Caddy listener wrapper on the Azure *candidate*
# only. It never changes a site block, DNS, firewall, Sub2API container, data
# service, or host lifecycle. Caddy is reloaded in its existing container.

readonly CADDYFILE="/opt/sub2api/Caddyfile"
readonly CADDY_CONTAINER="sub2api-candidate-caddy"
readonly CADDY_VERSION="v2.11.4"
readonly TRANSACTION_PATH="/opt/sub2api/.gcp-tw-caddy-transaction.env"
readonly CUSTOMER_HOST_TRANSACTION_PATH="/opt/sub2api/.cf-opt-totools-caddy.env"
readonly BLUE_GREEN_TRANSACTION_PATH="/opt/sub2api/.sub2api-blue-green-caddy-transaction.env"
readonly BACKUP_ROOT="/opt/sub2api/backups"

phase="${1:-}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
renderer="${AZURE_CADDY_RENDERER:-${script_dir}/render-azure-caddy-listeners.py}"
json_verifier="${AZURE_CADDY_JSON_VERIFIER:-${script_dir}/verify-azure-caddy-json.py}"
lock_helper="${SUB2API_MAINTENANCE_LOCK_HELPER:-/opt/sub2api/scripts/sub2api-maintenance-lock.sh}"

die() {
    printf 'azure-caddy-listeners: %s\n' "$*" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

file_sha() {
    sha256sum "$1" | awk '{print $1}'
}

assert_root() {
    [[ "$(id -u)" -eq 0 ]] || die "must run as root"
}

assert_regular_file() {
    local path="$1"
    [[ -f "$path" && ! -L "$path" ]] || die "expected regular non-symlink file: $path"
}

assert_root_owned_mode() {
    local path="$1"
    local metadata
    metadata="$(stat -c '%u:%g:%a' "$path")"
    case "$metadata" in
        0:0:600|0:0:640|0:0:644|0:0:750) ;;
        *) die "unsafe root-owned file metadata for $path: $metadata" ;;
    esac
}

container_config_sha() {
    docker exec "$CADDY_CONTAINER" sha256sum /etc/caddy/Caddyfile | awk '{print $1}'
}

container_config_identity() {
    docker exec "$CADDY_CONTAINER" stat -c '%d:%i' /etc/caddy/Caddyfile
}

assert_renderer() {
    assert_regular_file "$renderer"
    [[ "$(stat -c '%u:%g:%a' "$renderer")" == '0:0:750' ]] \
        || die "renderer must be root:root mode 0750: $renderer"
}

assert_json_verifier() {
    assert_regular_file "$json_verifier"
    [[ "$(stat -c '%u:%g:%a' "$json_verifier")" == '0:0:750' ]] \
        || die "Caddy JSON verifier must be root:root mode 0750: $json_verifier"
}

assert_lock_helper() {
    assert_regular_file "$lock_helper"
    [[ "$(stat -c '%u:%g:%a' "$lock_helper")" == '0:0:750' ]] \
        || die "maintenance lock helper must be root:root mode 0750: $lock_helper"
    # shellcheck disable=SC1090,SC1091 # Checked root-owned executable above.
    . "$lock_helper"
    [[ -z "${SUB2API_MAINTENANCE_LOCK_FILE:-}" ]] \
        || die "refusing a caller-selected maintenance lock path"
    sub2api_maintenance_lock_validate_configured_path "$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE" \
        || die "unsafe Sub2 maintenance lock: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
    sub2api_maintenance_lock_open "$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE" \
        || die "unsafe Sub2 maintenance lock: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
    flock -w 120 -x 8 || die "timed out waiting for the shared Sub2 maintenance lock"
}

assert_runtime_contract() {
    local mount caddy_version
    assert_regular_file "$CADDYFILE"
    assert_root_owned_mode "$CADDYFILE"
    docker inspect "$CADDY_CONTAINER" >/dev/null 2>&1 \
        || die "candidate Caddy container is missing: $CADDY_CONTAINER"
    [[ "$(docker inspect "$CADDY_CONTAINER" --format '{{.State.Running}}')" == 'true' ]] \
        || die "candidate Caddy container is not running; this script will not start it"
    if docker inspect sub2api-caddy >/dev/null 2>&1; then
        die "production Caddy container must not exist on the Azure candidate host"
    fi
    mount="$(docker inspect "$CADDY_CONTAINER" --format '{{range .Mounts}}{{if eq .Destination "/etc/caddy/Caddyfile"}}{{.Source}}|{{.Type}}|{{.RW}}{{end}}{{end}}')"
    [[ "$mount" == "$CADDYFILE|bind|false" ]] \
        || die "Caddyfile mount contract mismatch: $mount"
    [[ "$(container_config_sha)" == "$(file_sha "$CADDYFILE")" ]] \
        || die "host and container Caddyfile hashes differ"
    [[ "$(container_config_identity)" == "$(stat -c '%d:%i' "$CADDYFILE")" ]] \
        || die "host and container Caddyfile bind inodes differ; recreate Caddy before staging"
    docker exec "$CADDY_CONTAINER" sh -c \
        'command -v caddy >/dev/null && command -v mktemp >/dev/null && command -v rm >/dev/null && command -v sha256sum >/dev/null' \
        || die "candidate Caddy container is missing required runtime tools"
    caddy_version="$(docker exec "$CADDY_CONTAINER" caddy version | awk '{print $1}')"
    [[ "$caddy_version" == "$CADDY_VERSION" ]] \
        || die "expected Caddy $CADDY_VERSION, got $caddy_version"
    docker exec "$CADDY_CONTAINER" caddy list-modules 2>/dev/null \
        | grep -Fx 'caddy.listeners.proxy_protocol' >/dev/null \
        || die "Caddy $CADDY_VERSION does not expose caddy.listeners.proxy_protocol"
}

validate_and_adapt() {
    local source="$1"
    local container_path
    container_path="$(docker exec "$CADDY_CONTAINER" mktemp /tmp/Caddyfile.gcp-tw.XXXXXX)"
    docker cp "$source" "${CADDY_CONTAINER}:${container_path}" >/dev/null
    if ! docker exec "$CADDY_CONTAINER" caddy validate --config "$container_path" --adapter caddyfile \
        || ! docker exec "$CADDY_CONTAINER" caddy adapt --config "$container_path" --adapter caddyfile --pretty >/dev/null; then
        docker exec "$CADDY_CONTAINER" rm -f "$container_path" >/dev/null 2>&1 || true
        return 1
    fi
    docker exec "$CADDY_CONTAINER" rm -f "$container_path" >/dev/null
}

copy_into_live_bind() {
    local source="$1"
    # The Caddyfile is a read-only file bind mount. Write the existing host
    # inode in place so the running container continues to see the same file.
    # The transaction remains authoritative if a process is interrupted; a
    # caught write failure restores the exact bytes observed before the write.
    python3 - "$source" "$CADDYFILE" <<'PY'
import os
import sys

source_path, target_path = sys.argv[1:]
with open(source_path, "rb") as source:
    intended = source.read()
with open(target_path, "rb") as target:
    original = target.read()

def write_all(payload: bytes) -> None:
    descriptor = os.open(target_path, os.O_WRONLY | os.O_TRUNC | os.O_CLOEXEC)
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
    chown root:root "$CADDYFILE"
    chmod 0644 "$CADDYFILE"
    [[ "$(file_sha "$CADDYFILE")" == "$(file_sha "$source")" \
        && "$(container_config_sha)" == "$(file_sha "$source")" \
        && "$(container_config_identity)" == "$(stat -c '%d:%i' "$CADDYFILE")" ]]
}

reload_current() {
    docker exec "$CADDY_CONTAINER" caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile >/dev/null
}

make_backup_dir() {
    local stamp
    stamp="$(date -u +%Y%m%dT%H%M%SZ)"
    install -d -o root -g root -m 0700 "$BACKUP_ROOT"
    mktemp -d "${BACKUP_ROOT}/gcp-tw-caddy-${stamp}.XXXXXX"
}

publish_backup_copy() {
    local source="$1"
    local destination="$2"
    local expected_sha="$3"
    local temporary
    temporary="$(mktemp "$(dirname "$destination")/.${destination##*/}.XXXXXX")"
    cp -- "$source" "$temporary"
    [[ "$(file_sha "$temporary")" == "$expected_sha" ]] \
        || die "Caddyfile changed while its backup was being captured"
    chown root:root "$temporary"
    chmod 0600 "$temporary"
    mv -f -- "$temporary" "$destination"
}

state_value() {
    local key="$1"
    local value
    value="$(awk -F= -v key="$key" '
        $1 == key { count += 1; value = substr($0, length(key) + 2) }
        END {
            if (count != 1 || value == "") exit 1
            print value
        }
    ' "$TRANSACTION_PATH")" || die "transaction is missing a valid $key"
    printf '%s\n' "$value"
}

assert_safe_state_file() {
    assert_regular_file "$TRANSACTION_PATH"
    [[ "$(stat -c '%u:%g:%a' "$TRANSACTION_PATH")" == '0:0:600' ]] \
        || die "unsafe transaction file metadata: $TRANSACTION_PATH"
}

load_state() {
    assert_safe_state_file
    state_caddyfile="$(state_value CADDYFILE)"
    backup_path="$(state_value BACKUP_PATH)"
    staged_path="$(state_value STAGED_PATH)"
    before_sha="$(state_value BEFORE_SHA)"
    after_sha="$(state_value AFTER_SHA)"

    [[ "$state_caddyfile" == "$CADDYFILE" ]] \
        || die "transaction targets an unexpected Caddyfile"
    [[ "$backup_path" == "$BACKUP_ROOT"/gcp-tw-caddy-*/Caddyfile.before ]] \
        || die "transaction backup path is outside the bounded backup root"
    [[ "$staged_path" == "$BACKUP_ROOT"/gcp-tw-caddy-*/Caddyfile.after ]] \
        || die "transaction staged path is outside the bounded backup root"
    [[ "$before_sha" =~ ^[0-9a-f]{64}$ && "$after_sha" =~ ^[0-9a-f]{64}$ ]] \
        || die "transaction contains invalid SHA-256 values"
    assert_regular_file "$backup_path"
    assert_regular_file "$staged_path"
    [[ "$(file_sha "$backup_path")" == "$before_sha" ]] \
        || die "backup hash no longer matches the transaction"
    [[ "$(file_sha "$staged_path")" == "$after_sha" ]] \
        || die "staged-config hash no longer matches the transaction"
}

write_state() {
    local backup="$1"
    local staged="$2"
    local before="$3"
    local after="$4"
    local temporary
    temporary="$(mktemp "${TRANSACTION_PATH}.XXXXXX")"
    {
        printf 'CADDYFILE=%s\n' "$CADDYFILE"
        printf 'BACKUP_PATH=%s\n' "$backup"
        printf 'STAGED_PATH=%s\n' "$staged"
        printf 'BEFORE_SHA=%s\n' "$before"
        printf 'AFTER_SHA=%s\n' "$after"
    } >"$temporary"
    install -o root -g root -m 0600 "$temporary" "${temporary}.locked"
    mv -f -- "${temporary}.locked" "$TRANSACTION_PATH"
    rm -f -- "$temporary"
}

assert_staged_live_state() {
    local adapted_fingerprint active_fingerprint
    [[ "$(file_sha "$CADDYFILE")" == "$after_sha" ]] \
        || return 1
    "$renderer" verify "$CADDYFILE" || return 1
    validate_and_adapt "$CADDYFILE" || return 1
    [[ "$(container_config_sha)" == "$after_sha" ]] || return 1
    adapted_fingerprint="$(docker exec "$CADDY_CONTAINER" \
        caddy adapt --config /etc/caddy/Caddyfile --adapter caddyfile \
        | "$json_verifier")" || return 1
    active_fingerprint="$(docker exec "$CADDY_CONTAINER" sh -c \
        'wget -Y off -qO- http://127.0.0.1:2019/config/ 2>/dev/null || curl --noproxy "*" -fsS http://127.0.0.1:2019/config/' \
        | "$json_verifier")" || return 1
    [[ "$active_fingerprint" == "$adapted_fingerprint" ]]
}

restore_before() {
    validate_and_adapt "$backup_path" \
        && copy_into_live_bind "$backup_path" \
        && reload_current \
        && [[ "$(file_sha "$CADDYFILE")" == "$before_sha" ]] \
        && [[ "$(container_config_sha)" == "$before_sha" ]]
}

restore_after() {
    validate_and_adapt "$staged_path" \
        && copy_into_live_bind "$staged_path" \
        && reload_current \
        && assert_staged_live_state
}

stage() {
    local backup_dir backup_file candidate after_file before_sha_local after_sha_local
    assert_runtime_contract

    [[ ! -e "$CUSTOMER_HOST_TRANSACTION_PATH" && ! -L "$CUSTOMER_HOST_TRANSACTION_PATH" ]] \
        || die "unfinished customer Host Caddy transaction exists; commit or rollback it before staging the GCP Taiwan listener"
    [[ ! -e "$BLUE_GREEN_TRANSACTION_PATH" && ! -L "$BLUE_GREEN_TRANSACTION_PATH" ]] \
        || die "unfinished blue-green Caddy upstream transaction exists; recover it before staging the GCP Taiwan listener"

    if [[ -e "$TRANSACTION_PATH" ]]; then
        load_state
        assert_staged_live_state \
            || die "existing transaction live-state verification failed; run rollback before retrying stage"
        printf 'AZURE_CADDY_STAGED already=true config_sha=%s backup=%s\n' "$after_sha" "$backup_path"
        return
    fi

    candidate="$(mktemp "${CADDYFILE}.gcp-tw.XXXXXX")"
    trap 'rm -f -- "${candidate:-}"' EXIT
    "$renderer" render "$CADDYFILE" "$candidate"
    validate_and_adapt "$candidate" \
        || die "rendered Caddyfile failed offline validate/adapt"

    before_sha_local="$(file_sha "$CADDYFILE")"
    backup_dir="$(make_backup_dir)"
    backup_file="${backup_dir}/Caddyfile.before"
    after_file="${backup_dir}/Caddyfile.after"
    publish_backup_copy "$CADDYFILE" "$backup_file" "$before_sha_local"
    [[ "$(file_sha "$CADDYFILE")" == "$before_sha_local" ]] \
        || die "Caddyfile changed after backup; refusing to stage"
    after_sha_local="$(file_sha "$candidate")"
    publish_backup_copy "$candidate" "$after_file" "$after_sha_local"
    write_state "$backup_file" "$after_file" "$before_sha_local" "$after_sha_local"
    # The transaction file is the authority used by the shared restore and
    # verification helpers. Load it before those helpers run so a first stage
    # and an idempotent retry use the exact same validated state variables.
    load_state

    if ! copy_into_live_bind "$candidate"; then
        if restore_before; then
            rm -f -- "$TRANSACTION_PATH"
            die "candidate bind write failed; restored the exact pre-stage backup: $backup_path"
        fi
        die "candidate bind write failed; recovery state retained at $TRANSACTION_PATH (backup: $backup_path)"
    fi
    if ! reload_current || ! assert_staged_live_state; then
        if restore_before; then
            rm -f -- "$TRANSACTION_PATH"
            die "Caddy reload or post-check failed; restored the exact pre-stage backup"
        fi
        die "Caddy reload or post-check failed and automatic restoration failed"
    fi
    printf 'AZURE_CADDY_STAGED already=false config_sha=%s backup=%s\n' \
        "$after_sha_local" "$backup_file"
}

rollback() {
    local observed_sha
    assert_runtime_contract
    load_state
    observed_sha="$(file_sha "$CADDYFILE")"
    if [[ "$observed_sha" != "$before_sha" && "$observed_sha" != "$after_sha" ]]; then
        printf 'azure-caddy-listeners: warning: live SHA %s matches neither transaction endpoint; restoring BEFORE_SHA %s\n' \
            "$observed_sha" "$before_sha" >&2
    fi

    if ! restore_before; then
        if restore_after; then
            die "rollback reload failed; restored the staged policy and retained the transaction"
        fi
        die "rollback failed and could not restore the staged policy; manual intervention required"
    fi
    rm -f -- "$TRANSACTION_PATH"
    printf 'AZURE_CADDY_ROLLED_BACK restored_sha=%s backup_retained=%s\n' \
        "$before_sha" "$backup_path"
}

commit() {
    assert_runtime_contract
    load_state
    assert_staged_live_state \
        || die "staged Caddy state no longer matches its transaction"
    rm -f -- "$TRANSACTION_PATH"
    printf 'AZURE_CADDY_COMMITTED config_sha=%s backup_retained=%s\n' "$after_sha" "$backup_path"
}

status() {
    local transaction='absent' customer_transaction='absent' blue_green_transaction='absent'
    local current_sha='absent'
    assert_runtime_contract
    if [[ -e "$TRANSACTION_PATH" ]]; then
        load_state
        transaction='present'
    fi
    if [[ -e "$CUSTOMER_HOST_TRANSACTION_PATH" || -L "$CUSTOMER_HOST_TRANSACTION_PATH" ]]; then
        customer_transaction='present'
    fi
    if [[ -e "$BLUE_GREEN_TRANSACTION_PATH" || -L "$BLUE_GREEN_TRANSACTION_PATH" ]]; then
        blue_green_transaction='present'
    fi
    current_sha="$(file_sha "$CADDYFILE")"
    printf 'AZURE_CADDY_STATUS transaction=%s customer_transaction=%s blue_green_transaction=%s config_sha=%s container=%s\n' \
        "$transaction" "$customer_transaction" "$blue_green_transaction" "$current_sha" "$CADDY_CONTAINER"
}

case "$phase" in
    stage|rollback|commit|status) ;;
    *)
        printf 'usage: %s stage|rollback|commit|status\n' "$0" >&2
        exit 64
        ;;
esac

for command_name in awk chmod chown cp date docker flock grep id install mktemp mv python3 rm sha256sum stat; do
    require_command "$command_name"
done
assert_root
assert_renderer
assert_json_verifier
assert_lock_helper

case "$phase" in
    stage) stage ;;
    rollback) rollback ;;
    commit) commit ;;
    status) status ;;
esac
