//go:build unit

package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const clineEnvelopedChatCompletion = `{"data":{"id":"gen_1","object":"chat.completion","created":1,"model":"cline-pass/kimi-k3","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}},"success":true}`

func TestUnwrapOpenAICompatDataEnvelope(t *testing.T) {
	unwrapped := unwrapOpenAICompatDataEnvelope([]byte(clineEnvelopedChatCompletion))
	require.Equal(t, "pong", gjson.GetBytes(unwrapped, "choices.0.message.content").String())
	require.False(t, gjson.GetBytes(unwrapped, "success").Exists())

	responses := `{"data":{"id":"resp_1","object":"response","output":[]},"success":true}`
	require.Equal(t, "response", gjson.GetBytes(unwrapOpenAICompatDataEnvelope([]byte(responses)), "object").String())

	for _, untouched := range []string{
		`{"id":"x","object":"chat.completion","choices":[]}`,
		`{"object":"list","data":[{"id":"m"}]}`,
		`{"data":{"balance":1},"success":true}`,
		`{"error":{"message":"boom"},"success":false}`,
		`not json`,
	} {
		require.Equal(t, untouched, string(unwrapOpenAICompatDataEnvelope([]byte(untouched))), untouched)
	}
}

func TestClineNonStreamingEnvelopeIsUnwrappedForEveryIngress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name  string
		path  string
		body  string
		check func(t *testing.T, body []byte)
	}{
		{"chat completions", "/v1/chat/completions", `{"model":"kimi-k3","messages":[{"role":"user","content":"hi"}],"stream":false}`, func(t *testing.T, body []byte) {
			require.Equal(t, "pong", gjson.GetBytes(body, "choices.0.message.content").String(), string(body))
			require.False(t, gjson.GetBytes(body, "data").Exists())
		}},
		{"messages", "/v1/messages", `{"model":"kimi-k3","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"stream":false}`, func(t *testing.T, body []byte) {
			require.Equal(t, "pong", gjson.GetBytes(body, "content.#(type==\"text\").text").String(), string(body))
			require.Equal(t, int64(3), gjson.GetBytes(body, "usage.output_tokens").Int())
		}},
		{"responses", "/v1/responses", `{"model":"kimi-k3","input":"hi","stream":false}`, func(t *testing.T, body []byte) {
			require.Contains(t, gjson.GetBytes(body, "output").Raw, "pong", string(body))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(clineEnvelopedChatCompletion)),
			}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			account := clineTestAccount(9301)
			account.Credentials["model_mapping"] = map[string]any{"kimi-k3": "cline-pass/kimi-k3"}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			var err error
			switch tc.path {
			case "/v1/chat/completions":
				_, err = svc.ForwardAsChatCompletions(c.Request.Context(), c, account, []byte(tc.body), "", "")
			case "/v1/messages":
				_, err = svc.ForwardAsAnthropic(c.Request.Context(), c, account, []byte(tc.body), "", "")
			default:
				_, err = svc.Forward(c.Request.Context(), c, account, []byte(tc.body))
			}
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			tc.check(t, recorder.Body.Bytes())
		})
	}
}
