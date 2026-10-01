package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCodexDegradationProbeRequest(t *testing.T) {
	req, err := normalizeCodexDegradationProbeRequest(CodexDegradationProbeRequest{
		AccountIDs: []int64{3, 3, 1, 0},
		Model:      " gpt-5.6-terra ",
		Effort:     "HIGH",
		Prompt:     "  1+1?  ",
	})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 3}, req.AccountIDs)
	require.Equal(t, "gpt-5.6-terra", req.Model)
	require.Equal(t, "high", req.Effort)
	require.Equal(t, "1+1?", req.Prompt)

	tooMany := make([]int64, codexDegradationProbeMaxAccounts+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	cases := []CodexDegradationProbeRequest{
		{AccountIDs: []int64{1}, Model: "gpt-5.6-terra"},
		{AccountIDs: []int64{1}, Prompt: "x"},
		{AccountIDs: nil, Model: "gpt-5.6-terra", Prompt: "x"},
		{AccountIDs: tooMany, Model: "gpt-5.6-terra", Prompt: "x"},
		{AccountIDs: []int64{1}, Model: "gpt-5.6-terra", Prompt: "x", Effort: "turbo"},
		{AccountIDs: []int64{1}, Model: "gpt-5.6-terra", Prompt: strings.Repeat("字", codexDegradationProbeMaxPromptRunes+1)},
	}
	for i, tc := range cases {
		_, err := normalizeCodexDegradationProbeRequest(tc)
		require.Error(t, err, "case %d", i)
	}
}

func probeSSE(events ...string) string {
	var b strings.Builder
	for _, event := range events {
		_, _ = b.WriteString("data: ")
		_, _ = b.WriteString(event)
		_, _ = b.WriteString("\n\n")
	}
	return b.String()
}

func TestParseCodexDegradationProbeStream(t *testing.T) {
	out, err := parseCodexDegradationProbeStream(strings.NewReader(probeSSE(
		`{"type":"response.created"}`,
		`{"type":"response.output_text.delta","delta":"<html>"}`,
		`{"type":"response.output_text.delta","delta":"</html>"}`,
		`{"type":"response.completed","response":{"reasoning":{"effort":"high"},"usage":{"input_tokens":12,"output_tokens":34,"output_tokens_details":{"reasoning_tokens":20}}}}`,
	)))
	require.NoError(t, err)
	require.Equal(t, "<html></html>", out.Content)
	require.Equal(t, "high", out.AppliedEffort)
	require.Equal(t, 12, out.InputTokens)
	require.Equal(t, 34, out.OutputTokens)
	require.Equal(t, 20, out.ReasoningTokens)

	out, err = parseCodexDegradationProbeStream(strings.NewReader(probeSSE(
		`{"type":"response.completed","response":{"output":[{"type":"reasoning"},{"type":"message","content":[{"type":"output_text","text":"final"}]}]}}`,
	)))
	require.NoError(t, err)
	require.Equal(t, "final", out.Content)

	out, err = parseCodexDegradationProbeStream(strings.NewReader(probeSSE(
		`{"type":"response.output_text.delta","delta":"partial"}`,
		`{"type":"response.failed","response":{"error":{"message":"boom"}}}`,
	)))
	require.EqualError(t, err, "boom")
	require.Equal(t, "partial", out.Content)

	out, err = parseCodexDegradationProbeStream(strings.NewReader(probeSSE(`{"type":"response.output_text.delta","delta":"cut"}`)))
	require.ErrorContains(t, err, "before response.completed")
	require.Equal(t, "cut", out.Content)
}

func TestCodexDegradationProbeAccountSupported(t *testing.T) {
	parent := int64(1)
	require.True(t, codexDegradationProbeAccountSupported(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	require.False(t, codexDegradationProbeAccountSupported(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}))
	require.False(t, codexDegradationProbeAccountSupported(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent}))
	require.False(t, codexDegradationProbeAccountSupported(nil))
	var _ codexDegradationProbeRunner = (*AccountTestService)(nil)
}

