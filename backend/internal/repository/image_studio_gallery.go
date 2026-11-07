package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *batchImageRepository) ListBatchImageGalleryForOwner(ctx context.Context, userID, keyID int64, q service.BatchImageGalleryQuery) ([]service.BatchImageGalleryRecord, error) {
	// Page items, not batches: one huge batch cannot expand an initial page to
	// thousands of cards. The tuple cursor stays stable when newer jobs arrive.
	query := `SELECT j.id, j.batch_id, j.parent_batch_id, j.task_name, j.status,
j.provider, j.model, j.image_size, j.item_count, j.success_count, j.fail_count,
j.estimated_cost, j.hold_amount, j.actual_cost, j.created_at, j.settled_at,
j.output_deleted_at, j.last_error_code, j.last_error_message,
i.id, i.custom_id, i.status, i.prompt_preview, i.mime_type, i.file_extension,
i.image_count, i.error_code, i.error_message
FROM batch_image_jobs j JOIN batch_image_items i ON i.job_id = j.batch_id
WHERE j.user_id = $1 AND j.api_key_id = $2 AND j.user_deleted_at IS NULL
AND ($3::bigint = 0 OR j.id <= $3)
AND ($3::bigint = 0 OR (j.id, i.id) < ($3, $4))`
	args := []any{userID, keyID, q.BeforeJob, q.BeforeItem}
	if q.RootBatchID != "" {
		args = append(args, q.RootBatchID)
		query += fmt.Sprintf(` AND (j.batch_id = $%d OR j.parent_batch_id = $%d)`, len(args), len(args))
	}
	switch q.Status {
	case "pending":
		query += ` AND j.status NOT IN ('completed', 'failed', 'cancelled', 'output_deleted')`
	case "completed":
		query += ` AND i.image_count > 0 AND j.output_deleted_at IS NULL`
	case "failed":
		query += ` AND (i.status = 'failed' OR (j.status = 'failed' AND i.status IN ('pending', 'result_available')))`
	}
	if q.Search != "" {
		literal := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q.Search)
		args = append(args, "%"+literal+"%")
		query += fmt.Sprintf(` AND (i.prompt_preview ILIKE $%d OR j.model ILIKE $%d)`, len(args), len(args))
	}
	limit := q.Limit
	if limit <= 0 || limit > 51 {
		limit = 25
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY j.id DESC, i.id DESC LIMIT $%d`, len(args))
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read image studio gallery: %w", err)
	}
	defer rows.Close()
	result := make([]service.BatchImageGalleryRecord, 0)
	for rows.Next() {
		job, item := &service.BatchImageJob{}, &service.BatchImageItem{}
		if err := rows.Scan(&job.ID, &job.BatchID, &job.ParentBatchID, &job.TaskName, &job.Status,
			&job.Provider, &job.Model, &job.ImageSize, &job.ItemCount, &job.SuccessCount, &job.FailCount,
			&job.EstimatedCost, &job.HoldAmount, &job.ActualCost, &job.CreatedAt, &job.SettledAt,
			&job.OutputDeletedAt, &job.LastErrorCode, &job.LastErrorMessage,
			&item.ID, &item.CustomID, &item.Status, &item.PromptPreview, &item.MimeType, &item.FileExtension,
			&item.ImageCount, &item.ErrorCode, &item.ErrorMessage); err != nil {
			return nil, fmt.Errorf("scan image studio gallery: %w", err)
		}
		item.JobID = job.BatchID
		result = append(result, service.BatchImageGalleryRecord{Job: job, Item: item})
	}
	return result, rows.Err()
}
