package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeCodexBillingErrorCode(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{
			name: "subscription",
			code: "USAGE_LIMIT_EXCEEDED",
			want: "USAGE_LIMIT_EXCEEDED",
		},
		{
			name: "balance with whitespace",
			code: "  INSUFFICIENT_BALANCE  ",
			want: "INSUFFICIENT_BALANCE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, ok := NormalizeCodexBillingErrorCode(tt.code)

			require.True(t, ok)
			require.Equal(t, tt.want, code)
		})
	}
}

func TestNormalizeCodexBillingErrorCodeRejectsUnrelatedCodes(t *testing.T) {
	code, ok := NormalizeCodexBillingErrorCode("rate_limit_exceeded")

	require.False(t, ok)
	require.Empty(t, code)
}

func TestIsCodexBillingErrorClientByHeadersUsesStrictUserAgentBoundary(t *testing.T) {
	const claudianUA = "claudian/0.153.4 (Windows 10.0.26200; x86_64) unknown (claudian; 1.0.0)"

	require.True(t, IsCodexBillingErrorClientByHeaders("codex_cli_rs/0.145.0", ""))
	require.True(t, IsCodexBillingErrorClientByHeaders("Mozilla/5.0", "codex_cli_rs"))
	require.True(t, IsCodexBillingErrorClientByHeaders(claudianUA, ""))
	require.True(t, IsCodexBillingErrorClientByHeaders(claudianUA, "claudian"))
	require.True(t, IsCodexBillingErrorClientByHeaders(
		"claudian/0.154.0-alpha.6.2 (Mac OS 26.6.2; arm64) unknown (claudian; 1.0.0)",
		"claudian",
	))
	require.False(t, IsCodexBillingErrorClientByHeaders("Mozilla/5.0 codex_cli_rs/0.145.0", ""))
	require.False(t, IsCodexBillingErrorClientByHeaders("Mozilla/5.0 claudian/0.153.4", ""))
	require.False(t, IsCodexBillingErrorClientByHeaders("claudian_evil/0.153.4", ""))
	require.False(t, IsCodexBillingErrorClientByHeaders("claudian/invalid unknown (claudian; 1.0.0)", ""))
	require.False(t, IsCodexBillingErrorClientByHeaders("claudian/0.153.4 unknown", ""))
	require.False(t, IsCodexBillingErrorClientByHeaders("claudian/0.153.4 unknown (other; 1.0.0)", ""))
	require.False(t, IsCodexBillingErrorClientByHeaders(claudianUA, "other"))
	require.False(t, IsCodexBillingErrorClientByHeaders("Mozilla/5.0", "claudian"))

	require.False(t, IsCodexOfficialClientRequestStrict(claudianUA))
	require.False(t, IsCodexOfficialClientByHeaders(claudianUA, "claudian"))
	_, _, paired := PairCodexClientIdentity(claudianUA)
	require.False(t, paired)
}
