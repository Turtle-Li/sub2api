//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiChatCompletionsOutputLimitIntent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name  string
			limit string
			want  int64
		}{
			{name: "omitted"},
			{name: "null", limit: `,"max_tokens":null,"max_completion_tokens":null`},
			{name: "explicit", limit: `,"max_tokens":32768`, want: 32768},
			{name: "completion precedence", limit: `,"max_tokens":32768,"max_completion_tokens":64000`, want: 64000},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				svc, stub := newGeminiVertexAPIKeyTestService()
				if stream {
					stub.response.Header.Set("Content-Type", "text/event-stream")
					stub.response.Body = io.NopCloser(strings.NewReader("data: " + geminiVertexAPIKeyTestResponse + "\n\n"))
				}
				body := []byte(fmt.Sprintf(`{"model":"gemini-4-argon","stream":%t,"reasoning_effort":"high","messages":[{"role":"user","content":"Write the full scene."}]%s}`, stream, tc.limit))
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
				result, err := svc.ForwardAsChatCompletions(context.Background(), c, geminiVertexAPIKeyTestAccount(""), body)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 1, stub.calls)
				forwarded, err := io.ReadAll(stub.lastReq.Body)
				require.NoError(t, err)
				limit := gjson.GetBytes(forwarded, "generationConfig.maxOutputTokens")
				if tc.want == 0 {
					require.False(t, limit.Exists(), "omitted output limit became %s", limit.Raw)
				} else {
					require.Equal(t, tc.want, limit.Int())
				}
				require.Equal(t, "high", gjson.GetBytes(forwarded, "generationConfig.thinkingConfig.thinkingLevel").String())
			})
		}
	}
}

func TestAntigravityCompatGeminiOmittedOutputLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"chat", "responses"} {
		for _, model := range []string{"gemini-3.1-pro-high", "public-alias", "claude-sonnet-4-6"} {
			t.Run(protocol+"/"+model, func(t *testing.T) {
				stub := &queuedHTTPUpstreamStub{responses: []*http.Response{antigravityCompatSuccessResponse()}}
				svc := newAntigravityCompatService(config.GatewayConfig{MaxLineSize: defaultMaxLineSize}, stub)
				body := []byte(fmt.Sprintf(`{"model":%q,"stream":true,"reasoning_effort":"high","messages":[{"role":"user","content":"hello"}]}`, model))
				route := "/v1/chat/completions"
				if protocol == "responses" {
					route = "/v1/responses"
					body = []byte(fmt.Sprintf(`{"model":%q,"stream":true,"reasoning":{"effort":"high"},"input":"hello"}`, model))
				}
				c, _ := newAntigravityCompatContext(http.MethodPost, route, body)
				account := newAntigravityCompatAccount(AccountTypeOAuth)
				account.Credentials["model_mapping"].(map[string]any)["public-alias"] = "gemini-3.1-pro-high"
				var err error
				if protocol == "chat" {
					_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
				} else {
					_, err = svc.ForwardAsResponses(context.Background(), c, account, body, nil)
				}
				require.NoError(t, err)
				require.Len(t, stub.requestBodies, 1)
				limit := gjson.GetBytes(stub.requestBodies[0], "request.generationConfig.maxOutputTokens")
				if model != "claude-sonnet-4-6" {
					require.False(t, limit.Exists(), "omitted output limit became %s", limit.Raw)
				} else {
					require.Equal(t, int64(11240), limit.Int(), "real Claude default plus HIGH thinking adjustment must remain intact")
				}
			})
		}
	}
}
