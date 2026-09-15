package ent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupSubscriptionProductCodeIsInternalJSON(t *testing.T) {
	code := "openai_plus"
	encoded, err := json.Marshal(&Group{ID: 4, SubscriptionProductCode: &code})
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "subscription_product_code")
	require.NotContains(t, string(encoded), code)
}
