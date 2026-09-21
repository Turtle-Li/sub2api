package admin

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const codexPanelTestSourceID = "0123456789abcdef0123456789abcdef"

type codexPanelRoundTripper func(*http.Request) (*http.Response, error)

func (f codexPanelRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func writeCodexPanelToken(t *testing.T, token string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "panel-token")
	require.NoError(t, os.WriteFile(path, []byte(token), 0o600))
	return path
}

func TestCodexPanelAdapterAllowlist(t *testing.T) {
	for _, tc := range []struct {
		method  string
		path    string
		allowed bool
	}{
		{http.MethodGet, "/api/state", true},
		{http.MethodGet, "/api/accounts/7/models", true},
		{http.MethodGet, "/api/jobs/job-1", true},
		{http.MethodPost, "/api/proxy-sources", true},
		{http.MethodPost, "/api/proxy-sources/" + codexPanelTestSourceID + "/enabled", true},
		{http.MethodDelete, "/api/accounts/7/gpt-test", true},
		{http.MethodPost, "/api/proxy-sources/0123456789abcdef0123456789abcde/enabled", false},
		{http.MethodPost, "/api/proxy-sources/0123456789abcdef0123456789abcdef0/enabled", false},
		{http.MethodPost, "/api/proxy-sources/0123456789abcdef0123456789abcdef/enable", false},
		{http.MethodPost, "/api/proxy-sources/0123456789ABCDEF0123456789ABCDEF/enabled", false},
		{http.MethodGet, "/api/proxy-sources/" + codexPanelTestSourceID + "/enabled", false},
		{http.MethodDelete, "/api/accounts/7/..", false},
		{http.MethodDelete, "/api/accounts/7/gpt..x", false},
		{http.MethodGet, "/", false},
		{http.MethodGet, "/api/../../settings", false},
		{http.MethodPost, "/api/state", false},
		{http.MethodGet, "/api/settings", true},
		{http.MethodPost, "/api/settings", true},
		{http.MethodDelete, "/api/settings", false},
		{http.MethodPut, "/api/accounts", false},
		{http.MethodGet, "//remote.invalid", false},
	} {
		require.Equal(t, tc.allowed, allowedCodexPanelPath(tc.method, tc.path), tc.method+" "+tc.path)
	}
}

func TestCodexPanelAdapterForwardsEnabledToggleWithoutCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Cookie"))
		require.Empty(t, r.Header.Get("Origin"))
		require.Equal(t, "1", r.Header.Get("X-CTSM-Panel"))
		require.Equal(t, "/api/proxy-sources/"+codexPanelTestSourceID+"/enabled", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"enabled":true}`, string(body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, err = w.Write([]byte(`{"id":"` + codexPanelTestSourceID + `","enabled":true}`))
		require.NoError(t, err)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	handler := &CodexTurnStatePanelHandler{target: target, client: upstream.Client()}
	router := gin.New()
	router.POST("/panel/*path", handler.Proxy)
	request := httptest.NewRequest(http.MethodPost, "/panel/api/proxy-sources/"+codexPanelTestSourceID+"/enabled", strings.NewReader(`{"enabled":true}`))
	request.Header.Set("Authorization", "Bearer mock-admin")
	request.Header.Set("Cookie", "session=mock")
	request.Header.Set("Origin", "https://app.invalid")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"id":"`+codexPanelTestSourceID+`","enabled":true}}`, recorder.Body.String())
}

