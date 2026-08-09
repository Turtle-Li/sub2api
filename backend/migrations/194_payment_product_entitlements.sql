-- Structured subscription entitlements and immutable order product snapshots.
-- Existing plans/orders remain compatible with empty JSON objects.

ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS entitlements JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE payment_orders
    ADD COLUMN IF NOT EXISTS product_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_subscription_plans_entitlements
    ON subscription_plans USING GIN (entitlements);
