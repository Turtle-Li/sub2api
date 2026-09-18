package service

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountGetPinnedCodexTurnState(t *testing.T) {
	now := time.Now()
	future := now.Add(1 * time.Hour)
	past := now.Add(-10 * time.Minute)

	acc := &Account{
		ID:       11,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			PinnedCodexTurnStatesExtraKey: map[string]any{
				"gpt-6-astra": map[string]any{
					"state":      "gAAAAAB_astra_292",
					"state_len":  292,
					"expires_at": future.Format(time.RFC3339),
				},
				"gpt-5.6-sol": map[string]any{
					"state":      "gAAAAAB_sol_312",
					"state_len":  312,
					"expires_at": future.Format(time.RFC3339),
				},
				"gpt-5.6-terra": map[string]any{
					"state":      "gAAAAAB_terra_expired",
					"state_len":  312,
					"expires_at": past.Format(time.RFC3339),
				},
				"simple-model": "gAAAAAB_simple_state",
			},
		},
	}

	t.Run("exact match active state", func(t *testing.T) {
		require.Equal(t, "gAAAAAB_astra_292", acc.GetPinnedCodexTurnState("gpt-6-astra"))
		require.Equal(t, "gAAAAAB_sol_312", acc.GetPinnedCodexTurnState("gpt-5.6-sol"))
	})

	t.Run("case-insensitive match", func(t *testing.T) {
		require.Equal(t, "gAAAAAB_astra_292", acc.GetPinnedCodexTurnState("GPT-6-ASTRA"))
	})

	t.Run("prefix alias match with date suffix", func(t *testing.T) {
		require.Equal(t, "gAAAAAB_astra_292", acc.GetPinnedCodexTurnState("gpt-6-astra-20260301"))
		require.Equal(t, "gAAAAAB_sol_312", acc.GetPinnedCodexTurnState("gpt-5.6-sol-20260301"))
	})

	t.Run("strict cross-model isolation: prefix cannot match different model", func(t *testing.T) {
		accWithBroadModels := &Account{
			ID:       14,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				PinnedCodexTurnStatesExtraKey: map[string]any{
					"gpt-5": "gAAAAAB_gpt5_state",
					"gpt-6": "gAAAAAB_gpt6_state",
				},
			},
		}
		// gpt-5 must NEVER match gpt-5-codex
		require.Empty(t, accWithBroadModels.GetPinnedCodexTurnState("gpt-5-codex"))
		// gpt-6 must NEVER match gpt-6-astra
		require.Empty(t, accWithBroadModels.GetPinnedCodexTurnState("gpt-6-astra"))
		// but exact match and date-snapshots of the same model do match
		require.Equal(t, "gAAAAAB_gpt5_state", accWithBroadModels.GetPinnedCodexTurnState("gpt-5"))
		require.Equal(t, "gAAAAAB_gpt5_state", accWithBroadModels.GetPinnedCodexTurnState("gpt-5-20260301"))
	})

	t.Run("expired state returns empty", func(t *testing.T) {
		require.Empty(t, acc.GetPinnedCodexTurnState("gpt-5.6-terra"))
	})

	t.Run("unconfigured model returns empty", func(t *testing.T) {
		require.Empty(t, acc.GetPinnedCodexTurnState("gpt-5.6-luna"))
		require.Empty(t, acc.GetPinnedCodexTurnState("claude-3-5-sonnet"))
	})

	t.Run("non-OAuth account returns empty and does not inject", func(t *testing.T) {
		apiKeyAcc := &Account{
			ID:       12,
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Extra: map[string]any{
				PinnedCodexTurnStatesExtraKey: map[string]any{
					"gpt-6-astra": "gAAAAAB_astra_292",
				},
			},
		}
		require.Empty(t, apiKeyAcc.GetPinnedCodexTurnState("gpt-6-astra"))

		h := make(http.Header)
		h.Set("x-existing-header", "keep-me")
		applied := applyPinnedCodexTurnState(h, apiKeyAcc, "gpt-6-astra")
		require.False(t, applied)
		require.Empty(t, h.Get(openAICodexTurnStateHeader))
		require.Equal(t, "keep-me", h.Get("x-existing-header"))
	})

	t.Run("unconfigured account does not modify headers", func(t *testing.T) {
		unconfiguredAcc := &Account{
			ID:       13,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{},
		}
		require.Empty(t, unconfiguredAcc.GetPinnedCodexTurnState("gpt-6-astra"))

		h := make(http.Header)
		h.Set("x-codex-turn-state", "original-client-state")
		applied := applyPinnedCodexTurnState(h, unconfiguredAcc, "gpt-6-astra")
		require.False(t, applied)
		// Headers remain completely untouched!
		require.Equal(t, "original-client-state", h.Get(openAICodexTurnStateHeader))
	})

	t.Run("invalid header value is rejected and headers left untouched", func(t *testing.T) {
		corruptAcc := &Account{
			ID:       15,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				PinnedCodexTurnStatesExtraKey: map[string]any{
					"gpt-6-astra": "gAAAAAB_bad\r\nInject: evil",
				},
			},
		}
		h := make(http.Header)
		h.Set("x-codex-turn-state", "safe-state")
		applied := applyPinnedCodexTurnState(h, corruptAcc, "gpt-6-astra")
		require.False(t, applied)
		// Header untouched
		require.Equal(t, "safe-state", h.Get(openAICodexTurnStateHeader))
	})

	t.Run("applyPinnedCodexTurnState injects header", func(t *testing.T) {
		headers := make(http.Header)
		applied := applyPinnedCodexTurnState(headers, acc, "gpt-6-astra")
		require.True(t, applied)
		require.Equal(t, "gAAAAAB_astra_292", headers.Get(openAICodexTurnStateHeader))

		// When model has no pinned state, header is unchanged
		headers2 := make(http.Header)
		headers2.Set(openAICodexTurnStateHeader, "existing-state")
		applied2 := applyPinnedCodexTurnState(headers2, acc, "gpt-5.6-terra")
		require.False(t, applied2)
		require.Equal(t, "existing-state", headers2.Get(openAICodexTurnStateHeader))
	})
}
