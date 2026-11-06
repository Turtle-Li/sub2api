//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type museTestUpstream struct {
	calls     atomic.Int64
	lastProxy string
}

func (u *museTestUpstream) Do(r *http.Request, proxy string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	u.lastProxy = proxy
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxy != "" {
		parsed, err := url.Parse(proxy)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	if HTTPUpstreamRedirectsDisabled(r.Context()) {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return client.Do(r)
}
func (u *museTestUpstream) DoWithTLS(r *http.Request, p string, a int64, c int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, a, c)
}

type museSlotCache struct {
	stubConcurrencyCacheForTest
	active   atomic.Int64
	peak     atomic.Int64
	releases atomic.Int64
}

func (c *museSlotCache) AcquireAccountSlot(_ context.Context, _ int64, max int, _ string) (bool, error) {
	for {
		current := c.active.Load()
		if current >= int64(max) {
			return false, nil
		}
		if c.active.CompareAndSwap(current, current+1) {
			for {
				peak := c.peak.Load()
				if peak >= current+1 || c.peak.CompareAndSwap(peak, current+1) {
					break
				}
			}
			return true, nil
		}
	}
}
func (c *museSlotCache) ReleaseAccountSlot(context.Context, int64, string) error {
	c.active.Add(-1)
	c.releases.Add(1)
	return nil
}
func newTestMuseProvider(store BatchImageProviderObjectStore, prefix string) *OpenAIImagesBatchProvider {
	return NewMuseImagesBatchProvider(store, prefix, &OpenAIGatewayService{concurrencyService: NewConcurrencyService(&museSlotCache{}), httpUpstream: &museTestUpstream{}})
}
func museSubmitJob(t *testing.T, p *OpenAIImagesBatchProvider, a *Account, id string) *BatchImageJob {
	t.Helper()
	job := &BatchImageJob{BatchID: id, Provider: BatchImageProviderMuse, Model: "muse-image", ItemCount: 1}
	submitted, err := p.Submit(t.Context(), job, a, BatchImageInput{BatchID: id, Model: "muse-image", Items: []BatchImageInputItem{{CustomID: "one", Prompt: "image"}}})
	require.NoError(t, err)
	job.ProviderInputRef = &submitted.ProviderInputRef
	job.ProviderJobName = &submitted.ProviderJobName
	return job
}
func TestMuseRuntimeAdmissionAcrossWorkers(t *testing.T) {
	entered, unblock := make(chan struct{}, 1), make(chan struct{})
	var release sync.Once
	var posts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if posts.Add(1) == 1 {
				entered <- struct{}{}
				<-unblock
			}
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"task_id":"queued"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"queued"}`))
	}))
	defer server.Close()
	defer release.Do(func() { close(unblock) })
	cache := &museSlotCache{}
	upstream := &museTestUpstream{}
	gateway := &OpenAIGatewayService{concurrencyService: NewConcurrencyService(cache), httpUpstream: upstream}
	store := &museRuntimeStore{fakeOpenAIImageBatchStore: newFakeOpenAIImageBatchStore()}
	cfg := &config.Config{}
	cfg.BatchImage.DeliveryCOSPrefix = testOpenAIImageBatchObjectPrefix
	registry := NewBatchImageProviderRegistryWithRuntime(cfg, store, gateway)
	raw, ok := registry.Get(BatchImageProviderMuse)
	require.True(t, ok)
	p := raw.(*OpenAIImagesBatchProvider)
	require.Same(t, gateway, p.executor.(*museImageBatchExecutor).gateway)
	a := museTestAccount(server.URL)
	a.Concurrency = 1
	first := museSubmitJob(t, p, a, "imgbatch_first")
	second := museSubmitJob(t, p, a, "imgbatch_second")
	done := make(chan error, 1)
	go func() { _, err := p.Advance(context.Background(), first, a); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first POST did not start")
	}
	status, err := p.Advance(t.Context(), second, a)
	require.NoError(t, err)
	require.False(t, status.Done)
	require.Equal(t, int64(1), posts.Load())
	exists, err := store.Exists(t.Context(), p.itemAttemptKey(second.BatchID, 0))
	require.NoError(t, err)
	require.False(t, exists)
	release.Do(func() { close(unblock) })
	require.NoError(t, <-done)
	require.Zero(t, cache.active.Load())
	_, err = p.Advance(t.Context(), second, a)
	require.NoError(t, err)
	require.Equal(t, int64(2), posts.Load())
	require.Equal(t, int64(1), cache.peak.Load())
	// Polling reuses admission and releases its slot while remote work remains queued.
	cache.active.Store(1)
	_, err = p.Advance(t.Context(), first, a)
	require.NoError(t, err)
	require.Equal(t, int64(2), upstream.calls.Load())
	cache.active.Store(0)
	_, err = p.Advance(t.Context(), first, a)
	require.NoError(t, err)
	require.Equal(t, int64(3), upstream.calls.Load())
	require.Zero(t, cache.active.Load())
	require.Equal(t, int64(3), cache.releases.Load())
}
func TestMuseConfiguredProxyAndBrokenBinding(t *testing.T) {
	var direct, proxied atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { direct.Add(1) }))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		require.True(t, r.URL.IsAbs())
		if r.Method == http.MethodPost {
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"task_id":"proxy-task"}`))
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status": "queued"}))
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(proxyURL.Port())
	require.NoError(t, err)
	a := museTestAccount(target.URL)
	a.Concurrency = 1
	id := int64(7)
	a.ProxyID = &id
	for _, broken := range []*Proxy{nil, {ID: id, Status: StatusDisabled, Protocol: "http", Host: proxyURL.Hostname(), Port: port}} {
		p := newTestMuseProvider(newFakeOpenAIImageBatchStore(), testOpenAIImageBatchObjectPrefix)
		a.Proxy = broken
		job := museSubmitJob(t, p, a, "imgbatch_broken")
		_, err := p.Advance(t.Context(), job, a)
		require.ErrorIs(t, err, ErrAccountProxyUnavailable)
		exists, err := p.store.Exists(t.Context(), p.itemAttemptKey(job.BatchID, 0))
		require.NoError(t, err)
		require.False(t, exists)
		require.Zero(t, p.executor.(*museImageBatchExecutor).gateway.httpUpstream.(*museTestUpstream).calls.Load())
	}
	a.Proxy = &Proxy{ID: id, Status: StatusActive, Protocol: "http", Host: proxyURL.Hostname(), Port: port}
	p := newTestMuseProvider(newFakeOpenAIImageBatchStore(), testOpenAIImageBatchObjectPrefix)
	job := museSubmitJob(t, p, a, "imgbatch_proxy")
	_, err = p.Advance(t.Context(), job, a)
	require.NoError(t, err)
	_, err = p.Advance(t.Context(), job, a)
	require.NoError(t, err)
	require.Equal(t, int64(2), proxied.Load())
	require.Zero(t, direct.Load())
	require.Equal(t, proxy.URL, p.executor.(*museImageBatchExecutor).gateway.httpUpstream.(*museTestUpstream).lastProxy)
}

type museRuntimeStore struct {
	*fakeOpenAIImageBatchStore
	fakeBatchImageDeliveryStore
}

func (s *museRuntimeStore) Delete(ctx context.Context, keys []string) error {
	return s.fakeOpenAIImageBatchStore.Delete(ctx, keys)
}
func TestMuseRequiresAccountRuntime(t *testing.T) {
	p := NewMuseImagesBatchProvider(newFakeOpenAIImageBatchStore(), testOpenAIImageBatchObjectPrefix, nil)
	require.False(t, p.SubmissionAvailable())
	a := museTestAccount("http://unused")
	_, err := p.executor.(*museImageBatchExecutor).Execute(t.Context(), a, OpenAIImageBatchItemRequest{Model: "muse-image", Prompt: "image"}, func() error { t.Fatal("runtime unavailable must not claim attempt"); return nil })
	require.ErrorContains(t, err, "runtime is unavailable")
}
