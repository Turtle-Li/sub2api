-- 258_account_pools.sql
-- 账号池：仅用于后台折叠管理批量账号，不参与调度与计费。
CREATE TABLE IF NOT EXISTS account_pools (
    id         BIGSERIAL PRIMARY KEY,
    name       VARCHAR(100) NOT NULL,
    platform   VARCHAR(50)  NOT NULL,
    notes      TEXT,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_account_pools_platform ON account_pools (platform);

ALTER TABLE accounts ADD COLUMN IF NOT EXISTS pool_id BIGINT;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_accounts_pool_id') THEN
    ALTER TABLE accounts ADD CONSTRAINT fk_accounts_pool_id
      FOREIGN KEY (pool_id) REFERENCES account_pools(id) ON DELETE SET NULL NOT VALID;
  END IF;
END $$;

-- 新列全为 NULL，校验不需要扫描有效数据。
ALTER TABLE accounts VALIDATE CONSTRAINT fk_accounts_pool_id;
