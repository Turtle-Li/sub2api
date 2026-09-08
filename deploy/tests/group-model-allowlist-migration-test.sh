#!/usr/bin/env bash
set -euo pipefail
# Prove migrations 235 and fork-compatible 236 preserve the shared physical
# storage used by old binaries and Ent's model_allowlist StorageKey.
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
psql < "$root/backend/migrations/236_group_model_allowlist_repair.sql"
psql < "$root/backend/migrations/235_group_model_allowlist.sql"
psql < "$root/backend/migrations/236_group_model_allowlist_repair.sql"
psql <<'SQL'
DO $$ BEGIN
  IF (SELECT models_list_config FROM groups WHERE id=1) <> '{"enabled":false,"models":["gpt-5.5"]}'::jsonb
     OR (SELECT models_list_config FROM groups WHERE id=2) <> '{"enabled":true,"models":["gpt-6-*"]}'::jsonb THEN
    RAISE EXCEPTION 'migration changed the stored policy';
  END IF;
END $$;

-- A draining legacy binary writes the physical column directly.
UPDATE groups SET models_list_config='{"enabled":false,"models":["old-update"]}' WHERE id=1;

-- The current Ent model_allowlist field has StorageKey("models_list_config").
UPDATE groups SET models_list_config='{"enabled":true,"models":["new-update"]}' WHERE id=2;
INSERT INTO groups DEFAULT VALUES;
DO $$ BEGIN
  IF (SELECT models_list_config FROM groups WHERE id=1) <> '{"enabled":false,"models":["old-update"]}'::jsonb
     OR (SELECT models_list_config FROM groups WHERE id=2) <> '{"enabled":true,"models":["new-update"]}'::jsonb
     OR (SELECT models_list_config FROM groups WHERE id=3) <> '{}'::jsonb THEN
    RAISE EXCEPTION 'old/new readers or writers no longer share canonical storage';
  END IF;
END $$;
SQL

# Simulate a database that previously accepted upstream's alternate physical
# column, then apply the repair twice. Canonical storage must be restored.
psql <<'SQL'
ALTER TABLE groups RENAME COLUMN models_list_config TO model_allowlist;
SQL
psql < "$root/backend/migrations/236_group_model_allowlist_repair.sql"
psql < "$root/backend/migrations/236_group_model_allowlist_repair.sql"
psql <<'SQL'
DO $$ BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_attribute
    WHERE attrelid = 'groups'::regclass
      AND attname = 'model_allowlist'
      AND NOT attisdropped
  ) OR (SELECT models_list_config FROM groups WHERE id=1) <> '{"enabled":false,"models":["old-update"]}'::jsonb THEN
    RAISE EXCEPTION 'alternate-only repair did not restore canonical storage';
  END IF;
END $$;

-- When both physical columns exist, canonical {} is intentional and must not
-- be replaced by a stale alternate enabled policy. The alternate remains.
ALTER TABLE groups ADD COLUMN model_allowlist JSONB NOT NULL DEFAULT '{}'::jsonb;
INSERT INTO groups (models_list_config, model_allowlist)
VALUES ('{}'::jsonb, '{"enabled":true,"models":["stale-model"]}'::jsonb);
SQL
psql < "$root/backend/migrations/236_group_model_allowlist_repair.sql"
psql <<'SQL'
DO $$ BEGIN
  IF (SELECT models_list_config FROM groups WHERE id=4) <> '{}'::jsonb
     OR (SELECT model_allowlist FROM groups WHERE id=4) <> '{"enabled":true,"models":["stale-model"]}'::jsonb THEN
    RAISE EXCEPTION 'both-column repair overwrote or synchronized stored policy';
  END IF;
END $$;
SELECT models_list_config FROM groups ORDER BY id;
SQL
echo 'PASS: migrations 235+236 are repeatable; old/new shared reads and canonical policies survive'
