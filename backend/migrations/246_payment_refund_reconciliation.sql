-- Durable worker ownership for server-reviewed refund entitlement holds.
-- This is deliberately additive: old binaries ignore the columns while new
-- binaries use the same persisted provider idempotency keys and finalizers.
ALTER TABLE unified_payment_refund_attempts
    ADD COLUMN IF NOT EXISTS reconciliation_available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ADD COLUMN IF NOT EXISTS reconciliation_claimed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS reconciliation_claimed_by TEXT,
    ADD COLUMN IF NOT EXISTS reconciliation_attempts INTEGER NOT NULL DEFAULT 0 CHECK (reconciliation_attempts >= 0),
    ADD COLUMN IF NOT EXISTS reconciliation_last_error TEXT,
    ADD COLUMN IF NOT EXISTS reconciliation_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP;

-- Only current server-reviewed reservations may be retried automatically.
-- Historical attempts, manual-review fences and ordinary legacy balance
-- refunds remain outside this worker's authority.
CREATE INDEX IF NOT EXISTS idx_unified_refund_reviewed_reconciliation_due
    ON unified_payment_refund_attempts (reconciliation_available_at, created_at, product_refund_no)
    WHERE status = 'PENDING'
      AND entitlement_reserved = TRUE
      AND needs_manual_review = FALSE
      AND quote_revision <> ''
      AND refund_kind IN ('balance', 'subscription');

CREATE INDEX IF NOT EXISTS idx_unified_refund_reviewed_reconciliation_lease
    ON unified_payment_refund_attempts (reconciliation_claimed_at)
    WHERE status = 'PENDING'
      AND entitlement_reserved = TRUE
      AND needs_manual_review = FALSE
      AND quote_revision <> ''
      AND refund_kind IN ('balance', 'subscription')
      AND reconciliation_claimed_at IS NOT NULL;

COMMENT ON COLUMN unified_payment_refund_attempts.reconciliation_claimed_by IS
    'Ephemeral worker lease owner for reviewed entitlement-refund reconciliation; never a provider identity.';

-- Readiness includes manual/terminal reservations as well as retryable work.
-- Keep the zero-reservation gate bounded as completed refund history grows.
CREATE INDEX IF NOT EXISTS idx_unified_refund_reviewed_reserved
    ON unified_payment_refund_attempts (product_refund_no)
    WHERE entitlement_reserved = TRUE
      AND quote_revision <> ''
      AND refund_kind IN ('balance', 'subscription');
