package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type accountPoolRepoStub struct {
	AccountPoolRepository
	pools   []AccountPool
	stats   map[int64]*AccountPoolStats
	members []AccountPoolMemberRef
	created *AccountPool
}

func (r *accountPoolRepoStub) List(context.Context, string) ([]AccountPool, error) {
	return r.pools, nil
}

func (r *accountPoolRepoStub) Stats(context.Context, []int64) (map[int64]*AccountPoolStats, error) {
	return r.stats, nil
}

func (r *accountPoolRepoStub) ListMemberRefs(context.Context, []int64) ([]AccountPoolMemberRef, error) {
	return r.members, nil
}

func (r *accountPoolRepoStub) Create(_ context.Context, pool *AccountPool) error {
	pool.ID = 9
	r.created = pool
	return nil
}

type accountPoolUsageRepoStub struct {
	UsageLogRepository
	calls  int
	chunks [][]int64
	stats  map[int64]*usagestats.AccountStats
}

func (r *accountPoolUsageRepoStub) GetAccountWindowStatsBatch(_ context.Context, ids []int64, _ time.Time) (map[int64]*usagestats.AccountStats, error) {
	r.calls++
	r.chunks = append(r.chunks, append([]int64(nil), ids...))
	return r.stats, nil
}

func TestParseAccountListPoolFilter(t *testing.T) {
	cases := map[string]int64{"": 0, " none ": AccountListPoolNone, "12": 12}
	for raw, want := range cases {
		got, err := ParseAccountListPoolFilter(raw)
		require.NoError(t, err, raw)
		require.Equal(t, want, got, raw)
	}
	for _, raw := range []string{"-1", "0", "abc"} {
		_, err := ParseAccountListPoolFilter(raw)
		require.ErrorIs(t, err, ErrAccountPoolInvalidFilter, raw)
	}
}

func TestAccountPoolServiceCreateValidates(t *testing.T) {
	repo := &accountPoolRepoStub{}
	svc := NewAccountPoolService(repo, nil, nil)

	_, err := svc.Create(context.Background(), "  ", PlatformGrok, nil)
	require.ErrorIs(t, err, ErrAccountPoolInvalidName)
	_, err = svc.Create(context.Background(), "pool", " ", nil)
	require.ErrorIs(t, err, ErrAccountPoolInvalidPlatform)

	pool, err := svc.Create(context.Background(), " grok free ", PlatformGrok, nil)
	require.NoError(t, err)
	require.Equal(t, "grok free", pool.Name)
	require.Equal(t, int64(9), repo.created.ID)
}

func TestNormalizeAccountPoolMemberIDs(t *testing.T) {
	ids, err := normalizeAccountPoolMemberIDs([]int64{3, 0, 3, -1, 4})
	require.NoError(t, err)
	require.Equal(t, []int64{3, 4}, ids)
	_, err = normalizeAccountPoolMemberIDs([]int64{0})
	require.ErrorIs(t, err, ErrAccountPoolEmptyMembers)
	tooMany := make([]int64, accountPoolMemberBatchMax+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	_, err = normalizeAccountPoolMemberIDs(tooMany)
	require.ErrorIs(t, err, ErrAccountPoolTooManyMembers)
}

func TestAccountPoolServiceComputeUsageAggregatesGrokFree(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.Grok.FreeQuotaTokenLimit = 1000
	cfg.Gateway.Grok.FreeQuotaSoftGatePercent = 90
	cfg.Gateway.Grok.FreeQuotaWindowHours = 24

	members := make([]AccountPoolMemberRef, 0, accountPoolUsageChunkSize+2)
	for i := 1; i <= accountPoolUsageChunkSize+1; i++ {
		members = append(members, AccountPoolMemberRef{ID: int64(i), PoolID: 1})
	}
	members[0].GrokFree = true
	members[1].GrokFree = true
	members = append(members, AccountPoolMemberRef{ID: 5000, PoolID: 2})

	usage := &accountPoolUsageRepoStub{stats: map[int64]*usagestats.AccountStats{
		1:    {Requests: 2, Tokens: 950, Cost: 0.5},
		2:    {Requests: 1, Tokens: 100, Cost: 0.25},
		5000: {Requests: 7, Tokens: 70},
	}}
	repo := &accountPoolRepoStub{pools: []AccountPool{{ID: 1}, {ID: 2}, {ID: 3}}, members: members}
	svc := NewAccountPoolService(repo, usage, cfg)

	out, err := svc.computeUsage(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, usage.calls, "members are queried in bounded chunks")
	require.Len(t, usage.chunks[0], accountPoolUsageChunkSize)

	p1 := out[1]
	require.Equal(t, int64(3), p1.Requests)
	require.Equal(t, int64(1050), p1.Tokens)
	require.InDelta(t, 0.75, p1.Cost, 1e-9)
	require.Equal(t, 24, p1.WindowHours)
	require.NotNil(t, p1.GrokFree)
	require.Equal(t, int64(2), p1.GrokFree.Accounts)
	require.Equal(t, int64(1050), p1.GrokFree.UsedTokens)
	require.Equal(t, int64(2000), p1.GrokFree.LimitTokens)
	require.Equal(t, int64(1), p1.GrokFree.NearLimit)

	require.Equal(t, int64(7), out[2].Requests)
	require.Nil(t, out[2].GrokFree)
	require.NotNil(t, out[3])
	require.Zero(t, out[3].Requests)
}

func TestAccountPoolServiceListUsesCachedUsageWithoutBlocking(t *testing.T) {
	repo := &accountPoolRepoStub{
		pools: []AccountPool{{ID: 1, Name: "p", Platform: PlatformGrok}},
		stats: map[int64]*AccountPoolStats{1: {Total: 4, Normal: 3, Error: 1}},
	}
	usage := &accountPoolUsageRepoStub{stats: map[int64]*usagestats.AccountStats{}}
	svc := NewAccountPoolService(repo, usage, nil)
	now := time.Now()
	svc.usageTimeNow = func() time.Time { return now }
	svc.usage = map[int64]*AccountPoolUsage{1: {Requests: 42}}
	svc.usageAt = now

	out, err := svc.List(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, int64(4), out[0].Stats.Total)
	require.Equal(t, int64(42), out[0].Usage.Requests)
	require.Zero(t, usage.calls, "fresh cache must not trigger a refresh")

	svc.refreshUsage()
	require.Zero(t, usage.calls, "refresh within TTL is a no-op")

	now = now.Add(accountPoolUsageCacheTTL)
	svc.refreshUsage()
	require.Equal(t, 0, usage.calls, "pools without members skip usage_logs")
	svc.usageMu.RLock()
	defer svc.usageMu.RUnlock()
	require.Equal(t, now, svc.usageAt)
}
