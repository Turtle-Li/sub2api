//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageStudioWalletCatalogueUsesOneExplicitGroupAndExactTierPrices(t *testing.T) {
	svc, _, _, _, vertex := newTestBatchImagePublicService(true)
	image := publicBatchImageProvider{name: BatchImageProviderOpenAI}
	svc.ProviderRegistry = NewBatchImageProviderRegistry(&image, vertex)
	group := &Group{ID: 45, Status: StatusActive, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeStandard,
		AllowImageGeneration: true, ImagePrice1K: ptrFloat64(.2), RateMultiplier: 1,
		BatchImageDiscountMultiplier: .55, BatchImageHoldMultiplier: .6,
		ModelPricing: []ChannelModelPricing{
			{Models: []string{"gpt-image-2.6-*"}, BillingMode: BillingModeImage, Currency: PricingCurrencyCNY, PerRequestPrice: ptrFloat64(9)},
			{Models: []string{"gpt-image-2.6-sunburst"}, BillingMode: BillingModeImage, Currency: PricingCurrencyCNY, PerRequestPrice: ptrFloat64(.2)},
			{Models: []string{"gpt-image-2.6-flare"}, BillingMode: BillingModeImage, Currency: PricingCurrencyCNY, PerRequestPrice: ptrFloat64(.23)},
			{Models: []string{"gemini-2.5-flash-image"}, BillingMode: BillingModeImage, Currency: PricingCurrencyCNY, Intervals: []PricingInterval{{TierLabel: "1K", PerRequestPrice: ptrFloat64(.37)}}},
		},
	}
	svc.GroupRepo = &publicBatchImageGroupRepo{groups: map[int64]*Group{group.ID: group}}
	a := testBatchImageAccount(1, AccountTypeAPIKey)
	a.Platform = PlatformOpenAI
	a.Credentials["model_mapping"] = map[string]any{"gpt-image-2.6-sunburst": "gpt-image-2.6-sunburst", "gpt-6.2-sol": "gpt-6.2-sol"}
	b := testBatchImageAccount(2, AccountTypeServiceAccount)
	b.Credentials["model_mapping"] = map[string]any{"gemini-2.5-flash-image": "gemini-2.5-flash-image"}
	unselected := testBatchImageMappedAccount(3, AccountTypeAPIKey, map[string]any{"gpt-image-2.6-flare": "gpt-image-2.6-flare"})
	unselected.Platform = PlatformOpenAI
	reserve := testBatchImageMappedAccount(4, AccountTypeAPIKey, map[string]any{"gpt-image-2.6-flare": "gpt-image-2.6-flare"})
	reserve.Platform, reserve.Extra = PlatformOpenAI, map[string]any{PaidFallbackExtraKey: true}
	svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{a, b, unselected, reserve}}
	owner := BatchImageOwner{UserID: 8, GroupID: &group.ID, StudioWallet: true, StudioAccountIDs: []int64{a.ID, b.ID}}
	list, err := svc.ListModels(t.Context(), owner)
	require.NoError(t, err)
	require.Len(t, list.Data, 2)
	require.Equal(t, "gemini-2.5-flash-image", list.Data[0].ID)
	require.Equal(t, .37, list.Data[0].Prices["1K"].Min)
	require.Equal(t, []string{"1K"}, list.Data[0].Capabilities.ImageSizes)
	require.Equal(t, "gpt-image-2.6-sunburst", list.Data[1].ID)
	require.Equal(t, .2, list.Data[1].Prices["1K"].Max)
	_, _, err = svc.selectProviderAndAccount(t.Context(), owner, BatchImageProviderOpenAI, "gpt-image-2.6-flare")
	require.ErrorIs(t, err, ErrBatchImageNoAccountAvailable, "an unrelated account cannot be used even with a tariff")
	require.ErrorIs(t, svc.ValidateImageStudioWalletConfig(t.Context(), ImageStudioWalletConfig{GroupID: group.ID, AccountIDs: []int64{reserve.ID}}), ErrImageStudioWalletAccounts)
	require.NoError(t, svc.ValidateImageStudioWalletConfig(t.Context(), ImageStudioWalletConfig{GroupID: group.ID, AccountIDs: owner.StudioAccountIDs}))
	_, err = svc.ListModels(t.Context(), BatchImageOwner{UserID: 8, APIKeyID: 1, GroupID: &group.ID})
	require.ErrorIs(t, err, ErrBatchImageGroupDisabled, "wallet access must not turn on the external batch API")
	_, err = svc.resolvePricingSnapshot(t.Context(), owner, BatchImageSubmitRequest{Model: "gemini-2.5-flash-image", ImageSize: "2K"}, BatchImageProviderVertex, nil)
	require.ErrorIs(t, err, ErrBatchImageSettlementPricingMissing)
	group.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"gpt-image-2.6-sunburst"}}
	list, err = svc.ListModels(t.Context(), owner)
	require.NoError(t, err)
	require.Len(t, list.Data, 1)
	require.Equal(t, "gpt-image-2.6-sunburst", list.Data[0].ID)
}

