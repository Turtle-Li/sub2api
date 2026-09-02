package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fixedEgressHandlerProxyRepo only implements the lookup that the real OAuth
// service reaches in these handler-boundary tests. Embedding the production
// interface makes any unexpected repository method fail loudly instead of
// silently widening the test double.
type fixedEgressHandlerProxyRepo struct {
	service.ProxyRepository
	proxy      *service.Proxy
	lookupHits int32
}

func (r *fixedEgressHandlerProxyRepo) GetByID(_ context.Context, id int64) (*service.Proxy, error) {
	atomic.AddInt32(&r.lookupHits, 1)
	if r.proxy == nil || r.proxy.ID != id {
		return nil, service.ErrProxyNotFound
	}
	return r.proxy, nil
}

type fixedEgressHandlerOAuthClient struct {
	exchangeHits int32
	response     *openai.TokenResponse
}

func (c *fixedEgressHandlerOAuthClient) ExchangeCode(context.Context, string, string, string, string, string) (*openai.TokenResponse, error) {
	atomic.AddInt32(&c.exchangeHits, 1)
	if c.response != nil {
		return c.response, nil
	}
	return nil, errors.New("OAuth exchange client must not be called")
}

func (*fixedEgressHandlerOAuthClient) RefreshToken(context.Context, string, string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (*fixedEgressHandlerOAuthClient) RefreshTokenWithClientID(context.Context, string, string, string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func invalidFixedEgressHandlerProxy(id int64) *service.Proxy {
	return &service.Proxy{
		ID:           id,
		Status:       service.StatusActive,
		Protocol:     "socks5h",
		Host:         "not-a-tailnet-ip.example",
		Port:         1080,
		FallbackMode: service.FallbackModeNone,
	}
}

func openAIHandlerSession(t *testing.T, svc *service.OpenAIOAuthService) (string, string) {
	return openAIHandlerSessionWithProxy(t, svc, nil)
}

func openAIHandlerSessionWithProxy(t *testing.T, svc *service.OpenAIOAuthService, proxyID *int64) (string, string) {
	t.Helper()
	result, err := svc.GenerateAuthURL(context.Background(), proxyID, "", service.PlatformOpenAI)
	require.NoError(t, err)
	parsed, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	state := parsed.Query().Get("state")
	require.NotEmpty(t, state)
	return result.SessionID, state
}

func TestOpenAICreateFromOAuthPersistsSessionProxyWhenRequestOmitsIt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const proxyID int64 = 703
	proxyRepo := &fixedEgressHandlerProxyRepo{proxy: &service.Proxy{
		ID:           proxyID,
		Status:       service.StatusActive,
		Protocol:     "socks5h",
		Host:         "100.70.128.60",
		Port:         1080,
		FallbackMode: service.FallbackModeNone,
	}}
	oauthClient := &fixedEgressHandlerOAuthClient{response: &openai.TokenResponse{
		AccessToken:  "at-test-token",
		RefreshToken: "rt-test-token",
		ExpiresIn:    3600,
	}}
	svc := service.NewOpenAIOAuthService(proxyRepo, oauthClient)
	defer svc.Stop()
	selectedProxyID := proxyID
	sessionID, state := openAIHandlerSessionWithProxy(t, svc, &selectedProxyID)
	adminService := newStubAdminService()
	router := gin.New()
	router.POST("/openai/create-from-oauth", NewOpenAIOAuthHandler(svc, adminService, nil, nil).CreateAccountFromOAuth)

	payload := map[string]any{
		"session_id": sessionID,
		"code":       "authorization-code",
		"state":      state,
		"name":       "fixed-egress-account",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/openai/create-from-oauth", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	adminService.mu.Lock()
	defer adminService.mu.Unlock()
	require.Len(t, adminService.createdAccounts, 1)
	require.NotNil(t, adminService.createdAccounts[0].ProxyID)
	require.Equal(t, proxyID, *adminService.createdAccounts[0].ProxyID)
}

func requireFixedEgressUnavailableResponse(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	var body struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, service.AccountProxyUnavailableReason, body.Reason)
}

func TestOpenAIOAuthExchangeBoundary_InvalidFixedEgressFailsBeforeOAuthClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const proxyID int64 = 701
	proxyRepo := &fixedEgressHandlerProxyRepo{proxy: invalidFixedEgressHandlerProxy(proxyID)}
	oauthClient := &fixedEgressHandlerOAuthClient{}
	svc := service.NewOpenAIOAuthService(proxyRepo, oauthClient)
	defer svc.Stop()
	sessionID, state := openAIHandlerSession(t, svc)

	router := gin.New()
	router.POST("/openai/exchange-code", NewOpenAIOAuthHandler(svc, newStubAdminService(), nil, nil).ExchangeCode)
	payload := map[string]any{
		"session_id": sessionID,
		"code":       "authorization-code",
		"state":      state,
		"proxy_id":   proxyID,
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/openai/exchange-code", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	requireFixedEgressUnavailableResponse(t, recorder)
	require.Equal(t, int32(1), atomic.LoadInt32(&proxyRepo.lookupHits))
	require.Zero(t, atomic.LoadInt32(&oauthClient.exchangeHits), "invalid fixed-egress input must not reach the OAuth client")
}

func TestOpenAICodexPATBoundary_InvalidFixedEgressFailsBeforeHTTPTransport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const proxyID int64 = 702
	proxyRepo := &fixedEgressHandlerProxyRepo{proxy: invalidFixedEgressHandlerProxy(proxyID)}
	svc := service.NewOpenAIOAuthService(proxyRepo, nil)
	defer svc.Stop()
	adminService := newStubAdminService()
	router := gin.New()
	router.POST("/openai/create-from-codex-pat", NewOpenAIOAuthHandler(svc, adminService, nil, nil).CreateAccountFromCodexPAT)

	var transportHits int32
	traceCtx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		GetConn:      func(string) { atomic.AddInt32(&transportHits, 1) },
		DNSStart:     func(httptrace.DNSStartInfo) { atomic.AddInt32(&transportHits, 1) },
		ConnectStart: func(string, string) { atomic.AddInt32(&transportHits, 1) },
		WroteRequest: func(httptrace.WroteRequestInfo) { atomic.AddInt32(&transportHits, 1) },
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/openai/create-from-codex-pat",
		bytes.NewBufferString(`{"access_token":"at-test-token","proxy_id":702}`),
	).WithContext(traceCtx)
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	requireFixedEgressUnavailableResponse(t, recorder)
	require.Equal(t, int32(1), atomic.LoadInt32(&proxyRepo.lookupHits))
	require.Zero(t, atomic.LoadInt32(&transportHits), "invalid fixed-egress input must not enter PAT HTTP transport")
	adminService.mu.Lock()
	defer adminService.mu.Unlock()
	require.Empty(t, adminService.createdAccounts, "invalid fixed-egress input must not create an account")
}
