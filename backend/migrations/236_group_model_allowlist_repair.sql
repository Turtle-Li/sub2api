-- 236: 将 v0.2.3 上游列修复收敛回本 fork 的物理存储列。
--
-- Ent 的 model_allowlist 字段及仍在排空的旧/回滚二进制都读写
-- groups.models_list_config。某些已经执行过上游 235/236 的数据库只有
-- model_allowlist；若继续保留那种布局，当前 Ent 和旧二进制都会在查询
-- groups 时失败。
--
-- 本迁移可重放，并且用 regclass 解析表（跟随 search_path）：
--   1) 只有 models_list_config -> 保留其全部 JSON；
--   2) 只有 model_allowlist    -> 改名回 models_list_config，数据原样保留；
--   3) 两列并存                -> models_list_config 是权威列，绝不从备用列回填；
--   4) 两列都没有              -> 补建默认的 models_list_config。
-- 备用 model_allowlist 若已存在会被保留，但不是同步副本。
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_attribute
        WHERE attrelid = 'groups'::regclass
          AND attname = 'models_list_config'
          AND NOT attisdropped
    ) AND EXISTS (
        SELECT 1 FROM pg_attribute
        WHERE attrelid = 'groups'::regclass
          AND attname = 'model_allowlist'
          AND NOT attisdropped
    ) THEN
        ALTER TABLE groups RENAME COLUMN model_allowlist TO models_list_config;
    END IF;
END
$$;

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS models_list_config JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE groups
SET models_list_config = '{}'::jsonb
WHERE models_list_config IS NULL;

ALTER TABLE groups ALTER COLUMN models_list_config SET DEFAULT '{}'::jsonb;
ALTER TABLE groups ALTER COLUMN models_list_config SET NOT NULL;

COMMENT ON COLUMN groups.models_list_config IS
    'Group model allowlist; legacy storage name retained for rolling-upgrade compatibility';
