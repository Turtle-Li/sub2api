#!/usr/bin/env bash
set -Eeuo pipefail

test_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
line_dir="$(cd -- "${test_dir}/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/sub2-tw-transport-test.XXXXXX")"
test_root="$(cd "$test_root" && pwd -P)"
lock_holder_pid=""

cleanup() {
    if [[ -n "${lock_holder_pid:-}" ]] && kill -0 "$lock_holder_pid" >/dev/null 2>&1; then
        if [[ -n "${lock_holder_release:-}" ]]; then
            : >"$lock_holder_release"
        fi
        kill "$lock_holder_pid" >/dev/null 2>&1 || true
        wait "$lock_holder_pid" >/dev/null 2>&1 || true
    fi
    rm -rf -- "$test_root"
}
trap cleanup EXIT

fail() {
    printf 'transport-config-test: %s\n' "$*" >&2
    exit 1
}

for script in \
    azure-caddy-listeners.sh \
    gcp-startup-bootstrap.sh \
    gcp-update-bootstrap.sh \
    install-gcp-haproxy.sh \
    verify-transport.sh; do
    bash -n "${line_dir}/${script}"
done
PYTHONPYCACHEPREFIX="${test_root}/pycache" \
    python3 -m py_compile "${line_dir}/render-azure-caddy-listeners.py"
PYTHONPYCACHEPREFIX="${test_root}/pycache" \
    python3 -m py_compile "${line_dir}/verify-azure-caddy-json.py"
PYTHONPYCACHEPREFIX="${test_root}/pycache" \
    python3 -m py_compile "${line_dir}/patch-old-origin-node-state.py"
"${line_dir}/patch-old-origin-node-state.py" --self-test

source_caddy="${test_root}/Caddyfile.source"
staged_caddy="${test_root}/Caddyfile.staged"
cat >"$source_caddy" <<'EOF'
{
}

api.turtleligpt.com {
	handle /v1/responses* {
		reverse_proxy sub2api-green:8080 {
			flush_interval -1
		}
	}
	handle {
		reverse_proxy sub2api-green:8080 {
			flush_interval -1
		}
	}
}
EOF

"${line_dir}/render-azure-caddy-listeners.py" render "$source_caddy" "$staged_caddy"
"${line_dir}/render-azure-caddy-listeners.py" verify "$staged_caddy"
grep -Fqx $'\tservers :443 {' "$staged_caddy" \
    || fail 'renderer did not scope the listener policy to :443'
grep -Fqx $'\t\tprotocols h1 h2' "$staged_caddy" \
    || fail 'renderer did not disable HTTP/3 on the TCP-only ingress'
grep -Fqx $'\t\t\t\tallow 130.211.243.139/32' "$staged_caddy" \
    || fail 'renderer did not pin the PROXY allowlist to the exact GCP address'
grep -Fqx $'\t\t\t\tfallback_policy skip' "$staged_caddy" \
    || fail 'renderer did not preserve direct TLS fallback'
if grep -Fq 'servers :80' "$staged_caddy"; then
    fail 'renderer unexpectedly wrapped Caddy automatic HTTP redirect traffic'
fi
proxy_line="$(grep -nF $'\t\t\tproxy_protocol {' "$staged_caddy" | cut -d: -f1)"
tls_line="$(grep -nF $'\t\t\ttls' "$staged_caddy" | cut -d: -f1)"
[[ -n "$proxy_line" && -n "$tls_line" && "$proxy_line" -lt "$tls_line" ]] \
    || fail 'PROXY listener wrapper must precede TLS'
sed -n '/^api\.turtleligpt\.com/,$p' "$source_caddy" >"${test_root}/source.tail"
sed -n '/^api\.turtleligpt\.com/,$p' "$staged_caddy" >"${test_root}/staged.tail"
sed -e '/^[[:space:]]*header_up X-Forwarded-For {remote_host}$/d' \
    -e '/^[[:space:]]*header_up -X-Real-IP$/d' \
    -e '/^[[:space:]]*header_up -CF-Connecting-IP$/d' \
    "${test_root}/staged.tail" >"${test_root}/staged-without-policy.tail"
cmp -s "${test_root}/source.tail" "${test_root}/staged-without-policy.tail" \
    || fail 'renderer changed bytes outside the reviewed global/header policies'
