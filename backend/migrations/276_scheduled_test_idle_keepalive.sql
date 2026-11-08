-- Opt-in inactivity gating for the existing account test scheduler.
ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS idle_hours INT NOT NULL DEFAULT 0 CHECK (idle_hours BETWEEN 0 AND 8760),
    ADD COLUMN IF NOT EXISTS last_success_at TIMESTAMPTZ;

UPDATE scheduled_test_plans p
SET last_success_at = recent.finished_at
FROM (
    SELECT plan_id, MAX(finished_at) AS finished_at
    FROM scheduled_test_results WHERE status = 'success' GROUP BY plan_id
) recent
WHERE p.id = recent.plan_id AND p.last_success_at IS NULL;

-- Different models must not create competing keep-alive loops for one account.
CREATE UNIQUE INDEX IF NOT EXISTS idx_stp_enabled_idle_account
    ON scheduled_test_plans (account_id) WHERE enabled = true AND idle_hours > 0;
