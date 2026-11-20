//go:build integration

package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestGoodPutVideoJobsReserveSettleAndRefund(t *testing.T) {
	ctx := t.Context()
	client := testEntClient(t)
	repo := &usageBillingRepository{db: integrationDB}
	group := mustCreateGroup(t, client, &service.Group{Name: "H3 " + uuid.NewString(), Platform: service.PlatformOpenAI})
	account := mustCreateAccount(t, client, &service.Account{Name: "GoodPut " + uuid.NewString(), Type: service.AccountTypeAPIKey, Platform: service.PlatformOpenAI})
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com", PasswordHash: "hash", Balance: 2})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, GroupID: &group.ID, Key: "sk-" + uuid.NewString(), Name: "GoodPut", Quota: 1.5})
	job := service.DurableVideoJob{ID: uuid.NewString(), UserID: user.ID, APIKeyID: key.ID, GroupID: group.ID,
		AccountID: account.ID, Model: service.GoodPutH3Model, HoldAmount: .75, ReferenceAmount: .75,
		Seconds: 5, Currency: "USD", Multiplier: 1}
	wallet := func(balance, frozen float64) {
		t.Helper()
		var b, f float64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance,frozen_balance FROM users WHERE id=$1`, user.ID).Scan(&b, &f))
		require.InDelta(t, balance, b, 1e-8)
		require.InDelta(t, frozen, f, 1e-8)
	}
	require.ErrorIs(t, repo.ReserveVideoJob(ctx, job, false), service.ErrGoodPutVideoGroup)
	tooExpensive := job
	tooExpensive.HoldAmount = 1.51
	require.ErrorIs(t, repo.ReserveVideoJob(ctx, tooExpensive, true), service.ErrGoodPutVideoFunds)
	wallet(2, 0)
	require.NoError(t, repo.ReserveVideoJob(ctx, job, true))
	wallet(1.25, .75)
	other := job
	other.ID = uuid.NewString()
	require.ErrorIs(t, repo.ReserveVideoJob(ctx, other, true), service.ErrGoodPutVideoLimit)
	wallet(1.25, .75)
	publicID := "gpv_" + uuid.NewString()
	require.NoError(t, repo.AttachVideoReceipt(ctx, job.ID, publicID, "video_test"))
	unowned, err := repo.FindVideoJob(ctx, publicID, user.ID, key.ID+1, group.ID)
	require.NoError(t, err)
	require.Nil(t, unowned)
	claimed, err := repo.ClaimVideoJob(ctx)
	require.NoError(t, err)
	require.Equal(t, job.ID, claimed.ID)
	var contenders errgroup.Group
	for range 2 {
		contenders.Go(func() error { _, err := repo.CompleteGoodPutVideoJob(ctx, job.ID, 5); return err })
	}
	require.NoError(t, contenders.Wait())
	wallet(1.25, 0)
	var rows int
	var usage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(actual_cost),0) FROM usage_logs WHERE api_key_id=$1`, key.ID).Scan(&rows, &usage))
	require.Equal(t, 1, rows)
	require.InDelta(t, .75, usage, 1e-8)
	var quota float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT quota_used FROM api_keys WHERE id=$1`, key.ID).Scan(&quota))
	require.InDelta(t, .75, quota, 1e-8)
	require.NoError(t, repo.ReserveVideoJob(ctx, other, true))
	wallet(.5, .75)
	require.NoError(t, repo.AttachVideoReceipt(ctx, other.ID, "gpv_"+uuid.NewString(), "video_failure"))
	applied, err := repo.FinishVideoJob(ctx, other.ID, "failed")
	require.NoError(t, err)
	require.True(t, applied)
	wallet(1.25, 0)
	// No provider receipt survives an unknown-submit crash. Recovery fences the
	// reservation instead of releasing it or sending another provider POST.
	unknown := job
	unknown.ID = uuid.NewString()
	require.NoError(t, repo.ReserveVideoJob(ctx, unknown, true))
	_, err = integrationDB.ExecContext(ctx, `UPDATE goodput_video_jobs SET next_poll_at=NOW() WHERE id=$1`, unknown.ID)
	require.NoError(t, err)
	claimed, err = repo.ClaimVideoJob(ctx)
	require.NoError(t, err)
	require.Equal(t, unknown.ID, claimed.ID)
	require.Empty(t, claimed.NativeID)
	wallet(.5, .75)
	claimed, err = repo.ClaimVideoJob(ctx)
	require.NoError(t, err)
	require.Nil(t, claimed)
	lateID := "gpv_" + uuid.NewString()
	require.NoError(t, repo.AttachVideoReceipt(ctx, unknown.ID, lateID, "video_verified_late"))
	require.NoError(t, repo.AttachVideoReceipt(ctx, unknown.ID, lateID, "video_verified_late"))
	applied, err = repo.CompleteGoodPutVideoJob(ctx, unknown.ID, 5)
	require.NoError(t, err)
	require.True(t, applied)
	wallet(.5, 0)

}

func TestGoodPutVideoReconcilesLegacyReviewAtActualUsage(t *testing.T) {
	ctx := t.Context()
	client := testEntClient(t)
	repo := &usageBillingRepository{db: integrationDB}
	group := mustCreateGroup(t, client, &service.Group{Name: "H3 actual " + uuid.NewString(), Platform: service.PlatformOpenAI})
	account := mustCreateAccount(t, client, &service.Account{Name: "GoodPut actual " + uuid.NewString(), Type: service.AccountTypeAPIKey, Platform: service.PlatformOpenAI})
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com", PasswordHash: "hash", Balance: 10})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, GroupID: &group.ID, Key: "sk-" + uuid.NewString(), Name: "GoodPut", Quota: 4.4, RateLimit5h: 4.4})
	job := service.DurableVideoJob{ID: uuid.NewString(), UserID: user.ID, APIKeyID: key.ID, GroupID: group.ID, AccountID: account.ID,
		Model: service.GoodPutH3Model, Seconds: 15, HoldAmount: 2.25, ReferenceAmount: 1.5, Multiplier: 1.5, Currency: "USD",
		AccountQuotaCost: 3, AccountRequestCount: 1}
	publicID := "gpv_" + uuid.NewString()
	require.NoError(t, repo.ReserveVideoJob(ctx, job, true))
	require.NoError(t, repo.AttachVideoReceipt(ctx, job.ID, publicID, "video_legacy"))
	_, err := integrationDB.ExecContext(ctx, `UPDATE goodput_video_jobs SET state='review' WHERE id=$1`, job.ID)
	require.NoError(t, err)
	claimed, err := repo.ClaimVideoJob(ctx)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, job.ID, claimed.ID)
	require.Equal(t, "video_legacy", claimed.NativeID)
	invalid, err := repo.CompleteGoodPutVideoJob(ctx, job.ID, 3)
	require.Error(t, err)
	require.False(t, invalid)
	applied, err := repo.FinishVideoJob(ctx, job.ID, "failed")
	require.NoError(t, err)
	require.False(t, applied, "legacy review can be an already completed video")
	var contenders errgroup.Group
	for range 2 {
		contenders.Go(func() error { _, err := repo.CompleteGoodPutVideoJob(ctx, job.ID, 14); return err })
	}
	require.NoError(t, contenders.Wait())
	var balance, frozen, quotaUsed, usage5h, total, actual float64
	var billed, n int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance,frozen_balance FROM users WHERE id=$1`, user.ID).Scan(&balance, &frozen))
	require.InDelta(t, 7.90, balance, 1e-8)
	require.Zero(t, frozen)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT quota_used,usage_5h FROM api_keys WHERE id=$1`, key.ID).Scan(&quotaUsed, &usage5h))
	require.InDelta(t, 2.10, quotaUsed, 1e-8)
	require.InDelta(t, 2.10, usage5h, 1e-8)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*),sum(total_cost),sum(actual_cost),max(video_duration_seconds)
		FROM usage_logs WHERE api_key_id=$1`, key.ID).Scan(&n, &total, &actual, &billed))
	require.Equal(t, 1, n)
	require.Equal(t, 14, billed)
	require.InDelta(t, 1.40, total, 1e-8)
	require.InDelta(t, 2.10, actual, 1e-8)
	saved, err := repo.FindVideoJob(ctx, publicID, user.ID, key.ID, group.ID)
	require.NoError(t, err)
	require.NotNil(t, saved)
	require.Equal(t, "completed", saved.State)
	require.Equal(t, 15, saved.Seconds)
	require.Equal(t, 14, saved.BilledSeconds)

	// A second 15s job fits the allowance only because the first settled for 14s.
	job.ID = uuid.NewString()
	require.NoError(t, repo.ReserveVideoJob(ctx, job, true))
	require.NoError(t, repo.AttachVideoReceipt(ctx, job.ID, "gpv_"+uuid.NewString(), "video_invalid_meter"))
	applied, err = repo.ReviewGoodPutVideoJob(ctx, job.ID, service.GoodPutReviewUsageUnverified)
	require.NoError(t, err)
	require.True(t, applied)
	applied, err = repo.FinishVideoJob(ctx, job.ID, "failed")
	require.NoError(t, err)
	require.False(t, applied, "a late failed status cannot refund a verified completion")
	applied, err = repo.CompleteGoodPutVideoJob(ctx, job.ID, 16)
	require.Error(t, err)
	require.False(t, applied)
	claimed, err = repo.ClaimVideoJob(ctx)
	require.NoError(t, err)
	require.Nil(t, claimed, "do not hot-loop a malformed usage receipt")
	other := job
	other.ID, other.HoldAmount, other.ReferenceAmount, other.Seconds = uuid.NewString(), .75, .50, 5
	require.ErrorIs(t, repo.ReserveVideoJob(ctx, other, true), service.ErrGoodPutVideoFunds, "unsettled quote still counts against key quota")
	_, err = integrationDB.ExecContext(ctx, `UPDATE api_keys SET quota=10,rate_limit_5h=10 WHERE id=$1`, key.ID)
	require.NoError(t, err)
	require.NoError(t, repo.ReserveVideoJob(ctx, other, true), "a known completed video must not block an independent submission")
	require.NoError(t, repo.AttachVideoReceipt(ctx, other.ID, "gpv_"+uuid.NewString(), "video_over_quote"))
	applied, err = repo.CompleteGoodPutVideoJob(ctx, other.ID, 6)
	require.NoError(t, err)
	require.False(t, applied, "valid model seconds must still fit the individual quote")
	_, err = repo.FinishVideoJob(ctx, other.ID, "failed")
	require.NoError(t, err)

	// Once the original usage can be verified, recover it using the same saved receipt.
	_, err = integrationDB.ExecContext(ctx, `UPDATE goodput_video_jobs SET next_poll_at=NOW() WHERE id=$1`, job.ID)
	require.NoError(t, err)
	claimed, err = repo.ClaimVideoJob(ctx)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, "video_invalid_meter", claimed.NativeID)
	applied, err = repo.CompleteGoodPutVideoJob(ctx, job.ID, 14)
	require.NoError(t, err)
	require.True(t, applied)
}

