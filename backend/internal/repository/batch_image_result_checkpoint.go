package repository

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const batchImageCheckpointSoftLimitBytes = 1 << 30

func (r *batchImageRepository) ListBatchImageShanghaiAcknowledgements(ctx context.Context, limit int) ([]service.BatchImageShanghaiAcknowledgement, error) {
	rows, err := r.sql.QueryContext(ctx, `
SELECT j.batch_id, i.custom_id
FROM batch_image_jobs j JOIN batch_image_items i ON i.job_id = j.batch_id
WHERE j.provider_output_ref = 'studio-local-checkpoint:shanghai:v1'
  AND j.status IN ('completed', 'output_deleted') AND i.status = 'success'
ORDER BY j.id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pending []service.BatchImageShanghaiAcknowledgement
	for rows.Next() {
		var item service.BatchImageShanghaiAcknowledgement
		if err := rows.Scan(&item.BatchID, &item.CustomID); err != nil {
			return nil, err
		}
		pending = append(pending, item)
	}
	return pending, rows.Err()
}

func (r *batchImageRepository) ConfirmBatchImageShanghaiAcknowledgement(ctx context.Context, batchID string) error {
	_, err := r.sql.ExecContext(ctx, `
UPDATE batch_image_jobs SET provider_output_ref = $2
WHERE batch_id = $1 AND provider_output_ref = $3 AND status IN ('completed', 'output_deleted')`,
		batchID, service.BatchImageShanghaiAckedRef, service.BatchImageShanghaiSourceRef)
	return err
}

func clearBatchImageDeliveryRecovery(ctx context.Context, sqlq batchImageSQLExecutor, batchID string) error {
	if _, err := sqlq.ExecContext(ctx, `DELETE FROM batch_image_result_checkpoints WHERE job_id = $1`, batchID); err != nil {
		return err
	}
	_, err := sqlq.ExecContext(ctx, `DELETE FROM batch_image_delivery_retries WHERE job_id = $1`, batchID)
	return err
}

func (r *batchImageRepository) GetBatchImageCheckpoint(ctx context.Context, batchID, customID, stage string) (*service.BatchImageResultCheckpoint, error) {
	if r.spool != nil {
		dir, err := r.spool.directory(batchID, stage)
		if err != nil {
			return nil, err
		}
		if err := r.importBatchImageCheckpointFile(ctx, batchID, stage, filepath.Join(dir, batchImageCheckpointFilename(customID))); err != nil {
			return nil, err
		}
	}
	var checkpoint service.BatchImageResultCheckpoint
	err := r.sql.QueryRowContext(ctx, `
SELECT payload, mime_type, width, height
FROM batch_image_result_checkpoints
WHERE job_id = $1 AND stage = $2 AND custom_id = $3`, batchID, stage, customID).Scan(
		&checkpoint.Data, &checkpoint.MimeType, &checkpoint.Width, &checkpoint.Height)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &checkpoint, nil
}

func (r *batchImageRepository) ListBatchImageCheckpointIDs(ctx context.Context, batchID, stage string) (map[string]struct{}, error) {
	if r.spool != nil {
		files, err := r.spool.readyFiles(batchID, stage)
		if err != nil {
			return nil, err
		}
		for _, path := range files {
			if err := r.importBatchImageCheckpointFile(ctx, batchID, stage, path); err != nil {
				return nil, err
			}
		}
	}
	rows, err := r.sql.QueryContext(ctx, `
SELECT custom_id FROM batch_image_result_checkpoints WHERE job_id = $1 AND stage = $2`, batchID, stage)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = struct{}{}
	}
	return ids, rows.Err()
}

func (r *batchImageRepository) SaveBatchImageCheckpoint(ctx context.Context, batchID, customID, stage string, checkpoint service.BatchImageResultCheckpoint) (bool, error) {
	if r.spool == nil {
		return r.insertBatchImageCheckpoint(ctx, batchID, customID, stage, checkpoint)
	}
	path, wrote, spoolErr := r.spool.write(batchID, customID, stage, checkpoint)
	if path != "" && !wrote {
		_, staged, err := readBatchImageCheckpointFile(path, stage)
		if err != nil {
			return false, err
		}
		if staged != nil {
			defer clear(staged.Data)
			checkpoint = *staged
		}
	}
	created, dbErr := r.insertBatchImageCheckpoint(ctx, batchID, customID, stage, checkpoint)
	if dbErr == nil || errors.Is(dbErr, service.ErrBatchImageIndexStateConflict) {
		removeBatchImageCheckpointFile(path)
		if dbErr == nil && spoolErr != nil {
			r.reportBatchImageCheckpointAlert(ctx, batchID, stage, "IMAGE_CHECKPOINT_SPOOL_WRITE_FAILED")
		}
		return created, dbErr
	}
	if spoolErr == nil {
		return false, service.ErrBatchImageCheckpointImportPending
	}
	return false, errors.Join(dbErr, spoolErr)
}

func (r *batchImageRepository) importBatchImageCheckpointFile(ctx context.Context, batchID, stage, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	customID, checkpoint, err := readBatchImageCheckpointFile(path, stage)
	if err != nil || checkpoint == nil {
		return err
	}
	defer clear(checkpoint.Data)
	_, err = r.insertBatchImageCheckpoint(ctx, batchID, customID, stage, *checkpoint)
	if err == nil || errors.Is(err, service.ErrBatchImageIndexStateConflict) {
		// The fenced transaction either admitted the bytes or proved the stage has
		// advanced. Never reattach a late file to a completed/deleted job.
		removeBatchImageCheckpointFile(path)
		if err == nil {
			r.reportBatchImageCheckpointAlert(ctx, batchID, stage, "IMAGE_CHECKPOINT_SPOOL_RECOVERED")
		}
		return nil
	}
	return err
}

func (r *batchImageRepository) reportBatchImageCheckpointAlert(ctx context.Context, batchID, stage, code string) {
	if r.alerts == nil {
		return
	}
	reportCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := r.alerts.Notify(reportCtx, service.OperationalAlert{Component: "image_studio", Stage: stage, SubjectID: batchID, Code: code}); err != nil {
		slog.WarnContext(ctx, "batch_image.alert_enqueue_failed", "batch_id", batchID, "stage", stage, "code", code)
	}
}

func (r *batchImageRepository) insertBatchImageCheckpoint(ctx context.Context, batchID, customID, stage string, checkpoint service.BatchImageResultCheckpoint) (bool, error) {
	var eligible, inserted bool
	// Lock the same job row as publication, settlement and deletion. An old
	// lease holder cannot create a checkpoint after the job advanced or expired.
	err := r.sql.QueryRowContext(ctx, `
WITH eligible AS (
    SELECT j.batch_id FROM batch_image_jobs j
    WHERE j.batch_id = $1 AND j.output_deleted_at IS NULL AND j.user_deleted_at IS NULL
      AND (($3::varchar = 'provider_result' AND j.status IN ('submitted', 'running'))
           OR ($3::varchar = 'final_image' AND j.status = 'indexing'))
      AND EXISTS (SELECT 1 FROM batch_image_items WHERE job_id = $1 AND custom_id = $2)
    FOR UPDATE OF j
), inserted AS (
    INSERT INTO batch_image_result_checkpoints (job_id, custom_id, stage, payload, mime_type, width, height)
    SELECT batch_id, $2, $3, $4, $5, $6, $7 FROM eligible
    ON CONFLICT (job_id, stage, custom_id) DO NOTHING
    RETURNING 1
)
SELECT EXISTS (SELECT 1 FROM eligible), EXISTS (SELECT 1 FROM inserted)`,
		batchID, customID, stage, checkpoint.Data, checkpoint.MimeType, checkpoint.Width, checkpoint.Height).Scan(&eligible, &inserted)
	if err == nil && !eligible {
		return false, service.ErrBatchImageIndexStateConflict
	}
	return inserted, err
}

func (r *batchImageRepository) BatchImageCheckpointCapacityAvailable(ctx context.Context) (bool, error) {
	var available bool
	err := r.sql.QueryRowContext(ctx, `
SELECT COALESCE(SUM(octet_length(payload)), 0) < $1 FROM batch_image_result_checkpoints`,
		batchImageCheckpointSoftLimitBytes).Scan(&available)
	return available, err
}

func (r *batchImageRepository) BatchImageDeliveryRetryAfter(ctx context.Context, batchID string) (time.Duration, error) {
	var delayUS int64
	err := r.sql.QueryRowContext(ctx, `
SELECT GREATEST(0, EXTRACT(EPOCH FROM retry_at - NOW()) * 1000000)::bigint
FROM batch_image_delivery_retries WHERE job_id = $1`, batchID).Scan(&delayUS)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return time.Duration(delayUS) * time.Microsecond, err
}

func (r *batchImageRepository) RecordBatchImageDeliveryFailure(ctx context.Context, batchID, stage, code string) (time.Duration, error) {
	var delayUS int64
	// The wait is durable even if Redis loses the delayed member. Saturate the
	// exponential backoff, not the number of attempts; pending bytes never age out.
	err := r.sql.QueryRowContext(ctx, `
WITH active AS (
    SELECT batch_id FROM batch_image_jobs WHERE batch_id = $1
    AND status IN ('submitted', 'running', 'indexing', 'settling')
    AND output_deleted_at IS NULL AND user_deleted_at IS NULL
    FOR UPDATE
)
INSERT INTO batch_image_delivery_retries AS r (job_id, stage, code, failure_count, retry_at)
SELECT batch_id, $2, $3, 1, NOW() + INTERVAL '5 seconds' FROM active
ON CONFLICT (job_id) DO UPDATE SET
    stage = EXCLUDED.stage,
    code = EXCLUDED.code,
    failure_count = CASE WHEN r.stage = EXCLUDED.stage THEN LEAST(r.failure_count, 999999) + 1 ELSE 1 END,
    retry_at = NOW() + INTERVAL '5 seconds' *
        CASE WHEN r.stage = EXCLUDED.stage THEN POWER(3, LEAST(r.failure_count, 5)) ELSE 1 END
RETURNING (EXTRACT(EPOCH FROM retry_at - NOW()) * 1000000)::bigint`, batchID, stage, code).Scan(&delayUS)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, service.ErrBatchImageIndexStateConflict
	}
	return time.Duration(delayUS) * time.Microsecond, err
}

func (r *batchImageRepository) BatchImagePumpAttempt(ctx context.Context, batchID string) (int, error) {
	var attempt int
	err := r.sql.QueryRowContext(ctx, `SELECT attempt FROM batch_image_pump_attempts WHERE job_id = $1`, batchID).Scan(&attempt)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return attempt, err
}

func (r *batchImageRepository) AdvanceBatchImagePumpAttempt(ctx context.Context, batchID string, previous int) error {
	res, err := r.sql.ExecContext(ctx, `
WITH active AS (
  SELECT batch_id FROM batch_image_jobs
  WHERE batch_id = $1 AND status = 'indexing' AND provider = 'vertex'
    AND output_deleted_at IS NULL AND user_deleted_at IS NULL
  FOR UPDATE
)
INSERT INTO batch_image_pump_attempts AS p (job_id, attempt)
SELECT batch_id, $2::integer + 1 FROM active
WHERE $2::integer = 0 OR EXISTS (SELECT 1 FROM batch_image_pump_attempts WHERE job_id = $1)
ON CONFLICT (job_id) DO UPDATE SET attempt = EXCLUDED.attempt WHERE p.attempt = $2`, batchID, previous)
	if err != nil {
		return err
	}
	return requireBatchImageCheckpointUpdate(res)
}

func requireBatchImageCheckpointUpdate(res sql.Result) error {
	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		return service.ErrBatchImageIndexStateConflict
	}
	return err
}

var _ service.BatchImageResultCheckpointStore = (*batchImageRepository)(nil)
var _ service.BatchImageDeliveryRetryStore = (*batchImageRepository)(nil)
var _ service.BatchImagePumpAttemptStore = (*batchImageRepository)(nil)
