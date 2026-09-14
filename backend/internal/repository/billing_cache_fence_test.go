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

func TestBalanceCacheFenceRejectsLatePreInvalidationRefill(t *testing.T) {
	cache, _ := newMiniRedisCache(t)
	ctx := context.Background()

	// A cache miss observed one unique token and read the old balance before a
	// reviewed refund committed its reservation.
	oldFence, err := cache.CaptureBalanceCacheFence(ctx, 23)
	require.NoError(t, err)
	require.NotEmpty(t, oldFence)

	require.NoError(t, cache.InvalidateUserBalance(ctx, 23))
	wrote, err := cache.SetUserBalanceIfFence(ctx, 23, 100, oldFence)
	require.NoError(t, err)
	require.False(t, wrote, "a late balance fill must not recreate pre-refund authorization")
	_, err = cache.GetUserBalance(ctx, 23)
	require.ErrorIs(t, err, redis.Nil)

	currentFence, err := cache.CaptureBalanceCacheFence(ctx, 23)
	require.NoError(t, err)
	require.NotEmpty(t, currentFence)
	require.NotEqual(t, oldFence, currentFence)
	wrote, err = cache.SetUserBalanceIfFence(ctx, 23, 0, currentFence)
	require.NoError(t, err)
	require.True(t, wrote)
	balance, err := cache.GetUserBalance(ctx, 23)
	require.NoError(t, err)
	require.Zero(t, balance)
}

func TestBalanceCacheInvalidationDeletesFencedAndLegacyKeys(t *testing.T) {
	cache, _ := newMiniRedisCache(t)
	ctx := context.Background()

	require.NoError(t, cache.SetUserBalance(ctx, 29, 100))
	require.NoError(t, cache.rdb.Set(ctx, legacyBillingBalanceKey(29), 100, time.Hour).Err())
	balance, err := cache.GetUserBalance(ctx, 29)
	require.NoError(t, err)
	require.Equal(t, float64(100), balance)

	require.NoError(t, cache.InvalidateUserBalance(ctx, 29))
	_, err = cache.GetUserBalance(ctx, 29)
	require.ErrorIs(t, err, redis.Nil)
	_, err = cache.rdb.Get(ctx, legacyBillingBalanceKey(29)).Result()
	require.ErrorIs(t, err, redis.Nil)
	fence, err := cache.CaptureBalanceCacheFence(ctx, 29)
	require.NoError(t, err)
	require.NotEmpty(t, fence)
	ttl, err := cache.rdb.TTL(ctx, billingBalanceFenceKey(29)).Result()
	require.NoError(t, err)
	require.Positive(t, ttl)
}

func TestBalanceCacheFenceExpiryCannotAdmitAnOldToken(t *testing.T) {
	cache, _ := newMiniRedisCache(t)
	ctx := context.Background()

	oldFence, err := cache.CaptureBalanceCacheFence(ctx, 31)
	require.NoError(t, err)
	require.NoError(t, cache.InvalidateUserBalance(ctx, 31))
	// Model TTL expiry explicitly. A stale reader still holds oldFence; missing
	// must not mean generation zero or otherwise match that prior observation.
	require.NoError(t, cache.rdb.Del(ctx, billingBalanceFenceKey(31)).Err())
	wrote, err := cache.SetUserBalanceIfFence(ctx, 31, 100, oldFence)
	require.NoError(t, err)
	require.False(t, wrote)

	freshFence, err := cache.CaptureBalanceCacheFence(ctx, 31)
	require.NoError(t, err)
	require.NotEmpty(t, freshFence)
	require.NotEqual(t, oldFence, freshFence)
	wrote, err = cache.SetUserBalanceIfFence(ctx, 31, 0, freshFence)
	require.NoError(t, err)
	require.True(t, wrote)
}
