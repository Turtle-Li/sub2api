package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Reuse the dashboard's disjoint usage_logs token buckets and weighted ratio.
// The account join limits the scope to the actual pool, rather than every
// account on the platform. Peer bindings are deliberately not joined: one real
// request must contribute once even when its account serves multiple groups.
func (r *channelMonitorV3Repository) GroupRequestCache(ctx context.Context, groupIDs []int64, from, to time.Time) (map[int64][]service.ChannelMonitorV3RequestCache, error) {
	out := map[int64][]service.ChannelMonitorV3RequestCache{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `
		WITH pools AS (
			SELECT DISTINCT g.id AS group_id, g.platform, ag.account_id
			FROM groups g
			JOIN account_groups ag ON ag.group_id = g.id
			JOIN accounts a ON a.id = ag.account_id AND a.deleted_at IS NULL
			WHERE g.id = ANY($1) AND g.status = 'active'
			  AND NOT g.is_exclusive AND g.deleted_at IS NULL
		)
		SELECT p.group_id, date_trunc('hour', ul.created_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',
		       COUNT(*), SUM(ul.input_tokens),
		       SUM(ul.cache_creation_tokens), SUM(ul.cache_read_tokens)
		FROM pools p
		JOIN usage_logs ul ON ul.account_id = p.account_id
		JOIN groups source ON source.id = ul.group_id AND source.platform = p.platform
		WHERE ul.created_at >= $2 AND ul.created_at < $3
		  AND COALESCE(ul.request_type, 0) NOT IN (4, 6)
		  AND COALESCE(ul.image_count, 0) = 0 AND COALESCE(ul.video_count, 0) = 0
		  AND ul.input_tokens >= 0 AND ul.cache_creation_tokens >= 0 AND ul.cache_read_tokens >= 0
		  AND ul.input_tokens::numeric + ul.cache_creation_tokens + ul.cache_read_tokens > 0
		  AND source.status = 'active' AND source.deleted_at IS NULL
		GROUP BY p.group_id, date_trunc('hour', ul.created_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, pq.Array(groupIDs), from, to)
	if err != nil {
		return nil, fmt.Errorf("load status real-request cache: %w", err)
	}
	err = scanChannelMonitorV3Rows(rows, func(rows *sql.Rows) error {
		var id int64
		var fact service.ChannelMonitorV3RequestCache
		if err := rows.Scan(&id, &fact.HourStart, &fact.Requests, &fact.InputTokens, &fact.CacheCreationTokens, &fact.CacheReadTokens); err != nil {
			return err
		}
		out[id] = append(out[id], fact)
		return nil
	})
	return out, err
}
