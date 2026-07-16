-- Grantable, expiring reset-card batches for subscription usage windows.
-- Repeated grants create separate rows so quantities stack while each batch
-- retains its own expiry and audit metadata.

CREATE TABLE IF NOT EXISTS subscription_reset_grants (
    id BIGSERIAL PRIMARY KEY,
    subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    quantity INTEGER NOT NULL,
    used_count INTEGER NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ NOT NULL,
    issued_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT subscription_reset_grants_quantity_valid CHECK (quantity BETWEEN 1 AND 1000),
    CONSTRAINT subscription_reset_grants_used_count_valid CHECK (used_count >= 0 AND used_count <= quantity),
    CONSTRAINT subscription_reset_grants_expiry_valid CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS idx_subscription_reset_grants_available
    ON subscription_reset_grants (subscription_id, expires_at, id)
    WHERE used_count < quantity;

CREATE INDEX IF NOT EXISTS idx_subscription_reset_grants_user_expiry
    ON subscription_reset_grants (user_id, expires_at);

CREATE INDEX IF NOT EXISTS idx_subscription_reset_grants_group_created
    ON subscription_reset_grants (group_id, created_at DESC);
