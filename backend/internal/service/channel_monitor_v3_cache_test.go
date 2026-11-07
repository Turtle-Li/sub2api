package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3CacheUsesIndependentRealRequestPool(t *testing.T) {
	now := time.Date(2026, 11, 8, 2, 24, 0, 0, time.UTC)
	through := now.Add(-time.Minute)
	r := &monitorV3RepoFake{config: monitorV3Config(), through: &through}
	r.requestCache = map[int64]ChannelMonitorV3RequestCache{
		4: {Requests: 40, InputTokens: 20, CacheCreationTokens: 20, CacheReadTokens: 960},
	}
	// A cheap check group has no customer traffic of its own.
	s := NewChannelMonitorV3Service(r, nil)
	s.now = func() time.Time { return now }
	out, err := s.PlazaStatuses(context.Background(), []int64{4}, false)
	require.NoError(t, err)
	require.Nil(t, out[4].RequestSuccessRate)
	require.Nil(t, out[4].TTFTP50Ms)
	require.Equal(t, "ready", out[4].CacheSampleState)
	require.Equal(t, "shared_account_pool", out[4].CacheScope)
	require.InDelta(t, 96.0, *out[4].CacheReadRate, 1e-9)
	require.Equal(t, now.Add(-180*time.Minute), r.cacheFrom)
	require.Equal(t, now, r.cacheTo)
}

func TestChannelMonitorV3CachePublicationGuards(t *testing.T) {
	now := time.Date(2026, 11, 8, 2, 24, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, state string
		fact        ChannelMonitorV3RequestCache
		want        *float64
	}{
		{name: "no real requests", state: "none"},
		{name: "small sample", state: "insufficient", fact: ChannelMonitorV3RequestCache{Requests: 19, CacheReadTokens: 100}},
		{name: "no prompt tokens", state: "none", fact: ChannelMonitorV3RequestCache{Requests: 30}},
		{name: "invalid negative bucket", state: "none", fact: ChannelMonitorV3RequestCache{Requests: 30, InputTokens: -10, CacheReadTokens: 100}},
		{name: "low cache", state: "below_threshold", fact: ChannelMonitorV3RequestCache{Requests: 30, InputTokens: 500, CacheReadTokens: 500}},
		{name: "zero cache", state: "below_threshold", fact: ChannelMonitorV3RequestCache{Requests: 30, InputTokens: 100}},
		{name: "boundary", state: "ready", fact: ChannelMonitorV3RequestCache{Requests: 30, InputTokens: 5, CacheCreationTokens: 5, CacheReadTokens: 90}, want: func() *float64 { n := 90.0; return &n }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &monitorV3RepoFake{config: monitorV3Config(), requestCache: map[int64]ChannelMonitorV3RequestCache{4: tc.fact}}
			s := NewChannelMonitorV3Service(r, nil)
			s.now = func() time.Time { return now }
			out, err := s.PlazaStatuses(context.Background(), []int64{4}, false)
			require.NoError(t, err)
			require.Equal(t, tc.state, out[4].CacheSampleState)
			require.Equal(t, tc.want, out[4].CacheReadRate)
		})
	}
}
