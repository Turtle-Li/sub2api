package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
