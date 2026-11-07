-- no-transaction
-- Support owner/key keyset reads and bounded item pages within a large batch.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_batch_image_jobs_studio_owner_page
    ON batch_image_jobs (user_id, api_key_id, id DESC)
    WHERE user_deleted_at IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_batch_image_items_studio_job_page
    ON batch_image_items (job_id, id DESC);
