package service

import (
	"context"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	proxyPoolNameMaxLen     = 100
	proxyPoolMemberBatchMax = 5000
)

var (
	ErrProxyPoolNotFound         = infraerrors.NotFound("PROXY_POOL_NOT_FOUND", "proxy pool not found")
	ErrProxyPoolInvalidName      = infraerrors.BadRequest("PROXY_POOL_INVALID_NAME", "proxy pool name is required and must be at most 100 characters")
	ErrProxyPoolEmptyMembers     = infraerrors.BadRequest("PROXY_POOL_EMPTY_MEMBERS", "proxy_ids is required")
	ErrProxyPoolTooManyMembers   = infraerrors.BadRequest("PROXY_POOL_TOO_MANY_MEMBERS", "too many proxy_ids in one request")
	ErrProxyPoolUnavailable      = infraerrors.Conflict("PROXY_POOL_UNAVAILABLE", "proxy pool has no active non-expired proxy")
	ErrProxyPoolFreeOnly         = infraerrors.BadRequest("PROXY_POOL_FREE_ONLY", "automatic proxy pool assignment is only available for explicit Grok free accounts")
	ErrProxyPoolBindingChanged   = infraerrors.Conflict("PROXY_POOL_BINDING_CHANGED", "selected proxy changed before the account was created; retry the import")
	ErrProxyPoolGuardUnavailable = infraerrors.InternalServer(
		"PROXY_POOL_GUARD_UNAVAILABLE",
		"the proxy pool assignment guard is unavailable",
	)
)

// ProxyPool groups proxies for future automatic assignment. Existing accounts
// remain pinned to accounts.proxy_id even when membership later changes.
type ProxyPool struct {
	ID        int64
	Name      string
	Notes     *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ProxyPoolStats struct {
	Total         int64 `json:"total"`
	Available     int64 `json:"available"`
	Unavailable   int64 `json:"unavailable"`
	BoundAccounts int64 `json:"bound_accounts"`
}

type ProxyPoolSummary struct {
	Pool  ProxyPool
	Stats ProxyPoolStats
}

type ProxyPoolRepository interface {
	List(ctx context.Context) ([]ProxyPool, error)
	GetByID(ctx context.Context, id int64) (*ProxyPool, error)
	Create(ctx context.Context, pool *ProxyPool) error
	Update(ctx context.Context, pool *ProxyPool) error
	Delete(ctx context.Context, id int64) error
	Stats(ctx context.Context, poolIDs []int64) (map[int64]*ProxyPoolStats, error)
	AssignProxies(ctx context.Context, poolID int64, proxyIDs []int64) (int64, error)
	ReleaseProxies(ctx context.Context, proxyIDs []int64) (int64, error)
	ListProxyIDs(ctx context.Context, poolID int64) ([]int64, error)
	PickActiveProxy(ctx context.Context, poolID int64) (*Proxy, error)
}

type ProxyPoolService struct {
	repo ProxyPoolRepository
}

func NewProxyPoolService(repo ProxyPoolRepository) *ProxyPoolService {
	return &ProxyPoolService{repo: repo}
}

func (s *ProxyPoolService) List(ctx context.Context) ([]ProxyPoolSummary, error) {
	pools, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	if len(pools) == 0 {
		return []ProxyPoolSummary{}, nil
	}
	ids := make([]int64, len(pools))
	for i := range pools {
		ids[i] = pools[i].ID
	}
	stats, err := s.repo.Stats(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]ProxyPoolSummary, len(pools))
	for i := range pools {
		out[i].Pool = pools[i]
		if value := stats[pools[i].ID]; value != nil {
			out[i].Stats = *value
		}
	}
	return out, nil
}

func (s *ProxyPoolService) Get(ctx context.Context, id int64) (*ProxyPoolSummary, error) {
	pool, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	stats, err := s.repo.Stats(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	out := &ProxyPoolSummary{Pool: *pool}
	if value := stats[id]; value != nil {
		out.Stats = *value
	}
	return out, nil
}

func (s *ProxyPoolService) Create(ctx context.Context, name string, notes *string) (*ProxyPool, error) {
	pool := &ProxyPool{Name: strings.TrimSpace(name), Notes: normalizeProxyPoolNotes(notes)}
	if err := validateProxyPool(pool); err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, pool); err != nil {
		return nil, err
	}
	return pool, nil
}

func (s *ProxyPoolService) Update(ctx context.Context, id int64, name *string, notes *string) (*ProxyPool, error) {
	pool, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if name != nil {
		pool.Name = strings.TrimSpace(*name)
	}
	if notes != nil {
		pool.Notes = normalizeProxyPoolNotes(notes)
	}
	if err := validateProxyPool(pool); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, pool); err != nil {
		return nil, err
	}
	return pool, nil
}

func (s *ProxyPoolService) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

func (s *ProxyPoolService) AssignProxies(ctx context.Context, poolID int64, proxyIDs []int64) (int64, error) {
	ids, err := normalizeProxyPoolMemberIDs(proxyIDs)
	if err != nil {
		return 0, err
	}
	return s.repo.AssignProxies(ctx, poolID, ids)
}

func (s *ProxyPoolService) ReleaseProxies(ctx context.Context, proxyIDs []int64) (int64, error) {
	ids, err := normalizeProxyPoolMemberIDs(proxyIDs)
	if err != nil {
		return 0, err
	}
	return s.repo.ReleaseProxies(ctx, ids)
}

func (s *ProxyPoolService) ListProxyIDs(ctx context.Context, poolID int64) ([]int64, error) {
	if _, err := s.repo.GetByID(ctx, poolID); err != nil {
		return nil, err
	}
	return s.repo.ListProxyIDs(ctx, poolID)
}

func (s *ProxyPoolService) PickActiveProxy(ctx context.Context, poolID int64) (*Proxy, error) {
	if _, err := s.repo.GetByID(ctx, poolID); err != nil {
		return nil, err
	}
	return s.repo.PickActiveProxy(ctx, poolID)
}

func validateProxyPool(pool *ProxyPool) error {
	if pool == nil || pool.Name == "" || len([]rune(pool.Name)) > proxyPoolNameMaxLen {
		return ErrProxyPoolInvalidName
	}
	return nil
}

func normalizeProxyPoolNotes(notes *string) *string {
	if notes == nil {
		return nil
	}
	value := strings.TrimSpace(*notes)
	if value == "" {
		return nil
	}
	return &value
}

func normalizeProxyPoolMemberIDs(proxyIDs []int64) ([]int64, error) {
	seen := make(map[int64]struct{}, len(proxyIDs))
	ids := make([]int64, 0, len(proxyIDs))
	for _, id := range proxyIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, ErrProxyPoolEmptyMembers
	}
	if len(ids) > proxyPoolMemberBatchMax {
		return nil, ErrProxyPoolTooManyMembers
	}
	return ids, nil
}
