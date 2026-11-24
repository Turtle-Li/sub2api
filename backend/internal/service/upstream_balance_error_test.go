package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClassifyUpstreamBalanceError(t *testing.T) {
	for _, tc := range []struct {
		name, platform, body, code string
		status                     int
		hide, exhausted            bool
	}{
		{"bare payment", PlatformAnthropic, "unstructured provider detail", "", 402, true, true},
		{"workspace detail", PlatformOpenAI, `{"detail":{"code":"deactivated_workspace"}}`, "deactivated_workspace", 402, true, false},
		{"workspace code", PlatformOpenAI, `{"error":{"code":"deactivated_workspace"}}`, "deactivated_workspace", 402, true, false},
		{"workspace unrelated scope", PlatformAnthropic, `{"code":"deactivated_workspace"}`, "deactivated_workspace", 402, true, true},
		{"nested billing code", PlatformOpenAI, `{"error":{"code":"gateway_reservation_insufficient","message":"not a billing phrase"}}`, "gateway_reservation_insufficient", 400, true, true},
		{"embedded json envelope billing code", PlatformOpenAI, `{"error":{"type":"invalid_request_error","message":"{\"error\":{\"code\":\"gateway_reservation_insufficient\",\"message\":\"可用资金不足\"}}"}}`, "gateway_reservation_insufficient", 400, true, true},
		{"non-string error message object ignored", PlatformOpenAI, `{"error":{"message":{"secret":"internal","text":"your credit balance is too low"}}}`, "", 400, false, false},
		{"top-level error code", PlatformGemini, `{"code":"billing_not_active"}`, "billing_not_active", 400, true, true},
		{"error type", PlatformAnthropic, `{"error":{"type":"insufficient_quota"}}`, "insufficient_quota", 400, true, true},
		{"explicit official sentence", PlatformAnthropic, `{"error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits."}}`, "invalid_request_error", 400, true, true},
		{"short English text", PlatformOpenAI, "Your account has insufficient balance.\n", "", 400, true, true},
		{"short Chinese text", PlatformOpenAI, "可用资金不足以支付本次请求。", "", 400, true, true},
		{"prompt echo", PlatformAnthropic, `{"input":{"error":{"code":"insufficient_quota"}},"error":{"message":"Invalid prompt: your credit balance is too low"}}`, "", 400, false, false},
		{"not a trusted JSON field", PlatformAnthropic, `{"message":"your credit balance is too low","detail":{"code":"insufficient_quota"}}`, "", 400, false, false},
		{"no substring billing match", PlatformAnthropic, `{"error":{"message":"Please specify your credit balance category"}}`, "", 400, false, false},
		{"no substring code match", PlatformOpenAI, `{"error":{"code":"not_insufficient_quota"}}`, "not_insufficient_quota", 400, false, false},
		{"code whitelist", PlatformOpenAI, `{"error":{"code":"someone@example.invalid"}}`, "", 402, true, true},
		{"length bound", PlatformOpenAI, `{"code":"` + strings.Repeat("a", 65) + `"}`, "", 402, true, true},
		{"non billing status captures code", PlatformOpenAI, `{"error":{"code":"vendor:limited_2.7-beta"}}`, "vendor:limited_2.7-beta", 429, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := ClassifyUpstreamBalanceError(tc.platform, tc.status, []byte(tc.body))
			require.Equal(t, tc.hide, result.HideFromClient)
			require.Equal(t, tc.exhausted, result.IsBalanceExhausted)
			require.Equal(t, tc.code, result.ProviderCode)
			if tc.exhausted {
				require.Equal(t, "upstream_payment_required", result.NormalizedReason)
			}
		})
	}

	t.Run("extractUpstreamErrorMessage rejects non-string object and array", func(t *testing.T) {
		require.Equal(t, "", extractUpstreamErrorMessage([]byte(`{"error":{"message":{"private":"secret"}}}`)))
		require.Equal(t, "", extractUpstreamErrorMessage([]byte(`{"error":{"message":["private"]}}`)))
		require.Equal(t, "", extractUpstreamErrorMessage([]byte(`{"detail":{"private":"secret"}}`)))
		require.Equal(t, "", extractUpstreamErrorMessage([]byte(`{"message":{"private":"secret"}}`)))
		require.Equal(t, "safe text", extractUpstreamErrorMessage([]byte(`{"error":{"message":"safe text"}}`)))
	})

	t.Run("newHiddenUpstreamBalanceFailure redacts account credentials and upstream host", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		repo := &balanceTestAccountRepo{}
		limiter := NewRateLimitService(repo, nil, balanceTestConfig(), nil, nil)
		account := &Account{
			ID: 248, Name: "AIAPI", Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
			Credentials: map[string]any{
				"api_key":  "sk-secret-provider-key-9999",
				"base_url": "https://secret-relay.example.com/v1",
			},
		}
		raw := []byte(`{"error":{"code":"gateway_reservation_insufficient","message":"key sk-secret-provider-key-9999 failed at secret-relay.example.com"}}`)
		failure := newHiddenUpstreamBalanceFailure(context.Background(), c, limiter, account, http.StatusPaymentRequired, http.Header{}, raw, "claude-opus-5")
		require.NotNil(t, failure)
		require.NotContains(t, string(failure.ResponseBody), "sk-secret-provider-key-9999")
		require.NotContains(t, string(failure.ResponseBody), "secret-relay.example.com")
		require.NotContains(t, repo.lastErrorMsg, "sk-secret-provider-key-9999")
		require.NotContains(t, repo.lastErrorMsg, "secret-relay.example.com")
		evts, _ := c.Get(OpsUpstreamErrorsKey)
		opsEvents, ok := evts.([]*OpsUpstreamErrorEvent)
		require.True(t, ok)
		require.Len(t, opsEvents, 1)
		require.NotContains(t, opsEvents[0].Message, "sk-secret-provider-key-9999")
		require.NotContains(t, opsEvents[0].Message, "secret-relay.example.com")
	})
}

