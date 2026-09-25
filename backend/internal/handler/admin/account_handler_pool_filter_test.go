package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountHandlerListPoolFilter(t *testing.T) {
	router, adminSvc := setupAccountListRouter()
	poolID := int64(5)
	adminSvc.accounts = []service.Account{{ID: 1, Name: "pooled", Platform: service.PlatformGrok, PoolID: &poolID, Status: service.StatusActive}}

	cases := map[string]int64{"": 0, "&pool=none": service.AccountListPoolNone, "&pool=5": 5}
	for query, want := range cases {
		adminSvc.lastListAccounts.poolID = 999
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20"+query, nil))
		require.Equal(t, http.StatusOK, rec.Code, query)
		if want == 0 {
			require.Equal(t, int64(999), adminSvc.lastListAccounts.poolID, "no pool filter keeps the legacy ListAccounts path")
		} else {
			require.Equal(t, want, adminSvc.lastListAccounts.poolID, query)
		}
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20&pool=5", nil))
	var payload struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, float64(5), payload.Data.Items[0]["pool_id"])

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?pool=abc", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAccountHandlerUpstreamBillingRatesPoolFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adminSvc := newStubAdminService()
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.GET("/api/v1/admin/accounts/upstream-billing-rates", handler.GetUpstreamBillingRates)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/upstream-billing-rates?pool=none", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, service.AccountListPoolNone, adminSvc.lastListAccounts.poolID, "rates page must match the account list page")

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/upstream-billing-rates?pool=x", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
