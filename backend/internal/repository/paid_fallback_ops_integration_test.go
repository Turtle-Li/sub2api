//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type paidOpsFixture struct {
	store                     *paidFallbackOpsStore
	group, account, key, user int64
	model                     string
}

func newPaidOpsFixture(t *testing.T) paidOpsFixture {
	t.Helper()
	client := testEntClient(t)
	id := uuid.NewString()
	u := mustCreateUser(t, client, &service.User{Email: id + "@test.invalid"})
	k := mustCreateApiKey(t, client, &service.APIKey{UserID: u.ID, Key: id, Name: id})
	a := mustCreateAccount(t, client, &service.Account{Name: id})
	g := mustCreateGroup(t, client, &service.Group{Name: id, Platform: "openai"})
	f := paidOpsFixture{store: &paidFallbackOpsStore{db: integrationDB}, group: g.ID, account: a.ID, key: k.ID, user: u.ID, model: "public-" + id}
	t.Cleanup(func() {
		ctx := context.Background()
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM paid_fallback_deliveries WHERE incident_id IN (SELECT id FROM paid_fallback_incidents WHERE group_id=$1)`, f.group)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM paid_fallback_attempts WHERE group_id=$1`, f.group)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM paid_fallback_incidents WHERE group_id=$1`, f.group)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM usage_logs WHERE user_id=$1`, f.user)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM api_keys WHERE id=$1`, f.key)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, f.account)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, f.group)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, f.user)
		require.NoError(t, err)
	})
	return f
}

func (f paidOpsFixture) attempt(t *testing.T, inbound, transport string) string {
	t.Helper()
	id := uuid.NewString()
	require.NoError(t, f.store.StartAttempt(context.Background(), service.PaidFallbackAttempt{ID: id, InboundRequestID: inbound, APIKeyID: f.key, AccountID: f.account, GroupID: f.group, PublicModel: f.model, ForwardModel: "forward-ds", UpstreamModel: "cline-pass/ds", Transport: transport}))
	return id
}

func (f paidOpsFixture) usage(t *testing.T, inbound string, paid bool, cost float64, at time.Time) {
	t.Helper()
	rate := 3.0
	repo := newUsageLogRepositoryWithSQL(testEntClient(t), integrationDB)
	_, err := repo.Create(context.Background(), &service.UsageLog{UserID: f.user, APIKeyID: f.key, AccountID: f.account, GroupID: &f.group, RequestID: "client:" + inbound, Model: "wb-ds", RequestedModel: f.model, PaidFallback: paid, InputTokens: 1, OutputTokens: 1, AccountStatsCost: &cost, AccountRateMultiplier: &rate, CreatedAt: at})
	require.NoError(t, err)
}

func paidOpsSettings() service.PaidFallbackNotificationSettings {
	return service.PaidFallbackNotificationSettings{Enabled: true, ReminderMinutes: 30, RecoveryQuietSeconds: 30}
}

func TestPaidFallbackAccountMarkerHTTPPreservesExtra(t *testing.T) {
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	account := mustCreateAccount(t, client, &service.Account{Name: uuid.NewString(), Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey,
		Extra: map[string]any{"quota_limit": 3.0, "privacy_mode": true, "max_sessions": 2.0}})
	t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(account.ID).Exec(context.Background())) })
	svc := service.NewAdminService(nil, nil, nil, repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, client, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler := admin.NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.PUT("/admin/accounts/:id/paid-fallback", handler.SetPaidFallback)
	for _, enabled := range []bool{true, false} {
		request := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/admin/accounts/%d/paid-fallback", account.ID), strings.NewReader(fmt.Sprintf(`{"enabled":%t}`, enabled)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		stored, err := repo.GetByID(context.Background(), account.ID)
		require.NoError(t, err)
		require.Equal(t, map[string]any{"quota_limit": 3.0, "privacy_mode": true, "max_sessions": 2.0, service.PaidFallbackExtraKey: enabled}, stored.Extra)
	}
}

func TestPaidFallbackOpsRecoveryPreservesInflightDelivery(t *testing.T) {
	f := newPaidOpsFixture(t)
	ctx := context.Background()
	id := f.attempt(t, uuid.NewString(), "http")
	require.NoError(t, f.store.FinishAttempt(ctx, id, "http_headers", 200))
	recipients := []string{"sent@test.invalid", "unsent@test.invalid"}
	require.NoError(t, f.store.Advance(ctx, paidOpsSettings(), recipients))
	active, err := f.store.Claim(ctx, 0)
	require.NoError(t, err)
	require.NotNil(t, active)
	allowed, err := f.store.CanSend(ctx, active.ID, active.ClaimToken)
	require.NoError(t, err)
	require.True(t, allowed)
	_, err = integrationDB.ExecContext(ctx, `UPDATE paid_fallback_incidents SET last_attempt_at=clock_timestamp()-INTERVAL '1 minute' WHERE group_id=$1`, f.group)
	require.NoError(t, err)
	f.usage(t, uuid.NewString(), false, 0, time.Now().UTC())
	require.NoError(t, f.store.Advance(ctx, paidOpsSettings(), recipients))
	allowed, err = f.store.CanSend(ctx, active.ID, active.ClaimToken)
	require.NoError(t, err)
	require.False(t, allowed, "new open notifications stop after recovery")
	pending, err := f.store.Claim(ctx, 0)
	require.NoError(t, err)
	require.Nil(t, pending, "recovery waits for a delivered opening notification")
	require.NoError(t, f.store.Complete(ctx, active.ID, active.ClaimToken), "an email already sent before recovery keeps its delivery result")
	recovered, err := f.store.Claim(ctx, 0)
	require.NoError(t, err)
	require.NotNil(t, recovered)
	require.Equal(t, "recovered", recovered.Kind)
	require.Equal(t, active.Recipient, recovered.Recipient)
	require.NoError(t, f.store.Complete(ctx, recovered.ID, recovered.ClaimToken))
	pending, err = f.store.Claim(ctx, 0)
	require.NoError(t, err)
	require.Nil(t, pending, "recipients with no delivered opening receive no orphan recovery")
}

func TestPaidFallbackOpsSuppressionAndUnlimitedCap(t *testing.T) {
	f := newPaidOpsFixture(t)
	ctx := context.Background()
	id := f.attempt(t, uuid.NewString(), "http")
	require.NoError(t, f.store.FinishAttempt(ctx, id, "http_headers", 200))
	require.NoError(t, f.store.Advance(ctx, paidOpsSettings(), []string{"one@test.invalid", "two@test.invalid", "three@test.invalid"}))
	first, err := f.store.Claim(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.NoError(t, f.store.Suppress(ctx, first.ID, first.ClaimToken))
	var suppressed, delivered bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT suppressed_at IS NOT NULL,delivered_at IS NOT NULL FROM paid_fallback_deliveries WHERE id=$1`, first.ID).Scan(&suppressed, &delivered))
	require.True(t, suppressed)
	require.False(t, delivered, "suppression must not be reported as delivered")
	second, err := f.store.Claim(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, second, "suppression must not consume the hourly delivery cap")
	third, err := f.store.Claim(ctx, 0)
	require.NoError(t, err)
	require.NotNil(t, third, "zero means unlimited, including with another live lease")
	require.NoError(t, f.store.Complete(ctx, second.ID, second.ClaimToken))
	_, err = integrationDB.ExecContext(ctx, `UPDATE paid_fallback_deliveries SET created_at=clock_timestamp()-INTERVAL '25 hours' WHERE id=$1`, third.ID)
	require.NoError(t, err)
	require.NoError(t, f.store.Advance(ctx, paidOpsSettings(), nil))
	allowed, err := f.store.CanSend(ctx, third.ID, third.ClaimToken)
	require.NoError(t, err)
	require.False(t, allowed, "stale queued mail must not be resurrected")
}

