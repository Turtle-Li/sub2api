//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestOpsAlertMigration291PreservesExistingRows(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
CREATE SCHEMA ops_alert_migration_test;
SET LOCAL search_path TO ops_alert_migration_test;
CREATE TABLE ops_alert_rules (id bigint PRIMARY KEY, name text NOT NULL);
CREATE TABLE ops_alert_events (id bigint PRIMARY KEY, email_sent boolean NOT NULL);
INSERT INTO ops_alert_rules VALUES (7, 'old rule');
INSERT INTO ops_alert_events VALUES (8, true);`)
	require.NoError(t, err)
	ddl, err := migrations.FS.ReadFile("291_ops_alert_rule_notifications.sql")
	require.NoError(t, err)
	for range 2 { // Safe on a database already migrated as well.
		_, err = tx.ExecContext(ctx, string(ddl))
		require.NoError(t, err)
	}
	var name string
	var notifyFeishu, emailSent, feishuSent bool
	var emails, message sql.NullString
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT name, notify_feishu, notify_emails, custom_message FROM ops_alert_migration_test.ops_alert_rules WHERE id=7`).Scan(&name, &notifyFeishu, &emails, &message))
	require.Equal(t, "old rule", name)
	require.False(t, notifyFeishu || emails.Valid || message.Valid)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT email_sent, feishu_sent FROM ops_alert_migration_test.ops_alert_events WHERE id=8`).Scan(&emailSent, &feishuSent))
	require.True(t, emailSent)
	require.False(t, feishuSent)
	_, err = tx.ExecContext(ctx, `INSERT INTO ops_alert_migration_test.ops_alert_deliveries (event_id, channel, target) VALUES (8, 'feishu', 'payment_feishu')`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO ops_alert_migration_test.ops_alert_deliveries (event_id, channel, target) VALUES (8, 'feishu', 'payment_feishu')`)
	require.ErrorContains(t, err, "duplicate key")
}

func TestOpsTargetedUpstreamQueryMatchesOnlyOneHop(t *testing.T) {
	ctx := context.Background()
	repo := &opsRepository{db: integrationDB}
	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-5 * time.Minute)
	group := end.UnixNano()
	account248, account258 := int64(248), int64(258)
	status402 := 402
	message := `prefix literal%_\text suffix`
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM ops_error_logs WHERE group_id=$1`, group) })
	persist := func(secondsBefore int, hops []*service.OpsUpstreamErrorEvent) int64 {
		t.Helper()
		var hopsJSON *string
		if len(hops) > 0 {
			raw, err := json.Marshal(hops)
			require.NoError(t, err)
			s := string(raw)
			hopsJSON = &s
		}
		id, err := repo.InsertErrorLog(ctx, &service.OpsInsertErrorLogInput{
			StatusCode: 200, AccountID: &account248, GroupID: &group, Platform: service.PlatformOpenAI,
			ErrorPhase: "upstream", ErrorType: "upstream_error", Severity: "P1", CreatedAt: end.Add(-time.Duration(secondsBefore) * time.Second),
			UpstreamErrors: hops, UpstreamErrorsJSON: hopsJSON, UpstreamStatusCode: &status402, ProviderErrorCode: "vendor:balance",
			UpstreamErrorMessage: &message,
		})
		require.NoError(t, err)
		return id
	}
	hop := func(id int64, status int, code, message string, at time.Time) *service.OpsUpstreamErrorEvent {
		return &service.OpsUpstreamErrorEvent{AccountID: id, Platform: service.PlatformOpenAI, UpstreamStatusCode: status,
			ProviderCode: code, Message: message, AtUnixMs: at.UnixMilli()}
	}
	persist(20, []*service.OpsUpstreamErrorEvent{
		hop(account248, 429, "vendor:rate", message, end.Add(-21*time.Second)),
		hop(account258, 402, "vendor:balance", message, end.Add(-20*time.Second)),
	}) // The top-level #248/402 must not be counted either.
	legacy := persist(60, nil)
	persist(30, []*service.OpsUpstreamErrorEvent{
		hop(account248, 402, "vendor:balance", `literalZexample\text`, end.Add(-31*time.Second)),
		hop(account248, 402, "vendor:balance", message, end.Add(-30*time.Second)),
		hop(account248, 402, "vendor:balance", message, start.Add(-time.Second)),
	})
	corrupt := persist(10, nil)
	_, err := integrationDB.ExecContext(ctx, `UPDATE ops_error_logs SET upstream_errors=$2::jsonb WHERE id=$1`, corrupt, `[
		null, [], 7,
		{"account_id":"9223372036854775808","platform":"openai","upstream_status_code":"402","provider_code":"not relevant","at_unix_ms":"overflow"},
		{"account_id":248,"platform":"openai","upstream_status_code":{"bad":402},"provider_code":"vendor:balance"}
	]`)
	require.NoError(t, err)
	var providerCode string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT provider_error_code FROM ops_error_logs WHERE id=$1`, legacy).Scan(&providerCode))
	require.Equal(t, "vendor:balance", providerCode)
	legacyObject := persist(120, nil)
	_, err = integrationDB.ExecContext(ctx, `UPDATE ops_error_logs SET upstream_errors='{"old_format":true}'::jsonb WHERE id=$1`, legacyObject)
	require.NoError(t, err)
	filter := service.OpsAccountUpstreamErrorFilter{Start: start, End: end, Platform: service.PlatformOpenAI, GroupID: &group,
		AccountIDs: []int64{account248}, UpstreamStatusCodes: []int{402}, ProviderCodes: []string{"vendor:balance"},
		ErrorKeywords: []string{`literal%_\text`}}
	match, err := repo.CountAccountUpstreamErrors(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, service.OpsAccountUpstreamErrorMatch{Count: 3, LatestAccountID: account248, LatestUpstreamStatusCode: 402,
		LatestProviderCode: "vendor:balance", LatestPlatform: service.PlatformOpenAI, MatchedAccountIDs: []int64{account248}}, match)
	filter.AccountIDs = nil
	match, err = repo.CountAccountUpstreamErrors(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, int64(4), match.Count)
	require.Equal(t, account258, match.LatestAccountID)
	require.Equal(t, []int64{account248, account258}, match.MatchedAccountIDs)
	filter.ProviderCodes = []string{"vendor:rate"}
	match, err = repo.CountAccountUpstreamErrors(ctx, filter)
	require.NoError(t, err)
	require.Zero(t, match.Count, "do not borrow a code from the 429 hop")
}

