//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizePricingCurrency(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		err  bool
	}{
		{name: "legacy empty defaults to USD", raw: "", want: PricingCurrencyUSD},
		{name: "whitespace empty defaults to USD", raw: "  ", want: PricingCurrencyUSD},
		{name: "USD preserved", raw: "USD", want: PricingCurrencyUSD},
		{name: "CNY canonicalized", raw: " cny ", want: PricingCurrencyCNY},
		{name: "unknown rejected", raw: "EUR", err: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePricingCurrency(tt.raw)
			if tt.err {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestValidatePricingEntriesNormalizesCardCurrency(t *testing.T) {
	price := 1.0
	pricing := []ChannelModelPricing{{
		Models:      []string{"gpt-5"},
		BillingMode: BillingModeToken,
		Currency:    " cny ",
		InputPrice:  &price,
	}}

	require.NoError(t, validatePricingEntries(pricing))
	require.Equal(t, PricingCurrencyCNY, pricing[0].Currency)
}

func TestValidatePricingEntriesRejectsUnknownCardCurrency(t *testing.T) {
	price := 1.0
	err := validatePricingEntries([]ChannelModelPricing{{
		Models:      []string{"gpt-5"},
		BillingMode: BillingModeToken,
		Currency:    "EUR",
		InputPrice:  &price,
	}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "INVALID_PRICING_CURRENCY")
}

func TestChannelModelPricingClonePreservesLegacyCurrency(t *testing.T) {
	cloned := (ChannelModelPricing{Currency: ""}).Clone()
	require.Empty(t, cloned.Currency)
}
