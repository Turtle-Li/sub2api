#!/usr/bin/env bash
set -Eeuo pipefail

# Read-only transport checks. Every HTTP request is pinned with --resolve, so
# this script never reads or changes public DNS and never uses credentials.

readonly API_HOST="api.turtleligpt.com"
readonly GCP_IP="130.211.243.139"
readonly AZURE_IP="4.216.216.16"
readonly EXPECTED_GCP_HOSTNAME="sub2-tw-line-candidate"
readonly CADDY_CONTAINER="sub2api-candidate-caddy"
readonly CADDYFILE="/opt/sub2api/Caddyfile"
readonly CADDY_VERSION="v2.11.4"
readonly HAPROXY_CONFIG="/etc/haproxy/haproxy.cfg"

mode="${1:-}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
renderer="${AZURE_CADDY_RENDERER:-${script_dir}/render-azure-caddy-listeners.py}"
json_verifier="${AZURE_CADDY_JSON_VERIFIER:-${script_dir}/verify-azure-caddy-json.py}"

die() {
    printf 'verify-transport: %s\n' "$*" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

file_sha() {
    sha256sum "$1" | awk '{print $1}'
}

assert_root() {
    [[ "$(id -u)" -eq 0 ]] || die "must run as root for exact process/listener assertions"
}

assert_debian_12() {
    # /etc/os-release is OS-owned declarative state.
    # shellcheck disable=SC1091
    . /etc/os-release
    [[ "${ID:-}" == 'debian' && "${VERSION_ID:-}" == '12' ]] \
        || die "expected Debian 12 Bookworm"
}

expect_status() {
    local expected="$1"
    local ip="$2"
    local path="$3"
    local actual
    actual="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
        --noproxy '*' \
        --connect-timeout 10 --max-time 30 \
        --resolve "${API_HOST}:443:${ip}" "https://${API_HOST}${path}")"
    [[ "$actual" == "$expected" ]] \
        || die "${ip}${path} returned HTTP ${actual}; expected ${expected}"
}

expect_http_redirect() {
    local ip="$1"
    local response status location
    response="$(curl --silent --show-error --output /dev/null \
        --noproxy '*' \
        --write-out '%{http_code}\n%{redirect_url}\n' \
        --connect-timeout 10 --max-time 30 \
        --resolve "${API_HOST}:80:${ip}" "http://${API_HOST}/")"
    status="$(printf '%s\n' "$response" | sed -n '1p')"
    location="$(printf '%s\n' "$response" | sed -n '2p')"
    [[ "$status" == '308' && "$location" == "https://${API_HOST}/" ]] \
        || die "${ip}:80 returned redirect status=${status:-missing} location=${location:-missing}"
}

assert_no_h3_advertisement() {
    local ip="$1"
    if curl --silent --show-error --output /dev/null --dump-header - \
        --noproxy '*' \
        --connect-timeout 10 --max-time 30 \
        --resolve "${API_HOST}:443:${ip}" "https://${API_HOST}/health" \
        | tr -d '\r' | grep -Eiq '^alt-svc:.*h3'; then
        die "${ip}:443 advertised h3 even though the ingress is TCP-only"
    fi
}

certificate_fingerprint() {
    local ip="$1"
    openssl s_client -connect "${ip}:443" -servername "$API_HOST" \
        -verify_return_error </dev/null 2>/dev/null \
        | openssl x509 -noout -fingerprint -sha256 \
        | awk -F= '{gsub(":", "", $2); print tolower($2)}'
}

assert_haproxy_config_contract() {
    local http_server https_server
    [[ -f "$HAPROXY_CONFIG" && ! -L "$HAPROXY_CONFIG" ]] \
        || die "missing regular HAProxy configuration: $HAPROXY_CONFIG"
    haproxy -c -f "$HAPROXY_CONFIG" >/dev/null
    http_server="$(awk '$1 == "server" && $2 == "azure_http" { print }' "$HAPROXY_CONFIG")"
    https_server="$(awk '$1 == "server" && $2 == "azure_https" { print }' "$HAPROXY_CONFIG")"
    [[ "$http_server" == *"${AZURE_IP}:80"* && "$http_server" != *send-proxy-v2* ]] \
        || die "HTTP backend must stay plain TCP without PROXY protocol"
    [[ "$https_server" == *"${AZURE_IP}:443"* && "$https_server" == *send-proxy-v2* \
        && "$https_server" == *'check-sni api.turtleligpt.com'* \
        && "$https_server" == *'verify required'* \
        && "$https_server" == *'verifyhost api.turtleligpt.com'* ]] \
        || die "HTTPS backend lacks the pinned PROXY-v2/SNI/CA health contract"
    grep -Fqx '    chroot /var/lib/haproxy' "$HAPROXY_CONFIG" \
        || die "HAProxy config lacks the package chroot"
    grep -Fqx '    user haproxy' "$HAPROXY_CONFIG" \
        || die "HAProxy config lacks its unprivileged worker user"
    grep -Fqx '    group haproxy' "$HAPROXY_CONFIG" \
        || die "HAProxy config lacks its unprivileged worker group"
}

