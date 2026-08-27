-- Migration 195 widened the user auth-cache invalidation predicate to cover
-- concurrency (needed: payment fulfillment raises the cap and the cap is
-- embedded in API-key auth snapshots). It also added `balance`, which is a
-- hot-path column: every billed gateway request runs
-- `UPDATE users SET balance = balance - $1`, so the trigger enqueued one outbox
-- row per active API key per request. The worker then dropped L1, deleted the
-- Redis entry and broadcast an invalidation to every instance — collapsing the
-- auth cache for exactly the users who are actively serving traffic.
--
-- The cached balance is not a gate. Requests are rejected by the billing
-- statement's own `WHERE balance >= $1` guard, and low-balance notifications
-- reconstruct the pre-deduction balance from the transaction's RETURNING value
-- (see resolveOldBalance) precisely so they do not depend on the snapshot.
-- So `balance` buys no correctness here and costs write amplification.
--
-- `total_recharged` stays: it only moves on recharge/refund, which is rare, and
-- keeping it means a successful top-up still converges the snapshot even if the
-- application-level invalidation hook is unavailable.

CREATE OR REPLACE FUNCTION enqueue_user_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_user_id BIGINT;
BEGIN
    target_user_id := OLD.id;
    IF TG_OP = 'UPDATE'
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.role IS NOT DISTINCT FROM NEW.role
       AND OLD.concurrency IS NOT DISTINCT FROM NEW.concurrency
       AND OLD.total_recharged IS NOT DISTINCT FROM NEW.total_recharged
       AND OLD.rpm_limit IS NOT DISTINCT FROM NEW.rpm_limit
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;

    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')
    FROM api_keys AS k
    WHERE k.user_id = target_user_id
      AND k.deleted_at IS NULL
      AND k.key <> '';
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION enqueue_user_auth_cache_invalidation() IS
    'Invalidate API-key auth snapshots when user status, role, concurrency, recharge total, RPM, or deletion state changes. Deliberately excludes balance: it changes on every billed request and is not an auth gate.';
