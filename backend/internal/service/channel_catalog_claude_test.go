//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func claudeCatalogAccount(models map[string]any) Account {
	return Account{
		Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": models},
	}
}

func TestClaudeDisplaySyncRemovesManualAndPreviouslySyncedRowsByGroup(t *testing.T) {
	ctx := context.Background()
	const (
		keptAlias       = "my-sonnet"
		keptTarget      = "claude-sonnet-4-5"
		retiredAlias    = "my-old-claude"
		retiredTarget   = "claude-3-7-sonnet-20250219"
		retiredPriceRow = "claude-3-5-haiku-20241022"
		newModel        = "claude-sonnet-4-6"
		public, private = int64(11), int64(12)
		detached, gpt   = int64(13), int64(22)
	)
	ch := Channel{ID: 5, Status: StatusActive, GroupIDs: []int64{public, private, gpt},
		ModelMapping: map[string]map[string]string{
			PlatformAnthropic: {keptAlias: keptTarget, retiredAlias: retiredTarget},
			PlatformOpenAI:    {"gpt-6-sol": "gpt-6-sol"},
		},
		ModelPricing:   []ChannelModelPricing{{Platform: PlatformAnthropic, Models: []string{retiredPriceRow}, InputPrice: testPtrFloat64(2e-6)}},
		FeaturesConfig: map[string]any{featureKeyGPTDisplayModels: GPTDisplayModelsByGroup{gpt: {"gpt-6.2-sol"}}},
	}
	publicAccount := claudeCatalogAccount(map[string]any{keptTarget: keptTarget, newModel: newModel})
	limited := time.Now().Add(time.Hour)
	publicAccount.RateLimitResetAt = &limited
	accounts := &catalogAccountsStub{byGroup: map[int64][]Account{
		public:  {publicAccount},
		private: {claudeCatalogAccount(map[string]any{retiredTarget: retiredTarget, retiredPriceRow: retiredPriceRow})},
	}}
	groups := &stubGroupRepoForAvailable{activeGroups: []Group{
		{ID: public, Platform: PlatformAnthropic, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{keptAlias, retiredAlias, "claude-*"}}, RateMultiplier: .3},
		{ID: private, Platform: PlatformAnthropic, IsExclusive: true, RateMultiplier: 1},
		{ID: detached, Platform: PlatformAnthropic}, {ID: gpt, Platform: PlatformOpenAI},
	}}
	repo := &mockChannelRepository{
		listAllFn: func(context.Context) ([]Channel, error) { return []Channel{ch}, nil },
		getByIDFn: func(context.Context, int64) (*Channel, error) { return ch.Clone(), nil },
	}
	catalog := NewChannelModelCatalog(accounts, NewBillingService(&config.Config{}, nil))
	channels := NewChannelService(repo, groups, nil, nil, nil, catalog)
	plaza := NewModelPlazaService(repo, groups, nil, nil, nil, catalog)
	original := ch.Clone()

	checkPublic := func(want []string) {
		t.Helper()
		listed, err := plaza.ListGroups(ctx)
		require.NoError(t, err)
		var names []string
		for _, g := range listed {
			if g.ID == public {
				for _, m := range g.Models {
					names = append(names, m.Name)
				}
			}
			require.NotEqual(t, detached, g.ID)
		}
		require.Equal(t, want, names)
	}
	checkPublic([]string{retiredPriceRow, retiredAlias, keptAlias})
	require.Empty(t, accounts.calls)

	preview, err := channels.PreviewClaudeDisplayModels(ctx, &ch, ch.ID)
	require.NoError(t, err)
	require.Equal(t, map[int64][]string{
		public: {retiredPriceRow, retiredAlias, keptAlias}, private: {retiredPriceRow, retiredAlias, keptAlias},
	}, preview.PreviousModelsByGroup)
	require.Equal(t, map[int64][]string{
		public: {keptTarget, newModel, keptAlias}, private: {retiredPriceRow, retiredTarget, retiredAlias},
	}, preview.ModelsByGroup)
	require.Equal(t, []int64{public, private}, accounts.calls)
	checkPublic([]string{retiredPriceRow, retiredAlias, keptAlias})

	// Persist a generic JSON snapshot just as the admin update request does.
	save := func(preview ClaudeDisplayPreview) {
		t.Helper()
		data, err := json.Marshal(map[string]any{featureKeyClaudeDisplayModels: preview.ModelsByGroup})
		require.NoError(t, err)
		var features map[string]any
		require.NoError(t, json.Unmarshal(data, &features))
		require.NoError(t, channels.applyUpdateInput(ctx, &ch, &UpdateChannelInput{FeaturesConfig: features}))
	}
	save(preview)
	checkPublic([]string{keptTarget, newModel, keptAlias})
	available, err := channels.ListAvailable(ctx)
	require.NoError(t, err)
	require.Len(t, available, 1)
	require.Equal(t, preview.ModelsByGroup[public], claudeDisplayNames(&ch, available[0].DisplayModelsByGroup[public]))
	require.Equal(t, preview.ModelsByGroup[private], claudeDisplayNames(&ch, available[0].DisplayModelsByGroup[private]))
	require.Empty(t, available[0].DisplayModelsByGroup[detached])
	require.Len(t, available[0].AdditionalModelsByGroup[gpt], 1)
	require.Equal(t, []int64{public, private}, accounts.calls, "catalog reads must not refresh from accounts")

	delete(accounts.byGroup, public)
	checkPublic([]string{keptTarget, newModel, keptAlias})
	preview, err = channels.PreviewClaudeDisplayModels(ctx, &ch, ch.ID)
	require.NoError(t, err)
	require.Equal(t, []string{keptTarget, newModel, keptAlias}, preview.PreviousModelsByGroup[public])
	require.Equal(t, []string{}, preview.ModelsByGroup[public])
	save(preview)
	checkPublic(nil)
	available, err = channels.ListAvailable(ctx)
	require.NoError(t, err)
	require.Equal(t, []SupportedModel{}, available[0].DisplayModelsByGroup[public], "a saved empty list must not fall back to manual rows")
	require.NoError(t, channels.applyUpdateInput(ctx, &ch, &UpdateChannelInput{FeaturesConfig: map[string]any{}}))
	require.Equal(t, preview.ModelsByGroup, ch.displayModelsByGroup(featureKeyClaudeDisplayModels))
	require.Equal(t, original.gptDisplayModelsByGroup(), ch.gptDisplayModelsByGroup())
	require.Equal(t, original.GroupIDs, ch.GroupIDs)
	require.Equal(t, original.ModelMapping, ch.ModelMapping)
	require.Equal(t, original.ModelPricing, ch.ModelPricing)
}

