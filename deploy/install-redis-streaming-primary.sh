#!/usr/bin/env bash

# Production Redis streaming relay. This is online-only: it must never restart
# production Redis, the active application, or Caddy.
set -Eeuo pipefail

CONFIG_FILE="${SUB2API_AUTODEPLOY_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"
if [ -r "$CONFIG_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$CONFIG_FILE"
  set +a
fi

LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-/run/lock/sub2api-maintenance.lock}"
APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
REDIS_CONTAINER="${SUB2API_REDIS_CONTAINER:-sub2api-redis}"
RELAY_CONTAINER="${SUB2API_REDIS_RELAY_CONTAINER:-sub2api-redis-streaming-relay}"
RELAY_NETWORK="${SUB2API_REDIS_RELAY_NETWORK:-sub2api-redis-streaming}"
RELAY_SUBNET="${SUB2API_REDIS_RELAY_SUBNET:-172.30.241.0/29}"
REDIS_RELAY_IP="${SUB2API_REDIS_PRIMARY_RELAY_IP:-172.30.241.2}"
RELAY_IP="${SUB2API_REDIS_RELAY_IP:-172.30.241.3}"
RELAY_PORT="${SUB2API_REDIS_RELAY_PORT:-16380}"
REDIS_PORT="${SUB2API_REDIS_PORT:-6379}"
RELAY_IMAGE="${SUB2API_REDIS_RELAY_IMAGE:-nginx@sha256:a8b39bd9cf0f83869a2162827a0caf6137ddf759d50a171451b335cecc87d236}"
SOCKET_PROXY_BIN="${SUB2API_REDIS_SOCKET_PROXY_BIN:-/usr/lib/systemd/systemd-socket-proxyd}"
RELAY_SOCKET_UNIT=sub2api-redis-streaming-relay.socket
RELAY_PROXY_UNIT=sub2api-redis-streaming-relay.service
TUNNEL_USER="${SUB2API_REDIS_TUNNEL_USER:-sub2api-redis-tunnel}"
TUNNEL_HOME="${SUB2API_REDIS_TUNNEL_HOME:-/var/lib/sub2api-redis-tunnel}"
REPLICATION_USER="${SUB2API_REDIS_REPLICATION_USER:-sub2api_replication}"
REPL_BACKLOG_SIZE="${SUB2API_REDIS_REPL_BACKLOG_SIZE:-64mb}"

PUBLIC_KEY_FILE=""
SOURCE_AUTH_FILE=""
REPLICATION_AUTH_FILE=""
SOURCE_CIDR=""
LOCKED=false

usage() {
  cat <<'EOF'
Usage: install-redis-streaming-primary.sh \
  --public-key-file PATH --source-auth-file PATH \
  --replication-auth-file PATH --source-cidr IPv4/32

Creates an internal, loopback-only relay and a restricted SSH forwarding
account. Redis receives a dedicated replication ACL user and an online
repl-backlog-size setting. On command-line Redis, these source settings are
runtime-only and must be revalidated after a Redis restart. It never restarts
production Redis, app, or Caddy.
EOF
}

die() { echo "ERROR: $*" >&2; exit 1; }
require_cmd() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }
backlog_bytes() {
  python3 - "$1" <<'PY'
import re
import sys

match = re.fullmatch(r"([1-9][0-9]*)(kb|mb|gb)", sys.argv[1])
if not match:
    raise SystemExit(1)
amount, unit = match.groups()
print(int(amount) * {"kb": 1024, "mb": 1024 ** 2, "gb": 1024 ** 3}[unit])
PY
}