[[ "$(grep -Fc 'header_up X-Forwarded-For {remote_host}' "$staged_caddy")" -eq 2 \
    && "$(grep -Fc 'header_up -X-Real-IP' "$staged_caddy")" -eq 2 \
    && "$(grep -Fc 'header_up -CF-Connecting-IP' "$staged_caddy")" -eq 2 ]] \
    || fail 'renderer did not harden both production API reverse proxies'
if "${line_dir}/render-azure-caddy-listeners.py" render "$staged_caddy" \
    "${test_root}/second-render" >/dev/null 2>&1; then
    fail 'renderer accepted a non-empty global-options block'
fi
ln -s "$source_caddy" "${test_root}/source.link"
if "${line_dir}/render-azure-caddy-listeners.py" render "${test_root}/source.link" \
    "${test_root}/link-output" >/dev/null 2>&1; then
    fail 'renderer accepted a symlink source'
fi

caddy_json="${test_root}/caddy.json"
cat >"$caddy_json" <<'EOF'
{
  "apps": {"http": {"servers": {"srv0": {
    "listen": [":443"],
    "protocols": ["h1", "h2"],
    "listener_wrappers": [
      {"wrapper": "proxy_protocol", "allow": ["130.211.243.139/32"], "fallback_policy": "SKIP", "timeout": 2000000000},
      {"wrapper": "tls"}
    ],
    "routes": [{
      "match": [{"host": ["api-cf-test.turtleligpt.com"]}],
      "handle": [{"handler": "static_response", "status_code": 404}],
      "terminal": true
    }, {
      "match": [{"host": ["api.turtleligpt.com"]}],
      "handle": [{"handler": "subroute", "routes": [{"handle": [
        {"handler": "reverse_proxy", "headers": {"request": {
          "delete": ["X-Real-IP", "CF-Connecting-IP"],
          "set": {"X-Forwarded-For": ["{http.request.remote.host}"]}
        }}, "upstreams": [{"dial": "sub2api-green:8080"}]},
        {"handler": "reverse_proxy", "headers": {"request": {
          "delete": ["X-Real-IP", "CF-Connecting-IP"],
          "set": {"X-Forwarded-For": ["{http.request.remote.host}"]}
        }}, "upstreams": [{"dial": "sub2api-green:8080"}]}
      ]}]}],
      "terminal": true
    }]
  }}}}
}
EOF
contract_fingerprint="$("${line_dir}/verify-azure-caddy-json.py" <"$caddy_json")"
[[ "$contract_fingerprint" =~ ^[0-9a-f]{64}$ ]] \
    || fail 'Caddy JSON verifier did not fingerprint the valid runtime contract'
python3 - "$caddy_json" "${test_root}/caddy-bad.json" <<'PY'
import json
import sys

source, destination = sys.argv[1:]
with open(source, encoding="utf-8") as handle:
    document = json.load(handle)
server = document["apps"]["http"]["servers"]["srv0"]
server["listener_wrappers"][0]["allow"] = ["0.0.0.0/0"]
with open(destination, "w", encoding="utf-8") as handle:
    json.dump(document, handle)
PY
if "${line_dir}/verify-azure-caddy-json.py" <"${test_root}/caddy-bad.json" >/dev/null 2>&1; then
    fail 'Caddy JSON verifier accepted a widened PROXY allowlist'
fi
python3 - "$caddy_json" "${test_root}/caddy-forged-header.json" <<'PY'
import json
import sys

source, destination = sys.argv[1:]
with open(source, encoding="utf-8") as handle:
    document = json.load(handle)
route = document["apps"]["http"]["servers"]["srv0"]["routes"][1]
route["handle"].append({
    "handler": "headers",
    "request": {"set": {"CF-Connecting-IP": ["{http.request.header.CF-Connecting-IP}"]}},
})
with open(destination, "w", encoding="utf-8") as handle:
    json.dump(document, handle)
PY
if "${line_dir}/verify-azure-caddy-json.py" <"${test_root}/caddy-forged-header.json" >/dev/null 2>&1; then
    fail 'Caddy JSON verifier accepted a forged client-IP header path'
fi
python3 - "$caddy_json" "${test_root}/caddy-missing-header-policy.json" <<'PY'
import json
import sys

source, destination = sys.argv[1:]
with open(source, encoding="utf-8") as handle:
    document = json.load(handle)
handler = document["apps"]["http"]["servers"]["srv0"]["routes"][1]["handle"][0]["routes"][0]["handle"][0]
del handler["headers"]["request"]["delete"]
with open(destination, "w", encoding="utf-8") as handle:
    json.dump(document, handle)
