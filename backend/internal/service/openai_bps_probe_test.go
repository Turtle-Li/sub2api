package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeBPSProbeRequest(t *testing.T) {
	req, err := normalizeBPSProbeRequest(BPSProbeRequest{
		AccountIDs: []int64{3, 3, 1},
		Paths:      []string{"native", "bps", "other"},
		Model:      " gpt-5.6-terra ",
		Effort:     "HIGH",
		Prompt:     "  1+1?  ",
	})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 3}, req.AccountIDs)
	require.Equal(t, []string{BPSProbePathBPS, BPSProbePathNative}, req.Paths)
	require.Equal(t, "high", req.Effort)
	require.Equal(t, "1+1?", req.Prompt)

	cases := []BPSProbeRequest{
		{AccountIDs: []int64{1}, Paths: []string{"bps"}, Model: "gpt-5.6-terra"},
		{AccountIDs: []int64{1}, Paths: []string{"bps"}, Prompt: "x"},
		{AccountIDs: nil, Paths: []string{"bps"}, Model: "gpt-5.6-terra", Prompt: "x"},
		{AccountIDs: []int64{1}, Paths: []string{"x"}, Model: "gpt-5.6-terra", Prompt: "x"},
		{AccountIDs: []int64{1}, Paths: []string{"bps"}, Model: "gpt-5.6-terra", Prompt: "x", Effort: "turbo"},
		{AccountIDs: []int64{1}, Paths: []string{"bps"}, Model: "gpt-6-sol", Prompt: "x"},
		{AccountIDs: []int64{1}, Paths: []string{"bps"}, Model: "gpt-5.6-terra", Prompt: strings.Repeat("字", bpsProbeMaxPromptRunes+1)},
	}
	for i, tc := range cases {
		_, err := normalizeBPSProbeRequest(tc)
		require.Error(t, err, "case %d", i)
	}

	// 原生路径不受 BPS 模型白名单限制。
	_, err = normalizeBPSProbeRequest(BPSProbeRequest{AccountIDs: []int64{1}, Paths: []string{"native"}, Model: "gpt-6-sol", Prompt: "x"})
	require.NoError(t, err)
}

func probeSSE(events ...string) string {
	var b strings.Builder
	for _, event := range events {
		b.WriteString("data: ")
		b.WriteString(event)
		b.WriteString("\n\n")
	}
	return b.String()
}

func TestParseOpenAIProbeStream(t *testing.T) {
	out, err := parseOpenAIProbeStream(strings.NewReader(probeSSE(
		`{"type":"response.created"}`,
		`{"type":"response.output_text.delta","delta":"<html>"}`,
		`{"type":"response.output_text.delta","delta":"</html>"}`,
		`{"type":"response.completed","response":{"usage":{"input_tokens":12,"output_tokens":34,"output_tokens_details":{"reasoning_tokens":20}}}}`,
	)))
	require.NoError(t, err)
	require.Equal(t, "<html></html>", out.Content)
	require.Equal(t, 12, out.InputTokens)
	require.Equal(t, 34, out.OutputTokens)
	require.Equal(t, 20, out.ReasoningTokens)

	out, err = parseOpenAIProbeStream(strings.NewReader(probeSSE(
		`{"type":"response.completed","response":{"output":[{"type":"reasoning"},{"type":"message","content":[{"type":"output_text","text":"final"}]}]}}`,
	)))
	require.NoError(t, err)
	require.Equal(t, "final", out.Content)

	out, err = parseOpenAIProbeStream(strings.NewReader(probeSSE(
		`{"type":"response.output_text.delta","delta":"partial"}`,
		`{"type":"response.failed","response":{"error":{"message":"boom"}}}`,
	)))
	require.EqualError(t, err, "boom")
	require.Equal(t, "partial", out.Content)

	out, err = parseOpenAIProbeStream(strings.NewReader(probeSSE(`{"type":"response.output_text.delta","delta":"cut"}`)))
	require.ErrorContains(t, err, "before response.completed")
	require.Equal(t, "cut", out.Content)
}

func TestBPSProbeAccountSupported(t *testing.T) {
	parent := int64(1)
	require.True(t, bpsProbeAccountSupported(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	require.False(t, bpsProbeAccountSupported(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}))
	require.False(t, bpsProbeAccountSupported(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent}))
	require.False(t, bpsProbeAccountSupported(nil))
	var _ bpsProbeRunner = (*AccountTestService)(nil)
}
