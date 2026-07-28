//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type desktopConsoleRepoStub struct {
	values  map[string]string
	updates map[string]string
}

type desktopConsoleEncryptor struct{}

func (desktopConsoleEncryptor) Encrypt(value string) (string, error) {
	return "enc:" + value, nil
}

func (desktopConsoleEncryptor) Decrypt(value string) (string, error) {
	decrypted, ok := strings.CutPrefix(value, "enc:")
	if !ok {
		return "", errors.New("not encrypted")
	}
	return decrypted, nil
}

type desktopConsoleStorageProbe struct {
	key string
}

func (s *desktopConsoleStorageProbe) Save(
	context.Context,
	string,
	string,
	[]byte,
) (string, error) {
	return "", nil
}

func (s *desktopConsoleStorageProbe) Probe(_ context.Context, key string) error {
	s.key = key
	return nil
}

func (s *desktopConsoleRepoStub) Get(context.Context, string) (*service.Setting, error) {
	panic("unexpected Get call")
}

func (s *desktopConsoleRepoStub) GetValue(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}

func (s *desktopConsoleRepoStub) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	if s.updates == nil {
		s.updates = map[string]string{}
	}
	s.updates[key] = value
	return nil
}

func (s *desktopConsoleRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = s.values[key]
	}
	return result, nil
}

func (s *desktopConsoleRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	s.updates = make(map[string]string, len(settings))
	for key, value := range settings {
		s.values[key] = value
		s.updates[key] = value
	}
	return nil
}

func (s *desktopConsoleRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *desktopConsoleRepoStub) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

func TestDesktopConsoleHandlerReadsAndUpdatesOnlyDesktopSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &desktopConsoleRepoStub{values: map[string]string{
		service.SettingKeyDesktopControlPlaneURL: "https://accounts.example.com",
		service.SettingKeyDesktopPromotions:      "[]",
		service.SettingKeyDesktopUpdatePolicy:    `{"schema_version":1,"latest_version":"1.1.0","minimum_supported_version":"","enforcement_enabled":false}`,
		"unrelated_setting":                      "preserve-me",
	}}
	handler := NewSettingHandler(
		service.NewSettingService(repo, &config.Config{}),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	getRecorder := httptest.NewRecorder()
	getContext, _ := gin.CreateTestContext(getRecorder)
	getContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/desktop-console/settings", nil)
	handler.GetDesktopConsoleSettings(getContext)

	require.Equal(t, http.StatusOK, getRecorder.Code)
	var getResponse struct {
		Code int                            `json:"code"`
		Data service.DesktopConsoleSettings `json:"data"`
	}
	require.NoError(t, json.Unmarshal(getRecorder.Body.Bytes(), &getResponse))
	require.Equal(t, service.DesktopConsoleSchemaVersion, getResponse.Data.SchemaVersion)
	require.Equal(t, "https://accounts.example.com", getResponse.Data.ControlPlaneURL)

	body := []byte(`{
		"control_plane_url":"https://new-accounts.example.com/",
		"promotions":[{
			"id":"agent-desc-link",
			"title":"AgentDescLink",
			"summary":"Agent 工具",
			"target_url":"https://agent.example.com",
			"cta_label":"打开",
			"icon":"agent",
			"surfaces":["discover"],
			"enabled":true,
			"sort_order":10
		}],
		"update_policy":{
			"schema_version":1,
			"latest_version":"1.4.0",
			"minimum_supported_version":"1.2.0",
			"enforcement_enabled":true,
			"reason":"security fix",
			"manual_download_url":"https://download.example.com/tt-switch"
		}
	}`)
	putRecorder := httptest.NewRecorder()
	putContext, _ := gin.CreateTestContext(putRecorder)
	putContext.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/v1/desktop-console/settings",
		bytes.NewReader(body),
	)
	putContext.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateDesktopConsoleSettings(putContext)

	require.Equal(t, http.StatusOK, putRecorder.Code)
	require.Equal(t, "https://new-accounts.example.com", repo.updates[service.SettingKeyDesktopControlPlaneURL])
	require.Contains(t, repo.updates[service.SettingKeyDesktopPromotions], `"id":"agent-desc-link"`)
	require.Contains(t, repo.updates[service.SettingKeyDesktopUpdatePolicy], `"minimum_supported_version":"1.2.0"`)
	require.Len(t, repo.updates, 3)
	require.Equal(t, "preserve-me", repo.values["unrelated_setting"])
}

func TestDesktopConsoleHandlerRejectsPublicHTTPControlPlane(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &desktopConsoleRepoStub{values: map[string]string{}}
	handler := NewSettingHandler(
		service.NewSettingService(repo, &config.Config{}),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/v1/desktop-console/settings",
		bytes.NewBufferString(`{"control_plane_url":"http://accounts.example.com","promotions":[]}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateDesktopConsoleSettings(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Nil(t, repo.updates)
}

func TestDesktopConsoleStorageHandlersMaskSecretAndProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &desktopConsoleRepoStub{values: map[string]string{}}
	encryptor := desktopConsoleEncryptor{}
	backup := service.NewBackupService(repo, &config.Config{
		Totp: config.TotpConfig{EncryptionKeyConfigured: true},
	}, encryptor, nil, nil)
	probe := &desktopConsoleStorageProbe{}
	factory := func(
		_ context.Context,
		_ *config.ImageStorageConfig,
	) (service.ImageStorage, error) {
		return probe, nil
	}
	storage := service.NewDesktopStorageService(repo, encryptor, backup, factory)
	handler := NewSettingHandler(
		service.NewSettingService(repo, &config.Config{}),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	handler.SetDesktopStorageService(storage)

	body := []byte(`{
		"enabled":true,
		"region":"ap-guangzhou",
		"bucket":"tt-switch-1250000000",
		"secret_id":"AKIDEXAMPLE",
		"secret_key":"secret-value",
		"public_base_url":"https://download.example.com",
		"release_prefix":"releases",
		"theme_prefix":"themes",
		"quarantine_prefix":"theme-quarantine"
	}`)
	putRecorder := httptest.NewRecorder()
	putContext, _ := gin.CreateTestContext(putRecorder)
	putContext.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/v1/desktop-console/storage",
		bytes.NewReader(body),
	)
	putContext.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateDesktopStorageSettings(putContext)
	require.Equal(t, http.StatusOK, putRecorder.Code)
	require.NotContains(t, putRecorder.Body.String(), "secret-value")
	require.Contains(t, repo.values[service.SettingKeyDesktopStorageConfig], "enc:secret-value")

	getRecorder := httptest.NewRecorder()
	getContext, _ := gin.CreateTestContext(getRecorder)
	getContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/desktop-console/storage", nil)
	handler.GetDesktopStorageSettings(getContext)
	require.Equal(t, http.StatusOK, getRecorder.Code)
	require.Contains(t, getRecorder.Body.String(), `"secret_configured":true`)
	require.NotContains(t, getRecorder.Body.String(), "secret-value")

	testBody := bytes.Replace(body, []byte(`"secret_key":"secret-value"`), []byte(`"secret_key":""`), 1)
	testRecorder := httptest.NewRecorder()
	testContext, _ := gin.CreateTestContext(testRecorder)
	testContext.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/desktop-console/storage/test",
		bytes.NewReader(testBody),
	)
	testContext.Request.Header.Set("Content-Type", "application/json")
	handler.TestDesktopStorageConnection(testContext)
	require.Equal(t, http.StatusOK, testRecorder.Code)
	require.Contains(t, testRecorder.Body.String(), `"ok":true`)
	require.Contains(t, probe.key, "releases/_probes/")
}
