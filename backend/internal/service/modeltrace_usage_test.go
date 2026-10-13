package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelTraceRecordsActualRequestPathAndFirstToken(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/backend-api/codex/responses", "/custom/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			collector := &modelTraceUsageCollector{startedAt: time.Now().Add(-time.Second)}
			ctx := context.WithValue(context.Background(), modelTraceUsageKey{}, collector)
			req, err := newAccountTestRequest(ctx, http.MethodPost, "https://example.com"+path+"?key=secret", nil)
			require.NoError(t, err)
			require.Equal(t, path, collector.endpoint)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req
			svc := &AccountTestService{}
			svc.sendEvent(c, TestEvent{Type: "status", Text: "waiting"})
			require.Nil(t, collector.firstTokenMs)
			svc.sendEvent(c, TestEvent{Type: "content", Text: "1"})
			require.NotNil(t, collector.firstTokenMs)
			first := *collector.firstTokenMs
			svc.sendEvent(c, TestEvent{Type: "content", Text: "2"})
			require.Equal(t, first, *collector.firstTokenMs)
		})
	}
}

func TestModelTraceFailedAttemptsAreRecorded(t *testing.T) {
	// With no upstream, every attempted sample must remain inspectable, rather
	// than only retaining the final aggregate error.
	svc := NewModelTraceService(nil, &modelTraceTestSettings{}, nil, nil)
	record := svc.executeProbe(context.Background(), &Account{ID: 31}, "test")
	require.Equal(t, "Unknown", record.DetectedModel)
	require.NotEmpty(t, record.Error)
	require.Len(t, record.Requests, 4)
	for _, request := range record.Requests {
		require.NotEmpty(t, request.ID)
		require.Contains(t, request.Error, "accountTestService is not available")
		require.False(t, request.ValidSample)
		require.False(t, request.UsageReported)
	}
}
