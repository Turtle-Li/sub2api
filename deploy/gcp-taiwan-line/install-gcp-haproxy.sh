#!/usr/bin/env bash
set -Eeuo pipefail

# Stages only the transport configuration on the exact retained GCP VM. It
# never changes a firewall rule, DNS record, application container, database,
# Redis, or any Azure resource.

readonly EXPECTED_HOSTNAME="sub2-tw-line-candidate"
readonly EXPECTED_AZURE_IP="4.216.216.16"
readonly CONFIG_PATH="/etc/haproxy/haproxy.cfg"
readonly STATE_PATH="/etc/haproxy/.gcp-tw-line-transaction.env"
readonly BACKUP_ROOT="/etc/haproxy/backups"
readonly CA_FILE="/etc/ssl/certs/ca-certificates.crt"
readonly MUTATION_LOCK_DEFAULT_PATH="/run/sub2api-gcp-tw-line/haproxy-mutation.lock"

phase="${1:-}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
source_config="${HAPROXY_TEMPLATE:-${script_dir}/haproxy.cfg}"
post_update_verify="${HAPROXY_POST_UPDATE_VERIFY:-${script_dir}/verify-transport.sh}"
# Production never accepts an alternate lock identity. The explicit test-only
# override permits hermetic non-root fixtures below their private TMPDIR.
mutation_lock_path="${HAPROXY_MUTATION_LOCK_FILE_FOR_TESTS:-$MUTATION_LOCK_DEFAULT_PATH}"
mutation_lock_test_mode="${HAPROXY_MUTATION_LOCK_ALLOW_NON_ROOT_FOR_TESTS:-0}"

die() {
    printf 'gcp-tw-haproxy: %s\n' "$*" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

file_sha() {
    sha256sum "$1" | awk '{print $1}'
}

assert_regular_file() {
    local path="$1"
    [[ -f "$path" && ! -L "$path" ]] || die "expected regular non-symlink file: $path"
}

assert_root() {
    case "$mutation_lock_test_mode" in
        1) return ;;
        0) ;;
        *) die "HAPROXY_MUTATION_LOCK_ALLOW_NON_ROOT_FOR_TESTS must be 0 or 1" ;;
    esac
    [[ "$(id -u)" -eq 0 ]] || die "must run as root"
}

lock_metadata() {
    local path="$1" metadata

    if metadata="$(stat -c '%u:%g:%a:%h' "$path" 2>/dev/null)"; then
        printf '%s\n' "$metadata"
        return
    fi
    if metadata="$(stat -f '%u:%g:%Lp:%l' "$path" 2>/dev/null)"; then
        printf '%s\n' "$metadata"
        return
    fi
    die "could not inspect mutation-lock metadata: $path"
}

