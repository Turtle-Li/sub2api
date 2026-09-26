package repository

import (
	"context"
	"database/sql"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbproxy "github.com/Wei-Shaw/sub2api/ent/proxy"
	dbproxypool "github.com/Wei-Shaw/sub2api/ent/proxypool"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type proxyPoolRepository struct {
	client *dbent.Client
	sql    sqlExecutor
}

func NewProxyPoolRepository(client *dbent.Client, sqlDB *sql.DB) service.ProxyPoolRepository {
	return &proxyPoolRepository{client: client, sql: sqlDB}
}

func (r *proxyPoolRepository) List(ctx context.Context) ([]service.ProxyPool, error) {
	rows, err := r.client.ProxyPool.Query().Order(dbent.Asc(dbproxypool.FieldName), dbent.Asc(dbproxypool.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.ProxyPool, len(rows))
	for i := range rows {
		out[i] = proxyPoolEntityToService(rows[i])
	}
	return out, nil
}

func (r *proxyPoolRepository) GetByID(ctx context.Context, id int64) (*service.ProxyPool, error) {
	row, err := r.client.ProxyPool.Get(ctx, id)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrProxyPoolNotFound, nil)
	}
	pool := proxyPoolEntityToService(row)
	return &pool, nil
}

func (r *proxyPoolRepository) Create(ctx context.Context, pool *service.ProxyPool) error {
	row, err := r.client.ProxyPool.Create().SetName(pool.Name).SetNillableNotes(pool.Notes).Save(ctx)
	if err != nil {
		return err
	}
	*pool = proxyPoolEntityToService(row)
	return nil
}

func (r *proxyPoolRepository) Update(ctx context.Context, pool *service.ProxyPool) error {
	update := r.client.ProxyPool.UpdateOneID(pool.ID).SetName(pool.Name)
	if pool.Notes != nil {
		update = update.SetNotes(*pool.Notes)
	} else {
		update = update.ClearNotes()
	}
	row, err := update.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrProxyPoolNotFound, nil)
	}
	*pool = proxyPoolEntityToService(row)
	return nil
}

func (r *proxyPoolRepository) Delete(ctx context.Context, id int64) error {
	if err := r.client.ProxyPool.DeleteOneID(id).Exec(ctx); err != nil {
		return translatePersistenceError(err, service.ErrProxyPoolNotFound, nil)
	}
	return nil
}

func (r *proxyPoolRepository) Stats(ctx context.Context, poolIDs []int64) (map[int64]*service.ProxyPoolStats, error) {
	out := make(map[int64]*service.ProxyPoolStats, len(poolIDs))
	if len(poolIDs) == 0 {
		return out, nil
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT pp.id,
			COUNT(DISTINCT p.id),
			COUNT(DISTINCT p.id) FILTER (WHERE p.status = 'active' AND (p.expires_at IS NULL OR p.expires_at > NOW())),
			COUNT(DISTINCT p.id) FILTER (WHERE p.id IS NOT NULL AND (p.status <> 'active' OR (p.expires_at IS NOT NULL AND p.expires_at <= NOW()))),
			COUNT(DISTINCT a.id)
		FROM proxy_pools pp
		LEFT JOIN proxies p ON p.pool_id = pp.id AND p.deleted_at IS NULL
		LEFT JOIN accounts a ON a.proxy_id = p.id AND a.deleted_at IS NULL
		WHERE pp.id = ANY($1)
		GROUP BY pp.id`, pq.Array(poolIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var stats service.ProxyPoolStats
		if err := rows.Scan(&id, &stats.Total, &stats.Available, &stats.Unavailable, &stats.BoundAccounts); err != nil {
			return nil, err
		}
		value := stats
		out[id] = &value
	}
	return out, rows.Err()
}

func (r *proxyPoolRepository) AssignProxies(ctx context.Context, poolID int64, proxyIDs []int64) (int64, error) {
	if _, err := r.GetByID(ctx, poolID); err != nil {
		return 0, err
	}
	count, err := r.client.Proxy.Update().Where(dbproxy.IDIn(proxyIDs...)).SetPoolID(poolID).Save(ctx)
	return int64(count), err
}

func (r *proxyPoolRepository) ReleaseProxies(ctx context.Context, proxyIDs []int64) (int64, error) {
	count, err := r.client.Proxy.Update().Where(dbproxy.IDIn(proxyIDs...), dbproxy.PoolIDNotNil()).ClearPoolID().Save(ctx)
	return int64(count), err
}

func (r *proxyPoolRepository) ListProxyIDs(ctx context.Context, poolID int64) ([]int64, error) {
	return r.client.Proxy.Query().Where(dbproxy.PoolIDEQ(poolID)).Order(dbent.Asc(dbproxy.FieldID)).IDs(ctx)
}

func (r *proxyPoolRepository) PickActiveProxy(ctx context.Context, poolID int64) (*service.Proxy, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id
		FROM proxies
		WHERE pool_id = $1
		  AND deleted_at IS NULL
		  AND status = $2
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY random()
		LIMIT 1`, poolID, service.StatusActive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrProxyPoolUnavailable
	}
	var proxyID int64
	if err := rows.Scan(&proxyID); err != nil {
		return nil, err
	}
	row, err := r.client.Proxy.Get(ctx, proxyID)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrProxyNotFound, nil)
	}
	return proxyEntityToService(row), nil
}

func proxyPoolEntityToService(row *dbent.ProxyPool) service.ProxyPool {
	return service.ProxyPool{
		ID: row.ID, Name: row.Name, Notes: row.Notes, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
