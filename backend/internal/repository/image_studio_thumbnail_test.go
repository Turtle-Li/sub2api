//go:build unit

package repository

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestImageStudioThumbnailWriteFenceHoldsRowLockUntilPutCompletes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &batchImageRepository{db: db, sql: db}
	for _, allowed := range []bool{true, false} {
		mock.ExpectBegin()
		mock.ExpectQuery(`(?s)SELECT status IN \('indexing', 'completed'\).*output_deleted_at IS NULL AND user_deleted_at IS NULL AND thumbnail_disabled_at IS NULL.*output_expires_at > NOW\(\).*WHERE batch_id = \$1 FOR UPDATE`).WithArgs("batch").WillReturnRows(sqlmock.NewRows([]string{"allowed"}).AddRow(allowed))
		mock.ExpectRollback()
		permit, err := repo.BeginBatchImageThumbnailWrite(t.Context(), "batch")
		if allowed {
			require.NoError(t, err)
			require.NoError(t, permit.Release())
		} else {
			require.ErrorIs(t, err, service.ErrBatchImageOutputDeleted)
			require.Nil(t, permit)
		}
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImageStudioThumbnailReadsDurableDeletionAndRetryState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &batchImageRepository{db: db, sql: db}
	now := time.Now()
	mock.ExpectQuery(`SELECT thumbnail_disabled_at, thumbnail_retry_at`).WithArgs("batch").WillReturnRows(sqlmock.NewRows([]string{"disabled", "retry"}).AddRow(nil, now))
	at, err := repo.BatchImageThumbnailRetryAt(t.Context(), "batch")
	require.NoError(t, err)
	require.Equal(t, now, *at)
	mock.ExpectQuery(`SELECT thumbnail_disabled_at, thumbnail_retry_at`).WithArgs("batch").WillReturnRows(sqlmock.NewRows([]string{"disabled", "retry"}).AddRow(now, nil))
	_, err = repo.BatchImageThumbnailRetryAt(t.Context(), "batch")
	require.ErrorIs(t, err, service.ErrBatchImageOutputDeleted)
	mock.ExpectExec(`UPDATE batch_image_jobs SET thumbnail_disabled_at = COALESCE`).WithArgs("batch").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.DisableBatchImageThumbnails(t.Context(), "batch"))
	require.NoError(t, mock.ExpectationsWereMet())
	content, err := migrations.FS.ReadFile("271_image_studio_thumbnail_lifecycle.sql")
	require.NoError(t, err)
	noTx, err := validateMigrationExecutionMode("271_image_studio_thumbnail_lifecycle.sql", string(content))
	require.NoError(t, err)
	require.False(t, noTx)
}