func TestImageStudioWalletImage26TwoKTier(t *testing.T) {
	for _, tt := range []struct {
		model      string
		sourceEdge int
		wantScales []int
	}{
		{"gpt-image-2.6-flare", 1024, []int{2}},
		{"gpt-image-2.6-sunburst", 2048, nil},
	} {
		t.Run(tt.model, func(t *testing.T) {
			svc, repo, _, _, _ := newTestBatchImagePublicService(true)
			mini := newBatchImageUpscaleTestAPI()
			t.Cleanup(mini.Close)
			cfg, upscaler := newBatchImageUpscaleTestService(mini.URL(), "batch-image/delivery")
			svc.Config.ImageUpscale = cfg.ImageUpscale
			provider := &publicBatchImageProvider{name: BatchImageProviderOpenAI,
				result:      string(batchImageUpscaleTestResultLine("one", batchImageUpscaleTestPNG(tt.sourceEdge, tt.sourceEdge/2))),
				getStatuses: []*BatchProviderStatus{{InternalState: BatchProviderStateSucceeded}},
			}
			svc.ProviderRegistry = NewBatchImageProviderRegistry(provider)
			account := testBatchImageMappedAccount(202, AccountTypeOAuth, map[string]any{tt.model: tt.model})
			account.Platform = PlatformOpenAI
			svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{account}}
			group := &Group{ID: 45, Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeStandard,
				AllowImageGeneration: true, RateMultiplier: 1,
				ModelPricing: []ChannelModelPricing{{Models: []string{tt.model}, BillingMode: BillingModeImage, Currency: PricingCurrencyCNY,
					Intervals: []PricingInterval{{TierLabel: ImageBillingSize2K, PerRequestPrice: ptrFloat64(.4)}}}},
			}
			svc.GroupRepo = &publicBatchImageGroupRepo{groups: map[int64]*Group{group.ID: group}}
			owner := BatchImageOwner{UserID: 8, GroupID: &group.ID, StudioWallet: true, StudioAccountIDs: []int64{account.ID}}
			job, err := svc.Submit(t.Context(), owner, BatchImageSubmitRequest{Provider: provider.Name(), Model: tt.model, ImageSize: ImageBillingSize2K,
				Items: []BatchImageSubmitItem{{CustomID: "one", Prompt: "a pear"}}}, "")
			require.NoError(t, err)
			require.Equal(t, ImageBillingSize2K, job.ImageSize)
			require.Len(t, provider.submits, 1)
			require.Equal(t, ImageBillingSize1K, provider.submits[0].ImageSize)

			processor := BatchImageProviderProcessor{Repo: repo, ProviderRegistry: svc.ProviderRegistry, AccountResolver: &fakeBatchImageAccountResolver{account: &account},
				Indexer: &BatchImageResultIndexer{Repo: repo, Config: cfg, Upscaler: upscaler, UpscaleStore: newBatchImageUpscaleTestStore()}}
			_, err = processor.Process(t.Context(), job.ID)
			require.NoError(t, err)
			require.Equal(t, tt.wantScales, mini.SubmittedScales())
			require.Len(t, repo.items[job.ID], 1)
			item := repo.items[job.ID][0]
			require.Equal(t, BatchImageItemStatusSuccess, item.Status)
			require.Equal(t, &BatchImagePreviewSize{Width: 2048, Height: 1024}, item.PreviewSize)
		})
	}
}

