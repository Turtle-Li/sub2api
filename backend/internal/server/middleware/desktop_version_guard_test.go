//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type desktopVersionRepoStub struct {
	values map[string]string
}

func (s *desktopVersionRepoStub) Get(context.Context, string) (*service.Setting, error) {
	panic("unexpected Get")
}
func (s *desktopVersionRepoStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}
func (s *desktopVersionRepoStub) Set(context.Context, string, string) error {
	panic("unexpected Set")
}
func (s *desktopVersionRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	panic("unexpected GetMultiple")
}
func (s *desktopVersionRepoStub) SetMultiple(context.Context, map[string]string) error {
	panic("unexpected SetMultiple")
}
func (s *desktopVersionRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll")
}
func (s *desktopVersionRepoStub) Delete(context.Context, string) error {
	panic("unexpected Delete")
}

func TestDesktopVersionGuardBlocksUnsupportedOfficialClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &desktopVersionRepoStub{values: map[string]string{
		service.SettingKeyDesktopUpdatePolicy: `{
			"schema_version":1,
			"latest_version":"1.4.0",
			"minimum_supported_version":"1.2.0",
			"enforcement_enabled":true,
			"reason":"security fix"
		}`,
	}}
	settingService := service.NewSettingService(repo, &config.Config{})
	router := gin.New()
	router.Use(DesktopVersionGuard(settingService))
	router.GET("/api/v1/user/profile", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	oldRequest := httptest.NewRequest(http.MethodGet, "/api/v1/user/profile", nil)
	oldRequest.Header.Set("User-Agent", "TT-Switch/1.1.9")
	oldResponse := httptest.NewRecorder()
	router.ServeHTTP(oldResponse, oldRequest)
	require.Equal(t, http.StatusUpgradeRequired, oldResponse.Code)
	require.Contains(t, oldResponse.Body.String(), "DESKTOP_UPDATE_REQUIRED")
	require.Contains(t, oldResponse.Body.String(), `"minimum_supported_version":"1.2.0"`)

	newRequest := httptest.NewRequest(http.MethodGet, "/api/v1/user/profile", nil)
	newRequest.Header.Set("X-TT-Switch-Version", "1.2.0")
	newResponse := httptest.NewRecorder()
	router.ServeHTTP(newResponse, newRequest)
	require.Equal(t, http.StatusNoContent, newResponse.Code)
}

func TestDesktopVersionGuardLeavesWebAndUpdateRecoveryRoutesAvailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &desktopVersionRepoStub{values: map[string]string{
		service.SettingKeyDesktopUpdatePolicy: `{
			"schema_version":1,
			"latest_version":"2.0.0",
			"minimum_supported_version":"2.0.0",
			"enforcement_enabled":true
		}`,
	}}
	settingService := service.NewSettingService(repo, &config.Config{})
	router := gin.New()
	router.Use(DesktopVersionGuard(settingService))
	router.GET("/api/v1/user/profile", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.GET("/api/v1/desktop/update-policy", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	webRequest := httptest.NewRequest(http.MethodGet, "/api/v1/user/profile", nil)
	webRequest.Header.Set("User-Agent", "Mozilla/5.0")
	webResponse := httptest.NewRecorder()
	router.ServeHTTP(webResponse, webRequest)
	require.Equal(t, http.StatusNoContent, webResponse.Code)

	recoveryRequest := httptest.NewRequest(http.MethodGet, "/api/v1/desktop/update-policy", nil)
	recoveryRequest.Header.Set("User-Agent", "TT-Switch/1.0.0")
	recoveryResponse := httptest.NewRecorder()
	router.ServeHTTP(recoveryResponse, recoveryRequest)
	require.Equal(t, http.StatusNoContent, recoveryResponse.Code)
}
