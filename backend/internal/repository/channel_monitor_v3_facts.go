package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const channelMonitorV3SlotSQL = `date_bin($4::interval, %s.bucket_start, ` + channelMonitorV2DateBinOrigin + `)`

func (r *channelMonitorV3Repository) SlotFacts(ctx context.Context, groupIDs []int64, from, to time.Time, interval time.Duration, withLatency bool) (*service.ChannelMonitorV3Facts, error) {
	facts := &service.ChannelMonitorV3Facts{}
	if len(groupIDs) == 0 {
		return facts, nil
	}
	args := []any{pq.Array(groupIDs), from, to, fmt.Sprintf("%d seconds", int(interval.Seconds()))}
	rows, err := r.db.QueryContext(ctx, `
		SELECT m.group_id, m.model, `+fmt.Sprintf(channelMonitorV3SlotSQL, "m")+`, SUM(m.success_requests), SUM(m.error_requests)
		FROM channel_monitor_v2_metrics_1m m
		WHERE m.group_id = ANY($1) AND m.bucket_start >= $2 AND m.bucket_start < $3
		GROUP BY 1, 2, 3`, args...)
	if err != nil {
		return nil, fmt.Errorf("load channel monitor v3 metrics: %w", err)
	}
	if err := scanChannelMonitorV3Rows(rows, func(rows *sql.Rows) error {
		var f service.ChannelMonitorV3Fact
		if err := rows.Scan(&f.GroupID, &f.Model, &f.Slot, &f.Success, &f.Errors); err != nil {
			return err
		}
		facts.Metrics = append(facts.Metrics, f)
		return nil
	}); err != nil {
		return nil, err
	}
	rows, err = r.db.QueryContext(ctx, `
		SELECT e.group_id, e.model, `+fmt.Sprintf(channelMonitorV3SlotSQL, "e")+`, e.error_category, SUM(e.error_requests)
		FROM channel_monitor_v2_error_metrics_1m e
		WHERE e.group_id = ANY($1) AND e.bucket_start >= $2 AND e.bucket_start < $3 AND e.taxonomy_version = $5
		GROUP BY 1, 2, 3, 4`, append(args, service.ChannelMonitorV2TaxonomyVersion)...)
	if err != nil {
		return nil, fmt.Errorf("load channel monitor v3 errors: %w", err)
	}
	if err := scanChannelMonitorV3Rows(rows, func(rows *sql.Rows) error {
		var f service.ChannelMonitorV3ErrorFact
		if err := rows.Scan(&f.GroupID, &f.Model, &f.Slot, &f.Category, &f.Errors); err != nil {
			return err
		}
		facts.Errors = append(facts.Errors, f)
		return nil
	}); err != nil {
		return nil, err
	}
	if !withLatency {
		return facts, nil
	}
	// user_id 0 rows hold every user's samples.
	rows, err = r.db.QueryContext(ctx, `
		SELECT h.group_id, h.model, `+fmt.Sprintf(channelMonitorV3SlotSQL, "h")+`, h.upper_bound_ms, SUM(h.sample_count)
		FROM channel_monitor_v2_latency_histograms_1m h
		WHERE h.group_id = ANY($1) AND h.bucket_start >= $2 AND h.bucket_start < $3 AND h.user_id = 0 AND h.metric = 'ttft'
		GROUP BY 1, 2, 3, 4`, args...)
	if err != nil {
		return nil, fmt.Errorf("load channel monitor v3 latency: %w", err)
	}
	if err := scanChannelMonitorV3Rows(rows, func(rows *sql.Rows) error {
		var f service.ChannelMonitorV3LatencyFact
		if err := rows.Scan(&f.GroupID, &f.Model, &f.Slot, &f.UpperBound, &f.Count); err != nil {
			return err
		}
		facts.Latency = append(facts.Latency, f)
		return nil
	}); err != nil {
		return nil, err
	}
	return facts, nil
}

// RangeFacts reads V2's hourly rollups, which cover 30 days.
func (r *channelMonitorV3Repository) RangeFacts(ctx context.Context, groupIDs []int64, from, to time.Time) (*service.ChannelMonitorV3Facts, error) {
	facts := &service.ChannelMonitorV3Facts{}
	if len(groupIDs) == 0 {
		return facts, nil
	}
	args := []any{pq.Array(groupIDs), from, to}
	rows, err := r.db.QueryContext(ctx, `
		SELECT m.group_id, m.model, SUM(m.success_requests), SUM(m.error_requests)
		FROM channel_monitor_v2_metrics_rollup m
		WHERE m.bucket_seconds = 3600 AND m.group_id = ANY($1) AND m.bucket_start >= $2 AND m.bucket_start < $3
		GROUP BY 1, 2`, args...)
	if err != nil {
		return nil, fmt.Errorf("load channel monitor v3 totals: %w", err)
	}
	if err := scanChannelMonitorV3Rows(rows, func(rows *sql.Rows) error {
		var f service.ChannelMonitorV3Fact
		if err := rows.Scan(&f.GroupID, &f.Model, &f.Success, &f.Errors); err != nil {
			return err
		}
		facts.Metrics = append(facts.Metrics, f)
		return nil
	}); err != nil {
		return nil, err
	}
	rows, err = r.db.QueryContext(ctx, `
		SELECT e.group_id, e.model, e.error_category, SUM(e.error_requests)
		FROM channel_monitor_v2_error_metrics_rollup e
		WHERE e.bucket_seconds = 3600 AND e.group_id = ANY($1) AND e.bucket_start >= $2 AND e.bucket_start < $3
		  AND e.taxonomy_version = $4
		GROUP BY 1, 2, 3`, append(args, service.ChannelMonitorV2TaxonomyVersion)...)
	if err != nil {
		return nil, fmt.Errorf("load channel monitor v3 error totals: %w", err)
	}
	if err := scanChannelMonitorV3Rows(rows, func(rows *sql.Rows) error {
		var f service.ChannelMonitorV3ErrorFact
		if err := rows.Scan(&f.GroupID, &f.Model, &f.Category, &f.Errors); err != nil {
			return err
		}
		facts.Errors = append(facts.Errors, f)
		return nil
	}); err != nil {
		return nil, err
	}
	return facts, nil
}

func (r *channelMonitorV3Repository) DataThrough(ctx context.Context) (*time.Time, error) {
	var through sql.NullTime
	err := r.db.QueryRowContext(ctx, `SELECT data_through FROM channel_monitor_v2_watermarks WHERE id = 1`).Scan(&through)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !through.Valid) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load channel monitor v2 watermark: %w", err)
	}
	value := through.Time.UTC()
	return &value, nil
}

func scanChannelMonitorV3Rows(rows *sql.Rows, scan func(*sql.Rows) error) error {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
