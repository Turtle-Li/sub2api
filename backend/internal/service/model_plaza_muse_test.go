//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func musePlazaFixture() (Channel, Group) {
	const id = 27
	return Channel{
			ID: 1, Name: "MUSE", Status: StatusActive, GroupIDs: []int64{id},
			ModelMapping: map[string]map[string]string{PlatformOpenAI: {
				"muse-spark": "muse-spark", MuseImageModel: MuseImageModel, MuseVideoModel: MuseVideoModel,
			}},
		}, Group{
			ID: id, Name: "Muse", Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeStandard, IsExclusive: true,
			RateMultiplier: 1, ImagePrice1K: testPtrFloat64(0.10), VideoPrice720P: testPtrFloat64(0.99),
			ModelAllowlist:   GroupModelAllowlist{Enabled: true, Models: []string{legacyMuseTextModel, MuseImageModel, MuseVideoModel}},
			VideoModelPrices: map[string]map[string]float64{MuseVideoModel: {VideoBillingResolution720P: 0.06, VideoBillingResolution1080P: 0.2}},
			ModelPricing: []ChannelModelPricing{
				{Platform: PlatformOpenAI, Models: []string{"muse-spark"}, Currency: PricingCurrencyUSD, BillingMode: BillingModePerRequest, PerRequestPrice: testPtrFloat64(0.005)},
				{Platform: PlatformOpenAI, Models: []string{MuseImageModel}, Currency: PricingCurrencyCNY, BillingMode: BillingModeImage,
					Intervals: []PricingInterval{{TierLabel: ImageBillingSize4K, PerRequestPrice: testPtrFloat64(0.24)}}},
			},
		}
}

func TestListPlazaGroups_MuseUsesNativePricesWithoutChangingPlatform(t *testing.T) {
	ch, group := musePlazaFixture()
	svc := newPlazaServiceWithBilling([]Channel{ch}, []Group{group}, map[int64]string{group.ID: PlatformOpenAI}, nil)
	svc.billingService.currencyPolicy.Store(PricingCurrencySettings{SettlementCurrency: "CNY", USDToCNYRate: 6.75})
	groups, err := svc.ListGroups(t.Context())
	require.NoError(t, err)
	require.Len(t, groups, 1)
	pg := groups[0]
	require.Equal(t, "muse", pg.CatalogProvider)
	require.Equal(t, PlatformOpenAI, pg.Platform)
	require.True(t, pg.IsExclusive)
	require.Equal(t, "CNY", pg.DisplayCurrency)
	require.Equal(t, 1.0, pg.DisplayUSDRate)
	models := plazaModelsByName(pg.Models)
	require.Len(t, models, 2)
	require.NotContains(t, models, legacyMuseTextModel)
	require.Equal(t, &PlazaUnitPricing{Unit: "image", Quotes: []PlazaUnitQuote{
		{Label: ImageBillingSize1K, Price: 0.10},
		{Label: ImageBillingSize2K, Price: 0.10}, // Same fallback used to admit and settle 2K requests.
		{Label: ImageBillingSize4K, Price: 0.24},
	}}, models[MuseImageModel].UnitPricing)
	require.Equal(t, &PlazaUnitPricing{Unit: "second", Quotes: []PlazaUnitQuote{
		{Label: VideoBillingResolution720P, Price: 0.06},
	}}, models[MuseVideoModel].UnitPricing)
}

func TestListPlazaGroups_MuseDisabledModelsIgnoreStaleChannelMappingsAndPrices(t *testing.T) {
	ch, group := musePlazaFixture()
	group.ModelAllowlist.Models = []string{MuseImageModel}
	ch.ModelMapping[PlatformOpenAI]["gpt-other"] = "gpt-other"
	delete(ch.ModelMapping[PlatformOpenAI], legacyMuseTextModel)
	ch.ModelPricing = []ChannelModelPricing{{
		Platform: PlatformOpenAI, Models: []string{legacyMuseTextModel}, BillingMode: BillingModePerRequest, PerRequestPrice: testPtrFloat64(0.005),
	}}
	groups, err := newPlazaService([]Channel{ch}, []Group{group}, nil).ListGroups(t.Context())
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, "muse", groups[0].CatalogProvider)
	require.Len(t, groups[0].Models, 1)
	require.Equal(t, MuseImageModel, groups[0].Models[0].Name)
}

