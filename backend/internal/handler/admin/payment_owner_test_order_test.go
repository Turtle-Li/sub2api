//go:build unit

package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const (
	ownerTestOrderPath = "/api/v1/admin/payment/owner-test/orders"
	ownerTestOrderKey  = "owner-test-handler-key-0001"
	ownerTestOrderBody = `{"amount_fen":1,"payment_type":"alipay"}`
)

func newOwnerTestOrderRouter(handler *PaymentHandler, subject bool, apiKey bool) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if apiKey {
			c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
		}
		if subject {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 71})
			c.Set(middleware.ContextKeySessionID, "owner-test-step-up-session")
		}
		c.Next()
	})
	router.POST(ownerTestOrderPath, handler.CreateOwnerTestOrder)
	return router
}

func ownerTestOrderRequest(body string, idempotencyKeys ...string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, ownerTestOrderPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, key := range idempotencyKeys {
		req.Header.Add("Idempotency-Key", key)
	}
	return req
}

func TestOwnerTestOrderRequiresUnconditionalStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("admin API key is forbidden before request validation", func(t *testing.T) {
		// The nil services would panic if an API key could bypass this gate.
		handler := NewPaymentHandler(nil, nil, nil, nil)
		recorder := httptest.NewRecorder()
		newOwnerTestOrderRouter(handler, false, true).ServeHTTP(recorder, ownerTestOrderRequest(ownerTestOrderBody, ownerTestOrderKey))

		require.Equal(t, http.StatusForbidden, recorder.Code)
		require.Contains(t, recorder.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
	})

	t.Run("session without a grant is rejected even with optional step-up globally off", func(t *testing.T) {
		// EnforceStepUpAlways receives no SettingService, so the optional global
		// switch cannot bypass this endpoint.
		handler, cache := newRefundStepUpHandler(nil, false)
		recorder := httptest.NewRecorder()
		newOwnerTestOrderRouter(handler, true, false).ServeHTTP(recorder, ownerTestOrderRequest(ownerTestOrderBody, ownerTestOrderKey))

		require.Equal(t, http.StatusForbidden, recorder.Code)
		require.Contains(t, recorder.Body.String(), "STEP_UP_REQUIRED")
		require.Equal(t, 1, cache.checks)
		require.Equal(t, "owner-test-step-up-session", cache.sessionKey)
	})
}

func TestOwnerTestOrderRejectsInvalidRequestBeforePaymentService(t *testing.T) {
	gin.SetMode(gin.TestMode)

	validGrantHandler, cache := newRefundStepUpHandler(nil, true)
	validGrantRouter := newOwnerTestOrderRouter(validGrantHandler, true, false)

	// This proves the fixture's grant and canonical request pass the handler
	// boundary. The nil service is only reached after validation, yielding 500.
	t.Run("canonical request reaches the service boundary", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		validGrantRouter.ServeHTTP(recorder, ownerTestOrderRequest(ownerTestOrderBody, ownerTestOrderKey))

		require.Equal(t, http.StatusInternalServerError, recorder.Code)
		require.Contains(t, recorder.Body.String(), "Owner test payment service unavailable")
	})

	tests := []struct {
		name string
		body string
		keys []string
	}{
		{name: "amount must be an integer fen", body: `{"amount_fen":1.0,"payment_type":"alipay"}`, keys: []string{ownerTestOrderKey}},
		{name: "rejects user ID injection", body: `{"amount_fen":1,"payment_type":"alipay","user_id":99}`, keys: []string{ownerTestOrderKey}},
		{name: "rejects source injection", body: `{"amount_fen":1,"payment_type":"alipay","source":"admin"}`, keys: []string{ownerTestOrderKey}},
		{name: "rejects URL injection", body: `{"amount_fen":1,"payment_type":"alipay","url":"https://example.test"}`, keys: []string{ownerTestOrderKey}},
		{name: "rejects duplicate amount", body: `{"amount_fen":1,"amount_fen":2,"payment_type":"alipay"}`, keys: []string{ownerTestOrderKey}},
		{name: "requires amount", body: `{"payment_type":"alipay"}`, keys: []string{ownerTestOrderKey}},
		{name: "requires payment type", body: `{"amount_fen":1}`, keys: []string{ownerTestOrderKey}},
		{name: "requires idempotency key", body: ownerTestOrderBody},
		{name: "rejects malformed idempotency key", body: ownerTestOrderBody, keys: []string{"short key"}},
		{name: "rejects multiple idempotency keys", body: ownerTestOrderBody, keys: []string{ownerTestOrderKey, "owner-test-handler-key-0002"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checksBefore := cache.checks
			recorder := httptest.NewRecorder()
			validGrantRouter.ServeHTTP(recorder, ownerTestOrderRequest(test.body, test.keys...))

			// A nil payment service returns 500 only after valid input. Every
			// rejected request must stop at the handler boundary instead.
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.NotContains(t, recorder.Body.String(), "Owner test payment service unavailable")
			require.Equal(t, checksBefore+1, cache.checks)
		})
	}
}
