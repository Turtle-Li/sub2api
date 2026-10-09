//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type multiModelHandlerUpstream struct {
	request   *http.Request
	body      []byte
	accountID int64
}

func (u *multiModelHandlerUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.request, u.accountID = req, accountID
	u.body, _ = io.ReadAll(req.Body)
	model, _ := json.Marshal(gjson.GetBytes(u.body, "model").String())
	body := `{"id":"chatcmpl-test","object":"chat.completion","model":` + string(model) + `,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`
	contentType := "application/json"
	if gjson.GetBytes(u.body, "stream").Bool() {
		contentType = "text/event-stream"
		body = "data: " + `{"id":"chatcmpl-test","object":"chat.completion.chunk","model":` + string(model) + `,"choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}` + "\n\ndata: " + `{"id":"chatcmpl-test","object":"chat.completion.chunk","model":` + string(model) + `,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}` + "\n\ndata: [DONE]\n\n"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (u *multiModelHandlerUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

type multiModelHandlerUsageRepo struct {
	service.UsageLogRepository
	usage *service.UsageLog
}

func (r *multiModelHandlerUsageRepo) Create(_ context.Context, usage *service.UsageLog) (bool, error) {
	r.usage = usage
	return true, nil
}

func TestMultiModelProviderGroupHandlerIngress(t *testing.T) {
	for _, groupPlatform := range []string{service.PlatformAnthropic, service.PlatformGemini} {
		for _, endpoint := range []string{"messages", "chat/completions", "responses"} {
			t.Run(groupPlatform+"/"+endpoint, func(t *testing.T) {
				gid := int64(7)
				group := &service.Group{ID: gid, Hydrated: true, Platform: groupPlatform, Status: service.StatusActive, RateMultiplier: 0.7}
				model := "claude-sonnet-4-6"
				if groupPlatform == service.PlatformGemini {
					model = "gemini-3-pro"
				}
				account := &service.Account{ID: 221, Platform: service.PlatformCline, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{gid},
					AccountGroups: []service.AccountGroup{{AccountID: 221, GroupID: gid}},
					Credentials:   map[string]any{"api_key": "test-only-key", "model_mapping": map[string]any{model: "cline-pass/" + model}}}
				h, cleanup := newTestGatewayHandler(t, group, []*service.Account{account})
				t.Cleanup(cleanup)
				cfg := &config.Config{RunMode: config.RunModeSimple}
				h.cfg = cfg
				upstream, usageRepo := &multiModelHandlerUpstream{}, &multiModelHandlerUsageRepo{}
				repo := openAIImagesFailoverAccountRepo{accounts: []service.Account{*account}}
				h.openAIGatewayService = service.NewOpenAIGatewayService(repo, usageRepo, nil, nil, nil, nil, nil, cfg,
					nil, nil, service.NewBillingService(cfg, nil), nil, nil, upstream,
					service.NewDeferredService(repo, nil, time.Minute), nil, nil, nil, nil, nil, nil, nil)
				body := `{"model":"` + model + `","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"stream":false}`
				if endpoint == "responses" {
					body = `{"model":"` + model + `","input":"hello","stream":false}`
				}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, bytes.NewBufferString(body))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Group, group))
				key := &service.APIKey{ID: 20, UserID: 30, GroupID: &gid, Group: group, User: &service.User{ID: 30, Concurrency: 10, Balance: 100}}
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 30, Concurrency: 10})
				switch endpoint {
				case "messages":
					h.Messages(c)
				case "responses":
					h.Responses(c)
				default:
					h.ChatCompletions(c)
				}
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				require.Equal(t, account.ID, upstream.accountID)
				require.Equal(t, "https://api.cline.bot/api/v1/chat/completions", upstream.request.URL.String())
				require.Equal(t, "cline-pass/"+model, gjson.GetBytes(upstream.body, "model").String())
				require.Contains(t, recorder.Body.String(), "ok")
				require.NotNil(t, usageRepo.usage, "compatible forwarding must retain usage recording")
				require.Equal(t, &gid, usageRepo.usage.GroupID)
				require.Equal(t, 0.7, usageRepo.usage.RateMultiplier)
			})
		}
	}
}

func TestMultiModelMessagesUsesNormalizedForwardModel(t *testing.T) {
	for _, provider := range []string{service.PlatformOpenCodeGo, service.PlatformCommandCode, service.PlatformCline} {
		for _, allowed := range []bool{false, true} {
			t.Run(provider+map[bool]string{false: "/wrong-family", true: "/matching-family"}[allowed], func(t *testing.T) {
				gid := int64(7)
				group := &service.Group{ID: gid, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 0.7}
				rawTarget, normalizedTarget := "claude-sonnet-4-6", "deepseek-v4-pro"
				if allowed {
					rawTarget, normalizedTarget = normalizedTarget, rawTarget
				}
				account := &service.Account{ID: 221, Platform: provider, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{gid},
					AccountGroups: []service.AccountGroup{{AccountID: 221, GroupID: gid}},
					Credentials: map[string]any{"api_key": "test-only-key", "api_protocol": service.APIProtocolChatCompletions,
						"model_mapping": map[string]any{"gpt-5.4-high": rawTarget, "gpt-5.4": normalizedTarget}}}
				h, cleanup := newTestGatewayHandler(t, group, []*service.Account{account})
				t.Cleanup(cleanup)
				cfg := &config.Config{RunMode: config.RunModeSimple}
				h.cfg = cfg
				upstream, usageRepo := &multiModelHandlerUpstream{}, &multiModelHandlerUsageRepo{}
				repo := openAIImagesFailoverAccountRepo{accounts: []service.Account{*account}}
				h.openAIGatewayService = service.NewOpenAIGatewayService(repo, usageRepo, nil, nil, nil, nil, nil, cfg,
					nil, nil, service.NewBillingService(cfg, nil), nil, nil, upstream,
					service.NewDeferredService(repo, nil, time.Minute), nil, nil, nil, nil, nil, nil, nil)
				body := `{"model":"gpt-5.4-high","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"stream":false}`
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Group, group))
				key := &service.APIKey{ID: 20, UserID: 30, GroupID: &gid, Group: group, User: &service.User{ID: 30, Concurrency: 10, Balance: 100}}
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 30, Concurrency: 10})
				h.Messages(c)
				if allowed {
					require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
					require.Equal(t, account.ID, upstream.accountID)
					require.Equal(t, "claude-sonnet-4-6", gjson.GetBytes(upstream.body, "model").String())
					require.NotNil(t, usageRepo.usage)
					require.Equal(t, &gid, usageRepo.usage.GroupID)
				} else {
					require.NotEqual(t, http.StatusOK, recorder.Code)
					require.Nil(t, upstream.request, "wrong-family requests must be rejected before upstream I/O")
					require.Nil(t, usageRepo.usage)
				}
			})
		}
	}
}
