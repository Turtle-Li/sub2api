//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestSubscriptionCacheFenceRejectsLatePreInvalidationRefill(t *testing.T) {
	cache, _ := newMiniRedisCache(t)
	ctx := context.Background()
	old := &service.SubscriptionCacheData{
		Status: "active", ExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour), Version: 10,
	}
	updated := &service.SubscriptionCacheData{
		Status: "active", ExpiresAt: old.ExpiresAt.Add(-20 * 24 * time.Hour), Version: 11,
	}

	// This is the generation a cache-miss reader observed before it queried an
	// old subscription row. A refund commits before that reader can refill.
	oldFence, err := cache.CaptureSubscriptionCacheFence(ctx, 7, 9)
	require.NoError(t, err)
	require.Zero(t, oldFence)
	require.NoError(t, cache.InvalidateSubscriptionCache(ctx, 7, 9))

	wrote, err := cache.SetSubscriptionCacheIfFence(ctx, 7, 9, old, oldFence)
	require.NoError(t, err)
	require.False(t, wrote, "a late reader must not recreate the pre-refund authorization cache")
	_, err = cache.GetSubscriptionCache(ctx, 7, 9)
	require.ErrorIs(t, err, redis.Nil)

	currentFence, err := cache.CaptureSubscriptionCacheFence(ctx, 7, 9)
	require.NoError(t, err)
	require.Equal(t, int64(1), currentFence)
	wrote, err = cache.SetSubscriptionCacheIfFence(ctx, 7, 9, updated, currentFence)
	require.NoError(t, err)
	require.True(t, wrote)

	got, err := cache.GetSubscriptionCache(ctx, 7, 9)
	require.NoError(t, err)
	require.Equal(t, updated.ExpiresAt.Unix(), got.ExpiresAt.Unix())
	require.Equal(t, updated.Version, got.Version)
}

func TestSubscriptionCacheInvalidationDeletesFencedAndLegacyKeys(t *testing.T) {
	cache, _ := newMiniRedisCache(t)
	ctx := context.Background()
	data := &service.SubscriptionCacheData{Status: "active", ExpiresAt: time.Now().UTC().Add(time.Hour), Version: 3}
	require.NoError(t, cache.SetSubscriptionCache(ctx, 17, 19, data))
	require.NoError(t, cache.rdb.HSet(ctx, legacyBillingSubKey(17, 19), subFieldStatus, "active").Err())

	require.NoError(t, cache.InvalidateSubscriptionCache(ctx, 17, 19))
	_, err := cache.GetSubscriptionCache(ctx, 17, 19)
	require.ErrorIs(t, err, redis.Nil)
	legacy, err := cache.rdb.HGetAll(ctx, legacyBillingSubKey(17, 19)).Result()
	require.NoError(t, err)
	require.Empty(t, legacy)

	fence, err := cache.CaptureSubscriptionCacheFence(ctx, 17, 19)
	require.NoError(t, err)
	require.Equal(t, int64(1), fence)
}
