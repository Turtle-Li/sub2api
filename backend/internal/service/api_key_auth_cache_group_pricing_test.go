package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyAuthSnapshotGroupPricingRoundTrip(t *testing.T) {
	inputPrice := 2.5e-6
	outputPrice := 10e-6
	groupID := int64(91)
	apiKey := &APIKey{
		ID:      17,
		UserID:  23,
		GroupID: &groupID,
		Key:     "group-pricing-roundtrip",
		Status:  StatusActive,
		User: &User{
			ID:          23,
			Status:      StatusActive,
			Role:        RoleUser,
			Concurrency: 2,
		},
		Group: &Group{
			ID:                        groupID,
			Name:                      "priced-openai",
			Platform:                  PlatformOpenAI,
			Status:                    StatusActive,
			SubscriptionType:          SubscriptionTypeStandard,
			RateMultiplier:            1,
			LongContextPricingEnabled: true,
			ModelPricing: []ChannelModelPricing{{
				Platform:    PlatformOpenAI,
				Models:      []string{"gpt-5.4"},
				BillingMode: BillingModeToken,
				InputPrice:  &inputPrice,
				OutputPrice: &outputPrice,
			}},
		},
	}

	svc := &APIKeyService{}
	snapshot := svc.snapshotFromAPIKey(context.Background(), apiKey)
	require.NotNil(t, snapshot)
	require.Equal(t, apiKeyAuthSnapshotVersion, snapshot.Version)

	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: snapshot})
	require.NoError(t, err)
	var restored APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &restored))

	materialized, used, err := svc.applyAuthCacheEntry(apiKey.Key, &restored)
	require.NoError(t, err)
	require.True(t, used)
	require.NotNil(t, materialized.Group)
	require.True(t, materialized.Group.LongContextPricingEnabled)
	require.Equal(t, apiKey.Group.ModelPricing, materialized.Group.ModelPricing)
	require.NotNil(t, matchGroupModelPricing(materialized.Group, "gpt-5.4"))

	materialized.Group.ModelPricing[0].Models[0] = "changed"
	require.Equal(t, "gpt-5.4", apiKey.Group.ModelPricing[0].Models[0])
}
