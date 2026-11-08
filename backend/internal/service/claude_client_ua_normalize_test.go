package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeClaudeCLIUserAgent(t *testing.T) {
	cases := map[string]string{
		"claude-cli/2.1.281 (external, unknown)":                        "claude-cli/2.1.281 (external, sdk-cli)",
		"claude-cli/2.1.281 (external, unknown, agent-sdk/0.3.1)":       "claude-cli/2.1.281 (external, sdk-cli, agent-sdk/0.3.1)",
		"claude-cli/2.1.281 (external, cli)":                            "claude-cli/2.1.281 (external, cli)",
		"claude-cli/2.1.281 (external, sdk-cli)":                        "claude-cli/2.1.281 (external, sdk-cli)",
		"claude-cli/2.1.317 (external, local-agent, agent-sdk/0.3.317)": "claude-cli/2.1.317 (external, local-agent, agent-sdk/0.3.317)",
		"curl/8.7.1": "curl/8.7.1",
	}
	for in, want := range cases {
		require.Equal(t, want, normalizeClaudeCLIUserAgent(in), "输入: %s", in)
	}
}

func TestNormalizeOutboundClaudeCLIUserAgentHeader(t *testing.T) {
	h := http.Header{}
	h.Set("User-Agent", "claude-cli/2.1.281 (external, unknown)")
	normalizeOutboundClaudeCLIUserAgent(h)
	require.Equal(t, "claude-cli/2.1.281 (external, sdk-cli)", h.Get("User-Agent"))

	untouched := http.Header{}
	untouched.Set("User-Agent", "claude-cli/2.1.281 (external, cli)")
	normalizeOutboundClaudeCLIUserAgent(untouched)
	require.Equal(t, "claude-cli/2.1.281 (external, cli)", untouched.Get("User-Agent"))

	empty := http.Header{}
	normalizeOutboundClaudeCLIUserAgent(empty)
	require.Empty(t, empty.Get("User-Agent"))
}
