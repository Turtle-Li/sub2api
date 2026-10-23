//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerCacheSnapshotMemoProtectedRetirementInvalidates(t *testing.T) {
	ctx := context.Background()
	cache, _, _ := newSchedulerCacheMemoUnit(t, 1)
	bucket := service.SchedulerBucket{GroupID: 901, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	ordinary := memoTestAccountOn(service.PlatformOpenAI, 901, "ordinary")
	ordinary.Type = service.AccountTypeAPIKey
	memoPublish(t, cache, bucket, ordinary)
	require.Equal(t, "ordinary", memoRead(t, cache, bucket)[0].Name)
	before := memoRevision(t, cache, schedulerAccountMetaRevisionKey)

	protected := ordinary
	protected.Type = service.AccountTypeOAuth
	protected.Name = "protected"
	require.NoError(t, cache.RetireAccountSnapshot(ctx, &protected))
	require.NotEqual(t, before, memoRevision(t, cache, schedulerAccountMetaRevisionKey))
	got := memoRead(t, cache, bucket)
	require.Equal(t, "protected", got[0].Name)
	require.NotContains(t, got[0].Credentials, "access_token")
	full, err := cache.GetAccount(ctx, ordinary.ID)
	require.NoError(t, err)
	require.Nil(t, full)

	require.NoError(t, cache.SetAccount(ctx, &ordinary))
	require.Equal(t, "protected", memoRead(t, cache, bucket)[0].Name, "a delayed ordinary publisher cannot replace protected metadata")
}

func TestSchedulerCacheSnapshotMemoTerminalRetirementInvalidates(t *testing.T) {
	ctx := context.Background()
	cache, _, _ := newSchedulerCacheMemoUnit(t, 1)
	bucket := memoTestBucket(902)
	account := memoTestAccount(902, "before-delete")
	memoPublish(t, cache, bucket, account)
	require.Len(t, memoRead(t, cache, bucket), 1)
	before := memoRevision(t, cache, schedulerAccountMetaRevisionKey)

	require.NoError(t, cache.RetireDeletedAccountSnapshot(ctx, account.ID))
	require.NotEqual(t, before, memoRevision(t, cache, schedulerAccountMetaRevisionKey))
	memoRequireMiss(t, cache, bucket)
	require.NoError(t, cache.SetAccount(ctx, &account))
	memoRequireMiss(t, cache, bucket)
}

func TestSchedulerCacheSnapshotMemoUnencodableRetirementInvalidates(t *testing.T) {
	ctx := context.Background()
	cache, _, _ := newSchedulerCacheMemoUnit(t, 1)
	bucket := service.SchedulerBucket{GroupID: 903, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	account := memoTestAccountOn(service.PlatformOpenAI, 903, "before-invalid")
	memoPublish(t, cache, bucket, account)
	require.Len(t, memoRead(t, cache, bucket), 1)
	invalid := time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
	account.ExpiresAt = &invalid

	require.Error(t, cache.RetireAccountSnapshot(ctx, &account))
	memoRequireMiss(t, cache, bucket)
	full, err := cache.GetAccount(ctx, account.ID)
	require.NoError(t, err)
	require.Nil(t, full)
}

func TestSchedulerCacheSnapshotMemoTrailingUnencodableWriteKeepsRevision(t *testing.T) {
	ctx := context.Background()
	cache, _, recorder := newSchedulerCacheMemoUnit(t, 1)
	bucket := memoTestBucket(904)
	account := memoTestAccount(904, "before")
	memoPublish(t, cache, bucket, account)
	require.Equal(t, "before", memoRead(t, cache, bucket)[0].Name)
	account.Name = "after"
	invalid := memoTestAccount(905, "invalid")
	invalidTime := time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
	invalid.ExpiresAt = &invalidTime
	recorder.reset()
	ids, err := cache.writeAccountIDs(ctx, []service.Account{account, invalid})
	require.NoError(t, err)
	require.Equal(t, []int64{904}, ids)
	require.Len(t, recorder.pipelineBatches(), 1, "an unencodable trailing account does not flush an extra chunk")
	require.Equal(t, "after", memoRead(t, cache, bucket)[0].Name)
}
