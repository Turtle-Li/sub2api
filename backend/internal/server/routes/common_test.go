package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type fakeInternalHealth struct {
	authorized        bool
	live              bool
	ready             bool
	liveCalls         int
	readyCalls        int
	rollbackReadiness service.PaymentRefundRollbackReadiness
	rollbackErr       error
	rollbackCalls     int
	degraded          []service.DegradedAccount
	degradedErr       error
	degradedCalls     int
	degradedWindow    time.Duration
}

func (health *fakeInternalHealth) Authorized(token string) bool {
	return health.authorized && token == "monitor-token"
}

func (health *fakeInternalHealth) Live() bool {
	health.liveCalls++
	return health.live
}

func (health *fakeInternalHealth) Ready(context.Context) bool {
	health.readyCalls++
	return health.ready
}

func (health *fakeInternalHealth) RefundRollbackReadiness(context.Context) (service.PaymentRefundRollbackReadiness, error) {
	health.rollbackCalls++
	return health.rollbackReadiness, health.rollbackErr
}

func (health *fakeInternalHealth) DegradedAccounts(_ context.Context, window time.Duration) ([]service.DegradedAccount, error) {
	health.degradedCalls++
	health.degradedWindow = window
	return health.degraded, health.degradedErr
}

func TestInternalHealthRoutesRequireMonitorTokenBeforeProbing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	health := &fakeInternalHealth{authorized: true, live: true, ready: true}
	router := gin.New()
	RegisterCommonRoutes(router, health)

	for _, path := range []string{"/internal/livez", "/internal/readyz", "/internal/refund-rollback-readiness", "/internal/degraded-accounts"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusUnauthorized, response.Code)
		require.NotContains(t, response.Body.String(), "monitor-token")
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}
	require.Zero(t, health.liveCalls)
	require.Zero(t, health.readyCalls)
	require.Zero(t, health.rollbackCalls)
	require.Zero(t, health.degradedCalls, "the token check must fail closed before any database work")
}

func TestPaymentRefundRollbackReadinessRouteFailsClosedWithSafeCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	health := &fakeInternalHealth{
		authorized: true,
		rollbackReadiness: service.PaymentRefundRollbackReadiness{
			EntitlementReservedReviewedPendingCount: 2,
		},
	}
	router := gin.New()
	RegisterCommonRoutes(router, health)

	request := httptest.NewRequest(http.MethodGet, "/internal/refund-rollback-readiness", nil)
	request.Header.Set("X-Monitor-Token", "monitor-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.JSONEq(t, `{"ready":false,"entitlement_reserved_reviewed_pending_count":2}`, response.Body.String())
	require.Equal(t, 1, health.rollbackCalls)

	health.rollbackReadiness = service.PaymentRefundRollbackReadiness{Ready: true}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"ready":true,"entitlement_reserved_reviewed_pending_count":0}`, response.Body.String())

	health.rollbackReadiness = service.PaymentRefundRollbackReadiness{Ready: true, UnsettledResetCardPurchaseCount: 1}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.JSONEq(t, `{"ready":false,"entitlement_reserved_reviewed_pending_count":0,"unsettled_reset_card_purchase_count":1}`, response.Body.String())

	health.rollbackErr = errors.New("database unavailable")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.NotContains(t, response.Body.String(), "database unavailable")
	require.JSONEq(t, `{"ready":false,"entitlement_reserved_reviewed_pending_count":-1}`, response.Body.String())
}

func TestInternalHealthRoutesReturnDistinctContracts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	health := &fakeInternalHealth{authorized: true, live: true, ready: false}
	router := gin.New()
	RegisterCommonRoutes(router, health)

	liveRequest := httptest.NewRequest(http.MethodGet, "/internal/livez", nil)
	liveRequest.Header.Set("X-Monitor-Token", "monitor-token")
	liveResponse := httptest.NewRecorder()
	router.ServeHTTP(liveResponse, liveRequest)
	require.Equal(t, http.StatusOK, liveResponse.Code)
	require.JSONEq(t, `{"live":true}`, liveResponse.Body.String())

	readyRequest := httptest.NewRequest(http.MethodGet, "/internal/readyz", nil)
	readyRequest.Header.Set("X-Monitor-Token", "monitor-token")
	readyResponse := httptest.NewRecorder()
	router.ServeHTTP(readyResponse, readyRequest)
	require.Equal(t, http.StatusServiceUnavailable, readyResponse.Code)
	require.JSONEq(t, `{"ready":false}`, readyResponse.Body.String())
}

func TestPublicHealthRemainsBackwardCompatible(t *testing.T) {
	router := gin.New()
	RegisterCommonRoutes(router, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"status":"ok"}`, response.Body.String())
}

func (health *fakeInternalHealth) CompareAndSetReviewedRefunds(context.Context, string, bool) (bool, error) {
	health.rollbackCalls++
	return health.ready, health.rollbackErr
}