PY
if "${line_dir}/verify-azure-caddy-json.py" <"${test_root}/caddy-missing-header-policy.json" >/dev/null 2>&1; then
    fail 'Caddy JSON verifier accepted an incomplete client-IP header policy'
fi
python3 - "$caddy_json" "${test_root}/caddy-shadow-route.json" <<'PY'
import json
import sys

source, destination = sys.argv[1:]
with open(source, encoding="utf-8") as handle:
    document = json.load(handle)
server = document["apps"]["http"]["servers"]["srv0"]
server["routes"].insert(1, {
    "handle": [{"handler": "reverse_proxy", "upstreams": [{"dial": "shadow:8080"}]}],
    "terminal": True,
})
with open(destination, "w", encoding="utf-8") as handle:
    json.dump(document, handle)
PY
if "${line_dir}/verify-azure-caddy-json.py" <"${test_root}/caddy-shadow-route.json" >/dev/null 2>&1; then
    fail 'Caddy JSON verifier accepted an earlier catch-all production shadow route'
fi

http_server="$(awk '$1 == "server" && $2 == "azure_http" { print }' "${line_dir}/haproxy.cfg")"
https_server="$(awk '$1 == "server" && $2 == "azure_https" { print }' "${line_dir}/haproxy.cfg")"
[[ "$http_server" == *'4.216.216.16:80'* && "$http_server" != *send-proxy-v2* ]] \
    || fail 'HAProxy HTTP backend must be plain TCP without PROXY protocol'
[[ "$https_server" == *'4.216.216.16:443'* \
    && "$https_server" == *send-proxy-v2* \
    && "$https_server" == *'check-sni api.turtleligpt.com'* \
    && "$https_server" == *'verify required'* \
    && "$https_server" == *'verifyhost api.turtleligpt.com'* ]] \
    || fail 'HAProxy HTTPS backend lacks the frozen PROXY/SNI/CA contract'
if grep -Eq '^[[:space:]]*mode[[:space:]]+http([[:space:]]|$)|^[[:space:]]*bind[^#]*[[:space:]]ssl([[:space:]]|$)' \
    "${line_dir}/haproxy.cfg"; then
    fail 'HAProxy configuration unexpectedly terminates HTTP or TLS'
fi
grep -Fqx '    chroot /var/lib/haproxy' "${line_dir}/haproxy.cfg" \
    || fail 'HAProxy must retain the Debian chroot'
grep -Fqx '    user haproxy' "${line_dir}/haproxy.cfg" \
    || fail 'HAProxy must drop worker privileges to the package user'
grep -Fqx '    group haproxy' "${line_dir}/haproxy.cfg" \
    || fail 'HAProxy must drop worker privileges to the package group'
grep -Fqx '    timeout client-fin 1800s' "${line_dir}/haproxy.cfg" \
    || fail 'HAProxy must not truncate half-closed long streams at 30 seconds'
grep -Fq 'pgrep -u haproxy -x haproxy' "${line_dir}/verify-transport.sh" \
    || fail 'GCP verifier does not prove the HAProxy worker dropped privileges'
[[ "$(grep -Fc -- "--noproxy '*'" "${line_dir}/verify-transport.sh")" -eq 3 ]] \
    || fail 'transport HTTP probes do not consistently bypass ambient proxy settings'
for metadata_bootstrap in gcp-startup-bootstrap.sh gcp-update-bootstrap.sh; do
    grep -Fq -- "--noproxy '*'" "${line_dir}/${metadata_bootstrap}" \
        || fail "${metadata_bootstrap} can send metadata requests through an ambient proxy"
done
# The Cloudflare optimized-IP POC is retired and intentionally absent from the
# integrated release. Its historical transaction filename remains a safety
# fence in live mutators so an unfinished transaction cannot be ignored.
for host_control in sub2api-server-release.sh sub2api-runtime-guard.sh; do
    grep -Fq -- "--noproxy '*'" "${line_dir}/../${host_control}" \
        || fail "${host_control} public health can escape its pinned direct path"
done
for container_control in \
    azure-caddy-listeners.sh \
    verify-transport.sh \
    ../sub2api-blue-green-release.sh \
    ../sub2api-drain-monitor.sh \
    ../sub2api-server-release.sh \
    ../sub2api-runtime-guard.sh; do
    grep -Fq -- 'wget -Y off' "${line_dir}/${container_control}" \
        || fail "${container_control} local container control-plane probe can inherit a proxy"
done

