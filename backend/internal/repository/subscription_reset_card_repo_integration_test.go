//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionResetCardRepository_GrantConsumeAndOwnership(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewSubscriptionResetCardRepository(client)
	now := time.Now().UTC().Truncate(time.Microsecond)

	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("reset-card-%d@example.com", time.Now().UnixNano())})
	other := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("reset-card-other-%d@example.com", time.Now().UnixNano())})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             fmt.Sprintf("reset-card-group-%d", time.Now().UnixNano()),
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	sub := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:          user.ID,
		GroupID:         group.ID,
		Status:          service.SubscriptionStatusActive,
		ExpiresAt:       now.Add(24 * time.Hour),
		DailyUsageUSD:   3,
		WeeklyUsageUSD:  4,
		MonthlyUsageUSD: 5,
	})

	laterExpiry := now.Add(2 * time.Hour)
	earlierExpiry := now.Add(time.Hour)
	byGroup, err := repo.GrantToGroups(ctx, []int64{group.ID}, 2, laterExpiry, 0, now)
	require.NoError(t, err)
	require.Equal(t, int64(1), byGroup[group.ID])
	byGroup, err = repo.GrantToGroups(ctx, []int64{group.ID}, 1, earlierExpiry, 0, now)
	require.NoError(t, err)
	require.Equal(t, int64(1), byGroup[group.ID])

	summaries, err := repo.ListAvailable(ctx, []int64{sub.ID}, now)
	require.NoError(t, err)
	require.Equal(t, 3, summaries[sub.ID].AvailableCount)
	require.Equal(t, []service.SubscriptionResetCardBatch{
		{Remaining: 1, ExpiresAt: earlierExpiry},
		{Remaining: 2, ExpiresAt: laterExpiry},
	}, summaries[sub.ID].Batches)

	_, err = repo.ConsumeAndReset(ctx, other.ID, sub.ID, now, now)
	require.ErrorIs(t, err, service.ErrSubscriptionNotFound)
	summaries, err = repo.ListAvailable(ctx, []int64{sub.ID}, now)
	require.NoError(t, err)
	require.Equal(t, 3, summaries[sub.ID].AvailableCount, "ownership failure must not consume a card")

	groupID, err := repo.ConsumeAndReset(ctx, user.ID, sub.ID, now, now)
	require.NoError(t, err)
	require.Equal(t, group.ID, groupID)

	refreshed, err := client.UserSubscription.Get(ctx, sub.ID)
	require.NoError(t, err)
	require.Zero(t, refreshed.DailyUsageUsd)
	require.Zero(t, refreshed.WeeklyUsageUsd)
	require.Zero(t, refreshed.MonthlyUsageUsd)
	summaries, err = repo.ListAvailable(ctx, []int64{sub.ID}, now)
	require.NoError(t, err)
	require.Equal(t, 2, summaries[sub.ID].AvailableCount)

	var earlierUsed, laterUsed int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT used_count
		FROM subscription_reset_grants
		WHERE subscription_id = $1 AND expires_at = $2
	`, sub.ID, earlierExpiry).Scan(&earlierUsed))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT used_count
		FROM subscription_reset_grants
		WHERE subscription_id = $1 AND expires_at = $2
	`, sub.ID, laterExpiry).Scan(&laterUsed))
	require.Equal(t, 1, earlierUsed, "the earliest-expiring batch must be consumed first")
	require.Zero(t, laterUsed)

	expiredSummaries, err := repo.ListAvailable(ctx, []int64{sub.ID}, laterExpiry.Add(time.Microsecond))
	require.NoError(t, err)
	require.NotContains(t, expiredSummaries, sub.ID, "expired batches must not be exposed as available")
}

func TestSubscriptionResetCardRepository_GrantTargetsOnlySelectedSubscription(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewSubscriptionResetCardRepository(client)
	now := time.Now().UTC().Truncate(time.Microsecond)

	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("reset-card-direct-%d@example.com", time.Now().UnixNano())})
	other := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("reset-card-direct-other-%d@example.com", time.Now().UnixNano())})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             fmt.Sprintf("reset-card-direct-group-%d", time.Now().UnixNano()),
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	target := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:    user.ID,
		GroupID:   group.ID,
		Status:    service.SubscriptionStatusActive,
		ExpiresAt: now.Add(24 * time.Hour),
	})
	nonTarget := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:    other.ID,
		GroupID:   group.ID,
		Status:    service.SubscriptionStatusActive,
		ExpiresAt: now.Add(24 * time.Hour),
	})

	granted, err := repo.GrantToSubscriptions(ctx, []int64{target.ID}, 2, now.Add(time.Hour), 0, now)
	require.NoError(t, err)
	require.Equal(t, map[int64]int64{target.ID: 1}, granted)

	summaries, err := repo.ListAvailable(ctx, []int64{target.ID, nonTarget.ID}, now)
	require.NoError(t, err)
	require.Equal(t, 2, summaries[target.ID].AvailableCount)
	require.NotContains(t, summaries, nonTarget.ID)
}

func TestSubscriptionResetCardRepository_ConcurrentSingleCardUse(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewSubscriptionResetCardRepository(client)
	now := time.Now().UTC().Truncate(time.Microsecond)

	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("reset-card-race-%d@example.com", time.Now().UnixNano())})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             fmt.Sprintf("reset-card-race-group-%d", time.Now().UnixNano()),
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	sub := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:    user.ID,
		GroupID:   group.ID,
		Status:    service.SubscriptionStatusActive,
		ExpiresAt: now.Add(24 * time.Hour),
	})
	_, err := repo.GrantToGroups(ctx, []int64{group.ID}, 1, now.Add(time.Hour), 0, now)
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, consumeErr := repo.ConsumeAndReset(ctx, user.ID, sub.ID, now, now)
			errs <- consumeErr
		}()
	}
	wg.Wait()
	close(errs)

	var success, unavailable int
	for consumeErr := range errs {
		switch {
		case consumeErr == nil:
			success++
		case errors.Is(consumeErr, service.ErrResetCardUnavailable):
			unavailable++
		default:
			t.Fatalf("unexpected consume error: %v", consumeErr)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, unavailable)
}
