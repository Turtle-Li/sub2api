-- Concurrency is embedded in API-key auth snapshots. The original user
-- invalidation trigger intentionally ignored ordinary profile changes, but
-- that predicate also swallowed concurrency-only updates. Keep the trigger
-- durable and transactionally enqueue invalidation whenever concurrency
-- changes.

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
       AND OLD.balance IS NOT DISTINCT FROM NEW.balance
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
    'Invalidate API-key auth snapshots when user status, role, concurrency, balance, recharge total, RPM, or deletion state changes';
