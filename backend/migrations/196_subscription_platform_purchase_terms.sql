-- Subscription commercial terms and the one-subscription-per-platform invariant.
--
-- A user can change tiers within a platform, but a second live row for the same
-- platform would let both quota pools remain usable. Keep the preferred legacy
-- row and soft-delete the others so historical audits and reset-card grants are
-- retained instead of being destroyed.

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS platform VARCHAR(50) NOT NULL DEFAULT 'anthropic';

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS plan_id BIGINT NULL;

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS plan_price DECIMAL(20,2) NULL;

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS plan_validity_days INTEGER NULL;

UPDATE user_subscriptions AS us
SET platform = g.platform
FROM groups AS g
WHERE g.id = us.group_id
  AND COALESCE(g.platform, '') <> ''
  AND us.platform IS DISTINCT FROM g.platform;

-- Attach the latest successfully fulfilled plan for legacy paid subscriptions.
-- Admin-assigned terms intentionally remain NULL because they have no safely
-- inferable commercial price; they can still be renewed or re-subscribed.
WITH latest_completed_plan AS (
    SELECT DISTINCT ON (us.id)
        us.id AS subscription_id,
        po.plan_id,
        COALESCE(
            CASE
                WHEN po.product_snapshot->>'price' ~ '^[0-9]+(\.[0-9]+)?$'
                THEN (po.product_snapshot->>'price')::DECIMAL(20,2)
            END,
            NULLIF(po.amount, 0),
            plan.price
        ) AS plan_price,
        COALESCE(
            CASE
                WHEN po.product_snapshot->>'subscription_days' ~ '^[0-9]+$'
                THEN (po.product_snapshot->>'subscription_days')::INTEGER
            END,
            po.subscription_days,
            CASE LOWER(COALESCE(plan.validity_unit, ''))
                WHEN 'week' THEN plan.validity_days * 7
                WHEN 'weeks' THEN plan.validity_days * 7
                WHEN 'month' THEN plan.validity_days * 30
                WHEN 'months' THEN plan.validity_days * 30
                WHEN 'quarter' THEN plan.validity_days * 90
                WHEN 'quarters' THEN plan.validity_days * 90
                WHEN 'year' THEN plan.validity_days * 365
                WHEN 'years' THEN plan.validity_days * 365
                ELSE plan.validity_days
            END
        ) AS plan_validity_days
    FROM user_subscriptions AS us
    JOIN payment_orders AS po
      ON po.user_id = us.user_id
     AND po.subscription_group_id = us.group_id
     AND po.plan_id IS NOT NULL
     AND po.status = 'COMPLETED'
    LEFT JOIN subscription_plans AS plan ON plan.id = po.plan_id
    WHERE us.plan_id IS NULL
      AND us.assigned_by IS NULL
    ORDER BY us.id, po.completed_at DESC NULLS LAST, po.created_at DESC, po.id DESC
)
UPDATE user_subscriptions AS us
SET plan_id = latest_completed_plan.plan_id,
    plan_price = latest_completed_plan.plan_price,
    plan_validity_days = latest_completed_plan.plan_validity_days
FROM latest_completed_plan
WHERE us.id = latest_completed_plan.subscription_id;

WITH ranked AS (
    SELECT
        id,
        ROW_NUMBER() OVER (
            PARTITION BY user_id, platform
            ORDER BY
                CASE WHEN status = 'active' AND expires_at > NOW() THEN 0 ELSE 1 END,
                expires_at DESC,
                updated_at DESC,
                id DESC
        ) AS rank_in_platform
    FROM user_subscriptions
    WHERE deleted_at IS NULL
)
UPDATE user_subscriptions AS us
SET status = 'revoked',
    deleted_at = NOW(),
    updated_at = NOW()
FROM ranked
WHERE us.id = ranked.id
  AND ranked.rank_in_platform > 1;

DROP INDEX IF EXISTS user_subscriptions_user_group_unique_active;

CREATE UNIQUE INDEX IF NOT EXISTS user_subscriptions_user_platform_unique_active
    ON user_subscriptions(user_id, platform)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_plan_id
    ON user_subscriptions(plan_id)
    WHERE plan_id IS NOT NULL;
