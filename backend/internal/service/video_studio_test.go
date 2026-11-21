package service

import "testing"

func TestVideoStudioDurationCapabilities(t *testing.T) {
	for _, tc := range []struct {
		id            string
		mode          VideoStudioDurationMode
		min, max, def int
	}{{MuseVideoModel, VideoStudioDurationAutomatic, 6, 6, 6}, {GoodPutH3Model, VideoStudioDurationSelectable, 4, 15, 5}, {GoodPutH3FastModel, VideoStudioDurationSelectable, 5, 14, 5}} {
		t.Run(tc.id, func(t *testing.T) {
			m := studioModel(tc.id)
			if m.DurationMode != tc.mode || m.MinSeconds != tc.min || m.MaxSeconds != tc.max || m.DefaultSeconds != tc.def {
				t.Fatalf("incorrect duration capabilities: %+v", m)
			}
		})
	}
}
func TestVideoStudioQuoteBindsBothPriceAndWalletCurrency(t *testing.T) {
	ctx := WithVideoStudioQuote(t.Context(), 0.75, "CNY")
	if !acceptsStudioQuote(ctx, 0.75, "CNY") || acceptsStudioQuote(ctx, 0.75, "USD") || acceptsStudioQuote(ctx, 0.76, "CNY") {
		t.Fatal("changed wallet amount or denomination must require a new quote")
	}
	if !acceptsStudioQuote(t.Context(), 0.75, "USD") {
		t.Fatal("existing non-studio API requests have no studio quote")
	}
}
