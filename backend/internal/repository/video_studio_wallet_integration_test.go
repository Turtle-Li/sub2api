//go:build integration

package repository

import (
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestVideoStudioMuseWalletWithoutAnyKey(t *testing.T) {
	ctx := t.Context()
	client := testEntClient(t)
	store := &apiKeyRepository{sql: integrationDB}
	r := (&usageBillingRepository{db: integrationDB}).MuseStudioVideoJobs()
	u := mustCreateUser(t, client, &service.User{Balance: 2})
	other := mustCreateUser(t, client, &service.User{Balance: 2})
	g := mustCreateGroup(t, client, &service.Group{Name: "Muse Wallet " + uuid.NewString(), Platform: service.PlatformOpenAI, IsExclusive: true, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{service.MuseVideoModel}}})
	a := mustCreateAccount(t, client, &service.Account{Name: "local-only " + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey})
	_, err := integrationDB.ExecContext(ctx, `UPDATE groups SET allow_image_generation=true WHERE id=$1`, g.ID)
	require.NoError(t, err)
	exec := func(q string, args ...any) {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx, q, args...)
		require.NoError(t, err)
	}
	countKeys := func() int {
		t.Helper()
		var n int
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM api_keys WHERE user_id=$1`, u.ID).Scan(&n))
		return n
	}
	wallet := func(balance, frozen float64) {
		t.Helper()
		var b, f float64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance,frozen_balance FROM users WHERE id=$1`, u.ID).Scan(&b, &f))
		require.InDelta(t, balance, b, 1e-8)
		require.InDelta(t, frozen, f, 1e-8)
	}
	ids, err := store.VideoStudioGroupIDs(ctx, u.ID)
	require.NoError(t, err)
	require.NotContains(t, ids, g.ID)
	require.Zero(t, countKeys())
	j := service.DurableVideoJob{ID: uuid.NewString(), UserID: u.ID, GroupID: g.ID, AccountID: a.ID, Model: service.MuseVideoModel, Seconds: 6, Resolution: "720p", Currency: "USD", Multiplier: 1, HoldAmount: .9, ReferenceAmount: .9}
	claim := func(job *service.DurableVideoJob) {
		t.Helper()
		job.StudioRequestID = uuid.NewString()
		ok, err := store.ClaimVideoStudioJob(ctx, service.VideoStudioJob{ID: job.StudioRequestID, UserID: job.UserID, GroupID: job.GroupID, Model: job.Model, Seconds: job.Seconds, Fingerprint: "local"})
		require.NoError(t, err)
		require.True(t, ok)
	}
	claim(&j)
	require.ErrorIs(t, r.ReserveVideoJob(ctx, j, false), service.ErrGoodPutVideoGroup)
	wallet(2, 0)
	exec(`INSERT INTO user_allowed_groups(user_id,group_id) VALUES($1,$2),($3,$2)`, u.ID, g.ID, other.ID)
	ids, err = store.VideoStudioGroupIDs(ctx, u.ID)
	require.NoError(t, err)
	require.Contains(t, ids, g.ID)
	insufficient := j
	insufficient.HoldAmount = 2.01
	require.ErrorIs(t, r.ReserveVideoJob(ctx, insufficient, false), service.ErrGoodPutVideoFunds)
	wallet(2, 0)
	require.NoError(t, r.ReserveVideoJob(ctx, j, false))
	wallet(1.1, .9)
	next := j
	next.ID = uuid.NewString()
	require.ErrorIs(t, r.ReserveVideoJob(ctx, next, false), service.ErrGoodPutVideoLimit)
	wallet(1.1, .9)
	next.UserID = other.ID
	claim(&next)
	require.NoError(t, r.ReserveVideoJob(ctx, next, false), "zero is not a cross-user concurrency bucket")
	done, err := r.FinishVideoJob(ctx, next.ID, "failed")
	require.NoError(t, err)
	require.True(t, done)
	native := "task_" + uuid.NewString()[:8] + "0123456789ab"
	id := "muse_" + native
	require.NoError(t, r.AttachVideoReceipt(ctx, j.ID, id, native))
	for _, owner := range [][3]int64{{other.ID, 0, g.ID}, {u.ID, 1, g.ID}, {u.ID, 0, g.ID + 1}} {
		found, err := r.FindVideoJob(ctx, id, owner[0], owner[1], owner[2])
		require.NoError(t, err)
		require.Nil(t, found)
	}
	var applied atomic.Int32
	var parallel errgroup.Group
	for range 10 {
		parallel.Go(func() error {
			ok, err := r.FinishVideoJob(ctx, j.ID, "completed")
			if ok {
				applied.Add(1)
			}
			return err
		})
	}
	require.NoError(t, parallel.Wait())
	require.EqualValues(t, 1, applied.Load())
	wallet(1.1, 0)
	require.Zero(t, countKeys())
	charged, err := r.FindVideoJob(ctx, id, u.ID, 0, g.ID)
	require.NoError(t, err)
	require.NotNil(t, charged)
	require.Equal(t, "completed", charged.State)
	listed, err := store.FindVideoStudioJob(ctx, j.UserID, j.StudioRequestID)
	require.NoError(t, err)
	require.Equal(t, id, listed.Receipt)
	require.Equal(t, "completed", listed.Status)
	require.NotNil(t, listed.ChargedAmount)
	require.InDelta(t, .9, *listed.ChargedAmount, 1e-8)
	require.Equal(t, "USD", *listed.ChargedCurrency)
	require.Equal(t, g.ID, charged.GroupID)
	require.InDelta(t, .9, charged.HoldAmount, 1e-8)
	exec(`DELETE FROM user_allowed_groups WHERE user_id=$1 AND group_id=$2`, u.ID, g.ID)
	next.UserID = u.ID
	next.ID = uuid.NewString()
	require.ErrorIs(t, r.ReserveVideoJob(ctx, next, false), service.ErrGoodPutVideoGroup)
	wallet(1.1, 0)
}
