package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func TestInternalHealthRoutesRequireMonitorTokenBeforeProbing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	health := &fakeInternalHealth{authorized: true, live: true, ready: true}
	router := gin.New()
	RegisterCommonRoutes(router, health)

	for _, path := range []string{"/internal/livez", "/internal/readyz", "/internal/refund-rollback-readiness"} {
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
