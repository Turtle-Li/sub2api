-- Product-side durable correlation and finalization; central payment service
-- remains authoritative for channel funds and channel refund reservations.
CREATE TABLE IF NOT EXISTS unified_payment_refund_attempts (
    product_refund_no VARCHAR(64) PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES payment_orders(id) ON DELETE RESTRICT,
    payment_order_id UUID NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL UNIQUE,
    environment VARCHAR(16) NOT NULL CHECK (environment IN ('sandbox', 'live')),
    organization_id UUID NOT NULL,
    product_id UUID NOT NULL,
    app_id VARCHAR(80) NOT NULL,
    payment_method VARCHAR(16) NOT NULL CHECK (payment_method IN ('alipay', 'wechat_pay')),
    amount_fen BIGINT NOT NULL CHECK (amount_fen > 0),
    balance_amount_minor BIGINT NOT NULL CHECK (balance_amount_minor > 0),
    deduct_balance BOOLEAN NOT NULL,
    force_refund BOOLEAN NOT NULL,
    reason_summary VARCHAR(200) NOT NULL,
    status VARCHAR(16) NOT NULL CHECK (status IN ('PENDING', 'SUCCEEDED', 'FAILED')),
    refund_request_id UUID UNIQUE,
    channel_out_refund_no VARCHAR(64) UNIQUE,
    provider_refund_id VARCHAR(160),
    needs_manual_review BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS unified_payment_refund_one_active_order
    ON unified_payment_refund_attempts (order_id)
    WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS unified_payment_refund_order_history
    ON unified_payment_refund_attempts (order_id, created_at DESC);

-- The legacy payment_audit_logs table has UNIQUE(order_id, action) for
-- fulfillment. Preserve that fence; refund attempts need append-only history.
CREATE TABLE IF NOT EXISTS unified_payment_refund_events (
    id UUID PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES payment_orders(id) ON DELETE RESTRICT,
    action VARCHAR(50) NOT NULL,
    detail TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS unified_payment_refund_events_order_history
    ON unified_payment_refund_events (order_id, created_at DESC);

COMMENT ON TABLE unified_payment_refund_attempts IS
    'Stable product refund requests and trusted result correlation; never stores credentials or raw channel callbacks.';
