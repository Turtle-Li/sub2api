#!/usr/bin/env bash
set -euo pipefail
# Prove migration 235 preserves old readers and configuration on PostgreSQL.
root=$(cd "$(dirname "$0")/../.." && pwd)
container="sub2api-allowlist-migration-test-$$"
cleanup() { docker rm -f "$container" >/dev/null; }
trap cleanup EXIT
docker run -d --name "$container" --network none \
  -e POSTGRES_HOST_AUTH_METHOD=trust postgres:18-alpine >/dev/null
ready=false
for ((i=0; i<60; i++)); do
  if docker exec "$container" pg_isready -U postgres >/dev/null 2>&1; then ready=true; break; fi
  sleep 1
done
[[ "$ready" == true ]]
psql() { docker exec -i "$container" psql -X -U postgres -v ON_ERROR_STOP=1 "$@"; }
psql <<'SQL'
CREATE TABLE groups (id BIGSERIAL PRIMARY KEY, models_list_config JSONB NOT NULL DEFAULT '{}'::jsonb);
INSERT INTO groups (models_list_config) VALUES ('{"enabled":false,"models":["gpt-5.5"]}'), ('{"enabled":true,"models":["gpt-6-*"]}');
SQL
psql < "$root/backend/migrations/235_group_model_allowlist.sql"
psql < "$root/backend/migrations/235_group_model_allowlist.sql"
psql <<'SQL'
DO $$ BEGIN
  IF (SELECT models_list_config FROM groups WHERE id=1) <> '{"enabled":false,"models":["gpt-5.5"]}'::jsonb
     OR (SELECT models_list_config FROM groups WHERE id=2) <> '{"enabled":true,"models":["gpt-6-*"]}'::jsonb THEN
    RAISE EXCEPTION 'migration changed the stored policy';
  END IF;
END $$;
UPDATE groups SET models_list_config='{"enabled":false,"models":["old-update"]}' WHERE id=1;
INSERT INTO groups DEFAULT VALUES;
SELECT models_list_config FROM groups ORDER BY id;
SQL
echo 'PASS: migration is repeatable; old SQL and existing enabled/disabled policies survive'
