//go:build unit

package service

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountTestService_APIKeyResponsesDoesNotInjectInstructions(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		model    string
		prompt   string
	}{
		{name: "compatible Claude", platform: PlatformOpenAI, model: "claude-sonnet-4-5", prompt: "hi"},
		{name: "OpenAI API key", platform: PlatformOpenAI, model: "gpt-6.1-sol", prompt: "hello"},
		{name: "fixed DeepSeek Responses", platform: PlatformDeepseek, model: "deepseek-chat", prompt: "hi"},
		{name: "fixed Kimi Responses", platform: PlatformKimi, model: "kimi-for-coding", prompt: "hi"},
		{name: "default prompt", platform: PlatformOpenAI, model: "custom-model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			account := &Account{
				ID:          501,
				Platform:    tt.platform,
				Type:        AccountTypeAPIKey,
				Concurrency: 1,
				Credentials: map[string]any{
					"api_key":      "test-key",
					"base_url":     "https://relay.example.com/v1",
					"api_protocol": APIProtocolResponses,
				},
				Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
			}
			repo := &openAIAccountTestRepo{
				mockAccountRepoForGemini: mockAccountRepoForGemini{
					accountsByID: map[int64]*Account{account.ID: account},
				},
			}
			upstream := &queuedHTTPUpstream{responses: []*http.Response{
				newJSONResponse(http.StatusOK, "data: {\"type\":\"response.completed\"}\n\n"),
			}}
			svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{}}
			c, recorder := newTestContext()

			// When
			err := svc.TestAccountConnection(c, account.ID, tt.model, tt.prompt, AccountTestModeDefault)

			// Then
			require.NoError(t, err)
			require.Len(t, upstream.requests, 1)
			body, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(body, "instructions").Exists())
			require.False(t, gjson.GetBytes(body, "store").Exists())
			require.Equal(t, tt.model, gjson.GetBytes(body, "model").String())
			wantPrompt := tt.prompt
			if wantPrompt == "" {
				wantPrompt = "hi"
			}
			require.Equal(t, wantPrompt, gjson.GetBytes(body, "input.0.content.0.text").String())
			require.Contains(t, recorder.Body.String(), `"success":true`)
		})
	}
}

func TestAccountTestService_OAuthResponsesUsesUpstreamModelInstructions(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		upstreamModel string
	}{
		{name: "GPT 6", model: "gpt-6-astra", upstreamModel: "gpt-6-astra"},
		{name: "GPT 6.1", model: "gpt-6.1-sol", upstreamModel: "gpt-6.1-sol"},
		{name: "GPT 5.2", model: "gpt-5.2", upstreamModel: "gpt-5.2"},
		{name: "normalized alias", model: "gpt-5.6", upstreamModel: "gpt-5.6-sol"},
		{name: "legacy Codex", model: "gpt-5-codex", upstreamModel: "gpt-5.3-codex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			account := &Account{
				ID:          502,
				Platform:    PlatformOpenAI,
				Type:        AccountTypeOAuth,
				Concurrency: 1,
				Credentials: map[string]any{"access_token": "test-token"},
			}
			repo := &openAIAccountTestRepo{
				mockAccountRepoForGemini: mockAccountRepoForGemini{
					accountsByID: map[int64]*Account{account.ID: account},
				},
			}
			upstream := &queuedHTTPUpstream{responses: []*http.Response{
				newJSONResponse(http.StatusOK, "data: {\"type\":\"response.completed\"}\n\n"),
			}}
			svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
			c, recorder := newTestContext()
			wantInstructions := strings.TrimSpace(openai.CodexBaseInstructionsForModel(tt.upstreamModel))
			require.NotEmpty(t, wantInstructions)

			// When
			err := svc.TestAccountConnection(c, account.ID, tt.model, "hi", AccountTestModeDefault)

			// Then
			require.NoError(t, err)
			require.Len(t, upstream.requests, 1)
			body, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			require.Equal(t, tt.upstreamModel, gjson.GetBytes(body, "model").String())
			require.True(t, wantInstructions == strings.TrimSpace(gjson.GetBytes(body, "instructions").String()),
				"wrong instruction set for upstream model %s", tt.upstreamModel)
			require.Equal(t, gjson.False, gjson.GetBytes(body, "store").Type)
			require.Equal(t, "hi", gjson.GetBytes(body, "input.0.content.0.text").String())
			require.Contains(t, recorder.Body.String(), `"success":true`)
		})
	}
}
