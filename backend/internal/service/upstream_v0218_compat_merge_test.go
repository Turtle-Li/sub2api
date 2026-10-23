//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// v0218ObservedCancelContext lets the test wait until the handler has received
// the cancellation signal before releasing more upstream frames.
type v0218ObservedCancelContext struct {
	context.Context
	done     chan struct{}
	canceled chan struct{}
	observed chan struct{}
	stopped  chan struct{}
}

func newV0218ObservedCancelContext() *v0218ObservedCancelContext {
	return &v0218ObservedCancelContext{
		Context:  context.Background(),
		done:     make(chan struct{}),
		canceled: make(chan struct{}),
		observed: make(chan struct{}),
		stopped:  make(chan struct{}),
	}
}

func (ctx *v0218ObservedCancelContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *v0218ObservedCancelContext) Err() error {
	select {
	case <-ctx.canceled:
		return context.Canceled
	default:
		return nil
	}
}

func (ctx *v0218ObservedCancelContext) cancel() {
	close(ctx.canceled)
	go func() {
		select {
		case ctx.done <- struct{}{}:
			close(ctx.observed)
		case <-ctx.stopped:
		}
		close(ctx.done)
	}()
}

func (ctx *v0218ObservedCancelContext) stop() {
	close(ctx.stopped)
}

func v0218DataOnlyCCStream(multiline bool) string {
	var stream strings.Builder
	for _, frame := range claudeCompatTestFrames()[:5] {
		data := frame[1]
		if multiline {
			data = strings.Replace(data, ",", ",\ndata: ", 1)
		}
		stream.WriteString("data: ")
		stream.WriteString(data)
		stream.WriteString("\n\n")
	}
	return stream.String()
}

func TestHandleCCStreamingFromAnthropic_DataOnlyAndMultilineFramesPreserveUsageAndTerminate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name      string
		multiline bool
	}{
		{name: "data_only"},
		{name: "multiline_data", multiline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

			result, err := newStreamingResponseTestGatewayService().handleCCStreamingFromAnthropic(
				&http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(v0218DataOnlyCCStream(tc.multiline)))},
				c, "claude-test", "claude-test", nil, time.Now(),
			)

			require.NoError(t, err)
			require.False(t, result.ClientDisconnect)
			require.Equal(t, 12, result.Usage.InputTokens)
			require.Equal(t, 7, result.Usage.OutputTokens)
			require.Equal(t, 3, result.Usage.CacheReadInputTokens)
			require.Contains(t, rec.Body.String(), `"content":"OK"`)
			require.Equal(t, 1, strings.Count(rec.Body.String(), "data: [DONE]"))
		})
	}
}

func TestHandleCCStreamingFromAnthropic_CanceledDownstreamDrainsUsageWithoutWriting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := newV0218ObservedCancelContext()
	t.Cleanup(ctx.stop)

	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pw.Close()
		_ = pr.Close()
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	resultCh := make(chan compatStreamOutcome, 1)
	go func() {
		result, err := newStreamingResponseTestGatewayService().handleCCStreamingFromAnthropic(
			&http.Response{Header: http.Header{}, Body: pr}, c, "claude-test", "claude-test", nil, time.Now(),
		)
		resultCh <- compatStreamOutcome{result: result, err: err}
	}()

	ctx.cancel()
	select {
	case <-ctx.observed:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not observe client cancellation")
	}

	_, err := io.WriteString(pw, v0218DataOnlyCCStream(true))
	require.NoError(t, err)
	require.NoError(t, pw.Close())

	select {
	case got := <-resultCh:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
		require.True(t, got.result.ClientDisconnect)
		require.Equal(t, 12, got.result.Usage.InputTokens)
		require.Equal(t, 7, got.result.Usage.OutputTokens)
		require.Equal(t, 3, got.result.Usage.CacheReadInputTokens)
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not finish draining upstream usage")
	}

	require.Empty(t, rec.Body.String(), "client cancellation must suppress downstream output")
}
