package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateAccountPreservesServerManagedCodexPins(t *testing.T) {
	for _, kind := range []string{"omitted", "clear", "injected"} {
		t.Run(kind, func(t *testing.T) {
			pins := map[string]any{"gpt-a": map[string]any{"state": "private-a"}, "gpt-b": map[string]any{"state": "private-b"}}
			base := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{7: {ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Extra: map[string]any{PinnedCodexTurnStatesExtraKey: pins, "quota_used": 3.0}}}}
			repo := &upstreamBillingProbeAdminRepo{base}
			extra := map[string]any{"custom": "updated"}
			if kind == "clear" {
				extra = map[string]any{}
			}
			if kind == "injected" {
				extra[PinnedCodexTurnStatesExtraKey] = map[string]any{"gpt-a": "untrusted"}
			}
			updated, err := (&adminServiceImpl{accountRepo: repo}).UpdateAccount(context.Background(), 7, &UpdateAccountInput{Extra: extra})
			require.NoError(t, err)
			require.Equal(t, pins, updated.Extra[PinnedCodexTurnStatesExtraKey])
			require.Equal(t, pins, base.accounts[7].Extra[PinnedCodexTurnStatesExtraKey])
			require.Equal(t, 3.0, updated.Extra["quota_used"])
		})
	}
}

func TestGenericExtraUpdatesCannotInjectCodexPins(t *testing.T) {
	base := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{7: {ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Extra: map[string]any{}}}}
	svc := &adminServiceImpl{accountRepo: &upstreamBillingProbeAdminRepo{base}}
	extra := func() map[string]any {
		return map[string]any{PinnedCodexTurnStatesExtraKey: map[string]any{"gpt-a": "untrusted"}, "custom": "value"}
	}
	_, err := svc.UpdateAccount(context.Background(), 7, &UpdateAccountInput{Extra: extra()})
	require.NoError(t, err)
	require.NotContains(t, base.accounts[7].Extra, PinnedCodexTurnStatesExtraKey)
	err = svc.UpdateAccountExtra(context.Background(), 7, extra())
	require.NoError(t, err)
	require.NotContains(t, base.accounts[7].Extra, PinnedCodexTurnStatesExtraKey)
	result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{7}, Extra: extra()})
	require.NoError(t, err)
	require.Equal(t, 1, result.Success)
	require.Len(t, base.bulkUpdates, 1)
	require.NotContains(t, base.bulkUpdates[0].Extra, PinnedCodexTurnStatesExtraKey)
	require.Equal(t, "value", base.bulkUpdates[0].Extra["custom"])
	account, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Name: "sample"}, extra())
	require.NoError(t, err)
	require.NotContains(t, account.Extra, PinnedCodexTurnStatesExtraKey)
}