validate_secret_file() {
  local path="$1" mode
  [ -f "$path" ] || die "credential file must be a regular file: $path"
  [ "$(stat -c '%U' "$path")" = root ] || die "credential file must be owned by root: $path"
  mode="$(stat -c '%a' "$path")"
  [ "$((8#$mode & 077))" -eq 0 ] || die "credential file must not grant group/other access: $path"
  awk 'NR > 1 { exit 1 } !/^[[:xdigit:]]{48,}$/ { exit 1 } END { if (NR != 1) exit 1 }' "$path" \
    || die "credential file must contain one 48+ character hexadecimal value"
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --public-key-file) PUBLIC_KEY_FILE="${2:-}"; shift ;;
    --source-auth-file) SOURCE_AUTH_FILE="${2:-}"; shift ;;
    --replication-auth-file) REPLICATION_AUTH_FILE="${2:-}"; shift ;;
    --source-cidr) SOURCE_CIDR="${2:-}"; shift ;;
    --locked) LOCKED=true ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
  shift
done

if [ "$LOCKED" != true ]; then
  case "$LOCK_FILE" in /*) ;; *) die "maintenance lock path must be absolute" ;; esac
  command -v flock >/dev/null 2>&1 || die "flock is required"
  exec flock -w 30 "$LOCK_FILE" "$0" --locked \
    --public-key-file "$PUBLIC_KEY_FILE" --source-auth-file "$SOURCE_AUTH_FILE" \
    --replication-auth-file "$REPLICATION_AUTH_FILE" --source-cidr "$SOURCE_CIDR"
fi

[ "$(id -u)" -eq 0 ] || die "run as root on the production host"
[ -r "$PUBLIC_KEY_FILE" ] || die "public key file is not readable"
[ -n "$SOURCE_CIDR" ] || die "--source-cidr is required"
for command_name in awk cp docker flock getent grep id install mktemp nc python3 rm seq sha256sum ssh-keygen sshd ss stat systemctl systemd-analyze timeout useradd userdel usermod; do require_cmd "$command_name"; done
validate_secret_file "$SOURCE_AUTH_FILE"
validate_secret_file "$REPLICATION_AUTH_FILE"

python3 - "$SOURCE_CIDR" <<'PY' || die "source CIDR must be one exact IPv4 /32"
import ipaddress
import sys
network = ipaddress.ip_network(sys.argv[1], strict=True)
if network.version != 4 or network.prefixlen != 32:
    raise SystemExit(1)
PY

case "$TUNNEL_USER" in ''|-*|*[!a-zA-Z0-9_-]*) die "tunnel user contains unsupported characters" ;; esac
case "$REPLICATION_USER" in ''|-*|*[!a-zA-Z0-9_]*) die "replication user contains unsupported characters" ;; esac
for value in "$RELAY_PORT" "$REDIS_PORT"; do
  case "$value" in ''|*[!0-9]*) die "port must be numeric" ;; esac
  [ "$value" -ge 1 ] && [ "$value" -le 65535 ] || die "port is outside 1-65535"
done
[[ "$REPL_BACKLOG_SIZE" =~ ^[1-9][0-9]*(kb|mb|gb)$ ]] || die "backlog size must use positive kb, mb, or gb units"
EXPECTED_REPL_BACKLOG_BYTES="$(backlog_bytes "$REPL_BACKLOG_SIZE")" || die "backlog size could not be normalized"

key_line="$(awk 'NF && $1 !~ /^#/ { print; exit }' "$PUBLIC_KEY_FILE")"
printf '%s\n' "$key_line" | ssh-keygen -lf - >/dev/null || die "invalid SSH public key"
case "$key_line" in ssh-ed25519\ *) ;; *) die "only Ed25519 public keys are accepted" ;; esac

docker inspect "$REDIS_CONTAINER" >/dev/null 2>&1 || die "production Redis container is missing"
[ "$(docker inspect "$REDIS_CONTAINER" --format '{{.State.Status}}')" = running ] || die "production Redis is not running"
[ "$(docker inspect "$REDIS_CONTAINER" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}')" = healthy ] || die "production Redis is not healthy"
[ "$(docker inspect "$REDIS_CONTAINER" --format '{{json .HostConfig.PortBindings}}')" = '{}' ] || die "production Redis has a public host port binding; remove it before streaming"
[ -x "$SOCKET_PROXY_BIN" ] || die "systemd-socket-proxyd is missing"

caddy_targets="$(sed -nE 's/^[[:space:]]*reverse_proxy[[:space:]]+(sub2api-(blue|green)|sub2api):8080.*/\1/p' "$APP_DIR/Caddyfile" | sort -u)"
[ "$(printf '%s\n' "$caddy_targets" | awk 'NF { count++ } END { print count+0 }')" -eq 1 ] || die "Caddy does not resolve to exactly one application color"
active_container="$caddy_targets"
[ "$(docker inspect "$active_container" --format '{{.State.Status}}')" = running ] || die "active application is not running"
[ "$(docker inspect "$active_container" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}')" = healthy ] || die "active application is not healthy"

