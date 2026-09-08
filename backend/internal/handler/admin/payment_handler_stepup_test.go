//go:build unit

package admin

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

type refundStepUpUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *refundStepUpUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return r.user, nil
}

func (*refundStepUpUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

type refundStepUpCache struct {
	service.TotpCache
	granted    bool
	checks     int
	sessionKey string
}

func (c *refundStepUpCache) HasStepUpGrant(_ context.Context, _ int64, sessionKey string) (bool, error) {
	c.checks++
	c.sessionKey = sessionKey
	return c.granted, nil
}

func newRefundStepUpHandler(paymentService *service.PaymentService, granted bool) (*PaymentHandler, *refundStepUpCache) {
	cache := &refundStepUpCache{granted: granted}
	totpService := service.NewTotpService(nil, nil, cache, nil, nil, nil)
	userService := service.NewUserService(&refundStepUpUserRepo{user: &service.User{
		ID:           71,
		Email:        "refund-step-up@example.test",
		PasswordHash: "test-only-password-hash",
		TotpEnabled:  true,
	}}, nil, nil, nil)
	return NewPaymentHandler(paymentService, nil, totpService, userService), cache
}

func newRefundStepUpRouter(handler *PaymentHandler, subject bool, apiKey bool) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if apiKey {
			c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
		}
		if subject {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 71})
			c.Set(middleware.ContextKeySessionID, "refund-step-up-session")
		}
		c.Next()
	})
	router.POST("/api/v1/admin/payment/orders/:id/refund", handler.ProcessRefund)
	router.POST("/api/v1/admin/payment/orders/:id/refund/query", handler.QueryAndFinalizeRefund)
	return router
}

func refundStepUpRequest(path, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestAdminRefundMutationsRequireStepUpBeforeCallingPaymentService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoints := []struct {
		name string
		path string
		body string
	}{
		{name: "process", path: "/api/v1/admin/payment/orders/1/refund", body: `{"amount":1,"reason":"test"}`},
		{name: "query", path: "/api/v1/admin/payment/orders/1/refund/query", body: ""},
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint.name+"_admin_api_key", func(t *testing.T) {
			// A nil service would panic if the endpoint proceeded past the gate.
			handler := NewPaymentHandler(nil, nil, nil, nil)
			recorder := httptest.NewRecorder()
			newRefundStepUpRouter(handler, false, true).ServeHTTP(recorder, refundStepUpRequest(endpoint.path, endpoint.body))

			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.Contains(t, recorder.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
		})

		t.Run(endpoint.name+"_session_without_grant", func(t *testing.T) {
			// No SettingService is involved: EnforceStepUpAlways keeps this gate on
			// even when the optional global step-up setting is disabled.
			handler, cache := newRefundStepUpHandler(nil, false)
			recorder := httptest.NewRecorder()
			newRefundStepUpRouter(handler, true, false).ServeHTTP(recorder, refundStepUpRequest(endpoint.path, endpoint.body))

			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.Contains(t, recorder.Body.String(), "STEP_UP_REQUIRED")
			require.Equal(t, 1, cache.checks)
			require.Equal(t, "refund-step-up-session", cache.sessionKey)
		})
	}
}

func TestAdminRefundMutationsReachPaymentServiceWithStepUpGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := sql.Open("sqlite", "file:admin_refund_step_up_grant?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	driver := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(driver)))
	t.Cleanup(func() { _ = client.Close() })
	paymentService := service.NewPaymentService(client, payment.NewRegistry(), nil, nil, nil, nil, nil, nil, nil)
	handler, cache := newRefundStepUpHandler(paymentService, true)
	router := newRefundStepUpRouter(handler, true, false)

	for _, endpoint := range []struct {
		name string
		path string
		body string
	}{
		{name: "process", path: "/api/v1/admin/payment/orders/999/refund", body: `{"amount":1,"reason":"test"}`},
		{name: "query", path: "/api/v1/admin/payment/orders/999/refund/query", body: ""},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, refundStepUpRequest(endpoint.path, endpoint.body))

			// The actual service reports the missing order; a step-up response here
			// would show that a valid human-session grant was not accepted.
			require.Equal(t, http.StatusNotFound, recorder.Code)
			require.Contains(t, recorder.Body.String(), "NOT_FOUND")
		})
	}
	require.Equal(t, 2, cache.checks)
	require.Equal(t, "refund-step-up-session", cache.sessionKey)
}
