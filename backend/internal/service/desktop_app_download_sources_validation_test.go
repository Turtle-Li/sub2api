//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDesktopAppDownloadSources_DefaultsAndValidation(t *testing.T) {
	defaults := DefaultDesktopAppDownloadSources()
	require.Equal(t, defaults, parseDesktopAppDownloadSources(""))

	custom := defaults
	custom.ChatGPTMacOSArm64URL = "https://downloads.example.com/chatgpt-arm64.dmg"
	custom.ChatGPTWindowsStoreProductID = "ABC12345"

	normalized, err := NormalizeAndValidateDesktopAppDownloadSources(custom)
	require.NoError(t, err)
	require.Equal(t, custom, normalized)

	custom.ClaudeWindowsX64URL = "http://downloads.example.com/claude.exe"
	_, err = NormalizeAndValidateDesktopAppDownloadSources(custom)
	require.ErrorContains(t, err, "must use https")
}

func TestDesktopAppDownloadSources_EmptyFieldsFallBackIndividually(t *testing.T) {
	normalized, err := NormalizeAndValidateDesktopAppDownloadSources(
		DesktopAppDownloadSources{
			ClaudeMacOSURL: "https://downloads.example.com/claude.dmg",
		},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		DefaultDesktopAppDownloadSources().ChatGPTMacOSArm64URL,
		normalized.ChatGPTMacOSArm64URL,
	)
	require.Equal(t, "https://downloads.example.com/claude.dmg", normalized.ClaudeMacOSURL)
}