func TestCodexPanelAdapterInjectsTokenFromFileOnEveryRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	seenTokens := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Cookie"))
		require.Empty(t, r.Header.Get("Origin"))
		seenTokens <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"state":"ok"}`))
		require.NoError(t, err)
	}))
	defer upstream.Close()

	tokenFile := writeCodexPanelToken(t, "server-token-one\n")
	t.Setenv("CODEX_TURN_STATE_PANEL_URL", upstream.URL)
	t.Setenv("CODEX_TURN_STATE_PANEL_TOKEN_FILE", tokenFile)
	handler := NewCodexTurnStatePanelHandler()
	router := gin.New()
	router.GET("/panel/*path", handler.Proxy)

	for _, tc := range []struct {
		contents string
		expected string
	}{
		{contents: "server-token-one\n", expected: "server-token-one"},
		{contents: "server-token-two", expected: "server-token-two"},
	} {
		require.NoError(t, os.WriteFile(tokenFile, []byte(tc.contents), 0o600))
		request := httptest.NewRequest(http.MethodGet, "/panel/api/state", nil)
		request.Header.Set("Authorization", "Bearer browser-admin-token")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, "Bearer "+tc.expected, <-seenTokens)
	}
}

func TestCodexPanelAdapterFailsClosedWhenTokenFileCannotBeRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var upstreamRequests int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&upstreamRequests, 1)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"state":"unexpected"}`))
		require.NoError(t, err)
	}))
	defer upstream.Close()

	t.Setenv("CODEX_TURN_STATE_PANEL_URL", upstream.URL)
	t.Setenv("CODEX_TURN_STATE_PANEL_TOKEN_FILE", filepath.Join(t.TempDir(), "missing-token"))
	handler := NewCodexTurnStatePanelHandler()
	router := gin.New()
	router.GET("/panel/*path", handler.Proxy)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panel/api/state", nil))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Zero(t, atomic.LoadInt32(&upstreamRequests))
}

func TestCodexPanelAdapterRejectsPublicTargetWhenTokenConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var transportCalls int32
	handler := &CodexTurnStatePanelHandler{
		target:    &url.URL{Scheme: "http", Host: "198.51.100.7"},
		tokenFile: writeCodexPanelToken(t, "server-token"),
		client: &http.Client{Transport: codexPanelRoundTripper(func(*http.Request) (*http.Response, error) {
			atomic.AddInt32(&transportCalls, 1)
			return nil, errors.New("public target must not be called")
		})},
	}
	router := gin.New()
	router.GET("/panel/*path", handler.Proxy)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panel/api/state", nil))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Zero(t, atomic.LoadInt32(&transportCalls))
}

func TestCodexPanelAdapterDoesNotFollowRedirectWithToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var redirectTargetRequests int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&redirectTargetRequests, 1)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"state":"unexpected"}`))
		require.NoError(t, err)
	}))
	defer redirectTarget.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer server-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", redirectTarget.URL+"/captured-token")
		w.WriteHeader(http.StatusFound)
		_, err := w.Write([]byte(`{"redirect":true}`))
		require.NoError(t, err)
	}))
	defer upstream.Close()

	target, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	handler := &CodexTurnStatePanelHandler{
		target:    target,
		tokenFile: writeCodexPanelToken(t, "server-token"),
		client:    upstream.Client(),
	}
	router := gin.New()
	router.GET("/panel/*path", handler.Proxy)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panel/api/state", nil))

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Zero(t, atomic.LoadInt32(&redirectTargetRequests))
}

func TestReadCodexPanelTokenRejectsEmptyControlAndOversizedValues(t *testing.T) {
	for _, token := range []string{
		"\n",
		"token\nsecond-line",
		strings.Repeat("a", codexPanelTokenMaxBytes+1),
	} {
		path := writeCodexPanelToken(t, token)
		_, err := readCodexPanelToken(path)
		require.ErrorIs(t, err, errInvalidCodexPanelToken)
	}
}

func TestCodexPanelAdapterFailsClosedOnRawErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, err := w.Write([]byte(`{"error":"mock-secret-must-not-leak"}`))
		require.NoError(t, err)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	handler := &CodexTurnStatePanelHandler{target: target, client: upstream.Client()}
	router := gin.New()
	router.GET("/panel/*path", handler.Proxy)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panel/api/state", nil))
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "mock-secret")
}