validate_mutation_lock_configuration() {
    local test_tmp_root

    case "$mutation_lock_test_mode" in
        0|1) ;;
        *) die "HAPROXY_MUTATION_LOCK_ALLOW_NON_ROOT_FOR_TESTS must be 0 or 1" ;;
    esac
    case "$mutation_lock_path" in
        /*) ;;
        *) die "mutation lock path must be absolute" ;;
    esac
    case "$mutation_lock_path" in
        *$'\n'*|*$'\r'*|*'//'*) die "mutation lock path contains an unsafe component" ;;
    esac
    case "/${mutation_lock_path}/" in
        */./*|*/../*) die "mutation lock path must not contain . or .. components" ;;
    esac
    if [[ "$mutation_lock_test_mode" != 1 ]]; then
        [[ -z "${HAPROXY_MUTATION_LOCK_FILE_FOR_TESTS:-}" \
            && "$mutation_lock_path" == "$MUTATION_LOCK_DEFAULT_PATH" ]] \
            || die "mutation lock path is fixed to $MUTATION_LOCK_DEFAULT_PATH"
        return
    fi

    test_tmp_root="${TMPDIR:-/tmp}"
    [[ -d "$test_tmp_root" ]] || die "test mutation-lock root does not exist: $test_tmp_root"
    test_tmp_root="$(cd "$test_tmp_root" && pwd -P)"
    case "$mutation_lock_path" in
        "$test_tmp_root"/*) ;;
        *) die "test mutation-lock path must be inside TMPDIR" ;;
    esac
}

assert_mutation_lock_parent_container() {
    local path="$1" expected_uid="$2" metadata uid gid mode mode_value

    [[ -d "$path" && ! -L "$path" ]] \
        || die "mutation-lock parent container is not a regular directory: $path"
    metadata="$(lock_metadata "$path")"
    IFS=: read -r uid gid mode _ <<<"$metadata"
    [[ "$uid" == "$expected_uid" ]] \
        || die "mutation-lock parent container has an unexpected owner: $path"
    case "$mode" in ''|*[!0-7]*) die "mutation-lock parent container has an invalid mode: $path" ;; esac
    mode_value=$((8#$mode))
    [[ $((mode_value & 0022)) -eq 0 ]] \
        || die "mutation-lock parent container is group/world-writable: $path"
}

assert_mutation_lock_private_parent() {
    local path="$1" expected_uid="$2" expected_gid="$3" metadata uid gid mode

    [[ -d "$path" && ! -L "$path" ]] \
        || die "mutation-lock parent is not a regular directory: $path"
    metadata="$(lock_metadata "$path")"
    IFS=: read -r uid gid mode _ <<<"$metadata"
    [[ "$uid" == "$expected_uid" && "$gid" == "$expected_gid" && "$mode" == 700 ]] \
        || die "mutation-lock parent must be owned privately with mode 0700: $path"
}

assert_mutation_lock_file() {
    local path="$1" expected_uid="$2" expected_gid="$3" metadata uid gid mode links

    [[ -f "$path" && ! -L "$path" ]] \
        || die "mutation-lock path is not a regular non-symlink file: $path"
    metadata="$(lock_metadata "$path")"
    IFS=: read -r uid gid mode links <<<"$metadata"
    [[ "$uid" == "$expected_uid" && "$gid" == "$expected_gid" \
        && "$mode" == 600 && "$links" == 1 ]] \
        || die "mutation-lock file must be privately owned mode 0600 with one link: $path"
}

acquire_mutation_lock() {
    local expected_uid expected_gid lock_parent lock_parent_container previous_umask

    validate_mutation_lock_configuration
    expected_uid="$(id -u)"
    expected_gid="$(id -g)"
    lock_parent="${mutation_lock_path%/*}"
    lock_parent_container="${lock_parent%/*}"
    [[ -n "$lock_parent" && -n "$lock_parent_container" ]] \
        || die "mutation lock path has no private parent"
    assert_mutation_lock_parent_container "$lock_parent_container" "$expected_uid"
    if [[ -e "$lock_parent" || -L "$lock_parent" ]]; then
        assert_mutation_lock_private_parent "$lock_parent" "$expected_uid" "$expected_gid"
    elif [[ "$mutation_lock_test_mode" == 1 ]]; then
        install -d -m 0700 "$lock_parent"
        assert_mutation_lock_private_parent "$lock_parent" "$expected_uid" "$expected_gid"
    else
        install -d -o root -g root -m 0700 "$lock_parent"
        assert_mutation_lock_private_parent "$lock_parent" 0 0
    fi
    if [[ -e "$mutation_lock_path" || -L "$mutation_lock_path" ]]; then
        assert_mutation_lock_file "$mutation_lock_path" "$expected_uid" "$expected_gid"
    fi

    previous_umask="$(umask)"
    umask 077
    if ! exec 9>>"$mutation_lock_path"; then
        umask "$previous_umask"
        die "could not open mutation lock: $mutation_lock_path"
    fi
    umask "$previous_umask"
    assert_mutation_lock_file "$mutation_lock_path" "$expected_uid" "$expected_gid"
    flock -n 9 || die "another GCP Taiwan HAProxy mutation is already running"
}

assert_target_host() {
    local actual_hostname
    actual_hostname="$(hostname -s)"
    [[ "$actual_hostname" == "$EXPECTED_HOSTNAME" ]] \
        || die "refusing host '$actual_hostname'; expected '$EXPECTED_HOSTNAME'"
}

assert_debian_12() {
    # /etc/os-release is an OS-owned declarative file, not user input.
    # shellcheck disable=SC1091
    . /etc/os-release
    [[ "${ID:-}" == "debian" && "${VERSION_ID:-}" == "12" ]] \
        || die "expected Debian 12 Bookworm"
}

assert_no_application_runtime() {
    local process_listing
    if command -v docker >/dev/null 2>&1 \
        && docker ps --format '{{.Names}}' 2>/dev/null | grep -q '.'; then
        die "Docker containers are present; this VM must remain transport-only"
    fi
    process_listing="$(pgrep -af '(^|/)(sub2api|postgres|redis-server)([[:space:]]|$)' || true)"
    [[ -z "$process_listing" ]] \
        || die "application/database/Redis process detected; refusing transport install"
}

assert_template() {
    local http_server https_server
    assert_regular_file "$source_config"
    grep -Fqx 'frontend ingress_http' "$source_config" \
        || die "template lacks the expected HTTP frontend"
    grep -Fqx 'frontend ingress_https' "$source_config" \
        || die "template lacks the expected HTTPS frontend"
    grep -Fq "${EXPECTED_AZURE_IP}:443" "$source_config" \
        || die "template is not pinned to the Azure HTTPS origin"
    http_server="$(awk '$1 == "server" && $2 == "azure_http" { print }' "$source_config")"
    https_server="$(awk '$1 == "server" && $2 == "azure_https" { print }' "$source_config")"
    [[ "$http_server" == *"${EXPECTED_AZURE_IP}:80"* && "$http_server" != *send-proxy-v2* ]] \
        || die "HTTP backend must remain plain TCP without PROXY protocol"
    [[ "$https_server" == *"${EXPECTED_AZURE_IP}:443"* && "$https_server" == *send-proxy-v2* ]] \
        || die "HTTPS backend must send PROXY protocol v2"
}

assert_official_debian_candidate() {
    local candidate origin_record origin_kind mirror_file mirror_metadata non_comment_lines
    candidate="$(apt-cache policy haproxy | awk '$1 == "Candidate:" { print $2 }')"
    [[ "$candidate" =~ ^2\.6\.[0-9]+([+~:.A-Za-z0-9-]*)?$ ]] \
        || die "unexpected Debian HAProxy candidate: ${candidate:-missing}"
    origin_record="$(apt-cache madison haproxy | awk -F '|' -v candidate="$candidate" '
        {
            version = $2
            source = $3
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", version)
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", source)
            if (version == candidate \
                && source ~ /^https:\/\/deb\.debian\.org\/debian[[:space:]]+bookworm(-updates)?\/main([[:space:]]|$)/) {
                print "direct"
                exit
            }
            if (version == candidate \
                && source ~ /^https:\/\/(deb\.debian\.org\/debian-security|security\.debian\.org\/debian-security)[[:space:]]+bookworm-security\/main([[:space:]]|$)/) {
                print "direct"
                exit
            }
            if (version == candidate \
                && source ~ /^mirror\+file:\/+etc\/apt\/mirrors\/debian\.list[[:space:]]+bookworm\/main([[:space:]]|$)/) {
                print "gce-mirror-file|/etc/apt/mirrors/debian.list"
                exit
            }
            if (version == candidate \
                && source ~ /^mirror\+file:\/+etc\/apt\/mirrors\/debian-security\.list[[:space:]]+bookworm-security\/main([[:space:]]|$)/) {
                print "gce-security-mirror-file|/etc/apt/mirrors/debian-security.list"
                exit
            }
        }
    ')"
    IFS='|' read -r origin_kind mirror_file <<<"$origin_record"
    case "$origin_kind" in
        direct) return ;;
        gce-mirror-file|gce-security-mirror-file) ;;
        *) die "HAProxy candidate is not from an official Debian Bookworm main repository" ;;
    esac

    # Current GCE Debian images use a deb822 mirror+file URI. Accept only its
    # exact root-owned manifest and only when every active entry is Debian's
    # official HTTPS mirror; a caller-controlled or mixed mirror file fails.
    assert_regular_file "$mirror_file"
    mirror_metadata="$(stat -c '%u:%g:%a' "$mirror_file")"
    [[ "$mirror_metadata" =~ ^0:0:(600|640|644)$ ]] \
        || die "unsafe Debian mirror manifest metadata: $mirror_metadata"
    non_comment_lines="$(awk -v kind="$origin_kind" '
        /^[[:space:]]*(#|$)/ { next }
        {
            count += 1
            if (kind == "gce-mirror-file" \
                && $0 !~ /^https:\/\/deb\.debian\.org\/debian\/?[[:space:]]*$/) bad = 1
            if (kind == "gce-security-mirror-file" \
                && $0 !~ /^https:\/\/(deb\.debian\.org\/debian-security|security\.debian\.org\/debian-security)\/?[[:space:]]*$/) bad = 1
        }
        END { if (count != 1 || bad) exit 1; print count }
    ' "$mirror_file")" \
        || die "GCE Debian mirror manifest is not pinned to the official HTTPS mirror"
    [[ "$non_comment_lines" == '1' ]] \
        || die "unexpected Debian mirror manifest entry count"
}

assert_haproxy_version() {
    local version
    version="$(haproxy -v | head -n 1)"
    [[ "$version" =~ HAProxy[[:space:]]version[[:space:]]2\.6\. ]] \
        || die "expected Debian 12 HAProxy 2.6.x, got: $version"
}

validate_config() {
    local path="$1"
    assert_regular_file "$path"
    [[ -r "$CA_FILE" && ! -L "$CA_FILE" ]] || die "missing CA bundle: $CA_FILE"
    haproxy -c -f "$path" >/dev/null
}

run_post_update_verify() {
    local metadata
    [[ -n "$post_update_verify" ]] || return 0
    if [[ ! -f "$post_update_verify" || -L "$post_update_verify" ]]; then
        printf 'gcp-tw-haproxy: invalid post-update verifier: %s\n' \
            "$post_update_verify" >&2
        return 1
    fi
    metadata="$(stat -c '%u:%g:%a' "$post_update_verify")" || return 1
    if [[ "$metadata" != '0:0:750' ]]; then
        printf 'gcp-tw-haproxy: post-update verifier must be root:root mode 0750: %s\n' \
            "$post_update_verify" >&2
        return 1
    fi
    "$post_update_verify" gcp
}

make_backup_dir() {
    local stamp
    stamp="$(date -u +%Y%m%dT%H%M%SZ)"
    install -d -o root -g root -m 0700 "$BACKUP_ROOT"
    mktemp -d "${BACKUP_ROOT}/gcp-tw-line-${stamp}.XXXXXX"
}

atomic_install() {
    local source="$1"
    local destination="$2"
    local temporary
    temporary="$(mktemp "${destination}.gcp-tw.XXXXXX")"
    install -o root -g root -m 0644 "$source" "$temporary"
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
    ' "$STATE_PATH")" || die "transaction is missing a valid $key"
    printf '%s\n' "$value"
}

state_value_optional() {
    local key="$1"
    awk -F= -v key="$key" '
        $1 == key { count += 1; value = substr($0, length(key) + 2) }
        END {
            if (count > 1) exit 1
            if (count == 1) print value
        }
    ' "$STATE_PATH" || die "transaction contains duplicate $key entries"
}

assert_safe_state_file() {
    assert_regular_file "$STATE_PATH"
    [[ "$(stat -c '%u:%g:%a' "$STATE_PATH")" == '0:0:600' ]] \
        || die "unsafe transaction file metadata: $STATE_PATH"
}

load_state() {
    assert_safe_state_file
    state_config_path="$(state_value CONFIG_PATH)"
    backup_path="$(state_value BACKUP_PATH)"
    staged_path="$(state_value STAGED_PATH)"
    before_sha="$(state_value BEFORE_SHA)"
    after_sha="$(state_value AFTER_SHA)"
    transaction_status="$(state_value_optional STATUS)"
    origin_backup_path="$(state_value_optional ORIGIN_BACKUP_PATH)"
    origin_sha="$(state_value_optional ORIGIN_SHA)"
    # The already-deployed first revision had no STATUS field; its only state
    # was a successfully staged configuration, so migrate it logically here.
    [[ -n "$transaction_status" ]] || transaction_status='STAGED'
    if [[ -z "$origin_backup_path" && -z "$origin_sha" ]]; then
        # Legacy transactions predate the immutable origin fields. Their
        # recorded pre-stage backup is the only authoritative origin anchor.
        origin_backup_path="$backup_path"
        origin_sha="$before_sha"
    elif [[ -z "$origin_backup_path" || -z "$origin_sha" ]]; then
        die "transaction contains an incomplete immutable origin anchor"
    fi

    [[ "$state_config_path" == "$CONFIG_PATH" ]] \
        || die "transaction targets an unexpected configuration path"
    [[ "$backup_path" == "$BACKUP_ROOT"/gcp-tw-line-*/haproxy.cfg.before ]] \
        || die "transaction backup path is outside the bounded backup root"
    [[ "$staged_path" == "$BACKUP_ROOT"/gcp-tw-line-*/haproxy.cfg.after ]] \
        || die "transaction staged path is outside the bounded backup root"
    [[ "$before_sha" =~ ^[0-9a-f]{64}$ && "$after_sha" =~ ^[0-9a-f]{64}$ ]] \
        || die "transaction contains invalid SHA-256 values"
    case "$transaction_status" in
        STAGED|ROLLED_BACK) ;;
        *) die "transaction contains an invalid STATUS" ;;
    esac
    assert_regular_file "$backup_path"
    assert_regular_file "$staged_path"
    [[ "$(file_sha "$backup_path")" == "$before_sha" ]] \
        || die "backup hash no longer matches the transaction"
    [[ "$(file_sha "$staged_path")" == "$after_sha" ]] \
        || die "staged-config hash no longer matches the transaction"
    [[ "$origin_backup_path" == "$BACKUP_ROOT"/gcp-tw-line-*/haproxy.cfg.before ]] \
        || die "immutable origin backup path is outside the bounded backup root"
    [[ "$origin_sha" =~ ^[0-9a-f]{64}$ ]] \
        || die "transaction contains an invalid immutable origin SHA-256"
    assert_regular_file "$origin_backup_path"
    [[ "$(file_sha "$origin_backup_path")" == "$origin_sha" ]] \
        || die "immutable origin backup hash no longer matches the transaction"
}