func TestImageStudioWalletSubmissionOwnsAndSettlesItsOwnBalanceOnce(t *testing.T) {
	svc, repo, _, _, vertex := newTestBatchImagePublicService(true)
	model := "gemini-2.5-flash-image"
	group := &Group{ID: 45, Status: StatusActive, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeStandard,
		AllowImageGeneration: true, RateMultiplier: 1, BatchImageHoldMultiplier: 1,
		ModelPricing: []ChannelModelPricing{{Models: []string{model}, BillingMode: BillingModeImage, Currency: PricingCurrencyCNY, PerRequestPrice: ptrFloat64(.37)}},
	}
	svc.GroupRepo = &publicBatchImageGroupRepo{groups: map[int64]*Group{group.ID: group}}
	account := testBatchImageMappedAccount(202, AccountTypeServiceAccount, map[string]any{model: model})
	svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{account}}
	owner := BatchImageOwner{UserID: 8, GroupID: &group.ID, StudioWallet: true, StudioAccountIDs: []int64{account.ID}}
	request := BatchImageSubmitRequest{Model: model, Provider: BatchImageProviderVertex, Items: []BatchImageSubmitItem{{CustomID: "one", Prompt: "a pear"}}}
	ctx := t.Context()
	first, err := svc.Submit(ctx, owner, request, "once")
	require.NoError(t, err)
	replay, err := svc.Submit(ctx, owner, request, "once")
	require.NoError(t, err)
	require.Equal(t, first.ID, replay.ID)
	require.Len(t, vertex.submits, 1)
	billing, ok := svc.BillingRepo.(*fakeBatchImageBillingRepo)
	require.True(t, ok)
	require.Len(t, billing.reserves, 1)
	require.Equal(t, owner.UserID, billing.reserves[0].UserID)
	require.Equal(t, ImageStudioWalletOwnerSlot, billing.reserves[0].APIKeyID)
	require.Equal(t, .37, billing.reserves[0].HoldAmount)
	job, err := repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, ImageStudioWalletOwnerSlot, first.ID)
	require.NoError(t, err)
	require.Equal(t, &group.ID, job.StudioGroupID)
	_, err = repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, 22, first.ID)
	require.ErrorIs(t, err, ErrBatchImageJobNotFound)
	_, err = repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID+1, ImageStudioWalletOwnerSlot, first.ID)
	require.ErrorIs(t, err, ErrBatchImageJobNotFound)
	job.Status, job.SuccessCount = BatchImageJobStatusSettling, 1
	usage := &openAIRecordUsageLogRepoStub{}
	settle := BatchImageSettlementService{Repo: repo, BillingRepo: billing, Pricing: svc.Pricing, UsageLogRepo: usage}
	result, err := settle.Settle(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, .37, result.ActualCost)
	require.Equal(t, 360*time.Hour, job.OutputExpiresAt.Sub(*job.SettledAt))
	require.Nil(t, usage.lastLog, "no synthetic API key may reach the usage log foreign key")
	result, err = settle.Settle(ctx, first.ID)
	require.NoError(t, err)
	require.True(t, result.AlreadySettled)
	require.Len(t, billing.captures, 1)
}

type imageStudioSettingTestRepo struct {
	SettingRepository
	value string
}

func (r *imageStudioSettingTestRepo) GetValue(_ context.Context, key string) (string, error) {
	if key != imageStudioWalletSetting || r.value == "" {
		return "", ErrSettingNotFound
	}
	return r.value, nil
}

func (r *imageStudioSettingTestRepo) CompareAndSet(_ context.Context, key, expected, value string) (bool, error) {
	if key != imageStudioWalletSetting || r.value != expected {
		return false, nil
	}
	r.value = value
	return true, nil
}

func TestImageStudioWalletConfigCASIncludesAccountSelection(t *testing.T) {
	ctx := t.Context()
	svc := NewSettingService(&imageStudioSettingTestRepo{}, nil)
	off, err := svc.ImageStudioWalletConfig(ctx)
	require.NoError(t, err)
	require.Zero(t, off.GroupID)
	initial := ImageStudioWalletConfig{GroupID: 45, AccountIDs: []int64{253, 252}}
	persisted, err := svc.SetImageStudioWalletConfig(ctx, off, initial)
	require.NoError(t, err)
	require.Equal(t, []int64{252, 253}, persisted.AccountIDs)
	changed := ImageStudioWalletConfig{GroupID: 45, AccountIDs: []int64{252}}
	_, err = svc.SetImageStudioWalletConfig(ctx, initial, changed)
	require.NoError(t, err)
	_, err = svc.SetImageStudioWalletConfig(ctx, initial, off)
	require.ErrorIs(t, err, ErrImageStudioWalletGroupChange)
	updated, err := svc.SetImageStudioWalletConfig(ctx, changed, off)
	require.NoError(t, err)
	require.Equal(t, off, updated)
}

func TestImageStudioWalletMissingGroupCannotUseUnboundAccounts(t *testing.T) {
	svc, _, _, _, vertex := newTestBatchImagePublicService(true)
	owner := BatchImageOwner{UserID: 1, StudioWallet: true}
	_, err := svc.Submit(t.Context(), owner, BatchImageSubmitRequest{Model: "gemini-2.5-flash-image", Items: []BatchImageSubmitItem{{CustomID: "one", Prompt: "a pear"}}}, "must-deny")
	require.ErrorIs(t, err, ErrImageStudioWalletUnavailable)
	require.Empty(t, vertex.submits)
}
