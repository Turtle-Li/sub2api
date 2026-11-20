//go:build integration

package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestHiAPIVideoJobsAreIsolatedAndSettleExactlyOnce(t *testing.T) {
	ctx := t.Context()
	client := testEntClient(t)
	repo := &usageBillingRepository{db: integrationDB}
	ledger := repo.HiAPIVideoJobs()
	group := mustCreateGroup(t, client, &service.Group{Name: uuid.NewString(), Platform: service.PlatformOpenAI, IsExclusive: true})
	account := mustCreateAccount(t, client, &service.Account{Name: uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey})
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com", PasswordHash: "hash", Balance: 1})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, GroupID: &group.ID, Key: "sk-" + uuid.NewString(), Name: "HiAPI", Quota: 1})
	job := service.DurableVideoJob{ID: uuid.NewString(), UserID: user.ID, APIKeyID: key.ID, GroupID: group.ID,
		AccountID: account.ID, Model: service.HiAPISeedanceMiniModel, HoldAmount: .6, ReferenceAmount: .6,
		Seconds: 4, Resolution: service.VideoBillingResolution480P, Currency: "CNY", Multiplier: 1, AccountRate: 1}
	require.NoError(t, ledger.ReserveVideoJob(ctx, job, false))
	publicID := "hav_" + uuid.NewString()
	require.NoError(t, ledger.AttachVideoReceipt(ctx, job.ID, publicID, "tk-hiapi-01HZTQ8BX2N3GM3YFK4Z9D7VQR"))
	wrongKey, err := ledger.FindVideoJob(ctx, publicID, job.UserID, key.ID+1, group.ID)
	require.NoError(t, err)
	require.Nil(t, wrongKey)
	wrongLedger, err := repo.FindVideoJob(ctx, publicID, job.UserID, key.ID, group.ID)
	require.NoError(t, err)
	require.Nil(t, wrongLedger)
	previousWorker, err := repo.ClaimVideoJob(ctx)
	require.NoError(t, err)
	require.Nil(t, previousWorker)
	claim, err := ledger.ClaimVideoJob(ctx)
	require.NoError(t, err)
	require.Equal(t, job.ID, claim.ID)
	require.Equal(t, "480p", claim.Resolution)
	for _, expected := range []bool{true, false} {
		applied, err := ledger.FinishVideoJob(ctx, job.ID, "completed")
		require.NoError(t, err)
		require.Equal(t, expected, applied)
	}
	var rows int
	var charge, balance, frozen float64
	var resolution, endpoint string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*),SUM(actual_cost),MIN(video_resolution),MIN(upstream_endpoint) FROM usage_logs WHERE api_key_id=$1`, key.ID).Scan(&rows, &charge, &resolution, &endpoint))
	require.Equal(t, 1, rows)
	require.Equal(t, "480p", resolution)
	require.Equal(t, "/v1/tasks", endpoint)
	require.InDelta(t, .6, charge, 1e-8)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance,frozen_balance FROM users WHERE id=$1`, user.ID).Scan(&balance, &frozen))
	require.InDelta(t, .4, balance, 1e-8)
	require.Zero(t, frozen)
}