network_created=false
redis_connected=false
relay_started=false
relay_preexisting=false
account_created=false
ssh_state_saved=false
socket_state_saved=false
socket_was_active=false
socket_was_enabled=false
acl_created=false
old_backlog=""
state_backup_dir="$(mktemp -d)"
relay_config="$APP_DIR/redis-streaming/nginx.conf"
socket_unit_path="/etc/systemd/system/$RELAY_SOCKET_UNIT"
proxy_unit_path="/etc/systemd/system/$RELAY_PROXY_UNIT"
sshd_config="/etc/ssh/sshd_config.d/57-sub2api-redis-tunnel.conf"
authorized_keys_path="$TUNNEL_HOME/.ssh/authorized_keys"

backup_file() {
  local name="$1" path="$2"
  if [ -e "$path" ]; then cp -a "$path" "$state_backup_dir/$name"; else : >"$state_backup_dir/$name.absent"; fi
}
restore_file() {
  local name="$1" path="$2"
  if [ -e "$state_backup_dir/$name.absent" ]; then rm -f "$path"; else cp -a "$state_backup_dir/$name" "$path"; fi
}
restore_redis_online_state() {
  [ -n "$old_backlog" ] || return 0
  awk 'NR == 1 { print; exit }' "$SOURCE_AUTH_FILE" | docker exec -i "$REDIS_CONTAINER" sh -ceu '
    IFS= read -r source_auth
    REDISCLI_AUTH="$source_auth" redis-cli --no-auth-warning CONFIG SET repl-backlog-size "$1" >/dev/null
    if [ "$2" = true ]; then REDISCLI_AUTH="$source_auth" redis-cli --no-auth-warning ACL DELUSER "$3" >/dev/null; fi
  ' sh "$old_backlog" "$acl_created" "$REPLICATION_USER" >/dev/null 2>&1 || true
}
cleanup_on_error() {
  local status=$?
  trap - EXIT
  if [ "$status" -ne 0 ]; then
    restore_redis_online_state
    if [ "$ssh_state_saved" = true ]; then
      if [ "$account_created" = true ]; then userdel -r "$TUNNEL_USER" >/dev/null 2>&1 || true; else restore_file authorized_keys "$authorized_keys_path"; fi
      restore_file sshd_config "$sshd_config"
      sshd -t >/dev/null 2>&1 && systemctl reload ssh >/dev/null 2>&1 || true
    fi
    if [ "$socket_state_saved" = true ]; then
      systemctl stop "$RELAY_SOCKET_UNIT" "$RELAY_PROXY_UNIT" >/dev/null 2>&1 || true
      restore_file relay_socket "$socket_unit_path"; restore_file relay_proxy "$proxy_unit_path"; systemctl daemon-reload >/dev/null 2>&1 || true
      if [ "$socket_was_enabled" = true ]; then systemctl enable "$RELAY_SOCKET_UNIT" >/dev/null 2>&1 || true; else systemctl disable "$RELAY_SOCKET_UNIT" >/dev/null 2>&1 || true; fi
      [ "$socket_was_active" = false ] || systemctl start "$RELAY_SOCKET_UNIT" >/dev/null 2>&1 || true
    fi
    [ "$relay_started" = false ] || docker rm -f "$RELAY_CONTAINER" >/dev/null 2>&1 || true
    [ "$redis_connected" = false ] || docker network disconnect "$RELAY_NETWORK" "$REDIS_CONTAINER" >/dev/null 2>&1 || true
    [ "$network_created" = false ] || docker network rm "$RELAY_NETWORK" >/dev/null 2>&1 || true
    restore_file relay_config "$relay_config"
    if [ "$relay_preexisting" = true ]; then docker exec "$RELAY_CONTAINER" nginx -t >/dev/null 2>&1 && docker exec "$RELAY_CONTAINER" nginx -s reload >/dev/null 2>&1 || true; fi
  fi
  rm -rf "$state_backup_dir"
  exit "$status"
}
trap cleanup_on_error EXIT

