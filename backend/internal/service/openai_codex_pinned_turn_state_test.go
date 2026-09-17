package service

import (
	"context"
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
		ID: 11,
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

	t.Run("expired state returns empty", func(t *testing.T) {
		require.Empty(t, acc.GetPinnedCodexTurnState("gpt-5.6-terra"))
	})

	t.Run("unconfigured model returns empty", func(t *testing.T) {
		require.Empty(t, acc.GetPinnedCodexTurnState("gpt-5.6-luna"))
		require.Empty(t, acc.GetPinnedCodexTurnState("claude-3-5-sonnet"))
	})

	t.Run("simple string entry without expiration", func(t *testing.T) {
		require.Equal(t, "gAAAAAB_simple_state", acc.GetPinnedCodexTurnState("simple-model"))
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
		ID:    11,
		Extra: make(map[string]any),
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
}
