package service

import (
	"context"
	"math"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestResetCardPriceForMonthlyPlanRoundsToCents(t *testing.T) {
	tests := []struct {
		monthly string
		want    string
	}{
		{monthly: "120.00", want: "40.00"},
		{monthly: "550.00", want: "183.33"},
		{monthly: "10.01", want: "3.34"},
	}
	for _, tc := range tests {
		t.Run(tc.monthly, func(t *testing.T) {
			got, err := resetCardPriceForMonthlyPlan(decimal.RequireFromString(tc.monthly))
			require.NoError(t, err)
			require.Equal(t, tc.want, got.StringFixed(resetCardPurchasePriceScale))
		})
	}
}

func TestResetCardPriceForMonthlyPlanRejectsZero(t *testing.T) {
	_, err := resetCardPriceForMonthlyPlan(decimal.Zero)
	require.ErrorIs(t, err, ErrResetCardPriceInvalid)
}

func TestNormalizeResetCardExpectedPriceRequiresExactCents(t *testing.T) {
	value, err := normalizeResetCardExpectedPrice(183.33)
	require.NoError(t, err)
	require.True(t, value.Equal(decimal.RequireFromString("183.33")))

	_, err = normalizeResetCardExpectedPrice(183.333)
	require.ErrorIs(t, err, ErrResetCardPriceInvalid)
	_, err = normalizeResetCardExpectedPrice(math.NaN())
	require.ErrorIs(t, err, ErrResetCardPriceInvalid)
}

type resetCardPurchaseCacheStub struct {
	billingCacheWorkerStub
	invalidations atomic.Int64
}

func (s *resetCardPurchaseCacheStub) InvalidateUserBalance(context.Context, int64) error {
	s.invalidations.Add(1)
	return nil
}

func TestResetCardPurchaseBalanceInvalidationRunsOnlyAfterCommit(t *testing.T) {
	client := newPaymentConfigServiceTestClient(t)
	cache := &resetCardPurchaseCacheStub{}
	svc := &SubscriptionService{billingCacheService: &BillingCacheService{cache: cache}}

	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	svc.scheduleResetCardPurchaseBalanceInvalidation(dbent.NewTxContext(context.Background(), tx), 77)
	require.Zero(t, cache.invalidations.Load())
	require.NoError(t, tx.Commit())
	require.Eventually(t, func() bool {
		return cache.invalidations.Load() == 1
	}, time.Second, 10*time.Millisecond)
}

func TestResetCardPurchaseBalanceInvalidationSkipsRolledBackTransaction(t *testing.T) {
	client := newPaymentConfigServiceTestClient(t)
	cache := &resetCardPurchaseCacheStub{}
	svc := &SubscriptionService{billingCacheService: &BillingCacheService{cache: cache}}

	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	svc.scheduleResetCardPurchaseBalanceInvalidation(dbent.NewTxContext(context.Background(), tx), 77)
	require.NoError(t, tx.Rollback())
	require.Never(t, func() bool {
		return cache.invalidations.Load() != 0
	}, 100*time.Millisecond, 10*time.Millisecond)
}
