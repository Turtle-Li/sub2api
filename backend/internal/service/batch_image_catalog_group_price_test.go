//go:build unit

package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeImageCatalogUsesGroupTierPrice(t *testing.T) {
	for _, model := range []string{"muse-image", "cn:hunyuan-image-alpha"} {
		for _, tc := range []struct {
			name    string
			price   *float64
			visible bool
		}{
			{"missing", nil, false}, {"positive", ptrFloat64(0.01), true},
			{"zero", ptrFloat64(0), false}, {"negative", ptrFloat64(-1), false},
			{"nan", ptrFloat64(math.NaN()), false}, {"infinite", ptrFloat64(math.Inf(1)), false},
		} {
			t.Run(model+"/"+tc.name, func(t *testing.T) {
				svc, _, _, _, _ := newTestBatchImagePublicService(true)
				svc.Pricing = &BatchImageModelPricingResolver{Resolver: NewModelPricingResolver(nil, newTestBillingService())}
				_, err := svc.Pricing.BatchImageUnitPrice(t.Context(), &BatchImageJob{Model: model, ImageSize: "1K"})
				require.ErrorIs(t, err, ErrBatchImageSettlementPricingMissing)
				gid := int64(40)
				owner := testBatchImageOwner()
				owner.GroupID = &gid
				svc.GroupRepo = &publicBatchImageGroupRepo{groups: map[int64]*Group{gid: {ID: gid, Platform: PlatformOpenAI, AllowBatchImageGeneration: true, ImagePrice1K: tc.price, RateMultiplier: 1, BatchImageHoldMultiplier: 1}}}
				a := workBuddyTestAccount("http://unused", model)
				a.Status, a.Schedulable = StatusActive, true
				svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{*a}}
				gateway := &OpenAIGatewayService{concurrencyService: NewConcurrencyService(&museSlotCache{}), httpUpstream: &museTestUpstream{}}
				store := newFakeOpenAIImageBatchStore()
				svc.ProviderRegistry = NewBatchImageProviderRegistry(NewMuseImagesBatchProvider(store, testOpenAIImageBatchObjectPrefix, gateway), newWorkBuddyImagesProvider(store, testOpenAIImageBatchObjectPrefix, gateway))
				got, err := svc.ListModels(t.Context(), owner)
				require.NoError(t, err)
				if tc.visible {
					require.Len(t, got.Data, 1)
					require.Equal(t, model, got.Data[0].ID)
				} else {
					require.Empty(t, got.Data)
				}
			})
		}
	}
}

func TestNativeImageCatalogUsesSupportedBridgeTier(t *testing.T) {
	for _, provider := range []string{BatchImageProviderWan, BatchImageProviderSeedream} {
		t.Run(provider, func(t *testing.T) {
			p, a, _ := bridgeFixture(t, provider)
			a.Status, a.Schedulable = StatusActive, true
			svc, _, _, _, _ := newTestBatchImagePublicService(true)
			svc.Pricing = &BatchImageModelPricingResolver{Resolver: NewModelPricingResolver(nil, newTestBillingService())}
			svc.ProviderRegistry = NewBatchImageProviderRegistry(p)
			svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{*a}}
			gid := int64(40)
			owner := testBatchImageOwner()
			owner.GroupID = &gid
			group := &Group{ID: gid, Platform: PlatformOpenAI, AllowBatchImageGeneration: true, ImagePrice1K: ptrFloat64(0.01), RateMultiplier: 1, BatchImageHoldMultiplier: 1}
			svc.GroupRepo = &publicBatchImageGroupRepo{groups: map[int64]*Group{gid: group}}
			list, err := svc.ListModels(t.Context(), owner)
			require.NoError(t, err)
			require.Empty(t, list.Data, "unsupported 1K tariff must not admit a 2K-only model")
			group.ImagePrice2K = ptrFloat64(0.02)
			list, err = svc.ListModels(t.Context(), owner)
			require.NoError(t, err)
			require.Len(t, list.Data, 1)
			require.Equal(t, []string{"2K"}, list.Data[0].Capabilities.ImageSizes)
		})
	}
}
