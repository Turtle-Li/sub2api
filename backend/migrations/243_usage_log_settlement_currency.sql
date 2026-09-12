-- Customer usage-log amounts carry the settlement currency that was active when
-- the row was written. The additive default preserves the legacy USD meaning
-- without rewriting any historical amount.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS currency VARCHAR(3) NOT NULL DEFAULT 'USD';

DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_usage_logs_currency'
          AND conrelid = 'usage_logs'::regclass
    ) THEN
        ALTER TABLE usage_logs
            ADD CONSTRAINT chk_usage_logs_currency
            CHECK (currency IN ('USD', 'CNY')) NOT VALID;
    END IF;
END $$;

COMMENT ON COLUMN usage_logs.currency IS
    'Customer cost currency (USD or CNY); account_stats_cost remains USD';
