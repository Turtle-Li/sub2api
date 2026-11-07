-- Keep claims and private consumption receipts when clearing public history.
ALTER TABLE channel_monitor_v3_group_probes
    -- NULL identifies legacy claims, including writes by a naturally draining binary.
    ADD COLUMN attempt_count INTEGER CHECK (attempt_count BETWEEN 0 AND 3),
    ADD COLUMN missing_usage_attempts INTEGER NOT NULL DEFAULT 0 CHECK (missing_usage_attempts BETWEEN 0 AND COALESCE(attempt_count, 1)),
    ADD COLUMN status_hidden BOOLEAN NOT NULL DEFAULT FALSE;
