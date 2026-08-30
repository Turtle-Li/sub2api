#!/usr/bin/env bash

# Install one fail-closed SOCKS5 egress gateway for Sub2API accounts. The
# listener is bound to one Tailnet IPv4 address and permits TCP CONNECT only
# from explicitly listed Tailnet/Docker CIDRs. DNS is resolved on the gateway
# by clients using socks5h; private, metadata, multicast, and Tailnet targets
# are denied before the public-connect rule.

set -Eeuo pipefail
umask 077

# The application-side fixed-egress contract and CAS validation intentionally
# pin TCP 1080. Keeping the host installer fixed prevents a gateway that can be
# installed successfully but can never be represented by a compliant Proxy.
PORT=1080
CONFIG_FILE="${SUB2API_FIXED_EGRESS_CONFIG_FILE:-/etc/sub2api-fixed-egress.conf}"
NFT_FILE="${SUB2API_FIXED_EGRESS_NFT_FILE:-/etc/sub2api-fixed-egress.nft}"
SERVICE_FILE="${SUB2API_FIXED_EGRESS_SERVICE_FILE:-/etc/systemd/system/sub2api-fixed-egress.service}"
FIREWALL_SERVICE_FILE="${SUB2API_FIXED_EGRESS_FIREWALL_SERVICE_FILE:-/etc/systemd/system/sub2api-fixed-egress-firewall.service}"
STATE_DIR="${SUB2API_FIXED_EGRESS_STATE_DIR:-/var/lib/sub2api-fixed-egress}"
BACKUP_ROOT="${SUB2API_FIXED_EGRESS_BACKUP_ROOT:-/var/backups/sub2api-fixed-egress}"
CURRENT_BACKUP_DIR=""
INSTALL_COMPLETE=false
TEMP_FILES=()
VALIDATION_NFT_TABLE=""

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

validate_interface() {
  case "$1" in
    ''|*[!A-Za-z0-9_.:-]*) die "external interface contains unsupported characters" ;;
    lo|tailscale0) die "external interface must be the public egress interface" ;;
  esac
  [ -d "/sys/class/net/$1" ] || die "external interface does not exist: $1"
}

validate_network_values() {
  python3 - "$@" <<'PY'
import ipaddress
import sys

tailnet_ip = ipaddress.ip_address(sys.argv[1])
if tailnet_ip.version != 4 or tailnet_ip not in ipaddress.ip_network("100.64.0.0/10"):
    raise SystemExit("tailnet listener must be an IPv4 address inside 100.64.0.0/10")
for raw in sys.argv[2:]:
    network = ipaddress.ip_network(raw, strict=True)
    if network.version != 4:
        raise SystemExit(f"allowed client CIDR must be IPv4: {raw}")
    if network.prefixlen < 16:
        raise SystemExit(f"allowed client CIDR is broader than /16: {raw}")
PY
}

backup_file() {
  local source="$1" destination="$2"
  if [ -e "$source" ] || [ -L "$source" ]; then
    cp -a -- "$source" "$destination"
  else
    : >"${destination}.absent"
  fi
}

restore_file() {
  local backup_dir="$1" name="$2" destination="$3"
  if [ -e "${backup_dir}/${name}.absent" ]; then
    rm -f -- "$destination"
  elif [ -e "${backup_dir}/${name}" ] || [ -L "${backup_dir}/${name}" ]; then
    cp -a -- "${backup_dir}/${name}" "$destination"
  else
    die "rollback backup is missing ${name}"
  fi
}

restore_unit_state() {
  local backup_dir="$1" unit="$2" prefix="$3"
  if grep -qx enabled "${backup_dir}/${prefix}-enabled.txt" 2>/dev/null; then
    systemctl enable "$unit" >/dev/null 2>&1 || return 1
  else
    systemctl disable "$unit" >/dev/null 2>&1 || true
  fi
  if grep -qx active "${backup_dir}/${prefix}-active.txt" 2>/dev/null; then
    systemctl start "$unit" >/dev/null 2>&1 || return 1
  else
    systemctl stop "$unit" >/dev/null 2>&1 || true
  fi
}

restore_backup() {
  local backup_dir="$1"
  systemctl disable --now sub2api-fixed-egress.service >/dev/null 2>&1 || true
  systemctl disable --now sub2api-fixed-egress-firewall.service >/dev/null 2>&1 || true

  restore_file "$backup_dir" sub2api-fixed-egress.conf "$CONFIG_FILE"
  restore_file "$backup_dir" sub2api-fixed-egress.nft "$NFT_FILE"
  restore_file "$backup_dir" sub2api-fixed-egress.service "$SERVICE_FILE"
  restore_file "$backup_dir" sub2api-fixed-egress-firewall.service "$FIREWALL_SERVICE_FILE"
  restore_file "$backup_dir" install.env "${STATE_DIR}/install.env"
  systemctl daemon-reload

  nft delete table ip sub2api_fixed_egress >/dev/null 2>&1 || true
  if [ -f "${backup_dir}/nft-table.txt" ]; then
    nft -f "${backup_dir}/nft-table.txt" || return 1
  fi

  restore_unit_state "$backup_dir" danted.service danted || return 1
  restore_unit_state "$backup_dir" sub2api-fixed-egress-firewall.service firewall || return 1
  restore_unit_state "$backup_dir" sub2api-fixed-egress.service service || return 1
}