type balanceTestUpstream struct {
	status int
	body   string
	hits   int
}

type balanceTestAccountRepo struct {
	AccountRepository
	setErrCalls  int
	lastErrorMsg string
}

func (r *balanceTestAccountRepo) SetError(_ context.Context, _ int64, message string) error {
	r.setErrCalls++
	r.lastErrorMsg = message
	return nil
}

func balanceTestConfig() *config.Config {
	return &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{AllowInsecureHTTP: true}}}
}

func (u *balanceTestUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.hits++
	return &http.Response{StatusCode: u.status, Header: http.Header{
		"Content-Type": {"application/json"}, "X-Request-Id": {"vendor-request"}, "Retry-After": {"600"},
	}, Body: io.NopCloser(strings.NewReader(u.body))}, nil
}

func (u *balanceTestUpstream) DoWithTLS(req *http.Request, proxy string, account int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, account, concurrency)
}

func TestUpstreamBalanceHTTPProtocolEntriesDoNotCommitOrRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const messages = `{"model":"claude-sonnet-4-6","max_tokens":256,"messages":[{"role":"user","content":"hello"}]}`
	const chat = `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`
	const responses = `{"model":"claude-sonnet-4-6","input":"hello"}`
	for _, tc := range []struct{ route, path, body string }{
		{"claude", "/v1/messages", messages},
		{"claude passthrough", "/v1/messages", messages},
		{"bedrock", "/v1/messages", messages},
		{"claude to chat", "/v1/chat/completions", chat},
		{"claude to responses", "/v1/responses", responses},
		{"gemini to messages", "/v1/messages", strings.ReplaceAll(messages, "claude-sonnet-4-6", "gemini-2.5-flash")},
		{"antigravity claude", "/v1/messages", messages},
		{"openai responses", "/v1/responses", strings.ReplaceAll(responses, "claude-sonnet-4-6", "gpt-5.4")},
		{"openai passthrough", "/v1/responses", strings.ReplaceAll(responses, "claude-sonnet-4-6", "gpt-5.4")},
		{"openai chat", "/v1/chat/completions", strings.ReplaceAll(chat, "claude-sonnet-4-6", "gpt-5.4")},
		{"openai messages", "/v1/messages", strings.ReplaceAll(messages, "claude-sonnet-4-6", "gpt-5.4")},
		{"count tokens", "/v1/messages/count_tokens", messages},
		{"count tokens passthrough", "/v1/messages/count_tokens", messages},
	} {
		for _, status := range []int{400, 402} {
			t.Run(fmt.Sprintf("%s/%d", tc.route, status), func(t *testing.T) {
				upstream := &balanceTestUpstream{status: status, body: `{"error":{"code":"insufficient_quota","message":"private provider balance detail"}}`}
				cfg := balanceTestConfig()
				repo := &balanceTestAccountRepo{}
				limiter := NewRateLimitService(repo, nil, cfg, nil, nil)
				gateway := &GatewayService{cfg: cfg, httpUpstream: upstream, rateLimitService: limiter}
				openai := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, rateLimitService: limiter}
				account := &Account{ID: 248, Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
					Credentials: map[string]any{"api_key": "fake-key", "pool_mode": true,
						"pool_mode_retry_count": 5, "pool_mode_retry_status_codes": []any{float64(400), float64(402)}},
					Extra: map[string]any{}, Concurrency: 1}
				require.True(t, account.IsPoolModeRetryableStatus(status))
				body := []byte(tc.body)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
				c.Request.Header.Set("Content-Type", "application/json")
				ctx := c.Request.Context()
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, err)
				switch tc.route {
				case "claude", "claude passthrough", "bedrock":
					if tc.route == "bedrock" {
						account.Type = AccountTypeBedrock
						account.Credentials["auth_mode"] = "apikey"
						account.Credentials["region"] = "us-east-1"
					} else if tc.route == "claude passthrough" {
						account.Extra["anthropic_passthrough"] = true
					}
					_, err = gateway.Forward(ctx, c, account, parsed)
				case "claude to chat":
					_, err = gateway.ForwardAsChatCompletions(ctx, c, account, body, nil)
				case "claude to responses":
					_, err = gateway.ForwardAsResponses(ctx, c, account, body, nil)
				case "gemini to messages":
					account.Platform = PlatformGemini
					svc := &GeminiMessagesCompatService{cfg: cfg, httpUpstream: upstream, rateLimitService: limiter}
					_, err = svc.Forward(ctx, c, account, body)
				case "antigravity claude":
					account = newAntigravityCompatAccount(AccountTypeOAuth)
					svc := newAntigravityCompatService(cfg.Gateway, upstream)
					svc.rateLimitService = limiter
					_, err = svc.Forward(ctx, c, account, body, false)
				case "openai responses", "openai passthrough", "openai chat", "openai messages":
					account.Platform = PlatformOpenAI
					account.Credentials["base_url"] = "https://upstream.invalid/v1"
					switch tc.route {
					case "openai responses", "openai passthrough":
						account.Extra["openai_passthrough"] = tc.route == "openai passthrough"
						_, err = openai.Forward(ctx, c, account, body)
					case "openai chat":
						_, err = openai.ForwardAsChatCompletions(ctx, c, account, body, "", "")
					case "openai messages":
						_, err = openai.ForwardAsAnthropic(ctx, c, account, body, "", "")
					}
				case "count tokens", "count tokens passthrough":
					account.Extra["anthropic_passthrough"] = tc.route == "count tokens passthrough"
					err = gateway.ForwardCountTokens(ctx, c, account, parsed)
				}
				var failure *UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.Equal(t, status, failure.StatusCode)
				require.True(t, failure.HiddenUpstreamBalance)
				require.False(t, failure.RetryableOnSameAccount)
				require.True(t, failure.ShouldRetryNextAccount())
				require.Equal(t, 1, upstream.hits)
				require.False(t, c.Writer.Written())
				require.False(t, IsResponseCommitted(c))
				value, _ := c.Get(OpsUpstreamErrorsKey)
				events := value.([]*OpsUpstreamErrorEvent)
				require.Len(t, events, 1)
				require.Equal(t, status, events[0].UpstreamStatusCode)
				require.Equal(t, "insufficient_quota", events[0].ProviderCode)
				require.Empty(t, events[0].UpstreamResponseBody)
			})
		}
	}
}