func newPinnedProbeAccount(id int64, model string, expiresAt time.Time) Account {
	return Account{
		ID:          id,
		Name:        "probe",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			PinnedCodexTurnStatesExtraKey: map[string]any{
				model: map[string]any{
					"state":             "gAAAAAB_probe_state",
					"expires_at":        expiresAt.Format(time.RFC3339),
					"cookie":            "__cflb=probe-lb",
					"cookie_expires_at": expiresAt.Format(time.RFC3339),
				},
			},
		},
	}
}

func TestCodexDegradationProbeCreateRequiresPinnedState(t *testing.T) {
	const model = "gpt-5.6-sol"
	future := time.Now().Add(time.Hour)
	pinned := newPinnedProbeAccount(1, codexDegradationProbeUpstreamModel(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, model), future)
	expired := newPinnedProbeAccount(2, model, time.Now().Add(-time.Minute))
	bare := Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	apiKey := Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	svc := NewCodexDegradationProbeService(nil, stubOpenAIAccountRepo{accounts: []Account{pinned, expired, bare, apiKey}}, nil)

	for _, tc := range []struct {
		ids    []int64
		reason string
		inMsg  string
	}{
		{ids: []int64{1, 2}, reason: "CODEX_DEGRADATION_PROBE_NO_PINNED_STATE", inMsg: "account 2"},
		{ids: []int64{3, 1}, reason: "CODEX_DEGRADATION_PROBE_NO_PINNED_STATE", inMsg: "account 3"},
		{ids: []int64{4}, reason: "CODEX_DEGRADATION_PROBE_INVALID", inMsg: "account 4"},
		{ids: []int64{9}, reason: "CODEX_DEGRADATION_PROBE_INVALID", inMsg: "account 9"},
	} {
		_, err := svc.Create(context.Background(), CodexDegradationProbeRequest{AccountIDs: tc.ids, Model: model, Prompt: "x"})
		require.Error(t, err, "ids %v", tc.ids)
		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err), "ids %v", tc.ids)
		require.Equal(t, tc.reason, infraerrors.Reason(err), "ids %v", tc.ids)
		require.Contains(t, infraerrors.Message(err), tc.inMsg, "ids %v", tc.ids)
	}
}

func TestRunCodexDegradationProbeInjectsPinnedTurnState(t *testing.T) {
	const model = "gpt-5.6-sol"
	upstreamModel := codexDegradationProbeUpstreamModel(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, model)
	account := newPinnedProbeAccount(1, upstreamModel, time.Now().Add(time.Hour))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(probeSSE(
			`{"type":"response.output_text.delta","delta":"ok"}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":2}}}`,
		))),
	}}
	svc := &AccountTestService{httpUpstream: upstream}

	out, err := svc.runCodexDegradationProbe(context.Background(), &account, model, "high", "hi")
	require.NoError(t, err)
	require.Equal(t, "ok", out.Content)
	require.Len(t, upstream.requests, 1)
	req := upstream.requests[0]
	require.Equal(t, chatgptCodexAPIURL, req.URL.String())
	require.Equal(t, "gAAAAAB_probe_state", req.Header.Get(openAICodexTurnStateHeader))
	require.Contains(t, req.Header.Get("Cookie"), "__cflb=probe-lb")
	require.Equal(t, "Bearer test-token", req.Header.Get("Authorization"))
}

func TestRunCodexDegradationProbeFailsWhenPinnedStateExpired(t *testing.T) {
	const model = "gpt-5.6-sol"
	account := newPinnedProbeAccount(1, model, time.Now().Add(-time.Minute))
	upstream := &httpUpstreamRecorder{}
	svc := &AccountTestService{httpUpstream: upstream}

	_, err := svc.runCodexDegradationProbe(context.Background(), &account, model, "", "hi")
	require.EqualError(t, err, "票据已过期或不存在")
	require.Empty(t, upstream.requests)
}
