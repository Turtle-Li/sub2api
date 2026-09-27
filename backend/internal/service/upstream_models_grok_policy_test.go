//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBuildGrokUpstreamModelsRequestUsesHTTPRelayPolicy(t *testing.T) {
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "grok-key",
			"base_url": "http://relay.example.test/v1",
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Security.URLAllowlist.UpstreamHosts = []string{"relay.example.test"}
	svc := &AccountTestService{cfg: cfg}

	req, err := svc.buildGrokUpstreamModelsRequest(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, req.Method)
	require.Equal(t, "http://relay.example.test/v1/models", req.URL.String())
	require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
	allowPrivate, forced := HTTPUpstreamResolvedIPValidation(req.Context())
	require.False(t, allowPrivate)
	require.True(t, forced)
}

func TestBuildGrokUpstreamModelsRequestRejectsHTTPRelayWithoutEndpointAllowlist(t *testing.T) {
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "grok-key",
			"base_url": "http://relay.example.test/v1",
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := &AccountTestService{cfg: cfg}

	_, err := svc.buildGrokUpstreamModelsRequest(context.Background(), account)
	require.EqualError(t, err, "Invalid Grok base URL: base URL rejected by URL security policy")
}
