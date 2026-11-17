package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func runEmptyCompatStream(t *testing.T, committed bool) error {
	t.Helper()
	svc := newAntigravityCompatService(config.GatewayConfig{
		MaxLineSize: defaultMaxLineSize, StreamDataIntervalTimeout: 30, StreamKeepaliveInterval: 0,
	}, nil)
	c, _ := newAntigravityCompatContext(http.MethodPost, "/v1/responses", nil)
	if committed {
		c.Writer.WriteHeaderNow()
		MarkResponseCommitted(c)
	}
	resp := &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}
	_, err := svc.handleAntigravityCompatStream(
		c, resp, time.Now(), "gemini-4-hydrogen-high",
		newAntigravityResponsesStreamAdapter("gemini-4-hydrogen-high"), "test",
	)
	return err
}

func TestAntigravityCompatEmptyStreamAfterResponseCommitted(t *testing.T) {
	err := runEmptyCompatStream(t, true)
	require.Error(t, err)
	require.True(t, IsAntigravityCompatCommittedNoContent(err), "got %v", err)
}

func TestAntigravityCompatEmptyStreamBeforeResponseCommittedCanFailOver(t *testing.T) {
	err := runEmptyCompatStream(t, false)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "got %v", err)
	require.False(t, IsAntigravityCompatCommittedNoContent(err))
}

func TestReleaseStickySessionAfterCommittedNoContent(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10)
	committed := &antigravityCompatCommittedNoContentError{msg: "empty Antigravity compatibility stream after keepalive"}

	t.Run("unbinds the session from the account that failed", func(t *testing.T) {
		cache := &stubGatewayCache{sessionBindings: map[string]int64{"sess": 5}}
		svc := &GatewayService{cache: cache}
		require.True(t, svc.ReleaseStickySessionAfterCommittedNoContent(ctx, &groupID, "sess", 5, committed))
		_, bound := cache.sessionBindings["sess"]
		require.False(t, bound)
	})

	t.Run("keeps a binding that already moved to another account", func(t *testing.T) {
		cache := &stubGatewayCache{sessionBindings: map[string]int64{"sess": 6}}
		svc := &GatewayService{cache: cache}
		require.False(t, svc.ReleaseStickySessionAfterCommittedNoContent(ctx, &groupID, "sess", 5, committed))
		require.Equal(t, int64(6), cache.sessionBindings["sess"])
	})

	t.Run("ignores other errors", func(t *testing.T) {
		cache := &stubGatewayCache{sessionBindings: map[string]int64{"sess": 5}}
		svc := &GatewayService{cache: cache}
		require.False(t, svc.ReleaseStickySessionAfterCommittedNoContent(ctx, &groupID, "sess", 5, errors.New("stream data interval timeout")))
		require.Equal(t, int64(5), cache.sessionBindings["sess"])
	})

	t.Run("no session hash is a no-op", func(t *testing.T) {
		cache := &stubGatewayCache{sessionBindings: map[string]int64{"sess": 5}}
		svc := &GatewayService{cache: cache}
		require.False(t, svc.ReleaseStickySessionAfterCommittedNoContent(ctx, &groupID, "", 5, committed))
		require.Equal(t, int64(5), cache.sessionBindings["sess"])
	})
}
