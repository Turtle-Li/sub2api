#!/usr/bin/env bash

# Install the production-side endpoint and identities required by the Tokyo
# PostgreSQL physical standby. This is an online operation: it must not restart
# the production application, PostgreSQL, Redis, or Caddy.

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
POSTGRES_CONTAINER="${SUB2API_POSTGRES_CONTAINER:-sub2api-postgres}"
RELAY_CONTAINER="${SUB2API_PG_RELAY_CONTAINER:-sub2api-pg-streaming-relay}"
RELAY_NETWORK="${SUB2API_PG_RELAY_NETWORK:-sub2api-pg-streaming}"
RELAY_SUBNET="${SUB2API_PG_RELAY_SUBNET:-172.30.240.0/29}"
POSTGRES_RELAY_IP="${SUB2API_PG_PRIMARY_RELAY_IP:-172.30.240.2}"
RELAY_IP="${SUB2API_PG_RELAY_IP:-172.30.240.3}"
RELAY_PORT="${SUB2API_PG_RELAY_PORT:-15432}"
RELAY_IMAGE="${SUB2API_PG_RELAY_IMAGE:-nginx@sha256:a8b39bd9cf0f83869a2162827a0caf6137ddf759d50a171451b335cecc87d236}"
RELAY_SOCKET_UNIT="sub2api-pg-streaming-relay.socket"
RELAY_PROXY_UNIT="sub2api-pg-streaming-relay.service"
SOCKET_PROXY_BIN="${SUB2API_PG_SOCKET_PROXY_BIN:-/usr/lib/systemd/systemd-socket-proxyd}"
TUNNEL_USER="${SUB2API_PG_TUNNEL_USER:-sub2api-pg-tunnel}"
TUNNEL_HOME="${SUB2API_PG_TUNNEL_HOME:-/var/lib/sub2api-pg-tunnel}"
REPLICATION_USER="${SUB2API_PG_REPLICATION_USER:-sub2api_streaming}"
SLOT_WAL_CEILING="${SUB2API_PG_SLOT_WAL_CEILING:-4GB}"
PUBLIC_KEY_FILE=""
PASSWORD_FILE=""
SOURCE_CIDR=""
LOCKED=false

usage() {
  cat <<'EOF'
Usage: install-postgres-streaming-primary.sh \
  --public-key-file PATH --password-file PATH --source-cidr CIDR

Installs a loopback-only PostgreSQL relay, a source-restricted SSH forwarding
account, the least-privileged replication role, an exact pg_hba rule, and a
bounded replication-slot WAL ceiling. PostgreSQL configuration is reloaded;
no production container is restarted.
EOF
}

die() {
  echo "ERROR: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --public-key-file) PUBLIC_KEY_FILE="${2:-}"; shift ;;
    --password-file) PASSWORD_FILE="${2:-}"; shift ;;
    --source-cidr) SOURCE_CIDR="${2:-}"; shift ;;
    --locked) LOCKED=true ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
  shift
done

