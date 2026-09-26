package admin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGrokProxyPoolEligibleSubscriptionTier(t *testing.T) {
	tests := []struct {
		name string
		tier string
		want bool
	}{
		{name: "missing upstream tier", tier: "", want: true},
		{name: "free", tier: "free", want: true},
		{name: "x basic", tier: "x_basic", want: true},
		{name: "numeric x basic", tier: "2", want: false},
		{name: "paid", tier: "supergrok", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isGrokProxyPoolEligibleSubscriptionTier(tt.tier))
		})
	}
}

func TestGrokSSOImportRejectsManualProxyAndProxyPoolTogether(t *testing.T) {
	h := &GrokOAuthHandler{}
	manualProxyID := int64(11)
	poolID := int64(3)
	result := h.createAccountFromSSOToken(context.Background(), GrokSSOToOAuthRequest{
		ProxyID:     &manualProxyID,
		ProxyPoolID: &poolID,
	}, "sso-token", 0, 1)

	require.False(t, result.created)
	require.Contains(t, result.item.Error, "mutually exclusive")
}
