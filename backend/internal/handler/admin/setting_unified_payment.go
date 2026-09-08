package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

var bindingIdempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,80}$`)

const unifiedPaymentBindingMutationScope = "admin.settings.unified_payment_binding.mutate"

type unifiedPaymentBindingMutationPayload struct {
	Operation         string `json:"operation"`
	BaseURL           string `json:"base_url,omitempty"`
	BindingCodeSHA256 string `json:"binding_code_sha256,omitempty"`
	ExpectedRevision  uint64 `json:"expected_revision"`
}

func bindingCodeSHA256(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// executeUnifiedPaymentBindingMutation deliberately refuses to fall back to a
// process-local execution path. A successful mutation must have a durable,
// shared idempotency record before it can change the persisted binding.
func executeUnifiedPaymentBindingMutation(
	c *gin.Context,
	payload unifiedPaymentBindingMutationPayload,
	execute func(context.Context) (any, error),
) {
	coordinator := service.DefaultIdempotencyCoordinator()
	if coordinator == nil {
		response.Error(c, http.StatusServiceUnavailable, "Unified payment binding idempotency store is unavailable")
		return
	}
	result, err := coordinator.Execute(c.Request.Context(), service.IdempotencyExecuteOptions{
		Scope: unifiedPaymentBindingMutationScope, ActorScope: adminActorScope(c),
		Method: c.Request.Method, Route: c.FullPath(),
		IdempotencyKey: c.GetHeader("Idempotency-Key"), Payload: payload,
		TTL: service.DefaultWriteIdempotencyTTL(), RequireKey: true, RequireFencedFinalizer: true,
	}, execute)
	if err != nil {
		if retryAfter := service.RetryAfterSecondsFromError(err); retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		response.ErrorFrom(c, err)
		return
	}
	if result.Replayed {
		c.Header("X-Idempotency-Replayed", "true")
	}
	response.Success(c, result.Data)
}

func (h *SettingHandler) GetUnifiedPaymentBinding(c *gin.Context) {
	status, err := h.settingService.GetUnifiedPaymentBindingStatus(c.Request.Context())
	if err != nil {
		response.Error(c, 503, "Unable to read unified payment binding")
		return
	}
	response.Success(c, status)
}
func (h *SettingHandler) BindUnifiedPayment(c *gin.Context) {
	// Always enforce MFA, even if the general optional step-up switch is off.
	if !middleware.EnforceStepUpAlways(c, h.totpService, h.userService) {
		return
	}
	idem := c.GetHeader("Idempotency-Key")
	if len(c.Request.Header.Values("Idempotency-Key")) != 1 || !bindingIdempotencyPattern.MatchString(idem) {
		response.BadRequest(c, "Invalid binding request ID")
		return
	}
	var request struct {
		BaseURL     string  `json:"base_url"`
		BindingCode string  `json:"binding_code"`
		Revision    *uint64 `json:"revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil {
		response.BadRequest(c, "Invalid binding request")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		response.BadRequest(c, "Invalid binding request")
		return
	}
	if request.Revision == nil {
		response.BadRequest(c, "Binding revision is required")
		return
	}
	executeUnifiedPaymentBindingMutation(c, unifiedPaymentBindingMutationPayload{
		Operation:         "bind",
		BaseURL:           request.BaseURL,
		BindingCodeSHA256: bindingCodeSHA256(request.BindingCode),
		ExpectedRevision:  *request.Revision,
	}, func(ctx context.Context) (any, error) {
		return h.settingService.BindUnifiedPayment(ctx, request.BaseURL, request.BindingCode, idem, *request.Revision)
	})
}
func (h *SettingHandler) UseManualUnifiedPayment(c *gin.Context) {
	if len(c.Request.Header.Values("Idempotency-Key")) != 1 || !bindingIdempotencyPattern.MatchString(c.GetHeader("Idempotency-Key")) {
		response.BadRequest(c, "Invalid binding request ID")
		return
	}
	if !middleware.EnforceStepUpAlways(c, h.totpService, h.userService) {
		return
	}
	var request struct {
		Revision *uint64 `json:"revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil {
		response.BadRequest(c, "Invalid binding request")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || request.Revision == nil {
		response.BadRequest(c, "Invalid binding request")
		return
	}
	executeUnifiedPaymentBindingMutation(c, unifiedPaymentBindingMutationPayload{Operation: "manual", ExpectedRevision: *request.Revision}, func(ctx context.Context) (any, error) {
		return h.settingService.UseManualUnifiedPayment(ctx, *request.Revision)
	})
}
