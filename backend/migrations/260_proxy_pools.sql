-- 代理池只管理未来的自动分配；账号固定出口仍由 accounts.proxy_id 持久化。
CREATE TABLE IF NOT EXISTS proxy_pools (
    id         BIGSERIAL PRIMARY KEY,
    name       VARCHAR(100) NOT NULL,
    notes      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE proxies ADD COLUMN IF NOT EXISTS pool_id BIGINT;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_proxies_pool_id') THEN
    ALTER TABLE proxies ADD CONSTRAINT fk_proxies_pool_id
      FOREIGN KEY (pool_id) REFERENCES proxy_pools(id) ON DELETE SET NULL NOT VALID;
  END IF;
END $$;

ALTER TABLE proxies VALIDATE CONSTRAINT fk_proxies_pool_id;
