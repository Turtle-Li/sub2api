-- The reserved ownership slot is not a credential, and keeps the existing
-- owner/idempotency and gallery indexes usable without a parallel task table.
ALTER TABLE batch_image_jobs ADD COLUMN studio_group_id BIGINT;

ALTER TABLE batch_image_jobs ADD CONSTRAINT batch_image_jobs_studio_wallet_owner_check CHECK (
    (studio_group_id IS NULL AND api_key_id IS DISTINCT FROM 0)
    OR (studio_group_id IS NOT NULL AND studio_group_id > 0 AND api_key_id IS NOT DISTINCT FROM 0)
) NOT VALID;
