ALTER TABLE batch_image_jobs
    ADD COLUMN IF NOT EXISTS image_size VARCHAR(2) NOT NULL DEFAULT '1K';

ALTER TABLE batch_image_jobs
    DROP CONSTRAINT IF EXISTS batch_image_jobs_image_size_check;

ALTER TABLE batch_image_jobs
    ADD CONSTRAINT batch_image_jobs_image_size_check
    CHECK (image_size IN ('1K', '2K', '4K'));

COMMENT ON COLUMN batch_image_jobs.image_size IS
    'Requested output tier. Image 2.5 2K/4K jobs generate at 1K then use the private upscale adapter.';
