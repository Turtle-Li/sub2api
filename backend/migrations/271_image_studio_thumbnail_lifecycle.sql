-- Additive derivative lifecycle; generation and billing remain authoritative.
ALTER TABLE batch_image_jobs
    ADD COLUMN IF NOT EXISTS thumbnail_disabled_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS thumbnail_retry_at TIMESTAMPTZ;
