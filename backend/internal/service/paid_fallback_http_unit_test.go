//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type paidFallbackLocalHTTP struct{ client *http.Client }

func (u paidFallbackLocalHTTP) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	finish, err := BeginPaidFallbackAttempt(req.Context())
	if err != nil {
		return nil, err
	}
	response, err := u.client.Do(req)
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	finish(status, err)
	return response, err
}

func (u paidFallbackLocalHTTP) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestPaidFallbackLocalHTTPFailureRecoveryAndStop(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformDeepseek} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", platform, stream), func(t *testing.T) {
				var normalCalls, paidCalls atomic.Int64
				var recovered atomic.Bool
				reply := func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/v1/chat/completions" {
						t.Errorf("unexpected upstream path: %s", r.URL.Path)
						http.Error(w, "unexpected path", 400)
						return
					}
					var input struct {
						Stream bool `json:"stream"`
					}
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
					if input.Stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-local\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\ndata: [DONE]\n\n")
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"chatcmpl-local","object":"chat.completion","model":"local","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
				}
				normal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					normalCalls.Add(1)
					if !recovered.Load() {
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{"error":{"message":"local upstream unavailable","type":"server_error"}}`))
						return
					}
					reply(w, r)
				}))
				defer normal.Close()
				paid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { paidCalls.Add(1); reply(w, r) }))
				defer paid.Close()
				svc, baseCtx, settings := newPaidFallbackScheduler(t, platform, false)
				svc.cfg = rawChatCompletionsTestConfig()
				svc.httpUpstream = paidFallbackLocalHTTP{client: &http.Client{Timeout: time.Second}}
				repo := paidFallbackTestAccountRepo(t, svc)
				repo.accounts[0].Credentials = map[string]any{"api_key": "local-normal-test", "base_url": normal.URL}
				repo.accounts[1].Credentials = map[string]any{"api_key": "local-paid-test", "base_url": paid.URL}
				model := "gpt-5.4"
				if platform == PlatformDeepseek {
					model = "deepseek-v4-flash"
				}
				body, err := json.Marshal(map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "local test"}}, "stream": stream})
				require.NoError(t, err)
				forward := func(ctx context.Context, selection *AccountSelectionResult) error {
					defer selection.ReleaseFunc()
					ctx = ContextWithSelectionProfitGate(ctx, selection)
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(ctx)
					_, err := svc.forwardAsRawChatCompletions(ctx, c, selection.Account, body, "")
					if err == nil {
						require.Equal(t, http.StatusOK, recorder.Code)
						require.Contains(t, recorder.Body.String(), `"content":"ok"`)
						if stream {
							require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
							require.Contains(t, recorder.Body.String(), "data: [DONE]")
						} else {
							require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
						}
					}
					return err
				}
				selection, err := selectPaidFallbackTestAccount(svc, baseCtx, platform, nil)
				require.NoError(t, err)
				err = forward(baseCtx, selection)
				var failure *UpstreamFailoverError
				require.True(t, errors.As(err, &failure), "%v", err)
				failureCtx := WithPaidFallbackFailure(baseCtx, selection.Account, failure.StatusCode)
				selection, err = selectPaidFallbackTestAccount(svc, failureCtx, platform, map[int64]struct{}{1: {}})
				require.NoError(t, err)
				require.NoError(t, forward(failureCtx, selection))
				require.EqualValues(t, 1, paidCalls.Load())
				recovered.Store(true)
				selection, err = selectPaidFallbackTestAccount(svc, baseCtx, platform, nil)
				require.NoError(t, err)
				require.EqualValues(t, 1, selection.Account.ID)
				require.NoError(t, forward(baseCtx, selection))
				require.EqualValues(t, 2, normalCalls.Load())
				require.EqualValues(t, 1, paidCalls.Load())
				selection, err = selectPaidFallbackTestAccount(svc, failureCtx, platform, map[int64]struct{}{1: {}})
				require.NoError(t, err)
				settings.setPaidPolicy(`{"enabled":false}`)
				require.Error(t, forward(failureCtx, selection))
				require.EqualValues(t, 1, paidCalls.Load(), "kill switch prevents another network request")
			})
		}
	}
}