cleanup() {
  local status="$?" file
  trap - EXIT
  for file in "${TEMP_FILES[@]:-}"; do
    [ -n "$file" ] && rm -f -- "$file"
  done
  if [ -n "$VALIDATION_NFT_TABLE" ] && command -v nft >/dev/null 2>&1; then
    nft delete table ip "$VALIDATION_NFT_TABLE" >/dev/null 2>&1 || true
  fi
  if [ "$status" -ne 0 ] && [ -n "$CURRENT_BACKUP_DIR" ] && [ "$INSTALL_COMPLETE" != true ]; then
    printf 'Install failed; restoring rollback backup %s\n' "$CURRENT_BACKUP_DIR" >&2
    if ! restore_backup "$CURRENT_BACKUP_DIR"; then
      printf 'ERROR: automatic rollback was incomplete; backup=%s\n' "$CURRENT_BACKUP_DIR" >&2
    fi
  fi
  exit "$status"
}

render_dante_config() {
  local tailnet_ip="$1" external_interface="$2"
  shift 2
  local cidr

  cat <<EOF
logoutput: syslog
internal.protocol: ipv4
external.protocol: ipv4
internal: ${tailnet_ip} port = ${PORT}
external: ${external_interface}
clientmethod: none
socksmethod: none
user.notprivileged: nobody

EOF
  for cidr in "$@"; do
    cat <<EOF
client pass {
    from: ${cidr} to: 0.0.0.0/0
    log: error
}
EOF
  done
  cat <<'EOF'
client block {
    from: 0.0.0.0/0 to: 0.0.0.0/0
    log: connect error
}

socks block {
    from: 0.0.0.0/0 to: 0.0.0.0/0
    command: bind udpassociate
    log: connect error
}
socks block {
    from: 0.0.0.0/0 to: 0.0.0.0/8
    command: connect
    log: connect error
}
socks block {
    from: 0.0.0.0/0 to: 10.0.0.0/8
    command: connect
    log: connect error
}
socks block {
    from: 0.0.0.0/0 to: 100.64.0.0/10
    command: connect
    log: connect error
}
socks block {
    from: 0.0.0.0/0 to: 127.0.0.0/8
    command: connect
    log: connect error
}
socks block {
    from: 0.0.0.0/0 to: 169.254.0.0/16
    command: connect
    log: connect error
}
socks block {
    from: 0.0.0.0/0 to: 172.16.0.0/12
    command: connect
    log: connect error
}
socks block {
    from: 0.0.0.0/0 to: 192.168.0.0/16
    command: connect
    log: connect error
}
socks block {
    from: 0.0.0.0/0 to: 224.0.0.0/4
    command: connect
    log: connect error
}
socks block {
    from: 0.0.0.0/0 to: 240.0.0.0/4
    command: connect
    log: connect error
}
socks pass {
    from: 0.0.0.0/0 to: 0.0.0.0/0
    command: connect
    protocol: tcp
    log: error
}
socks block {
    from: 0.0.0.0/0 to: 0.0.0.0/0
    command: bind connect udpassociate
    log: connect error
}
EOF
}

render_nft_config() {
  local tailnet_ip="$1"
  shift
  local joined="" cidr
  for cidr in "$@"; do
    if [ -n "$joined" ]; then
      joined="${joined}, ${cidr}"
    else
      joined="$cidr"
    fi
  done
  cat <<EOF
delete table ip sub2api_fixed_egress
table ip sub2api_fixed_egress {
    chain input {
        type filter hook input priority -10; policy accept;
        tcp dport ${PORT} ip daddr ${tailnet_ip} ip saddr { ${joined} } accept
        tcp dport ${PORT} drop
        udp dport ${PORT} drop
    }
}
EOF
}

verify_runtime() {
  local tailnet_ip="$1" listen_lines="" udp_lines attempt
  for attempt in $(seq 1 30); do
    if systemctl is-active --quiet sub2api-fixed-egress-firewall.service \
      && systemctl is-active --quiet sub2api-fixed-egress.service; then
      listen_lines="$(ss -H -lnt "sport = :${PORT}" | awk '{print $4}')"
      udp_lines="$(ss -H -lnu "sport = :${PORT}" || true)"
      if [ "$listen_lines" = "${tailnet_ip}:${PORT}" ] \
        && [ -z "$udp_lines" ] \
        && nft list table ip sub2api_fixed_egress >/dev/null 2>&1; then
        return 0
      fi
    fi
    sleep 1
  done
  die "fixed-egress runtime verification failed: listener=${listen_lines:-missing}"
}