write_state() {
    local status="$1"
    local backup="$2"
    local staged="$3"
    local before="$4"
    local after="$5"
    local origin_backup="${6:-$2}"
    local origin_hash="${7:-$4}"
    local temporary
    temporary="$(mktemp "${STATE_PATH}.XXXXXX")"
    {
        printf 'STATUS=%s\n' "$status"
        printf 'CONFIG_PATH=%s\n' "$CONFIG_PATH"
        printf 'BACKUP_PATH=%s\n' "$backup"
        printf 'STAGED_PATH=%s\n' "$staged"
        printf 'BEFORE_SHA=%s\n' "$before"
        printf 'AFTER_SHA=%s\n' "$after"
        printf 'ORIGIN_BACKUP_PATH=%s\n' "$origin_backup"
        printf 'ORIGIN_SHA=%s\n' "$origin_hash"
    } >"$temporary"
    install -o root -g root -m 0600 "$temporary" "$temporary.locked"
    mv -f -- "$temporary.locked" "$STATE_PATH"
    rm -f -- "$temporary"
}

write_loaded_state_status() {
    write_state "$1" "$backup_path" "$staged_path" "$before_sha" "$after_sha" \
        "$origin_backup_path" "$origin_sha"
    transaction_status="$1"
}

ensure_dependencies() {
    local command_name
    for command_name in apt-cache apt-get awk date dpkg-query flock grep haproxy hostname id install \
        md5sum mktemp mv pgrep sha256sum stat systemctl; do
        if [[ "$command_name" == 'haproxy' ]] && ! command -v haproxy >/dev/null 2>&1; then
            continue
        fi
        require_command "$command_name"
    done
}

