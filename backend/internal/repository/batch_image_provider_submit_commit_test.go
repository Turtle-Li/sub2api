package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBatchImageRepositoryProviderSubmitCommitErrorIsMarkedUncertain(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := &batchImageRepository{db: db, sql: db}
	commitErr := errors.New("commit acknowledgement lost")
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT status FROM batch_image_jobs`).
		WithArgs("imgbatch_commit_uncertain").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(service.BatchImageJobStatusUploading))
	mock.ExpectExec(`UPDATE batch_image_jobs`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO batch_image_events`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit().WillReturnError(commitErr)

	err = repo.UpdateBatchImageJobProviderSubmit(context.Background(), service.UpdateBatchImageJobProviderSubmitParams{
		BatchID:          "imgbatch_commit_uncertain",
		ProviderJobName:  "openai-images:imgbatch_commit_uncertain",
		ProviderInputRef: "batch/provider/openai/imgbatch_commit_uncertain/input/manifest.json",
	})
	require.ErrorIs(t, err, service.ErrBatchImageProviderSubmitCommitUncertain)
	require.Contains(t, err.Error(), commitErr.Error())
	require.NoError(t, mock.ExpectationsWereMet())
}
