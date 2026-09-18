package dto

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAccountDTOHidesCodexPinsWithoutMutatingServiceAccount(t *testing.T) {
	source := &service.Account{ID: 7, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Extra: map[string]any{service.PinnedCodexTurnStatesExtraKey: map[string]any{"gpt-a": map[string]any{"state": "private-pin-canary"}}, "custom": "visible"}}
	for _, mapper := range []func(*service.Account) *Account{AccountFromServiceShallow, AccountFromService} {
		got := mapper(source)
		require.NotContains(t, got.Extra, service.PinnedCodexTurnStatesExtraKey)
		payload, err := json.Marshal(got)
		require.NoError(t, err)
		require.NotContains(t, string(payload), "private-pin-canary")
		require.Equal(t, "visible", got.Extra["custom"])
		require.Contains(t, source.Extra, service.PinnedCodexTurnStatesExtraKey)
	}
}
