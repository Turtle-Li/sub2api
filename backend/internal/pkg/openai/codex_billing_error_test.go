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
	require.True(t, IsCodexBillingErrorClientByHeaders("codex_cli_rs/0.145.0", ""))
	require.True(t, IsCodexBillingErrorClientByHeaders("Mozilla/5.0", "codex_cli_rs"))
	require.False(t, IsCodexBillingErrorClientByHeaders("Mozilla/5.0 codex_cli_rs/0.145.0", ""))
}