func TestPaidFallbackOpsDurableAttemptsAndCost(t *testing.T) {
	f := newPaidOpsFixture(t)
	ctx := context.Background()
	unsent := f.attempt(t, "unsent", "http")
	require.NoError(t, f.store.FinishAttempt(ctx, unsent, "not_sent", 0))
	settings := paidOpsSettings()
	settings.Enabled = false
	require.NoError(t, f.store.Advance(ctx, settings, nil))
	var n int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM paid_fallback_incidents WHERE group_id=$1`, f.group).Scan(&n))
	require.Zero(t, n)
	inbound := uuid.NewString()
	first := f.attempt(t, inbound, "http")
	second := f.attempt(t, inbound, "http")
	require.NoError(t, f.store.FinishAttempt(ctx, first, "unknown", 0))
	require.NoError(t, f.store.FinishAttempt(ctx, second, "http_headers", 200))
	f.usage(t, inbound, true, 2, time.Now().UTC())
	ws := f.attempt(t, uuid.NewString(), "ws_ctx_pool")
	require.NoError(t, f.store.FinishAttempt(ctx, ws, "ws_frame_written", 0))
	require.NoError(t, f.store.Advance(ctx, settings, nil))
	report, err := f.store.Report(ctx)
	require.NoError(t, err)
	var found *service.PaidFallbackIncident
	for i := range report.Incidents {
		if report.Incidents[i].GroupID == f.group {
			found = &report.Incidents[i]
		}
	}
	require.NotNil(t, found)
	require.Equal(t, int64(3), found.Attempts)
	require.Equal(t, int64(2), found.UnknownCostAttempts)
	require.InDelta(t, 6.0, found.RecordedCostUSD, 1e-9)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM paid_fallback_deliveries WHERE incident_id=$1`, found.ID).Scan(&n))
	require.Zero(t, n, "notification off must still audit without enqueueing email")
	stale := f.attempt(t, uuid.NewString(), "http")
	_, err = integrationDB.ExecContext(ctx, `UPDATE paid_fallback_attempts SET started_at=clock_timestamp()-INTERVAL '16 minutes' WHERE id=$1`, stale)
	require.NoError(t, err)
	require.NoError(t, f.store.Advance(ctx, settings, nil))
	var outcome string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT outcome FROM paid_fallback_attempts WHERE id=$1`, stale).Scan(&outcome))
	require.Equal(t, "unknown", outcome)
}

func TestPaidFallbackOpsClaimsAndRecovery(t *testing.T) {
	f := newPaidOpsFixture(t)
	ctx := context.Background()
	settings := paidOpsSettings()
	id := f.attempt(t, uuid.NewString(), "http")
	require.NoError(t, f.store.FinishAttempt(ctx, id, "http_headers", 200))
	recipients := []string{"one@test.invalid", "two@test.invalid"}
	require.NoError(t, f.store.Advance(ctx, settings, recipients))
	require.NoError(t, f.store.Advance(ctx, settings, recipients))
	var n int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM paid_fallback_deliveries WHERE incident_id IN (SELECT id FROM paid_fallback_incidents WHERE group_id=$1)`, f.group).Scan(&n))
	require.Equal(t, 2, n, "one open event per recipient")
	claims := make(chan *service.PaidFallbackDelivery, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); d, e := f.store.Claim(ctx, 1); claims <- d; errs <- e }()
	}
	wg.Wait()
	close(claims)
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	var claimed *service.PaidFallbackDelivery
	for d := range claims {
		if d != nil {
			require.Nil(t, claimed, "database cap applies across instances")
			claimed = d
		}
	}
	require.NotNil(t, claimed)
	allowed, err := f.store.CanSend(ctx, claimed.ID, claimed.ClaimToken)
	require.NoError(t, err)
	require.True(t, allowed)
	_, err = integrationDB.ExecContext(ctx, `UPDATE paid_fallback_deliveries SET lease_expires_at=clock_timestamp()-INTERVAL '1 second' WHERE id=$1`, claimed.ID)
	require.NoError(t, err)
	reclaimed, err := f.store.Claim(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, reclaimed)
	require.Equal(t, claimed.ID, reclaimed.ID)
	require.NotEqual(t, claimed.ClaimToken, reclaimed.ClaimToken)
	require.Error(t, f.store.Complete(ctx, claimed.ID, claimed.ClaimToken))
	require.NoError(t, f.store.Retry(ctx, reclaimed.ID, reclaimed.ClaimToken))
	var deferred bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT next_attempt_at>clock_timestamp(),claim_token IS NULL FROM paid_fallback_deliveries WHERE id=$1`, claimed.ID).Scan(&deferred, &allowed))
	require.True(t, deferred)
	require.True(t, allowed)
	active, err := f.store.Claim(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, active)
	allowed, err = f.store.CanSend(ctx, active.ID, active.ClaimToken)
	require.NoError(t, err)
	require.True(t, allowed)
	_, err = integrationDB.ExecContext(ctx, `UPDATE paid_fallback_incidents SET last_attempt_at=clock_timestamp()-INTERVAL '1 minute' WHERE group_id=$1`, f.group)
	require.NoError(t, err)
	require.NoError(t, f.store.Advance(ctx, settings, recipients))
	var resolved sql.NullTime
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT resolved_at FROM paid_fallback_incidents WHERE group_id=$1`, f.group).Scan(&resolved))
	require.False(t, resolved.Valid, "quiet without ordinary success is not recovery")
	f.usage(t, uuid.NewString(), false, 0, time.Now().UTC())
	lateTx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = lateTx.Rollback() }()
	lateID := uuid.NewString()
	_, err = lateTx.ExecContext(ctx, `INSERT INTO paid_fallback_attempts(id,inbound_request_id,api_key_id,account_id,group_id,public_model,forward_model,upstream_model,transport,outcome) VALUES($1,$2,$3,$4,$5,$6,'forward','cline','http','http_headers')`, lateID, uuid.NewString(), f.key, f.account, f.group, f.model)
	require.NoError(t, err)
	require.NoError(t, f.store.Advance(ctx, settings, recipients))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT resolved_at FROM paid_fallback_incidents WHERE group_id=$1`, f.group).Scan(&resolved))
	require.True(t, resolved.Valid)
	allowed, err = f.store.CanSend(ctx, active.ID, active.ClaimToken)
	require.NoError(t, err)
	require.False(t, allowed)
	require.NoError(t, lateTx.Commit())
	require.NoError(t, f.store.Advance(ctx, settings, recipients))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM paid_fallback_incidents WHERE group_id=$1`, f.group).Scan(&n))
	require.Equal(t, 2, n, "late committed send opens a new generation")
}

