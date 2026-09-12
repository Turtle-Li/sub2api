package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTrafficAdmissionDrainsWithoutInterruptingAdmittedRequest(t *testing.T) {
	state := filepath.Join(t.TempDir(), "traffic-state")
	require.NoError(t, os.WriteFile(state, []byte("accepting\n"), 0600))
	s := newHealthService(nil, nil, "", state, time.Second)
	r := gin.New()
	r.Use(s.TrafficAdmission())
	entered, finish, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	r.POST("/work", func(c *gin.Context) { close(entered); <-finish; c.Status(http.StatusOK) })
	r.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	first := httptest.NewRecorder()
	go func() { defer close(done); r.ServeHTTP(first, httptest.NewRequest("POST", "/work", nil)) }()
	<-entered
	require.EqualValues(t, 1, s.InFlightRequests())
	require.NoError(t, os.WriteFile(state, []byte("draining\n"), 0600))
	second := httptest.NewRecorder()
	r.ServeHTTP(second, httptest.NewRequest("POST", "/work", nil))
	require.Equal(t, http.StatusServiceUnavailable, second.Code)
	require.Equal(t, "30", second.Header().Get("Retry-After"))
	require.EqualValues(t, 1, s.InFlightRequests())
	health := httptest.NewRecorder()
	r.ServeHTTP(health, httptest.NewRequest("GET", "/health", nil))
	require.Equal(t, http.StatusOK, health.Code)
	close(finish)
	<-done
	require.Equal(t, http.StatusOK, first.Code)
	require.Zero(t, s.InFlightRequests())
	require.NoError(t, os.Remove(state))
	missing := httptest.NewRecorder()
	r.ServeHTTP(missing, httptest.NewRequest("POST", "/work", nil))
	require.Equal(t, http.StatusServiceUnavailable, missing.Code)
}

func TestTrafficAdmissionCanaryRequiresSocketLoopbackAndMonitorToken(t *testing.T) {
	token := filepath.Join(t.TempDir(), "monitor-token")
	require.NoError(t, os.WriteFile(token, []byte("local-canary-token"), 0600))
	s := newHealthService(nil, nil, token, "", time.Second)
	s.SetAccepting(false)
	r := gin.New()
	r.Use(s.TrafficAdmission())
	r.POST("/work", func(c *gin.Context) { c.Status(http.StatusAccepted) })
	for _, tt := range []struct {
		addr, token string
		status      int
	}{
		{"127.0.0.1:1234", "local-canary-token", http.StatusAccepted},
		{"[::1]:1234", "local-canary-token", http.StatusAccepted},
		{"127.0.0.1:1234", "", http.StatusServiceUnavailable},
		{"192.0.2.1:1234", "local-canary-token", http.StatusServiceUnavailable},
	} {
		req := httptest.NewRequest("POST", "/work", nil)
		req.RemoteAddr = tt.addr
		req.Header.Set("X-Monitor-Token", tt.token)
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		require.Equal(t, tt.status, out.Code)
	}
}
