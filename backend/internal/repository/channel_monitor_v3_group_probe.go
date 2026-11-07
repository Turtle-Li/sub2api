package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *channelMonitorV3Repository) ClaimGroupProbe(ctx context.Context, target service.ChannelMonitorV3GroupProbeTarget, slot time.Time) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `INSERT INTO channel_monitor_v3_group_probes (group_id, model, slot_start)
 VALUES ($1,$2,$3) ON CONFLICT (group_id,slot_start) DO NOTHING RETURNING id`, target.GroupID, target.Model, slot).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (r *channelMonitorV3Repository) FinishGroupProbe(ctx context.Context, result service.ChannelMonitorV3GroupProbeResult) error {
	_, err := r.db.ExecContext(ctx, `UPDATE channel_monitor_v3_group_probes SET finished_at=$2, outcome=$3,
 category=$4, latency_ms=$5, account_id=NULLIF($6,0), input_tokens=$7, output_tokens=$8 WHERE id=$1 AND outcome='pending'`,
		result.ID, result.FinishedAt, result.Outcome, result.Category, result.LatencyMs, result.AccountID, result.InputTokens, result.OutputTokens)
	return err
}

func (r *channelMonitorV3Repository) GroupProbes(ctx context.Context, groups []int64, from, to time.Time) ([]service.ChannelMonitorV3GroupProbeResult, error) {
	out := []service.ChannelMonitorV3GroupProbeResult{}
	if len(groups) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,group_id,model,slot_start,finished_at,outcome,category,latency_ms,
 COALESCE(account_id,0),input_tokens,output_tokens FROM channel_monitor_v3_group_probes
 WHERE group_id=ANY($1::bigint[]) AND slot_start >= $2 AND slot_start < $3 ORDER BY slot_start,id`, pq.Array(groups), from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p service.ChannelMonitorV3GroupProbeResult
		if err := rows.Scan(&p.ID, &p.GroupID, &p.Model, &p.Slot, &p.FinishedAt, &p.Outcome, &p.Category, &p.LatencyMs, &p.AccountID, &p.InputTokens, &p.OutputTokens); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *channelMonitorV3Repository) PruneGroupProbes(ctx context.Context, before time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM channel_monitor_v3_group_probes WHERE slot_start < $1`, before)
	return err
}
