package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBatchImageUpscaleWriteFenceHoldsIndexingRowUntilRelease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &batchImageRepository{db: db, sql: db}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT status, output_deleted_at
FROM batch_image_jobs
WHERE batch_id = $1
FOR UPDATE`)).
		WithArgs("imgbatch_fenced").
		WillReturnRows(sqlmock.NewRows([]string{"status", "output_deleted_at"}).
			AddRow(service.BatchImageJobStatusIndexing, nil))
	mock.ExpectRollback()

	permit, err := repo.BeginBatchImageUpscaleWrite(context.Background(), "imgbatch_fenced")
	require.NoError(t, err)
	require.NotNil(t, permit)
	require.NoError(t, permit.Release())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchImageUpscaleWriteFenceRejectsTerminalJob(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &batchImageRepository{db: db, sql: db}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`
SELECT status, output_deleted_at
FROM batch_image_jobs
WHERE batch_id = $1
FOR UPDATE`)).
		WithArgs("imgbatch_terminal").
		WillReturnRows(sqlmock.NewRows([]string{"status", "output_deleted_at"}).
			AddRow(service.BatchImageJobStatusCancelled, nil))
	mock.ExpectRollback()

	permit, err := repo.BeginBatchImageUpscaleWrite(context.Background(), "imgbatch_terminal")
	require.ErrorIs(t, err, service.ErrBatchImageIndexStateConflict)
	require.Nil(t, permit)
	require.NoError(t, mock.ExpectationsWereMet())
}
