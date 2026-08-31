//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestBatchImageRepository_ListProviderSubmittedBatchImageJobsForQueueRecoveryUsesStrictEligibility(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := &batchImageRepository{sql: db}
	mock.ExpectQuery(`(?s)SELECT .* FROM batch_image_jobs\s+WHERE status IN \('submitted', 'running', 'indexing', 'settling'\)\s+AND provider_job_name IS NOT NULL\s+AND BTRIM\(provider_job_name\) <> ''\s+AND id > \$1\s+ORDER BY id ASC\s+LIMIT \$2`).
		WithArgs(int64(41), 17).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	jobs, err := repo.ListProviderSubmittedBatchImageJobsForQueueRecovery(context.Background(), 41, 17)
	require.NoError(t, err)
	require.Empty(t, jobs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchImageRepository_ListProviderSubmittedBatchImageJobsForQueueRecoveryCapsOversizedPage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := &batchImageRepository{sql: db}
	mock.ExpectQuery(`(?s)SELECT .* FROM batch_image_jobs.*LIMIT \$2`).
		WithArgs(int64(0), maxBatchImageQueueRecoveryLimit).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	jobs, err := repo.ListProviderSubmittedBatchImageJobsForQueueRecovery(
		context.Background(), 0, maxBatchImageQueueRecoveryLimit+1,
	)
	require.NoError(t, err)
	require.Empty(t, jobs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchImageRepository_RevalidatesQueueRecoveryEligibility(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := &batchImageRepository{sql: db}
	mock.ExpectQuery(`(?s)SELECT EXISTS .*status IN \('submitted', 'running', 'indexing', 'settling'\).*provider_job_name IS NOT NULL.*BTRIM\(provider_job_name\) <> ''`).
		WithArgs("imgbatch_revalidate").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	eligible, err := repo.IsProviderSubmittedBatchImageJobQueueRecoveryEligible(
		context.Background(), "imgbatch_revalidate",
	)
	require.NoError(t, err)
	require.True(t, eligible)
	require.NoError(t, mock.ExpectationsWereMet())
}