if [ "$LOCKED" != true ]; then
  case "$LOCK_FILE" in /*) ;; *) die "maintenance lock path must be absolute" ;; esac
  exec flock -w 30 "$LOCK_FILE" "$0" --locked \
    --public-key-file "$PUBLIC_KEY_FILE" \
    --password-file "$PASSWORD_FILE" \
    --source-cidr "$SOURCE_CIDR"
fi

[ "$(id -u)" -eq 0 ] || die "run as root on the production host"
[ -r "$PUBLIC_KEY_FILE" ] || die "public key file is not readable"
[ -r "$PASSWORD_FILE" ] || die "password file is not readable"
[ -n "$SOURCE_CIDR" ] || die "--source-cidr is required"

for command_name in awk cp docker flock getent id install ip mktemp nc python3 rm seq ssh-keygen sshd ss systemctl systemd-analyze timeout useradd userdel usermod; do
  require_cmd "$command_name"
done

python3 - "$SOURCE_CIDR" <<'PY' || die "source CIDR must be one exact IPv4 /32 or IPv6 /128"
import ipaddress
import sys

network = ipaddress.ip_network(sys.argv[1], strict=True)
if network.num_addresses != 1:
    raise SystemExit(1)
PY

case "$TUNNEL_USER" in
  ''|-*|*[!a-zA-Z0-9_-]*) die "tunnel user contains unsupported characters" ;;
esac
case "$REPLICATION_USER" in
  ''|-*|*[!a-zA-Z0-9_]*) die "replication role contains unsupported characters" ;;
esac
case "$RELAY_PORT" in
  ''|*[!0-9]*) die "relay port must be numeric" ;;
esac
[ "$RELAY_PORT" -ge 1024 ] && [ "$RELAY_PORT" -le 65535 ] || die "relay port is outside 1024-65535"
[[ "$SLOT_WAL_CEILING" =~ ^[1-9][0-9]*(MB|GB)$ ]] \
  || die "slot WAL ceiling must use positive MB or GB units"

key_line="$(awk 'NF && $1 !~ /^#/ { print; exit }' "$PUBLIC_KEY_FILE")"
printf '%s\n' "$key_line" | ssh-keygen -lf - >/dev/null || die "invalid SSH public key"
case "$key_line" in
  ssh-ed25519\ *) ;;
  *) die "only Ed25519 public keys are accepted" ;;
esac

password="$(tr -d '\r\n' <"$PASSWORD_FILE")"
case "$password" in
  '') die "replication password is empty" ;;
  *[!a-fA-F0-9]*) die "replication password must be hexadecimal" ;;
esac
[ "${#password}" -ge 48 ] || die "replication password must be at least 48 hexadecimal characters"

docker inspect "$POSTGRES_CONTAINER" >/dev/null 2>&1 || die "production PostgreSQL container is missing"
[ "$(docker inspect "$POSTGRES_CONTAINER" --format '{{.State.Status}}')" = running ] \
  || die "production PostgreSQL is not running"
postgres_health="$(docker inspect "$POSTGRES_CONTAINER" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}')"
[ "$postgres_health" = healthy ] || die "production PostgreSQL is not healthy"
[ -x "$SOCKET_PROXY_BIN" ] || die "systemd-socket-proxyd is missing"

caddy_targets="$(sed -nE 's/^[[:space:]]*reverse_proxy[[:space:]]+(sub2api-(blue|green)|sub2api):8080.*/\1/p' "$APP_DIR/Caddyfile" | sort -u)"
[ "$(printf '%s\n' "$caddy_targets" | awk 'NF { count++ } END { print count+0 }')" -eq 1 ] \
  || die "Caddy does not resolve to exactly one application color"
active_container="$caddy_targets"
[ "$(docker inspect "$active_container" --format '{{.State.Status}}')" = running ] \
  || die "active application is not running"
[ "$(docker inspect "$active_container" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}')" = healthy ] \
  || die "active application is not healthy"

network_created=false
postgres_connected=false
relay_started=false
relay_preexisting=false
hba_backup=""
state_backup_dir="$(mktemp -d)"
account_created=false
role_created=false
role_password_changed=false
old_role_password=""
old_wal_ceiling=""
ssh_state_saved=false
socket_state_saved=false
socket_was_active=false
socket_was_enabled=false
relay_config="$APP_DIR/pg-streaming/nginx.conf"
socket_unit_path="/etc/systemd/system/$RELAY_SOCKET_UNIT"
proxy_unit_path="/etc/systemd/system/$RELAY_PROXY_UNIT"

backup_file() {
  local name="$1"
  local path="$2"
  if [ -e "$path" ]; then
    cp -a "$path" "$state_backup_dir/$name"
  else
    : >"$state_backup_dir/$name.absent"
  fi
}

restore_file() {
  local name="$1"
  local path="$2"
  if [ -e "$state_backup_dir/$name.absent" ]; then
    rm -f "$path"
  else
    cp -a "$state_backup_dir/$name" "$path"
  fi
}

