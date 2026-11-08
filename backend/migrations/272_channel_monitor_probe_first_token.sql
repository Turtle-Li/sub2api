-- Old writers and historical checks retain NULL; total duration is not TTFT.
ALTER TABLE channel_monitor_v3_group_probes
    ADD COLUMN first_token_ms BIGINT CHECK (first_token_ms >= 0);
