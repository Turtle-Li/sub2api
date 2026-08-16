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

func TestSettingHandlerGetDesktopPromotionsOnlyReturnsActiveItems(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewSettingHandler(service.NewSettingService(&settingHandlerPublicRepoStub{
		values: map[string]string{
			service.SettingKeyDesktopPromotions: `[
				{"id":"turtle-chat","title":"Turtle Chat","summary":"网页聊天","target_url":"https://chat.example.com","cta_label":"打开","icon":"chat","surfaces":["discover"],"enabled":true,"sort_order":1},
				{"id":"hidden","title":"Hidden","summary":"","target_url":"https://hidden.example.com","cta_label":"打开","icon":"link","surfaces":["discover"],"enabled":false,"sort_order":2}
			]`,
		},
	}, &config.Config{}), "test-version")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/desktop-promotions", nil)

	h.GetDesktopPromotions(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Code int `json:"code"`
		Data struct {
			SchemaVersion int                        `json:"schema_version"`
			Items         []service.DesktopPromotion `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, 1, response.Data.SchemaVersion)
	require.Len(t, response.Data.Items, 1)
	require.Equal(t, "turtle-chat", response.Data.Items[0].ID)
}

func TestSettingHandlerGetDesktopToolsSupportsVersionProbeAndDynamicTools(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &settingHandlerPublicRepoStub{values: map[string]string{
		service.SettingKeyDesktopTools: `{
			"schema_version":1,
			"version":9,
			"tools":[
				{"id":"test_tool","name":"测试工具","description":"无需更新客户端","icon":"tool","enabled":true,"ui":{"type":"switch"},"action":{"type":"config_update","target":"codex"},"defaults":{"enabled":false},"settings":[]},
				{"id":"hidden_tool","name":"隐藏工具","description":"停用","icon":"tool","enabled":false,"ui":{"type":"switch"},"action":{"type":"config_update","target":"codex"},"defaults":{"enabled":false},"settings":[]}
			]
		}`,
	}}
	h := NewSettingHandler(service.NewSettingService(repo, &config.Config{}), "test-version")

	versionRecorder := httptest.NewRecorder()
	versionContext, _ := gin.CreateTestContext(versionRecorder)
	versionContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/desktop/tools/version", nil)
	h.GetDesktopToolVersion(versionContext)
	require.Equal(t, http.StatusOK, versionRecorder.Code)
	require.JSONEq(t, `{"code":0,"data":{"schema_version":1,"version":9},"message":"success"}`, versionRecorder.Body.String())

	catalogRecorder := httptest.NewRecorder()
	catalogContext, _ := gin.CreateTestContext(catalogRecorder)
	catalogContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/desktop/tools", nil)
	h.GetDesktopTools(catalogContext)
	require.Equal(t, http.StatusOK, catalogRecorder.Code)
	var response struct {
		Data service.DesktopToolCatalog `json:"data"`
	}
	require.NoError(t, json.Unmarshal(catalogRecorder.Body.Bytes(), &response))
	require.Equal(t, int64(9), response.Data.Version)
	require.Len(t, response.Data.Tools, 1)
	require.Equal(t, "test_tool", response.Data.Tools[0].ID)
}

func TestSettingHandlerGetDesktopUpdatePolicyComputesRequiredStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewSettingHandler(service.NewSettingService(&settingHandlerPublicRepoStub{
		values: map[string]string{
			service.SettingKeyDesktopUpdatePolicy: `{
				"schema_version":1,
				"latest_version":"1.4.0",
				"minimum_supported_version":"1.2.0",
				"enforcement_enabled":true,
				"reason":"security fix",
				"manual_download_url":"https://download.example.com/tt-switch"
			}`,
		},
	}, &config.Config{}), "test-version")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/desktop/update-policy", nil)
	c.Request.Header.Set("User-Agent", "TT-Switch/1.1.9")

	h.GetDesktopUpdatePolicy(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Code int                         `json:"code"`
		Data service.DesktopUpdateStatus `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Data.UpdateRequired)
	require.True(t, response.Data.UpdateAvailable)
	require.Equal(t, "1.2.0", response.Data.MinimumSupportedVersion)
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
