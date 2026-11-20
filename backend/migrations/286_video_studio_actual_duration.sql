-- File metadata, independent of submitted seconds and actual/billable provider usage.
-- No default or historical backfill: an unmeasured file has no known duration.
ALTER TABLE video_studio_jobs ADD COLUMN actual_duration_ms BIGINT
    CHECK (actual_duration_ms > 0 AND actual_duration_ms <= 9007199254740991);
