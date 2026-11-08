package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var dailyRequestExpiredExpr = strings.NewReplacer(
	"quota_daily_start", "quota_daily_request_start",
	"quota_daily_reset_at", "quota_daily_request_reset_at",
).Replace(dailyExpiredExpr)

var dailyRequestQuotaIncrementSQL = `
	UPDATE accounts SET extra = COALESCE(extra, '{}'::jsonb) || jsonb_build_object(
		'quota_daily_request_used', CASE WHEN ` + dailyRequestExpiredExpr + `
			THEN $1::bigint ELSE COALESCE((extra->>'quota_daily_request_used')::bigint, 0) + $1::bigint END,
		'quota_daily_request_start', CASE WHEN ` + dailyRequestExpiredExpr + `
			THEN ` + nowUTC + ` ELSE extra->>'quota_daily_request_start' END
	) || CASE WHEN COALESCE(extra->>'quota_daily_reset_mode', 'rolling') = 'fixed'
		THEN jsonb_build_object('quota_daily_request_reset_at', ` + nextDailyResetAtExpr + `)
		ELSE '{}'::jsonb END, updated_at = NOW()
	WHERE id = $2 AND deleted_at IS NULL
		AND type IN ('apikey', 'bedrock', 'service_account')
		AND COALESCE((extra->>'quota_daily_request_limit')::numeric, 0) > 0
	RETURNING (extra->>'quota_daily_request_used')::bigint,
		(extra->>'quota_daily_request_limit')::numeric::bigint
`

// The upstream owns the hard limit. Record completed calls in the existing
// settlement transaction; in-flight work may cross the local threshold.
func incrementDailyRequestQuota(ctx context.Context, tx *sql.Tx, accountID int64, count int) error {
	if accountID <= 0 || count <= 0 {
		return nil
	}
	var used, limit int64
	err := tx.QueryRowContext(ctx, dailyRequestQuotaIncrementSQL, count, accountID).Scan(&used, &limit)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if used >= limit && used-int64(count) < limit {
		return enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil)
	}
	return nil
}

func mergeDailyRequestUsageForEdit(ctx context.Context, client *dbent.Client, account *service.Account, extra map[string]any) error {
	_, hasUsage := extra["quota_daily_request_used"]
	if account.GetQuotaDailyRequestLimit() <= 0 && !hasUsage {
		return nil
	}
	// The caller already holds this account's row lock. Read fresh counters so
	// saving an older admin form cannot overwrite concurrent settlement.
	rows, err := client.QueryContext(ctx, "SELECT extra FROM accounts WHERE id = $1", account.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var raw []byte
	if !rows.Next() {
		return rows.Err()
	}
	if err := rows.Scan(&raw); err != nil {
		return err
	}
	current := make(map[string]any)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &current); err != nil {
			return err
		}
	}
	for _, key := range []string{"quota_daily_request_used", "quota_daily_request_start"} {
		delete(extra, key)
		if value, ok := current[key]; ok {
			extra[key] = value
		}
	}
	service.NormalizeFixedDailyRequestWindow(extra)
	service.ComputeQuotaResetAt(extra)
	return rows.Err()
}
