#!/usr/bin/env bash
set -Eeuo pipefail

# One-shot, fail-safe updater for the already-active GCP transport candidate.
# Artifact bodies and hashes arrive through temporary instance metadata. The
# installer validates the new HAProxy config before a seamless reload and
# restores/reloads the previous config on any update failure.

readonly EXPECTED_HOSTNAME="sub2-tw-line-candidate"
readonly METADATA_ROOT="http://metadata.google.internal/computeMetadata/v1/instance/attributes"
readonly INSTALL_ROOT="/opt/sub2-tw-line"
readonly STATUS_PATH="/var/lib/sub2-tw-line/update.status"

record_status() {
    local value="$1"
    install -d -o root -g root -m 0700 "${STATUS_PATH%/*}"
    printf '%s\n' "$value" >"${STATUS_PATH}.tmp"
    chown root:root "${STATUS_PATH}.tmp"
    chmod 0600 "${STATUS_PATH}.tmp"
    mv -f -- "${STATUS_PATH}.tmp" "$STATUS_PATH"
}

fail_update() {
    local status="$1"
    trap - ERR
    record_status "GCP_UPDATE_FAIL exit=${status}" >/dev/null 2>&1 || true
    printf 'GCP_UPDATE_FAIL exit=%s\n' "$status" >&2
    exit "$status"
}

die() {
    printf 'gcp-tw-update: %s\n' "$*" >&2
    fail_update 1
}

on_error() {
    local status=$?
    fail_update "$status"
}
trap on_error ERR

metadata() {
    local key="$1"
    curl --fail --silent --show-error --noproxy '*' --connect-timeout 2 --max-time 10 \
        -H 'Metadata-Flavor: Google' "${METADATA_ROOT}/${key}"
}

install_metadata_file() {
    local metadata_key="$1"
    local sha_key="$2"
    local destination="$3"
    local mode="$4"
    local expected_sha temporary actual_sha

    expected_sha="$(metadata "$sha_key")" || die "missing SHA metadata: $sha_key"
    [[ "$expected_sha" =~ ^[0-9a-f]{64}$ ]] || die "invalid SHA metadata: $sha_key"
    temporary="$(mktemp "${INSTALL_ROOT}/.${destination##*/}.XXXXXX")"
    if ! metadata "$metadata_key" >"$temporary"; then
        rm -f -- "$temporary"
        die "missing artifact metadata: $metadata_key"
    fi
    actual_sha="$(sha256sum "$temporary" | awk '{print $1}')"
    if [[ "$actual_sha" != "$expected_sha" ]]; then
        rm -f -- "$temporary"
        die "artifact hash mismatch: $metadata_key"
    fi
    install -o root -g root -m "$mode" "$temporary" "$destination"
    rm -f -- "$temporary"
}

[[ "$(id -u)" -eq 0 ]] || die "must run as root"
[[ "$(hostname -s)" == "$EXPECTED_HOSTNAME" ]] \
    || die "refusing host '$(hostname -s)'; expected '$EXPECTED_HOSTNAME'"
for command_name in awk curl hostname id install mktemp mv rm sha256sum; do
    command -v "$command_name" >/dev/null 2>&1 \
        || die "missing required command: $command_name"
done

install -d -o root -g root -m 0750 "$INSTALL_ROOT"
install_metadata_file sub2-tw-haproxy-config sub2-tw-haproxy-config-sha \
    "${INSTALL_ROOT}/haproxy.cfg" 0644
install_metadata_file sub2-tw-haproxy-installer sub2-tw-haproxy-installer-sha \
    "${INSTALL_ROOT}/install-gcp-haproxy.sh" 0750
install_metadata_file sub2-tw-transport-verifier sub2-tw-transport-verifier-sha \
    "${INSTALL_ROOT}/verify-transport.sh" 0750

HAPROXY_TEMPLATE="${INSTALL_ROOT}/haproxy.cfg" \
HAPROXY_POST_UPDATE_VERIFY="${INSTALL_ROOT}/verify-transport.sh" \
    "${INSTALL_ROOT}/install-gcp-haproxy.sh" update

record_status 'GCP_UPDATE_PASS'
printf 'GCP_UPDATE_PASS\n'
