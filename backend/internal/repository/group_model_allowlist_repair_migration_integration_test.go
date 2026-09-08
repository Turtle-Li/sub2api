//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const groupModelAllowlistRepairMigration = "236_group_model_allowlist_repair.sql"

// 236 keeps the fork's rolling-upgrade storage contract: both Ent and old
// binaries use groups.models_list_config. It repairs databases that previously
// accepted upstream's physical model_allowlist column without letting it
// overwrite canonical policy data.
func TestMigration236PreservesLegacyCanonicalStorage(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()

	// Exercise nullable/no-default recovery while preserving every non-SQL-NULL
	// JSON value, including disabled policies, {}, and JSON null.
	_, err := tx.ExecContext(ctx, `
ALTER TABLE groups ALTER COLUMN models_list_config DROP NOT NULL;
ALTER TABLE groups ALTER COLUMN models_list_config DROP DEFAULT;
`)
	require.NoError(t, err)

	var disabledID, emptyID, jsonNullID, sqlNullID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform, rate_multiplier, status, models_list_config)
VALUES ('migration-236-legacy-disabled', 'anthropic', 1, 'active', '{"enabled":false,"models":["gpt-5.5"]}'::jsonb)
RETURNING id
`).Scan(&disabledID))
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform, rate_multiplier, status, models_list_config)
VALUES ('migration-236-legacy-empty', 'anthropic', 1, 'active', '{}'::jsonb)
RETURNING id
`).Scan(&emptyID))
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform, rate_multiplier, status, models_list_config)
VALUES ('migration-236-legacy-json-null', 'anthropic', 1, 'active', 'null'::jsonb)
RETURNING id
`).Scan(&jsonNullID))
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform, rate_multiplier, status, models_list_config)
VALUES ('migration-236-legacy-sql-null', 'anthropic', 1, 'active', NULL)
RETURNING id
`).Scan(&sqlNullID))

	applyGroupModelAllowlistRepair(ctx, t, tx)

	requireGroupCanonicalJSON(ctx, t, tx, disabledID, `{"enabled":false,"models":["gpt-5.5"]}`)
	requireGroupCanonicalJSON(ctx, t, tx, emptyID, `{}`)
	requireGroupCanonicalJSON(ctx, t, tx, jsonNullID, `null`)
	requireGroupCanonicalJSON(ctx, t, tx, sqlNullID, `{}`)
	requireColumnPresence(ctx, t, tx, "models_list_config", true)
	requireColumnPresence(ctx, t, tx, "model_allowlist", false)
	requireCanonicalModelAllowlistColumnShape(ctx, t, tx)
}

func TestMigration236PreservesCanonicalDataWhenBothColumnsExist(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()

	_, err := tx.ExecContext(ctx,
		"ALTER TABLE groups ADD COLUMN model_allowlist JSONB NOT NULL DEFAULT '{}'::jsonb")
	require.NoError(t, err)

	// {} is a stored canonical policy, not evidence that an alternate column is
	// newer. A stale alternate must never turn it into an enabled allowlist.
	var emptyCanonicalID, disabledCanonicalID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform, rate_multiplier, status, models_list_config, model_allowlist)
VALUES ('migration-236-both-empty-canonical', 'anthropic', 1, 'active', '{}'::jsonb, '{"enabled":true,"models":["stale-model"]}'::jsonb)
RETURNING id
`).Scan(&emptyCanonicalID))
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform, rate_multiplier, status, models_list_config, model_allowlist)
VALUES ('migration-236-both-disabled-canonical', 'anthropic', 1, 'active', '{"enabled":false,"models":["gpt-5.6-sol"]}'::jsonb, '{"enabled":true,"models":["different-stale-model"]}'::jsonb)
RETURNING id
`).Scan(&disabledCanonicalID))

	applyGroupModelAllowlistRepair(ctx, t, tx)

	requireGroupCanonicalJSON(ctx, t, tx, emptyCanonicalID, `{}`)
	requireGroupCanonicalJSON(ctx, t, tx, disabledCanonicalID, `{"enabled":false,"models":["gpt-5.6-sol"]}`)
	requireGroupAlternateJSON(ctx, t, tx, emptyCanonicalID, `{"enabled":true,"models":["stale-model"]}`)
	requireGroupAlternateJSON(ctx, t, tx, disabledCanonicalID, `{"enabled":true,"models":["different-stale-model"]}`)
	requireColumnPresence(ctx, t, tx, "models_list_config", true)
	requireColumnPresence(ctx, t, tx, "model_allowlist", true)
	requireCanonicalModelAllowlistColumnShape(ctx, t, tx)
}

func TestMigration236RestoresCanonicalColumnFromAlternateOnlyAndIsRepeatable(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()

	_, err := tx.ExecContext(ctx, `
ALTER TABLE groups RENAME COLUMN models_list_config TO model_allowlist;
ALTER TABLE groups ALTER COLUMN model_allowlist DROP NOT NULL;
ALTER TABLE groups ALTER COLUMN model_allowlist DROP DEFAULT;
`)
	require.NoError(t, err)

	var configuredID, sqlNullID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform, rate_multiplier, status, model_allowlist)
