//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestChannelMonitorV3ProbeKeepsExactRestrictedGroup(t *testing.T) {
	id, fallback := int64(10), int64(11)
	svc := &GatewayService{groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{
		id: {ID: id, ClaudeCodeOnly: true, FallbackGroupID: &fallback}, fallback: {ID: fallback},
	}}}
	_, resolved, err := svc.resolveGatewayGroup(context.Background(), &id)
	require.NoError(t, err)
	require.Equal(t, fallback, *resolved, "ordinary client restrictions retain their existing fallback")
	ctx := context.WithValue(context.Background(), channelMonitorV3ProbeGroupContextKey{}, id)
	_, resolved, err = svc.resolveGatewayGroup(ctx, &id)
	require.NoError(t, err)
	require.Equal(t, id, *resolved, "an admin-owned probe must not certify another group")
	ctx = context.WithValue(context.Background(), channelMonitorV3ProbeGroupContextKey{}, fallback)
	_, resolved, err = svc.resolveGatewayGroup(ctx, &id)
	require.NoError(t, err)
	require.Equal(t, fallback, *resolved, "the capability is scoped to the exact target group")
}

func TestChannelMonitorV3ProbeOpenAIProviderForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, oauth := range []bool{false, true} {
		name := "api_key"
		if oauth {
			name = "oauth"
		}
		t.Run(name, func(t *testing.T) {
			stream := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_probe\",\"model\":\"gpt-5.4-mini\",\"status\":\"in_progress\",\"output\":[]}}\n\n" +
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"1\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_probe\",\"model\":\"gpt-5.4-mini\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"1\"}]}],\"usage\":{\"input_tokens\":9,\"output_tokens\":1}}}\n\n"
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}}
			cfg := &config.Config{}
			cfg.Gateway.MinOutputTokens = 16
			account := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://api.openai.com"}}
			if oauth {
				account.Type = AccountTypeOAuth
				account.Credentials = map[string]any{"access_token": "fixture", "chatgpt_account_id": "fixture-account"}
			}
			probe := &channelMonitorV3GatewayProbe{openai: &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}}
			result := probe.checkAccount(context.Background(), &Group{ID: 6, Platform: PlatformOpenAI}, "gpt-5.4-mini", account)
			require.Equal(t, "success", result.Outcome)
			require.EqualValues(t, 9, result.InputTokens)
			require.EqualValues(t, 1, result.OutputTokens)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
			if oauth {
				require.False(t, gjson.GetBytes(upstream.lastBody, "max_output_tokens").Exists(), "native Codex protocol strips unsupported limits; do not claim a hard OAuth token ceiling")
			} else {
				require.EqualValues(t, 16, gjson.GetBytes(upstream.lastBody, "max_output_tokens").Int())
			}
		})
	}
}

func TestChannelMonitorV3ProbeAnthropicProviderForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_probe\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":9}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"1\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"},\"usage\":{\"output_tokens\":1}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}}
	probe := &channelMonitorV3GatewayProbe{gateway: newForwardPartialUsageServiceForTest(upstream)}
	result := probe.checkAccount(context.Background(), &Group{ID: 10, Platform: PlatformAnthropic}, "claude-haiku-4-5", newAnthropicAPIKeyAccountForTest())
	require.Equal(t, "success", result.Outcome)
	require.EqualValues(t, 9, result.InputTokens)
	require.EqualValues(t, 1, result.OutputTokens)
	require.EqualValues(t, 1, gjson.GetBytes(upstream.lastBody, "max_tokens").Int())
	require.Equal(t, "claude-haiku-4-5", gjson.GetBytes(upstream.lastBody, "model").String())
}

func TestChannelMonitorV3ProbeRejectsMappedMediaBeforeSending(t *testing.T) {
	probe := &channelMonitorV3GatewayProbe{}
	result := probe.checkAccount(context.Background(), &Group{ID: 6, Platform: PlatformOpenAI}, "cheap-alias", &Account{Credentials: map[string]any{"model_mapping": map[string]any{"cheap-alias": "gpt-image-2"}}})
	require.Equal(t, "failure", result.Outcome)
	require.Equal(t, "unsupported_model", result.Category)
}

func TestChannelMonitorV3ProbeDomesticTextProvidersUseNativeChatForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ platform, model string }{
		{PlatformDeepseek, "deepseek-chat"}, {PlatformKimi, "kimi-k2.5"}, {PlatformZhipu, "glm-5"}, {PlatformMiniMax, "MiniMax-M2.7"},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			stream := "data: {\"id\":\"chat_probe\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"1\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":1,\"total_tokens\":10}}\n\ndata: [DONE]\n\n"
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}}
			cfg := &config.Config{}
			cfg.Gateway.MinOutputTokens = 16
			account := &Account{ID: 2, Platform: tc.platform, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://provider.example.test/v1"}}
			probe := &channelMonitorV3GatewayProbe{openai: &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}}
			out := probe.checkAccount(context.Background(), &Group{ID: 6, Platform: tc.platform}, tc.model, account)
			require.Equal(t, "success", out.Outcome)
			require.Len(t, upstream.requests, 1, "the monitor may not add a retry")
			require.Equal(t, tc.model, gjson.GetBytes(upstream.lastBody, "model").String())
			require.EqualValues(t, 9, out.InputTokens)
			require.EqualValues(t, 1, out.OutputTokens)
		})
	}
}

func TestChannelMonitorV3ProbeCaptureOverflowFailsClosed(t *testing.T) {
	w := &channelMonitorV3ProbeWriter{header: make(http.Header)}
	_, err := w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"1\"},\"finish_reason\":\"stop\"}]}\n\n"))
	require.NoError(t, err)
	require.True(t, channelMonitorV3ProbeResponseOK(w.body.Bytes()))
	_, err = w.Write([]byte(strings.Repeat("x", 64*1024)))
	require.NoError(t, err)
	require.True(t, w.overflow, "a later error outside the capture bound must not turn the check green")
}

func TestChannelMonitorV3ProbePreservesOwnedDeadline(t *testing.T) {
	for _, detach := range []func(context.Context) (context.Context, context.CancelFunc){
		detachUpstreamContext,
		func(ctx context.Context) (context.Context, context.CancelFunc) {
			return detachStreamUpstreamContext(ctx, true)
		},
	} {
		base, cancel := context.WithTimeout(context.Background(), time.Second)
		ctx := context.WithValue(base, channelMonitorV3ProbeGroupContextKey{}, int64(4))
		probe, release := detach(ctx)
		_, hasDeadline := probe.Deadline()
		require.True(t, hasDeadline)
		cancel()
		require.ErrorIs(t, probe.Err(), context.Canceled)
		release()
		ordinary, release := detach(base)
		require.NoError(t, ordinary.Err(), "ordinary streaming clients retain their existing usage-drain behavior")
		release()
	}
}