# shellcheck disable=SC2016 # The search text intentionally names shell variables literally.
write_state_line="$(grep -nF '    write_state "$backup_file" "$after_file" "$before_sha_local" "$after_sha_local"' \
    "${line_dir}/azure-caddy-listeners.sh" | cut -d: -f1)"
load_state_line="$(awk -v start="$write_state_line" 'NR > start && $0 == "    load_state" { print NR; exit }' \
    "${line_dir}/azure-caddy-listeners.sh")"
copy_live_line="$(awk -v start="$write_state_line" 'NR > start && /if ! copy_into_live_bind/ { print NR; exit }' \
    "${line_dir}/azure-caddy-listeners.sh")"
[[ -n "$write_state_line" && -n "$load_state_line" && -n "$copy_live_line" \
    && "$write_state_line" -lt "$load_state_line" && "$load_state_line" -lt "$copy_live_line" ]] \
    || fail 'Azure transaction state is not loaded before live verification/restore helpers'

# shellcheck disable=SC2016 # The search text intentionally names a shell variable literally.
copy_failure_line="$(grep -nF '    if ! copy_into_live_bind "$candidate"; then' \
    "${line_dir}/azure-caddy-listeners.sh" | cut -d: -f1)"
copy_restore_line="$(awk -v start="$copy_failure_line" \
    'NR > start && /if restore_before/ { print NR; exit }' "${line_dir}/azure-caddy-listeners.sh")"
copy_remove_line="$(awk -v start="$copy_failure_line" \
    'NR > start && /rm -f -- "\$TRANSACTION_PATH"/ { print NR; exit }' "${line_dir}/azure-caddy-listeners.sh")"
[[ -n "$copy_failure_line" && -n "$copy_restore_line" && -n "$copy_remove_line" \
    && "$copy_failure_line" -lt "$copy_restore_line" && "$copy_restore_line" -lt "$copy_remove_line" ]] \
    || fail 'Azure bind-write failure discards recovery state before restoring its backup'
grep -Fq 'matches neither transaction endpoint; restoring BEFORE_SHA' \
    "${line_dir}/azure-caddy-listeners.sh" \
    || fail 'Azure rollback does not surface a live-state drift warning'

if grep -Fq 'os.replace(' "${line_dir}/../sub2api-blue-green-release.sh"; then
    fail 'blue-green Caddy updates must preserve the running file-bind inode'
fi
for mutator in sub2api-blue-green-release.sh sub2api-server-release.sh sub2api-runtime-guard.sh; do
    grep -Fq '.gcp-tw-caddy-transaction.env' "${line_dir}/../${mutator}" \
        || fail "${mutator} lacks the retained listener-transaction fence"
    grep -Fq '.cf-opt-totools-caddy.env' "${line_dir}/../${mutator}" \
        || fail "${mutator} lacks the retained customer-Host transaction fence"
    grep -Fq '.sub2api-blue-green-caddy-transaction.env' "${line_dir}/../${mutator}" \
        || fail "${mutator} lacks the durable blue-green Caddy transaction"
done
grep -Fq '.cf-opt-totools-caddy.env' "${line_dir}/azure-caddy-listeners.sh" \
    || fail 'Azure listener staging lacks the customer-Host transaction fence'
grep -Fq '.sub2api-blue-green-caddy-transaction.env' "${line_dir}/azure-caddy-listeners.sh" \
    || fail 'Azure listener staging lacks the blue-green transaction fence'

grep -Fq 'gce-security-mirror-file|/etc/apt/mirrors/debian-security.list' \
    "${line_dir}/install-gcp-haproxy.sh" \
    || fail 'HAProxy installer rejects Debian security mirror manifests'
grep -Fq 'stage|activate|update|rollback|status' "${line_dir}/install-gcp-haproxy.sh" \
    || fail 'HAProxy installer lacks its non-disruptive update phase'
grep -Fq "write_state STAGED" "${line_dir}/install-gcp-haproxy.sh" \
    || fail 'HAProxy recovery authority is not published before mutation'
grep -Fq 'ORIGIN_BACKUP_PATH=%s' "${line_dir}/install-gcp-haproxy.sh" \
    || fail 'HAProxy updates do not preserve an immutable origin recovery anchor'
grep -Fq '|| ! run_post_update_verify; then' "${line_dir}/install-gcp-haproxy.sh" \
    || fail 'HAProxy runtime verification is outside the update rollback transaction'