func TestOpsAlertOutboxTransactionAndClaimFence(t *testing.T) {
	ctx := context.Background()
	repo := &opsRepository{db: integrationDB}
	rule, err := repo.CreateAlertRule(ctx, &service.OpsAlertRule{Name: uuid.NewString(), Enabled: true, Severity: "P1", MetricType: service.OpsAlertMetricAccountUpstreamError,
		Operator: ">=", Threshold: 1, WindowMinutes: 15, SustainedMinutes: 1, NotifyEmail: true, NotifyFeishu: true,
		NotifyEmails: []string{"ops@example.invalid"}, CustomMessage: "check", Filters: map[string]any{"upstream_status_codes": []int{402}}})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM ops_alert_events WHERE rule_id=$1`, rule.ID)
		_ = repo.DeleteAlertRule(ctx, rule.ID)
	})
	require.True(t, rule.NotifyFeishu)
	require.Equal(t, []string{"ops@example.invalid"}, rule.NotifyEmails)
	legacyPut := *rule
	legacyPut.NotifyFeishu = false
	legacyPut.NotifyEmails = nil
	legacyPut.CustomMessage = ""
	legacyPut.NotificationUpdate = &service.OpsAlertNotificationUpdate{}
	afterUpdate, err := repo.UpdateAlertRule(ctx, &legacyPut)
	require.NoError(t, err)
	require.True(t, afterUpdate.NotifyFeishu)
	require.Equal(t, "check", afterUpdate.CustomMessage)
	require.Equal(t, []string{"ops@example.invalid"}, afterUpdate.NotifyEmails)

	newEvent := func(plan []service.OpsAlertDeliveryTarget) *service.OpsAlertEvent {
		return &service.OpsAlertEvent{RuleID: rule.ID, Status: service.OpsAlertStatusFiring, Severity: "P1", FiredAt: time.Now(), DeliveryPlan: plan}
	}
	feishu := service.OpsAlertDeliveryTarget{Channel: service.OpsAlertDeliveryFeishu, Target: service.OpsAlertFeishuTarget}
	_, err = repo.CreateAlertEvent(ctx, newEvent([]service.OpsAlertDeliveryTarget{feishu, {Channel: "invalid", Target: "invalid"}}))
	require.Error(t, err)
	var total int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM ops_alert_events WHERE rule_id=$1`, rule.ID).Scan(&total))
	require.Zero(t, total, "a delivery insert failure must roll back the event as well")
	historical, err := repo.CreateAlertEvent(ctx, newEvent(nil))
	require.NoError(t, err)
	require.Equal(t, "unknown", historical.FeishuDeliveryStatus)
	require.Equal(t, "unknown", historical.EmailDeliveryStatus)
	created, err := repo.CreateAlertEvent(ctx, newEvent([]service.OpsAlertDeliveryTarget{feishu, feishu}))
	require.NoError(t, err)
	require.Equal(t, "pending", created.FeishuDeliveryStatus)
	require.Equal(t, "disabled", created.EmailDeliveryStatus)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM ops_alert_deliveries WHERE event_id=$1`, created.ID).Scan(&total))
	require.Equal(t, 1, total)

	now := time.Now().UTC().Add(time.Second)
	oldClaim, err := repo.ClaimAlertDelivery(ctx, now, "generation-a", time.Second)
	require.NoError(t, err)
	require.NotNil(t, oldClaim)
	require.Equal(t, created.ID, oldClaim.EventID)
	other, err := repo.ClaimAlertDelivery(ctx, now, "busy", time.Second)
	require.NoError(t, err)
	require.Nil(t, other)
	taken, err := repo.StartAlertDelivery(ctx, oldClaim, now)
	require.NoError(t, err)
	require.True(t, taken)
	now = now.Add(2 * time.Second)
	newClaim, err := repo.ClaimAlertDelivery(ctx, now, "generation-b", time.Minute)
	require.NoError(t, err)
	require.NotNil(t, newClaim)
	require.Equal(t, oldClaim.ID, newClaim.ID)
	require.Equal(t, 1, newClaim.AttemptCount)
	outcome := service.OpsAlertDeliveryOutcome{Status: service.OpsAlertDeliverySent, Now: now, NextAttempt: now}
	acked, err := repo.FinishAlertDelivery(ctx, oldClaim, outcome)
	require.NoError(t, err)
	require.False(t, acked)
	current, err := repo.GetAlertEventByID(ctx, created.ID)
	require.NoError(t, err)
	require.False(t, current.FeishuSent)
	acked, err = repo.FinishAlertDelivery(ctx, newClaim, outcome)
	require.NoError(t, err)
	require.True(t, acked)
	current, err = repo.GetAlertEventByID(ctx, created.ID)
	require.NoError(t, err)
	require.True(t, current.FeishuSent)
	require.False(t, current.EmailSent)
	require.Equal(t, "sent", current.FeishuDeliveryStatus)
}

func TestUpstreamBalanceStickySessionCAS(t *testing.T) {
	ctx := context.Background()
	cache := NewGatewayCache(integrationRedis).(*gatewayCache)
	const groupID = 9
	hash := uuid.NewString()
	t.Cleanup(func() { _ = cache.DeleteSessionAccountID(ctx, groupID, hash) })
	require.NoError(t, cache.SetSessionAccountID(ctx, groupID, hash, 248, time.Minute))
	require.NoError(t, cache.ReplaceFailedStickySession(ctx, groupID, hash, 248, 258, time.Minute))
	id, err := cache.GetSessionAccountID(ctx, groupID, hash)
	require.NoError(t, err)
	require.Equal(t, int64(258), id)
	// A second request reading the original failed binding cannot overwrite or
	// clear the winning concurrent binding.
	require.NoError(t, cache.ReplaceFailedStickySession(ctx, groupID, hash, 248, 268, time.Minute))
	require.NoError(t, cache.ReplaceFailedStickySession(ctx, groupID, hash, 248, 0, time.Minute))
	id, err = cache.GetSessionAccountID(ctx, groupID, hash)
	require.NoError(t, err)
	require.Equal(t, int64(258), id)
	require.NoError(t, cache.ReplaceFailedStickySession(ctx, groupID, hash, 258, 0, time.Minute))
	_, err = cache.GetSessionAccountID(ctx, groupID, hash)
	require.ErrorIs(t, err, service.ErrStickySessionNotFound)
}