main() {
  trap cleanup EXIT
  if [ "${1:-}" = rollback ]; then
    [ "$#" -eq 2 ] || die "usage: $0 rollback BACKUP_DIR"
    [ "$(id -u)" -eq 0 ] || die "rollback requires root"
    for command_name in cp flock grep install nft realpath rm systemctl; do
      require_cmd "$command_name"
    done
    local requested_backup canonical_backup canonical_root
    requested_backup="$2"
    canonical_backup="$(realpath -e -- "$requested_backup")" || die "rollback backup does not exist"
    canonical_root="$(realpath -e -- "$BACKUP_ROOT")" || die "rollback root does not exist"
    case "$canonical_backup" in
      "$canonical_root"/*) ;;
      *) die "rollback backup is outside the bounded backup root" ;;
    esac
    install -d -m 700 "$STATE_DIR"
    exec 9>"${STATE_DIR}/install.lock"
    flock -w 30 -x 9 || die "timed out waiting for fixed-egress install lock"
    restore_backup "$canonical_backup" || die "fixed-egress rollback was incomplete"
    printf 'ROLLED_BACK backup=%s\n' "$canonical_backup"
    return
  fi
  [ "$#" -ge 3 ] || {
    printf 'Usage: %s TAILNET_IPV4 EXTERNAL_INTERFACE ALLOWED_CLIENT_CIDR...\n' "$0" >&2
    printf '       %s rollback BACKUP_DIR\n' "$0" >&2
    exit 2
  }
  [ "$(id -u)" -eq 0 ] || die "installation requires root"
  local tailnet_ip="$1" external_interface="$2"
  shift 2
  local clients=("$@") stamp backup_dir rendered_config rendered_nft

  for command_name in apt-get awk cp cut date flock grep install ip mktemp python3 realpath rm sed seq sleep ss systemctl timeout; do
    require_cmd "$command_name"
  done
  if ! command -v nft >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends nftables
  fi
  require_cmd nft
  case "$PORT" in
    ''|*[!0-9]*) die "port must be numeric" ;;
  esac
  [ "$PORT" -ge 1024 ] && [ "$PORT" -le 65535 ] || die "port must be between 1024 and 65535"
  validate_interface "$external_interface"
  validate_network_values "$tailnet_ip" "${clients[@]}"
  ip -4 -o addr show dev tailscale0 | awk '{print $4}' | cut -d/ -f1 | grep -qxF "$tailnet_ip" \
    || die "tailnet listener address is not assigned to tailscale0"

  install -d -m 700 "$STATE_DIR" "$BACKUP_ROOT"
  exec 9>"${STATE_DIR}/install.lock"
  flock -w 30 -x 9 || die "timed out waiting for fixed-egress install lock"

  stamp="$(date -u '+%Y%m%dT%H%M%SZ')"
  backup_dir="$(mktemp -d "${BACKUP_ROOT}/${stamp}.XXXXXX")"
  chmod 700 "$backup_dir"
  backup_file "$CONFIG_FILE" "$backup_dir/sub2api-fixed-egress.conf"
  backup_file "$NFT_FILE" "$backup_dir/sub2api-fixed-egress.nft"
  backup_file "$SERVICE_FILE" "$backup_dir/sub2api-fixed-egress.service"
  backup_file "$FIREWALL_SERVICE_FILE" "$backup_dir/sub2api-fixed-egress-firewall.service"
  backup_file "${STATE_DIR}/install.env" "$backup_dir/install.env"
  systemctl is-enabled sub2api-fixed-egress.service >"$backup_dir/service-enabled.txt" 2>&1 || true
  systemctl is-active sub2api-fixed-egress.service >"$backup_dir/service-active.txt" 2>&1 || true
  systemctl is-enabled sub2api-fixed-egress-firewall.service >"$backup_dir/firewall-enabled.txt" 2>&1 || true
  systemctl is-active sub2api-fixed-egress-firewall.service >"$backup_dir/firewall-active.txt" 2>&1 || true
  systemctl is-enabled danted.service >"$backup_dir/danted-enabled.txt" 2>&1 || true
  systemctl is-active danted.service >"$backup_dir/danted-active.txt" 2>&1 || true
  if ! nft list table ip sub2api_fixed_egress >"$backup_dir/nft-table.txt" 2>/dev/null; then
    rm -f -- "$backup_dir/nft-table.txt"
    : >"$backup_dir/nft-table.absent"
  fi
  CURRENT_BACKUP_DIR="$backup_dir"
  {
    printf 'status=installing\n'
    printf 'installed_at=%s\n' "$stamp"
    printf 'tailnet_ipv4=%s\n' "$tailnet_ip"
    printf 'external_interface=%s\n' "$external_interface"
    printf 'port=%s\n' "$PORT"
    printf 'allowed_clients=%s\n' "$(IFS=,; printf '%s' "${clients[*]}")"
    printf 'rollback_backup=%s\n' "$backup_dir"
  } >"${STATE_DIR}/install.env"
  chmod 600 "${STATE_DIR}/install.env"

  rendered_config="$(mktemp)"
  rendered_nft="$(mktemp)"
  TEMP_FILES+=("$rendered_config" "$rendered_nft")
  render_dante_config "$tailnet_ip" "$external_interface" "${clients[@]}" >"$rendered_config"
  render_nft_config "$tailnet_ip" "${clients[@]}" >"$rendered_nft"
  chmod 600 "$rendered_config" "$rendered_nft"
  VALIDATION_NFT_TABLE="sub2api_fixed_egress_validate_$$"
  nft add table ip "$VALIDATION_NFT_TABLE"
  sed "s/sub2api_fixed_egress/${VALIDATION_NFT_TABLE}/g" "$rendered_nft" | nft -c -f -
  nft delete table ip "$VALIDATION_NFT_TABLE"
  VALIDATION_NFT_TABLE=""

  install -m 600 "$rendered_nft" "$NFT_FILE"
  cat >"$FIREWALL_SERVICE_FILE" <<EOF
[Unit]
Description=Sub2API fixed egress port firewall
Before=sub2api-fixed-egress.service
After=network-pre.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStartPre=-/usr/sbin/nft add table ip sub2api_fixed_egress
ExecStart=/usr/sbin/nft -f ${NFT_FILE}
ExecReload=/usr/sbin/nft -f ${NFT_FILE}

[Install]
WantedBy=multi-user.target
EOF
  chmod 644 "$FIREWALL_SERVICE_FILE"
  systemctl daemon-reload
  systemctl enable --now sub2api-fixed-egress-firewall.service

  if [ ! -x /usr/sbin/danted ]; then
    DEBIAN_FRONTEND=noninteractive apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends dante-server
  fi
  systemctl disable --now danted.service >/dev/null 2>&1 || true
  # Package installation can leave the distro-managed unit in failed state
  # before our isolated service takes ownership. Keep it disabled, but clear
  # the stale failure so host health views do not report a false outage.
  systemctl reset-failed danted.service >/dev/null 2>&1 || true
  [ -x /usr/sbin/danted ] || die "danted was not installed at /usr/sbin/danted"

  install -m 644 "$rendered_config" "$CONFIG_FILE"
  cat >"$SERVICE_FILE" <<EOF
[Unit]
Description=Sub2API Tailnet-only fixed SOCKS5 egress
After=network-online.target tailscaled.service sub2api-fixed-egress-firewall.service
Requires=tailscaled.service sub2api-fixed-egress-firewall.service
StartLimitIntervalSec=0

[Service]
Type=simple
User=nobody
Group=nogroup
RuntimeDirectory=sub2api-fixed-egress
RuntimeDirectoryMode=0750
ExecStartPre=/usr/bin/timeout 120 /bin/sh -c 'until /usr/sbin/ip -4 -o addr show dev tailscale0 | /usr/bin/grep -qF " ${tailnet_ip}/"; do /usr/bin/sleep 1; done'
ExecStartPre=/usr/sbin/danted -V -f ${CONFIG_FILE}
ExecStart=/usr/sbin/danted -f ${CONFIG_FILE} -p /run/sub2api-fixed-egress/danted.pid
Restart=always
RestartSec=2s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_NETLINK
RestrictNamespaces=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
EOF
  chmod 644 "$SERVICE_FILE"
  systemctl daemon-reload
  systemctl enable --now sub2api-fixed-egress.service
  verify_runtime "$tailnet_ip"

  {
    printf 'status=installed\n'
    printf 'installed_at=%s\n' "$stamp"
    printf 'tailnet_ipv4=%s\n' "$tailnet_ip"
    printf 'external_interface=%s\n' "$external_interface"
    printf 'port=%s\n' "$PORT"
    printf 'allowed_clients=%s\n' "$(IFS=,; printf '%s' "${clients[*]}")"
    printf 'rollback_backup=%s\n' "$backup_dir"
  } >"${STATE_DIR}/install.env"
  chmod 600 "${STATE_DIR}/install.env"
  INSTALL_COMPLETE=true
  printf 'INSTALLED tailnet=%s port=%s backup=%s\n' "$tailnet_ip" "$PORT" "$backup_dir"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
