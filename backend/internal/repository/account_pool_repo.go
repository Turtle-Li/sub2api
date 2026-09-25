package repository

import (
	"context"
	"database/sql"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbaccountpool "github.com/Wei-Shaw/sub2api/ent/accountpool"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type accountPoolRepository struct {
	client *dbent.Client
	sql    sqlExecutor
}

func NewAccountPoolRepository(client *dbent.Client, sqlDB *sql.DB) service.AccountPoolRepository {
	return &accountPoolRepository{client: client, sql: sqlDB}
}

func (r *accountPoolRepository) List(ctx context.Context, platform string) ([]service.AccountPool, error) {
	q := r.client.AccountPool.Query()
	if platform != "" {
		q = q.Where(dbaccountpool.PlatformEQ(platform))
	}
	rows, err := q.Order(dbent.Asc(dbaccountpool.FieldName), dbent.Asc(dbaccountpool.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.AccountPool, len(rows))
	for i, row := range rows {
		out[i] = accountPoolEntityToService(row)
	}
	return out, nil
}

func (r *accountPoolRepository) GetByID(ctx context.Context, id int64) (*service.AccountPool, error) {
	row, err := r.client.AccountPool.Get(ctx, id)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrAccountPoolNotFound, nil)
	}
	pool := accountPoolEntityToService(row)
	return &pool, nil
}

func (r *accountPoolRepository) Create(ctx context.Context, pool *service.AccountPool) error {
	row, err := r.client.AccountPool.Create().
		SetName(pool.Name).
		SetPlatform(pool.Platform).
		SetNillableNotes(pool.Notes).
		Save(ctx)
	if err != nil {
		return err
	}
	*pool = accountPoolEntityToService(row)
	return nil
}

func (r *accountPoolRepository) Update(ctx context.Context, pool *service.AccountPool) error {
	update := r.client.AccountPool.UpdateOneID(pool.ID).SetName(pool.Name)
	if pool.Notes != nil {
		update = update.SetNotes(*pool.Notes)
	} else {
		update = update.ClearNotes()
	}
	row, err := update.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrAccountPoolNotFound, nil)
	}
	*pool = accountPoolEntityToService(row)
	return nil
}

func (r *accountPoolRepository) Delete(ctx context.Context, id int64) error {
	if err := r.client.AccountPool.DeleteOneID(id).Exec(ctx); err != nil {
		return translatePersistenceError(err, service.ErrAccountPoolNotFound, nil)
	}
	return nil
}

// codexEffectiveUsedPercentSQL treats a snapshot whose window already reset as 0% used,
// so stale snapshots do not inflate the pool average.
func codexEffectiveUsedPercentSQL(window string) string {
	used := fmt.Sprintf("extra->'codex_%s_used_percent'", window)
	resetAt := fmt.Sprintf("extra->>'codex_%s_reset_at'", window)
	return fmt.Sprintf(`CASE WHEN jsonb_typeof(%[1]s) <> 'number' THEN NULL
		WHEN %[2]s ~ '^\d{4}-\d{2}-\d{2}T' AND (%[2]s)::timestamptz <= NOW() THEN 0
		ELSE (%[1]s)::float8 END`, used, resetAt)
}

// Status buckets mirror buildAccountListFilteredQuery so pool stat
// click-through into the member list shows the same counts.
var accountPoolStatsQuery = fmt.Sprintf(`
WITH members AS (
	SELECT pool_id, status, schedulable, expires_at,
		(rate_limit_reset_at IS NOT NULL AND rate_limit_reset_at > NOW()) AS rate_limited,
		(temp_unschedulable_until IS NOT NULL AND temp_unschedulable_until > NOW()) AS temp_unsched,
		(%s) AS codex_5h,
		(%s) AS codex_7d
	FROM accounts
	WHERE deleted_at IS NULL AND pool_id = ANY($1)
)
SELECT pool_id,
	COUNT(*),
	COUNT(*) FILTER (WHERE status = 'active' AND schedulable AND NOT rate_limited AND NOT temp_unsched),
	COUNT(*) FILTER (WHERE status = 'active' AND rate_limited AND NOT temp_unsched),
	COUNT(*) FILTER (WHERE status = 'active' AND temp_unsched),
	COUNT(*) FILTER (WHERE status = 'active' AND NOT schedulable AND NOT rate_limited AND NOT temp_unsched),
	COUNT(*) FILTER (WHERE status = 'error'),
	COUNT(*) FILTER (WHERE status NOT IN ('active', 'error')),
	COUNT(*) FILTER (WHERE expires_at IS NOT NULL AND expires_at <= NOW()),
	COUNT(*) FILTER (WHERE codex_5h IS NOT NULL OR codex_7d IS NOT NULL),
	AVG(codex_5h),
	AVG(codex_7d),
	COUNT(*) FILTER (WHERE codex_5h >= 100 OR codex_7d >= 100)
FROM members
GROUP BY pool_id`, codexEffectiveUsedPercentSQL("5h"), codexEffectiveUsedPercentSQL("7d"))

