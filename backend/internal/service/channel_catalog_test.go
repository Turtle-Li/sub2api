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

type catalogAccountsStub struct {
	AccountRepository
	byGroup map[int64][]Account
	err     error
	calls   []int64
}

func (r *catalogAccountsStub) ListModelAvailabilityCandidates(_ context.Context, groupID *int64, _ []string, _ bool) ([]Account, error) {
	r.calls = append(r.calls, *groupID)
	return r.byGroup[*groupID], r.err
}

func catalogOpenAIAccount(mapping map[string]any) Account {
	return Account{
		Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": mapping},
	}
}

func TestChannelCatalogPublishesOnlyAfterSavingThePreview(t *testing.T) {
	ctx := context.Background()
	const sol = "gpt-6.2-sol"
	const astra = "gpt-6.2-astra"
	const image = "gpt-image-2.5-flare"
	const manual = "gpt-6.1-sol"
	groups := []Group{
		{ID: 6, Platform: PlatformOpenAI, RateMultiplier: .3, AllowImageGeneration: true, ImageRateIndependent: true, ImageRateMultiplier: 1,
			ImagePrice1K: testPtrFloat64(.2), ImagePrice2K: testPtrFloat64(.2), ImagePrice4K: testPtrFloat64(.2)},
		{ID: 7, Platform: PlatformOpenAI, RateMultiplier: 1, IsExclusive: true},
	}
	ch := Channel{ID: 1, Status: StatusActive, GroupIDs: []int64{6, 7},
		FeaturesConfig: map[string]any{"follow_openai_group_models": true},
		ModelMapping:   map[string]map[string]string{PlatformOpenAI: {manual: manual}},
	}
	public := catalogOpenAIAccount(map[string]any{image: image, manual: manual})
	limited := time.Now().Add(time.Hour)
	public.RateLimitResetAt = &limited
	accounts := &catalogAccountsStub{byGroup: map[int64][]Account{
		6: {public, catalogOpenAIAccount(map[string]any{sol: sol})},
		7: {catalogOpenAIAccount(map[string]any{astra: astra, image: image})},
	}}
	repo := &mockChannelRepository{listAllFn: func(context.Context) ([]Channel, error) { return []Channel{ch}, nil }}
	groupRepo := &stubGroupRepoForAvailable{activeGroups: groups}
	billing := NewBillingService(&config.Config{}, nil)
	catalog := NewChannelModelCatalog(accounts, billing)
	channels := NewChannelService(repo, groupRepo, nil, nil, nil, catalog)
	plaza := NewModelPlazaService(repo, groupRepo, nil, billing, NewModelPricingResolver(channels, billing), catalog)

	available, err := channels.ListAvailable(ctx)
	require.NoError(t, err)
	require.Len(t, available, 1)
	require.Equal(t, manual, available[0].SupportedModels[0].Name)
	require.Empty(t, available[0].AdditionalModelsByGroup)
	require.Empty(t, accounts.calls, "viewing a channel must not discover account models")

	snapshot, err := channels.PreviewGPTDisplayModels(ctx, &ch)
	require.NoError(t, err)
	require.Equal(t, GPTDisplayModelsByGroup{6: {sol, image}, 7: {astra}}, snapshot)
	listed, err := plaza.ListGroups(ctx)
	require.NoError(t, err)
	require.Len(t, listed[0].Models, 1, "preview must not publish anything")
	require.Equal(t, []int64{6, 7}, accounts.calls)

	// Use the same generic JSON shape as the persisted features_config.
	data, err := json.Marshal(map[string]any{featureKeyGPTDisplayModels: snapshot})
	require.NoError(t, err)
	ch.FeaturesConfig = nil
	require.NoError(t, json.Unmarshal(data, &ch.FeaturesConfig))
	available, err = channels.ListAvailable(ctx)
	require.NoError(t, err)
	require.Len(t, available[0].AdditionalModelsByGroup[6], 2)
	require.Len(t, available[0].AdditionalModelsByGroup[7], 1)
	require.Equal(t, astra, available[0].AdditionalModelsByGroup[7][0].Name)
	listed, err = plaza.ListGroups(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	require.Equal(t, int64(6), listed[0].ID)
	require.Len(t, listed[0].Models, 3)
	require.Equal(t, sol, listed[0].Models[1].Name)
	require.InDelta(t, 2e-6, *listed[0].Models[1].Pricing.InputPrice, 1e-12)
	require.InDelta(t, 10e-6, *listed[0].Models[1].Pricing.OutputPrice, 1e-12)
	require.False(t, listed[0].Models[1].GPTImage)
	require.True(t, listed[0].Models[2].GPTImage)
	require.Len(t, listed[0].Models[2].Pricing.Intervals, 3)
	for _, tier := range listed[0].Models[2].Pricing.Intervals {
		require.InDelta(t, 0.2, *tier.PerRequestPrice, 1e-12)
	}
	require.Equal(t, map[string]string{manual: manual}, ch.ModelMapping[PlatformOpenAI])
	require.Empty(t, ch.ModelPricing)

	accounts.byGroup[6] = []Account{catalogOpenAIAccount(map[string]any{astra: astra})}
	listed, err = plaza.ListGroups(ctx)
	require.NoError(t, err)
	require.Len(t, listed[0].Models, 3, "account edits cannot update the saved display")
	require.Equal(t, sol, listed[0].Models[1].Name)
	require.Equal(t, []int64{6, 7}, accounts.calls)

	snapshot, err = channels.PreviewGPTDisplayModels(ctx, &ch)
	require.NoError(t, err)
	require.Equal(t, GPTDisplayModelsByGroup{6: {astra}, 7: {astra}}, snapshot)
	ch.FeaturesConfig[featureKeyGPTDisplayModels] = snapshot
	listed, err = plaza.ListGroups(ctx)
	require.NoError(t, err)
	require.Len(t, listed[0].Models, 2)
	require.Equal(t, astra, listed[0].Models[1].Name)
}

func TestChannelCatalogRejectsUnapprovedOrUnpricedGPTModels(t *testing.T) {
	const sol = "gpt-6.2-sol"
	const astra = "gpt-6.2-astra"
	ch := Channel{Status: StatusActive,
		FeaturesConfig: map[string]any{featureKeyModelPlazaHiddenModels: []string{astra}},
		ModelMapping:   map[string]map[string]string{PlatformOpenAI: {"gpt-image-*": "different-image"}},
	}
	group := Group{ID: 6, Platform: PlatformOpenAI, AllowImageGeneration: true,
		ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-*"}},
	}
	active := catalogOpenAIAccount(map[string]any{
		sol: sol, astra: astra, "gpt-*": "gpt-*", "gpt-unknown-good-looking": "gpt-unknown-good-looking",
		"gpt-image-2": "gpt-image-2", "gpt-5.5": "gpt-5.5", "gpt-6-sol": sol,
	})
	reserve := catalogOpenAIAccount(map[string]any{"gpt-6.1-sol": "gpt-6.1-sol"})
	reserve.Extra = map[string]any{PaidFallbackExtraKey: true}
	repo := &catalogAccountsStub{byGroup: map[int64][]Account{6: {active, reserve}}}
	svc := NewChannelModelCatalog(repo, NewBillingService(&config.Config{}, nil))

	names, err := svc.previewGroup(context.Background(), &ch, &group, nil)
	require.NoError(t, err)
	require.Equal(t, []string{sol}, names)
	models := svc.savedModels(&ch, &group, nil, names)
	require.False(t, models[0].ProbeEligible, "publishing a model does not opt it in to connectivity probes")

	group.ModelAllowlist.Models = []string{astra}
	names, err = svc.previewGroup(context.Background(), &ch, &group, nil)
	require.NoError(t, err)
	require.Empty(t, names)

	group.ModelAllowlist.Models = []string{sol}
	repo.err = errors.New("account catalog offline")
	names, err = svc.previewGroup(context.Background(), &ch, &group, nil)
	require.ErrorIs(t, err, repo.err)
	require.Nil(t, names)
}

func TestChannelCatalogSnapshotRespectsCurrentDisplayRestrictions(t *testing.T) {
	const model = "gpt-image-2"
	repo := &catalogAccountsStub{}
	svc := NewChannelModelCatalog(repo, nil)
	channel := Channel{Status: StatusActive}
	group := Group{ID: 6, Platform: PlatformOpenAI, AllowImageGeneration: true}
	names := []string{model}
	require.Equal(t, []SupportedModel{{Name: model, Platform: PlatformOpenAI}}, svc.savedModels(&channel, &group, nil, names))

	channel.RestrictModels = true
	require.Empty(t, svc.savedModels(&channel, &group, nil, names))

	channel.RestrictModels = false
	group.CodexModelsManifestConfig.Enabled = true
	require.Empty(t, svc.savedModels(&channel, &group, nil, names))

	group.CodexModelsManifestConfig.Enabled = false
	group.AllowImageGeneration = false
	require.Empty(t, svc.savedModels(&channel, &group, nil, names))
	require.Empty(t, repo.calls)
}

func TestChannelCatalogKeepsSnapshotOnUnrelatedChannelEdits(t *testing.T) {
	snapshot := GPTDisplayModelsByGroup{6: {"gpt-6.2-sol"}}
	ch := Channel{FeaturesConfig: map[string]any{featureKeyGPTDisplayModels: snapshot}}
	svc := &ChannelService{}
	config := map[string]any{featureKeyBedrockCCCompat: true}
	require.NoError(t, svc.applyUpdateInput(context.Background(), &ch, &UpdateChannelInput{FeaturesConfig: config}))
	require.Equal(t, snapshot, ch.gptDisplayModelsByGroup())
	require.NotContains(t, config, featureKeyGPTDisplayModels)

	config[featureKeyGPTDisplayModels] = GPTDisplayModelsByGroup{}
	require.NoError(t, svc.applyUpdateInput(context.Background(), &ch, &UpdateChannelInput{FeaturesConfig: config}))
	require.Empty(t, ch.gptDisplayModelsByGroup(), "an explicitly saved empty preview clears the snapshot")
}
