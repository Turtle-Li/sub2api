//go:build integration

package repository

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestVideoStudioReceiptIsSingleWriterOwnerBoundAndRestartSafe(t *testing.T) {
	ctx := t.Context()
	store := &apiKeyRepository{sql: integrationDB}
	client := testEntClient(t)
	u := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com", PasswordHash: "hash"})
	owner := u.ID
	g := mustCreateGroup(t, client, &service.Group{Name: "Studio " + uuid.NewString(), Platform: service.PlatformOpenAI, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{service.GoodPutH3Model}}})
	_, err := integrationDB.ExecContext(ctx, `UPDATE groups SET allow_image_generation=true WHERE id=$1`, g.ID)
	require.NoError(t, err)
	k := mustCreateApiKey(t, client, &service.APIKey{UserID: owner, GroupID: &g.ID, Key: "sk-" + uuid.NewString(), Name: "Studio"})
	j := service.VideoStudioJob{ID: uuid.NewString(), UserID: owner, KeyID: k.ID, GroupID: g.ID, Model: service.GoodPutH3Model, Seconds: 5, Size: "1344x768", Fingerprint: "hash"}
	zero := int64(0)
	j.Parameters = &service.VideoStudioParameters{MaterialMode: "references", ReferenceCount: 2, Seed: &zero}
	var count atomic.Int32
	var group errgroup.Group
	for range 12 {
		group.Go(func() error {
			won, err := store.ClaimVideoStudioJob(ctx, j)
			if won {
				count.Add(1)
			}
			return err
		})
	}
	require.NoError(t, group.Wait())
	require.Equal(t, int32(1), count.Load())
	fresh := &apiKeyRepository{sql: integrationDB}
	_, err = fresh.FindVideoStudioJob(ctx, owner+1, j.ID)
	require.ErrorIs(t, err, service.ErrVideoStudioNotFound)
	r, err := fresh.FindVideoStudioJob(ctx, owner, j.ID)
	require.NoError(t, err)
	require.Equal(t, "submitting", r.Status)
	require.Equal(t, "hash", r.Fingerprint)
	require.Equal(t, j.Parameters, r.Parameters)
	won, err := fresh.ClaimVideoStudioJob(ctx, j)
	require.NoError(t, err)
	require.False(t, won)
	require.NoError(t, fresh.UpdateVideoStudioJob(ctx, owner, j.ID, "gpv_receipt", "running", false))
	require.NoError(t, fresh.UpdateVideoStudioJob(ctx, owner, j.ID, "gpv_receipt", "completed", true))
	require.NoError(t, fresh.UpdateVideoStudioJob(ctx, owner, j.ID, "gpv_receipt", "queued", false))
	r, err = fresh.FindVideoStudioJob(ctx, owner, j.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", r.Status)
	require.True(t, r.BillingReview)
	require.Error(t, fresh.UpdateVideoStudioJob(ctx, owner, j.ID, "different", "queued", false))
	require.NoError(t, fresh.UpdateVideoStudioJob(ctx, owner, j.ID, "gpv_receipt", "completed", false))
	require.NoError(t, fresh.UpdateVideoStudioJob(ctx, owner, j.ID, "gpv_receipt", "completed", true))
	r, err = fresh.FindVideoStudioJob(ctx, owner, j.ID)
	require.NoError(t, err)
	require.False(t, r.BillingReview, "a stale parallel observation cannot undo verified usage")
	next := j
	next.ID = uuid.NewString()
	next.Parameters = nil
	_, err = store.ClaimVideoStudioJob(ctx, next)
	require.NoError(t, err)
	items, err := store.ListVideoStudioJobs(ctx, owner, "", 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	legacy, err := fresh.FindVideoStudioJob(ctx, owner, next.ID)
	require.NoError(t, err)
	require.Nil(t, legacy.Parameters)
	following, err := store.ListVideoStudioJobs(ctx, owner, items[0].ID, 10)
	require.NoError(t, err)
	require.NotEmpty(t, following)
	require.NotEqual(t, items[0].ID, following[0].ID)
	empty, err := store.ListVideoStudioJobs(ctx, owner+1, items[0].ID, 10)
	require.NoError(t, err)
	require.Empty(t, empty)
	ids, err := store.VideoStudioGroupIDs(context.Background(), owner)
	require.NoError(t, err)
	require.Contains(t, ids, g.ID)
	_, err = integrationDB.ExecContext(ctx, `UPDATE api_keys SET quota=1, quota_used=1 WHERE id=$1`, k.ID)
	require.NoError(t, err)
	ids, err = store.VideoStudioGroupIDs(ctx, owner)
	require.NoError(t, err)
	require.Contains(t, ids, g.ID, "key quota must not control the session wallet")
}

func TestVideoStudioPosterCacheUsesRealRedis(t *testing.T) {
	c := NewGatewayCache(integrationRedis).(service.VideoStudioPosterCache)
	ctx := t.Context()
	k := uuid.NewString()
	lease := uuid.NewString()
	won, err := c.ClaimStudioPoster(ctx, k, lease, time.Minute)
	require.NoError(t, err)
	require.True(t, won)
	data := []byte("fixture-poster-bytes")
	require.NoError(t, c.SaveStudioPoster(ctx, k, uuid.NewString(), data, time.Hour))
	sizes, err := c.StudioPosterSizes(ctx, []string{k, "other-" + k})
	require.NoError(t, err)
	require.Equal(t, []int64{int64(len(lease)), 0}, sizes)
	require.NoError(t, c.SaveStudioPoster(ctx, k, lease, data, time.Minute))
	got, err := c.StudioPosterBytes(ctx, k)
	require.NoError(t, err)
	require.Equal(t, data, got)
	ttl, err := integrationRedis.PTTL(ctx, studioPosterPrefix+k).Result()
	require.NoError(t, err)
	require.Positive(t, ttl)
	require.LessOrEqual(t, ttl, time.Minute)
}

func TestVideoStudioFileDurationIsScopedAndIndependentOfBilling(t *testing.T) {
	ctx := t.Context()
	store := &apiKeyRepository{sql: integrationDB}
	cl := testEntClient(t)
	u := mustCreateUser(t, cl, &service.User{Email: uuid.NewString() + "@example.com", PasswordHash: "hash"})
	j := service.VideoStudioJob{ID: uuid.NewString(), UserID: u.ID, GroupID: 514, KeyID: 0, Model: service.MuseVideoModel, Seconds: 6, Fingerprint: "measurement-test"}
	won, err := store.ClaimVideoStudioJob(ctx, j)
	require.NoError(t, err)
	require.True(t, won)
	check := func(want int64) {
		t.Helper()
		v, err := store.FindVideoStudioJob(ctx, u.ID, j.ID)
		require.NoError(t, err)
		require.Equal(t, want, v.ActualDurationMS)
		require.Equal(t, 6, v.Seconds)
		require.Nil(t, v.ChargedAmount)
	}
	j.Receipt = "muse_test"
	require.NoError(t, store.RecordVideoStudioDuration(ctx, j, 10000))
	check(0)
	require.NoError(t, store.UpdateVideoStudioJob(ctx, u.ID, j.ID, j.Receipt, "completed", false))
	check(0)
	for _, change := range []func(*service.VideoStudioJob){func(k *service.VideoStudioJob) { k.UserID++ }, func(k *service.VideoStudioJob) { k.GroupID++ }, func(k *service.VideoStudioJob) { k.KeyID++ }, func(k *service.VideoStudioJob) { k.Receipt += "x" }, func(k *service.VideoStudioJob) { k.Model = "other" }, func(k *service.VideoStudioJob) { k.ID = uuid.NewString() }} {
		other := j
		change(&other)
		require.NoError(t, store.RecordVideoStudioDuration(ctx, other, 999))
		check(0)
	}
	require.NoError(t, store.RecordVideoStudioDuration(ctx, j, 0))
	check(0)
	// The reconciliation worker and the request handler share the same guarded write.
	worker := &usageBillingRepository{db: integrationDB}
	require.NoError(t, worker.RecordVideoStudioDuration(ctx, j, 10000))
	require.NoError(t, store.RecordVideoStudioDuration(ctx, j, 6000))
	check(10000)
	rows, err := store.ListVideoStudioJobs(ctx, u.ID, "", 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(10000), rows[0].ActualDurationMS)
}
