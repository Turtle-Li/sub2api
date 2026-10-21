package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type modelTraceHTTPSettings struct {
	service.SettingRepository
	mu    sync.Mutex
	value string
}

func (s *modelTraceHTTPSettings) GetValue(context.Context, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value, nil
}

func (s *modelTraceHTTPSettings) Set(_ context.Context, _ string, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = value
	return nil
}

func TestModelTracePolicyHTTPConfiguration(t *testing.T) {
	svc := service.NewModelTraceService(nil, &modelTraceHTTPSettings{}, nil, nil)
	t.Cleanup(svc.Stop)
	handler := NewModelTraceHandler(svc)
	router := gin.New()
	router.PUT("/global-config", handler.UpdateGlobalConfig)
	router.GET("/global-config", handler.GetGlobalConfig)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	request, err := http.NewRequest(http.MethodPut, server.URL+"/global-config", strings.NewReader(`{
		"notify_on_mismatch":true,"pause_on_mismatch":true,"probe_count":3,
		"notification_cooldown_minutes":30,"allowed_detections":{"gpt-6.1-sol":["gpt-6-astra"],"custom-sol":["gpt-6-sol","gpt-6-astra"]}
	}`))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.NoError(t, response.Body.Close())
	response, err = server.Client().Get(server.URL + "/global-config")
	require.NoError(t, err)
	responseBody := response.Body
	defer func() { require.NoError(t, responseBody.Close()) }()
	var cfg service.ModelTraceGlobalConfig
	require.NoError(t, json.NewDecoder(response.Body).Decode(&cfg))
	require.True(t, cfg.NotifyOnMismatch)
	require.True(t, cfg.PauseOnMismatch)
	require.Equal(t, 30, cfg.NotificationCooldownMinutes)
	require.Equal(t, []string{"gpt-6-astra"}, cfg.AllowedDetections["gpt-6.1-sol"])
	require.Equal(t, []string{"gpt-6-sol", "gpt-6-astra"}, cfg.AllowedDetections["custom-sol"])
	request, err = http.NewRequest(http.MethodPut, server.URL+"/global-config", strings.NewReader(`{"notification_cooldown_minutes":-1}`))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err = server.Client().Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, response.StatusCode)
	require.NoError(t, response.Body.Close())
	require.True(t, svc.GetGlobalConfig(context.Background()).PauseOnMismatch)
}
