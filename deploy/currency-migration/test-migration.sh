#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
# Require the local Docker Desktop context; never run fixtures in a remote context.
[[ $(docker context show) == desktop-linux ]] || { echo 'Use local Docker Desktop for this fixture'; exit 1; }
name="sub2-currency-fixture-$$"
trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT
docker run -d --name "$name" -e POSTGRES_HOST_AUTH_METHOD=trust postgres:17-alpine >/dev/null
for i in {1..30}; do
  if docker exec "$name" pg_isready -U postgres >/dev/null 2>&1; then break; fi
  sleep 1
done
sql() { docker exec -i "$name" psql -X -U postgres -v ON_ERROR_STOP=1 "$@"; }
sql < test-fixture.sql >/dev/null
sql -v apply=false < wallet-to-cny.sql >/dev/null
[[ $(sql -Atc 'SELECT balance FROM users WHERE id=1') == 100.00000000 ]]
sql -v apply=true < wallet-to-cny.sql >/dev/null
sql < verify-fixture.sql >/dev/null
if sql -v apply=true < wallet-to-cny.sql >/dev/null 2>&1; then echo 'ERROR: repeated migration accepted'; exit 1; fi
sql < rollback-before-reopen.sql >/dev/null
[[ $(sql -Atc 'SELECT balance FROM users WHERE id=1') == 100.00000000 ]]
[[ $(sql -Atc "SELECT count(*) FROM settings WHERE key='pricing_currency_settings'") == 0 ]]
echo 'PASS: dry-run, conversion, rounding, subscription/discount/history preservation, repeat rejection, exact rollback'
