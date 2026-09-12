#!/usr/bin/env bash
# Run on the dedicated database host after the CNY transaction, while the
# reviewed application is bypassing monetary/auth caches. Never flush Redis.
set -euo pipefail
set +x
umask 077
[[ $(id -u) == 0 ]] || { echo 'Run as root on sub2api-db' >&2; exit 1; }
[[ ${1:-} == --bypass-confirmed ]] || { echo 'Confirm application cache bypass first' >&2; exit 2; }
set -a
# Existing protected runtime injection; never print its contents.
# shellcheck source=/dev/null
. /opt/sub2api-migration/runtime.env
set +a
export REDISCLI_AUTH="$REDIS_PASSWORD"
workdir=$(mktemp -d /opt/sub2api-migration/currency-cache-refresh.XXXXXX)
trap 'rm -rf -- "$workdir"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
sql() {
  docker exec -i sub2api-migration-postgres psql -X -At \
    -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 "$@"
}
redis() {
  docker exec -e REDISCLI_AUTH sub2api-migration-redis-public \
    redis-cli --no-auth-warning -n "$REDIS_DB" "$@"
}
[[ $(sql -c "SELECT value::jsonb->>'settlement_currency' FROM settings WHERE key='pricing_currency_settings'") == CNY ]]
[[ $(redis SCARD billing:upq:dirty) == 0 ]] || { echo 'Refusing to discard dirty quota snapshots' >&2; exit 1; }

# Hash inside PostgreSQL: neither API keys nor auth snapshots leave the DB.
# Include hashes absent from Redis so in-process L1 entries are invalidated too.
sql -c "SELECT encode(sha256(convert_to(key,'UTF8')),'hex') FROM api_keys WHERE deleted_at IS NULL" > "$workdir/hashes"
[[ -s "$workdir/hashes" ]] || { echo 'Unexpected database with no active keys' >&2; exit 1; }
while IFS= read -r hash; do
  [[ $hash =~ ^[0-9a-f]{64}$ ]] || exit 1
done < "$workdir/hashes"

patterns=('apikey:auth:*' 'billing:balance:*' 'billing:user_platform_quota:*' 'apikey:rate:*')
# Validate the complete scan before deleting anything. Empty namespaces are
# valid while bypass is active; the operator must match REDIS_DB to the app.
for i in "${!patterns[@]}"; do
  pattern=${patterns[$i]}
  redis --scan --pattern "$pattern" > "$workdir/keys-$i"
  count=$(wc -l < "$workdir/keys-$i")
  [[ $count -le 100000 ]] || { echo 'Unexpected namespace size' >&2; exit 1; }
  while IFS= read -r key; do
    [[ $key == ${pattern%\*}* ]] || exit 1
  done < "$workdir/keys-$i"
done
publish_invalidations() {
  while IFS= read -r hash; do
    printf 'PUBLISH auth:cache:invalidate %s\n' "$hash"
  done < "$workdir/hashes" | docker exec -i -e REDISCLI_AUTH \
    sub2api-migration-redis-public redis-cli --no-auth-warning -n "$REDIS_DB" >/dev/null
}
publish_invalidations
printf 'target Redis database=%s\n' "$REDIS_DB"
for i in "${!patterns[@]}"; do
  pattern=${patterns[$i]}
  count=$(wc -l < "$workdir/keys-$i")
  batch=()
  while IFS= read -r key; do
    [[ $key == ${pattern%\*}* ]] || exit 1
    batch+=("$key")
    if [[ ${#batch[@]} -ge 100 ]]; then
      redis UNLINK "${batch[@]}" >/dev/null
      batch=()
    fi
  done < "$workdir/keys-$i"
  if [[ ${#batch[@]} -gt 0 ]]; then redis UNLINK "${batch[@]}" >/dev/null; fi
  printf 'refreshed namespace=%s scanned=%d\n' "$pattern" "$count"
done
publish_invalidations
echo 'PASS: scoped monetary/auth cache refresh; subscription cache preserved'