assert_haproxy_runtime_isolation() {
    local worker_pid worker_root
    worker_pid="$(pgrep -u haproxy -x haproxy | head -n 1)"
    [[ "$worker_pid" =~ ^[0-9]+$ ]] \
        || die "no HAProxy worker is running as the unprivileged haproxy user"
    worker_root="$(readlink -f "/proc/${worker_pid}/root")"
    [[ "$worker_root" == '/var/lib/haproxy' ]] \
        || die "HAProxy worker is not confined to /var/lib/haproxy (got: ${worker_root:-missing})"
}

assert_gcp_listeners() {
    local tcp_listeners
    tcp_listeners="$(ss -H -ltnp)"
    printf '%s\n' "$tcp_listeners" | grep -Eq '0\.0\.0\.0:80([[:space:]].*)?haproxy' \
        || die "HAProxy is not the IPv4 TCP :80 listener"
    printf '%s\n' "$tcp_listeners" | grep -Eq '0\.0\.0\.0:443([[:space:]].*)?haproxy' \
        || die "HAProxy is not the IPv4 TCP :443 listener"
    if ss -H -lun | awk '$4 ~ /:443$/ { found = 1 } END { exit !found }'; then
        die "UDP/443 listener detected; this TCP-only ingress must not advertise QUIC"
    fi
}

verify_gcp() {
    local actual_hostname
    for command_name in awk curl grep haproxy head hostname id pgrep readlink ss systemctl; do
        require_command "$command_name"
    done
    assert_root
    assert_debian_12
    actual_hostname="$(hostname -s)"
    [[ "$actual_hostname" == "$EXPECTED_GCP_HOSTNAME" ]] \
        || die "refusing host '$actual_hostname'; expected '$EXPECTED_GCP_HOSTNAME'"
    systemctl is-active --quiet haproxy || die "HAProxy is not active"
    assert_haproxy_config_contract
    assert_haproxy_runtime_isolation
    assert_gcp_listeners
    expect_status 200 127.0.0.1 /health
    expect_status 401 127.0.0.1 /v1/models
    printf 'GCP_TRANSPORT_VERIFY_PASS host=%s backend=%s https_proxy_v2=true http_proxy_v2=false\n' \
        "$actual_hostname" "$AZURE_IP"
}

assert_azure_runtime_contract() {
    local mount caddy_version host_identity container_identity
    local adapted_fingerprint active_fingerprint
    [[ -f "$CADDYFILE" && ! -L "$CADDYFILE" ]] \
        || die "missing regular Azure Caddyfile: $CADDYFILE"
    [[ -f "$renderer" && ! -L "$renderer" ]] \
        || die "missing renderer beside this verifier: $renderer"
    [[ -f "$json_verifier" && ! -L "$json_verifier" && -x "$json_verifier" ]] \
        || die "missing executable Caddy JSON verifier: $json_verifier"
    docker inspect "$CADDY_CONTAINER" >/dev/null 2>&1 \
        || die "candidate Caddy container is missing: $CADDY_CONTAINER"
    [[ "$(docker inspect "$CADDY_CONTAINER" --format '{{.State.Running}}')" == 'true' ]] \
        || die "candidate Caddy container is not running"
    mount="$(docker inspect "$CADDY_CONTAINER" --format '{{range .Mounts}}{{if eq .Destination "/etc/caddy/Caddyfile"}}{{.Source}}|{{.Type}}|{{.RW}}{{end}}{{end}}')"
    [[ "$mount" == "$CADDYFILE|bind|false" ]] \
        || die "Caddyfile mount contract mismatch: $mount"
    [[ "$(file_sha "$CADDYFILE")" == "$(docker exec "$CADDY_CONTAINER" sha256sum /etc/caddy/Caddyfile | awk '{print $1}')" ]] \
        || die "host and container Caddyfile hashes differ"
    host_identity="$(stat -c '%d:%i' "$CADDYFILE")"
    container_identity="$(docker exec "$CADDY_CONTAINER" stat -c '%d:%i' /etc/caddy/Caddyfile)"
    [[ "$host_identity" == "$container_identity" ]] \
        || die "host and container Caddyfile bind inodes differ"
    caddy_version="$(docker exec "$CADDY_CONTAINER" caddy version | awk '{print $1}')"
    [[ "$caddy_version" == "$CADDY_VERSION" ]] \
        || die "expected Caddy $CADDY_VERSION, got $caddy_version"
    docker exec "$CADDY_CONTAINER" caddy list-modules 2>/dev/null \
        | grep -Fx 'caddy.listeners.proxy_protocol' >/dev/null \
        || die "Caddy lacks the required PROXY protocol listener wrapper"
    "$renderer" verify "$CADDYFILE"
    docker exec "$CADDY_CONTAINER" caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile >/dev/null
    adapted_fingerprint="$(docker exec "$CADDY_CONTAINER" \
        caddy adapt --config /etc/caddy/Caddyfile --adapter caddyfile \
        | "$json_verifier")" \
        || die "adapted startup Caddy config violates the ingress contract"
    active_fingerprint="$(docker exec "$CADDY_CONTAINER" sh -c \
        'wget -Y off -qO- http://127.0.0.1:2019/config/ 2>/dev/null || curl --noproxy "*" -fsS http://127.0.0.1:2019/config/' \
        | "$json_verifier")" \
        || die "active Caddy admin config violates the ingress contract"
    [[ "$active_fingerprint" == "$adapted_fingerprint" ]] \
        || die "active Caddy listener/API contract differs from its startup file"
}