func TestPaidFallbackOpsDailyCostUTCAndLateUsage(t *testing.T) {
	f := newPaidOpsFixture(t)
	ctx := context.Background()
	settings := paidOpsSettings()
	id := f.attempt(t, uuid.NewString(), "http")
	require.NoError(t, f.store.FinishAttempt(ctx, id, "http_headers", 200))
	recipients := []string{"cost@test.invalid"}
	require.NoError(t, f.store.Advance(ctx, settings, recipients))
	var day time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT date_trunc('day',clock_timestamp() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`).Scan(&day))
	f.usage(t, uuid.NewString(), true, 100, day.Add(-time.Second))
	f.usage(t, uuid.NewString(), true, 2, day.Add(time.Second))
	require.NoError(t, f.store.Advance(ctx, settings, recipients))
	count := func() int {
		var n int
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM paid_fallback_deliveries WHERE kind='daily_cost' AND incident_id IN (SELECT id FROM paid_fallback_incidents WHERE group_id=$1)`, f.group).Scan(&n))
		return n
	}
	require.Zero(t, count(), "unset threshold must not alert")
	threshold := 10.0
	settings.DailyCostUSD = &threshold
	require.NoError(t, f.store.Advance(ctx, settings, recipients))
	require.Zero(t, count(), "yesterday's costs are excluded")
	_, err := integrationDB.ExecContext(ctx, `UPDATE paid_fallback_incidents SET resolved_at=clock_timestamp() WHERE group_id=$1`, f.group)
	require.NoError(t, err)
	f.usage(t, uuid.NewString(), true, 2, day.Add(2*time.Second))
	require.NoError(t, f.store.Advance(ctx, settings, recipients))
	require.Equal(t, 1, count(), "late usage still alerts after recovery")
	newID := f.attempt(t, uuid.NewString(), "http")
	require.NoError(t, f.store.FinishAttempt(ctx, newID, "http_headers", 200))
	require.NoError(t, f.store.Advance(ctx, settings, recipients))
	require.Equal(t, 1, count(), "one daily event across incident generations")
}