backup_file relay_config "$relay_config"
if docker network inspect "$RELAY_NETWORK" >/dev/null 2>&1; then
  [ "$(docker network inspect "$RELAY_NETWORK" --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}')" = "$RELAY_SUBNET" ] || die "relay network has an unexpected subnet"
  [ "$(docker network inspect "$RELAY_NETWORK" --format '{{.Internal}}')" = true ] || die "relay network must be internal"
else
  docker network create --driver bridge --internal --subnet "$RELAY_SUBNET" "$RELAY_NETWORK" >/dev/null
  network_created=true
fi
current_redis_ip="$(docker inspect "$REDIS_CONTAINER" --format "{{with index .NetworkSettings.Networks \"$RELAY_NETWORK\"}}{{.IPAddress}}{{end}}")"
if [ -z "$current_redis_ip" ]; then
  docker network connect --ip "$REDIS_RELAY_IP" --alias redis-primary "$RELAY_NETWORK" "$REDIS_CONTAINER"
  redis_connected=true
elif [ "$current_redis_ip" != "$REDIS_RELAY_IP" ]; then
  die "production Redis has an unexpected relay address: $current_redis_ip"
fi

install -d -o root -g root -m 755 "$APP_DIR/redis-streaming"
relay_config_temp="$(mktemp)"
cat >"$relay_config_temp" <<EOF
worker_processes 1;
pid /tmp/nginx.pid;
error_log /dev/stderr notice;
events { worker_connections 256; }
stream {
    server {
        listen ${RELAY_PORT};
        proxy_connect_timeout 5s;
        proxy_timeout 1h;
        proxy_pass ${REDIS_RELAY_IP}:${REDIS_PORT};
    }
}
EOF
install -o root -g root -m 644 "$relay_config_temp" "$relay_config"; rm -f "$relay_config_temp"
docker run --rm --network none --read-only --tmpfs /tmp:rw,noexec,nosuid,size=1m --volume "$relay_config:/etc/nginx/nginx.conf:ro" "$RELAY_IMAGE" nginx -t >/dev/null

if docker inspect "$RELAY_CONTAINER" >/dev/null 2>&1; then
  relay_preexisting=true
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{index .Config.Labels "com.turtleroute.sub2api.role"}}')" = redis-streaming-relay ] || die "refusing to replace unmanaged relay container"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.Config.Image}}')" = "$RELAY_IMAGE" ] || die "relay image differs"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{len .HostConfig.PortBindings}}')" -eq 0 ] || die "relay must not publish host ports"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.HostConfig.NetworkMode}}')" = "$RELAY_NETWORK" ] || die "relay network differs"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{len .NetworkSettings.Networks}}')" -eq 1 ] || die "relay has extra networks"
  [ "$(docker inspect "$RELAY_CONTAINER" --format "{{with index .NetworkSettings.Networks \"$RELAY_NETWORK\"}}{{.IPAddress}}{{end}}")" = "$RELAY_IP" ] || die "relay address differs"
  relay_mounts="$(docker inspect "$RELAY_CONTAINER" --format '{{range .Mounts}}{{.Source}}|{{.Destination}}|{{.RW}}|{{.Type}}{{end}}')"
  [ "$relay_mounts" = "$relay_config|/etc/nginx/nginx.conf|false|bind" ] || die "relay configuration mount differs"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.HostConfig.ReadonlyRootfs}}')" = true ] || die "relay root filesystem is writable"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{json .HostConfig.CapDrop}}')" = '["ALL"]' ] || die "relay does not drop all capabilities"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{len .HostConfig.CapAdd}}')" -eq 0 ] || die "relay has added capabilities"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{json .HostConfig.SecurityOpt}}')" = '["no-new-privileges"]' ] || die "relay is missing no-new-privileges"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.HostConfig.Privileged}}')" = false ] || die "relay is privileged"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.Config.User}}')" = '101:101' ] || die "relay uses an unexpected user"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.HostConfig.RestartPolicy.Name}}')" = unless-stopped ] || die "relay uses an unexpected restart policy"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{index .HostConfig.Tmpfs "/tmp"}}')" = 'rw,noexec,nosuid,size=1m' ] || die "relay tmpfs differs"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.HostConfig.Memory}}')" -eq 67108864 ] || die "relay memory limit differs"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.HostConfig.NanoCpus}}')" -eq 250000000 ] || die "relay CPU limit differs"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.State.Status}}')" = running ] || docker start "$RELAY_CONTAINER" >/dev/null
  docker exec "$RELAY_CONTAINER" nginx -t >/dev/null; docker exec "$RELAY_CONTAINER" nginx -s reload >/dev/null