# shellcheck disable=SC2016 # The assertion intentionally names the shell expression literally.
grep -Fq 'post_update_verify="${HAPROXY_POST_UPDATE_VERIFY:-${script_dir}/verify-transport.sh}"' \
    "${line_dir}/install-gcp-haproxy.sh" \
    || fail 'manual HAProxy updates do not default to the sibling runtime verifier'
# shellcheck disable=SC2016 # The assertion intentionally names the shell expression literally.
grep -Fq 'HAPROXY_POST_UPDATE_VERIFY="${INSTALL_ROOT}/verify-transport.sh"' \
    "${line_dir}/gcp-update-bootstrap.sh" \
    || fail 'GCE updater does not bind the runtime verifier into the HAProxy transaction'

# The HAProxy transaction has one root-private lock domain. Use a host guard as
# a post-lock barrier to prove every mutation phase is rejected before it can
# touch the config, transaction, package manager, or systemd.
grep -Fq 'readonly MUTATION_LOCK_DEFAULT_PATH="/run/sub2api-gcp-tw-line/haproxy-mutation.lock"' \
    "${line_dir}/install-gcp-haproxy.sh" \
    || fail 'HAProxy mutation lock default is not a private runtime path'
grep -Fq 'apt-get awk date dpkg-query flock grep haproxy' "${line_dir}/install-gcp-haproxy.sh" \
    || fail 'HAProxy installer does not require flock before mutation'
grep -Fq 'stage|activate|update|rollback) acquire_mutation_lock ;;' \
    "${line_dir}/install-gcp-haproxy.sh" \
    || fail 'one or more HAProxy mutation phases bypass the private lock'

lock_fake_bin="${test_root}/haproxy-lock-bin"
lock_dir="${test_root}/haproxy-locks"
lock_file="${lock_dir}/haproxy-mutation.lock"
lock_hostname_calls="${test_root}/haproxy-lock-hostname-calls.log"
lock_holder_ready="${test_root}/haproxy-lock-holder-ready"
lock_holder_release="${test_root}/haproxy-lock-holder-release"
mkdir -p "$lock_fake_bin" "$lock_dir"
chmod 700 "$lock_dir"
for command_name in apt-cache apt-get dpkg-query haproxy systemctl; do
    cat >"${lock_fake_bin}/${command_name}" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
    chmod +x "${lock_fake_bin}/${command_name}"
done
cat >"${lock_fake_bin}/flock" <<'EOF'
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
EOF
cat >"${lock_fake_bin}/hostname" <<'EOF'
#!/usr/bin/env bash

set -Eeuo pipefail

printf '%s\n' "$*" >>"$HAPROXY_LOCK_TEST_HOSTNAME_CALLS"
if [[ "${HAPROXY_LOCK_TEST_HOLD:-0}" == 1 ]]; then
    : >"$HAPROXY_LOCK_TEST_READY"
    while [[ ! -e "$HAPROXY_LOCK_TEST_RELEASE" ]]; do
        sleep 0.01
    done
fi
# End the holder before any config, transaction, service, or package operation.
printf '%s\n' 'not-the-gcp-taiwan-candidate'
EOF
chmod +x "${lock_fake_bin}/flock" "${lock_fake_bin}/hostname"

wait_for_haproxy_lock_file() {
    local path="$1" label="$2"

    for _ in $(seq 1 200); do
        [[ -e "$path" ]] && return
        sleep 0.01
    done
    fail "${label} did not become ready"
}

run_haproxy_lock_phase() {
    local requested_phase="$1" requested_lock="$2"

    env \
        PATH="${lock_fake_bin}:${PATH}" \
        TMPDIR="$test_root" \
        HAPROXY_MUTATION_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1 \
        HAPROXY_MUTATION_LOCK_FILE_FOR_TESTS="$requested_lock" \
        HAPROXY_LOCK_TEST_HOLD="${HAPROXY_LOCK_TEST_HOLD:-0}" \
        HAPROXY_LOCK_TEST_READY="$lock_holder_ready" \
        HAPROXY_LOCK_TEST_RELEASE="$lock_holder_release" \
        HAPROXY_LOCK_TEST_HOSTNAME_CALLS="$lock_hostname_calls" \
        /bin/bash "${line_dir}/install-gcp-haproxy.sh" "$requested_phase"
}

HAPROXY_LOCK_TEST_HOLD=1 run_haproxy_lock_phase stage "$lock_file" \
    >"${test_root}/haproxy-lock-holder.out" 2>&1 &
