package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3CacheQueriesCompleteHours(t *testing.T) {
	now := time.Date(2026, 11, 8, 2, 24, 0, 0, time.UTC)
	r := &monitorV3RepoFake{config: monitorV3Config()}
	s := NewChannelMonitorV3Service(r, nil)
	s.now = func() time.Time { return now }
	_, err := s.PlazaStatuses(context.Background(), []int64{4}, false)
	require.NoError(t, err)
	require.Equal(t, now.Truncate(time.Hour).Add(-24*time.Hour), r.cacheFrom)
	require.Equal(t, now.Truncate(time.Hour), r.cacheTo)
}

func TestChannelMonitorV3CacheUsesIndependentRealRequestPool(t *testing.T) {
	now := time.Date(2026, 11, 8, 2, 24, 0, 0, time.UTC)
	through := now.Add(-time.Minute)
	r := &monitorV3RepoFake{config: monitorV3Config(), through: &through}
	r.requestCache = map[int64][]ChannelMonitorV3RequestCache{
		4: {{HourStart: now.Truncate(time.Hour).Add(-time.Hour), Requests: 40, InputTokens: 20, CacheCreationTokens: 20, CacheReadTokens: 960}},
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
	require.Nil(t, out[4].CacheReadRate, "legacy clients must not call a historical maximum a current rate")
	require.InDelta(t, 96.0, *out[4].CachePeakHourRate, 1e-9)
	require.Equal(t, "best_complete_hour", out[4].CacheStatistic)
	require.Equal(t, 24, out[4].CacheLookbackHours)
	require.Equal(t, now.Truncate(time.Hour).Add(-time.Hour), *out[4].CacheHourStart)
	require.Equal(t, now.Truncate(time.Hour), *out[4].CacheHourEnd)
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
		{name: "low cache remains real", state: "ready", fact: ChannelMonitorV3RequestCache{Requests: 30, InputTokens: 500, CacheReadTokens: 500}, want: func() *float64 { n := 50.0; return &n }()},
		{name: "zero cache remains real", state: "ready", fact: ChannelMonitorV3RequestCache{Requests: 30, InputTokens: 100}, want: func() *float64 { n := 0.0; return &n }()},
		{name: "boundary", state: "ready", fact: ChannelMonitorV3RequestCache{Requests: 30, InputTokens: 5, CacheCreationTokens: 5, CacheReadTokens: 90}, want: func() *float64 { n := 90.0; return &n }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.fact.HourStart = now.Truncate(time.Hour).Add(-time.Hour)
			r := &monitorV3RepoFake{config: monitorV3Config(), requestCache: map[int64][]ChannelMonitorV3RequestCache{4: {tc.fact}}}
			s := NewChannelMonitorV3Service(r, nil)
			s.now = func() time.Time { return now }
			out, err := s.PlazaStatuses(context.Background(), []int64{4}, false)
			require.NoError(t, err)
			require.Equal(t, tc.state, out[4].CacheSampleState)
			require.Equal(t, tc.want, out[4].CachePeakHourRate)
			require.Nil(t, out[4].CacheReadRate)
			if tc.want == nil {
				require.Nil(t, out[4].CacheHourStart)
				require.Nil(t, out[4].CacheHourEnd)
			}
		})
	}
}

func TestChannelMonitorV3CacheBestCompleteHourAndExpiry(t *testing.T) {
	now := time.Date(2026, 11, 8, 2, 24, 0, 0, time.UTC)
	end := now.Truncate(time.Hour)
	fact := func(at time.Time, count, read int64) ChannelMonitorV3RequestCache {
		return ChannelMonitorV3RequestCache{HourStart: at, Requests: count, InputTokens: 100 - read, CacheReadTokens: read}
	}
	r := &monitorV3RepoFake{config: monitorV3Config(), requestCache: map[int64][]ChannelMonitorV3RequestCache{4: {
		fact(end.Add(-25*time.Hour), 100, 100), // Expired best cannot stick forever.
		fact(end.Add(-24*time.Hour), 20, 97),   // Inclusive start of the window.
		fact(end.Add(-4*time.Hour), 20, 98),
		fact(end.Add(-3*time.Hour), 20, 50), // A low hour does not rewrite a better hour.
		fact(end.Add(-2*time.Hour), 20, 98), // Latest hour wins a tie.
		fact(end.Add(-time.Hour), 19, 100),  // Sparse spike is ineligible.
		fact(end, 100, 100),                 // Current incomplete hour is ineligible.
		fact(end.Add(time.Hour), 100, 100),
		fact(end.Add(-90*time.Minute), 100, 100), // Misaligned aggregates are invalid.
	}}}
	s := NewChannelMonitorV3Service(r, nil)
	s.now = func() time.Time { return now }
	out, err := s.PlazaStatuses(context.Background(), []int64{4}, false)
	require.NoError(t, err)
	require.InDelta(t, 98.0, *out[4].CachePeakHourRate, 1e-9)
	require.Equal(t, end.Add(-2*time.Hour), *out[4].CacheHourStart)
	// Advancing the window clears the old high when there is no new evidence.
	r.requestCache[4] = []ChannelMonitorV3RequestCache{fact(end.Add(-2*time.Hour), 20, 98)}
	s.now = func() time.Time { return now.Add(24 * time.Hour) }
	out, err = s.PlazaStatuses(context.Background(), []int64{4}, false)
	require.NoError(t, err)
	require.Nil(t, out[4].CachePeakHourRate)
	require.Equal(t, "none", out[4].CacheSampleState)
}

func TestChannelMonitorV3CacheMinimumAppliesToEachHour(t *testing.T) {
	now := time.Date(2026, 11, 8, 2, 24, 0, 0, time.UTC)
	cfg := monitorV3Config()
	cfg.MinRequests = 50
	r := &monitorV3RepoFake{config: cfg, requestCache: map[int64][]ChannelMonitorV3RequestCache{4: {
		{HourStart: now.Truncate(time.Hour).Add(-time.Hour), Requests: 40, InputTokens: 1, CacheReadTokens: 99},
		{HourStart: now.Truncate(time.Hour).Add(-2 * time.Hour), Requests: 40, InputTokens: 1, CacheReadTokens: 99},
	}}}
	s := NewChannelMonitorV3Service(r, nil)
	s.now = func() time.Time { return now }
	out, err := s.PlazaStatuses(context.Background(), []int64{4}, false)
	require.NoError(t, err)
	require.Equal(t, "insufficient", out[4].CacheSampleState)
	require.Nil(t, out[4].CachePeakHourRate)
}
