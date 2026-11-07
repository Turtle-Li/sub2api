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

func TestImageStudioGalleryRepositoryScopesMetadataPage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	columns := []string{"id", "batch_id", "parent_batch_id", "task_name", "status", "provider", "model", "image_size", "item_count", "success_count", "fail_count", "estimated_cost", "hold_amount", "actual_cost", "created_at", "settled_at", "output_deleted_at", "last_error_code", "last_error_message", "item_id", "custom_id", "item_status", "prompt_preview", "mime_type", "file_extension", "image_count", "error_code", "error_message"}
	created := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	rows := sqlmock.NewRows(columns).AddRow(8, "owned", "root", "", "completed", "vertex", "gemini-image", "1K", 1, 1, 0, .1, .12, .1, created, nil, nil, nil, nil, 20, "one", "success", "cat", "image/png", "png", 1, nil, nil)
	mock.ExpectQuery(`(?s)SELECT j.id, .*FROM batch_image_jobs j JOIN batch_image_items i ON i.job_id = j.batch_id.*j.user_id = \$1 AND j.api_key_id = \$2 AND j.user_deleted_at IS NULL.*\(j.id, i.id\) < \(\$3, \$4\).*j.batch_id = \$5 OR j.parent_batch_id = \$5.*i.image_count > 0 AND j.output_deleted_at IS NULL.*i.prompt_preview ILIKE \$6 OR j.model ILIKE \$6.*ORDER BY j.id DESC, i.id DESC LIMIT \$7`).
		WithArgs(int64(10), int64(20), int64(9), int64(30), "root", "%cat%", 25).WillReturnRows(rows)
	repo := &batchImageRepository{sql: db}
	got, err := repo.ListBatchImageGalleryForOwner(t.Context(), 10, 20, service.BatchImageGalleryQuery{RootBatchID: "root", Limit: 25, BeforeJob: 9, BeforeItem: 30, Status: "completed", Search: "cat"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "owned", got[0].Job.BatchID)
	require.Equal(t, created, got[0].Job.CreatedAt)
	require.Equal(t, "owned", got[0].Item.JobID)
	require.Equal(t, "one", got[0].Item.CustomID)
	require.Nil(t, got[0].Job.ProviderJobName)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImageStudioGalleryPendingItemsPollBindsLoadedIDs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(`(?s)FROM batch_image_items WHERE job_id = \$1 AND custom_id IN \(\$2,\$3\) AND status = \$4 ORDER BY id ASC LIMIT \$5 OFFSET \$6`).
		WithArgs("large-batch", "item-999", "item-1000", "pending", 100, 0).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, err = (&batchImageRepository{sql: db}).ListBatchImageItems(t.Context(), "large-batch", service.BatchImageItemFilter{CustomIDs: []string{"item-999", "item-1000"}, Status: "pending", Limit: 100})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImageStudioGallerySearchTreatsWildcardsLiterally(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(`(?s)FROM batch_image_jobs j.*i.prompt_preview ILIKE \$5 OR j.model ILIKE \$5.*LIMIT \$6`).
		WithArgs(int64(1), int64(2), int64(0), int64(0), `%cat\_\%%`, 25).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, err = (&batchImageRepository{sql: db}).ListBatchImageGalleryForOwner(t.Context(), 1, 2, service.BatchImageGalleryQuery{Search: "cat_%"})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImageStudioGalleryIndexesUseConcurrentMigrationMode(t *testing.T) {
	const name = "270_image_studio_gallery_indexes_notx.sql"
	content, err := migrations.FS.ReadFile(name)
	require.NoError(t, err)
	nonTransactional, err := validateMigrationExecutionMode(name, string(content))
	require.NoError(t, err)
	require.True(t, nonTransactional)
}
