//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerCachePaidFallbackSnapshotClassification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]any
		paid  bool
	}{
		{name: "absent"},
		{name: "false", extra: map[string]any{service.PaidFallbackExtraKey: false}},
		{name: "true", extra: map[string]any{service.PaidFallbackExtraKey: true}, paid: true},
		{name: "null", extra: map[string]any{service.PaidFallbackExtraKey: nil}, paid: true},
		{name: "string", extra: map[string]any{service.PaidFallbackExtraKey: "false"}, paid: true},
		{name: "number", extra: map[string]any{service.PaidFallbackExtraKey: 0}, paid: true},
		{name: "array", extra: map[string]any{service.PaidFallbackExtraKey: []any{}}, paid: true},
		{name: "object", extra: map[string]any{service.PaidFallbackExtraKey: map[string]any{}}, paid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cache := newSchedulerCacheUnit(t)
			account := service.Account{
				ID: 238, Platform: service.PlatformCline, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{33}, Extra: tc.extra,
			}
			require.Equal(t, tc.paid, account.IsPaidFallback())
			bucket := service.SchedulerBucket{GroupID: 33, Platform: service.PlatformCline, Mode: service.SchedulerModeSingle}
			token, err := cache.CaptureBucketWriteToken(ctx, bucket)
			require.NoError(t, err)
			require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))

			full, err := cache.GetAccount(ctx, account.ID)
			require.NoError(t, err)
			require.NotNil(t, full)
			require.Equal(t, tc.paid, full.IsPaidFallback())
			for read := 0; read < 2; read++ {
				snapshot, hit, err := cache.GetSnapshot(ctx, bucket)
				require.NoError(t, err)
				require.True(t, hit)
				require.Len(t, snapshot, 1)
				require.Equal(t, tc.paid, snapshot[0].IsPaidFallback(), "metadata must retain the full account's pool classification")
				_, expectedPresence := tc.extra[service.PaidFallbackExtraKey]
				marker, present := snapshot[0].Extra[service.PaidFallbackExtraKey]
				require.Equal(t, expectedPresence, present)
				if tc.name == "null" {
					require.Nil(t, marker, "an explicit null is quarantined, unlike an absent marker")
				}
			}
		})
	}
}

func TestSchedulerCachePaidFallbackMarkerRefresh(t *testing.T) {
	ctx := context.Background()
	cache := newSchedulerCacheUnit(t)
	account := service.Account{
		ID: 238, Platform: service.PlatformCline, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{33},
		Extra: map[string]any{service.PaidFallbackExtraKey: false},
	}
	bucket := service.SchedulerBucket{GroupID: 33, Platform: service.PlatformCline, Mode: service.SchedulerModeSingle}
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))
	for _, marker := range []any{true, false, nil} {
		account.Extra[service.PaidFallbackExtraKey] = marker
		require.NoError(t, cache.SetAccount(ctx, &account))
		snapshot, hit, err := cache.GetSnapshot(ctx, bucket)
		require.NoError(t, err)
		require.True(t, hit)
		require.Len(t, snapshot, 1)
		require.Equal(t, account.IsPaidFallback(), snapshot[0].IsPaidFallback(), "account refresh must update warm snapshot classification without rebuilding the bucket")
	}
}

func TestSchedulerCachePaidFallbackIgnoresLegacyMetadata(t *testing.T) {
	ctx := context.Background()
	cache := newSchedulerCacheUnit(t)
	account := service.Account{
		ID: 238, Platform: service.PlatformCline, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{33},
		Extra: map[string]any{service.PaidFallbackExtraKey: true},
	}
	bucket := service.SchedulerBucket{GroupID: 33, Platform: service.PlatformCline, Mode: service.SchedulerModeSingle}
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))
	id := strconv.FormatInt(account.ID, 10)
	legacy := account
	legacy.Extra = nil
	payload, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.NoError(t, cache.rdb.Del(ctx, schedulerAccountMetaKey(id)).Err())
	legacyKey := "sched:meta:" + id
	require.NoError(t, cache.rdb.Set(ctx, legacyKey, payload, 0).Err())

	_, hit, err := cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.False(t, hit, "old metadata lost the marker and must trigger the existing DB fallback")

	require.NoError(t, cache.SetAccount(ctx, &account))
	require.NoError(t, cache.rdb.Set(ctx, legacyKey, payload, 0).Err(), "a draining old writer may still publish legacy metadata")
	snapshot, hit, err := cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.True(t, hit)
	require.Len(t, snapshot, 1)
	require.True(t, snapshot[0].IsPaidFallback(), "legacy publication must not overwrite the repaired classification")
}