func TestClaudeDisplaySyncDoesNotExpandWildcardsAndRespectsAdmission(t *testing.T) {
	ctx := context.Background()
	const kept = "claude-sonnet-4-5"
	const blocked = "claude-sonnet-4-6"
	reserve := claudeCatalogAccount(map[string]any{"claude-opus-4-6": "claude-opus-4-6"})
	reserve.Extra = map[string]any{PaidFallbackExtraKey: true}
	accounts := &catalogAccountsStub{byGroup: map[int64][]Account{
		11: {claudeCatalogAccount(map[string]any{"claude-*": "claude-*"}), reserve},
	}}
	ch := Channel{GroupIDs: []int64{11}, ModelMapping: map[string]map[string]string{
		PlatformAnthropic: {kept: kept, blocked: blocked},
	}}
	group := Group{ID: 11, Platform: PlatformAnthropic, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{kept, "claude-opus-*"}}}
	channels := &ChannelService{groupRepo: &stubGroupRepoForAvailable{activeGroups: []Group{group}},
		modelCatalog: NewChannelModelCatalog(accounts, NewBillingService(&config.Config{}, nil))}
	preview, err := channels.PreviewClaudeDisplayModels(ctx, &ch, 0)
	require.NoError(t, err)
	require.Equal(t, []string{kept, blocked}, preview.PreviousModelsByGroup[11])
	require.Equal(t, []string{kept}, preview.ModelsByGroup[11])

	accounts.err = errors.New("catalog unavailable")
	preview, err = channels.PreviewClaudeDisplayModels(ctx, &ch, 0)
	require.ErrorIs(t, err, accounts.err)
	require.Nil(t, preview.ModelsByGroup, "a failed lookup must not masquerade as a successful empty list")

	accounts.err = nil
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	preview, err = channels.PreviewClaudeDisplayModels(canceled, &ch, 0)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, preview.ModelsByGroup)
}
