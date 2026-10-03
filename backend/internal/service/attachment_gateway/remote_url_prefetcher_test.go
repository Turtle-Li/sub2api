package attachment_gateway

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type remoteImageRoundTripper func(*http.Request) (*http.Response, error)

func (f remoteImageRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func remotePNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestRemoteURLPrefetchRewritesHTTPSImageOnly(t *testing.T) {
	imageBytes := remotePNG(t)
	client := &http.Client{Transport: remoteImageRoundTripper(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "https://images.example.test/a.png", req.URL.String())
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(imageBytes)),
			Header:     http.Header{"Content-Type": []string{"image/png"}},
			Request:    req,
		}, nil
	})}
	prefetcher, err := newRemoteURLPrefetcherForTest(RemoteURLPrefetchConfig{
		Enabled: true, MaxImageBytes: 1 << 20, MaxPixels: 100, MaxImagesPerRequest: 2,
		MaxConcurrent: 1, Timeout: time.Second, CacheDir: t.TempDir(),
	}, client)
	require.NoError(t, err)
	body := []byte(`{"input":[{"type":"input_image","image_url":"https://images.example.test/a.png"},{"type":"input_text","text":"https://images.example.test/ignore.png"}]}`)
	result := prefetcher.Prefetch(context.Background(), body)
	require.Equal(t, 1, result.Metrics.ImageCount)
	require.Equal(t, 1, result.Metrics.DownloadedCount)
	require.Equal(t, 1, result.Metrics.RewrittenCount)
	require.Contains(t, string(result.Body), `"image_url":"data:image/png;base64,`)
	require.Contains(t, string(result.Body), `https://images.example.test/ignore.png`)
}

func TestRemoteURLPrefetchFailureKeepsOriginalURL(t *testing.T) {
	client := &http.Client{Transport: remoteImageRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusGatewayTimeout, Body: http.NoBody, Header: make(http.Header), Request: req}, nil
	})}
	prefetcher, err := newRemoteURLPrefetcherForTest(RemoteURLPrefetchConfig{Enabled: true, MaxImagesPerRequest: 1, CacheDir: t.TempDir()}, client)
	require.NoError(t, err)
	body := []byte(`{"input":[{"type":"input_image","image_url":"https://images.example.test/slow.png"}]}`)
	result := prefetcher.Prefetch(context.Background(), body)
	require.Equal(t, body, result.Body)
	require.Equal(t, 1, result.Metrics.ImageCount)
	require.Equal(t, 1, result.Metrics.Errors)
}

func TestRemoteURLPrefetchRetriesTransientCOSOrR2Failure(t *testing.T) {
	imageBytes := remotePNG(t)
	requests := 0
	client := &http.Client{Transport: remoteImageRoundTripper(func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody, Header: make(http.Header), Request: req}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(imageBytes)),
			Header:     http.Header{"Content-Type": []string{"image/png"}},
			Request:    req,
		}, nil
	})}
	prefetcher, err := newRemoteURLPrefetcherForTest(RemoteURLPrefetchConfig{
		Enabled: true, MaxImageBytes: 1 << 20, MaxPixels: 100, MaxImagesPerRequest: 1,
		MaxConcurrent: 1, Timeout: time.Second, RetryCount: 2, CacheDir: t.TempDir(),
	}, client)
	require.NoError(t, err)

	result := prefetcher.Prefetch(context.Background(), []byte(`{"input":[{"type":"input_image","image_url":"https://bucket.r2.example.test/cos.png"}]}`))
	require.Equal(t, 2, requests)
	require.Equal(t, 1, result.Metrics.RetryCount)
	require.Equal(t, 1, result.Metrics.DownloadedCount)
	require.Equal(t, 1, result.Metrics.RewrittenCount)
	require.Contains(t, string(result.Body), `"image_url":"data:image/png;base64,`)
}

