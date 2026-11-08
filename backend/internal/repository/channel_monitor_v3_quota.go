package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Read only quota/model context. Credentials, names, account IDs and provider
// errors never enter the public status DTO or this snapshot.
func (r *channelMonitorV3Repository) GroupQuotaAccounts(ctx context.Context, groupIDs []int64) (map[int64][]service.Account, error) {
	out := map[int64][]service.Account{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `
		SELECT ag.group_id,a.platform,a.type,a.status,a.schedulable,a.auto_pause_on_expired,
		       a.expires_at,a.rate_limit_reset_at,a.session_window_end,
		       jsonb_build_object('session_window_utilization',a.extra->'session_window_utilization',
		           'passive_usage_7d_utilization',a.extra->'passive_usage_7d_utilization',
		           'passive_usage_7d_reset',a.extra->'passive_usage_7d_reset')
		FROM account_groups ag JOIN accounts a ON a.id=ag.account_id
		JOIN groups g ON g.id=ag.group_id AND g.platform=a.platform
		WHERE ag.group_id=ANY($1) AND a.deleted_at IS NULL AND a.platform='anthropic'
		  AND g.status='active' AND NOT g.is_exclusive AND g.deleted_at IS NULL`, pq.Array(groupIDs))
	if err != nil {
		return nil, fmt.Errorf("load status quota snapshot: %w", err)
	}
	err = scanChannelMonitorV3Rows(rows, func(rows *sql.Rows) error {
		var id int64
		var a service.Account
		var extra []byte
		if err := rows.Scan(&id, &a.Platform, &a.Type, &a.Status, &a.Schedulable, &a.AutoPauseOnExpired, &a.ExpiresAt, &a.RateLimitResetAt, &a.SessionWindowEnd, &extra); err != nil {
			return err
		}
		if err := json.Unmarshal(extra, &a.Extra); err != nil {
			return err
		}
		out[id] = append(out[id], a)
		return nil
	})
	return out, err
}
