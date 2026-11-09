package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// Return binding identity only; never load credentials or publish account IDs.
// Include temporarily unavailable accounts so cooldowns cannot change identity.
func (r *channelMonitorV3Repository) GroupProbePools(ctx context.Context, groupIDs []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT ag.group_id, ag.account_id
		FROM account_groups ag
		JOIN groups g ON g.id=ag.group_id
		JOIN accounts a ON a.id=ag.account_id
		WHERE ag.group_id=ANY($1) AND g.status='active'
		  AND NOT g.is_exclusive AND g.deleted_at IS NULL AND a.deleted_at IS NULL
		ORDER BY ag.group_id,ag.account_id`, pq.Array(groupIDs))
	if err != nil {
		return nil, fmt.Errorf("load shared check pool bindings: %w", err)
	}
	err = scanChannelMonitorV3Rows(rows, func(rows *sql.Rows) error {
		var groupID, accountID int64
		if err := rows.Scan(&groupID, &accountID); err != nil {
			return err
		}
		out[groupID] = append(out[groupID], accountID)
		return nil
	})
	return out, err
}