backup_file relay_config "$relay_config"
cleanup_on_error() {
  status=$?
  trap - EXIT
  if [ "$status" -ne 0 ]; then
    if [ -n "$hba_backup" ] && [ -f "$hba_backup" ]; then
      cp -p "$hba_backup" "$hba" || true
      docker exec "$POSTGRES_CONTAINER" sh -lc 'exec psql -XAt -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT pg_reload_conf()"' >/dev/null 2>&1 || true
    fi
    if [ "$role_created" = true ]; then
      docker exec "$POSTGRES_CONTAINER" sh -lc "exec psql -XAt -U \"\$POSTGRES_USER\" -d \"\$POSTGRES_DB\" -v ON_ERROR_STOP=1 -c 'DROP ROLE IF EXISTS ${REPLICATION_USER}'" >/dev/null 2>&1 || true
    elif [ "$role_password_changed" = true ]; then
      if [ -n "$old_role_password" ]; then
        {
          printf '\\set old_repl_password %s\n' "$old_role_password"
          printf "SELECT format('ALTER ROLE %s PASSWORD %%L', :'old_repl_password') \\\\gexec\n" "$REPLICATION_USER"
        } | docker exec -i "$POSTGRES_CONTAINER" sh -lc 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' >/dev/null 2>&1 || true
      else
        docker exec "$POSTGRES_CONTAINER" sh -lc "exec psql -XAt -U \"\$POSTGRES_USER\" -d \"\$POSTGRES_DB\" -v ON_ERROR_STOP=1 -c 'ALTER ROLE ${REPLICATION_USER} PASSWORD NULL'" >/dev/null 2>&1 || true
      fi
    fi
    if [ -n "$old_wal_ceiling" ]; then
      docker exec "$POSTGRES_CONTAINER" sh -lc "exec psql -XAt -U \"\$POSTGRES_USER\" -d \"\$POSTGRES_DB\" -v ON_ERROR_STOP=1 -c \"ALTER SYSTEM SET max_slot_wal_keep_size = '${old_wal_ceiling}'\"" >/dev/null 2>&1 || true
      docker exec "$POSTGRES_CONTAINER" sh -lc 'exec psql -XAt -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -c "SELECT pg_reload_conf()"' >/dev/null 2>&1 || true
    fi
    if [ "$ssh_state_saved" = true ]; then
      if [ "$account_created" = true ]; then
        userdel -r "$TUNNEL_USER" >/dev/null 2>&1 || true
      else
        restore_file authorized_keys "$authorized_keys_path"
      fi
      restore_file sshd_config "$sshd_config"
      sshd -t >/dev/null 2>&1 && systemctl reload ssh >/dev/null 2>&1 || true
    fi
    if [ "$socket_state_saved" = true ]; then
      systemctl stop "$RELAY_SOCKET_UNIT" "$RELAY_PROXY_UNIT" >/dev/null 2>&1 || true
      restore_file relay_socket "$socket_unit_path"
      restore_file relay_proxy "$proxy_unit_path"
      systemctl daemon-reload >/dev/null 2>&1 || true
      if [ "$socket_was_enabled" = true ]; then
        systemctl enable "$RELAY_SOCKET_UNIT" >/dev/null 2>&1 || true
      else
        systemctl disable "$RELAY_SOCKET_UNIT" >/dev/null 2>&1 || true
      fi
      [ "$socket_was_active" = false ] || systemctl start "$RELAY_SOCKET_UNIT" >/dev/null 2>&1 || true
    fi
    [ "$relay_started" = false ] || docker rm -f "$RELAY_CONTAINER" >/dev/null 2>&1 || true
    [ "$postgres_connected" = false ] || docker network disconnect "$RELAY_NETWORK" "$POSTGRES_CONTAINER" >/dev/null 2>&1 || true
    [ "$network_created" = false ] || docker network rm "$RELAY_NETWORK" >/dev/null 2>&1 || true
    restore_file relay_config "$relay_config"
    if [ "$relay_preexisting" = true ]; then
      docker exec "$RELAY_CONTAINER" nginx -t >/dev/null 2>&1 \
        && docker exec "$RELAY_CONTAINER" nginx -s reload >/dev/null 2>&1 || true
    fi
  fi
  rm -rf "$state_backup_dir"
  exit "$status"
}
trap cleanup_on_error EXIT

