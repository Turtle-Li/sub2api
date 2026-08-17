//go:build unit

package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpdateSettingsDesktopAppDownloadSources(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeySiteName: "Example Gateway",
	})
	sources := service.DefaultDesktopAppDownloadSources()
	sources.ClaudeMacOSURL = "https://downloads.example.com/claude.dmg"

	rec := doUpdateSettings(t, h, map[string]any{
		"desktop_app_download_sources": sources,
	}, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var stored service.DesktopAppDownloadSources
	require.NoError(t, json.Unmarshal(
		[]byte(repo.values[service.SettingKeyDesktopAppDownloadSources]),
		&stored,
	))
	require.Equal(t, sources, stored)
	require.Equal(t, "Example Gateway", repo.values[service.SettingKeySiteName])
}

func TestUpdateSettingsDesktopAppDownloadSourcesRejectsInsecureURL(t *testing.T) {
	h, _ := newStepUpSwitchTestHandler(t, map[string]string{})
	sources := service.DefaultDesktopAppDownloadSources()
	sources.ChatGPTWindowsInstallerURL = "http://downloads.example.com/chatgpt.exe"

	rec := doUpdateSettings(t, h, map[string]any{
		"desktop_app_download_sources": sources,
	}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
