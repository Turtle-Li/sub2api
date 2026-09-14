package service

import (
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestPurchasedResetCardGrantWritesFrozenTierSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() {
		_ = client.Close()
		_ = db.Close()
	})

	now := time.Now().UTC()
	sourcePlanID := int64(9)
	mock.ExpectQuery(`(?s)INSERT INTO subscription_reset_grants.*card_family_key, source_tier_rank, source_plan_id, tier_snapshot_resolved.*VALUES.*TRUE.*RETURNING id`).
		WithArgs(int64(2), int64(1), int64(3), now.Add(time.Hour), "gpt", 2, sourcePlanID, now).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(91)))

	grantID, err := insertPurchasedResetCardGrant(context.Background(), client, 1, 2, 3, now.Add(time.Hour), now, &SubscriptionResetCardTierSnapshot{
		FamilyKey: "gpt", TierRank: 2, SourcePlanID: &sourcePlanID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(91), grantID)
	require.NoError(t, mock.ExpectationsWereMet())
}
