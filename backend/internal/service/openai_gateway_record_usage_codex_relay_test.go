//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayRecordUsage_CodexRelayFastBilling(t *testing.T) {
	for _, tt := range []struct {
		name                      string
		compat, free              bool
		wantTier                  string
		accountFactor, userFactor float64
	}{
		{"public API downgrade", false, false, "default", 1, 1},
		{"relay Fast charged", true, false, "priority", 3, 3},
		{"relay free Fast", true, true, "priority", 3, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Given fixed group prices, a final outbound Fast turn and a default echo.
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			groupID := int64(77)
			inputPrice, outputPrice, fastMultiplier := 0.001, 0.002, 3.0
			tier := "priority"
			result := &OpenAIForwardResult{RequestID: "req_relay_fast_usage", ServiceTier: &tier, UpstreamResponseServiceTier: "default", Model: "gpt-5.6-sol", Usage: OpenAIUsage{InputTokens: 100, OutputTokens: 50}, Duration: time.Second}
			apiKey := &APIKey{ID: 1020, GroupID: &groupID, Group: &Group{
				ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 0.5, FreeOpenAIFast: tt.free,
				ModelPricing: []ChannelModelPricing{{Models: []string{"gpt-5.6-sol"}, BillingMode: BillingModeToken, InputPrice: &inputPrice, OutputPrice: &outputPrice, FastMultiplier: &fastMultiplier}},
			}}
			account := &Account{ID: 3020, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{OpenAICodexOAuthRelayExtraKey: tt.compat}}

			// When real usage recording settles tier and customer/account costs.
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: apiKey, User: &User{ID: 2020}, Account: account})

			// Then persisted tier and both accounting bases reflect the chosen contract.
			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			require.NotNil(t, usageRepo.lastLog.ServiceTier)
			require.Equal(t, tt.wantTier, *usageRepo.lastLog.ServiceTier)
			standardCost := 100*inputPrice + 50*outputPrice
			require.InDelta(t, standardCost*tt.accountFactor, usageRepo.lastLog.TotalCost, 1e-10)
			require.InDelta(t, standardCost*tt.userFactor*0.5, usageRepo.lastLog.ActualCost, 1e-10)
			require.Equal(t, "default", result.UpstreamResponseServiceTier)
		})
	}
}
