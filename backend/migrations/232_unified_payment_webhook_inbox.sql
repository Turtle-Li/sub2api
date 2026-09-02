CREATE TABLE IF NOT EXISTS unified_payment_webhook_cursor (
    payment_order_id UUID PRIMARY KEY,
    product_order_no VARCHAR(64) NOT NULL UNIQUE,
    max_processed_sequence BIGINT NOT NULL DEFAULT 0
        CHECK (max_processed_sequence >= 0),
    active_event_id UUID,
    active_sequence BIGINT,
    active_updated_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT unified_payment_webhook_active_lease_complete CHECK (
        (active_event_id IS NULL AND active_sequence IS NULL AND active_updated_at IS NULL)
        OR
        (active_event_id IS NOT NULL AND active_sequence IS NOT NULL
            AND active_sequence > 0 AND active_updated_at IS NOT NULL)
    )
);

CREATE TABLE IF NOT EXISTS unified_payment_webhook_inbox (
    event_id UUID PRIMARY KEY,
    payment_order_id UUID NOT NULL,
    product_order_no VARCHAR(64) NOT NULL,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    event_type VARCHAR(64) NOT NULL,
    body_sha256 CHAR(64) NOT NULL CHECK (body_sha256 ~ '^[0-9a-f]{64}$'),
    status VARCHAR(24) NOT NULL CHECK (status IN (
        'PROCESSING', 'PROCESSED', 'RETRYABLE_FAILED', 'REJECTED'
    )),
    attempts INTEGER NOT NULL DEFAULT 1 CHECK (attempts > 0),
    last_error_code VARCHAR(80),
    occurred_at TIMESTAMPTZ NOT NULL,
    processed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT unified_payment_webhook_order_sequence_unique
        UNIQUE (payment_order_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_unified_payment_webhook_product_order
    ON unified_payment_webhook_inbox (product_order_no, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_unified_payment_webhook_retry
    ON unified_payment_webhook_inbox (status, updated_at)
    WHERE status IN ('PROCESSING', 'RETRYABLE_FAILED');

COMMENT ON TABLE unified_payment_webhook_inbox IS
    'Durable, metadata-only inbox for signed unified-payment Webhooks; raw callback bodies are never stored.';

COMMENT ON TABLE unified_payment_webhook_cursor IS
    'Per-payment-order sequence and active-processing fence for unified-payment Webhooks across all Sub2 replicas.';
