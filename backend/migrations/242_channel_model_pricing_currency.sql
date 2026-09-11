-- Model price-card currency is explicit. Existing price cards retain their
-- legacy USD interpretation through the additive default; no price values are
-- rewritten by this migration.
ALTER TABLE channel_model_pricing
    ADD COLUMN IF NOT EXISTS currency VARCHAR(3) NOT NULL DEFAULT 'USD';

ALTER TABLE channel_account_stats_model_pricing
    ADD COLUMN IF NOT EXISTS currency VARCHAR(3) NOT NULL DEFAULT 'USD';

DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_channel_model_pricing_currency'
          AND conrelid = 'channel_model_pricing'::regclass
    ) THEN
        ALTER TABLE channel_model_pricing
            ADD CONSTRAINT chk_channel_model_pricing_currency
            CHECK (currency IN ('USD', 'CNY')) NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_channel_account_stats_model_pricing_currency'
          AND conrelid = 'channel_account_stats_model_pricing'::regclass
    ) THEN
        ALTER TABLE channel_account_stats_model_pricing
            ADD CONSTRAINT chk_channel_account_stats_model_pricing_currency
            CHECK (currency IN ('USD', 'CNY')) NOT VALID;
    END IF;
END $$;

COMMENT ON COLUMN channel_model_pricing.currency IS
    'Currency for the entire model price card and all of its intervals; USD or CNY';
COMMENT ON COLUMN channel_account_stats_model_pricing.currency IS
    'Currency for the entire account-stats model price card and all of its intervals; USD or CNY';
