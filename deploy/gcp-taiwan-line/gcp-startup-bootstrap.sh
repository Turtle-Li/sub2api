#!/usr/bin/env bash
set -Eeuo pipefail

# One-shot GCE startup bootstrap for the exact retained Taiwan ingress VM.
# The operator supplies the three non-secret artifacts and their SHA-256 values
# as instance metadata, starts the VM without a public ingress tag, waits for
# GCP_BOOTSTRAP_PASS in the serial log, then removes the bootstrap metadata.
# This script never creates firewall rules, changes DNS, or installs Sub2API.

readonly EXPECTED_HOSTNAME="sub2-tw-line-candidate"
readonly METADATA_ROOT="http://metadata.google.internal/computeMetadata/v1/instance/attributes"
readonly INSTALL_ROOT="/opt/sub2-tw-line"
readonly STATUS_PATH="/var/lib/sub2-tw-line/bootstrap.status"
bootstrap_activation_attempted=false

die() {
    printf 'gcp-tw-bootstrap: %s\n' "$*" >&2
    fail_bootstrap 1
}

metadata() {
    local key="$1"
    curl --fail --silent --show-error --connect-timeout 2 --max-time 10 \
        -H 'Metadata-Flavor: Google' "${METADATA_ROOT}/${key}"
}

install_metadata_file() {
    local metadata_key="$1"
    local sha_key="$2"
    local destination="$3"
    local mode="$4"
    local expected_sha temporary actual_sha

    expected_sha="$(metadata "$sha_key")" \
        || die "missing SHA metadata: $sha_key"
    [[ "$expected_sha" =~ ^[0-9a-f]{64}$ ]] \
        || die "invalid SHA metadata: $sha_key"
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

record_status() {
    local value="$1"
    install -d -o root -g root -m 0700 "${STATUS_PATH%/*}"
    printf '%s\n' "$value" >"${STATUS_PATH}.tmp"
    chown root:root "${STATUS_PATH}.tmp"
    chmod 0600 "${STATUS_PATH}.tmp"
    mv -f -- "${STATUS_PATH}.tmp" "$STATUS_PATH"
}

fail_bootstrap() {
    local status="$1"
    trap - ERR
    if [[ "$bootstrap_activation_attempted" == true ]]; then
        systemctl disable --now haproxy >/dev/null 2>&1 || true
    fi
    record_status "GCP_BOOTSTRAP_FAIL exit=${status}" >/dev/null 2>&1 || true
    printf 'GCP_BOOTSTRAP_FAIL exit=%s\n' "$status" >&2
    exit "$status"
}

on_error() {
    local status=$?
    fail_bootstrap "$status"
}
trap on_error ERR

[[ "$(id -u)" -eq 0 ]] || die "must run as root"
[[ "$(hostname -s)" == "$EXPECTED_HOSTNAME" ]] \
    || die "refusing host '$(hostname -s)'; expected '$EXPECTED_HOSTNAME'"
for command_name in awk curl grep hostname id install mktemp mv rm sha256sum systemctl; do
    command -v "$command_name" >/dev/null 2>&1 \
        || die "missing required command: $command_name"
done

if [[ -f "$STATUS_PATH" && ! -L "$STATUS_PATH" ]] \
    && grep -Fxq 'GCP_BOOTSTRAP_PASS' "$STATUS_PATH"; then
    printf 'GCP_BOOTSTRAP_PASS already=true\n'
    exit 0
fi

install -d -o root -g root -m 0750 "$INSTALL_ROOT"
install_metadata_file sub2-tw-haproxy-config sub2-tw-haproxy-config-sha \
    "${INSTALL_ROOT}/haproxy.cfg" 0644
install_metadata_file sub2-tw-haproxy-installer sub2-tw-haproxy-installer-sha \
    "${INSTALL_ROOT}/install-gcp-haproxy.sh" 0750
install_metadata_file sub2-tw-transport-verifier sub2-tw-transport-verifier-sha \
    "${INSTALL_ROOT}/verify-transport.sh" 0750

HAPROXY_TEMPLATE="${INSTALL_ROOT}/haproxy.cfg" \
    "${INSTALL_ROOT}/install-gcp-haproxy.sh" stage
bootstrap_activation_attempted=true
"${INSTALL_ROOT}/install-gcp-haproxy.sh" activate
"${INSTALL_ROOT}/verify-transport.sh" gcp

record_status 'GCP_BOOTSTRAP_PASS'
bootstrap_activation_attempted=false
printf 'GCP_BOOTSTRAP_PASS\n'
