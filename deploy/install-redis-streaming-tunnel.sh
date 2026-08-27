#!/usr/bin/env bash

# Install the Tokyo-side local forward. It listens only on the gateway of the
# candidate's internal Docker network; no host-public Redis listener is created.
set -Eeuo pipefail

ORIGINAL_ARGS=("$@")
LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-/run/lock/sub2api-maintenance.lock}"
LOCKED=false
TUNNEL_USER="${SUB2API_REDIS_TUNNEL_USER:-sub2api-redis-tunnel}"
TUNNEL_HOME="${SUB2API_REDIS_TUNNEL_HOME:-/var/lib/sub2api-redis-tunnel}"
CONFIG_DIR="${SUB2API_REDIS_TUNNEL_CONFIG_DIR:-/etc/sub2api-redis-tunnel}"
CONFIG_FILE="${SUB2API_REDIS_TUNNEL_CONFIG:-/etc/sub2api-redis-tunnel.conf}"
SERVICE_NAME=sub2api-redis-streaming-tunnel.service
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SSH_HOST=""
SSH_PORT=""
SSH_USER=sub2api-redis-tunnel
KEY_FILE=""
KNOWN_HOSTS_FILE=""
LOCAL_BIND=""
LOCAL_PORT="${SUB2API_REDIS_RELAY_PORT:-16380}"
REMOTE_PORT="${SUB2API_REDIS_RELAY_PORT:-16380}"
REMOTE_HOST=127.0.0.1
PROBE_NETWORK="${SUB2API_REDIS_PROBE_NETWORK:-sub2api-candidate-internal}"
PROBE_IMAGE="${SUB2API_REDIS_PROBE_IMAGE:-redis@sha256:becdda6c7f4b3fb42e42fd7f120bbf5c54c4caaaf16f26da24e4563d2c1f0576}"

usage() {
  cat <<'EOF'
Usage: install-redis-streaming-tunnel.sh \
  --ssh-host IPv4 --ssh-port PORT --key-file PATH \
  --known-hosts-file PATH --local-bind CANDIDATE_DOCKER_GATEWAY

Installs a strict-host-key SSH forward from the candidate's internal Docker
gateway to the production loopback Redis relay. The installer adds one exact
UFW allow rule for that internal network.
EOF
}
die() { echo "ERROR: $*" >&2; exit 1; }
require_cmd() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }

while [ "$#" -gt 0 ]; do
  case "$1" in
    --ssh-host) SSH_HOST="${2:-}"; shift ;;
    --ssh-port) SSH_PORT="${2:-}"; shift ;;
    --ssh-user) SSH_USER="${2:-}"; shift ;;
    --key-file) KEY_FILE="${2:-}"; shift ;;
    --known-hosts-file) KNOWN_HOSTS_FILE="${2:-}"; shift ;;
    --local-bind) LOCAL_BIND="${2:-}"; shift ;;
    --local-port) LOCAL_PORT="${2:-}"; shift ;;
    --remote-port) REMOTE_PORT="${2:-}"; shift ;;
    --locked) LOCKED=true ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
  shift
done

