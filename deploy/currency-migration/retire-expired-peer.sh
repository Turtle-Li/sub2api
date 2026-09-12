#!/usr/bin/env bash
# Retire an expired peer's exact database sources while preserving the current
# Azure public source and replacing its Tailnet fallback. Run only on the DB
# host while root holds the candidate maintenance lock.
set -euo pipefail

case $- in
  *x*) echo 'Refusing to run with shell tracing enabled' >&2; exit 1 ;;
esac

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

readonly FIREWALL_ENV=/etc/sub2api-db-firewall.conf
readonly RUNTIME_ENV=/opt/sub2api-migration/runtime.env
readonly FIREWALL_HELPER=/usr/local/sbin/sub2api-db-docker-firewall
readonly PG_CONTAINER=sub2api-migration-postgres
readonly REDIS_CONTAINER=sub2api-migration-redis-public
readonly EXPECTED_HBA_FILE=/var/lib/postgresql/18/docker/pg_hba.conf
readonly BACKUP_PARENT=/var/backups/sub2api-db-peer-retire
readonly TRANSITION_LOCK=/run/lock/sub2api-db-peer-retire.lock

backup_dir=''
hba_uploaded=false
hba_container_tmp=''
phase=preflight

usage() {
  cat >&2 <<'USAGE'
Usage: SUB2API_CANDIDATE_MAINTENANCE_LOCK_HELD=1 retire-expired-peer.sh \
  RETIRED_PUBLIC/32 CURRENT_PUBLIC/32 RETIRED_TAILNET/32 CURRENT_TAILNET/32
USAGE
}

die() {
  echo "ERROR: $*" >&2
  [[ -z $backup_dir ]] || echo "Protected backups: ${backup_dir}" >&2
  exit 1
}

on_error() {
  local status=$?
  echo "ERROR: stopped during ${phase}; no automatic rollback was attempted" >&2
  [[ -z $backup_dir ]] || echo "Protected backups: ${backup_dir}" >&2
  exit "$status"
}

cleanup() {
  if [[ $hba_uploaded == true && -n $hba_container_tmp ]]; then
    docker exec --user 0:0 "$PG_CONTAINER" rm -f -- "$hba_container_tmp" >/dev/null 2>&1 || true
  fi
}
trap on_error ERR
trap cleanup EXIT

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

is_root_regular_600() {
  [[ -f $1 && ! -L $1 && $(stat -c '%u:%g:%a' "$1") == '0:0:600' ]]
}

is_uint() {
  [[ $1 =~ ^[0-9]+$ ]]
}

assert_eq() {
  local expected=$1 actual=$2 label=$3
  [[ $actual == "$expected" ]] || die "$label did not match the expected precondition"
}

