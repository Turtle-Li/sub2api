//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestDoGrokNativeResponsesJSON_EnforcesConfiguredURLPolicy(t *testing.T) {
	account := healthyGrokOAuthGatewayTestAccount(9903, "access-token")
	account.Credentials["base_url"] = "https://relay.example.test/v1"

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = true
	cfg.Security.URLAllowlist.UpstreamHosts = []string{"allowed.example.test"}

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte("{\"ok\":true}"))),
	}}
	svc := &GatewayService{cfg: cfg, httpUpstream: upstream}

	_, err := svc.DoGrokNativeResponsesJSON(context.Background(), account, []byte("{\"model\":\"grok\",\"input\":\"search\"}"))
	require.EqualError(t, err, "invalid base url: base URL rejected by URL security policy")
	require.Empty(t, upstream.requests)
}
