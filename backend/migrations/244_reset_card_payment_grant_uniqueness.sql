-- An externally paid reset-card order grants exactly one durable card batch.
-- Manual/admin grants keep payment_order_id NULL and are unaffected.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM subscription_reset_grants
        WHERE payment_order_id IS NOT NULL
        GROUP BY payment_order_id
        HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION
            'cannot enforce reset-card payment grant uniqueness: duplicate payment_order_id rows require manual review';
    END IF;
END
$$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_reset_grants_payment_order_unique
    ON subscription_reset_grants (payment_order_id)
    WHERE payment_order_id IS NOT NULL;
