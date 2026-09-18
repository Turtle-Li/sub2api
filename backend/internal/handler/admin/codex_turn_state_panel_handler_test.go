package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCodexPanelAdapterAllowlist(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		allowed      bool
	}{
		{"GET", "/api/state", true}, {"GET", "/api/jobs/job-1", true}, {"POST", "/api/proxy-sources", true}, {"DELETE", "/api/accounts/7/gpt-test", true},
		{"DELETE", "/api/accounts/7/..", false}, {"DELETE", "/api/accounts/7/gpt..x", false}, {"GET", "/", false}, {"GET", "/api/../../settings", false}, {"POST", "/api/state", false}, {"PUT", "/api/accounts", false}, {"GET", "//remote.invalid", false},
	} {
		require.Equal(t, tc.allowed, allowedCodexPanelPath(tc.method, tc.path), tc.path)
	}
}
func TestCodexPanelAdapterDoesNotForwardCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Cookie"))
		require.Empty(t, r.Header.Get("Origin"))
		require.Equal(t, "1", r.Header.Get("X-CTSM-Panel"))
		require.Equal(t, "/api/probe", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"job_id":"sample"}`))
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	handler := &CodexTurnStatePanelHandler{target: target, client: upstream.Client()}
	router := gin.New()
	router.POST("/panel/*path", handler.Proxy)
	request := httptest.NewRequest("POST", "/panel/api/probe", strings.NewReader(`{"account_id":7}`))
	request.Header.Set("Authorization", "Bearer mock-admin")
	request.Header.Set("Cookie", "session=mock")
	request.Header.Set("Origin", "https://app.invalid")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, 202, recorder.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"job_id":"sample"}}`, recorder.Body.String())
}
func TestCodexPanelAdapterFailsClosedOnRedirectAndRawErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"mock-secret-must-not-leak"}`))
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	handler := &CodexTurnStatePanelHandler{target: target, client: upstream.Client()}
	router := gin.New()
	router.GET("/panel/*path", handler.Proxy)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("GET", "/panel/api/state", nil))
	require.Equal(t, 502, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "mock-secret")
}