assert_package_default_config() {
    local packaged_md5 current_md5
    assert_regular_file "$CONFIG_PATH"
    packaged_md5="$(dpkg-query -W -f='${Conffiles}\n' haproxy 2>/dev/null \
        | awk -v path="$CONFIG_PATH" '$1 == path { print $2; exit }')"
    [[ "$packaged_md5" =~ ^[0-9a-f]{32}$ ]] \
        || die "could not identify the packaged HAProxy configuration checksum"
    current_md5="$(md5sum "$CONFIG_PATH" | awk '{print $1}')"
    [[ "$current_md5" == "$packaged_md5" ]] \
        || die "HAProxy is installed without a transaction and its configuration is not the packaged default"
}

restore_loaded_before() {
    validate_config "$backup_path" \
        && atomic_install "$backup_path" "$CONFIG_PATH" \
        && [[ "$(file_sha "$CONFIG_PATH")" == "$before_sha" ]]
}

begin_stage_from_current() {
    local backup_dir backup_file before_sha_local after_file after_sha_local

    validate_config "$source_config"
    backup_dir="$(make_backup_dir)"
    backup_file="${backup_dir}/haproxy.cfg.before"
    after_file="${backup_dir}/haproxy.cfg.after"
    install -o root -g root -m 0600 "$CONFIG_PATH" "$backup_file"
    install -o root -g root -m 0600 "$source_config" "$after_file"
    before_sha_local="$(file_sha "$backup_file")"
    after_sha_local="$(file_sha "$after_file")"

    # Publish recovery authority before the first live-config mutation. A
    # killed or failed install is therefore recoverable and safely retryable.
    write_state STAGED "$backup_file" "$after_file" "$before_sha_local" "$after_sha_local" \
        "${origin_backup_path:-$backup_file}" "${origin_sha:-$before_sha_local}"
    load_state
    if ! atomic_install "$staged_path" "$CONFIG_PATH" || ! validate_config "$CONFIG_PATH"; then
        if restore_loaded_before; then
            write_loaded_state_status ROLLED_BACK
            die "staged HAProxy configuration failed; restored the exact pre-stage configuration"
        fi
        die "staged HAProxy configuration failed and restoration failed; transaction retained"
    fi
    printf 'GCP_HAPROXY_STAGED already=false config_sha=%s backup=%s\n' \
        "$after_sha" "$backup_path"
}

