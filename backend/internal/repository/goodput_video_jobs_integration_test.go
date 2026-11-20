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
		contenders.Go(func() error { _, err := repo.FinishVideoJob(ctx, job.ID, "completed"); return err })
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
	applied, err = repo.FinishVideoJob(ctx, unknown.ID, "completed")
	require.NoError(t, err)
	require.True(t, applied)
	wallet(.5, 0)

}
