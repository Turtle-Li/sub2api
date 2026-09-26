package service

import (
	"context"
	"net/http"
	"strings"
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

	t.Run("cookie injection and merging", func(t *testing.T) {
		cookieFuture := now.Add(240 * time.Second)
		accWithCookie := &Account{
			ID:       20,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				PinnedCodexTurnStatesExtraKey: map[string]any{
					"gpt-6-astra": map[string]any{
						"state":             "gAAAAAB_astra_292",
						"state_len":         292,
						"expires_at":        future.Format(time.RFC3339),
						"cookie":            "__cflb=02DiuF1; __oailb=node-123",
						"cookie_expires_at": cookieFuture.Format(time.RFC3339),
					},
				},
			},
		}

		// Fresh request without Cookie header
		h1 := make(http.Header)
		applied := applyPinnedCodexTurnState(h1, accWithCookie, "gpt-6-astra")
		require.True(t, applied)
		require.Equal(t, "gAAAAAB_astra_292", h1.Get(openAICodexTurnStateHeader))
		require.Equal(t, "__cflb=02DiuF1; __oailb=node-123", h1.Get("Cookie"))

		// Existing client cookie should be preserved and merged
		h2 := make(http.Header)
		h2.Set("Cookie", "session_id=xyz987; __cflb=old_val")
		applied2 := applyPinnedCodexTurnState(h2, accWithCookie, "gpt-6-astra")
		require.True(t, applied2)
		mergedCookie := h2.Get("Cookie")
		require.Contains(t, mergedCookie, "session_id=xyz987")
		require.Contains(t, mergedCookie, "__cflb=02DiuF1")
		require.Contains(t, mergedCookie, "__oailb=node-123")
		require.NotContains(t, mergedCookie, "old_val")
	})

	t.Run("account-level fallback cookie when model has no cookie", func(t *testing.T) {
		cookieFuture := now.Add(240 * time.Second)
		accWithFallback := &Account{
			ID:       21,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				PinnedCodexRoutingCookieExtraKey: map[string]any{
					"cookie":     "__cflb=shared_fallback; __oailb=node-shared",
					"expires_at": cookieFuture.Format(time.RFC3339),
				},
				PinnedCodexTurnStatesExtraKey: map[string]any{
					"gpt-6-astra": map[string]any{
						"state":      "gAAAAAB_astra_292",
						"state_len":  292,
						"expires_at": future.Format(time.RFC3339),
						// No model-specific cookie
					},
				},
			},
		}

		h := make(http.Header)
		applied := applyPinnedCodexTurnState(h, accWithFallback, "gpt-6-astra")
		require.True(t, applied)
		require.Equal(t, "gAAAAAB_astra_292", h.Get(openAICodexTurnStateHeader))
		require.Equal(t, "__cflb=shared_fallback; __oailb=node-shared", h.Get("Cookie"))
	})

	t.Run("expired cookie is omitted", func(t *testing.T) {
		cookiePast := now.Add(-30 * time.Second)
		accWithExpiredCookie := &Account{
			ID:       22,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				PinnedCodexTurnStatesExtraKey: map[string]any{
					"gpt-6-astra": map[string]any{
						"state":             "gAAAAAB_astra_292",
						"state_len":         292,
						"expires_at":        future.Format(time.RFC3339),
						"cookie":            "__cflb=expired_val",
						"cookie_expires_at": cookiePast.Format(time.RFC3339),
					},
				},
			},
		}

		h := make(http.Header)
		applied := applyPinnedCodexTurnState(h, accWithExpiredCookie, "gpt-6-astra")
		require.True(t, applied)
		require.Equal(t, "gAAAAAB_astra_292", h.Get(openAICodexTurnStateHeader))
		require.Empty(t, h.Get("Cookie"))
	})
}

type mockPinnedTurnStateAccountRepo struct {
	AccountRepository
	account *Account
}

func (m *mockPinnedTurnStateAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if m.account != nil && m.account.ID == id {
		return m.account, nil
	}
	return nil, ErrAccountNotFound
}

func (m *mockPinnedTurnStateAccountRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	if m.account == nil || m.account.ID != id {
		return ErrAccountNotFound
	}
	if m.account.Extra == nil {
		m.account.Extra = make(map[string]any)
	}
	for k, v := range updates {
		m.account.Extra[k] = v
	}
	return nil
}

func TestAdminServicePinnedCodexTurnState(t *testing.T) {
	acc := &Account{
		ID:       11,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra:    make(map[string]any),
	}
	repo := &mockPinnedTurnStateAccountRepo{account: acc}
	svc := &adminServiceImpl{accountRepo: repo}
	ctx := context.Background()

	exp := time.Now().Add(1 * time.Hour).UTC()
	// Set single
	updated, err := svc.SetPinnedCodexTurnState(ctx, 11, "gpt-6-astra", PinnedCodexTurnStateEntry{
		State:     "gAAAAAB_292",
		ExpiresAt: &exp,
		StateLen:  292,
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, "gAAAAAB_292", updated.GetPinnedCodexTurnState("gpt-6-astra"))

	// Get all
	states, err := svc.GetPinnedCodexTurnStates(ctx, 11)
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Equal(t, 292, states["gpt-6-astra"].StateLen)

	// Set batch
	_, err = svc.SetPinnedCodexTurnStates(ctx, 11, map[string]PinnedCodexTurnStateEntry{
		"gpt-5.6-sol": {
			State:     "gAAAAAB_312",
			ExpiresAt: &exp,
			StateLen:  312,
		},
	})
	require.NoError(t, err)
	states2, err := svc.GetPinnedCodexTurnStates(ctx, 11)
	require.NoError(t, err)
	require.Len(t, states2, 2)
	require.Equal(t, "gAAAAAB_312", states2["gpt-5.6-sol"].State)

	// Delete single
	_, err = svc.DeletePinnedCodexTurnState(ctx, 11, "gpt-5.6-sol")
	require.NoError(t, err)
	states3, err := svc.GetPinnedCodexTurnStates(ctx, 11)
	require.NoError(t, err)
	require.Len(t, states3, 1)
	require.NotContains(t, states3, "gpt-5.6-sol")
	require.Contains(t, states3, "gpt-6-astra")

	// Delete all
	_, err = svc.DeletePinnedCodexTurnState(ctx, 11, "all")
	require.NoError(t, err)
	states4, err := svc.GetPinnedCodexTurnStates(ctx, 11)
	require.NoError(t, err)
	require.Empty(t, states4)

	// Validation: reject non-OAuth accounts
	apiKeyAcc := &Account{
		ID:       12,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
	}
	apiKeyRepo := &mockPinnedTurnStateAccountRepo{account: apiKeyAcc}
	apiKeySvc := &adminServiceImpl{accountRepo: apiKeyRepo}
	_, err = apiKeySvc.SetPinnedCodexTurnState(ctx, 12, "gpt-6-astra", PinnedCodexTurnStateEntry{State: "gAAAAAB_valid"})
	require.Error(t, err)

	// Validation: reject invalid header characters
	_, err = svc.SetPinnedCodexTurnState(ctx, 11, "gpt-6-astra", PinnedCodexTurnStateEntry{State: "bad\r\nvalue"})
	require.Error(t, err)

	// Validation: reject oversized state
	hugeState := strings.Repeat("x", 8193)
	_, err = svc.SetPinnedCodexTurnState(ctx, 11, "gpt-6-astra", PinnedCodexTurnStateEntry{State: hugeState})
	require.Error(t, err)
}