lock_holder_pid=$!
wait_for_haproxy_lock_file "$lock_holder_ready" 'HAProxy mutation lock holder'
for requested_phase in stage activate update rollback; do
    output="${test_root}/haproxy-${requested_phase}-contended.out"
    if HAPROXY_LOCK_TEST_HOLD=0 run_haproxy_lock_phase "$requested_phase" "$lock_file" >"$output" 2>&1; then
        : >"$lock_holder_release"
        wait "$lock_holder_pid" || true
        fail "${requested_phase} bypassed an active HAProxy mutation lock"
    fi
    grep -Fq 'another GCP Taiwan HAProxy mutation is already running' "$output" \
        || { sed -n '1,120p' "$output" >&2; fail "${requested_phase} did not fail at lock contention"; }
done
[[ "$(wc -l <"$lock_hostname_calls")" -eq 1 ]] \
    || fail 'a contended HAProxy phase reached host/config/service work'

: >"$lock_holder_release"
if wait "$lock_holder_pid"; then
    fail 'test lock holder unexpectedly reached a successful stage'
fi
lock_holder_pid=""

# The holder's descriptor is released on exit: a new phase reaches the host
# guard instead of reporting stale flock contention.
if HAPROXY_LOCK_TEST_HOLD=0 run_haproxy_lock_phase stage "$lock_file" \
    >"${test_root}/haproxy-lock-reentry.out" 2>&1; then
    fail 'post-holder stage unexpectedly succeeded'
fi
grep -Fq "refusing host 'not-the-gcp-taiwan-candidate'" "${test_root}/haproxy-lock-reentry.out" \
    || { sed -n '1,120p' "${test_root}/haproxy-lock-reentry.out" >&2; fail 'mutation lock remained held after owner exit'; }

unsafe_lock_parent="${test_root}/haproxy-unsafe-lock-parent"
mkdir -m 755 "$unsafe_lock_parent"
if HAPROXY_LOCK_TEST_HOLD=0 run_haproxy_lock_phase stage "${unsafe_lock_parent}/lock" \
    >"${test_root}/haproxy-unsafe-parent.out" 2>&1; then
    fail 'permissive mutation-lock parent was accepted'
fi
grep -Fq 'mutation-lock parent must be owned privately with mode 0700' \
    "${test_root}/haproxy-unsafe-parent.out" \
    || { sed -n '1,120p' "${test_root}/haproxy-unsafe-parent.out" >&2; fail 'unsafe lock parent did not fail closed'; }

grep -Fq 'readonly EXPECTED_HOSTNAME="sub2-tw-line-candidate"' \
    "${line_dir}/gcp-startup-bootstrap.sh" \
    || fail 'GCE bootstrap lacks an exact-host refusal'
grep -Fq "GCP_BOOTSTRAP_PASS" "${line_dir}/gcp-startup-bootstrap.sh" \
    || fail 'GCE bootstrap lacks a machine-readable success marker'
grep -Fq "GCP_BOOTSTRAP_FAIL" "${line_dir}/gcp-startup-bootstrap.sh" \
    || fail 'GCE bootstrap lacks a machine-readable failure marker'
grep -Fq "GCP_UPDATE_PASS" "${line_dir}/gcp-update-bootstrap.sh" \
    || fail 'GCE updater lacks a machine-readable success marker'
grep -Fq "GCP_UPDATE_FAIL" "${line_dir}/gcp-update-bootstrap.sh" \
    || fail 'GCE updater lacks a machine-readable failure marker'

python3 - \
    "${line_dir}/haproxy.cfg" \
    "${line_dir}/install-gcp-haproxy.sh" \
    "${line_dir}/gcp-startup-bootstrap.sh" \
    "${line_dir}/gcp-update-bootstrap.sh" \
    "${line_dir}/patch-old-origin-node-state.py" \
    "${line_dir}/render-azure-caddy-listeners.py" \
    "${line_dir}/verify-azure-caddy-json.py" \
    "${line_dir}/azure-caddy-listeners.sh" \
    "${line_dir}/verify-transport.sh" \
    "$0" <<'PY'
import pathlib
import sys

for name in sys.argv[1:]:
    data = pathlib.Path(name).read_bytes()
    if not data.endswith(b"\n"):
        raise SystemExit(f"{name}: missing final newline")
    if data.endswith(b"\n\n"):
        raise SystemExit(f"{name}: new blank line at EOF")
    for number, line in enumerate(data.splitlines(), 1):
        if line.endswith((b" ", b"\t")):
            raise SystemExit(f"{name}:{number}: trailing whitespace")
PY

printf 'Sub2 Taiwan transport static tests passed.\n'
