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
	grantResetCardsPattern                = `(?s)INSERT INTO subscription_reset_grants.*FROM user_subscriptions us.*RETURNING group_id`
	grantResetCardsToSubscriptionsPattern = `(?s)INSERT INTO subscription_reset_grants.*FROM user_subscriptions us.*WHERE us.id = ANY.*RETURNING subscription_id`
	listResetCardsPattern                 = `(?s)SELECT rg.subscription_id, rg.expires_at, SUM.*FROM subscription_reset_grants rg.*ORDER BY rg.subscription_id ASC, rg.expires_at ASC`
	lockSubscriptionPattern               = `(?s)SELECT group_id, status, expires_at.*FROM user_subscriptions.*WHERE id = \$1 AND user_id = \$2.*FOR UPDATE`
	lockResetCardPattern                  = `(?s)SELECT id.*FROM subscription_reset_grants.*ORDER BY expires_at ASC, id ASC.*LIMIT 1.*FOR UPDATE`
	consumeResetCardPattern               = `(?s)UPDATE subscription_reset_grants.*SET used_count = used_count \+ 1`
	resetSubscriptionPattern              = `(?s)UPDATE user_subscriptions.*SET daily_usage_usd = 0.*monthly_usage_usd = 0`
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

	mock.ExpectQuery(grantResetCardsPattern).
		WithArgs(sqlmock.AnyArg(), 3, expiresAt, int64(99), now).
		WillReturnRows(sqlmock.NewRows([]string{"group_id"}).
			AddRow(int64(2)).
			AddRow(int64(5)).
			AddRow(int64(5)))

	result, err := repo.GrantToGroups(context.Background(), []int64{2, 5}, 3, expiresAt, 99, now)

	require.NoError(t, err)
	require.Equal(t, map[int64]int64{2: 1, 5: 2}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSubscriptionResetCardRepository_GrantTargetsSpecificSubscriptions(t *testing.T) {
	repo, mock := newResetCardSQLMock(t)
	now := time.Now().UTC()
	expiresAt := now.Add(24 * time.Hour)

	mock.ExpectQuery(grantResetCardsToSubscriptionsPattern).
		WithArgs(sqlmock.AnyArg(), 2, expiresAt, int64(99), now).
		WillReturnRows(sqlmock.NewRows([]string{"subscription_id"}).AddRow(int64(7)))

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
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "status", "expires_at"}))
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
				WillReturnRows(sqlmock.NewRows([]string{"group_id", "status", "expires_at"}).
					AddRow(int64(20), testCase.status, testCase.expiresAt(now)))
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
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "status", "expires_at"}).
			AddRow(int64(20), service.SubscriptionStatusActive, now.Add(time.Hour)))
	mock.ExpectQuery(lockResetCardPattern).
		WithArgs(int64(7), int64(10), now).
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
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "status", "expires_at"}).
			AddRow(int64(20), service.SubscriptionStatusActive, now.Add(time.Hour)))
	mock.ExpectQuery(lockResetCardPattern).
		WithArgs(int64(7), int64(10), now).
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
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "status", "expires_at"}).
			AddRow(int64(20), service.SubscriptionStatusActive, now.Add(time.Hour)))
	mock.ExpectQuery(lockResetCardPattern).
		WithArgs(int64(7), int64(10), now).
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
