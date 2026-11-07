//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminGroupStatusModelsRespectBoundAccountsAllowlistAndTextOnly(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformOpenAI, Status: StatusActive,
		ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"cheap-*", "alias-*", "gpt-*"}}}
	accounts := &accountRepoStubForCompositeModelsList{accounts: []Account{
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{
			"cheap-text": "gpt-text", "expensive-text": "gpt-text", "alias-image": "gpt-image-1", "alias-text": "gpt-text", "wild-*": "gpt-text",
		}}},
		{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-wrong-platform": "claude-text"}}},
	}}
	s := &adminServiceImpl{groupRepo: &groupRepoStubForAdmin{getByID: group}, accountRepo: accounts}
	models, err := s.GetGroupModelsListCandidates(context.Background(), 9, PlatformAnthropic, true)
	require.NoError(t, err)
	require.Equal(t, []string{"alias-text", "cheap-text"}, models, "platform query cannot override the selected group; unservable defaults and mapped media are excluded")
	accounts.accounts = nil
	models, err = s.GetGroupModelsListCandidates(context.Background(), 9, "", true)
	require.NoError(t, err)
	require.Empty(t, models, "no bound accounts must never return platform defaults")
	group.IsExclusive = true
	_, err = s.GetGroupModelsListCandidates(context.Background(), 9, "", true)
	require.Error(t, err)
	group.IsExclusive = false
	group.Status = "disabled"
	_, err = s.GetGroupModelsListCandidates(context.Background(), 9, "", true)
	require.Error(t, err)
	_, err = s.GetGroupModelsListCandidates(context.Background(), 0, "", true)
	require.Error(t, err)
}

func TestAdminGroupStatusModelsExpandExactAllowlistAliasesForWildcardAccounts(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformOpenAI, Status: StatusActive,
		ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"custom-cheap"}}}
	s := &adminServiceImpl{groupRepo: &groupRepoStubForAdmin{getByID: group}, accountRepo: &accountRepoStubForCompositeModelsList{accounts: []Account{
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"custom-*": "gpt-text"}}},
	}}}
	models, err := s.GetGroupModelsListCandidates(context.Background(), 9, "", true)
	require.NoError(t, err)
	require.Equal(t, []string{"custom-cheap"}, models)
}
