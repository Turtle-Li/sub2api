package handler

import (
	"context"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDesktopVerificationURIEncodesOnlyTheUserCode(t *testing.T) {
	base, err := url.Parse("https://console.example.test/control/desktop-auth")
	require.NoError(t, err)

	verification := desktopVerificationURI(base, "ABCD-EFGH")
	parsed, err := url.Parse(verification)
	require.NoError(t, err)
	require.Equal(t, "https", parsed.Scheme)
	require.Equal(t, "console.example.test", parsed.Host)
	require.Equal(t, "/control/desktop-auth", parsed.Path)
	require.Equal(t, "ABCD-EFGH", parsed.Query().Get("user_code"))
}

func TestDesktopVerificationBaseURIRejectsUnusableSettings(t *testing.T) {
	var handler *DesktopAuthHandler
	_, err := handler.desktopVerificationBaseURI(context.Background())
	require.Error(t, err)
}
