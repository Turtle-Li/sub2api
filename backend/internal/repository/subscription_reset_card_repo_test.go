package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const (
	grantResetCardsPattern                    = `(?s)INSERT INTO subscription_reset_grants.*source_plan_id, tier_snapshot_resolved.*SELECT.*tier\.family_key, tier\.tier_rank, NULL, TRUE.*FROM user_subscriptions us.*LEFT JOIN subscription_reset_card_tiers tier.*RETURNING group_id`
	grantResetCardsToSubscriptionsPattern     = `(?s)INSERT INTO subscription_reset_grants.*source_plan_id, tier_snapshot_resolved.*SELECT.*tier\.family_key, tier\.tier_rank, NULL, TRUE.*FROM user_subscriptions us.*LEFT JOIN subscription_reset_card_tiers tier.*WHERE us.id = ANY.*RETURNING subscription_id`
	lockTierSnapshotGroupsPattern             = `(?s)SELECT id.*FROM groups.*WHERE id = ANY.*ORDER BY id ASC.*FOR SHARE`
	lockTierSnapshotSubscriptionGroupsPattern = `(?s)SELECT g.id.*FROM groups AS g.*JOIN user_subscriptions AS us.*WHERE us.id = ANY.*ORDER BY g.id ASC.*FOR SHARE OF g`
	lockTierPoliciesForGroupsPattern          = `(?s)SELECT group_id.*FROM subscription_reset_card_tiers.*WHERE group_id = ANY.*FOR SHARE`
	lockTierPoliciesForSubscriptionsPattern   = `(?s)SELECT tier.group_id.*FROM user_subscriptions us.*JOIN subscription_reset_card_tiers tier.*WHERE us.id = ANY.*FOR SHARE OF tier`
	listResetCardsPattern                     = `(?s)WITH target AS.*SELECT target.subscription_id, rg.expires_at, SUM.*FROM target.*subscription_reset_grants rg.*ORDER BY target.subscription_id ASC, rg.expires_at ASC`
	lockSubscriptionPattern                   = `(?s)SELECT us.group_id, us.status, us.expires_at, tier.family_key, tier.tier_rank.*FROM user_subscriptions us.*WHERE us.id = \$1 AND us.user_id = \$2.*FOR UPDATE OF us`
	lockResetCardPattern                      = `(?s)WITH target AS.*SELECT rg.id.*FROM subscription_reset_grants rg.*ORDER BY rg.expires_at ASC, rg.source_tier_rank ASC NULLS FIRST, rg.id ASC.*LIMIT 1.*FOR UPDATE`
	lowerResetCardTierPattern                 = `(?s)SELECT EXISTS.*FROM subscription_reset_grants rg.*rg.card_family_key = target.family_key.*rg.source_tier_rank < target.tier_rank`
	consumeResetCardPattern                   = `(?s)UPDATE subscription_reset_grants.*SET used_count = used_count \+ 1`
	resetSubscriptionPattern                  = `(?s)UPDATE user_subscriptions.*SET daily_usage_usd = 0.*monthly_usage_usd = 0`
)

func newResetCardSQLMock(t *testing.T) (service.SubscriptionResetCardRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() {
		_ = client.Close()
	})
	return NewSubscriptionResetCardRepository(client), mock
}