func TestListPlazaGroups_MuseDoesNotRelabelMixedGroupsOrAliases(t *testing.T) {
	ch, group := musePlazaFixture()
	ch.ModelMapping[PlatformOpenAI]["gpt-6.2-sol"] = "gpt-6.2-sol"
	group.ModelAllowlist.Models = append(group.ModelAllowlist.Models, "gpt-6.2-sol")
	otherGroup := group
	otherGroup.ID = 41
	aliasChannel := Channel{
		ID: 2, Status: StatusActive, GroupIDs: []int64{otherGroup.ID},
		ModelMapping: map[string]map[string]string{PlatformOpenAI: {"muse-spark-1.3-contributor": "muse-spark-1.3-contributor"}},
	}
	groups, err := newPlazaService([]Channel{ch, aliasChannel}, []Group{group, otherGroup}, nil).ListGroups(t.Context())
	require.NoError(t, err)
	require.Len(t, groups, 2)
	for _, g := range groups {
		require.Empty(t, g.CatalogProvider)
		for _, model := range g.Models {
			require.Nil(t, model.UnitPricing)
		}
	}
}

func TestListPlazaGroups_MuseVideoRespectsBillingAndAdmission(t *testing.T) {
	for _, tc := range []struct {
		name             string
		mode             BillingMode
		canAdmit         bool
		wantSecondsQuote bool
	}{
		{name: "group video card", mode: BillingModeVideo, canAdmit: true, wantSecondsQuote: true},
		{name: "no admitted video price", mode: BillingModeVideo},
		{name: "token override is not a seconds quote", mode: BillingModeToken, canAdmit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch, group := musePlazaFixture()
			if !tc.canAdmit {
				group.VideoPrice720P, group.VideoModelPrices = nil, nil
			}
			group.ModelPricing = append(group.ModelPricing, ChannelModelPricing{
				Platform: PlatformOpenAI, Models: []string{MuseVideoModel}, BillingMode: tc.mode,
				Currency: PricingCurrencyCNY, PerRequestPrice: testPtrFloat64(0.55),
				Intervals: []PricingInterval{{TierLabel: VideoBillingResolution720P, PerRequestPrice: testPtrFloat64(0.24)}},
			})
			svc := newPlazaServiceWithBilling([]Channel{ch}, []Group{group}, map[int64]string{group.ID: PlatformOpenAI}, nil)
			svc.billingService.currencyPolicy.Store(PricingCurrencySettings{SettlementCurrency: "CNY", USDToCNYRate: 6.75})
			groups, err := svc.ListGroups(t.Context())
			require.NoError(t, err)
			require.Len(t, groups, 1)
			price := plazaModelsByName(groups[0].Models)[MuseVideoModel].UnitPricing
			require.NotNil(t, price)
			require.Equal(t, "second", price.Unit)
			if !tc.wantSecondsQuote {
				require.Empty(t, price.Quotes)
				return
			}
			require.Len(t, price.Quotes, 1)
			require.Equal(t, VideoBillingResolution720P, price.Quotes[0].Label)
			key := &APIKey{Group: &group}
			gateway := &OpenAIGatewayService{billingService: svc.billingService, resolver: svc.resolver}
			cost := gateway.calculateOpenAIVideoCost(t.Context(), MuseVideoModel, key, &OpenAIForwardResult{
				VideoCount: 1, VideoResolution: VideoBillingResolution720P, VideoDurationSeconds: 5,
			}, 1)
			finalizeUsageCurrency(cost, key, false)
			require.InDelta(t, 0.24/6.75, price.Quotes[0].Price, 1e-12)
			require.InDelta(t, cost.ActualCost, 5*price.Quotes[0].Price, 1e-12)
		})
	}
}
