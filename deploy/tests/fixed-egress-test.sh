#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "$0")" && pwd)"
DEPLOY_DIR="$(cd "$TEST_DIR/.." && pwd)"
SCRIPT="$DEPLOY_DIR/install-sub2api-fixed-egress.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-fixed-egress-test.XXXXXX")"

cleanup() {
  rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  grep -Fq -- "$2" "$1" || fail "missing required fixed-egress contract: $2"
}

assert_not_contains() {
  if grep -Fq -- "$2" "$1"; then
    fail "found forbidden fixed-egress contract: $2"
  fi
}

bash -n "$SCRIPT"
# shellcheck source=../install-sub2api-fixed-egress.sh
source "$SCRIPT"
render_dante_config 100.79.114.100 eth0 100.80.10.114/32 172.18.0.0/16 >"$TEST_ROOT/danted.conf"
render_nft_config 100.79.114.100 100.80.10.114/32 172.18.0.0/16 >"$TEST_ROOT/firewall.nft"

python3 - "$TEST_ROOT/danted.conf" <<'PY'
import pathlib
import re
import sys

text = pathlib.Path(sys.argv[1]).read_text()
passes = list(re.finditer(r"socks pass\s*\{.*?command:\s*connect.*?\}", text, re.S))
if len(passes) != 1:
    raise SystemExit(f"expected one public CONNECT pass, got {len(passes)}")
pass_offset = passes[0].start()
for network in (
    "0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
    "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16",
    "224.0.0.0/4", "240.0.0.0/4",
):
    match = re.search(
        r"socks block\s*\{(?:(?!\n\}).)*?to:\s*" + re.escape(network) +
        r"(?:(?!\n\}).)*?command:\s*connect(?:(?!\n\}).)*?\n\}",
        text,
        re.S,
    )
    if match is None:
        raise SystemExit(f"missing CONNECT block for {network}")
    if match.start() > pass_offset:
        raise SystemExit(f"CONNECT block for {network} appears after public pass")
if "internal: 100.79.114.100 port = 1080" not in text:
    raise SystemExit("rendered listener is not the requested Tailnet address")
if text.index("internal.protocol: ipv4") > text.index("internal: 100.79.114.100 port = 1080"):
    raise SystemExit("internal protocol must precede the internal address for Dante 1.4.3")
if text.index("external.protocol: ipv4") > text.index("external: eth0"):
    raise SystemExit("external protocol must precede the external address for Dante 1.4.3")
PY

assert_contains "$TEST_ROOT/firewall.nft" 'delete table ip sub2api_fixed_egress'
assert_contains "$TEST_ROOT/firewall.nft" 'tcp dport 1080 ip daddr 100.79.114.100 ip saddr { 100.80.10.114/32, 172.18.0.0/16 } accept'
assert_contains "$TEST_ROOT/firewall.nft" 'tcp dport 1080 drop'
assert_contains "$TEST_ROOT/firewall.nft" 'udp dport 1080 drop'
assert_contains "$SCRIPT" 'internal: ${tailnet_ip} port = ${PORT}'
assert_contains "$SCRIPT" 'internal.protocol: ipv4'
assert_contains "$SCRIPT" 'external.protocol: ipv4'
assert_contains "$SCRIPT" 'command: bind udpassociate'
assert_contains "$SCRIPT" 'command: connect'
assert_contains "$SCRIPT" 'protocol: tcp'
assert_contains "$SCRIPT" 'to: 100.64.0.0/10'
assert_contains "$SCRIPT" 'to: 169.254.0.0/16'
assert_contains "$SCRIPT" 'tcp dport ${PORT} drop'
assert_contains "$SCRIPT" 'udp dport ${PORT} drop'
assert_contains "$SCRIPT" 'ss -H -lnt "sport = :${PORT}"'
assert_contains "$SCRIPT" 'ss -H -lnu "sport = :${PORT}"'
assert_contains "$SCRIPT" 'ProtectSystem=strict'
assert_contains "$SCRIPT" 'RestrictAddressFamilies=AF_UNIX AF_INET AF_NETLINK'
assert_contains "$SCRIPT" 'ExecStartPre=/usr/sbin/danted -V -f ${CONFIG_FILE}'
assert_contains "$SCRIPT" 'ExecStart=/usr/sbin/danted -f ${CONFIG_FILE}'
assert_contains "$SCRIPT" 'RuntimeDirectory=sub2api-fixed-egress'
assert_contains "$SCRIPT" 'StartLimitIntervalSec=0'
assert_contains "$SCRIPT" 'ExecStartPre=/usr/bin/timeout 120'
assert_contains "$SCRIPT" 'Restart=always'
assert_contains "$SCRIPT" 'status=installing'
assert_contains "$SCRIPT" 'restore_backup'
assert_contains "$SCRIPT" 'rollback BACKUP_DIR'
assert_contains "$SCRIPT" 'rollback_backup='
assert_not_contains "$SCRIPT" '0.0.0.0 port = ${PORT}'
assert_not_contains "$SCRIPT" 'danted -D'
assert_not_contains "$SCRIPT" 'ExecStartPre=-/usr/sbin/nft delete table ip sub2api_fixed_egress'

printf 'Fixed egress installer contract tests passed.\n'