func TestSubscriptionResetCardRepository_GrantCountsRecipientsByGroup(t *testing.T) {
	repo, mock := newResetCardSQLMock(t)
	now := time.Now().UTC()
	expiresAt := now.Add(24 * time.Hour)

	mock.ExpectBegin()
	mock.ExpectQuery(lockTierSnapshotGroupsPattern).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(2)).AddRow(int64(5)))
	mock.ExpectQuery(lockTierPoliciesForGroupsPattern).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"group_id"}))
	mock.ExpectQuery(grantResetCardsPattern).
		WithArgs(sqlmock.AnyArg(), 3, expiresAt, int64(99), now).
		WillReturnRows(sqlmock.NewRows([]string{"group_id"}).
			AddRow(int64(2)).
			AddRow(int64(5)).
			AddRow(int64(5)))
	mock.ExpectCommit()

	result, err := repo.GrantToGroups(context.Background(), []int64{2, 5}, 3, expiresAt, 99, now)

	require.NoError(t, err)
	require.Equal(t, map[int64]int64{2: 1, 5: 2}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSubscriptionResetCardRepository_GrantTargetsSpecificSubscriptions(t *testing.T) {
	repo, mock := newResetCardSQLMock(t)
	now := time.Now().UTC()
	expiresAt := now.Add(24 * time.Hour)

	mock.ExpectBegin()
	mock.ExpectQuery(lockTierSnapshotSubscriptionGroupsPattern).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(2)))
	mock.ExpectQuery(lockTierPoliciesForSubscriptionsPattern).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"group_id"}))
	mock.ExpectQuery(grantResetCardsToSubscriptionsPattern).
		WithArgs(sqlmock.AnyArg(), 2, expiresAt, int64(99), now).
		WillReturnRows(sqlmock.NewRows([]string{"subscription_id"}).AddRow(int64(7)))
	mock.ExpectCommit()

	result, err := repo.GrantToSubscriptions(context.Background(), []int64{7}, 2, expiresAt, 99, now)

	require.NoError(t, err)
	require.Equal(t, map[int64]int64{7: 1}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSubscriptionResetCardRepository_ListAvailableAggregatesOrderedBatches(t *testing.T) {
	repo, mock := newResetCardSQLMock(t)
	now := time.Now().UTC()
	firstExpiry := now.Add(time.Hour)
	secondExpiry := now.Add(2 * time.Hour)

	mock.ExpectQuery(listResetCardsPattern).
		WithArgs(sqlmock.AnyArg(), now).
		WillReturnRows(sqlmock.NewRows([]string{"subscription_id", "expires_at", "remaining"}).
			AddRow(int64(7), firstExpiry, int64(2)).
			AddRow(int64(7), secondExpiry, int64(3)).
			AddRow(int64(8), secondExpiry, int64(1)))

	result, err := repo.ListAvailable(context.Background(), []int64{7, 8}, now)

	require.NoError(t, err)
	require.Equal(t, 5, result[7].AvailableCount)
	require.Equal(t, []service.SubscriptionResetCardBatch{
		{Remaining: 2, ExpiresAt: firstExpiry},
		{Remaining: 3, ExpiresAt: secondExpiry},
	}, result[7].Batches)
	require.Equal(t, 1, result[8].AvailableCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSubscriptionResetCardRepository_ConsumeRejectsWrongOwnerWithoutUsingCard(t *testing.T) {
	repo, mock := newResetCardSQLMock(t)
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery(lockSubscriptionPattern).
		WithArgs(int64(7), int64(10)).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "status", "expires_at", "family_key", "tier_rank"}))
	mock.ExpectRollback()

	_, err := repo.ConsumeAndReset(context.Background(), 10, 7, now, now)

	require.ErrorIs(t, err, service.ErrSubscriptionNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSubscriptionResetCardRepository_ConsumeRejectsInactiveSubscriptionWithoutUsingCard(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		status    string
		expiresAt func(time.Time) time.Time
		wantErr   error
	}{
		{
			name:      "expired by time",
			status:    service.SubscriptionStatusActive,
			expiresAt: func(now time.Time) time.Time { return now },
			wantErr:   service.ErrSubscriptionExpired,
		},
		{
			name:      "suspended",
			status:    service.SubscriptionStatusSuspended,
			expiresAt: func(now time.Time) time.Time { return now.Add(time.Hour) },
			wantErr:   service.ErrSubscriptionSuspended,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo, mock := newResetCardSQLMock(t)
			now := time.Now().UTC()

			mock.ExpectBegin()
			mock.ExpectQuery(lockSubscriptionPattern).
				WithArgs(int64(7), int64(10)).
				WillReturnRows(sqlmock.NewRows([]string{"group_id", "status", "expires_at", "family_key", "tier_rank"}).
					AddRow(int64(20), testCase.status, testCase.expiresAt(now), nil, nil))
			mock.ExpectRollback()

			_, err := repo.ConsumeAndReset(context.Background(), 10, 7, now, now)

			require.ErrorIs(t, err, testCase.wantErr)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestSubscriptionResetCardRepository_ConsumeRejectsWhenCountIsExhausted(t *testing.T) {
	repo, mock := newResetCardSQLMock(t)
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery(lockSubscriptionPattern).
		WithArgs(int64(7), int64(10)).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "status", "expires_at", "family_key", "tier_rank"}).
			AddRow(int64(20), service.SubscriptionStatusActive, now.Add(time.Hour), nil, nil))
	mock.ExpectQuery(lockResetCardPattern).
		WithArgs(int64(7), int64(10), nil, nil, now).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()

	_, err := repo.ConsumeAndReset(context.Background(), 10, 7, now, now)

	require.ErrorIs(t, err, service.ErrResetCardUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSubscriptionResetCardRepository_ConsumeAndResetCommitsTogether(t *testing.T) {
	repo, mock := newResetCardSQLMock(t)
	now := time.Now().UTC()
	windowStart := now.Truncate(24 * time.Hour)

	mock.ExpectBegin()
	mock.ExpectQuery(lockSubscriptionPattern).
		WithArgs(int64(7), int64(10)).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "status", "expires_at", "family_key", "tier_rank"}).
			AddRow(int64(20), service.SubscriptionStatusActive, now.Add(time.Hour), nil, nil))
	mock.ExpectQuery(lockResetCardPattern).
		WithArgs(int64(7), int64(10), nil, nil, now).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(55)))
	mock.ExpectExec(consumeResetCardPattern).
		WithArgs(int64(55), now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(resetSubscriptionPattern).
		WithArgs(int64(7), windowStart, now, int64(10)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	groupID, err := repo.ConsumeAndReset(context.Background(), 10, 7, now, windowStart)

	require.NoError(t, err)
	require.Equal(t, int64(20), groupID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSubscriptionResetCardRepository_ConsumeRollbackPreservesCardOnResetFailure(t *testing.T) {
	repo, mock := newResetCardSQLMock(t)
	now := time.Now().UTC()
	dbErr := errors.New("reset failed")

	mock.ExpectBegin()
	mock.ExpectQuery(lockSubscriptionPattern).
		WithArgs(int64(7), int64(10)).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "status", "expires_at", "family_key", "tier_rank"}).
			AddRow(int64(20), service.SubscriptionStatusActive, now.Add(time.Hour), nil, nil))
	mock.ExpectQuery(lockResetCardPattern).
		WithArgs(int64(7), int64(10), nil, nil, now).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(55)))
	mock.ExpectExec(consumeResetCardPattern).
		WithArgs(int64(55), now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(resetSubscriptionPattern).
		WithArgs(int64(7), now, now, int64(10)).
		WillReturnError(dbErr)
	mock.ExpectRollback()

	_, err := repo.ConsumeAndReset(context.Background(), 10, 7, now, now)

	require.ErrorIs(t, err, dbErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSubscriptionResetCardRepository_ConsumeAllowsHigherTierCardForLowerTarget(t *testing.T) {
	repo, mock := newResetCardSQLMock(t)
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery(lockSubscriptionPattern).
		WithArgs(int64(7), int64(10)).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "status", "expires_at", "family_key", "tier_rank"}).
			AddRow(int64(20), service.SubscriptionStatusActive, now.Add(time.Hour), "gpt", int64(1)))
	mock.ExpectQuery(lockResetCardPattern).
		WithArgs(int64(7), int64(10), "gpt", int64(1), now).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(55)))
	mock.ExpectExec(consumeResetCardPattern).
		WithArgs(int64(55), now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(resetSubscriptionPattern).
		WithArgs(int64(7), now, now, int64(10)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	_, err := repo.ConsumeAndReset(context.Background(), 10, 7, now, now)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Contains(t, resetCardTierEligibilityPredicateSQL, "rg.source_tier_rank >= target.tier_rank")
}

func TestSubscriptionResetCardRepository_ConsumeRejectsLowerSameFamilyCardWithTierError(t *testing.T) {
	repo, mock := newResetCardSQLMock(t)
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery(lockSubscriptionPattern).
		WithArgs(int64(7), int64(10)).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "status", "expires_at", "family_key", "tier_rank"}).
			AddRow(int64(20), service.SubscriptionStatusActive, now.Add(time.Hour), "gpt", int64(2)))
	mock.ExpectQuery(lockResetCardPattern).
		WithArgs(int64(7), int64(10), "gpt", int64(2), now).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(lowerResetCardTierPattern).
		WithArgs(int64(10), "gpt", int64(2), now).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	_, err := repo.ConsumeAndReset(context.Background(), 10, 7, now, now)
	require.ErrorIs(t, err, service.ErrResetCardTierInsufficient)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSubscriptionResetCardRepository_ConsumeCrossFamilyCardReturnsUnavailable(t *testing.T) {
	repo, mock := newResetCardSQLMock(t)
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery(lockSubscriptionPattern).
		WithArgs(int64(7), int64(10)).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "status", "expires_at", "family_key", "tier_rank"}).
			AddRow(int64(20), service.SubscriptionStatusActive, now.Add(time.Hour), "gpt", int64(1)))
	mock.ExpectQuery(lockResetCardPattern).
		WithArgs(int64(7), int64(10), "gpt", int64(1), now).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(lowerResetCardTierPattern).
		WithArgs(int64(10), "gpt", int64(1), now).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	_, err := repo.ConsumeAndReset(context.Background(), 10, 7, now, now)
	require.ErrorIs(t, err, service.ErrResetCardUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSubscriptionResetCardRepository_TierPredicateKeepsLegacyExactOnly(t *testing.T) {
	require.Contains(t, resetCardTierEligibilityPredicateSQL, "rg.card_family_key IS NULL")
	require.Contains(t, resetCardTierEligibilityPredicateSQL, "rg.source_tier_rank IS NULL")
	require.NotContains(t, resetCardTierEligibilityPredicateSQL, "rg.source_plan_id IS NULL")
	require.Contains(t, resetCardTierEligibilityPredicateSQL, "rg.subscription_id = target.subscription_id")
	require.NotContains(t, resetCardTierEligibilityPredicateSQL, "JOIN user_subscriptions")
}

func TestSubscriptionResetCardRepository_TierPredicateKeepsUntypedScheduleCardsExactOnly(t *testing.T) {
	// Monthly issuance retains source_plan_id for auditability. Without an
	// explicit family/rank policy it must still use the owning subscription's
	// exact-only card path.
	require.Contains(t, resetCardTierEligibilityPredicateSQL, "rg.card_family_key IS NULL")
	require.Contains(t, resetCardTierEligibilityPredicateSQL, "rg.source_tier_rank IS NULL")
	require.Contains(t, resetCardTierEligibilityPredicateSQL, "rg.subscription_id = target.subscription_id")
	require.NotContains(t, resetCardTierEligibilityPredicateSQL, "AND rg.source_plan_id IS NULL")
}