stage() {
    local current_sha desired_sha

    assert_target_host
    assert_debian_12
    assert_no_application_runtime
    assert_template

    if [[ -e "$STATE_PATH" ]]; then
        load_state
        current_sha="$(file_sha "$CONFIG_PATH")"
        desired_sha="$(file_sha "$source_config")"
        if [[ "$transaction_status" == 'STAGED' && "$current_sha" == "$after_sha" ]]; then
            [[ "$desired_sha" == "$after_sha" ]] \
                || die "a different template requires the non-disruptive update phase"
            validate_config "$CONFIG_PATH"
            printf 'GCP_HAPROXY_STAGED already=true config_sha=%s backup=%s\n' "$after_sha" "$backup_path"
            return
        fi
        systemctl is-active --quiet haproxy \
            && die "HAProxy is active in a non-staged transaction state; use update or rollback"
        if [[ "$transaction_status" == 'STAGED' ]]; then
            [[ "$desired_sha" == "$after_sha" ]] \
                || die "interrupted stage template differs from the recorded transaction"
            restore_loaded_before \
                || die "could not restore the interrupted stage before retry"
            atomic_install "$staged_path" "$CONFIG_PATH"
            if ! validate_config "$CONFIG_PATH"; then
                restore_loaded_before || die "stage retry failed and restoration failed"
                write_loaded_state_status ROLLED_BACK
                die "stage retry failed; restored the exact pre-stage configuration"
            fi
            write_loaded_state_status STAGED
            printf 'GCP_HAPROXY_STAGED already=false retry=true config_sha=%s backup=%s\n' \
                "$after_sha" "$backup_path"
            return
        fi
        [[ "$current_sha" == "$before_sha" ]] \
            || die "rolled-back HAProxy transaction does not match its retained backup"
        begin_stage_from_current
        return
    fi

    if command -v haproxy >/dev/null 2>&1; then
        assert_haproxy_version
        assert_package_default_config
    else
        apt-get update
        assert_official_debian_candidate
        DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -y haproxy ca-certificates
    fi
    assert_haproxy_version
    assert_regular_file "$CONFIG_PATH"

    # Do not expose the VM merely because a package was installed. Activation
    # is an explicit separate phase after the human has staged its GCP firewall.
    systemctl disable --now haproxy
    begin_stage_from_current
}

