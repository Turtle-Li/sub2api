package repository

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const imageStudioKeysFrom = ` FROM api_keys k
 LEFT JOIN groups g ON g.id = k.group_id AND g.deleted_at IS NULL
 WHERE k.user_id = $1 AND k.deleted_at IS NULL`

func (r *apiKeyRepository) ListImageStudioKeys(ctx context.Context, userID int64, params pagination.PaginationParams) ([]service.ImageStudioKey, *pagination.PaginationResult, error) {
	// No secret, IP history or live-concurrency projection belongs in this list.
	rows, err := r.sql.QueryContext(ctx, `SELECT k.id, k.name, k.status, k.group_id,
 COALESCE(g.name, ''), k.expires_at,
 (k.expires_at IS NOT NULL AND k.expires_at <= CURRENT_TIMESTAMP),
 (k.quota > 0 AND k.quota_used >= k.quota),
 COALESCE(g.status = 'active' AND g.platform IN ('gemini', 'openai') AND g.allow_batch_image_generation, false),
 EXISTS (SELECT 1 FROM batch_image_jobs j WHERE j.user_id = k.user_id
   AND j.api_key_id = k.id AND j.user_deleted_at IS NULL), COUNT(*) OVER ()`+
		imageStudioKeysFrom+` ORDER BY k.id ASC LIMIT $2 OFFSET $3`, userID, params.Limit(), params.Offset())
	if err != nil {
		return nil, nil, fmt.Errorf("list image studio keys: %w", err)
	}
	defer rows.Close()
	out := make([]service.ImageStudioKey, 0)
	var total int64
	for rows.Next() {
		var key service.ImageStudioKey
		if err := rows.Scan(&key.ID, &key.Name, &key.Status, &key.GroupID, &key.GroupName,
			&key.ExpiresAt, &key.Expired, &key.QuotaExhausted, &key.CanGenerate, &key.HasHistory, &total); err != nil {
			return nil, nil, fmt.Errorf("scan image studio key: %w", err)
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("read image studio keys: %w", err)
	}
	if len(out) == 0 && params.Offset() > 0 {
		if err := scanSingleRow(ctx, r.sql, `SELECT COUNT(*)`+imageStudioKeysFrom, []any{userID}, &total); err != nil {
			return nil, nil, fmt.Errorf("count image studio keys: %w", err)
		}
	}
	return out, paginationResultFromTotal(total, params), nil
}
