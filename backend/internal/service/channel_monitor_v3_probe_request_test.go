//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestChannelMonitorV3ProbeAnthropicKeepsWireInputSmall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		kind       string
		restricted bool
		cli        bool
		runner     bool
		disabled   bool
	}{
		{"api_key", AccountTypeAPIKey, false, false, true, false},
		{"restricted_api_key", AccountTypeAPIKey, true, true, true, false},
		{"oauth", AccountTypeOAuth, false, true, true, false},
		{"restricted_oauth", AccountTypeOAuth, true, true, true, false},
		{"setup_token", AccountTypeSetupToken, false, true, true, false},
		{"without_runner_context", AccountTypeOAuth, false, true, false, false},
		{"injection_disabled", AccountTypeOAuth, false, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a native provider receipt and the actual forwarding service.
			stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_probe\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":20}}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"H\"}}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"},\"usage\":{\"output_tokens\":1}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}}
			probe := &channelMonitorV3GatewayProbe{gateway: newForwardPartialUsageServiceForTest(upstream)}
			if tc.disabled {
				resetGatewayForwardingSettingsCacheForTest(t)
				probe.gateway.settingService = NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
					SettingKeyEnableClaudeOAuthSystemPromptInjection: "false",
				}}, &config.Config{})
			}
			account := newAnthropicAPIKeyAccountForTest()
			if tc.kind != AccountTypeAPIKey {
				account = newAnthropicOAuthAccountForPartialUsageTest()
				account.Type = tc.kind
			}
			group := &Group{ID: 10, Platform: PlatformAnthropic, ClaudeCodeOnly: tc.restricted}
			ctx := context.Background()
			if tc.runner {
				ctx = context.WithValue(ctx, channelMonitorV3ProbeGroupContextKey{}, group.ID)
			}

			// When the scheduled check passes through the real gateway.
			result := probe.checkAccount(ctx, group, "claude-haiku-4-5", account)

			// Then the wire request stays tiny, without the general OAuth expansion.
			require.Equal(t, "success", result.Outcome)
			require.NotNil(t, upstream.lastReq)
			require.EqualValues(t, 1, gjson.GetBytes(upstream.lastBody, "max_tokens").Int())
			messages := gjson.GetBytes(upstream.lastBody, "messages").Array()
			require.Len(t, messages, 1)
			content := messages[0].Get("content")
			if content.IsArray() {
				blocks := content.Array()
				require.Len(t, blocks, 1)
				content = blocks[0].Get("text")
			}
			require.LessOrEqual(t, len(content.String()), 3, "connectivity needs only a greeting")
			system := gjson.GetBytes(upstream.lastBody, "system")
			if account.IsOAuth() {
				require.Equal(t, "Bearer oauth-token", getHeaderRaw(upstream.lastReq.Header, "Authorization"))
				require.Contains(t, getHeaderRaw(upstream.lastReq.Header, "Anthropic-Beta"), "oauth-2025-04-20")
				if tc.disabled {
					require.False(t, system.Exists(), "respect the operator's injection switch")
					return
				}
				require.True(t, systemHasBillingAttributionBlock(upstream.lastBody), "retain OAuth billing attribution")
				chars := 0
				for _, block := range system.Array() {
					chars += len(block.Get("text").String())
				}
				if tc.runner {
					require.LessOrEqual(t, chars, 300, "scheduled checks omit agent instructions")
				} else {
					require.Greater(t, chars, 1000, "ordinary forwarding retains its configured system behavior")
				}
				require.True(t, isOfficialClaudeCLIUserAgent(getHeaderRaw(upstream.lastReq.Header, "User-Agent")))
			} else if tc.cli {
				blocks := system.Array()
				require.Len(t, blocks, 1, "the native account-test identity must not expand into an agent prompt")
				require.LessOrEqual(t, len(blocks[0].Get("text").String()), 100)
				require.True(t, isOfficialClaudeCLIUserAgent(getHeaderRaw(upstream.lastReq.Header, "User-Agent")))
			} else {
				require.False(t, system.Exists())
				require.False(t, gjson.GetBytes(upstream.lastBody, "metadata").Exists())
			}
		})
	}
}
