package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUnifiedPaymentBindingAlwaysRequiresSessionStepUp(t *testing.T) {
	for _, manual := range []bool{false, true} {
		for _, apiKey := range []bool{false, true} {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/settings/unified-payment-binding", strings.NewReader(`{"binding_code":"never-log-this-code"}`))
			if apiKey {
				c.Set("auth_method", "admin_api_key")
			}
			handler := &SettingHandler{}
			if manual {
				handler.UseManualUnifiedPayment(c)
			} else {
				handler.BindUnifiedPayment(c)
			}
			require.NotEqual(t, http.StatusOK, recorder.Code)
			require.NotContains(t, recorder.Body.String(), "never-log-this-code")
		}
	}
}

func TestUnifiedPaymentBindingMutationReplaysOldManualRetryWithoutDeletingLaterSave(t *testing.T) {
	previousCoordinator := service.DefaultIdempotencyCoordinator()
	repo := newMemoryIdempotencyRepoStub()
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(repo, service.DefaultIdempotencyConfig()))
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(previousCoordinator) })
	gin.SetMode(gin.TestMode)

	state := "initial"
	manualCalls := 0
	bindCalls := 0
	router := gin.New()
	router.DELETE("/api/v1/admin/settings/unified-payment-binding", func(c *gin.Context) {
		executeUnifiedPaymentBindingMutation(c, unifiedPaymentBindingMutationPayload{Operation: "manual"}, func(context.Context) (any, error) {
			manualCalls++
			return &bindingTestFinalizer{repo: repo, data: gin.H{"state": "manual"}, apply: func() { state = "manual" }}, nil
		})
	})
	router.POST("/api/v1/admin/settings/unified-payment-binding", func(c *gin.Context) {
		executeUnifiedPaymentBindingMutation(c, unifiedPaymentBindingMutationPayload{
			Operation:         "bind",
			BaseURL:           "https://pay.totools.cn",
			BindingCodeSHA256: bindingCodeSHA256("test-only-code"),
		}, func(context.Context) (any, error) {
			bindCalls++
			return &bindingTestFinalizer{repo: repo, data: gin.H{"state": "saved"}, apply: func() { state = "saved" }}, nil
		})
	})
	call := func(method, key string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, "/api/v1/admin/settings/unified-payment-binding", nil)
		request.Header.Set("Idempotency-Key", key)
		router.ServeHTTP(recorder, request)
		return recorder
	}

	firstManual := call(http.MethodDelete, "binding-manual-retry-0001")
	require.Equal(t, http.StatusOK, firstManual.Code)
	require.Equal(t, "manual", state)
	require.Equal(t, 1, manualCalls)

	laterSave := call(http.MethodPost, "binding-save-newer-0002")
	require.Equal(t, http.StatusOK, laterSave.Code)
	require.Equal(t, "saved", state)
	require.Equal(t, 1, bindCalls)

	oldManualRetry := call(http.MethodDelete, "binding-manual-retry-0001")
	require.Equal(t, http.StatusOK, oldManualRetry.Code)
	require.Equal(t, "true", oldManualRetry.Header().Get("X-Idempotency-Replayed"))
	require.Equal(t, "saved", state)
	require.Equal(t, 1, manualCalls)

	conflictingSave := call(http.MethodPost, "binding-manual-retry-0001")
	require.Equal(t, http.StatusConflict, conflictingSave.Code)
	require.Equal(t, "saved", state)
	require.Equal(t, 1, bindCalls)
}

func TestUnifiedPaymentBindingMutationFailsClosedWithoutSharedCoordinator(t *testing.T) {
	previousCoordinator := service.DefaultIdempotencyCoordinator()
	service.SetDefaultIdempotencyCoordinator(nil)
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(previousCoordinator) })
	gin.SetMode(gin.TestMode)

	executed := false
	router := gin.New()
	router.POST("/api/v1/admin/settings/unified-payment-binding", func(c *gin.Context) {
		executeUnifiedPaymentBindingMutation(c, unifiedPaymentBindingMutationPayload{Operation: "bind"}, func(context.Context) (any, error) {
			executed = true
			return gin.H{"ok": true}, nil
		})
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/settings/unified-payment-binding", nil)
	request.Header.Set("Idempotency-Key", "binding-no-shared-store-0003")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.False(t, executed)
}

// Actual transaction/CAS semantics are covered by repository PostgreSQL tests.
// This stub exercises handler opt-in and public replay headers.
type bindingTestFinalizer struct {
	repo  *memoryIdempotencyRepoStub
	data  any
	apply func()
}

func (f *bindingTestFinalizer) IdempotencyResponseData() any { return f.data }
func (f *bindingTestFinalizer) FinalizeIdempotencySuccess(ctx context.Context, claim service.IdempotencyExecutionClaim, status int, body string, expires time.Time) error {
	if err := f.repo.MarkSucceeded(ctx, claim.ID, status, body, expires); err != nil {
		return err
	}
	f.apply()
	return nil
}
