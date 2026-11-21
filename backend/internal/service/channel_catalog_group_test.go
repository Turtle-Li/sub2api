//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestPlatformDisplayKimiReconcilesWorkBuddyModelsWithoutRewritingPrices(t *testing.T) {
	const gid = int64(17)
	g := Group{ID: gid, Platform: PlatformKimi, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"public-fast", "kimi-*", "claude-old"}}}
	ch := Channel{ID: 6, Status: StatusActive, GroupIDs: []int64{gid},
		ModelMapping:   map[string]map[string]string{PlatformKimi: {"public-fast": "kimi-k3", "claude-old": "kimi-retired"}},
		ModelPricing:   []ChannelModelPricing{{Platform: PlatformKimi, Models: []string{"kimi-retired"}, Currency: PricingCurrencyCNY, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(0.04)}},
		FeaturesConfig: map[string]any{"model_plaza_hidden_models": []string{"kimi-hidden"}},
	}
	a := claudeCatalogAccount(map[string]any{"kimi-k3": "cn:kimi-k3", "kimi-hidden": "cn:kimi-k3", "kimi-wrong": "cn:glm-5", "kimi-invalid": "unknown", "claude-only": "claude-sonnet-4-6"})
	a.Platform = PlatformWorkbuddy
	reserve := claudeCatalogAccount(map[string]any{"kimi-reserve": "cn:kimi-k3"})
	reserve.Platform, reserve.Extra = PlatformWorkbuddy, map[string]any{PaidFallbackExtraKey: true}
	accounts := &catalogAccountsStub{byGroup: map[int64][]Account{gid: {a, reserve}, 18: {a}}}
	groups := &stubGroupRepoForAvailable{activeGroups: []Group{g, {ID: 18, Platform: PlatformKimi, IsExclusive: true}}}
	repo := &mockChannelRepository{getByIDFn: func(context.Context, int64) (*Channel, error) { return ch.Clone(), nil }, listAllFn: func(context.Context) ([]Channel, error) { return []Channel{ch}, nil }}
	catalog := NewChannelModelCatalog(accounts, nil)
	svc := NewChannelService(repo, groups, nil, nil, nil, catalog)
	plaza := NewModelPlazaService(repo, groups, nil, nil, nil, catalog)
	original := ch.Clone()
	preview, err := svc.PreviewPlatformDisplayModels(t.Context(), &ch, ch.ID, PlatformKimi)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"claude-old", "kimi-retired", "public-fast"}, preview.PreviousModelsByGroup[gid])
	require.Equal(t, map[int64][]string{gid: {"kimi-k3", "public-fast"}}, preview.ModelsByGroup)
	require.Nil(t, ch.FeaturesConfig[platformDisplayFeature(PlatformKimi)])
	require.NoError(t, svc.applyUpdateInput(t.Context(), &ch, &UpdateChannelInput{DisplayModelUpdates: map[string]map[int64][]string{PlatformKimi: preview.ModelsByGroup}}))
	listed, err := plaza.ListGroups(t.Context())
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Len(t, listed[0].Models, 2)
	require.Equal(t, "kimi-k3", listed[0].Models[0].Name)
	require.Equal(t, "public-fast", listed[0].Models[1].Name)
	available, err := svc.ListAvailable(t.Context())
	require.NoError(t, err)
	require.Equal(t, preview.ModelsByGroup[gid], platformDisplayNames(&ch, PlatformKimi, available[0].DisplayModelsByGroup[gid]))
	require.Equal(t, []int64{gid}, accounts.calls, "public reads must never rediscover accounts")
	delete(accounts.byGroup, gid)
	empty, err := svc.PreviewPlatformDisplayModels(t.Context(), &ch, ch.ID, PlatformKimi)
	require.NoError(t, err)
	require.Equal(t, preview.ModelsByGroup, empty.PreviousModelsByGroup)
	require.Equal(t, map[int64][]string{gid: {}}, empty.ModelsByGroup)
	require.NoError(t, svc.applyUpdateInput(t.Context(), &ch, &UpdateChannelInput{DisplayModelUpdates: map[string]map[int64][]string{PlatformKimi: empty.ModelsByGroup}}))
	require.NoError(t, svc.applyUpdateInput(t.Context(), &ch, &UpdateChannelInput{FeaturesConfig: map[string]any{"unrelated": true}}))
	listed, err = plaza.ListGroups(t.Context())
	require.NoError(t, err)
	require.Empty(t, listed)
	require.Equal(t, original.ModelMapping, ch.ModelMapping)
	require.Equal(t, original.ModelPricing, ch.ModelPricing)
}