assert_untrusted_proxy_header_rejected() {
    python3 - "$AZURE_IP" "$API_HOST" <<'PY'
import socket
import ssl
import struct
import sys

ip, hostname = sys.argv[1:]
signature = b"\r\n\r\n\x00\r\nQUIT\n"
header = signature + bytes((0x21, 0x11)) + struct.pack("!H", 12)
header += socket.inet_aton("198.51.100.10") + socket.inet_aton(ip)
header += struct.pack("!HH", 45678, 443)

try:
    connection = socket.create_connection((ip, 443), timeout=10)
except OSError as exc:
    raise SystemExit(f"could not establish the direct Azure test connection: {exc}")

try:
    connection.sendall(header)
    context = ssl.create_default_context()
    tls = context.wrap_socket(connection, server_hostname=hostname)
except (ConnectionError, OSError, ssl.SSLError):
    print("untrusted PROXY v2 header rejected")
    raise SystemExit(0)
else:
    tls.close()
    raise SystemExit("untrusted source unexpectedly completed a TLS handshake after a PROXY v2 header")
PY
}

verify_azure() {
    for command_name in awk curl docker grep id python3 sha256sum stat; do
        require_command "$command_name"
    done
    assert_root
    assert_azure_runtime_contract
    # This direct connection originates outside the GCP allowlist. `skip`
    # preserves ordinary TLS fallback while a forged PROXY preface is rejected.
    expect_status 200 "$AZURE_IP" /health
    expect_status 401 "$AZURE_IP" /v1/models
    assert_no_h3_advertisement "$AZURE_IP"
    assert_untrusted_proxy_header_rejected
    printf 'AZURE_TRANSPORT_VERIFY_PASS direct_fallback=true forged_proxy_rejected=true\n'
}

verify_canary() {
    local azure_fingerprint gcp_fingerprint
    for command_name in awk curl grep openssl sed tr; do
        require_command "$command_name"
    done
    expect_status 200 "$GCP_IP" /health
    expect_status 401 "$GCP_IP" /v1/models
    expect_http_redirect "$GCP_IP"
    assert_no_h3_advertisement "$GCP_IP"
    openssl s_client -connect "${GCP_IP}:443" -servername "$API_HOST" \
        -verify_return_error </dev/null 2>/dev/null \
        | openssl x509 -noout -subject -ext subjectAltName \
        | grep -F "DNS:${API_HOST}" >/dev/null \
        || die "GCP canary did not present Azure's verified API certificate"
    azure_fingerprint="$(certificate_fingerprint "$AZURE_IP")"
    gcp_fingerprint="$(certificate_fingerprint "$GCP_IP")"
    [[ "$azure_fingerprint" =~ ^[0-9a-f]{64}$ && "$gcp_fingerprint" == "$azure_fingerprint" ]] \
        || die "GCP and direct Azure TLS certificate fingerprints differ"
    printf 'GCP_CANARY_VERIFY_PASS ip=%s dns_changed=false tls_fingerprint_equal=true http_redirect=true h3=false\n' \
        "$GCP_IP"
}

case "$mode" in
    gcp|azure|canary) ;;
    *)
        printf 'usage: %s gcp|azure|canary\n' "$0" >&2
        exit 64
        ;;
esac

case "$mode" in
    gcp) verify_gcp ;;
    azure) verify_azure ;;
    canary) verify_canary ;;
esac
