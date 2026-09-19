-- Coupon applicability is configuration, not a client-side hint. Existing
-- coupons intentionally retain the original balance + subscription behavior.
ALTER TABLE payment_discount_codes
    ADD COLUMN IF NOT EXISTS order_types JSONB NOT NULL
        DEFAULT '["balance", "subscription"]'::jsonb,
    ADD COLUMN IF NOT EXISTS plan_ids JSONB NOT NULL
        DEFAULT '[]'::jsonb;

-- The service validates individual plan existence and canonicalizes the
-- arrays. These checks keep direct database writes from widening a coupon to
-- unsupported purchase kinds or attaching plan restrictions to balance-only
-- coupons.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'payment_discount_codes'::regclass
          AND conname = 'payment_discount_codes_order_types_valid'
    ) THEN
        ALTER TABLE payment_discount_codes
            ADD CONSTRAINT payment_discount_codes_order_types_valid CHECK (
                jsonb_typeof(order_types) = 'array'
                AND jsonb_array_length(order_types) > 0
                AND order_types <@ '["balance", "subscription"]'::jsonb
            );
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'payment_discount_codes'::regclass
          AND conname = 'payment_discount_codes_plan_ids_valid'
    ) THEN
        ALTER TABLE payment_discount_codes
            ADD CONSTRAINT payment_discount_codes_plan_ids_valid CHECK (
                jsonb_typeof(plan_ids) = 'array'
                AND jsonb_array_length(plan_ids) <= 100
                AND (jsonb_array_length(plan_ids) = 0 OR order_types ? 'subscription')
            );
    END IF;
END
$$;