else
  docker run -d --name "$RELAY_CONTAINER" --restart unless-stopped --network "$RELAY_NETWORK" --ip "$RELAY_IP" \
    --read-only --tmpfs /tmp:rw,noexec,nosuid,size=1m --cap-drop ALL --security-opt no-new-privileges --user 101:101 \
    --memory 64m --cpus 0.25 --label com.turtleroute.sub2api.role=redis-streaming-relay \
    --volume "$relay_config:/etc/nginx/nginx.conf:ro" "$RELAY_IMAGE" >/dev/null
  relay_started=true
fi
[ "$(docker inspect "$RELAY_CONTAINER" --format '{{.State.Status}}')" = running ] || die "relay did not start"

backup_file relay_socket "$socket_unit_path"; backup_file relay_proxy "$proxy_unit_path"
systemctl is-active --quiet "$RELAY_SOCKET_UNIT" 2>/dev/null && socket_was_active=true
systemctl is-enabled --quiet "$RELAY_SOCKET_UNIT" 2>/dev/null && socket_was_enabled=true
socket_state_saved=true
socket_temp="$(mktemp)"
cat >"$socket_temp" <<EOF
[Unit]
Description=Sub2API loopback socket for Redis streaming relay
[Socket]
ListenStream=127.0.0.1:${RELAY_PORT}
NoDelay=true
[Install]
WantedBy=sockets.target
EOF
install -o root -g root -m 644 "$socket_temp" "$socket_unit_path"; rm -f "$socket_temp"
proxy_temp="$(mktemp)"
cat >"$proxy_temp" <<EOF
[Unit]
Description=Sub2API loopback proxy to the isolated Redis streaming relay
Requires=${RELAY_SOCKET_UNIT}
After=${RELAY_SOCKET_UNIT} docker.service
[Service]
ExecStart=${SOCKET_PROXY_BIN} ${RELAY_IP}:${RELAY_PORT}
DynamicUser=true
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
LockPersonality=true
MemoryDenyWriteExecute=true
CapabilityBoundingSet=
AmbientCapabilities=
EOF
install -o root -g root -m 644 "$proxy_temp" "$proxy_unit_path"; rm -f "$proxy_temp"
systemd-analyze verify "$socket_unit_path" "$proxy_unit_path" >/dev/null
systemctl daemon-reload; systemctl stop "$RELAY_PROXY_UNIT" >/dev/null 2>&1 || true; systemctl restart "$RELAY_SOCKET_UNIT"; systemctl enable "$RELAY_SOCKET_UNIT" >/dev/null
ss -lntH | awk '{print $4}' | grep -Fxq "127.0.0.1:${RELAY_PORT}" || die "loopback relay socket did not bind"
timeout 5 nc -z 127.0.0.1 "$RELAY_PORT" || die "loopback relay proxy did not reach Redis"

