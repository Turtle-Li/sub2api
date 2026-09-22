-- A local cancellation is an immediate product-side admission fence. Provider
-- close and any direct-provider late refund are separate durable operations so
-- a browser request never waits for an upstream call and a process restart
-- cannot lose money-moving follow-up work.
CREATE TABLE IF NOT EXISTS payment_local_cancellation_work (
    order_id BIGINT NOT NULL REFERENCES payment_orders(id) ON DELETE RESTRICT,
    work_kind VARCHAR(24) NOT NULL CHECK (work_kind IN ('CLOSE', 'DIRECT_REFUND')),
    status VARCHAR(24) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'COMPLETED', 'MANUAL_REVIEW')),
    provider_key VARCHAR(64) NOT NULL DEFAULT '',
    payment_trade_no VARCHAR(200) NOT NULL DEFAULT '',
    -- Direct providers return a provider-generated refund identifier after a
    -- late-payment refund.  Retain only this bounded correlation and its
    -- normalized status so a completed cash movement is visible after a
    -- process restart without retaining provider response bodies.
    provider_refund_id VARCHAR(160) NOT NULL DEFAULT '',
    provider_status VARCHAR(32) NOT NULL DEFAULT '',
    amount_fen BIGINT NOT NULL DEFAULT 0 CHECK (amount_fen >= 0),
    currency VARCHAR(16) NOT NULL DEFAULT 'CNY',
    idempotency_key VARCHAR(128) NOT NULL,
    available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    claimed_at TIMESTAMPTZ,
    claimed_by TEXT,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error TEXT,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (order_id, work_kind)
);

CREATE INDEX IF NOT EXISTS idx_payment_local_cancellation_work_due
    ON payment_local_cancellation_work (available_at, created_at, order_id)
    WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_payment_local_cancellation_work_lease
    ON payment_local_cancellation_work (claimed_at)
    WHERE status = 'PENDING' AND claimed_at IS NOT NULL;

-- `balance_amount_minor` is an entitlement-accounting amount for ordinary
-- product refunds. A cancellation after accepted payment has no product
-- entitlement to reverse, so only its dedicated unified attempt may persist
-- zero while its channel amount remains the exact positive original pay-in.
DO $$
DECLARE
    v_constraint_name TEXT;
BEGIN
    SELECT conname INTO v_constraint_name
    FROM pg_constraint
    WHERE conrelid = 'unified_payment_refund_attempts'::regclass
      AND contype = 'c'
      AND POSITION('balance_amount_minor > 0' IN pg_get_constraintdef(oid)) > 0
    LIMIT 1;

    IF v_constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE unified_payment_refund_attempts DROP CONSTRAINT %I', v_constraint_name);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'unified_payment_refund_attempts'::regclass
          AND conname = 'unified_refund_balance_amount_by_kind'
    ) THEN
        ALTER TABLE unified_payment_refund_attempts
            ADD CONSTRAINT unified_refund_balance_amount_by_kind
            CHECK (
                balance_amount_minor > 0
                OR (refund_kind = 'cancel_late_payment' AND balance_amount_minor = 0)
            );
    END IF;
END
$$;

-- No-entitlement late refunds use the existing correlation/recovery table and
-- its stable provider idempotency. They are deliberately isolated from the
-- reviewed-entitlement indexes so a manual review fence remains independent.
CREATE INDEX IF NOT EXISTS idx_unified_refund_cancel_late_reconciliation_due
    ON unified_payment_refund_attempts (reconciliation_available_at, created_at, product_refund_no)
    WHERE status = 'PENDING'
      AND needs_manual_review = FALSE
      AND refund_kind = 'cancel_late_payment';

CREATE INDEX IF NOT EXISTS idx_unified_refund_cancel_late_reconciliation_lease
    ON unified_payment_refund_attempts (reconciliation_claimed_at)
    WHERE status = 'PENDING'
      AND needs_manual_review = FALSE
      AND refund_kind = 'cancel_late_payment'
      AND reconciliation_claimed_at IS NOT NULL;

COMMENT ON TABLE payment_local_cancellation_work IS
    'Durable close and direct late-refund jobs for immediately locally-cancelled payment orders.';