if [ "$LOCKED" != true ]; then
  case "$LOCK_FILE" in /*) ;; *) die "maintenance lock path must be absolute" ;; esac
  command -v flock >/dev/null 2>&1 || die "flock is required"
  exec flock -w 30 "$LOCK_FILE" "$0" --locked "${ORIGINAL_ARGS[@]}"
fi

[ "$(id -u)" -eq 0 ] || die "run as root on the Tokyo candidate host"
for command_name in awk cp cut docker getent grep id install ip mktemp python3 seq ss ssh-keygen systemctl systemd-analyze ufw useradd usermod; do require_cmd "$command_name"; done
[ -n "$SSH_HOST" ] || die "--ssh-host is required"
[ -n "$SSH_PORT" ] || die "--ssh-port is required"
[ -r "$KEY_FILE" ] || die "--key-file is unreadable"
[ -r "$KNOWN_HOSTS_FILE" ] || die "--known-hosts-file is unreadable"
[ -n "$LOCAL_BIND" ] || die "--local-bind is required"
python3 - "$SSH_HOST" "$LOCAL_BIND" <<'PY' || die "SSH host and local bind must be non-wildcard IPv4 addresses"
import ipaddress
import sys
for raw in sys.argv[1:]:
    address = ipaddress.ip_address(raw)
    if address.version != 4 or address.is_unspecified:
        raise SystemExit(1)
PY
for value in "$SSH_PORT" "$LOCAL_PORT" "$REMOTE_PORT"; do
  case "$value" in ''|*[!0-9]*) die "ports must be numeric" ;; esac
  [ "$value" -ge 1 ] && [ "$value" -le 65535 ] || die "port is outside 1-65535"
done
case "$TUNNEL_USER" in ''|-*|*[!a-zA-Z0-9_-]*) die "tunnel user contains unsupported characters" ;; esac
case "$SSH_USER" in ''|-*|*[!a-zA-Z0-9_-]*) die "SSH user contains unsupported characters" ;; esac
ssh-keygen -y -f "$KEY_FILE" >/dev/null || die "private key did not validate"
ssh-keygen -lf "$KNOWN_HOSTS_FILE" >/dev/null || die "known_hosts did not validate"
[ -x "$SCRIPT_DIR/sub2api-redis-streaming-tunnel.sh" ] || die "tunnel runner is missing"
[ -r "$SCRIPT_DIR/sub2api-redis-streaming-tunnel.service" ] || die "tunnel service unit is missing"
docker image inspect "$PROBE_IMAGE" >/dev/null 2>&1 || die "pinned Redis probe image is not present"
docker network inspect "$PROBE_NETWORK" >/dev/null 2>&1 || die "candidate internal Docker network is missing"
[ "$(docker network inspect "$PROBE_NETWORK" --format '{{.Internal}}')" = true ] || die "candidate probe network must be internal"

probe_gateway="$(docker network inspect "$PROBE_NETWORK" --format '{{range .IPAM.Config}}{{.Gateway}}{{end}}')"
[ "$LOCAL_BIND" = "$probe_gateway" ] || die "local bind must equal the candidate network gateway: $probe_gateway"
probe_subnet="$(docker network inspect "$PROBE_NETWORK" --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}')"
probe_network_id="$(docker network inspect "$PROBE_NETWORK" --format '{{.Id}}')"
probe_bridge="$(docker network inspect "$PROBE_NETWORK" --format '{{index .Options "com.docker.network.bridge.name"}}')"
[ -n "$probe_bridge" ] || probe_bridge="br-${probe_network_id:0:12}"
ip link show "$probe_bridge" >/dev/null 2>&1 || die "candidate probe bridge is missing"
ufw status | grep -Fq 'Status: active' || die "UFW must be active"

runner_target=/usr/local/libexec/sub2api-redis-streaming-tunnel.sh
unit_target="/etc/systemd/system/$SERVICE_NAME"
key_target="$CONFIG_DIR/id_ed25519"
known_hosts_target="$CONFIG_DIR/known_hosts"
backup_dir="$(mktemp -d)"
was_active=false
was_enabled=false
firewall_rule_added=false
systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null && was_active=true
systemctl is-enabled --quiet "$SERVICE_NAME" 2>/dev/null && was_enabled=true

backup_file() {
  local name="$1" path="$2"
  if [ -e "$path" ]; then cp -a "$path" "$backup_dir/$name"; else : >"$backup_dir/$name.absent"; fi
}
restore_file() {
  local name="$1" path="$2"
  if [ -e "$backup_dir/$name.absent" ]; then rm -f "$path"; else cp -a "$backup_dir/$name" "$path"; fi
}
backup_file key "$key_target"; backup_file known_hosts "$known_hosts_target"; backup_file config "$CONFIG_FILE"; backup_file runner "$runner_target"; backup_file unit "$unit_target"
rollback_armed=true
cleanup() {
  local status=$?
  trap - EXIT
  if [ "$status" -ne 0 ] && [ "$rollback_armed" = true ]; then
    systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true
    restore_file key "$key_target"; restore_file known_hosts "$known_hosts_target"; restore_file config "$CONFIG_FILE"; restore_file runner "$runner_target"; restore_file unit "$unit_target"
    systemctl daemon-reload >/dev/null 2>&1 || true
    if [ "$was_enabled" = true ]; then systemctl enable "$SERVICE_NAME" >/dev/null 2>&1 || true; else systemctl disable "$SERVICE_NAME" >/dev/null 2>&1 || true; fi
    [ "$was_active" = false ] || systemctl start "$SERVICE_NAME" >/dev/null 2>&1 || true
    if [ "$firewall_rule_added" = true ]; then ufw --force delete allow in on "$probe_bridge" from "$probe_subnet" to "$probe_gateway" port "$LOCAL_PORT" proto tcp >/dev/null 2>&1 || true; fi
  fi
  rm -rf "$backup_dir"
  exit "$status"
}
trap cleanup EXIT

firewall_rule="ufw allow in on $probe_bridge from $probe_subnet to $probe_gateway port $LOCAL_PORT proto tcp"
if ! ufw show added | grep -Fq "$firewall_rule"; then
  ufw allow in on "$probe_bridge" from "$probe_subnet" to "$probe_gateway" port "$LOCAL_PORT" proto tcp comment 'Sub2API internal Redis streaming tunnel' >/dev/null
  firewall_rule_added=true
fi

if id "$TUNNEL_USER" >/dev/null 2>&1; then
  [ "$(getent passwd "$TUNNEL_USER" | awk -F: '{print $6}')" = "$TUNNEL_HOME" ] || die "existing tunnel service account has unexpected home"
else
  useradd --system --create-home --home-dir "$TUNNEL_HOME" --shell /usr/sbin/nologin --user-group "$TUNNEL_USER"
fi
usermod -L -s /usr/sbin/nologin "$TUNNEL_USER"
install -d -o "$TUNNEL_USER" -g "$TUNNEL_USER" -m 700 "$CONFIG_DIR"
install -o "$TUNNEL_USER" -g "$TUNNEL_USER" -m 600 "$KEY_FILE" "$key_target"
install -o "$TUNNEL_USER" -g "$TUNNEL_USER" -m 600 "$KNOWN_HOSTS_FILE" "$known_hosts_target"

config_temp="$(mktemp)"
cat >"$config_temp" <<EOF
SSH_HOST=${SSH_HOST}
SSH_PORT=${SSH_PORT}
SSH_USER=${SSH_USER}
SSH_KEY=${CONFIG_DIR}/id_ed25519
KNOWN_HOSTS=${CONFIG_DIR}/known_hosts
LOCAL_BIND=${LOCAL_BIND}
LOCAL_PORT=${LOCAL_PORT}
REMOTE_HOST=${REMOTE_HOST}
REMOTE_PORT=${REMOTE_PORT}
EOF
install -o root -g "$TUNNEL_USER" -m 640 "$config_temp" "$CONFIG_FILE"; rm -f "$config_temp"
install -d -o root -g root -m 755 /usr/local/libexec
install -o root -g root -m 755 "$SCRIPT_DIR/sub2api-redis-streaming-tunnel.sh" "$runner_target"
install -o root -g root -m 644 "$SCRIPT_DIR/sub2api-redis-streaming-tunnel.service" "$unit_target"
systemd-analyze verify "$unit_target" >/dev/null
systemctl daemon-reload; systemctl enable "$SERVICE_NAME" >/dev/null; systemctl restart "$SERVICE_NAME"

for _ in $(seq 1 20); do
  if systemctl is-active --quiet "$SERVICE_NAME" && ss -lntH | awk '{print $4}' | grep -Fxq "${LOCAL_BIND}:${LOCAL_PORT}"; then
    probe_output="$(docker run --rm --network "$PROBE_NETWORK" --read-only --tmpfs /tmp:rw,noexec,nosuid,size=1m "$PROBE_IMAGE" redis-cli --raw -h "$LOCAL_BIND" -p "$LOCAL_PORT" PING 2>&1 || true)"
    case "$probe_output" in PONG|NOAUTH*) rollback_armed=false; firewall_rule_added=false; rm -rf "$backup_dir"; trap - EXIT; echo "Installed Tokyo Redis streaming tunnel on ${LOCAL_BIND}:${LOCAL_PORT}."; exit 0 ;; esac
  fi
  sleep 1
done
systemctl status "$SERVICE_NAME" --no-pager >&2 || true
die "tunnel did not become ready"
