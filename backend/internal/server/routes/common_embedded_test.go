//go:build embed

package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/web"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type internalRoutePublicSettings struct{}

func (internalRoutePublicSettings) GetPublicSettingsForInjection(context.Context) (any, error) {
	return map[string]string{"site_name": "internal route test"}, nil
}

func TestRefundInternalRoutesWithEmbeddedFrontend(t *testing.T) {
	frontend, err := web.NewFrontendServer(internalRoutePublicSettings{})
	require.NoError(t, err)
	for _, mode := range []struct {
		name       string
		middleware gin.HandlerFunc
	}{{"settings", frontend.Middleware()}, {"legacy", web.ServeEmbeddedFrontend()}} {
		t.Run(mode.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, method, path, body, token, wantJSON string
				ready                                     bool
				pending                                   int64
				status, calls                             int
			}{
				{name: "readiness unauthorized", method: http.MethodGet, path: "/internal/refund-rollback-readiness", status: 401, wantJSON: `{"ready":false}`},
				{name: "readiness pending", method: http.MethodGet, path: "/internal/refund-rollback-readiness", token: "monitor-token", ready: true, pending: 1, status: 503, calls: 1, wantJSON: `{"ready":false,"entitlement_reserved_reviewed_pending_count":1}`},
				{name: "readiness zero", method: http.MethodGet, path: "/internal/refund-rollback-readiness", token: "monitor-token", ready: true, status: 200, calls: 1, wantJSON: `{"ready":true,"entitlement_reserved_reviewed_pending_count":0}`},
				{name: "rollout unauthorized", method: http.MethodPost, path: "/internal/reviewed-refunds-rollout", body: `{"expected":"","enabled":true}`, status: 401, wantJSON: `{"changed":false}`},
				{name: "rollout conflict", method: http.MethodPost, path: "/internal/reviewed-refunds-rollout", body: `{"expected":"","enabled":true}`, token: "monitor-token", status: 409, calls: 1, wantJSON: `{"changed":false}`},
				{name: "rollout success", method: http.MethodPost, path: "/internal/reviewed-refunds-rollout", body: `{"expected":"","enabled":true}`, token: "monitor-token", ready: true, status: 200, calls: 1, wantJSON: `{"changed":true,"enabled":true}`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					health := &fakeInternalHealth{authorized: true, ready: tc.ready, rollbackReadiness: service.PaymentRefundRollbackReadiness{Ready: tc.ready, EntitlementReservedReviewedPendingCount: tc.pending}}
					router := gin.New()
					router.Use(mode.middleware)
					RegisterCommonRoutes(router, health)
					request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
					request.Header.Set("X-Monitor-Token", tc.token)
					request.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					require.Equal(t, tc.status, response.Code)
					require.Equal(t, "application/json; charset=utf-8", response.Header().Get("Content-Type"))
					require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
					require.JSONEq(t, tc.wantJSON, response.Body.String())
					require.Equal(t, tc.calls, health.rollbackCalls)
				})
			}
		})
	}
}
