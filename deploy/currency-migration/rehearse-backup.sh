#!/usr/bin/env bash
# Restore the approved backup into an isolated, portless container and test SQL.
set -Eeuo pipefail
[[ $(id -u) == 0 ]] || { echo 'Run on sub2api-db as root'; exit 1; }
archive=${1:?backup archive required}
recharge_factor=${2:?explicit recharge factor required}
[[ $recharge_factor == 1 || $recharge_factor == 6.75 ]] || exit 2
case "$archive" in /opt/sub2api-db-backups/sub2api-db-backup-*.tar.gz) ;; *) exit 2;; esac
bundle=$(cd "$(dirname "$0")" && pwd)
workdir=$(mktemp -d /opt/sub2api-migration/currency-rehearsal.XXXXXX)
container="sub2api-currency-rehearsal-${BASHPID}"
cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  if [[ "$workdir" == /opt/sub2api-migration/currency-rehearsal.* ]]; then rm -rf -- "$workdir"; fi
}
trap cleanup EXIT
(cd "$(dirname "$archive")" && sha256sum -c "$(basename "$archive").sha256")
tar -xzf "$archive" -C "$workdir"
(cd "$workdir" && sha256sum -c SHA256SUMS)
install -d -m 700 -o 70 -g 70 "$workdir/postgresql"
docker run -d --network none --name "$container" \
  -e POSTGRES_USER=sub2api -e POSTGRES_DB=sub2api -e POSTGRES_HOST_AUTH_METHOD=trust \
  -v "$workdir/postgresql:/var/lib/postgresql" postgres:18-alpine >/dev/null
for _ in {1..60}; do
  if docker exec "$container" pg_isready -U sub2api -d sub2api >/dev/null 2>&1; then break; fi
  sleep 2
done
docker cp "$workdir/postgres.dump" "$container:/tmp/postgres.dump"
docker exec "$container" pg_restore -U sub2api --exit-on-error --no-owner --no-privileges -d sub2api /tmp/postgres.dump
sql() { docker exec -i "$container" psql -X -U sub2api -d sub2api -v ON_ERROR_STOP=1 "$@"; }
# Hash only monetary fields and IDs. Output hash, not individual records.
check_sql="SELECT md5(string_agg(id::text||':'||balance::text||':'||total_recharged::text,',' ORDER BY id)) FROM users"
before=$(sql -Atc "$check_sql")
sql -v recharge_factor="$recharge_factor" -v apply=false < "$bundle/wallet-to-cny.sql" > "$workdir/dry-run.log"
[[ $(sql -Atc "$check_sql") == "$before" ]]
sql -v recharge_factor="$recharge_factor" -v apply=true < "$bundle/wallet-to-cny.sql" > "$workdir/apply.log"
if sql -v recharge_factor="$recharge_factor" -v apply=true < "$bundle/wallet-to-cny.sql" >/dev/null 2>&1; then echo 'Repeated migration was incorrectly accepted'; exit 1; fi
sql < "$bundle/rollback-before-reopen.sql" > "$workdir/rollback.log"
[[ $(sql -Atc "$check_sql") == "$before" ]]
echo 'PASS: actual backup schema/data rehearsal, dry-run, conversion, repeat rejection and exact wallet rollback'