func TestReviewedRefundRolloutRoute(t *testing.T) {
	for _, tc := range []struct {
		name, body, token string
		ready             bool
		err               error
		code, calls       int
	}{
		{name: "unauthorized", body: `{"expected":"","enabled":true}`, code: 401},
		{name: "missing expected", body: `{"enabled":true}`, token: "monitor-token", code: 400},
		{name: "missing enabled", body: `{"expected":""}`, token: "monitor-token", code: 400},
		{name: "arbitrary setting", body: `{"expected":"","enabled":true,"key":"anything"}`, token: "monitor-token", code: 400},
		{name: "invalid transition", body: `{"expected":"true","enabled":true}`, token: "monitor-token", code: 400},
		{name: "trailing json", body: `{"expected":"","enabled":true}{}`, token: "monitor-token", code: 400},
		{name: "enable", body: `{"expected":"","enabled":true}`, token: "monitor-token", ready: true, code: 200, calls: 1},
		{name: "disable", body: `{"expected":"true","enabled":false}`, token: "monitor-token", ready: true, code: 200, calls: 1},
		{name: "stale", body: `{"expected":"false","enabled":true}`, token: "monitor-token", code: 409, calls: 1},
		{name: "unsafe", body: `{"expected":"false","enabled":true}`, token: "monitor-token", err: errors.New("secret db detail"), code: 503, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			health := &fakeInternalHealth{authorized: true, ready: tc.ready, rollbackErr: tc.err}
			router := gin.New()
			RegisterCommonRoutes(router, health)
			request := httptest.NewRequest(http.MethodPost, "/internal/reviewed-refunds-rollout", strings.NewReader(tc.body))
			request.Header.Set("X-Monitor-Token", tc.token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, tc.code, response.Code)
			require.Equal(t, tc.calls, health.rollbackCalls)
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			require.NotContains(t, response.Body.String(), "secret db detail")
		})
	}
}

func TestDegradedAccountsRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sample := service.DegradedAccount{
		AccountID:      11,
		AccountName:    "noah",
		RequestedModel: "gpt-6-astra",
		SentModel:      "gpt-6-astra",
		ResponseModel:  "gpt-5.6-luna",
		Count:          14,
		FirstSeen:      time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC),
		LastSeen:       time.Date(2026, 9, 18, 1, 20, 0, 0, time.UTC),
	}

	t.Run("invalid window is rejected before the query", func(t *testing.T) {
		health := &fakeInternalHealth{authorized: true}
		router := gin.New()
		RegisterCommonRoutes(router, health)
		request := httptest.NewRequest(http.MethodGet, "/internal/degraded-accounts?window=abc", nil)
		request.Header.Set("X-Monitor-Token", "monitor-token")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Zero(t, health.degradedCalls)
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	})

	t.Run("query failure does not leak detail", func(t *testing.T) {
		health := &fakeInternalHealth{authorized: true, degradedErr: errors.New("pq: relation usage_logs does not exist")}
		router := gin.New()
		RegisterCommonRoutes(router, health)
		request := httptest.NewRequest(http.MethodGet, "/internal/degraded-accounts", nil)
		request.Header.Set("X-Monitor-Token", "monitor-token")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		require.NotContains(t, response.Body.String(), "usage_logs")
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	})

	t.Run("no result is an empty array, not null", func(t *testing.T) {
		health := &fakeInternalHealth{authorized: true, degraded: []service.DegradedAccount{}}
		router := gin.New()
		RegisterCommonRoutes(router, health)
		request := httptest.NewRequest(http.MethodGet, "/internal/degraded-accounts", nil)
		request.Header.Set("X-Monitor-Token", "monitor-token")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
		require.JSONEq(t, `[]`, response.Body.String())
		require.Equal(t, service.DegradedAccountsDefaultWindow, health.degradedWindow)
	})

	t.Run("success carries the full contract shape", func(t *testing.T) {
		health := &fakeInternalHealth{authorized: true, degraded: []service.DegradedAccount{sample}}
		router := gin.New()
		RegisterCommonRoutes(router, health)
		request := httptest.NewRequest(http.MethodGet, "/internal/degraded-accounts?window=1h", nil)
		request.Header.Set("X-Monitor-Token", "monitor-token")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		require.Equal(t, time.Hour, health.degradedWindow)
		// The Python probe daemon decodes this verbatim; pin every key.
		require.JSONEq(t, `[{
			"account_id": 11,
			"account_name": "noah",
			"requested_model": "gpt-6-astra",
			"sent_model": "gpt-6-astra",
			"response_model": "gpt-5.6-luna",
			"count": 14,
			"first_seen": "2026-09-18T01:00:00Z",
			"last_seen": "2026-09-18T01:20:00Z",
			"ttft_avg_ms": null
		}]`, response.Body.String())
	})
}