backup_file authorized_keys "$authorized_keys_path"; backup_file sshd_config "$sshd_config"; ssh_state_saved=true
if id "$TUNNEL_USER" >/dev/null 2>&1; then
  [ "$(getent passwd "$TUNNEL_USER" | awk -F: '{print $6}')" = "$TUNNEL_HOME" ] || die "tunnel user has unexpected home"
else
  useradd --system --create-home --home-dir "$TUNNEL_HOME" --shell /usr/sbin/nologin --user-group "$TUNNEL_USER"; account_created=true
fi
usermod -L -s /usr/sbin/nologin "$TUNNEL_USER"; install -d -o "$TUNNEL_USER" -g "$TUNNEL_USER" -m 700 "$TUNNEL_HOME/.ssh"
authorized_keys_temp="$(mktemp)"
printf 'from="%s",restrict,port-forwarding,permitopen="127.0.0.1:%s" %s\n' "$SOURCE_CIDR" "$RELAY_PORT" "$key_line" >"$authorized_keys_temp"
install -o "$TUNNEL_USER" -g "$TUNNEL_USER" -m 600 "$authorized_keys_temp" "$authorized_keys_path"; rm -f "$authorized_keys_temp"
sshd_config_temp="$(mktemp)"
cat >"$sshd_config_temp" <<EOF
Match User ${TUNNEL_USER}
    AuthenticationMethods publickey
    PasswordAuthentication no
    KbdInteractiveAuthentication no
    AllowAgentForwarding no
    AllowTcpForwarding local
    X11Forwarding no
    PermitTTY no
    PermitTunnel no
    GatewayPorts no
    PermitOpen 127.0.0.1:${RELAY_PORT}
    MaxSessions 0
EOF
install -o root -g root -m 644 "$sshd_config_temp" "$sshd_config"; rm -f "$sshd_config_temp"
sshd -t || die "generated SSH forwarding policy did not validate"; systemctl reload ssh

old_backlog="$(awk 'NR == 1 { print; exit }' "$SOURCE_AUTH_FILE" | docker exec -i "$REDIS_CONTAINER" sh -ceu '
  IFS= read -r source_auth
  REDISCLI_AUTH="$source_auth" redis-cli --no-auth-warning CONFIG GET repl-backlog-size | sed -n "2p"
')"
case "$old_backlog" in ''|*[!0-9]*) die "Redis did not return a numeric repl-backlog-size" ;; esac
acl_marker_file="$state_backup_dir/acl-transaction.marker"
acl_transaction_status=0
if {
  awk 'NR == 1 { print; exit }' "$SOURCE_AUTH_FILE"
  awk 'NR == 1 { print; exit }' "$REPLICATION_AUTH_FILE"
} | docker exec -i "$REDIS_CONTAINER" sh -ceu '
  replication_user="$1"; backlog_size="$2"
  IFS= read -r source_auth; IFS= read -r replication_auth
  redis_cli() { REDISCLI_AUTH="$source_auth" redis-cli --no-auth-warning "$@"; }
  resp() {
    printf "*%s\r\n" "$#"
    for argument; do printf "$%s\r\n%s\r\n" "${#argument}" "$argument"; done
  }
  acl_section() {
    awk -v wanted="$1" "
      \$0 == wanted { in_section=1; next }
      \$0 ~ /^(flags|passwords|commands|keys|channels|selectors)\$/ {
        if (in_section) exit
        next
      }
      in_section { print }
    "
  }
  validate_replication_user() {
    acl_info="$(redis_cli --raw ACL GETUSER "$replication_user")"
    [ -n "$acl_info" ] || return 1
    flags="$(printf "%s\n" "$acl_info" | acl_section flags)"
    flag_count=0
    have_on=false
    have_sanitize=false
    for flag in $flags; do
      flag_count=$((flag_count + 1))
      case "$flag" in
        on) [ "$have_on" = false ] || return 1; have_on=true ;;
        sanitize-payload) [ "$have_sanitize" = false ] || return 1; have_sanitize=true ;;
        *) return 1 ;;
      esac
    done
    [ "$flag_count" -eq 2 ] && [ "$have_on" = true ] && [ "$have_sanitize" = true ] || return 1
    expected_password_hash="$(printf "%s" "$replication_auth" | sha256sum | awk "{print \$1}")"
    passwords="$(printf "%s\n" "$acl_info" | acl_section passwords)"
    password_count=0
    while IFS= read -r password_hash; do
      [ -n "$password_hash" ] || continue
      [ "$password_hash" = "$expected_password_hash" ] || return 1
      password_count=$((password_count + 1))
    done <<EOF
