package admin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

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