normalize_sources() {
  local normalized
  if ! normalized=$(python3 - "$@" <<'PY'
import ipaddress
import sys

if len(sys.argv) != 5:
    raise SystemExit("exactly four IPv4 /32 sources are required")

values = []
for value in sys.argv[1:]:
    try:
        network = ipaddress.ip_network(value, strict=True)
    except ValueError:
        raise SystemExit("every source must be an exact IPv4 /32")
    if network.version != 4 or network.prefixlen != 32:
        raise SystemExit("every source must be an exact IPv4 /32")
    values.append(str(network))

if len(set(values)) != len(values):
    raise SystemExit("retired and replacement sources must all be distinct")
print("\n".join(values))
PY
); then
    die 'invalid source arguments'
  fi
  mapfile -t normalized_sources <<<"$normalized"
  [[ ${#normalized_sources[@]} == 4 ]] || die 'invalid source argument count'
}

pg() {
  PGPASSWORD="$POSTGRES_PASSWORD" docker exec --user postgres -e PGPASSWORD -i "$PG_CONTAINER" \
    psql -X -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 "$@"
}

redis() {
  REDISCLI_AUTH="$REDIS_PASSWORD" docker exec -e REDISCLI_AUTH "$REDIS_CONTAINER" \
    redis-cli --no-auth-warning "$@"
}

old_pg_session_counts() {
  pg -v retired_public_ip="$retired_public_ip" -v retired_tail_ip="$retired_tail_ip" -At <<'SQL'
SELECT count(*) FILTER (WHERE state = 'active'),
       count(*) FILTER (WHERE state = 'idle'),
       count(*) FILTER (WHERE state IS DISTINCT FROM 'active' AND state IS DISTINCT FROM 'idle')
FROM pg_stat_activity
WHERE client_addr IN (:'retired_public_ip'::inet, :'retired_tail_ip'::inet);
SQL
}

current_public_pg_session_count() {
  pg -v current_public_ip="$current_public_ip" -At <<'SQL'
SELECT count(*)
FROM pg_stat_activity
WHERE client_addr = :'current_public_ip'::inet;
SQL
}

redis_old_client_count() {
  redis --raw CLIENT LIST | awk -v public_ip="$retired_public_ip" -v tail_ip="$retired_tail_ip" '
    {
      for (field_index = 1; field_index <= NF; field_index++) {
        if (index($field_index, "addr=") == 1) {
          address = substr($field_index, 6)
          split(address, parts, ":")
          if (parts[1] == public_ip || parts[1] == tail_ip) {
            count++
          }
          break
        }
      }
    }
    END { print count + 0 }
  '
}

ufw_rule() {
  local interface=$1 source=$2 port=$3
  printf 'ufw allow in on %s from %s to any port %s proto tcp' "$interface" "$source" "$port"
}

ufw_rule_count() {
  local rules_file=$1 interface=$2 source=$3 port=$4
  local cidr_rule host_rule host_source
  cidr_rule=$(ufw_rule "$interface" "$source" "$port")
  host_source=${source%/32}
  host_rule=$(ufw_rule "$interface" "$host_source" "$port")
  awk -v cidr_rule="$cidr_rule" -v host_rule="$host_rule" '
    function is_exact_rule(line, base, suffix, quote) {
      if (line == base) {
        return 1
      }
      quote = sprintf("%c", 39)
      suffix = substr(line, length(base) + 1)
      return index(line, base) == 1 && suffix ~ ("^ comment " quote "[^" quote "]*" quote "$")
    }
    is_exact_rule($0, cidr_rule) || is_exact_rule($0, host_rule) { count++ }
    END { print count + 0 }
  ' "$rules_file"
}

require_ufw_rule_count() {
  local rules_file=$1 interface=$2 source=$3 port=$4 expected_count=$5 label=$6
  local actual_count
  actual_count=$(ufw_rule_count "$rules_file" "$interface" "$source" "$port")
  assert_eq "$expected_count" "$actual_count" "$label"
}

apply_ufw() {
  # Complete allow/delete rules are non-interactive. Some installed UFW
  # versions reject --force before an ordinary rule mutation.
  ufw "$@" >>"$backup_dir/ufw-actions.log" 2>&1 || die 'UFW exact-rule update failed'
}

replace_firewall_config() {
  local next_config=$1 temporary_config
  temporary_config=$(mktemp "${FIREWALL_ENV}.retire.XXXXXX")
  cp "$next_config" "$temporary_config"
  chown --reference="$FIREWALL_ENV" "$temporary_config"
  chmod --reference="$FIREWALL_ENV" "$temporary_config"
  mv -f -- "$temporary_config" "$FIREWALL_ENV"
}

install_hba_file() {
  local next_hba=$1 hba_owner=$2 hba_mode=$3
  hba_container_tmp="${EXPECTED_HBA_FILE}.sub2api-retire-${BASHPID}"
  docker cp "$next_hba" "${PG_CONTAINER}:${hba_container_tmp}"
  hba_uploaded=true
  docker exec --user 0:0 "$PG_CONTAINER" sh -eu -c '
    target=$1
    temporary=$2
    owner=$3
    mode=$4
    chown "$owner" "$temporary"
    chmod "$mode" "$temporary"
    mv -f "$temporary" "$target"
  ' sh "$EXPECTED_HBA_FILE" "$hba_container_tmp" "$hba_owner" "$hba_mode"
  hba_uploaded=false
  hba_container_tmp=''
}

[[ ${EUID:-999} -eq 0 ]] || die 'must run as root on the database host'
[[ ${SUB2API_CANDIDATE_MAINTENANCE_LOCK_HELD:-} == 1 ]] || die 'candidate maintenance lock acknowledgement is required'
[[ $# -eq 4 ]] || { usage; exit 2; }

for required_command in awk bash cp docker flock iptables-save mktemp python3 stat ufw; do
  require_command "$required_command"
done

is_root_regular_600 "$FIREWALL_ENV" || die 'database firewall config must be root-owned mode 600 and not a symlink'
is_root_regular_600 "$RUNTIME_ENV" || die 'migration runtime env must be root-owned mode 600 and not a symlink'
[[ -f $FIREWALL_HELPER && ! -L $FIREWALL_HELPER && -x $FIREWALL_HELPER && $(stat -c '%u' "$FIREWALL_HELPER") == 0 ]] \
  || die 'installed database firewall helper must be a root-owned regular executable'

normalize_sources "$@"
retired_public=${normalized_sources[0]}
current_public=${normalized_sources[1]}
retired_tail=${normalized_sources[2]}
current_tail=${normalized_sources[3]}
retired_public_ip=${retired_public%/32}
current_public_ip=${current_public%/32}
retired_tail_ip=${retired_tail%/32}

# The installed firewall helper uses this same protected Bash contract. Keep
# only its explicit source values and restore a known command path afterwards.
unset PRODUCTION_PUBLIC_SOURCE PRODUCTION_PUBLIC_SOURCES PRODUCTION_TAILNET_SOURCE
set +x
# shellcheck disable=SC1090
. "$FIREWALL_ENV"
set +x
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

# shellcheck disable=SC2153 # populated by the protected firewall config above
case "$(declare -p PRODUCTION_PUBLIC_SOURCES 2>/dev/null)" in
  'declare -a '*) ;;
  *) die 'firewall public sources must be a Bash array' ;;
esac
[[ ! ${PRODUCTION_PUBLIC_SOURCE+x} ]] || die 'deprecated singular public source is not allowed'
[[ ${#PRODUCTION_PUBLIC_SOURCES[@]} -eq 2 ]] || die 'firewall config must contain exactly two public sources'
assert_eq "$retired_public" "${PRODUCTION_PUBLIC_SOURCES[0]}" 'first configured public source'
assert_eq "$current_public" "${PRODUCTION_PUBLIC_SOURCES[1]}" 'second configured public source'
assert_eq "$retired_tail" "${PRODUCTION_TAILNET_SOURCE:-}" 'configured Tailnet source'
assert_eq eth0 "${PUBLIC_INTERFACE:-eth0}" 'configured public interface'
assert_eq tailscale0 "${TAILSCALE_INTERFACE:-tailscale0}" 'configured Tailnet interface'

# Runtime values are never printed or put in command arguments. docker exec
# receives them by environment-name only.
set +x
# shellcheck disable=SC1090
. "$RUNTIME_ENV"
set +x
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
: "${POSTGRES_USER:=sub2api}"
: "${POSTGRES_DB:=sub2api}"
: "${POSTGRES_PASSWORD:?missing PostgreSQL password in migration runtime env}"
[[ ${REDIS_PASSWORD+x} ]] || die 'missing Redis password in migration runtime env'

for container in "$PG_CONTAINER" "$REDIS_CONTAINER"; do
  [[ $(docker inspect --format '{{.State.Running}}' "$container" 2>/dev/null) == true ]] \
    || die 'required migration database container is not running'
done

[[ ! -L $TRANSITION_LOCK ]] || die 'expired-peer transition lock must not be a symlink'
exec 9>"$TRANSITION_LOCK"
flock -n 9 || die 'another expired-peer transition is already running'

hba_file=$(pg -Atqc 'SHOW hba_file')
assert_eq "$EXPECTED_HBA_FILE" "$hba_file" 'PostgreSQL hba_file'
docker exec --user 0:0 "$PG_CONTAINER" sh -eu -c '[ -f "$1" ] && [ ! -L "$1" ]' sh "$EXPECTED_HBA_FILE" \
  || die 'PostgreSQL hba_file must be a regular file'
hba_metadata=$(docker exec --user 0:0 "$PG_CONTAINER" stat -c '%u:%g:%a' -- "$EXPECTED_HBA_FILE")
[[ $hba_metadata =~ ^[0-9]+:[0-9]+:[0-7]{3,4}$ ]] || die 'PostgreSQL hba_file metadata is invalid'
hba_owner=${hba_metadata%:*}
hba_mode=${hba_metadata##*:}

old_pg_counts=$(old_pg_session_counts)
IFS='|' read -r old_pg_active old_pg_idle old_pg_unsafe <<<"$old_pg_counts"
if ! is_uint "$old_pg_active" || ! is_uint "$old_pg_idle" || ! is_uint "$old_pg_unsafe"; then
  die 'could not classify retired PostgreSQL sessions'
fi
[[ $old_pg_active == 0 && $old_pg_unsafe == 0 ]] \
  || die 'retired PostgreSQL source has active or non-idle sessions; aborting before network changes'
old_pg_idle_before_network_change=$old_pg_idle
redis_old_pre=$(redis_old_client_count)
is_uint "$redis_old_pre" || die 'could not classify retired Redis clients'
[[ $redis_old_pre == 0 ]] || die 'retired Redis clients are still present; aborting before network changes'
current_public_pg_pre=$(current_public_pg_session_count)
is_uint "$current_public_pg_pre" || die 'could not count current Azure PostgreSQL sessions'

umask 077
if [[ -e $BACKUP_PARENT || -L $BACKUP_PARENT ]]; then
  [[ -d $BACKUP_PARENT && ! -L $BACKUP_PARENT ]] || die 'backup parent must be a real directory'
else
  install -d -m 700 -o root -g root "$BACKUP_PARENT"
fi
[[ -d $BACKUP_PARENT && ! -L $BACKUP_PARENT && $(stat -c '%u:%g:%a' "$BACKUP_PARENT") == 0:0:700 ]] \
  || die 'backup parent must be root-owned mode 700'
backup_dir=$(mktemp -d "${BACKUP_PARENT}/retire-$(date -u +%Y%m%dT%H%M%SZ).XXXXXX")
chmod 700 "$backup_dir"

cp --preserve=mode,ownership,timestamps "$FIREWALL_ENV" "$backup_dir/firewall.conf.before"
iptables-save >"$backup_dir/iptables-save.before"
ufw status numbered >"$backup_dir/ufw-status-numbered.before"
ufw status verbose >"$backup_dir/ufw-status-verbose.before"
ufw show added >"$backup_dir/ufw-added.before"
docker cp "${PG_CONTAINER}:${EXPECTED_HBA_FILE}" "$backup_dir/pg_hba.conf.before"
chmod 600 "$backup_dir/pg_hba.conf.before"

grep -Fqx 'Status: active' "$backup_dir/ufw-status-verbose.before" \
  || die 'UFW must be active before retiring exact source rules'
for port in 5432 6379; do
  require_ufw_rule_count "$backup_dir/ufw-added.before" eth0 "$retired_public" "$port" 1 "retired public UFW ${port} rule"
  require_ufw_rule_count "$backup_dir/ufw-added.before" eth0 "$current_public" "$port" 1 "current public UFW ${port} rule"
  require_ufw_rule_count "$backup_dir/ufw-added.before" tailscale0 "$retired_tail" "$port" 1 "retired Tailnet UFW ${port} rule"
  require_ufw_rule_count "$backup_dir/ufw-added.before" tailscale0 "$current_tail" "$port" 0 "replacement Tailnet UFW ${port} rule"
done

python3 - "$FIREWALL_ENV" "$backup_dir/firewall.conf.next" "$current_public" "$current_tail" <<'PY'
import re
import sys
from pathlib import Path

source = Path(sys.argv[1])
destination = Path(sys.argv[2])
public = sys.argv[3]
tailnet = sys.argv[4]

public_single_pattern = re.compile(
    r"^(\s*)PRODUCTION_PUBLIC_SOURCES=\([^#\r\n]*\)(\s*(?:#.*)?)(\r?\n?)$"
)
public_multiline_start_pattern = re.compile(
    r"^(\s*)PRODUCTION_PUBLIC_SOURCES=\(\s*(?:#.*)?(\r?\n?)$"
)
public_multiline_end_pattern = re.compile(
    r"^\s*\)(\s*(?:#.*)?)(\r?\n?)$"
)
tailnet_pattern = re.compile(
    r"^(\s*)PRODUCTION_TAILNET_SOURCE=(?:\"[^\"\r\n]*\"|'[^'\r\n]*'|[^\s#\r\n]+)(\s*(?:#.*)?)(\r?\n?)$"
)

with source.open("r", encoding="utf-8", newline="") as handle:
    lines = handle.readlines()

public_assignments = 0
tailnet_assignments = 0
rewritten = []
line_index = 0
while line_index < len(lines):
    line = lines[line_index]
    match = public_single_pattern.match(line)
    if match:
        public_assignments += 1
        rewritten.append(f'{match.group(1)}PRODUCTION_PUBLIC_SOURCES=("{public}"){match.group(2)}{match.group(3)}')
        line_index += 1
        continue
    match = public_multiline_start_pattern.match(line)
    if match:
        public_assignments += 1
        closing_index = line_index + 1
        while closing_index < len(lines):
            closing_match = public_multiline_end_pattern.match(lines[closing_index])
            if closing_match:
                break
            closing_index += 1
        if closing_index == len(lines):
            raise SystemExit("unterminated multi-line public-source array")
        rewritten.append(
            f'{match.group(1)}PRODUCTION_PUBLIC_SOURCES=("{public}")'
            f'{closing_match.group(1)}{closing_match.group(2)}'
        )
        line_index = closing_index + 1
        continue
    match = tailnet_pattern.match(line)
    if match:
        tailnet_assignments += 1
        rewritten.append(f'{match.group(1)}PRODUCTION_TAILNET_SOURCE="{tailnet}"{match.group(2)}{match.group(3)}')
        line_index += 1
        continue
    rewritten.append(line)
    line_index += 1

if public_assignments != 1 or tailnet_assignments != 1:
    raise SystemExit("firewall config must contain one public-array and Tailnet assignment")

with destination.open("w", encoding="utf-8", newline="") as handle:
    handle.writelines(rewritten)
PY

python3 - "$backup_dir/pg_hba.conf.before" "$backup_dir/pg_hba.conf.next" \
  "$retired_public" "$retired_tail" "$current_tail" >"$backup_dir/hba-change-counts.txt" <<'PY'
import re
import sys
from pathlib import Path

source = Path(sys.argv[1])
destination = Path(sys.argv[2])
retired_public, retired_tail, current_tail = sys.argv[3:]
pattern = re.compile(r"^(\s*hostssl\s+\S+\s+\S+\s+)(\S+)([^\r\n]*)(\r?\n?)$")

with source.open("r", encoding="utf-8", newline="") as handle:
    lines = handle.readlines()

retired_public_matches = 0
retired_tail_matches = 0
current_tail_matches = 0
rewritten = []
for line in lines:
    match = pattern.match(line)
    if not match:
        rewritten.append(line)
        continue
    address = match.group(2)
    if address == retired_public:
        retired_public_matches += 1
        continue
    if address == retired_tail:
        retired_tail_matches += 1
        rewritten.append(f"{match.group(1)}{current_tail}{match.group(3)}{match.group(4)}")
        continue
    if address == current_tail:
        current_tail_matches += 1
    rewritten.append(line)

if retired_public_matches < 1:
    raise SystemExit("no exact retired-public hostssl HBA rule exists")
if retired_tail_matches != 1:
    raise SystemExit("expected exactly one retired-Tailnet hostssl HBA rule")
if current_tail_matches:
    raise SystemExit("replacement Tailnet hostssl HBA rule already exists")

with destination.open("w", encoding="utf-8", newline="") as handle:
    handle.writelines(rewritten)

print(f"retired_public_hostssl_removed={retired_public_matches}")
print("retired_tailnet_hostssl_replaced=1")
PY
chmod 600 "$backup_dir/firewall.conf.next" "$backup_dir/pg_hba.conf.next" "$backup_dir/hba-change-counts.txt"

phase=firewall-config
replace_firewall_config "$backup_dir/firewall.conf.next"
"$FIREWALL_HELPER" >"$backup_dir/firewall-helper.log" 2>&1 || die 'database firewall helper rejected the replacement allowlist'

phase=ufw
for port in 5432 6379; do
  apply_ufw allow in on tailscale0 from "$current_tail" to any port "$port" proto tcp
done
for port in 5432 6379; do
  apply_ufw delete allow in on eth0 from "$retired_public" to any port "$port" proto tcp
  apply_ufw delete allow in on tailscale0 from "$retired_tail" to any port "$port" proto tcp
done
ufw show added >"$backup_dir/ufw-added.after"
ufw status numbered >"$backup_dir/ufw-status-numbered.after"
for port in 5432 6379; do
  require_ufw_rule_count "$backup_dir/ufw-added.after" eth0 "$retired_public" "$port" 0 "retired public UFW ${port} removal"
  require_ufw_rule_count "$backup_dir/ufw-added.after" tailscale0 "$retired_tail" "$port" 0 "retired Tailnet UFW ${port} removal"
  require_ufw_rule_count "$backup_dir/ufw-added.after" eth0 "$current_public" "$port" 1 "current public UFW ${port} preservation"
  require_ufw_rule_count "$backup_dir/ufw-added.after" tailscale0 "$current_tail" "$port" 1 "replacement Tailnet UFW ${port} addition"
done

phase=postgres-hba
install_hba_file "$backup_dir/pg_hba.conf.next" "$hba_owner" "$hba_mode"
hba_error_count=$(pg -Atqc 'SELECT count(*) FROM pg_hba_file_rules WHERE error IS NOT NULL')
assert_eq 0 "$hba_error_count" 'PostgreSQL HBA parse errors'
assert_eq t "$(pg -Atqc 'SELECT pg_reload_conf()')" 'PostgreSQL HBA reload'
docker cp "${PG_CONTAINER}:${EXPECTED_HBA_FILE}" "$backup_dir/pg_hba.conf.after"
chmod 600 "$backup_dir/pg_hba.conf.after"

phase=postgres-sessions
old_pg_counts=$(old_pg_session_counts)
IFS='|' read -r old_pg_active old_pg_idle old_pg_unsafe <<<"$old_pg_counts"
if ! is_uint "$old_pg_active" || ! is_uint "$old_pg_idle" || ! is_uint "$old_pg_unsafe"; then
  die 'could not reclassify retired PostgreSQL sessions'
fi
[[ $old_pg_active == 0 && $old_pg_unsafe == 0 ]] \
  || die 'retired PostgreSQL source became active or non-idle; refusing to terminate it'

termination_counts=$(pg -v retired_public_ip="$retired_public_ip" -v retired_tail_ip="$retired_tail_ip" -At <<'SQL'
WITH victims AS (
  SELECT pid
  FROM pg_stat_activity
  WHERE client_addr IN (:'retired_public_ip'::inet, :'retired_tail_ip'::inet)
    AND state = 'idle'
), terminated AS (
  SELECT pg_terminate_backend(pid) AS terminated
  FROM victims
)
SELECT count(*) FILTER (WHERE terminated),
       count(*) FILTER (WHERE NOT terminated)
FROM terminated;
SQL
)
IFS='|' read -r terminated_count failed_termination_count <<<"$termination_counts"
if ! is_uint "$terminated_count" || ! is_uint "$failed_termination_count"; then
  die 'could not classify PostgreSQL termination results'
fi
assert_eq 0 "$failed_termination_count" 'retired PostgreSQL idle termination failures'

old_pg_counts=$(old_pg_session_counts)
IFS='|' read -r old_pg_active old_pg_idle old_pg_unsafe <<<"$old_pg_counts"
assert_eq 0 "$old_pg_active" 'retired PostgreSQL active sessions after termination'
assert_eq 0 "$old_pg_idle" 'retired PostgreSQL idle sessions after termination'
assert_eq 0 "$old_pg_unsafe" 'retired PostgreSQL non-idle sessions after termination'
redis_old_post=$(redis_old_client_count)
assert_eq 0 "$redis_old_post" 'retired Redis clients after retirement'
current_public_pg_post=$(current_public_pg_session_count)
is_uint "$current_public_pg_post" || die 'could not count current Azure PostgreSQL sessions after retirement'

phase=verification
iptables-save >"$backup_dir/iptables-save.after"
cp --preserve=mode,ownership,timestamps "$FIREWALL_ENV" "$backup_dir/firewall.conf.after"
cat >"$backup_dir/summary.txt" <<EOF
retired_pg_idle_before_network_change=${old_pg_idle_before_network_change}
retired_pg_terminated=${terminated_count}
retired_redis_clients_before_network_change=${redis_old_pre}
retired_redis_clients_after_retirement=${redis_old_post}
current_public_pg_sessions_before=${current_public_pg_pre}
current_public_pg_sessions_after=${current_public_pg_post}
EOF
chmod 600 "$backup_dir/summary.txt"

phase=complete
echo "PASS: expired peer sources retired; safe session counts recorded in ${backup_dir}"
