package attachment_gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoopbackUpstreamLimitRejectsOriginalAndAcceptsOptimized(t *testing.T) {
	const upstreamBodyLimit = 1024 * 1024
	var (
		receivedMu    sync.Mutex
		receivedBytes []int
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(io.LimitReader(request.Body, upstreamBodyLimit+1))
		if err != nil {
			http.Error(writer, "read request", http.StatusInternalServerError)
			return
		}
		receivedMu.Lock()
		receivedBytes = append(receivedBytes, len(body))
		receivedMu.Unlock()
		if len(body) > upstreamBodyLimit {
			writer.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	source := makeBenchmarkPNG(t, 1100, 800, 91)
	original := makeResponsesPayload(t, []string{dataURL("image/png", source)}, 0)
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = 1
	gateway, err := New(config)
	require.NoError(t, err)

	coldStarted := time.Now()
	cold := gateway.Optimize(context.Background(), original)
	coldDuration := time.Since(coldStarted)
	warmStarted := time.Now()
	warm := gateway.Optimize(context.Background(), original)
	warmDuration := time.Since(warmStarted)

	require.Greater(t, len(original), upstreamBodyLimit)
	require.Less(t, len(cold.Body), upstreamBodyLimit)
	require.Equal(t, cold.Body, warm.Body)
	require.True(t, warm.Metrics.CacheHit)

	originalResponse, err := upstream.Client().Post(upstream.URL+"/v1/responses", "application/json", bytes.NewReader(original))
	require.NoError(t, err)
	require.NoError(t, originalResponse.Body.Close())
	require.Equal(t, http.StatusRequestEntityTooLarge, originalResponse.StatusCode)

	optimizedResponse, err := upstream.Client().Post(upstream.URL+"/v1/responses", "application/json", bytes.NewReader(cold.Body))
	require.NoError(t, err)
	require.NoError(t, optimizedResponse.Body.Close())
	require.Equal(t, http.StatusNoContent, optimizedResponse.StatusCode)

	warmResponse, err := upstream.Client().Post(upstream.URL+"/v1/responses", "application/json", bytes.NewReader(warm.Body))
	require.NoError(t, err)
	require.NoError(t, warmResponse.Body.Close())
	require.Equal(t, http.StatusNoContent, warmResponse.StatusCode)

	receivedMu.Lock()
	gotReceivedBytes := append([]int(nil), receivedBytes...)
	receivedMu.Unlock()
	require.Equal(t, []int{upstreamBodyLimit + 1, len(cold.Body), len(warm.Body)}, gotReceivedBytes)
	t.Logf(
		"loopback upstream limit: body=%d->%d saved=%.2f%% cold=%s warm=%s cache_hit=%t",
		len(original),
		len(cold.Body),
		(1-float64(len(cold.Body))/float64(len(original)))*100,
		coldDuration,
		warmDuration,
		warm.Metrics.CacheHit,
	)
}
