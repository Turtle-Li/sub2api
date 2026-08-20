package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cnProviderAccountTestRepo struct {
	AccountRepository
	account *Account
}

func (r *cnProviderAccountTestRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	return r.account, nil
}

func TestAccountTestService_CNProviderChatCompletionsUsesOpenAIEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/201/test", nil)

	account := &Account{
		ID:          201,
		Platform:    PlatformKimi,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"account_mode": AccountModeCoding,
			"api_key":      "test-kimi-key",
			"api_protocol": APIProtocolChatCompletions,
			"base_url":     "https://api.kimi.com/coding/v1",
		},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`data: {"choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}

data: [DONE]

`)),
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = true
	cfg.Security.URLAllowlist.UpstreamHosts = []string{"api.kimi.com"}
	svc := &AccountTestService{
		accountRepo:  &cnProviderAccountTestRepo{account: account},
		httpUpstream: upstream,
		cfg:          cfg,
	}

	err := svc.TestAccountConnection(ctx, account.ID, "kimi-for-coding", "reply OK", "")
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://api.kimi.com/coding/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer test-kimi-key", upstream.lastReq.Header.Get("Authorization"))
	require.Contains(t, string(upstream.lastBody), `"model":"kimi-for-coding"`)
	require.Contains(t, recorder.Body.String(), `"success":true`)
}
