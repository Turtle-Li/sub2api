package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

type openaiOAuthClientStateStub struct {
	exchangeCalled int32
	lastClientID   string
	lastProxyURL   string
}

// openaiOAuthStateProxyRepoStub supplies only the lookup used by these tests.
// The embedded interface keeps the stub in sync with the production dependency.
type openaiOAuthStateProxyRepoStub struct {
	ProxyRepository
	getByIDFunc func(context.Context, int64) (*Proxy, error)
}

func (s *openaiOAuthStateProxyRepoStub) GetByID(ctx context.Context, id int64) (*Proxy, error) {
	return s.getByIDFunc(ctx, id)
}

func (s *openaiOAuthClientStateStub) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string) (*openai.TokenResponse, error) {
	atomic.AddInt32(&s.exchangeCalled, 1)
	s.lastClientID = clientID
	s.lastProxyURL = proxyURL
	return &openai.TokenResponse{
		AccessToken:  "at",
		RefreshToken: "rt",
		ExpiresIn:    3600,
	}, nil
}

func (s *openaiOAuthClientStateStub) RefreshToken(ctx context.Context, refreshToken, proxyURL string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *openaiOAuthClientStateStub) RefreshTokenWithClientID(ctx context.Context, refreshToken, proxyURL string, clientID string) (*openai.TokenResponse, error) {
	return s.RefreshToken(ctx, refreshToken, proxyURL)
}

func TestOpenAIOAuthService_ExchangeCode_StateRequired(t *testing.T) {
	client := &openaiOAuthClientStateStub{}
	svc := NewOpenAIOAuthService(nil, client)
	defer svc.Stop()

	svc.sessionStore.Set("sid", &openai.OAuthSession{
		State:        "expected-state",
		CodeVerifier: "verifier",
		RedirectURI:  openai.DefaultRedirectURI,
		CreatedAt:    time.Now(),
	})

	_, err := svc.ExchangeCode(context.Background(), &OpenAIExchangeCodeInput{
		SessionID: "sid",
		Code:      "auth-code",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "oauth state is required")
	require.Equal(t, int32(0), atomic.LoadInt32(&client.exchangeCalled))
}

func TestOpenAIOAuthService_ExchangeCode_StateMismatch(t *testing.T) {
	client := &openaiOAuthClientStateStub{}
	svc := NewOpenAIOAuthService(nil, client)
	defer svc.Stop()

	svc.sessionStore.Set("sid", &openai.OAuthSession{
		State:        "expected-state",
		CodeVerifier: "verifier",
		RedirectURI:  openai.DefaultRedirectURI,
		CreatedAt:    time.Now(),
	})

	_, err := svc.ExchangeCode(context.Background(), &OpenAIExchangeCodeInput{
		SessionID: "sid",
		Code:      "auth-code",
		State:     "wrong-state",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid oauth state")
	require.Equal(t, int32(0), atomic.LoadInt32(&client.exchangeCalled))
}

func TestOpenAIOAuthService_ExchangeCode_StateMatch(t *testing.T) {
	client := &openaiOAuthClientStateStub{}
	svc := NewOpenAIOAuthService(nil, client)
	defer svc.Stop()

	svc.sessionStore.Set("sid", &openai.OAuthSession{
		State:        "expected-state",
		CodeVerifier: "verifier",
		RedirectURI:  openai.DefaultRedirectURI,
		CreatedAt:    time.Now(),
	})

	info, err := svc.ExchangeCode(context.Background(), &OpenAIExchangeCodeInput{
		SessionID: "sid",
		Code:      "auth-code",
		State:     "expected-state",
	})
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Equal(t, "at", info.AccessToken)
	require.Equal(t, openai.ClientID, info.ClientID)
	require.Equal(t, openai.ClientID, client.lastClientID)
	require.Equal(t, int32(1), atomic.LoadInt32(&client.exchangeCalled))

	_, ok := svc.sessionStore.Get("sid")
	require.False(t, ok)
}

func validOpenAIOAuthFixedEgressProxy(id int64) *Proxy {
	return &Proxy{
		ID:           id,
		Status:       StatusActive,
		Protocol:     "socks5h",
		Host:         "100.70.128.60",
		Port:         1080,
		FallbackMode: FallbackModeNone,
	}
}

func TestOpenAIOAuthService_ExchangeCode_RevalidatesSessionProxy(t *testing.T) {
	const proxyID int64 = 901
	selectedProxyID := proxyID
	proxy := validOpenAIOAuthFixedEgressProxy(proxyID)
	repo := &openaiOAuthStateProxyRepoStub{getByIDFunc: func(context.Context, int64) (*Proxy, error) {
		return proxy, nil
	}}
	client := &openaiOAuthClientStateStub{}
	svc := NewOpenAIOAuthService(repo, client)
	defer svc.Stop()

	result, err := svc.GenerateAuthURL(context.Background(), &selectedProxyID, "", PlatformOpenAI)
	require.NoError(t, err)
	session, ok := svc.sessionStore.Get(result.SessionID)
	require.True(t, ok)
	require.NotNil(t, session.ProxyID)
	require.Equal(t, proxyID, *session.ProxyID)

	proxy.Status = StatusDisabled
	_, err = svc.ExchangeCode(context.Background(), &OpenAIExchangeCodeInput{
		SessionID: result.SessionID,
		Code:      "auth-code",
		State:     session.State,
	})
	require.ErrorIs(t, err, ErrAccountProxyUnavailable)
	require.Zero(t, atomic.LoadInt32(&client.exchangeCalled), "a stale session URL must not reach the OAuth client")
}

func TestOpenAIOAuthService_ExchangeCode_RejectsProxySwitchAndReturnsBoundIdentity(t *testing.T) {
	const proxyID int64 = 902
	selectedProxyID := proxyID
	proxy := validOpenAIOAuthFixedEgressProxy(proxyID)
	repo := &openaiOAuthStateProxyRepoStub{getByIDFunc: func(_ context.Context, id int64) (*Proxy, error) {
		require.Equal(t, proxyID, id)
		return proxy, nil
	}}
	client := &openaiOAuthClientStateStub{}
	svc := NewOpenAIOAuthService(repo, client)
	defer svc.Stop()

	result, err := svc.GenerateAuthURL(context.Background(), &selectedProxyID, "", PlatformOpenAI)
	require.NoError(t, err)
	session, ok := svc.sessionStore.Get(result.SessionID)
	require.True(t, ok)
	otherProxyID := proxyID + 1
	_, err = svc.ExchangeCode(context.Background(), &OpenAIExchangeCodeInput{
		SessionID: result.SessionID,
		Code:      "auth-code",
		State:     session.State,
		ProxyID:   &otherProxyID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "proxy_id must match")
	require.Zero(t, atomic.LoadInt32(&client.exchangeCalled))

	info, err := svc.ExchangeCode(context.Background(), &OpenAIExchangeCodeInput{
		SessionID: result.SessionID,
		Code:      "auth-code",
		State:     session.State,
	})
	require.NoError(t, err)
	require.NotNil(t, info.ProxyID)
	require.Equal(t, proxyID, *info.ProxyID)
	require.Equal(t, "socks5h://100.70.128.60:1080", client.lastProxyURL)
}
