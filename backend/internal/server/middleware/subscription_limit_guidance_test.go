package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type subscriptionLimitResetCardRepoStub struct {
	available map[int64]service.SubscriptionResetCardSummary
	err       error
}

func (r subscriptionLimitResetCardRepoStub) GrantToGroups(context.Context, []int64, int, time.Time, int64, time.Time) (map[int64]int64, error) {
	panic("unexpected GrantToGroups call")
}

func (r subscriptionLimitResetCardRepoStub) GrantToSubscriptions(context.Context, []int64, int, time.Time, int64, time.Time) (map[int64]int64, error) {
	panic("unexpected GrantToSubscriptions call")
}

func (r subscriptionLimitResetCardRepoStub) ListAvailable(context.Context, []int64, time.Time) (map[int64]service.SubscriptionResetCardSummary, error) {
	return r.available, r.err
}

func (r subscriptionLimitResetCardRepoStub) ConsumeAndReset(context.Context, int64, int64, time.Time, time.Time) (int64, error) {
	panic("unexpected ConsumeAndReset call")
}

func TestSubscriptionUsageLimitMessage(t *testing.T) {
	t.Run("offers reset when uses remain", func(t *testing.T) {
		svc := service.NewSubscriptionService(nil, nil, nil, nil, nil)
		svc.SetResetCardRepository(subscriptionLimitResetCardRepoStub{
			available: map[int64]service.SubscriptionResetCardSummary{
				42: {AvailableCount: 2},
			},
		})

		message := subscriptionUsageLimitMessage(
			context.Background(),
			svc,
			&service.UserSubscription{ID: 42},
			service.ErrDailyLimitExceeded,
		)

		require.Equal(t, "订阅每日额度已用完。你当前还有 2 次可用重置次数，请前往「订阅」页面使用后再试。", message)
	})

	t.Run("offers upgrade when no uses remain", func(t *testing.T) {
		svc := service.NewSubscriptionService(nil, nil, nil, nil, nil)
		svc.SetResetCardRepository(subscriptionLimitResetCardRepoStub{
			available: map[int64]service.SubscriptionResetCardSummary{},
		})

		message := subscriptionUsageLimitMessage(
			context.Background(),
			svc,
			&service.UserSubscription{ID: 42},
			service.ErrWeeklyLimitExceeded,
		)

		require.Equal(t, "订阅每周额度已用完。当前没有可用重置次数，请升级套餐或购买额外额度后再试。", message)
	})

	t.Run("keeps neutral guidance when count lookup fails", func(t *testing.T) {
		svc := service.NewSubscriptionService(nil, nil, nil, nil, nil)
		svc.SetResetCardRepository(subscriptionLimitResetCardRepoStub{err: errors.New("database unavailable")})

		message := subscriptionUsageLimitMessage(
			context.Background(),
			svc,
			&service.UserSubscription{ID: 42},
			service.ErrMonthlyLimitExceeded,
		)

		require.Equal(t, "订阅每月额度已用完。请前往「订阅」页面查看可用重置次数，或升级套餐/购买额外额度后再试。", message)
	})
}
