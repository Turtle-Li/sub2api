package service

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGoodPutVideoRequiresPositiveGroupVideoPrice(t *testing.T) {
	price := floatPtr(0.15)
	videoCard := ChannelModelPricing{Models: []string{GoodPutH3Model}, BillingMode: BillingModeVideo, Currency: "USD", PerRequestPrice: price}
	tierCard := videoCard
	tierCard.PerRequestPrice = nil
	tierCard.Intervals = []PricingInterval{{TierLabel: VideoBillingResolution720P, PerRequestPrice: price}}
	zeroCard := videoCard
	zeroCard.PerRequestPrice = floatPtr(0)
	tokenCard := videoCard
	tokenCard.BillingMode = BillingModeToken
	wildcardCard := videoCard
	wildcardCard.Models = []string{"minimax/*"}
	for _, tt := range []struct {
		name   string
		cards  []ChannelModelPricing
		legacy *float64
		ok     bool
	}{
		{"exact per-model video price", []ChannelModelPricing{videoCard}, nil, true},
		{"720p tier price", []ChannelModelPricing{tierCard}, nil, true},
		{"legacy 720p price", nil, price, true},
		{"missing price", nil, nil, false},
		{"zero card overrides positive legacy price", []ChannelModelPricing{zeroCard}, price, false},
		{"token card with positive legacy price", []ChannelModelPricing{tokenCard}, price, false},
		{"wildcard is not an explicit H3 price", []ChannelModelPricing{wildcardCard}, nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			billing := NewBillingService(&config.Config{}, nil)
			billing.currencyPolicy.Store(PricingCurrencySettings{SettlementCurrency: "CNY", USDToCNYRate: 6.75})
			gateway := OpenAIGatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
			group := &Group{
				ID: 46, Platform: PlatformOpenAI, IsExclusive: true, SubscriptionType: SubscriptionTypeStandard,
				ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{GoodPutH3Model}},
				ModelPricing:   tt.cards, VideoPrice720P: tt.legacy,
			}
			key := &APIKey{GroupID: &group.ID, Group: group}

			require.Equal(t, tt.ok, gateway.HasGoodPutVideoPrice(t.Context(), key, GoodPutH3Model))
			if tt.ok {
				cost := gateway.calculateOpenAIVideoCost(t.Context(), GoodPutH3Model, key, &OpenAIForwardResult{
					VideoCount: 1, VideoDurationSeconds: 5, VideoResolution: VideoBillingResolution720P,
				}, 1)
				finalizeUsageCurrency(cost, key, false)
				require.InDelta(t, 0.75, cost.ActualCost, 0.000001)
			}
		})
	}
}

func TestGoodPutVideoRequest(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		seconds    int
	}{
		{"standard accepts 4 seconds and a square", `{"model":"minimax/h3","prompt":"Clouds move","seconds":4,"size":"768x768"}`, 4},
		{"fast accepts string seconds and seed", `{"model":"minimax/h3-fast","prompt":"Clouds move","seconds":"15","size":"768x1344","seed":7}`, 14},
		{"fast defaults to five seconds", `{"model":"minimax/h3-fast","prompt":"Clouds move"}`, 5},
		{"related model is not H3", `{"model":"minimax/h3-lite","prompt":"Clouds move","seconds":5}`, 0},
		{"fractional seconds", `{"model":"minimax/h3","prompt":"Clouds move","seconds":6.5}`, 0},
		{"unavailable fast duration", `{"model":"minimax/h3-fast","prompt":"Clouds move","seconds":4}`, 0},
		{"unavailable fast canvas", `{"model":"minimax/h3-fast","prompt":"Clouds move","size":"768x768"}`, 0},
		{"non-fast seed", `{"model":"minimax/h3","prompt":"Clouds move","seed":7}`, 0},
		{"fast rejects out-of-range seed", `{"model":"minimax/h3-fast","prompt":"Clouds move","seed":2147483648}`, 0},
		{"private media userinfo", `{"model":"minimax/h3","prompt":"Clouds move","input_reference":"https://user:password@media.example/frame.png"}`, 0},
		{"reference audio URL", `{"model":"minimax/h3","prompt":"Clouds move","media":[{"type":"reference_audio","url":"https://media.example/voice.wav"}]}`, 5},
		{"frames and references cannot mix", `{"model":"minimax/h3","prompt":"Clouds move","input_reference":"https://media.example/frame.png","media":[{"type":"reference_video","url":"https://media.example/ref.mp4"}]}`, 0},
		{"no unsupported overrides", `{"model":"minimax/h3","prompt":"Clouds move","resolution":"2k"}`, 0},
		{"reject a second JSON object", `{"model":"minimax/h3","prompt":"Clouds move"}{}`, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, err := ParseGoodPutVideoRequest("application/json", []byte(tt.body))
			if tt.seconds == 0 {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.seconds, req.Info().DurationSeconds)
		})
	}
}

func TestGoodPutVideoCatalogPriceGuard(t *testing.T) {
	const catalog = `{"models":[{"public_model":"minimax/h3","price":{"mode":"meter","currency":"CNY","per_video_second":"0.090000","per_image":null,"input_per_1m":null,"meter_prices":[{"meter":"video_seconds","unit":"second","price_per_unit":"0.090000000000000"}]}}]}`
	for _, tt := range []struct {
		name, body string
		ok         bool
	}{
		{"current promo", catalog, true},
		{"lower", strings.ReplaceAll(catalog, "0.090000", "0.080000"), true},
		{"regular price", strings.ReplaceAll(catalog, "0.090000", "0.500000"), false},
		{"barely exceeds cap", strings.Replace(catalog, `"0.090000"`, `"0.090000000000001"`, 1), false},
		{"changed currency", strings.ReplaceAll(catalog, "CNY", "USD"), false},
		{"changed pricing mode", strings.Replace(catalog, `"mode":"meter"`, `"mode":"token"`, 1), false},
		{"new image price", strings.Replace(catalog, `"per_image":null`, `"per_image":"0.10"`, 1), false},
		{"new token price", strings.Replace(catalog, `"input_per_1m":null`, `"input_per_1m":"1.00"`, 1), false},
		{"new time window", strings.Replace(catalog, `"per_image":null`, `"windows":[{}]`, 1), false},
		{"unexpected additional meter", strings.Replace(catalog, `}]}}`, `},{"meter":"reference_video_seconds","unit":"second","price_per_unit":"0.010000"}]}}`, 1), false},
		{"unlisted model", strings.ReplaceAll(catalog, "minimax/h3", "minimax/h3-fast"), false},
		{"invalid price", strings.ReplaceAll(catalog, "0.090000", "NaN"), false},
		{"trailing document", catalog + `{}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.ok, goodPutVideoCatalogAllows(strings.NewReader(tt.body), GoodPutH3Model))
		})
	}
}
