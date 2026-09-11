-- Refund safety and fulfillment reclaim bookkeeping.
--
-- refund_amount is the settled cumulative amount.  Requests that are still
-- pending (or failed and awaiting an admin retry) live in the separate
-- refund_requested_amount column so an in-flight request cannot consume the
-- remaining refundable allowance.
ALTER TABLE payment_orders
    ADD COLUMN IF NOT EXISTS refund_requested_amount DECIMAL(20,2) NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'payment_orders'::regclass
          AND conname = 'payment_orders_refund_requested_amount_nonnegative'
    ) THEN
        ALTER TABLE payment_orders
            ADD CONSTRAINT payment_orders_refund_requested_amount_nonnegative
            CHECK (refund_requested_amount >= 0);
    END IF;
END $$;

-- Older deployments stored the in-flight amount in refund_amount. Move that
-- value to the new field only when the row carries an unmistakable in-flight
-- marker. A REFUND_FAILED row can already include a settled partial refund, so
-- it is migrated only when it has a request timestamp and no settled timestamp.
UPDATE payment_orders
SET refund_requested_amount = refund_amount,
    refund_amount = 0
WHERE (
        status IN ('REFUND_REQUESTED', 'REFUNDING', 'REFUND_PENDING')
        OR (status = 'REFUND_FAILED' AND refund_at IS NULL AND refund_requested_at IS NOT NULL)
      )
  AND refund_amount > 0
  AND refund_requested_amount = 0;

-- Payment-generated reset-card batches need an order correlation before they
-- can be safely reclaimed.  Existing manual/admin grants remain NULL and are
-- deliberately never removed by an order refund.
ALTER TABLE subscription_reset_grants
    ADD COLUMN IF NOT EXISTS payment_order_id BIGINT NULL REFERENCES payment_orders(id) ON DELETE RESTRICT;

CREATE INDEX IF NOT EXISTS idx_subscription_reset_grants_payment_order
    ON subscription_reset_grants (payment_order_id, id)
    WHERE payment_order_id IS NOT NULL;

COMMENT ON COLUMN payment_orders.refund_requested_amount IS
    'Current in-flight refund request; refund_amount remains cumulative settled refunds.';
COMMENT ON COLUMN subscription_reset_grants.payment_order_id IS
    'Payment order that issued this batch; NULL denotes a manual/admin grant.';