func TestGoodPutVideoIncidentsAreDurableAndIndependent(t *testing.T) {
	ctx := t.Context()
	client := testEntClient(t)
	ledger := &usageBillingRepository{db: integrationDB}
	ops := &opsRepository{db: integrationDB}
	group := mustCreateGroup(t, client, &service.Group{Name: "H3 incidents " + uuid.NewString(), Platform: service.PlatformOpenAI})
	account := mustCreateAccount(t, client, &service.Account{Name: "GoodPut incidents " + uuid.NewString(), Type: service.AccountTypeAPIKey, Platform: service.PlatformOpenAI})
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com", PasswordHash: "hash", Balance: 20})
	var ruleID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT id FROM ops_alert_rules WHERE metric_type=$1 AND enabled AND notify_email`, service.OpsAlertMetricGoodPutVideoIncident).Scan(&ruleID))
	rule := &service.OpsAlertRule{ID: ruleID, Name: "H3 incident", Severity: "P1", WindowMinutes: 15}
	reserve := func() service.DurableVideoJob {
		t.Helper()
		key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, GroupID: &group.ID, Key: "sk-" + uuid.NewString(), Name: "GoodPut"})
		job := service.DurableVideoJob{ID: uuid.NewString(), UserID: user.ID, APIKeyID: key.ID, GroupID: group.ID,
			AccountID: account.ID, Model: service.GoodPutH3Model, Seconds: 15, HoldAmount: 2.25, ReferenceAmount: 2.25, Multiplier: 1, Currency: "USD"}
		require.NoError(t, ledger.ReserveVideoJob(ctx, job, true))
		return job
	}
	unknown := reserve()
	_, err := integrationDB.ExecContext(ctx, `UPDATE goodput_video_jobs SET next_poll_at=NOW() WHERE id=$1`, unknown.ID)
	require.NoError(t, err)
	claimed, err := ledger.ClaimVideoJob(ctx)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, unknown.ID, claimed.ID)
	require.Empty(t, claimed.NativeID)
	read := func() []*service.OpsAlertEvent {
		t.Helper()
		events, err := ops.SyncGoodPutVideoAlerts(ctx, rule, &group.ID)
		require.NoError(t, err)
		return events
	}
	events := read()
	require.Len(t, events, 1)
	first := events[0]
	require.Equal(t, unknown.ID, first.Dimensions["task_id"])
	require.Equal(t, service.OpsAlertStatusFiring, first.Status)
	require.Equal(t, first.ID, read()[0].ID, "retry delivery of the same incident before SMTP succeeds")
	require.NoError(t, ops.UpdateAlertEventEmailSent(ctx, first.ID, true))
	require.Empty(t, read())

	job := reserve()
	publicID := "gpv_" + uuid.NewString()
	require.NoError(t, ledger.AttachVideoReceipt(ctx, job.ID, publicID, "video_private_receipt"))
	_, err = integrationDB.ExecContext(ctx, `UPDATE goodput_video_jobs SET state='review' WHERE id=$1`, job.ID)
	require.NoError(t, err)
	applied, err := ledger.CompleteGoodPutVideoJob(ctx, job.ID, 14)
	require.NoError(t, err)
	require.True(t, applied)
	events = read()
	require.Len(t, events, 1, "a separate incident is not suppressed by the unresolved one")
	recovered := events[0]
	require.Equal(t, service.OpsAlertStatusResolved, recovered.Status, "repair before the next scan is still reported once")
	require.Equal(t, publicID, recovered.Dimensions["task_id"])
	require.NotContains(t, recovered.Description, "video_private_receipt")
	require.NoError(t, ops.UpdateAlertEventEmailSent(ctx, recovered.ID, true))

	require.NoError(t, ledger.RecordGoodPutContentOutcome(ctx, job.ID, true))
	require.Empty(t, read(), "give transient download failures time to recover")
	_, err = integrationDB.ExecContext(ctx, `UPDATE goodput_video_jobs SET content_failure_at=NOW()-INTERVAL '2 minutes' WHERE id=$1`, job.ID)
	require.NoError(t, err)
	events = read()
	require.Len(t, events, 1)
	delivery := events[0]
	require.Equal(t, service.OpsAlertStatusFiring, delivery.Status)
	require.NotEqual(t, recovered.ID, delivery.ID)
	require.NoError(t, ops.UpdateAlertEventEmailSent(ctx, delivery.ID, true))
	require.NoError(t, ledger.RecordGoodPutContentOutcome(ctx, job.ID, false))
	require.Empty(t, read())
	delivery, err = ops.GetAlertEventByID(ctx, delivery.ID)
	require.NoError(t, err)
	require.Equal(t, service.OpsAlertStatusResolved, delivery.Status)
}