func TestPlatformDisplayEveryRegisteredPlatformCanPreview(t *testing.T) {
	for _, spec := range domain.Platforms() {
		t.Run(spec.ID, func(t *testing.T) {
			model := "vendor/custom_v1"
			if spec.ID == PlatformTypeSafe {
				model = "jev-latest"
			}
			if spec.ID == PlatformAntigravity {
				model = "gemini-2.5-flash"
			}
			a := claudeCatalogAccount(map[string]any{model: model})
			a.Platform = spec.ID
			g := Group{ID: 3, Platform: spec.ID, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{model}}}
			accounts := &catalogAccountsStub{byGroup: map[int64][]Account{3: {a}}}
			svc := &ChannelService{groupRepo: &stubGroupRepoForAvailable{activeGroups: []Group{g}}, modelCatalog: NewChannelModelCatalog(accounts, nil)}
			preview, err := svc.PreviewPlatformDisplayModels(t.Context(), &Channel{GroupIDs: []int64{3}}, 0, spec.ID)
			require.NoError(t, err)
			require.Equal(t, []string{model}, preview.ModelsByGroup[3])
		})
	}
}

func TestPlatformDisplayWorkBuddyGroupPickerAndManualSelection(t *testing.T) {
	account := Account{
		Platform: PlatformWorkbuddy, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"glm-5.3": "cn:glm-5.3", "fast-a": "cn:glm-5.3",
			"MiniMax-M3": "cn:minimax-m3", "fast-b": "cn:MiniMax-M3",
			"kimi-k3": "cn:kimi-k3", "claude-sonnet": "claude-sonnet-4-6",
		}},
	}
	for _, tc := range []struct {
		platform   string
		candidates []string
		selected   []string
	}{
		{PlatformZhipu, []string{"fast-a", "glm-5.3"}, []string{"fast-a"}},
		{PlatformMiniMax, []string{"MiniMax-M3", "fast-b"}, []string{"MiniMax-M3"}},
		{PlatformWorkbuddy, []string{"MiniMax-M3", "claude-sonnet", "fast-a", "fast-b", "glm-5.3", "kimi-k3"}, []string{"MiniMax-M3", "glm-5.3"}},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			g := Group{ID: 17, Platform: tc.platform, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: tc.selected}}
			accounts := &catalogAccountsStub{byGroup: map[int64][]Account{g.ID: {account}}}
			admin := &adminServiceImpl{groupRepo: &groupRepoStubForAdmin{getByID: &g}, accountRepo: accounts}
			candidates, err := admin.GetGroupModelsListCandidates(t.Context(), g.ID, g.Platform)
			require.NoError(t, err)
			require.Equal(t, tc.candidates, candidates, "the picker offers account aliases even if not selected")

			channels := &ChannelService{groupRepo: &stubGroupRepoForAvailable{activeGroups: []Group{g}}, modelCatalog: NewChannelModelCatalog(accounts, nil)}
			draft := &Channel{GroupIDs: []int64{g.ID}}
			preview, err := channels.PreviewPlatformDisplayModels(t.Context(), draft, 0, g.Platform)
			require.NoError(t, err)
			require.Equal(t, map[int64][]string{g.ID: tc.selected}, preview.ModelsByGroup, "display only the selected public names")
		})
	}
}

func TestPlatformDisplayCompositePreservesOtherSectionsAndEmptySnapshot(t *testing.T) {
	const gid int64 = 9
	g := &Group{ID: gid, Platform: PlatformComposite}
	ch := &Channel{FeaturesConfig: map[string]any{}, ModelMapping: map[string]map[string]string{
		PlatformKimi: {"retired": "kimi-k2"}, PlatformGemini: {"gemini-custom": "gemini-custom"},
	}}
	catalog := NewChannelModelCatalog(nil, nil)
	manual := ch.SupportedModels()
	require.Nil(t, catalog.projectGroupDisplay(ch, g, manual, nil))
	ch.FeaturesConfig[platformDisplayFeature(PlatformKimi)] = map[int64][]string{gid: {"new-kimi"}}
	filtered := catalog.projectGroupDisplay(ch, g, manual, ch.platformDisplaySnapshots())
	require.Equal(t, []string{"new-kimi"}, platformDisplayNames(ch, PlatformKimi, filtered))
	require.Equal(t, []string{"gemini-custom"}, platformDisplayNames(ch, PlatformGemini, filtered))
	ch.FeaturesConfig[platformDisplayFeature(PlatformGemini)] = map[int64][]string{gid: {}}
	filtered = catalog.projectGroupDisplay(ch, g, manual, ch.platformDisplaySnapshots())
	require.Len(t, filtered, 1)
	require.Equal(t, "new-kimi", filtered[0].Name)
}

