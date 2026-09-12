#!/usr/bin/env bash
# Local PostgreSQL concurrency rehearsal for the fenced USD -> CNY cutover.
# It uses only synthetic fixture rows and refuses every Docker context except
# Docker Desktop's local desktop-linux context.
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
[[ $(docker context show) == desktop-linux ]] || {
  echo 'Use local Docker Desktop (desktop-linux) for this fixture' >&2
  exit 1
}

container="sub2-currency-online-migration-$$"
tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/sub2-currency-online-migration.XXXXXX")
pre_debit_pid=''
migration_pid=''

cleanup() {
  local pid
  for pid in "${pre_debit_pid:-}" "${migration_pid:-}"; do
    [[ -z $pid ]] || kill "$pid" >/dev/null 2>&1 || true
  done
  docker rm -f "$container" >/dev/null 2>&1 || true
  rm -rf "$tmpdir"
}
trap cleanup EXIT

docker run -d --name "$container" --network none \
  -e POSTGRES_HOST_AUTH_METHOD=trust postgres:17-alpine >/dev/null

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

assert_eq() {
  local expected=$1 actual=$2 label=$3
  [[ $actual == "$expected" ]] || fail "$label: expected $expected, got $actual"
}

# PGAPPNAME makes the lock-wait observation deterministic without exposing a
# connection string or using a remote database.
sql_app() {
  local app_name=$1
  shift
  docker exec -e "PGAPPNAME=$app_name" -i "$container" \
    psql -X -U postgres -v ON_ERROR_STOP=1 "$@"
}

sql() {
  sql_app currency_online_migration_assertions "$@"
}

scalar() {
  sql -Atc "$1"
}

balance() {
  scalar 'SELECT balance::numeric(20,8) FROM users WHERE id = 1'
}

settlement_currency() {
  scalar "SELECT COALESCE((SELECT value::jsonb->>'settlement_currency' FROM settings WHERE key = 'pricing_currency_settings'), 'USD')"
}

recharge_multiplier() {
  scalar "SELECT value::numeric(20,8) FROM settings WHERE key = 'BALANCE_RECHARGE_MULTIPLIER'"
}

atomic_deduct() {
  local amount=$1
  scalar "WITH deducted AS (
    UPDATE users
      SET balance = balance - ${amount}::numeric,
          updated_at = NOW()
      WHERE id = 1
        AND deleted_at IS NULL
        AND balance >= ${amount}::numeric
      RETURNING balance
  )
  SELECT balance::numeric(20,8) FROM deducted"
}

reset_fixture() {
  sql -c 'DROP SCHEMA IF EXISTS currency_cutover_20260912 CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public; GRANT ALL ON SCHEMA public TO postgres;' >/dev/null 2>&1
  sql < "$script_dir/test-fixture.sql" >/dev/null
  # Match the fields used by the repository's atomic users.balance debit.
  sql -c 'ALTER TABLE users ADD COLUMN deleted_at timestamptz, ADD COLUMN updated_at timestamptz;' >/dev/null
}

apply_migration() {
  sql_app currency_online_migration -v recharge_factor=1 -v apply=true \
    < "$script_dir/wallet-to-cny.sql"
}

wait_for_pre_debit() {
  local _
  for _ in {1..50}; do
    if [[ $(scalar "SELECT count(*) FROM pg_stat_activity WHERE application_name = 'currency_online_pre_usd_debit' AND state = 'active' AND query LIKE 'SELECT pg_sleep%'") == 1 ]]; then
      return
    fi
    sleep 0.1
  done
  fail 'pre-cutover debit did not reach its held transaction'
}

wait_for_migration_lock() {
  local _
  for _ in {1..25}; do
    if [[ $(scalar "SELECT count(*) FROM pg_stat_activity WHERE application_name = 'currency_online_migration' AND wait_event_type = 'Lock'") == 1 ]]; then
      return
    fi
    sleep 0.1
  done
  fail 'migration did not wait behind the atomic balance debit'
}

for _ in {1..30}; do
  if docker exec "$container" pg_isready -U postgres >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker exec "$container" pg_isready -U postgres >/dev/null 2>&1 || fail 'local PostgreSQL fixture did not become ready'

# An old USD debit begins first and holds the row lock. The migration must wait,
# while other sessions still see the USD state. Once the debit commits, the
# migration must multiply the resulting USD principal exactly once.
reset_fixture
assert_eq 100.00000000 "$(balance)" 'initial wallet balance'

sql_app currency_online_pre_usd_debit >"$tmpdir/pre-usd-debit.log" 2>&1 <<'SQL' &
BEGIN;
UPDATE users SET balance = balance - 10.00000000, updated_at = NOW()
WHERE id = 1 AND deleted_at IS NULL AND balance >= 10.00000000;
SELECT pg_sleep(4);
COMMIT;
SQL
pre_debit_pid=$!
wait_for_pre_debit

sql_app currency_online_migration -v recharge_factor=1 -v apply=true \
  < "$script_dir/wallet-to-cny.sql" >"$tmpdir/migration.log" 2>&1 &
migration_pid=$!
wait_for_migration_lock

# The blocked migration has not leaked its CNY setting or converted balance.
assert_eq 100.00000000 "$(balance)" 'balance visibility before migration commit'
assert_eq USD "$(settlement_currency)" 'currency visibility before migration commit'

wait "$pre_debit_pid"
pre_debit_pid=''
wait "$migration_pid"
migration_pid=''

assert_eq 607.50000000 "$(balance)" 'USD debit committed before migration converts once'
assert_eq CNY "$(settlement_currency)" 'currency commits with converted wallet'
assert_eq 1.00000000 "$(recharge_multiplier)" 'future recharge remains one CNY credit per CNY paid'
assert_eq '90.00000000:607.50000000' "$(scalar "SELECT old_value::numeric(20,8)::text || ':' || new_value::numeric(20,8)::text FROM currency_cutover_20260912.amounts WHERE table_name = 'users' AND row_id = 1 AND column_name = 'balance'")" 'migration snapshot includes the prior USD debit'
echo 'PASS: pre-cutover atomic USD debit blocks migration; committed principal is converted once with the CNY setting'

# A legacy request whose USD amount reaches the old atomic debit after the
# cutover is deliberately charged by its unconverted numeric value. This is
# the approved short transition behavior, and must not silently multiply again.
reset_fixture
apply_migration >/dev/null
assert_eq 675.00000000 "$(balance)" 'post-migration wallet balance'
assert_eq CNY "$(settlement_currency)" 'post-migration currency'
assert_eq 665.00000000 "$(atomic_deduct 10.00000000)" 'post-cutover legacy USD numeric debit'
assert_eq 665.00000000 "$(balance)" 'legacy USD numeric debit persists without a second conversion'
echo 'PASS: a post-cutover legacy USD amount deducts its original numeric value (temporary undercharge), without a second conversion'

# A CNY-priced request after commit uses its CNY amount exactly. Reapplying the
# migration must fail and must leave that new CNY write intact.
reset_fixture
apply_migration >/dev/null
assert_eq 607.50000000 "$(atomic_deduct 67.50000000)" 'post-cutover CNY debit'
if apply_migration >"$tmpdir/repeat.log" 2>&1; then
  fail 'repeated migration was accepted'
fi
assert_eq 607.50000000 "$(balance)" 'repeat rejection preserves post-cutover CNY debit'
assert_eq CNY "$(settlement_currency)" 'repeat rejection preserves CNY setting'
echo 'PASS: post-cutover CNY debit is exact and repeated migration is rejected without mutating it'
