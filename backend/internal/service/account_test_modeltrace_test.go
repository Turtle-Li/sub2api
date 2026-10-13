//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelTraceProbeDoesNotChangeAvailability(t *testing.T) {
	for _, observational := range []bool{false, true} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
			c, _ := newTestContext()
			response := newJSONResponse(status, `{"error":{"message":"upstream error","resets_in_seconds":60}}`)
			response.Header.Set("x-codex-primary-used-percent", "100")
			response.Header.Set("x-codex-primary-reset-after-seconds", "60")
			response.Header.Set("x-codex-primary-window-minutes", "300")
			repo := &openAIAccountTestRepo{}
			upstream := &queuedHTTPUpstream{responses: []*http.Response{response}}
			svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
			account := &Account{ID: 89, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{"access_token": "test-token"}}
			if observational {
				repo.accountsByID = map[int64]*Account{account.ID: account}
				_, err := svc.RunModelTraceProbe(context.Background(), account.ID, "gpt-5.4", "challenge", "low")
				require.Error(t, err)
			} else {
				require.Error(t, svc.testOpenAIAccountConnection(c, account, "gpt-5.4", "challenge", "", AccountTestOptions{ReasoningEffort: "low"}))
			}
			if observational {
				require.Zero(t, repo.setErrorID)
				require.Zero(t, repo.rateLimitedID)
				require.Zero(t, repo.clearedErrorID)
				require.Nil(t, repo.updatedExtra)
			} else if status == http.StatusUnauthorized {
				require.Equal(t, account.ID, repo.setErrorID)
			} else {
				require.Equal(t, account.ID, repo.rateLimitedID)
			}
		}
	}
}

func TestModelTraceReasoningPayloadUsesProtocolFields(t *testing.T) {
	responses := createOpenAITestPayload("gpt-5.4", true, "challenge", "low")
	require.NotContains(t, responses, "reasoning_effort")
	require.Equal(t, map[string]any{"effort": "low"}, responses["reasoning"])
	chat := createOpenAIChatCompletionsTestPayload("gpt-5.4", "challenge", "low")
	require.NotContains(t, chat, "reasoning")
	require.Equal(t, "low", chat["reasoning_effort"])
}
