package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type balancePassthroughRules struct {
	service.ErrorPassthroughRepository
}

func (balancePassthroughRules) List(context.Context) ([]*model.ErrorPassthroughRule, error) {
	return []*model.ErrorPassthroughRule{{Name: "private message passthrough", Enabled: true, MatchMode: model.MatchModeAny,
		ErrorCodes: []int{400, 402}, PassthroughCode: true, PassthroughBody: true}}, nil
}

func TestUpstreamBalanceTerminalFormatsOverridePassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	policy := service.NewErrorPassthroughService(balancePassthroughRules{}, nil)
	require.NotNil(t, policy.MatchRule(service.PlatformOpenAI, 402, nil))
	gateway := &GatewayHandler{errorPassthroughService: policy}
	openai := &OpenAIGatewayHandler{errorPassthroughService: policy}
	for _, tc := range []struct {
		name, path, prefix string
		terminal           func(*gin.Context, *service.UpstreamFailoverError, bool)
	}{
		{"messages", "/v1/messages", "event: error", func(c *gin.Context, e *service.UpstreamFailoverError, s bool) {
			gateway.handleFailoverExhausted(c, e, service.PlatformAnthropic, s)
		}},
		{"claude to chat", "/v1/chat/completions", `data: {"error":`, gateway.handleCCFailoverExhausted},
		{"claude to responses", "/v1/responses", "event: response.failed", gateway.handleResponsesFailoverExhausted},
		{"openai responses", "/v1/responses", "event: response.failed", openai.handleFailoverExhausted},
		{"openai chat", "/v1/chat/completions", `data: {"error":`, openai.handleChatCompletionsFailoverExhausted},
		{"openai messages", "/v1/messages", "event: error", openai.handleAnthropicFailoverExhausted},
		{"gemini", "/v1beta/models/gemini:streamGenerateContent", `data: {"error":`, func(c *gin.Context, e *service.UpstreamFailoverError, _ bool) {
			gateway.handleGeminiFailoverExhausted(c, e)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := &service.UpstreamFailoverError{StatusCode: 402,
				ResponseBody:    []byte(`{"error":{"code":"insufficient_quota","message":"provider-secret-marker"}}`),
				ResponseHeaders: http.Header{"Retry-After": {"999"}}}
			makeContext := func() (*gin.Context, *httptest.ResponseRecorder) {
				response := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(response)
				c.Request = httptest.NewRequest(http.MethodPost, tc.path, nil)
				return c, response
			}
			c, response := makeContext()
			tc.terminal(c, failure, false)
			require.Equal(t, 502, response.Code)
			require.Contains(t, response.Body.String(), service.UpstreamBalanceClientMessage)
			require.NotContains(t, response.Body.String(), "provider-secret-marker")
			require.Empty(t, response.Header().Get("Retry-After"))
			require.True(t, service.IsResponseCommitted(c))
			c, response = makeContext()
			c.Header("Content-Type", "text/event-stream")
			_, err := c.Writer.WriteString(": keepalive\n\n")
			require.NoError(t, err)
			tc.terminal(c, failure, true)
			first := response.Body.String()
			require.Equal(t, 200, response.Code)
			require.Contains(t, first, tc.prefix)
			require.NotContains(t, first, "provider-secret-marker")
			require.Contains(t, first, service.UpstreamBalanceClientMessage)
			tc.terminal(c, failure, true)
			require.Equal(t, first, response.Body.String(), "the second handler must not append another terminal frame")
		})
	}
}

func TestUpstreamBalanceFailurePersistsExclusionThrough503Backoff(t *testing.T) {
	ctx := context.Background()
	states := NewFailoverState(4, true)
	switchAction := states.HandleFailoverError(ctx, nil, 248, service.PlatformAnthropic, 5, &service.UpstreamFailoverError{
		StatusCode: 400, ResponseBody: []byte(`{"error":{"code":"insufficient_quota"}}`), RetryableOnSameAccount: true,
	})
	require.Equal(t, FailoverContinue, switchAction)
	require.Zero(t, states.SameAccountRetryCount[248])
	require.True(t, states.ForceCacheBilling)
	transient := &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}
	require.Equal(t, FailoverContinue, states.HandleFailoverError(ctx, nil, 258, service.PlatformAnthropic, 0, transient))
	require.Same(t, transient, states.LastFailoverErr, "mixed failures are decided by the terminal attempt")
	require.Equal(t, FailoverContinue, states.HandleSelectionExhausted(ctx))
	require.Equal(t, map[int64]struct{}{248: {}}, states.FailedAccountIDs)
	require.Equal(t, FailoverExhausted, states.HandleSelectionExhausted(ctx), "a deterministic-only excluded set must not spin")
}

func TestUpstreamBalanceGeminiExplicitCacheNeverPassthrough(t *testing.T) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini:generateContent", nil)
	(&GatewayHandler{}).handleGeminiCachedContentFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode: 402, ResponseBody: []byte(`{"error":{"message":"provider-secret-marker"}}`), BoundUpstreamPassthrough: true,
	})
	require.Equal(t, 502, response.Code)
	require.NotContains(t, response.Body.String(), "provider-secret-marker")
}

func TestOpsUpstreamProviderCodeUsesFinalHop(t *testing.T) {
	entry := &service.OpsInsertErrorLogInput{}
	applyOpsUpstreamErrorEvents(entry, []*service.OpsUpstreamErrorEvent{
		{AccountID: 248, UpstreamStatusCode: 402, ProviderCode: "insufficient_quota"},
		{AccountID: 258, UpstreamStatusCode: 503},
	})
	require.NotNil(t, entry.UpstreamStatusCode)
	require.Equal(t, 503, *entry.UpstreamStatusCode)
	require.Empty(t, entry.ProviderErrorCode)
	require.Len(t, entry.UpstreamErrors, 2)
}
