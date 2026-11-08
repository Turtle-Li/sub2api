//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeModelTariffsDoNotInheritMuseDefault(t *testing.T) {
	for _, test := range []struct {
		model, provider, size string
		want                  float64
	}{
		{"muse-image", BatchImageProviderMuse, "1K", .1},
		{"wan2.7-image", BatchImageProviderWan, "2K", .3},
		{"wan2.7-image-pro", BatchImageProviderWan, "1K", .6},
		{"doubao-seedream-5-0-lite-260128", BatchImageProviderSeedream, "4K", .32},
		{"doubao-seedream-5-0-pro-260628", BatchImageProviderSeedream, "1K", .4},
		{"doubao-seedream-5-0-pro-260628", BatchImageProviderSeedream, "2K", .7},
	} {
		t.Run(test.model+"/"+test.size, func(t *testing.T) {
			// Given one group/key with independently authored native image cards.
			svc, _, _, _, _ := newTestBatchImagePublicService(true)
			gid := int64(27)
			group := &Group{ID: gid, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeStandard,
				AllowBatchImageGeneration: true, ImageRateIndependent: true, ImageRateMultiplier: 1,
				ImagePrice1K: ptrFloat64(.1), ImagePrice2K: ptrFloat64(.1), ImagePrice4K: ptrFloat64(.1),
				BatchImageDiscountMultiplier: .5, BatchImageHoldMultiplier: 1,
				ModelPricing: []ChannelModelPricing{
					{Models: []string{"wan2.7-image"}, BillingMode: BillingModeImage, Currency: PricingCurrencyCNY, PerRequestPrice: ptrFloat64(.3)},
					{Models: []string{"wan2.7-image-pro"}, BillingMode: BillingModeImage, Currency: PricingCurrencyCNY, PerRequestPrice: ptrFloat64(.6)},
					{Models: []string{"doubao-seedream-5-0-lite-260128"}, BillingMode: BillingModeImage, Currency: PricingCurrencyCNY, PerRequestPrice: ptrFloat64(.32)},
					{Models: []string{"doubao-seedream-5-0-pro-260628"}, BillingMode: BillingModeImage, Currency: PricingCurrencyCNY,
						Intervals: []PricingInterval{{TierLabel: "1K", PerRequestPrice: ptrFloat64(.4)}, {TierLabel: "2K", PerRequestPrice: ptrFloat64(.7)}}},
				}}
			svc.GroupRepo = &publicBatchImageGroupRepo{groups: map[int64]*Group{gid: group}}
			// When quoting that model and resolution.
			quote, err := svc.resolvePricingSnapshot(t.Context(), BatchImageOwner{UserID: 1, GroupID: &gid}, BatchImageSubmitRequest{Model: test.model, ImageSize: test.size}, test.provider, nil)
			// Then the exact per-image tariff is also the undiscounted native hold.
			require.NoError(t, err)
			require.InDelta(t, test.want, quote.BillableUnitPrice, 1e-12)
			require.InDelta(t, test.want, quote.HoldUnitPrice, 1e-12)
		})
	}
}
