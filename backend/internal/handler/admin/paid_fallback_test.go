package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type paidFallbackAdminSettings struct {
	service.SettingRepository
	value  string
	writes int
}

func (s *paidFallbackAdminSettings) GetValue(context.Context, string) (string, error) {
	return s.value, nil
}
func (s *paidFallbackAdminSettings) Set(_ context.Context, _ string, value string) error {
	s.value = value
	s.writes++
	return nil
}

func TestPaidFallbackAdminSettings(t *testing.T) {
	repo := &paidFallbackAdminSettings{}
	h := &SettingHandler{settingService: service.NewSettingService(repo, nil)}
	router := gin.New()
	router.GET("/settings", h.GetPaidFallbackSettings)
	router.PUT("/settings", h.UpdatePaidFallbackSettings)
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"enabled":true}`, http.StatusBadRequest},
		{`{"enabled":true,"group_ids":[7],"api_key_ids":[11]}`, http.StatusOK},
		{`{"enabled":false}`, http.StatusOK},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
	}
	require.Equal(t, 2, repo.writes)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/settings", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"enabled":false`)
}

type paidFallbackAccountAdmin struct {
	service.AdminService
	account *service.Account
	update  map[string]any
}

func (s *paidFallbackAccountAdmin) GetAccount(context.Context, int64) (*service.Account, error) {
	return s.account, nil
}
func (s *paidFallbackAccountAdmin) UpdateAccountExtra(_ context.Context, _ int64, extra map[string]any) error {
	s.update = extra
	return nil
}

func TestPaidFallbackAdminAccountMarker(t *testing.T) {
	admin := &paidFallbackAccountAdmin{account: &service.Account{ID: 23, Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey}}
	h := &AccountHandler{adminService: admin}
	router := gin.New()
	router.PUT("/accounts/:id/paid-fallback", h.SetPaidFallback)
	for _, tc := range []struct {
		body     string
		platform string
		status   int
	}{
		{`{}`, service.PlatformDeepseek, http.StatusBadRequest},
		{`{"enabled":true}`, service.PlatformDeepseek, http.StatusOK},
		{`{"enabled":true}`, service.PlatformGemini, http.StatusBadRequest},
	} {
		admin.account.Platform = tc.platform
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/accounts/23/paid-fallback", strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
	}
	require.Equal(t, map[string]any{service.PaidFallbackExtraKey: true}, admin.update)
}