if docker network inspect "$RELAY_NETWORK" >/dev/null 2>&1; then
  actual_subnet="$(docker network inspect "$RELAY_NETWORK" --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}')"
  [ "$actual_subnet" = "$RELAY_SUBNET" ] || die "relay network has unexpected subnet: $actual_subnet"
  [ "$(docker network inspect "$RELAY_NETWORK" --format '{{.Internal}}')" = true ] \
    || die "relay network is not internal"
else
  docker network create --driver bridge --internal --subnet "$RELAY_SUBNET" "$RELAY_NETWORK" >/dev/null
  network_created=true
fi

current_primary_ip="$(docker inspect "$POSTGRES_CONTAINER" --format "{{with index .NetworkSettings.Networks \"$RELAY_NETWORK\"}}{{.IPAddress}}{{end}}")"
if [ -z "$current_primary_ip" ]; then
  docker network connect --ip "$POSTGRES_RELAY_IP" --alias postgres-primary "$RELAY_NETWORK" "$POSTGRES_CONTAINER"
  postgres_connected=true
elif [ "$current_primary_ip" != "$POSTGRES_RELAY_IP" ]; then
  die "PostgreSQL has unexpected relay-network address: $current_primary_ip"
fi

install -d -o root -g root -m 755 "$APP_DIR/pg-streaming"
relay_config_temp="$(mktemp)"
cat >"$relay_config_temp" <<EOF
worker_processes 1;
pid /tmp/nginx.pid;
error_log /dev/stderr notice;

events {
    worker_connections 256;
}

stream {
    server {
        listen ${RELAY_PORT};
        proxy_connect_timeout 5s;
        proxy_timeout 1h;
        proxy_pass ${POSTGRES_RELAY_IP}:5432;
    }
}
EOF
install -o root -g root -m 644 "$relay_config_temp" "$relay_config"
rm -f "$relay_config_temp"

docker run --rm --network none --read-only --tmpfs /tmp:rw,noexec,nosuid,size=1m \
  --volume "$relay_config:/etc/nginx/nginx.conf:ro" \
  "$RELAY_IMAGE" nginx -t >/dev/null

if docker inspect "$RELAY_CONTAINER" >/dev/null 2>&1; then
  relay_preexisting=true
  managed="$(docker inspect "$RELAY_CONTAINER" --format '{{index .Config.Labels "com.turtleroute.sub2api.role"}}')"
  [ "$managed" = postgres-streaming-relay ] || die "refusing to replace unmanaged container: $RELAY_CONTAINER"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.Config.Image}}')" = "$RELAY_IMAGE" ] \
    || die "existing relay uses an unexpected image"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{len .HostConfig.PortBindings}}')" -eq 0 ] \
    || die "existing relay must not publish Docker host ports"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.HostConfig.NetworkMode}}')" = "$RELAY_NETWORK" ] \
    || die "existing relay uses an unexpected network mode"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{len .NetworkSettings.Networks}}')" -eq 1 ] \
    || die "existing relay has unexpected extra networks"
  [ "$(docker inspect "$RELAY_CONTAINER" --format "{{with index .NetworkSettings.Networks \"$RELAY_NETWORK\"}}{{.IPAddress}}{{end}}")" = "$RELAY_IP" ] \
    || die "existing relay uses an unexpected address"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.HostConfig.ReadonlyRootfs}}')" = true ] \
    || die "existing relay root filesystem is writable"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{json .HostConfig.CapDrop}}')" = '["ALL"]' ] \
    || die "existing relay does not drop all capabilities"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{len .HostConfig.CapAdd}}')" -eq 0 ] \
    || die "existing relay has added capabilities"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.HostConfig.Privileged}}')" = false ] \
    || die "existing relay is privileged"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{json .HostConfig.SecurityOpt}}')" = '["no-new-privileges"]' ] \
    || die "existing relay is missing no-new-privileges"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.Config.User}}')" = '101:101' ] \
    || die "existing relay uses an unexpected user"
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.HostConfig.RestartPolicy.Name}}')" = unless-stopped ] \
    || die "existing relay uses an unexpected restart policy"
  if [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.State.Status}}')" != running ]; then
    docker start "$RELAY_CONTAINER" >/dev/null
  fi
  docker exec "$RELAY_CONTAINER" nginx -t >/dev/null
  docker exec "$RELAY_CONTAINER" nginx -s reload >/dev/null
