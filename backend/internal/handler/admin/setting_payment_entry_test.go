//go:build unit

package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaymentEntryOnlyUpdateIsRecognized(t *testing.T) {
	var req UpdateSettingsRequest
	require.NoError(t, json.Unmarshal([]byte(`{"payment_entry_enabled":false}`), &req))
	require.NotNil(t, req.PaymentEntryEnabled)
	require.False(t, *req.PaymentEntryEnabled)
	require.True(t, hasPaymentFields(req))
	require.Nil(t, req.PaymentEnabled)
	require.False(t, hasPaymentFields(UpdateSettingsRequest{}))
}