func (r *accountPoolRepository) Stats(ctx context.Context, poolIDs []int64) (map[int64]*service.AccountPoolStats, error) {
	out := make(map[int64]*service.AccountPoolStats, len(poolIDs))
	if len(poolIDs) == 0 {
		return out, nil
	}
	rows, err := r.sql.QueryContext(ctx, accountPoolStatsQuery, pq.Array(poolIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			poolID         int64
			stats          service.AccountPoolStats
			codexAccounts  int64
			avg5h, avg7d   sql.NullFloat64
			codexExhausted int64
		)
		if err := rows.Scan(&poolID, &stats.Total, &stats.Normal, &stats.RateLimited, &stats.TempUnschedulable,
			&stats.Unschedulable, &stats.Error, &stats.Inactive, &stats.Expired,
			&codexAccounts, &avg5h, &avg7d, &codexExhausted); err != nil {
			return nil, err
		}
		if codexAccounts > 0 {
			stats.Codex = &service.AccountPoolCodexQuota{Accounts: codexAccounts, Exhausted: codexExhausted}
			if avg5h.Valid {
				v := avg5h.Float64
				stats.Codex.Avg5hUsedPercent = &v
			}
			if avg7d.Valid {
				v := avg7d.Float64
				stats.Codex.Avg7dUsedPercent = &v
			}
		}
		st := stats
		out[poolID] = &st
	}
	return out, rows.Err()
}

// Mirrors isExplicitGrokFreeOAuthAccount.
const accountPoolMemberRefsQuery = `
SELECT id, pool_id,
	(platform = 'grok' AND type = 'oauth' AND (
		LOWER(TRIM(COALESCE(credentials->>'subscription_tier', ''))) = 'free' OR
		LOWER(TRIM(COALESCE(credentials->>'plan_type', ''))) = 'free' OR
		LOWER(TRIM(COALESCE(extra->>'subscription_tier', ''))) = 'free' OR
		LOWER(TRIM(COALESCE(extra->>'plan_type', ''))) = 'free'
	)) AS grok_free
FROM accounts
WHERE deleted_at IS NULL AND pool_id = ANY($1)
ORDER BY id`

func (r *accountPoolRepository) ListMemberRefs(ctx context.Context, poolIDs []int64) ([]service.AccountPoolMemberRef, error) {
	if len(poolIDs) == 0 {
		return nil, nil
	}
	rows, err := r.sql.QueryContext(ctx, accountPoolMemberRefsQuery, pq.Array(poolIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []service.AccountPoolMemberRef
	for rows.Next() {
		var ref service.AccountPoolMemberRef
		if err := rows.Scan(&ref.ID, &ref.PoolID, &ref.GrokFree); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

func (r *accountPoolRepository) AssignAccounts(ctx context.Context, poolID int64, accountIDs []int64) (int64, error) {
	pool, err := r.GetByID(ctx, poolID)
	if err != nil {
		return 0, err
	}
	mismatched, err := r.client.Account.Query().
		Where(dbaccount.IDIn(accountIDs...), dbaccount.PlatformNEQ(pool.Platform)).
		Exist(ctx)
	if err != nil {
		return 0, err
	}
	if mismatched {
		return 0, service.ErrAccountPoolPlatformMismatch
	}
	n, err := r.client.Account.Update().
		Where(dbaccount.IDIn(accountIDs...), dbaccount.PlatformEQ(pool.Platform)).
		SetPoolID(poolID).
		Save(ctx)
	return int64(n), err
}

func (r *accountPoolRepository) ReleaseAccounts(ctx context.Context, accountIDs []int64) (int64, error) {
	n, err := r.client.Account.Update().
		Where(dbaccount.IDIn(accountIDs...), dbaccount.PoolIDNotNil()).
		ClearPoolID().
		Save(ctx)
	return int64(n), err
}

func (r *accountPoolRepository) ListAccountIDs(ctx context.Context, poolID int64, status string) ([]int64, error) {
	return buildAccountListFilteredQuery(r.client, service.AccountListFilter{Status: status, PoolID: poolID}).
		Order(dbent.Asc(dbaccount.FieldID)).
		IDs(ctx)
}

// ensureAccountPoolAcceptsPlatform validates the pool referenced by a new account.
func ensureAccountPoolAcceptsPlatform(ctx context.Context, client *dbent.Client, poolID int64, platform string) error {
	pool, err := client.AccountPool.Get(ctx, poolID)
	if err != nil {
		return translatePersistenceError(err, service.ErrAccountPoolNotFound, nil)
	}
	if pool.Platform != platform {
		return service.ErrAccountPoolPlatformMismatch
	}
	return nil
}

func accountPoolEntityToService(row *dbent.AccountPool) service.AccountPool {
	return service.AccountPool{
		ID:        row.ID,
		Name:      row.Name,
		Platform:  row.Platform,
		Notes:     row.Notes,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}
