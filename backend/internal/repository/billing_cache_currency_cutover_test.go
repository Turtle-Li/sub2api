package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type currencyCutoverCacheBypassStub struct {
	active bool
}

func (s currencyCutoverCacheBypassStub) Enabled() bool { return s.active }

func newCurrencyCutoverBillingCaches(t *testing.T) (service.BillingCache, service.BillingCache) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	return NewBillingCache(rdb), newBillingCacheWithCurrencyCutoverBypass(rdb, currencyCutoverCacheBypassStub{active: true})
}

func TestCurrencyCutoverBillingCacheBypassesWalletBackedReadsAndWrites(t *testing.T) {
	ctx := context.Background()
	normal, bypassed := newCurrencyCutoverBillingCaches(t)
	limit := 20.0
	initialQuota := &service.UserPlatformQuotaCacheEntry{
		DailyUsageUSD: 3,
		Version:       1,
		SchemaVersion: service.UserPlatformQuotaCacheSchemaV1,
		DailyLimitUSD: &limit,
		DailyWindowStart: func() *time.Time {
			now := time.Now().UTC()
			return &now
		}(),
	}
	initialRate := &service.APIKeyRateLimitCacheData{Usage5h: 1, Usage1d: 2, Usage7d: 3}

	require.NoError(t, normal.SetUserBalance(ctx, 1, 10))
	require.NoError(t, normal.SetAPIKeyRateLimit(ctx, 2, initialRate))
	require.NoError(t, normal.SetUserPlatformQuotaCache(ctx, 1, "openai", initialQuota, time.Hour))

	_, err := bypassed.GetUserBalance(ctx, 1)
	require.ErrorIs(t, err, redis.Nil)
	_, err = bypassed.GetAPIKeyRateLimit(ctx, 2)
	require.ErrorIs(t, err, redis.Nil)
	entry, ok, err := bypassed.GetUserPlatformQuotaCache(ctx, 1, "openai")
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, entry)

	require.NoError(t, bypassed.SetUserBalance(ctx, 1, 99))
	require.NoError(t, bypassed.DeductUserBalance(ctx, 1, 2))
	require.NoError(t, bypassed.SetAPIKeyRateLimit(ctx, 2, &service.APIKeyRateLimitCacheData{Usage5h: 99}))
	require.NoError(t, bypassed.UpdateAPIKeyRateLimitUsage(ctx, 2, 2))
	require.NoError(t, bypassed.SetUserPlatformQuotaCache(ctx, 1, "openai", &service.UserPlatformQuotaCacheEntry{SchemaVersion: service.UserPlatformQuotaCacheSchemaV1}, time.Hour))
	require.NoError(t, bypassed.IncrUserPlatformQuotaUsageCache(ctx, 1, "openai", 2, time.Hour, true))

	balance, err := normal.GetUserBalance(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 10.0, balance)
	rate, err := normal.GetAPIKeyRateLimit(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, initialRate.Usage5h, rate.Usage5h)
	require.Equal(t, initialRate.Usage1d, rate.Usage1d)
	quota, ok, err := normal.GetUserPlatformQuotaCache(ctx, 1, "openai")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, initialQuota.DailyUsageUSD, quota.DailyUsageUSD)
	require.Equal(t, initialQuota.Version, quota.Version)

	// Subscription entitlements retain their USD cache during a wallet cutover.
	subscription := &service.SubscriptionCacheData{Status: "active", ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, bypassed.SetSubscriptionCache(ctx, 1, 9, subscription))
	gotSubscription, err := bypassed.GetSubscriptionCache(ctx, 1, 9)
	require.NoError(t, err)
	require.Equal(t, subscription.Status, gotSubscription.Status)
}

func TestCurrencyCutoverBillingCacheBypassesQueuedAndFallbackWrites(t *testing.T) {
	ctx := context.Background()
	normal, bypassed := newCurrencyCutoverBillingCaches(t)
	require.NoError(t, normal.SetUserBalance(ctx, 1, 10))
	require.NoError(t, normal.SetAPIKeyRateLimit(ctx, 2, &service.APIKeyRateLimitCacheData{Usage5h: 1}))

	svc := service.NewBillingCacheService(bypassed, nil, nil, nil, nil, nil, &config.Config{}, nil)
	svc.QueueDeductBalance(1, 2)
	svc.QueueUpdateAPIKeyRateLimitUsage(2, 2)
	// Stop drains the worker queue, proving queued writes also reach the bypass.
	svc.Stop()

	balance, err := normal.GetUserBalance(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 10.0, balance)
	rate, err := normal.GetAPIKeyRateLimit(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, 1.0, rate.Usage5h)

	// A stopped queue forces QueueDeductBalance through its synchronous fallback.
	svc.QueueDeductBalance(1, 3)
	balance, err = normal.GetUserBalance(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 10.0, balance)
}