activate() {
    assert_target_host
    assert_debian_12
    assert_no_application_runtime
    load_state
    [[ "$transaction_status" == 'STAGED' ]] \
        || die "HAProxy transaction is rolled back; run stage before activate"
    [[ "$(file_sha "$CONFIG_PATH")" == "$after_sha" ]] \
        || die "current HAProxy configuration differs from the staged transaction"
    validate_config "$CONFIG_PATH"

    systemctl enable --now haproxy
    systemctl is-active --quiet haproxy \
        || die "HAProxy did not become active; run rollback after inspecting journalctl -u haproxy"
    printf 'GCP_HAPROXY_ACTIVE config_sha=%s\n' "$after_sha"
}

update() {
    local backup_dir backup_file before_sha_local after_file after_sha_local current_sha
    assert_target_host
    assert_debian_12
    assert_no_application_runtime
    assert_template
    load_state
    current_sha="$(file_sha "$CONFIG_PATH")"
    case "$transaction_status" in
        STAGED) [[ "$current_sha" == "$after_sha" ]] \
            || die "active HAProxy config differs from the staged transaction" ;;
        ROLLED_BACK) [[ "$current_sha" == "$before_sha" ]] \
            || die "active HAProxy config differs from the rolled-back transaction" ;;
    esac
    systemctl is-active --quiet haproxy || die "HAProxy must be active for a non-disruptive update"
    validate_config "$CONFIG_PATH"
    validate_config "$source_config"
    if [[ "$(file_sha "$source_config")" == "$current_sha" ]]; then
        run_post_update_verify \
            || die "unchanged HAProxy configuration failed runtime verification"
        printf 'GCP_HAPROXY_UPDATED already=true config_sha=%s\n' "$current_sha"
        return
    fi

    backup_dir="$(make_backup_dir)"
    backup_file="${backup_dir}/haproxy.cfg.before"
    after_file="${backup_dir}/haproxy.cfg.after"
    install -o root -g root -m 0600 "$CONFIG_PATH" "$backup_file"
    install -o root -g root -m 0600 "$source_config" "$after_file"
    before_sha_local="$(file_sha "$backup_file")"
    after_sha_local="$(file_sha "$after_file")"
    write_state STAGED "$backup_file" "$after_file" "$before_sha_local" "$after_sha_local" \
        "$origin_backup_path" "$origin_sha"
    load_state

    if ! atomic_install "$staged_path" "$CONFIG_PATH" \
        || ! validate_config "$CONFIG_PATH" \
        || ! systemctl reload haproxy \
        || ! systemctl is-active --quiet haproxy \
        || ! run_post_update_verify; then
        if restore_loaded_before && systemctl reload haproxy && systemctl is-active --quiet haproxy; then
            write_loaded_state_status ROLLED_BACK
            die "HAProxy update or runtime verification failed; reloaded the exact pre-update configuration"
        fi
        die "HAProxy update/verification and automatic restoration failed; transaction retained"
    fi
    printf 'GCP_HAPROXY_UPDATED already=false config_sha=%s backup=%s\n' \
        "$after_sha" "$backup_path"
}