func TestRemoteURLPrefetchExhaustedRetriesKeepsOriginalURL(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: remoteImageRoundTripper(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody, Header: make(http.Header), Request: req}, nil
	})}
	prefetcher, err := newRemoteURLPrefetcherForTest(RemoteURLPrefetchConfig{
		Enabled: true, MaxImagesPerRequest: 1, MaxConcurrent: 1, Timeout: time.Second,
		RetryCount: 2, CacheDir: t.TempDir(),
	}, client)
	require.NoError(t, err)

	body := []byte(`{"input":[{"type":"input_image","image_url":"https://bucket.cos.example.test/temporary.png"}]}`)
	result := prefetcher.Prefetch(context.Background(), body)
	require.Equal(t, 3, requests)
	require.Equal(t, 2, result.Metrics.RetryCount)
	require.Equal(t, 1, result.Metrics.Errors)
	require.Equal(t, body, result.Body)
}

func TestRemoteURLPrefetchUsesPersistentURLCache(t *testing.T) {
	imageBytes := remotePNG(t)
	requests := 0
	client := &http.Client{Transport: remoteImageRoundTripper(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(imageBytes)),
			Header:     http.Header{"Content-Type": []string{"image/png"}},
			Request:    req,
		}, nil
	})}
	prefetcher, err := newRemoteURLPrefetcherForTest(RemoteURLPrefetchConfig{
		Enabled: true, MaxImageBytes: 1 << 20, MaxPixels: 100, MaxImagesPerRequest: 1,
		MaxConcurrent: 1, Timeout: time.Second, CacheDir: t.TempDir(), CacheTTL: time.Hour,
	}, client)
	require.NoError(t, err)
	body := []byte(`{"input":[{"type":"input_image","image_url":"https://images.example.test/cached.png"}]}`)
	first := prefetcher.Prefetch(context.Background(), body)
	second := prefetcher.Prefetch(context.Background(), body)
	require.Equal(t, 1, requests)
	require.Equal(t, 1, first.Metrics.CacheMisses)
	require.Equal(t, 1, second.Metrics.CacheHits)
	require.Equal(t, 0, second.Metrics.DownloadedCount)
	require.Equal(t, first.Body, second.Body)
}

func TestRemoteURLPrefetchRealObjectStoreImage(t *testing.T) {
	rawURL := strings.TrimSpace(os.Getenv("ATTACHMENT_GATEWAY_REMOTE_IMAGE_URL"))
	if rawURL == "" {
		t.Skip("set ATTACHMENT_GATEWAY_REMOTE_IMAGE_URL for a live COS/R2 smoke test")
	}
	prefetcher, err := NewRemoteURLPrefetcher(RemoteURLPrefetchConfig{
		Enabled: true, MaxImageBytes: 8 << 20, MaxPixels: 50_000_000,
		MaxImagesPerRequest: 1, MaxConcurrent: 1, Timeout: 10 * time.Second,
		RetryCount: 2, CacheDir: t.TempDir(), CacheTTL: time.Hour,
	})
	require.NoError(t, err)
	body := []byte(`{"input":[{"type":"input_image","image_url":"` + rawURL + `"}]}`)
	first := prefetcher.Prefetch(context.Background(), body)
	require.Equal(t, 1, first.Metrics.ImageCount)
	require.Equal(t, 1, first.Metrics.RewrittenCount)
	require.Equal(t, 0, first.Metrics.Errors)
	require.Contains(t, string(first.Body), `"image_url":"data:image/`)
	second := prefetcher.Prefetch(context.Background(), body)
	require.Equal(t, 1, second.Metrics.CacheHits)
	require.Equal(t, 0, second.Metrics.DownloadedCount)
	require.Equal(t, first.Body, second.Body)
}

func TestRemoteURLPrefetchRejectsNonHTTPSAndPrivateAddress(t *testing.T) {
	require.False(t, isRemoteImageURL("http://images.example.test/a.png"))
	require.False(t, isRemoteImageURL("https://127.0.0.1/a.png"))
	require.False(t, isRemoteImageURL("https://user:pass@images.example.test/a.png"))
	require.True(t, isRemoteImageURL("https://images.example.test/a.png"))
	server := httptest.NewServer(nil)
	defer server.Close()
	require.False(t, isRemoteImageURL(server.URL+"/a.png"))
}
