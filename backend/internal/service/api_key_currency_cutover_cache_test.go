//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type currencyCutoverAuthCacheBypassStub struct {
	active bool
}

func (s *currencyCutoverAuthCacheBypassStub) Enabled() bool { return s.active }

func TestAPIKeyAuthCacheCurrencyCutoverBypassesL1AndL2AndRecovers(t *testing.T) {
	seed := &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{APIKeyID: 1}}
	cache := &authCacheStub{getAuthCache: func(context.Context, string) (*APIKeyAuthCacheEntry, error) {
		return seed, nil
	}}
	cfg := &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{
		L1Size:       128,
		L1TTLSeconds: 60,
		L2TTLSeconds: 60,
	}}
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, cache, cfg)
	t.Cleanup(func() {
		if svc.authCacheL1 != nil {
			svc.authCacheL1.Close()
		}
	})

	svc.setAuthCacheL1("seed", seed)
	require.NotNil(t, svc.authCacheL1)
	svc.authCacheL1.Wait()

	gate := &currencyCutoverAuthCacheBypassStub{active: true}
	svc.setCurrencyCutoverCacheBypass(gate)

	entry, ok := svc.getAuthCacheEntry(context.Background(), "seed")
	require.False(t, ok)
	require.Nil(t, entry)
	require.Zero(t, cache.getAuthCalls, "the marker bypasses Redis L2 as well as L1")
	_, l1Stored := svc.authCacheL1.Get("seed")
	require.False(t, l1Stored, "activating the marker clears pre-cutover local entries")

	svc.setAuthCacheL1("blocked-l1", seed)
	svc.setAuthCacheEntry(context.Background(), "blocked-l2", seed, time.Minute)
	svc.authCacheL1.Wait()
	_, l1Stored = svc.authCacheL1.Get("blocked-l1")
	require.False(t, l1Stored)
	require.Empty(t, cache.setAuthKeys)

	gate.active = false
	entry, ok = svc.getAuthCacheEntry(context.Background(), "seed")
	require.True(t, ok, "removing the marker restores normal cache use")
	require.Same(t, seed, entry)
	require.Equal(t, 1, cache.getAuthCalls)
	svc.setAuthCacheEntry(context.Background(), "restored-l2", seed, time.Minute)
	require.Equal(t, []string{"restored-l2"}, cache.setAuthKeys)
}
