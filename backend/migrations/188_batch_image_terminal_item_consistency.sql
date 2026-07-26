UPDATE batch_image_items AS item
SET status = 'failed',
    error_code = COALESCE(
        NULLIF(item.error_code, ''),
        NULLIF(job.last_error_code, ''),
        'BATCH_IMAGE_JOB_FAILED'
    ),
    error_message = COALESCE(
        NULLIF(item.error_message, ''),
        NULLIF(job.last_error_message, ''),
        'batch image job failed before item processing'
    )
FROM batch_image_jobs AS job
WHERE item.job_id = job.batch_id
  AND item.status = 'pending'
  AND job.status = 'failed';

UPDATE batch_image_jobs AS job
SET success_count = (
        SELECT COUNT(*)
        FROM batch_image_items AS item
        WHERE item.job_id = job.batch_id
          AND item.status IN ('success', 'result_available')
    ),
    fail_count = (
        SELECT COUNT(*)
        FROM batch_image_items AS item
        WHERE item.job_id = job.batch_id
          AND item.status = 'failed'
    )
WHERE job.status = 'failed'
  AND EXISTS (
      SELECT 1
      FROM batch_image_items AS item
      WHERE item.job_id = job.batch_id
  );