rollback() {
    assert_target_host
    load_state

    # Removing the L4 listener is the fastest safe GCP-side rollback. Do not
    # start any replacement service or touch the Azure application stack.
    systemctl disable --now haproxy
    restore_loaded_before || die "HAProxy was stopped, but rollback restoration failed"
    write_loaded_state_status ROLLED_BACK
    printf 'GCP_HAPROXY_ROLLED_BACK restored_sha=%s backup_retained=%s\n' \
        "$before_sha" "$backup_path"
}

status() {
    local active enabled state='absent' current_sha='absent' origin='absent'
    active="$(systemctl is-active haproxy 2>/dev/null || true)"
    enabled="$(systemctl is-enabled haproxy 2>/dev/null || true)"
    if [[ -e "$STATE_PATH" ]]; then
        load_state
        state="$transaction_status"
        origin="$origin_sha"
    fi
    if [[ -f "$CONFIG_PATH" && ! -L "$CONFIG_PATH" ]]; then
        current_sha="$(file_sha "$CONFIG_PATH")"
    fi
    printf 'GCP_HAPROXY_STATUS transaction=%s active=%s enabled=%s config_sha=%s origin_sha=%s\n' \
        "$state" "${active:-unknown}" "${enabled:-unknown}" "$current_sha" "$origin"
}

case "$phase" in
    stage|activate|update|rollback|status) ;;
    *)
        printf 'usage: %s stage|activate|update|rollback|status\n' "$0" >&2
        exit 64
        ;;
esac

ensure_dependencies
assert_root

# Stage/update/rollback all alter the same config, transaction, and HAProxy
# lifecycle. Activate is fenced too, so it cannot race a retained transaction.
case "$phase" in
    stage|activate|update|rollback) acquire_mutation_lock ;;
esac

case "$phase" in
    stage) stage ;;
    activate) activate ;;
    update) update ;;
    rollback) rollback ;;
    status) status ;;
esac
