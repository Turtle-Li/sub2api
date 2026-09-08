package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupModelAllowlistRepairMigration(t *testing.T) {
	content, err := FS.ReadFile("236_group_model_allowlist_repair.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")

	// 236 必须回到 Ent 和排空中的旧二进制共同使用的物理列。
	require.Contains(t, sql, "ALTER TABLE groups RENAME COLUMN model_allowlist TO models_list_config")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS models_list_config JSONB NOT NULL DEFAULT '{}'::jsonb")
	require.Contains(t, sql, "UPDATE groups SET models_list_config = '{}'::jsonb WHERE models_list_config IS NULL")
	require.Contains(t, sql, "ALTER TABLE groups ALTER COLUMN models_list_config SET DEFAULT '{}'::jsonb")
	require.Contains(t, sql, "ALTER TABLE groups ALTER COLUMN models_list_config SET NOT NULL")
	require.Contains(t, sql, "COMMENT ON COLUMN groups.models_list_config")

	// 两列同时存在时，canonical 列即使是 {} 也不能被备用列覆盖。
	require.NotContains(t, sql, "RENAME COLUMN models_list_config TO model_allowlist")
	require.NotContains(t, sql, "SET models_list_config = model_allowlist")

	// 迁移必须跟随 ALTER TABLE 使用的 search_path，不能写死 public。
	require.NotContains(t, sql, "table_schema = 'public'")
	require.Contains(t, sql, "attrelid = 'groups'::regclass")
}
