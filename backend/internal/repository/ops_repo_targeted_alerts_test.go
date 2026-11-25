package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestOpsAlertOutboxRollsBackWhenQueueInsertFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO ops_alert_events`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectExec(`INSERT INTO ops_alert_deliveries`).WithArgs(int64(1), "feishu", "payment_feishu").WillReturnResult(sqlmock.NewResult(1, 1))
	failure := errors.New("fault while writing second recipient")
	mock.ExpectExec(`INSERT INTO ops_alert_deliveries`).WithArgs(int64(1), "email", "ops@example.invalid").WillReturnError(failure)
	mock.ExpectRollback()
	repo := &opsRepository{db: db}
	created, err := repo.CreateAlertEvent(context.Background(), &service.OpsAlertEvent{
		RuleID: 1, Severity: "P1", Status: service.OpsAlertStatusFiring, FiredAt: time.Now(),
		DeliveryPlan: []service.OpsAlertDeliveryTarget{{Channel: "feishu", Target: "payment_feishu"}, {Channel: "email", Target: "ops@example.invalid"}},
	})
	require.ErrorIs(t, err, failure)
	require.Nil(t, created)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpsAlertEscalationRollsBackWhenQueueInsertFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE ops_alert_events SET status = 'resolved', resolved_at = \$3 WHERE id = \$1 AND rule_id = \$2 AND status = 'firing'`).
		WithArgs(int64(1), int64(7), now).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`INSERT INTO ops_alert_events`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))
	failure := errors.New("fault while writing escalation delivery")
	mock.ExpectExec(`INSERT INTO ops_alert_deliveries`).WithArgs(int64(2), "feishu", "payment_feishu").WillReturnError(failure)
	mock.ExpectRollback()
	repo := &opsRepository{db: db}
	created, err := repo.CreateAlertEvent(context.Background(), &service.OpsAlertEvent{
		RuleID: 7, Status: service.OpsAlertStatusFiring, FiredAt: now, SupersedesEventID: 1,
		DeliveryPlan: []service.OpsAlertDeliveryTarget{{Channel: "feishu", Target: "payment_feishu"}},
	})
	require.ErrorIs(t, err, failure)
	require.Nil(t, created)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpsTargetedUpstreamQueryScansOneResultAndEscapesPattern(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	end := time.Now().UTC().Truncate(time.Minute)
	start := end.Add(-15 * time.Minute)
	repo := &opsRepository{db: db}
	f := service.OpsAccountUpstreamErrorFilter{Start: start, End: end, AccountIDs: []int64{248, 258}, UpstreamStatusCodes: []int{400, 402},
		ProviderCodes: []string{"insufficient_quota"}, ErrorKeywords: []string{`literal%_\path`}}
	mock.ExpectQuery(`(?s)WITH matched AS MATERIALIZED .*jsonb_array_elements.*hop\.status_code = ANY.*SELECT summary\.match_count`).
		WithArgs(start, end, nil, "", pq.Array(f.AccountIDs), pq.Array(f.UpstreamStatusCodes), pq.Array(f.ProviderCodes), pq.Array([]string{`%literal\%\_\\path%`})).
		WillReturnRows(sqlmock.NewRows([]string{"match_count", "account_id", "status_code", "provider_code", "platform", "matched_account_ids"}).
			AddRow(3, 258, 402, "insufficient_quota", "openai", "{248,258}"))
	match, err := repo.CountAccountUpstreamErrors(context.Background(), f)
	require.NoError(t, err)
	require.Equal(t, int64(3), match.Count)
	require.Equal(t, int64(258), match.LatestAccountID)
	require.Equal(t, 402, match.LatestUpstreamStatusCode)
	require.Equal(t, []int64{248, 258}, match.MatchedAccountIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}
