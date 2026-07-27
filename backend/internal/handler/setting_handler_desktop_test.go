//go:build unit

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingHandlerGetDesktopSettingsUsesConfiguredControlPlane(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewSettingHandler(service.NewSettingService(&settingHandlerPublicRepoStub{
		values: map[string]string{
			service.SettingKeyDesktopControlPlaneURL: "https://control.example.com/",
		},
	}, &config.Config{}), "test-version")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "https://discovery.example.com/api/v1/settings/desktop", nil)

	h.GetDesktopSettings(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	response := decodeDesktopSettingsResponse(t, recorder)
	require.Equal(t, 0, response.Code)
	require.Equal(t, 1, response.Data.SchemaVersion)
	require.Equal(t, "https://control.example.com", response.Data.ControlPlaneURL)
}

func TestSettingHandlerGetDesktopSettingsFallsBackToRequestOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewSettingHandler(service.NewSettingService(&settingHandlerPublicRepoStub{
		values: map[string]string{
			service.SettingKeyAPIBaseURL: "https://models.example.com",
		},
	}, &config.Config{}), "test-version")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "http://internal:8080/api/v1/settings/desktop", nil)
	c.Request.Host = "discovery.example.com"
	c.Request.Header.Set("X-Forwarded-Proto", "https")

	h.GetDesktopSettings(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	response := decodeDesktopSettingsResponse(t, recorder)
	require.Equal(t, 0, response.Code)
	require.Equal(t, 1, response.Data.SchemaVersion)
	require.Equal(t, "https://discovery.example.com", response.Data.ControlPlaneURL)
}

type desktopSettingsResponse struct {
	Code int `json:"code"`
	Data struct {
		SchemaVersion   int    `json:"schema_version"`
		ControlPlaneURL string `json:"control_plane_url"`
	} `json:"data"`
}

func decodeDesktopSettingsResponse(t *testing.T, recorder *httptest.ResponseRecorder) desktopSettingsResponse {
	t.Helper()
	var result desktopSettingsResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	return result
}
