//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestCodexOAuthRelayDefaultEchoRetainsOutboundTier(t *testing.T) {
	for _, tier := range []string{"priority", "fast"} {
		t.Run(tier, func(t *testing.T) {
			// Given an explicitly declared API-key relay and an outbound Fast turn.
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_codex_oauth_relay": true}}
			result := &OpenAIForwardResult{ServiceTier: &tier, UpstreamResponseServiceTier: "default"}

			// When the OAuth upstream echoes default.
			resolution := ApplyOpenAIServiceTierBillingResolution(account, result)

			// Then accounting retains the final outbound tier and the observation.
			require.Equal(t, tier, resolution.Billing)
			require.Equal(t, "default", resolution.Observed)
			require.False(t, resolution.Downgraded)
			require.Same(t, &tier, result.ServiceTier)
		})
	}
}

func TestCodexOAuthRelayCreateValidatesDeclaration(t *testing.T) {
	for _, tt := range []struct {
		name, platform, accountType string
		value                       any
	}{
		{"string", PlatformOpenAI, AccountTypeAPIKey, "true"},
		{"number", PlatformOpenAI, AccountTypeAPIKey, 1},
		{"null", PlatformOpenAI, AccountTypeAPIKey, nil},
		{"oauth", PlatformOpenAI, AccountTypeOAuth, true},
		{"other platform", PlatformAnthropic, AccountTypeAPIKey, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Given an invalid declaration, including imports using CreateAccount.
			repo := &longContextBillingRepoStub{}
			svc := &adminServiceImpl{accountRepo: repo}
			input := &CreateAccountInput{Platform: tt.platform, Type: tt.accountType, SkipDefaultGroupBind: true, Extra: map[string]any{"openai_codex_oauth_relay": tt.value}}

			// When creation is attempted.
			account, err := svc.CreateAccount(context.Background(), input)

			// Then no account is persisted.
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			require.Nil(t, account)
			require.Nil(t, repo.createdAccount)
		})
	}
}

func TestCodexOAuthRelayCreatePersistsExplicitDeclaration(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			// Given an eligible API-key account with an explicit boolean.
			repo := &longContextBillingRepoStub{}
			svc := &adminServiceImpl{accountRepo: repo}

			// When creating it through the shared create/import boundary.
			account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, SkipDefaultGroupBind: true, Extra: map[string]any{OpenAICodexOAuthRelayExtraKey: enabled}})

			// Then the value is persisted without changing credential type.
			require.NoError(t, err)
			require.Equal(t, AccountTypeAPIKey, repo.createdAccount.Type)
			require.Equal(t, enabled, account.Extra[OpenAICodexOAuthRelayExtraKey])
		})
	}
}

func TestCodexOAuthRelayUpdatePreservesOmittedDeclaration(t *testing.T) {
	for _, tt := range []struct {
		name  string
		extra map[string]any
	}{
		{"nil", nil}, {"empty", map[string]any{}}, {"unrelated key", map[string]any{"other_setting": true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Given a stored opt-in and an older full-form client omitting it.
			repo := &longContextBillingRepoStub{account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_codex_oauth_relay": true}}}
			svc := &adminServiceImpl{accountRepo: repo}

			// When replacing the Extra map.
			account, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: tt.extra})

			// Then the stored declaration survives.
			require.NoError(t, err)
			require.Equal(t, true, account.Extra["openai_codex_oauth_relay"])
		})
	}
}

func TestCodexOAuthRelayUpdateAllowsExplicitDisable(t *testing.T) {
	// Given a stored opt-in.
	repo := &longContextBillingRepoStub{account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_codex_oauth_relay": true}}}
	svc := &adminServiceImpl{accountRepo: repo}

	// When an admin explicitly disables compatibility.
	account, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{"openai_codex_oauth_relay": false}})

	// Then false is saved and the public API downgrade contract returns.
	require.NoError(t, err)
	require.Equal(t, false, account.Extra["openai_codex_oauth_relay"])
	require.Equal(t, "default", ResolveOpenAIServiceTierBilling(account, "priority", "default").Billing)
}

func TestCodexOAuthRelayExtraPatchRejectsInvalidDeclaration(t *testing.T) {
	// Given the key-level merge endpoint and a malformed boolean.
	repo := &longContextBillingRepoStub{account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}
	svc := &adminServiceImpl{accountRepo: repo}

	// When merging the invalid setting.
	err := svc.UpdateAccountExtra(context.Background(), 1, map[string]any{"openai_codex_oauth_relay": "true"})

	// Then the repository is not called.
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Zero(t, repo.updateExtraCalls)
}

