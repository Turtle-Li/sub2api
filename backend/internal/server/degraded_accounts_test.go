package server

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

func degradedAccountRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"account_id", "name", "requested_model", "sent_model", "response_model",
		"occurrences", "first_seen", "last_seen", "ttft_avg_ms",
	})
}

// capturedTime records the window bound the query computed internally so the
// test can assert the span rather than the wall clock.
type capturedTime struct{ at *time.Time }

func (c capturedTime) Match(v driver.Value) bool {
	moment, ok := v.(time.Time)
	if ok {
		*c.at = moment
	}
	return ok
}

func TestDegradedAccountsFiltersNamingVariants(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	var start, end time.Time
	mock.ExpectQuery("upstream_model_mismatch IS TRUE").
		WithArgs(capturedTime{&start}, capturedTime{&end}, domain.PlatformOpenAI, degradedAccountsScanLimit).
		WillReturnRows(degradedAccountRows().
			// A naming variant: persisted as a mismatch, must not be reported.
			AddRow(int64(7), "acct-variant", "gpt-6-astra", "gpt-6-astra", "gpt-6-astra-2026-03-01",
				int64(40), now, now, 120.0).
			// Real degradation.
			AddRow(int64(11), "acct-degraded", "gpt-6-astra", "gpt-6-astra", "gpt-5.6-luna",
				int64(14), now, now, 29400.0).
			// Historical grok row written before canonicalGrokBuildRuntimeModel.
			AddRow(int64(3), "acct-grok", "grok-4.6", "grok-4.6", "grok-4.6-build",
				int64(9), now, now, nil))

	health := newHealthService(db, nil, "", "", time.Second)
	degraded, err := health.DegradedAccounts(context.Background(), 30*time.Minute)
	require.NoError(t, err)

	require.Len(t, degraded, 1, "only the genuine downgrade survives the variant filter")
	require.Equal(t, int64(11), degraded[0].AccountID)
	require.Equal(t, "acct-degraded", degraded[0].AccountName)
	require.Equal(t, "gpt-5.6-luna", degraded[0].ResponseModel)
	require.Equal(t, int64(14), degraded[0].Count)
	require.NotNil(t, degraded[0].TTFTAvgMs)
	require.InDelta(t, 29400.0, *degraded[0].TTFTAvgMs, 0.001)
	require.Equal(t, 30*time.Minute, end.Sub(start))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDegradedAccountsScansNullTTFT(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	mock.ExpectQuery("upstream_model_mismatch IS TRUE").
		WillReturnRows(degradedAccountRows().
			AddRow(int64(11), "acct", "gpt-6-astra", "gpt-6-astra", "gpt-5.6-luna",
				int64(2), now, now, nil))

	health := newHealthService(db, nil, "", "", time.Second)
	degraded, err := health.DegradedAccounts(context.Background(), time.Hour)
	require.NoError(t, err)
	require.Len(t, degraded, 1)
	require.Nil(t, degraded[0].TTFTAvgMs, "a group where no row recorded TTFT stays null")
}

// The MATERIALIZED fence keeps the aggregate on the partial mismatch index. If
// it is dropped, PostgreSQL inlines the CTE and can flip to an accounts-driven
// plan that reads every request in the window. That regression is invisible in
// a functional test, so assert the SQL text carries it.
func TestDegradedAccountsQueryFencesThePlan(t *testing.T) {
	require.Contains(t, degradedAccountsQuery, "AS MATERIALIZED")
	require.Contains(t, degradedAccountsQuery, "ul.upstream_model_mismatch IS TRUE")
}

func TestDegradedAccountsClampsWindowAndReturnsEmptySlice(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	var start, end time.Time
	mock.ExpectQuery("upstream_model_mismatch IS TRUE").
		WithArgs(capturedTime{&start}, capturedTime{&end}, domain.PlatformOpenAI, degradedAccountsScanLimit).
		WillReturnRows(degradedAccountRows())

	health := newHealthService(db, nil, "", "", time.Second)
	// 99h is past the ceiling; the query layer re-clamps rather than trusting
	// its caller to have done so.
	degraded, err := health.DegradedAccounts(context.Background(), 99*time.Hour)
	require.NoError(t, err)
	require.NotNil(t, degraded, "an empty result must encode as [] not null")
	require.Empty(t, degraded)
	require.Equal(t, 6*time.Hour, end.Sub(start))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDegradedAccountsWithoutDatabaseFailsClosed(t *testing.T) {
	var health *HealthService
	degraded, err := health.DegradedAccounts(context.Background(), time.Minute)
	require.Error(t, err)
	require.Nil(t, degraded)

	health = newHealthService(nil, nil, "", "", time.Second)
	degraded, err = health.DegradedAccounts(context.Background(), time.Minute)
	require.Error(t, err)
	require.Nil(t, degraded)
}