VALUES ('migration-236-alternate-only', 'anthropic', 1, 'active', '{"enabled":true,"models":["upstream-only-model"]}'::jsonb)
RETURNING id
`).Scan(&configuredID))
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform, rate_multiplier, status, model_allowlist)
VALUES ('migration-236-alternate-only-null', 'anthropic', 1, 'active', NULL)
RETURNING id
`).Scan(&sqlNullID))

	applyGroupModelAllowlistRepair(ctx, t, tx)
	requireGroupCanonicalJSON(ctx, t, tx, configuredID, `{"enabled":true,"models":["upstream-only-model"]}`)
	requireGroupCanonicalJSON(ctx, t, tx, sqlNullID, `{}`)
	requireColumnPresence(ctx, t, tx, "models_list_config", true)
	requireColumnPresence(ctx, t, tx, "model_allowlist", false)
	requireCanonicalModelAllowlistColumnShape(ctx, t, tx)

	applyGroupModelAllowlistRepair(ctx, t, tx)
	requireGroupCanonicalJSON(ctx, t, tx, configuredID, `{"enabled":true,"models":["upstream-only-model"]}`)
	requireGroupCanonicalJSON(ctx, t, tx, sqlNullID, `{}`)
}

func TestMigration236CreatesCanonicalColumnWhenBothAreMissing(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()

	_, err := tx.ExecContext(ctx, "ALTER TABLE groups DROP COLUMN models_list_config")
	require.NoError(t, err)

	var groupID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform, rate_multiplier, status)
VALUES ('migration-236-neither-column', 'anthropic', 1, 'active')
RETURNING id
`).Scan(&groupID))

	applyGroupModelAllowlistRepair(ctx, t, tx)

	requireGroupCanonicalJSON(ctx, t, tx, groupID, `{}`)
	requireColumnPresence(ctx, t, tx, "models_list_config", true)
	requireColumnPresence(ctx, t, tx, "model_allowlist", false)
	requireCanonicalModelAllowlistColumnShape(ctx, t, tx)
}

func TestMigration236ResolvesGroupsThroughNonPublicSearchPath(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()

	_, err := tx.ExecContext(ctx, `
CREATE SCHEMA migration_236_nonpublic;
SET LOCAL search_path TO migration_236_nonpublic, pg_catalog;
CREATE TABLE groups (
    id BIGSERIAL PRIMARY KEY,
    model_allowlist JSONB
);
INSERT INTO groups (model_allowlist)
VALUES ('{"enabled":false,"models":["nonpublic-model"]}'::jsonb);
`)
	require.NoError(t, err)

	applyGroupModelAllowlistRepair(ctx, t, tx)

	requireGroupCanonicalJSON(ctx, t, tx, 1, `{"enabled":false,"models":["nonpublic-model"]}`)
	requireColumnPresence(ctx, t, tx, "models_list_config", true)
	requireColumnPresence(ctx, t, tx, "model_allowlist", false)
	requireCanonicalModelAllowlistColumnShape(ctx, t, tx)
}

func applyGroupModelAllowlistRepair(ctx context.Context, t *testing.T, tx *sql.Tx) {
	t.Helper()

	migrationSQL, err := dbmigrations.FS.ReadFile(groupModelAllowlistRepairMigration)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
}

func requireGroupCanonicalJSON(ctx context.Context, t *testing.T, tx *sql.Tx, groupID int64, expected string) {
	t.Helper()

	var actual string
	require.NoError(t, tx.QueryRowContext(ctx,
		"SELECT models_list_config::text FROM groups WHERE id = $1", groupID).Scan(&actual))
	require.JSONEq(t, expected, actual)
}

func requireGroupAlternateJSON(ctx context.Context, t *testing.T, tx *sql.Tx, groupID int64, expected string) {
	t.Helper()

	var actual string
	require.NoError(t, tx.QueryRowContext(ctx,
		"SELECT model_allowlist::text FROM groups WHERE id = $1", groupID).Scan(&actual))
	require.JSONEq(t, expected, actual)
}

func requireColumnPresence(ctx context.Context, t *testing.T, tx *sql.Tx, column string, want bool) {
	t.Helper()

	var exists bool
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM pg_attribute
    WHERE attrelid = 'groups'::regclass
      AND attname = $1
      AND NOT attisdropped
)
`, column).Scan(&exists))
	require.Equal(t, want, exists, "column %s presence", column)
}

func requireCanonicalModelAllowlistColumnShape(ctx context.Context, t *testing.T, tx *sql.Tx) {
	t.Helper()

	var notNull bool
	var columnDefault sql.NullString
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT a.attnotnull, pg_get_expr(d.adbin, d.adrelid)
FROM pg_attribute a
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE a.attrelid = 'groups'::regclass
  AND a.attname = 'models_list_config'
  AND NOT a.attisdropped
`).Scan(&notNull, &columnDefault))
	require.True(t, notNull)
	require.True(t, columnDefault.Valid)
	require.Contains(t, columnDefault.String, "'{}'::jsonb")
}
