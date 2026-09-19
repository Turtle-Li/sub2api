-- Payment discounts are a financial admission ledger.  The tables deliberately
-- stay outside Ent: order creation, provider callbacks, and cancellation use
-- one transaction and lock the coupon parent before changing capacity.

CREATE TABLE IF NOT EXISTS payment_discount_codes (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(32) NOT NULL UNIQUE,
    discount_type VARCHAR(16) NOT NULL,
    discount_value NUMERIC(20,8) NOT NULL,
    currency CHAR(3) NOT NULL,
    max_uses BIGINT NOT NULL DEFAULT 0,
    per_user_max_uses BIGINT NOT NULL DEFAULT 1,
    target_user_id BIGINT NULL REFERENCES users(id) ON DELETE RESTRICT,
    starts_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    version BIGINT NOT NULL DEFAULT 1,
    notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT payment_discount_codes_code_valid CHECK (
        code ~ '^[A-Z0-9_-]{8,32}$'
    ),
    CONSTRAINT payment_discount_codes_kind_valid CHECK (
        discount_type IN ('fixed', 'percent')
    ),
    CONSTRAINT payment_discount_codes_value_valid CHECK (
        discount_value > 0
        AND discount_value = trunc(discount_value, 2)
        AND (discount_type <> 'percent' OR discount_value <= 100)
    ),
    CONSTRAINT payment_discount_codes_currency_valid CHECK (
        currency IN ('CNY', 'USD')
    ),
    CONSTRAINT payment_discount_codes_caps_valid CHECK (
        max_uses >= 0 AND per_user_max_uses >= 0
    ),
    CONSTRAINT payment_discount_codes_schedule_valid CHECK (
        starts_at < expires_at
    ),
    CONSTRAINT payment_discount_codes_version_valid CHECK (
        version > 0
    ),
    CONSTRAINT payment_discount_codes_notes_valid CHECK (
        char_length(notes) <= 2000
    )
);

CREATE INDEX IF NOT EXISTS idx_payment_discount_codes_active_window
    ON payment_discount_codes (enabled, starts_at, expires_at);

CREATE INDEX IF NOT EXISTS idx_payment_discount_codes_target_user
    ON payment_discount_codes (target_user_id)
    WHERE target_user_id IS NOT NULL;

-- Every administrative mutation writes one immutable record.  Its RESTRICT
-- foreign key intentionally prevents a code from ever being hard-deleted.
CREATE TABLE IF NOT EXISTS payment_discount_code_audits (
    id BIGSERIAL PRIMARY KEY,
    code_id BIGINT NOT NULL REFERENCES payment_discount_codes(id) ON DELETE RESTRICT,
    admin_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    action VARCHAR(32) NOT NULL,
    detail JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT payment_discount_code_audits_action_valid CHECK (
        action IN ('created', 'updated')
    ),
    CONSTRAINT payment_discount_code_audits_detail_valid CHECK (
        jsonb_typeof(detail) = 'object'
    )
);

CREATE INDEX IF NOT EXISTS idx_payment_discount_code_audits_history
    ON payment_discount_code_audits (code_id, id DESC);

-- A use is created with the order and retains the exact quote plus the
-- provider response.  `released` does not consume capacity; a late trusted
-- payment can only move it back to `consumed` if the current hard caps permit.
CREATE TABLE IF NOT EXISTS payment_discount_uses (
    id BIGSERIAL PRIMARY KEY,
    code_id BIGINT NOT NULL REFERENCES payment_discount_codes(id) ON DELETE RESTRICT,
    order_id BIGINT NOT NULL UNIQUE REFERENCES payment_orders(id) ON DELETE RESTRICT,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    status VARCHAR(16) NOT NULL DEFAULT 'reserved',
    original_amount NUMERIC(20,8) NOT NULL,
    discount_amount NUMERIC(20,8) NOT NULL,
    pay_amount NUMERIC(20,8) NOT NULL,
    currency CHAR(3) NOT NULL,
    quote_version BIGINT NOT NULL,
    quote_revision VARCHAR(64) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    idempotency_hash VARCHAR(64) NOT NULL,
    quote_snapshot JSONB NOT NULL,
    response_snapshot JSONB NULL,
    reserved_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    consumed_at TIMESTAMPTZ NULL,
    released_at TIMESTAMPTZ NULL,
    paid_review_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT payment_discount_uses_status_valid CHECK (
        status IN ('reserved', 'consumed', 'released', 'paid_review')
    ),
    CONSTRAINT payment_discount_uses_money_valid CHECK (
        original_amount > 0
        AND discount_amount >= 0
        AND pay_amount > 0
        AND original_amount = trunc(original_amount, 2)
        AND discount_amount = trunc(discount_amount, 2)
        AND pay_amount = trunc(pay_amount, 2)
        AND original_amount = discount_amount + pay_amount
    ),
    CONSTRAINT payment_discount_uses_currency_valid CHECK (
        currency IN ('CNY', 'USD')
    ),
    CONSTRAINT payment_discount_uses_quote_version_valid CHECK (
        quote_version > 0
    ),
    CONSTRAINT payment_discount_uses_quote_revision_valid CHECK (
        quote_revision ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT payment_discount_uses_request_hash_valid CHECK (
        request_hash ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT payment_discount_uses_idempotency_hash_valid CHECK (
        idempotency_hash ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT payment_discount_uses_quote_snapshot_valid CHECK (
        jsonb_typeof(quote_snapshot) = 'object'
    ),
    CONSTRAINT payment_discount_uses_response_snapshot_valid CHECK (
        response_snapshot IS NULL OR jsonb_typeof(response_snapshot) = 'object'
    ),
    CONSTRAINT payment_discount_uses_transition_timestamps_valid CHECK (
        (status = 'reserved'
            AND consumed_at IS NULL AND released_at IS NULL AND paid_review_at IS NULL)
        OR (status = 'consumed'
            AND consumed_at IS NOT NULL AND paid_review_at IS NULL)
        OR (status = 'released'
            AND consumed_at IS NULL AND released_at IS NOT NULL AND paid_review_at IS NULL)
        OR (status = 'paid_review'
            AND consumed_at IS NULL AND released_at IS NOT NULL AND paid_review_at IS NOT NULL)
    ),
    CONSTRAINT payment_discount_uses_transition_order_valid CHECK (
        (consumed_at IS NULL OR consumed_at >= reserved_at)
        AND (released_at IS NULL OR released_at >= reserved_at)
        AND (paid_review_at IS NULL OR paid_review_at >= released_at)
    ),
    CONSTRAINT payment_discount_uses_idempotency_unique UNIQUE (user_id, idempotency_hash)
);

CREATE INDEX IF NOT EXISTS idx_payment_discount_uses_capacity
    ON payment_discount_uses (code_id, status);

CREATE INDEX IF NOT EXISTS idx_payment_discount_uses_user_capacity
    ON payment_discount_uses (code_id, user_id, status);

CREATE INDEX IF NOT EXISTS idx_payment_discount_uses_history
    ON payment_discount_uses (code_id, id DESC);

-- One bounded database bucket applies to both quote and create attempts.  It
-- deliberately has a durable, per-user primary key so every API instance sees
-- the same atomic 20-attempt window.
CREATE TABLE IF NOT EXISTS payment_discount_rate_limits (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE RESTRICT,
    window_started_at TIMESTAMPTZ NOT NULL,
    attempt_count INTEGER NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT payment_discount_rate_limits_count_valid CHECK (
        attempt_count BETWEEN 0 AND 20
    )
);