func TestUpstreamBalanceProbeOnlyChangesMatchingResponse(t *testing.T) {
	cfg := balanceTestConfig()
	repo := &balanceTestAccountRepo{}
	limiter := NewRateLimitService(repo, nil, cfg, nil, nil)
	account := &Account{ID: 248, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx := context.Background()
	for _, body := range []string{`{"error":{"message":"your credit balance information is missing"}}`, strings.Repeat(" ", upstreamBalanceProbeLimit) + "your credit balance is too low"} {
		resp := &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(body))}
		require.Nil(t, probeUpstreamBalanceResponse(ctx, c, limiter, account, resp, ""))
		replayed, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, body, string(replayed))
		require.NoError(t, resp.Body.Close())
	}
	require.Zero(t, repo.setErrCalls)
	resp := &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"code":"billing_not_active"}`))}
	require.NotNil(t, probeUpstreamBalanceResponse(ctx, c, limiter, account, resp, ""))
	require.Equal(t, http.NoBody, resp.Body)
	require.Equal(t, 1, repo.setErrCalls)
	require.Contains(t, repo.lastErrorMsg, "(400)")
}

func TestOpsUpstreamProviderCodeSurvivesBodyLoggingDisabled(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	appendOpsUpstreamResponseError(c, []byte(`{"error":{"code":"vendor:capacity","message":"private"}}`), OpsUpstreamErrorEvent{
		Platform: PlatformOpenAI, AccountID: 248, UpstreamStatusCode: 429,
	})
	appendOpsUpstreamResponseError(c, []byte(`{"error":{"code":"not a valid code"}}`), OpsUpstreamErrorEvent{
		Platform: PlatformOpenAI, AccountID: 258, UpstreamStatusCode: 503,
	})
	value, _ := c.Get(OpsUpstreamErrorsKey)
	events := value.([]*OpsUpstreamErrorEvent)
	require.Len(t, events, 2)
	require.Equal(t, "vendor:capacity", events[0].ProviderCode)
	require.Empty(t, events[0].UpstreamResponseBody)
	require.Empty(t, events[1].ProviderCode)
}

type balanceStickyTestCache struct {
	GatewayCache
	accountID int64
	writes    int
}

func (c *balanceStickyTestCache) SetSessionAccountID(_ context.Context, _ int64, _ string, next int64, _ time.Duration) error {
	c.writes++
	c.accountID = next
	return nil
}

func (c *balanceStickyTestCache) ReplaceFailedStickySession(_ context.Context, _ int64, _ string, failed, next int64, _ time.Duration) error {
	if c.accountID == failed {
		c.writes++
		c.accountID = next
	}
	return nil
}

func TestUpstreamBalanceStickyMutationDeferredUntilSuccessfulCAS(t *testing.T) {
	cache := &balanceStickyTestCache{accountID: 248}
	gateway := &GatewayService{cache: cache}
	ctx := WithDeferredBalanceStickyMutation(context.Background(), nil, "affinity")
	require.NoError(t, gateway.bindGatewayStickySessionDuringSelection(ctx, nil, "affinity", 258))
	require.NoError(t, gateway.BindStickySessionAfterProfitAdmission(ctx, nil, "affinity", 258))
	require.Zero(t, cache.writes, "selection or admission is not upstream success")
	require.NoError(t, gateway.ReplaceFailedStickySession(ctx, nil, "affinity", 248, 258))
	require.Equal(t, int64(258), cache.accountID)
	require.Equal(t, 1, cache.writes)
	require.NoError(t, gateway.ReplaceFailedStickySession(ctx, nil, "affinity", 248, 0))
	require.Equal(t, int64(258), cache.accountID)
	require.NoError(t, gateway.ReplaceFailedStickySession(ctx, nil, "affinity", 258, 0))
	require.Zero(t, cache.accountID, "a paid reserve can clear failed ordinary affinity without replacing it")
}

func TestUpstreamBalanceAntigravitySmartRetriesPropagateTerminalFailure(t *testing.T) {
	const baseURL = "https://example.invalid"
	ctx := context.Background()
	for _, variant := range []string{"model retry", "single account 503", "paid credits"} {
		t.Run(variant, func(t *testing.T) {
			upstream := &balanceTestUpstream{status: 402, body: `{"error":{"code":"insufficient_quota"}}`}
			account := &Account{ID: 0, Name: "retry-test", Platform: PlatformAntigravity, Type: AccountTypeOAuth}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			p := antigravityRetryLoopParams{ctx: ctx, account: account, c: c, action: "generateContent",
				accessToken: "fake", body: []byte(`{"model":"claude-opus-4","request":{}}`), httpUpstream: upstream}
			svc := &AntigravityGatewayService{}
			var err error
			switch variant {
			case "model retry":
				body := []byte(`{"error":{"status":"RESOURCE_EXHAUSTED","details":[
					{"@type":"type.googleapis.com/google.rpc.ErrorInfo","metadata":{"model":"claude-opus-4"},"reason":"RATE_LIMIT_EXCEEDED"},
					{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"0.001s"}
				]}}`)
				result := svc.handleSmartRetry(p, &http.Response{StatusCode: 429}, body, baseURL, 0, []string{baseURL})
				err = result.err
			case "single account 503":
				result := svc.handleSingleAccountRetryInPlace(p, &http.Response{StatusCode: 503}, nil, baseURL, time.Millisecond, "claude-opus-4")
				err = result.err
			case "paid credits":
				account.Extra = map[string]any{"allow_overages": true}
				body := []byte(`{"error":{"status":"RESOURCE_EXHAUSTED","message":"QUOTA_EXHAUSTED"}}`)
				result := svc.handleSmartRetry(p, &http.Response{StatusCode: 429}, body, baseURL, 0, []string{baseURL})
				err = result.err
			}
			var failure *UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.True(t, failure.HiddenUpstreamBalance)
			require.False(t, failure.RetryableOnSameAccount)
			require.Equal(t, 402, failure.StatusCode)
			require.Equal(t, 1, upstream.hits)
			value, _ := c.Get(OpsUpstreamErrorsKey)
			require.Len(t, value, 1)
		})
	}
}
