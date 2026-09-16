package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/stretchr/testify/require"
)

type proxyTimezoneBackfillWrite struct {
	proxyID  int64
	timezone string
}

type proxyTimezoneBackfillRepoStub struct {
	ProxyRepository
	proxies []Proxy

	mu     sync.Mutex
	writes []proxyTimezoneBackfillWrite
}

func (r *proxyTimezoneBackfillRepoStub) ListActive(context.Context) ([]Proxy, error) {
	return append([]Proxy(nil), r.proxies...), nil
}

func (r *proxyTimezoneBackfillRepoStub) UpdateDetectedTimezone(_ context.Context, proxy *Proxy, timezone string, _ time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes = append(r.writes, proxyTimezoneBackfillWrite{proxyID: proxy.ID, timezone: timezone})
	return proxy.Host != "stale.test", nil
}

func (r *proxyTimezoneBackfillRepoStub) savedWrites() []proxyTimezoneBackfillWrite {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]proxyTimezoneBackfillWrite(nil), r.writes...)
}

type proxyTimezoneBackfillProberStub struct {
	current atomic.Int32
	max     atomic.Int32
	calls   atomic.Int32
	block   bool
}

func (p *proxyTimezoneBackfillProberStub) ProbeProxy(ctx context.Context, proxyURL string) (*ProxyExitInfo, int64, error) {
	p.calls.Add(1)
	current := p.current.Add(1)
	defer p.current.Add(-1)
	for {
		observed := p.max.Load()
		if current <= observed || p.max.CompareAndSwap(observed, current) {
			break
		}
	}
	if p.block {
		<-ctx.Done()
		return nil, 0, ctx.Err()
	}
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case <-time.After(15 * time.Millisecond):
	}
	switch {
	case strings.Contains(proxyURL, "invalid.test"):
		return &ProxyExitInfo{Timezone: "GMT+9"}, 1, nil
	case strings.Contains(proxyURL, "error.test"):
		return nil, 1, errors.New("unreachable")
	case strings.Contains(proxyURL, "us.test"):
		return &ProxyExitInfo{Timezone: "America/Los_Angeles"}, 1, nil
	default:
		return &ProxyExitInfo{Timezone: "Asia/Tokyo"}, 1, nil
	}
}

func TestProxyTimezoneBackfillRunOnceOnlyProbesMissingMetadataWithBoundedConcurrency(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	t.Setenv(runtimegate.StateFileEnv, "")

	repo := &proxyTimezoneBackfillRepoStub{proxies: []Proxy{
		{ID: 1, Protocol: "http", Host: "jp-one.test", Port: 8080},
		{ID: 2, Protocol: "http", Host: "already.test", Port: 8080, DetectedTimezone: "Asia/Shanghai"},
		{ID: 3, Protocol: "http", Host: "invalid.test", Port: 8080},
		{ID: 4, Protocol: "http", Host: "stale.test", Port: 8080},
		{ID: 5, Protocol: "http", Host: "us.test", Port: 8080},
		{ID: 6, Protocol: "http", Host: "jp-two.test", Port: 8080},
		{ID: 7, Protocol: "http", Host: "error.test", Port: 8080},
	}}
	prober := &proxyTimezoneBackfillProberStub{}
	svc := NewProxyTimezoneBackfillService(repo, prober)

	stats, acquired, err := svc.runOnce(context.Background())
	require.NoError(t, err)
	require.True(t, acquired)
	require.Equal(t, proxyTimezoneBackfillStats{Candidates: 6, Updated: 3, Failed: 2, Stale: 1}, stats)
	require.EqualValues(t, 6, prober.calls.Load())
	require.LessOrEqual(t, prober.max.Load(), int32(proxyTimezoneBackfillConcurrency))
	require.Greater(t, prober.max.Load(), int32(1))
	require.ElementsMatch(t, []proxyTimezoneBackfillWrite{
		{proxyID: 1, timezone: "Asia/Tokyo"},
		{proxyID: 4, timezone: "Asia/Tokyo"},
		{proxyID: 5, timezone: "America/Los_Angeles"},
		{proxyID: 6, timezone: "Asia/Tokyo"},
	}, repo.savedWrites())
}

func TestProxyTimezoneBackfillStopCancelsInFlightProbe(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	t.Setenv(runtimegate.StateFileEnv, "")

	repo := &proxyTimezoneBackfillRepoStub{proxies: []Proxy{
		{ID: 1, Protocol: "http", Host: "jp.test", Port: 8080},
	}}
	prober := &proxyTimezoneBackfillProberStub{block: true}
	svc := NewProxyTimezoneBackfillService(repo, prober)
	svc.Start()
	require.Eventually(t, func() bool { return prober.calls.Load() == 1 }, time.Second, 5*time.Millisecond)

	stopped := make(chan struct{})
	go func() {
		svc.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel the in-flight timezone probe")
	}
	require.Empty(t, repo.savedWrites())
}