func TestPlatformDisplayOpenAIRespectsMuseAndPinnedCatalog(t *testing.T) {
	muse := Group{ID: 1, Platform: PlatformOpenAI, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{legacyMuseTextModel, MuseImageModel, MuseVideoModel}}}
	pinned := Group{ID: 2, Platform: PlatformOpenAI, CodexModelsManifestConfig: GroupCodexModelsManifestConfig{Enabled: true}}
	a := claudeCatalogAccount(map[string]any{legacyMuseTextModel: legacyMuseTextModel, MuseImageModel: MuseImageModel, MuseVideoModel: MuseVideoModel})
	a.Platform, a.Credentials["base_url"] = PlatformOpenAI, "https://muse.example/v1"
	accounts := &catalogAccountsStub{byGroup: map[int64][]Account{1: {a}}}
	ch := &Channel{ID: 7, GroupIDs: []int64{1, 2}, ModelMapping: map[string]map[string]string{PlatformOpenAI: {"manual": "manual"}}, FeaturesConfig: map[string]any{featureKeyGPTDisplayModels: map[int64][]string{1: {"gpt-6-sol"}}}}
	svc := &ChannelService{groupRepo: &stubGroupRepoForAvailable{activeGroups: []Group{muse, pinned}}, repo: &mockChannelRepository{getByIDFn: func(context.Context, int64) (*Channel, error) { return ch.Clone(), nil }}, modelCatalog: NewChannelModelCatalog(accounts, nil)}
	preview, err := svc.PreviewPlatformDisplayModels(t.Context(), ch, ch.ID, PlatformOpenAI)
	require.NoError(t, err)
	require.Equal(t, []string{"manual"}, preview.PreviousModelsByGroup[1], "do not report hidden legacy GPT supplements as removals")
	require.Equal(t, map[int64][]string{1: {MuseImageModel, MuseVideoModel}}, preview.ModelsByGroup)
	require.Equal(t, []int64{1}, accounts.calls)
	accounts.err = errors.New("catalog unavailable")
	failed, err := svc.PreviewPlatformDisplayModels(t.Context(), ch, ch.ID, PlatformOpenAI)
	require.ErrorIs(t, err, accounts.err)
	require.Nil(t, failed.ModelsByGroup)
}

func TestPlatformDisplayPatchPreservesSkippedGroupsAndExplicitEmptyGroup(t *testing.T) {
	key := platformDisplayFeature(PlatformOpenAI)
	ch := &Channel{GroupIDs: []int64{1, 2, 3}, FeaturesConfig: map[string]any{
		key:                                  map[int64][]string{1: {"remove"}, 2: {"pinned"}, 3: {"inactive"}, 4: {"detached"}},
		platformDisplayFeature(PlatformKimi): map[int64][]string{3: {"kimi-model"}},
	}}
	updates := map[string]map[int64][]string{PlatformOpenAI: {1: {}}}
	s := &ChannelService{}
	require.NoError(t, s.applyUpdateInput(t.Context(), ch, &UpdateChannelInput{FeaturesConfig: map[string]any{}, DisplayModelUpdates: updates}))
	want := map[int64][]string{1: {}, 2: {"pinned"}, 3: {"inactive"}, 4: {"detached"}}
	require.Equal(t, want, ch.displayModelsByGroup(key))
	require.NoError(t, ch.applyDisplayModelUpdates(map[string]map[int64][]string{PlatformOpenAI: {}}))
	for _, invalid := range []map[string]map[int64][]string{{PlatformOpenAI: {4: {}}}, {PlatformOpenAI: {1: nil}}, {"unknown": {1: {}}}} {
		require.Error(t, ch.applyDisplayModelUpdates(invalid))
	}
	require.Equal(t, want, ch.displayModelsByGroup(key))
	require.Equal(t, []string{"kimi-model"}, ch.displayModelsByGroup(platformDisplayFeature(PlatformKimi))[3])
	// Explicit rollback via the existing full snapshot API remains possible.
	require.NoError(t, s.applyUpdateInput(t.Context(), ch, &UpdateChannelInput{FeaturesConfig: map[string]any{key: map[int64][]string{}}}))
	require.Empty(t, ch.displayModelsByGroup(key))
}

