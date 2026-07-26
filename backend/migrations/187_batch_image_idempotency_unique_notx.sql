-- Enforce owner-scoped Idempotency-Key uniqueness without blocking production
-- writes. The migration runner rejects existing duplicates and removes a stale
-- invalid index before retrying this concurrent build.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_batch_image_jobs_owner_idempotency
    ON batch_image_jobs (user_id, api_key_id, idempotency_key)
    WHERE api_key_id IS NOT NULL
      AND idempotency_key IS NOT NULL
      AND idempotency_key <> '';
