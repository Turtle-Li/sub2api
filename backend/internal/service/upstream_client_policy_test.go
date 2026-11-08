//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 上游「客户端不在允许范围」的 403 是请求级信号：换客户端即可通过，
// 账号凭据本身没有失效。它不能把账号永久置为 error，否则任何一个非官方
// 客户端（Codex、脚本、探针）打进来一次，整组正常调用都会被连带打死。
func TestClientPolicyDenied403DoesNotDisableAccount(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "第三方 Sub2API 系网关中文分组限制",
			body: `{"error":{"code":"gateway_client_policy_denied","message":"当前分组限制了可用的客户端，你使用的客户端不在允许范围内。请改用分组允许的官方客户端（Claude Code 终端、Claude 桌面版或 VS Code 插件，以分组说明为准），或切换到其他分组。","type":"permission_error"},"request_id":"gw_1","type":"error"}`,
		},
		{
			name: "英文分组限制",
			body: `{"error":{"message":"This group is restricted to Claude Code clients (/v1/messages only)","type":"permission_error"}}`,
		},
		{
			name: "调度层本地文案",
			body: `{"error":{"message":"No available accounts: this group only allows Claude Code clients"}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &rateLimitAccountRepoStub{}
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			account := &Account{ID: 248, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}

			svc.HandleUpstreamError(
				context.Background(), account, http.StatusForbidden, http.Header{}, []byte(tc.body),
			)

			require.Zero(t, repo.setErrorCalls, "客户端策略拒绝不得永久禁用账号")
			require.Zero(t, repo.rateLimitedCalls, "客户端策略拒绝不得进入限流冷却")
			require.Zero(t, repo.lastErrorID)
		})
	}
}

// 真正的账号级 403 仍然必须处罚，避免这次豁免把封禁信号也一起放过。
func TestGeneric403StillDisablesAnthropicAccount(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 248, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}

	svc.HandleUpstreamError(
		context.Background(), account, http.StatusForbidden, http.Header{},
		[]byte(`{"error":{"message":"invalid api key","type":"authentication_error"}}`),
	)

	require.Equal(t, 1, repo.setErrorCalls, "普通 403 仍按账号级鉴权失败处理")
}

func TestIsUpstreamClientPolicyDeniedMarkers(t *testing.T) {
	require.True(t, isUpstreamClientPolicyDenied("", []byte(`{"code":"gateway_client_policy_denied"}`)))
	require.True(t, isUpstreamClientPolicyDenied("客户端不在允许范围内", nil))
	require.True(t, isUpstreamClientPolicyDenied("", []byte(`This group is restricted to Claude Code clients`)))
	require.True(t, isUpstreamClientPolicyDenied("this group only allows Claude Code clients", nil))

	require.False(t, isUpstreamClientPolicyDenied("", []byte(`<html><body>cf-error-code="1010"</body></html>`)))
	require.False(t, isUpstreamClientPolicyDenied("invalid api key", []byte(`{"error":{"type":"authentication_error"}}`)))
	require.False(t, isUpstreamClientPolicyDenied("", nil))
}