func TestPlatformDisplayOpenAIKeepsInferredGPTPriceAndIdentityRules(t *testing.T) {
	const priced = "gpt-6.2-sol"
	const img = "gpt-image-2"
	ch := &Channel{Status: StatusActive, GroupIDs: []int64{8}, ModelMapping: map[string]map[string]string{PlatformOpenAI: {"public-fast": priced}}}
	g := Group{ID: 8, Platform: PlatformOpenAI, AllowImageGeneration: true}
	a := catalogOpenAIAccount(map[string]any{priced: priced, img: img, "gpt-unknown-good-looking": "gpt-unknown-good-looking", "gpt-6-sol": priced, "implicit-alias": priced})
	s := &ChannelService{groupRepo: &stubGroupRepoForAvailable{activeGroups: []Group{g}}, modelCatalog: NewChannelModelCatalog(&catalogAccountsStub{byGroup: map[int64][]Account{8: {a}}}, NewBillingService(&config.Config{}, nil))}
	preview, err := s.PreviewPlatformDisplayModels(t.Context(), ch, 0, PlatformOpenAI)
	require.NoError(t, err)
	require.Equal(t, []string{priced, img, "public-fast"}, preview.ModelsByGroup[8])
}

func TestGroupPickerDoesNotEnumerateDormantPassthroughMappings(t *testing.T) {
	a := catalogOpenAIAccount(map[string]any{"claude-stale": "claude-stale", "gpt-6.2-sol": "ignored-target"})
	a.Type, a.Extra = AccountTypeAPIKey, map[string]any{"openai_passthrough": true}
	require.Empty(t, groupAccountModelNames([]Account{a}, PlatformOpenAI))
	require.False(t, nativeGPTDisplayIdentity(&a, "gpt-6.2-sol"))
	a.Extra["openai_passthrough_model_filter"] = true
	require.Equal(t, []string{"claude-stale", "gpt-6.2-sol"}, groupAccountModelNames([]Account{a}, PlatformOpenAI))
	require.True(t, nativeGPTDisplayIdentity(&a, "gpt-6.2-sol"))
}

func TestPlatformDisplayCompositeAllowlistDoesNotInventForeignModelsOnOpenRelays(t *testing.T) {
	g := Group{ID: 5, Platform: PlatformComposite, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"claude-sonnet-4-6", "kimi-k3"}}}
	a := claudeCatalogAccount(nil)
	s := &ChannelService{groupRepo: &stubGroupRepoForAvailable{activeGroups: []Group{g}}, modelCatalog: NewChannelModelCatalog(&catalogAccountsStub{byGroup: map[int64][]Account{5: {a}}}, nil)}
	preview, err := s.PreviewPlatformDisplayModels(t.Context(), &Channel{GroupIDs: []int64{5}}, 0, PlatformAnthropic)
	require.NoError(t, err)
	require.Equal(t, []string{"claude-sonnet-4-6"}, preview.ModelsByGroup[5])
}

func TestPlatformDisplayRequiresNativeMuseAndImageAdmissionForManualAliases(t *testing.T) {
	muse := claudeCatalogAccount(map[string]any{MuseImageModel: MuseImageModel})
	muse.Platform, muse.Credentials["base_url"] = PlatformOpenAI, "https://muse.example/v1"
	relay := Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	ch := &Channel{GroupIDs: []int64{4}, ModelMapping: map[string]map[string]string{PlatformOpenAI: {
		"ordinary-chat": MuseImageModel, MuseVideoModel: MuseVideoModel, "paint": "gpt-image-2",
	}}}
	g := &Group{ID: 4, Platform: PlatformOpenAI}
	catalog := NewChannelModelCatalog(nil, nil)
	require.Equal(t, []string{MuseImageModel}, catalog.previewPlatformGroup(ch, g, PlatformOpenAI, []Account{muse, relay}, ch.SupportedModels()))
	g.AllowImageGeneration = true
	models := catalog.previewPlatformGroup(ch, g, PlatformOpenAI, []Account{muse, relay}, ch.SupportedModels())
	require.Equal(t, []string{MuseImageModel, "paint"}, models)
	g.AllowImageGeneration = false
	projected := catalog.projectGroupDisplay(ch, g, ch.SupportedModels(), platformDisplaySnapshots{PlatformOpenAI: {4: models}})
	require.Equal(t, []string{MuseImageModel}, platformDisplayNames(ch, PlatformOpenAI, projected))
}
