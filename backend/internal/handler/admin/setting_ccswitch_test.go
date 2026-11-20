//go:build unit

package admin

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpdateSettingsCCSwitchCodexModel(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyCCSwitchCodexModel: "gpt-6.1-sol",
		service.SettingKeyAPIBaseURL:         "https://api.example.com",
	})
	rec := doUpdateSettings(t, h, map[string]any{"site_name": "Example"}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "gpt-6.1-sol", repo.values[service.SettingKeyCCSwitchCodexModel])
	_, written := repo.lastUpdates[service.SettingKeyCCSwitchCodexModel]
	require.False(t, written)

	rec = doUpdateSettings(t, h, map[string]any{"ccswitch_codex_model": " gpt-6.2-astra "}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "gpt-6.2-astra", repo.values[service.SettingKeyCCSwitchCodexModel])
	require.Contains(t, rec.Body.String(), `"ccswitch_codex_model":"gpt-6.2-astra"`)
	require.Equal(t, "https://api.example.com", repo.values[service.SettingKeyAPIBaseURL])

	rec = doUpdateSettings(t, h, map[string]any{"ccswitch_codex_model": "gpt-6.1-sol\nbad"}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "gpt-6.2-astra", repo.values[service.SettingKeyCCSwitchCodexModel])

	rec = doUpdateSettings(t, h, map[string]any{"ccswitch_codex_model": " "}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "gpt-6.2-sol", repo.values[service.SettingKeyCCSwitchCodexModel])
}
