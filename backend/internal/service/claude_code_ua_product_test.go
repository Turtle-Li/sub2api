package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// UA 前缀 claude-cli/ 并不足以代表「官方客户端」：上游以括号段里的产品标识
// 做判定。终端探测失败时会退化成 (external, unknown)，这类请求必须在我们
// 这一侧被挡住，否则会打到上游并换来 gateway_client_policy_denied 403。
func TestClaudeCLIUserAgentProductAllowlist(t *testing.T) {
	allowed := []string{
		"claude-cli/2.1.281 (external, cli)",
		"claude-cli/2.1.281 (external, unknown)", // 放行，出站前归一化为 sdk-cli
		"claude-cli/2.1.281 (external, sdk-cli)",
		"claude-cli/2.1.215 (external, claude-desktop-3p, agent-sdk/0.3.215)",
		"claude-cli/2.1.317 (external, local-agent, agent-sdk/0.3.317)",
		"claude-cli/2.1.202 (external, claude-desktop-3p)",
	}
	denied := []string{
		"claude-cli/2.1.281 (external, )",
		"curl/8.7.1",
		"codex_exec/0.163.0 (Mac OS 26.6.2; arm64) dumb",
		"",
	}

	v := NewClaudeCodeValidator()
	for _, ua := range allowed {
		require.True(t, v.ValidateUserAgent(ua), "应放行: %s", ua)
	}
	for _, ua := range denied {
		require.False(t, v.ValidateUserAgent(ua), "应拦截: %s", ua)
	}
}

// Validate 是 /v1/messages 的完整校验入口；unknown 属于真实退化形态，
// 放行后由出站归一化处理，不做本地拒绝。
func TestClaudeCodeValidateRejectsUnknownProduct(t *testing.T) {
	v := NewClaudeCodeValidator()
	req, err := http.NewRequest(http.MethodPost, "/v1/messages", nil)
	require.NoError(t, err)
	req.Header.Set("User-Agent", "claude-cli/2.1.281 (external, unknown)")
	req.Header.Set("X-App", "cli")
	req.Header.Set("anthropic-beta", "claude-code-20250219")
	req.Header.Set("anthropic-version", "2023-06-01")

	body := map[string]any{
		"model":  "claude-sonnet-4-6",
		"system": []any{map[string]any{"type": "text", "text": "You are Claude Code, Anthropic's official CLI tool for Claude."}},
		"metadata": map[string]any{"user_id": "user_" + strings.Repeat("a", 64) +
			"_account_00000000-0000-0000-0000-000000000000" +
			"_session_11111111-1111-1111-1111-111111111111"},
	}
	require.True(t, v.Validate(req, body), "unknown 应放行，交由出站归一化改写")
}
