-- Durable internal-credit purchases of exactly one subscription reset card.
-- This table is intentionally separate from payment_orders: no external
-- provider transaction is created or faked for a balance-only debit.

CREATE TABLE IF NOT EXISTS subscription_reset_card_purchases (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id) ON DELETE RESTRICT,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
    plan_id BIGINT NOT NULL REFERENCES subscription_plans(id) ON DELETE RESTRICT,
    price DECIMAL(20,2) NOT NULL,
    purchase_key UUID NOT NULL,
    grant_id BIGINT NOT NULL REFERENCES subscription_reset_grants(id) ON DELETE RESTRICT,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT subscription_reset_card_purchases_price_positive CHECK (price > 0),
    CONSTRAINT subscription_reset_card_purchases_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT subscription_reset_card_purchases_user_purchase_key_unique UNIQUE (user_id, purchase_key),
    CONSTRAINT subscription_reset_card_purchases_grant_unique UNIQUE (grant_id)
);

CREATE INDEX IF NOT EXISTS idx_subscription_reset_card_purchases_subscription_created
    ON subscription_reset_card_purchases (subscription_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_subscription_reset_card_purchases_grant
    ON subscription_reset_card_purchases (grant_id);
