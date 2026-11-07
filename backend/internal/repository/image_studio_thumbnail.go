package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *batchImageRepository) BatchImageThumbnailRetryAt(ctx context.Context, batchID string) (*time.Time, error) {
	var disabled, retry sql.NullTime
	err := r.sql.QueryRowContext(ctx, `SELECT thumbnail_disabled_at, thumbnail_retry_at
FROM batch_image_jobs WHERE batch_id = $1`, batchID).Scan(&disabled, &retry)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrBatchImageJobNotFound, nil)
	}
	if disabled.Valid {
		return nil, service.ErrBatchImageOutputDeleted
	}
	return batchImageNullTimePtr(retry), nil
}

func (r *batchImageRepository) BeginBatchImageThumbnailWrite(ctx context.Context, batchID string) (service.BatchImageUpscaleWritePermit, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT status IN ('indexing', 'completed')
AND output_deleted_at IS NULL AND user_deleted_at IS NULL AND thumbnail_disabled_at IS NULL
AND (output_expires_at IS NULL OR output_expires_at > NOW())
FROM batch_image_jobs WHERE batch_id = $1 FOR UPDATE`, batchID).Scan(&allowed)
	if err != nil || !allowed {
		_ = tx.Rollback()
		if err != nil {
			return nil, translatePersistenceError(err, service.ErrBatchImageJobNotFound, nil)
		}
		return nil, service.ErrBatchImageOutputDeleted
	}
	return &batchImageUpscaleWritePermit{tx: tx}, nil
}

func (r *batchImageRepository) DeferBatchImageThumbnailBackfill(ctx context.Context, batchID string, retryAt time.Time) error {
	_, err := r.sql.ExecContext(ctx, `UPDATE batch_image_jobs SET thumbnail_retry_at = $2
WHERE batch_id = $1 AND thumbnail_disabled_at IS NULL`, batchID, retryAt)
	return err
}

func (r *batchImageRepository) DisableBatchImageThumbnails(ctx context.Context, batchID string) error {
	// This UPDATE waits for all in-flight fenced Puts before cleanup deletes keys.
	_, err := r.sql.ExecContext(ctx, `UPDATE batch_image_jobs SET thumbnail_disabled_at = COALESCE(thumbnail_disabled_at, NOW())
WHERE batch_id = $1`, batchID)
	return err
}

var _ service.BatchImageThumbnailRepository = (*batchImageRepository)(nil)