func TestCodexOAuthRelayBulkRejectsMixedCredentialTypes(t *testing.T) {
	// Given a bulk selection containing an API key and an OAuth credential.
	repo := &longContextBillingRepoStub{accounts: []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
	}}
	svc := &adminServiceImpl{accountRepo: repo}

	// When enabling the relay declaration on both.
	result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1, 2}, Extra: map[string]any{"openai_codex_oauth_relay": true}})

	// Then the whole update fails before any persistence.
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Nil(t, result)
	require.Zero(t, repo.bulkUpdateCalls)
}

func TestCodexOAuthRelayBulkPersistsExplicitBoolean(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			// Given an eligible bulk selection.
			repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*Account{{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}}
			svc := &adminServiceImpl{accountRepo: repo}

			// When assigning a boolean declaration to the whole selection.
			result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: map[string]any{OpenAICodexOAuthRelayExtraKey: enabled}})

			// Then both true and false reach the repository merge.
			require.NoError(t, err)
			require.Equal(t, []int64{1}, result.SuccessIDs)
			require.Equal(t, enabled, repo.lastBulkUpdate.Extra[OpenAICodexOAuthRelayExtraKey])
		})
	}
}

func TestCodexOAuthRelayBulkRejectsMalformedBoolean(t *testing.T) {
	// Given an eligible account but a numeric declaration.
	repo := &longContextBillingRepoStub{account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}
	svc := &adminServiceImpl{accountRepo: repo}

	// When applying a bulk merge.
	result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: map[string]any{OpenAICodexOAuthRelayExtraKey: 1}})

	// Then no persistence happens.
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Nil(t, result)
	require.Zero(t, repo.bulkUpdateCalls)
}

func TestCodexOAuthRelayScopedAdminCannotWriteDeclaration(t *testing.T) {
	// Given the same Extra field via a restricted administrator key.
	fields := map[string]json.RawMessage{"extra": json.RawMessage(`{"openai_codex_oauth_relay":true}`)}

	// When requesting field permissions.
	_, err := scopedAccountFieldPermissions(fields, false)

	// Then the existing Extra restriction rejects it.
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
}

func TestCodexOAuthRelayCredentialTypeChangeClearsInheritedDeclaration(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			// Given an API-key credential with a stored declaration.
			repo := &longContextBillingRepoStub{account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{OpenAICodexOAuthRelayExtraKey: enabled, "custom_setting": "preserved"}}}
			svc := &adminServiceImpl{accountRepo: repo}

			// When explicitly changing credentials to OAuth without the declaration.
			account, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Type: AccountTypeOAuth})

			// Then the incompatible inherited declaration is removed.
			require.NoError(t, err)
			require.NotContains(t, account.Extra, OpenAICodexOAuthRelayExtraKey)
			require.Equal(t, "preserved", account.Extra["custom_setting"])
		})
	}
}

func TestCodexOAuthRelayUpdateRejectsInvalidDeclaration(t *testing.T) {
	for _, tt := range []struct {
		name, accountType string
		value             any
	}{
		{"malformed boolean", AccountTypeAPIKey, "true"},
		{"enabled for OAuth", AccountTypeOAuth, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Given an existing credential and an invalid declaration for its final type.
			repo := &longContextBillingRepoStub{account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}
			svc := &adminServiceImpl{accountRepo: repo}

			// When editing the account.
			account, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Type: tt.accountType, Extra: map[string]any{OpenAICodexOAuthRelayExtraKey: tt.value}})

			// Then validation stops the update.
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			require.Nil(t, account)
		})
	}
}

func TestCodexOAuthRelayUpdateFalseClearsUnsupportedDeclaration(t *testing.T) {
	// Given stale metadata on an OAuth credential.
	repo := &longContextBillingRepoStub{account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{OpenAICodexOAuthRelayExtraKey: true}}}
	svc := &adminServiceImpl{accountRepo: repo}

	// When explicitly clearing the declaration.
	account, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{OpenAICodexOAuthRelayExtraKey: false}})

	// Then it is removed rather than blocking the cleanup.
	require.NoError(t, err)
	require.NotContains(t, account.Extra, OpenAICodexOAuthRelayExtraKey)
}

func TestCodexOAuthRelayTypeChangePreservesOmittedExtraSemantics(t *testing.T) {
	// Given stored system-owned probe state beside a relay declaration.
	repo := &longContextBillingRepoStub{account: &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Extra: map[string]any{OpenAICodexOAuthRelayExtraKey: true, UpstreamBillingProbeEnabledExtraKey: true},
	}}
	svc := &adminServiceImpl{accountRepo: repo}
	input := &UpdateAccountInput{Type: AccountTypeOAuth}

	// When changing type with no Extra edits.
	account, err := svc.UpdateAccount(context.Background(), 1, input)

	// Then obsolete relay/probe state clears without requesting probe enablement.
	require.NoError(t, err)
	require.NotContains(t, account.Extra, OpenAICodexOAuthRelayExtraKey)
	require.NotContains(t, account.Extra, UpstreamBillingProbeEnabledExtraKey)
}