else
  docker run -d \
    --name "$RELAY_CONTAINER" \
    --restart unless-stopped \
    --network "$RELAY_NETWORK" \
    --ip "$RELAY_IP" \
    --read-only \
    --tmpfs /tmp:rw,noexec,nosuid,size=1m \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --user 101:101 \
    --memory 64m \
    --cpus 0.25 \
    --label com.turtleroute.sub2api.role=postgres-streaming-relay \
    --volume "$relay_config:/etc/nginx/nginx.conf:ro" \
    "$RELAY_IMAGE" >/dev/null
  relay_started=true
fi

for _ in $(seq 1 20); do
  [ "$(docker inspect "$RELAY_CONTAINER" --format '{{.State.Status}}')" = running ] && break
  sleep 1
done
[ "$(docker inspect "$RELAY_CONTAINER" --format '{{.State.Status}}')" = running ] || die "relay did not start"
[ "$(docker inspect "$RELAY_CONTAINER" --format "{{with index .NetworkSettings.Networks \"$RELAY_NETWORK\"}}{{.IPAddress}}{{end}}")" = "$RELAY_IP" ] \
  || die "relay received an unexpected address"

backup_file relay_socket "$socket_unit_path"
backup_file relay_proxy "$proxy_unit_path"
systemctl is-active --quiet "$RELAY_SOCKET_UNIT" 2>/dev/null && socket_was_active=true
systemctl is-enabled --quiet "$RELAY_SOCKET_UNIT" 2>/dev/null && socket_was_enabled=true
socket_state_saved=true

socket_temp="$(mktemp)"
cat >"$socket_temp" <<EOF
[Unit]
Description=Sub2API loopback socket for PostgreSQL streaming relay

[Socket]
ListenStream=127.0.0.1:${RELAY_PORT}
NoDelay=true

[Install]
WantedBy=sockets.target
EOF
install -o root -g root -m 644 "$socket_temp" "$socket_unit_path"
rm -f "$socket_temp"

proxy_temp="$(mktemp)"
cat >"$proxy_temp" <<EOF
[Unit]
Description=Sub2API loopback proxy to the isolated PostgreSQL streaming relay
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
install -o root -g root -m 644 "$proxy_temp" "$proxy_unit_path"
rm -f "$proxy_temp"

systemd-analyze verify "$socket_unit_path" "$proxy_unit_path" >/dev/null
systemctl daemon-reload
systemctl stop "$RELAY_PROXY_UNIT" >/dev/null 2>&1 || true
systemctl restart "$RELAY_SOCKET_UNIT"
systemctl enable "$RELAY_SOCKET_UNIT" >/dev/null
ss -lntH | awk '{print $4}' | grep -Fxq "127.0.0.1:${RELAY_PORT}" \
  || die "loopback relay socket did not bind"
timeout 5 nc -z 127.0.0.1 "$RELAY_PORT" || die "loopback relay proxy did not reach PostgreSQL"

sshd_config="/etc/ssh/sshd_config.d/56-sub2api-pg-tunnel.conf"
authorized_keys_path="$TUNNEL_HOME/.ssh/authorized_keys"
backup_file authorized_keys "$authorized_keys_path"
backup_file sshd_config "$sshd_config"
ssh_state_saved=true

if id "$TUNNEL_USER" >/dev/null 2>&1; then
  [ "$(getent passwd "$TUNNEL_USER" | awk -F: '{print $6}')" = "$TUNNEL_HOME" ] \
    || die "existing tunnel account has unexpected home"
else
  useradd --system --create-home --home-dir "$TUNNEL_HOME" --shell /usr/sbin/nologin --user-group "$TUNNEL_USER"
  account_created=true
fi
usermod -L -s /usr/sbin/nologin "$TUNNEL_USER"
install -d -o "$TUNNEL_USER" -g "$TUNNEL_USER" -m 700 "$TUNNEL_HOME/.ssh"
authorized_keys_temp="$(mktemp)"
printf 'from="%s",restrict,port-forwarding,permitopen="127.0.0.1:%s" %s\n' \
  "$SOURCE_CIDR" "$RELAY_PORT" "$key_line" >"$authorized_keys_temp"
install -o "$TUNNEL_USER" -g "$TUNNEL_USER" -m 600 "$authorized_keys_temp" "$authorized_keys_path"
rm -f "$authorized_keys_temp"

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
install -o root -g root -m 644 "$sshd_config_temp" "$sshd_config"
rm -f "$sshd_config_temp"
if ! sshd -t; then
  die "generated sshd policy did not validate"
fi
systemctl reload ssh

mount_source="$(docker inspect "$POSTGRES_CONTAINER" --format '{{range .Mounts}}{{if eq .Destination "/var/lib/postgresql/data"}}{{.Source}}{{end}}{{end}}')"
[ -n "$mount_source" ] || die "cannot resolve PostgreSQL data mount"
pgdata="$(docker exec "$POSTGRES_CONTAINER" sh -lc 'printf %s "$PGDATA"')"
case "$pgdata" in
  /var/lib/postgresql/data/*) pgdata_host="$mount_source/${pgdata#/var/lib/postgresql/data/}" ;;
  /var/lib/postgresql/data) pgdata_host="$mount_source" ;;
  *) die "unexpected PostgreSQL data directory: $pgdata" ;;
esac
hba="$pgdata_host/pg_hba.conf"
[ -f "$hba" ] || die "pg_hba.conf is missing"
hba_backup="$(mktemp --tmpdir="$pgdata_host")"
cp -p "$hba" "$hba_backup"
hba_temp="$(mktemp --tmpdir="$pgdata_host")"
cat >"$hba_temp" <<EOF
# BEGIN SUB2API TOKYO STREAMING
# SSH terminates on the host; this exact source is the fixed relay container.
host all ${REPLICATION_USER} ${RELAY_IP}/32 reject
host replication ${REPLICATION_USER} ${RELAY_IP}/32 scram-sha-256
# END SUB2API TOKYO STREAMING
EOF
awk '
  $0 == "# BEGIN SUB2API TOKYO STREAMING" { skipping=1; next }
  $0 == "# END SUB2API TOKYO STREAMING" { skipping=0; next }
  !skipping { print }
' "$hba" >>"$hba_temp"
chown --reference="$hba" "$hba_temp"
chmod --reference="$hba" "$hba_temp"
mv "$hba_temp" "$hba"

role_preexisting="$(docker exec "$POSTGRES_CONTAINER" sh -lc "exec psql -XAt -U \"\$POSTGRES_USER\" -d \"\$POSTGRES_DB\" -c \"SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '${REPLICATION_USER}')\"")"
if [ "$role_preexisting" = f ]; then
  {
    printf '\\set repl_password %s\n' "$password"
    cat <<SQL
SET password_encryption = 'scram-sha-256';
SELECT format(
  'CREATE ROLE ${REPLICATION_USER} WITH LOGIN REPLICATION NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT CONNECTION LIMIT 2 PASSWORD %L',
  :'repl_password'
) \gexec
SQL
  } | docker exec -i "$POSTGRES_CONTAINER" sh -lc 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' >/dev/null
  role_created=true
else
  existing_role_state="$(docker exec -i "$POSTGRES_CONTAINER" sh -lc 'exec psql -XAt -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1' <<SQL
SELECT rolsuper || '|' || rolcreatedb || '|' || rolcreaterole || '|' || rolcanlogin || '|' || rolreplication || '|' || rolbypassrls || '|' || rolinherit || '|' || rolconnlimit || '|' || (rolvaliduntil IS NULL)
FROM pg_roles WHERE rolname = '${REPLICATION_USER}';
SQL
  )"
  [ "$existing_role_state" = 'false|false|false|true|true|false|false|2|true' ] \
    || die "refusing to alter a pre-existing role with unexpected privileges: $existing_role_state"
  old_role_password="$(docker exec "$POSTGRES_CONTAINER" sh -lc "exec psql -XAt -U \"\$POSTGRES_USER\" -d \"\$POSTGRES_DB\" -v ON_ERROR_STOP=1 -c \"SELECT COALESCE(rolpassword, '') FROM pg_authid WHERE rolname = '${REPLICATION_USER}'\"")"
  {
    printf '\\set repl_password %s\n' "$password"
    printf "SET password_encryption = 'scram-sha-256';\nSELECT format('ALTER ROLE %s PASSWORD %%L', :'repl_password') \\\\gexec\n" "$REPLICATION_USER"
  } | docker exec -i "$POSTGRES_CONTAINER" sh -lc 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' >/dev/null
  role_password_changed=true
fi
unset password

old_wal_ceiling="$(docker exec "$POSTGRES_CONTAINER" sh -lc 'exec psql -XAt -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SHOW max_slot_wal_keep_size"')"
docker exec "$POSTGRES_CONTAINER" sh -lc "exec psql -XAt -U \"\$POSTGRES_USER\" -d \"\$POSTGRES_DB\" -v ON_ERROR_STOP=1 -c \"ALTER SYSTEM SET max_slot_wal_keep_size = '${SLOT_WAL_CEILING}'\"" >/dev/null
docker exec "$POSTGRES_CONTAINER" sh -lc 'exec psql -XAt -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -c "SELECT pg_reload_conf()"' >/dev/null

role_state="$(docker exec -i "$POSTGRES_CONTAINER" sh -lc 'exec psql -XAt -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1' <<SQL
SELECT rolsuper || '|' || rolcreatedb || '|' || rolcreaterole || '|' || rolcanlogin || '|' || rolreplication || '|' || rolbypassrls || '|' || rolinherit || '|' || rolconnlimit || '|' || (rolvaliduntil IS NULL)
FROM pg_roles WHERE rolname = '${REPLICATION_USER}';
SQL
)"
[ "$role_state" = 'false|false|false|true|true|false|false|2|true' ] \
  || die "replication role privileges are unexpected: $role_state"
password_is_scram="$(docker exec "$POSTGRES_CONTAINER" sh -lc "exec psql -XAt -U \"\$POSTGRES_USER\" -d \"\$POSTGRES_DB\" -v ON_ERROR_STOP=1 -c \"SELECT rolpassword LIKE 'SCRAM-SHA-256$%' FROM pg_authid WHERE rolname = '${REPLICATION_USER}'\"")"
[ "$password_is_scram" = t ] || die "replication role password is not SCRAM-SHA-256"

loaded_ceiling="$(docker exec "$POSTGRES_CONTAINER" sh -lc 'exec psql -XAt -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SHOW max_slot_wal_keep_size"')"
[ "$loaded_ceiling" != -1 ] || die "bounded slot WAL ceiling did not load"
docker exec "$POSTGRES_CONTAINER" sh -lc 'exec psql -XAt -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT pg_hba_file_rules.error FROM pg_hba_file_rules WHERE error IS NOT NULL"' | grep -q . \
  && die "PostgreSQL reports an invalid HBA rule"

rm -f "$hba_backup"
hba_backup=""
old_wal_ceiling=""
old_role_password=""
role_password_changed=false
relay_started=false
postgres_connected=false
network_created=false
rm -rf "$state_backup_dir"
trap - EXIT
echo "Installed production PostgreSQL streaming prerequisites."
echo "active_app=${active_container} relay=127.0.0.1:${RELAY_PORT} hba_source=${RELAY_IP}/32 slot_wal_ceiling=${loaded_ceiling}"
