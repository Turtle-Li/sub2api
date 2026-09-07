package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type openAIResponsesImageRoutingHTTPCall struct {
	accountID int64
	body      []byte
}

type openAIResponsesImageRoutingHTTPUpstream struct {
	service.HTTPUpstream
	mu    sync.Mutex
	calls []openAIResponsesImageRoutingHTTPCall
}

func (u *openAIResponsesImageRoutingHTTPUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.calls = append(u.calls, openAIResponsesImageRoutingHTTPCall{accountID: accountID, body: append([]byte(nil), body...)})
	u.mu.Unlock()

	if accountID == 73002 {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"temporary upstream failure"}}`)),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_image_routing","object":"response","model":"gpt-5.5-upstream","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		)),
	}, nil
}

func (u *openAIResponsesImageRoutingHTTPUpstream) recordedCalls() []openAIResponsesImageRoutingHTTPCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]openAIResponsesImageRoutingHTTPCall(nil), u.calls...)
}

func TestOpenAIResponses_ImageToolRoutingPersistsAcrossChannelMappingAndRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(73001)
	modelMapping := func(imageModels ...string) map[string]any {
		mapping := map[string]any{"gpt-5.5-upstream": "gpt-5.5-upstream"}
		for _, model := range imageModels {
			mapping[model] = model
		}
		return mapping
	}
	accounts := []service.Account{
		{
			ID: 73001, Name: "text-only-preferred", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: 1,
			Credentials: map[string]any{"api_key": "sk-text", "base_url": "https://api.example.test", "model_mapping": modelMapping()},
			Extra:       map[string]any{"openai_responses_supported": true},
		},
		{
			ID: 73002, Name: "image-capable-first-retry", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: 2,
			Credentials: map[string]any{"api_key": "sk-image-a", "base_url": "https://api.example.test", "model_mapping": modelMapping("gpt-image-2")},
			Extra:       map[string]any{"openai_responses_supported": true},
		},
		{
			ID: 73003, Name: "image-capable-retry-success", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: 3,
			Credentials: map[string]any{"api_key": "sk-image-b", "base_url": "https://api.example.test", "model_mapping": modelMapping("gpt-image-2")},
			Extra:       map[string]any{"openai_responses_supported": true},
		},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1

	accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
	channelSvc := service.NewChannelService(&openAIWSUsageHandlerChannelRepoStub{
		channels: []service.Channel{{
			ID:           73001,
			Name:         "responses-image-routing-channel",
			Status:       service.StatusActive,
			GroupIDs:     []int64{groupID},
			ModelMapping: map[string]map[string]string{service.PlatformOpenAI: {"gpt-5.5": "gpt-5.5-upstream"}},
		}},
		groupPlatforms: map[int64]string{groupID: service.PlatformOpenAI},
	}, nil, nil, nil)
	upstream := &openAIResponsesImageRoutingHTTPUpstream{}
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheSvc.Stop)
	gatewaySvc := service.NewOpenAIGatewayService(
		accountRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		cfg,
		nil,
		nil,
		service.NewBillingService(cfg, nil),
		nil,
		billingCacheSvc,
		upstream,
		&service.DeferredService{},
		nil,
		nil,
		nil,
		channelSvc,
		nil,
		nil,
		nil,
	)
	h := NewOpenAIGatewayHandler(
		gatewaySvc,
		service.NewConcurrencyService(nil),
		billingCacheSvc,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil,
		nil,
		nil,
		nil,
		cfg,
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(
		`{"model":"gpt-5.5","input":"draw","stream":false,"tools":[{"type":"image_generation"}]}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID:      73001,
		GroupID: &groupID,
		User:    &service.User{ID: 73001, Status: service.StatusActive},
		Group: &service.Group{
			ID:                   groupID,
			Platform:             service.PlatformOpenAI,
			Status:               service.StatusActive,
			AllowImageGeneration: true,
		},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 73001, Concurrency: 0})

	h.Responses(c)

	require.Equal(t, http.StatusOK, rec.Code)
	calls := upstream.recordedCalls()
	require.Len(t, calls, 2)
	require.Equal(t, []int64{73002, 73003}, []int64{calls[0].accountID, calls[1].accountID})
	for _, call := range calls {
		require.Equal(t, "gpt-5.5-upstream", gjson.GetBytes(call.body, "model").String())
		require.Equal(t, "image_generation", gjson.GetBytes(call.body, "tools.0.type").String())
	}
}
