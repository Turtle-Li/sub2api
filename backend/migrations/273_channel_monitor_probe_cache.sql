-- Cache is the final successful attempt, separate from cumulative paid input.
-- Legacy and old-writer receipts remain unknown; new explicit cold hits retain zero.
ALTER TABLE channel_monitor_v3_group_probes
    ADD COLUMN cache_input_tokens BIGINT,
    ADD COLUMN cache_read_tokens BIGINT,
    ADD CONSTRAINT channel_monitor_v3_group_probe_cache_pair CHECK (
        (cache_input_tokens IS NULL AND cache_read_tokens IS NULL)
        OR (cache_input_tokens IS NOT NULL AND cache_read_tokens IS NOT NULL
            AND cache_input_tokens > 0 AND cache_read_tokens >= 0
            AND cache_read_tokens <= cache_input_tokens)
    );