$passwords
EOF
    [ "$password_count" -eq 1 ] || return 1
    commands="$(printf "%s\n" "$acl_info" | acl_section commands)"
    token_count=0
    have_disable=false
    have_ping=false
    have_replconf=false
    have_psync=false
    for token in $commands; do
      token_count=$((token_count + 1))
      case "$token" in
        -@all) [ "$have_disable" = false ] || return 1; have_disable=true ;;
        +ping) [ "$have_ping" = false ] || return 1; have_ping=true ;;
        +replconf) [ "$have_replconf" = false ] || return 1; have_replconf=true ;;
        +psync) [ "$have_psync" = false ] || return 1; have_psync=true ;;
        *) return 1 ;;
      esac
    done
    [ "$token_count" -eq 4 ] && [ "$have_disable" = true ] && [ "$have_ping" = true ] \
      && [ "$have_replconf" = true ] && [ "$have_psync" = true ] || return 1
    [ -z "$(printf "%s\n" "$acl_info" | acl_section keys)" ] || return 1
    [ -z "$(printf "%s\n" "$acl_info" | acl_section channels)" ] || return 1
    [ -z "$(printf "%s\n" "$acl_info" | acl_section selectors)" ] || return 1
    replication_ping="$(REDISCLI_AUTH="$replication_auth" redis-cli --no-auth-warning --user "$replication_user" PING 2>/dev/null || true)"
    [ "$replication_ping" = PONG ]
  }
  if redis_cli ACL USERS | grep -Fxq "$replication_user"; then
    validate_replication_user || {
      echo "pre-existing replication ACL user does not match the required least-privilege credential contract" >&2
      exit 1
    }
  else
    resp ACL SETUSER "$replication_user" reset on ">$replication_auth" resetkeys resetchannels -@all +ping +replconf +psync |
      REDISCLI_AUTH="$source_auth" redis-cli --no-auth-warning --pipe >/dev/null
    printf "%s\n" ACL_CREATED
    validate_replication_user || {
      echo "new replication ACL user did not validate" >&2
      exit 1
    }
  fi
  redis_cli CONFIG SET repl-backlog-size "$backlog_size" >/dev/null
  echo "NOTICE: production Redis has no writable redis.conf; source ACL and backlog are runtime-only until Redis restarts" >&2
' sh "$REPLICATION_USER" "$REPL_BACKLOG_SIZE" >"$acl_marker_file"; then
  :
else
  acl_transaction_status=1
fi
if grep -Fxq ACL_CREATED "$acl_marker_file"; then acl_created=true; fi
[ "$acl_transaction_status" -eq 0 ] || die "replication ACL/backlog transaction failed"
loaded_backlog="$(awk 'NR == 1 { print; exit }' "$SOURCE_AUTH_FILE" | docker exec -i "$REDIS_CONTAINER" sh -ceu '
  IFS= read -r source_auth
  REDISCLI_AUTH="$source_auth" redis-cli --no-auth-warning CONFIG GET repl-backlog-size | sed -n "2p"
')"
[ "$loaded_backlog" = "$EXPECTED_REPL_BACKLOG_BYTES" ] || die "Redis backlog setting did not load as expected byte value"

old_backlog=""; acl_created=false; relay_started=false; redis_connected=false; network_created=false
rm -rf "$state_backup_dir"; trap - EXIT
echo "Installed production Redis streaming prerequisites."
echo "active_app=${active_container} relay=127.0.0.1:${RELAY_PORT} source=${SOURCE_CIDR} backlog_bytes=${loaded_backlog} persistence=runtime-only revalidate_before_cutover=true"
