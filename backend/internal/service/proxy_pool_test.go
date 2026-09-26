package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type proxyPoolRepositoryStub struct {
	pools       []ProxyPool
	stats       map[int64]*ProxyPoolStats
	created     *ProxyPool
	assignedIDs []int64
	assignedTo  int64
}

func (s *proxyPoolRepositoryStub) List(context.Context) ([]ProxyPool, error) { return s.pools, nil }
func (s *proxyPoolRepositoryStub) GetByID(_ context.Context, id int64) (*ProxyPool, error) {
	for i := range s.pools {
		if s.pools[i].ID == id {
			pool := s.pools[i]
			return &pool, nil
		}
	}
	return nil, ErrProxyPoolNotFound
}
func (s *proxyPoolRepositoryStub) Create(_ context.Context, pool *ProxyPool) error {
	pool.ID = 10
	pool.CreatedAt = time.Unix(1, 0)
	pool.UpdatedAt = time.Unix(1, 0)
	s.created = pool
	return nil
}
func (s *proxyPoolRepositoryStub) Update(context.Context, *ProxyPool) error { return nil }
func (s *proxyPoolRepositoryStub) Delete(context.Context, int64) error      { return nil }
func (s *proxyPoolRepositoryStub) Stats(_ context.Context, ids []int64) (map[int64]*ProxyPoolStats, error) {
	if s.stats == nil {
		return map[int64]*ProxyPoolStats{}, nil
	}
	return s.stats, nil
}
func (s *proxyPoolRepositoryStub) AssignProxies(_ context.Context, poolID int64, ids []int64) (int64, error) {
	s.assignedTo = poolID
	s.assignedIDs = ids
	return int64(len(ids)), nil
}
func (s *proxyPoolRepositoryStub) ReleaseProxies(_ context.Context, ids []int64) (int64, error) {
	s.assignedIDs = ids
	return int64(len(ids)), nil
}
func (s *proxyPoolRepositoryStub) ListProxyIDs(context.Context, int64) ([]int64, error) {
	return []int64{1, 2}, nil
}
func (s *proxyPoolRepositoryStub) PickActiveProxy(context.Context, int64) (*Proxy, error) {
	return &Proxy{ID: 1, Status: StatusActive}, nil
}

func TestProxyPoolServiceNormalizesCreateAndMembers(t *testing.T) {
	repo := &proxyPoolRepositoryStub{}
	svc := NewProxyPoolService(repo)
	notes := "  free grok exits  "

	pool, err := svc.Create(context.Background(), "  free-grok  ", &notes)
	require.NoError(t, err)
	require.Equal(t, "free-grok", pool.Name)
	require.Equal(t, "free grok exits", *pool.Notes)

	affected, err := svc.AssignProxies(context.Background(), pool.ID, []int64{3, 3, 0, -1, 5})
	require.NoError(t, err)
	require.Equal(t, int64(2), affected)
	require.Equal(t, []int64{3, 5}, repo.assignedIDs)
	require.Equal(t, pool.ID, repo.assignedTo)
}

func TestParseProxyListPoolFilter(t *testing.T) {
	cases := map[string]int64{"": 0, " none ": ProxyListPoolNone, "12": 12}
	for raw, want := range cases {
		got, err := ParseProxyListPoolFilter(raw)
		require.NoError(t, err, raw)
		require.Equal(t, want, got, raw)
	}
	for _, raw := range []string{"-1", "0", "abc"} {
		_, err := ParseProxyListPoolFilter(raw)
		require.ErrorIs(t, err, ErrProxyPoolInvalidFilter, raw)
	}
}

func TestProxyPoolServiceRejectsInvalidPoolAndMembers(t *testing.T) {
	svc := NewProxyPoolService(&proxyPoolRepositoryStub{})

	_, err := svc.Create(context.Background(), "   ", nil)
	require.ErrorIs(t, err, ErrProxyPoolInvalidName)

	_, err = svc.AssignProxies(context.Background(), 1, nil)
	require.ErrorIs(t, err, ErrProxyPoolEmptyMembers)
}

func TestProxyPoolServiceListIncludesStats(t *testing.T) {
	repo := &proxyPoolRepositoryStub{
		pools: []ProxyPool{{ID: 7, Name: "pool"}},
		stats: map[int64]*ProxyPoolStats{7: {Total: 200, Available: 198, BoundAccounts: 4}},
	}
	summaries, err := NewProxyPoolService(repo).List(context.Background())
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	require.Equal(t, int64(200), summaries[0].Stats.Total)
	require.Equal(t, int64(4), summaries[0].Stats.BoundAccounts)
}
