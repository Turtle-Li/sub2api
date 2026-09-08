package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

var ownerTestIdempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,80}$`)

type adminOwnerTestOrderRequest struct {
	AmountFen   int64  `json:"amount_fen"`
	PaymentType string `json:"payment_type"`
}

// CreateOwnerTestOrder creates the narrowly-scoped 1–2 fen live checkout used
// by a two-factor verified financial administrator. The service derives the
// beneficiary from the authenticated session and keeps the ordinary customer
// purchase setting disabled.
// POST /api/v1/admin/payment/owner-test/orders
func (h *PaymentHandler) CreateOwnerTestOrder(c *gin.Context) {
	// This is deliberately unconditional: a global optional-step-up setting
	// cannot make a live money mutation available to an API key or a session
	// without a recent human second factor.
	if !middleware.EnforceStepUpAlways(c, h.totpService, h.userService) {
		return
	}

	key := c.GetHeader("Idempotency-Key")
	if len(c.Request.Header.Values("Idempotency-Key")) != 1 || !ownerTestIdempotencyKeyPattern.MatchString(key) {
		response.BadRequest(c, "Invalid Idempotency-Key")
		return
	}

	request, err := decodeAdminOwnerTestOrderRequest(c)
	if err != nil {
		response.BadRequest(c, "Invalid owner test order request")
		return
	}

	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authorization required")
		return
	}
	if h == nil || h.paymentService == nil {
		response.InternalError(c, "Owner test payment service unavailable")
		return
	}

	result, err := h.paymentService.CreateOwnerTestOrder(c.Request.Context(), service.OwnerTestOrderRequest{
		AdminUserID:    subject.UserID,
		AmountFen:      request.AmountFen,
		PaymentType:    request.PaymentType,
		IdempotencyKey: key,
		ClientIP:       c.ClientIP(),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// decodeAdminOwnerTestOrderRequest is deliberately limited to this two-field
// body. json.Decoder.DisallowUnknownFields alone accepts duplicate object
// members, so consume the object tokens and reject each duplicate explicitly.
func decodeAdminOwnerTestOrderRequest(c *gin.Context) (adminOwnerTestOrderRequest, error) {
	var request adminOwnerTestOrderRequest
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1024))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return request, io.ErrUnexpectedEOF
	}
	seen := map[string]struct{}{}
	for decoder.More() {
		nameToken, tokenErr := decoder.Token()
		name, ok := nameToken.(string)
		if tokenErr != nil || !ok {
			return request, io.ErrUnexpectedEOF
		}
		if _, duplicate := seen[name]; duplicate {
			return request, io.ErrUnexpectedEOF
		}
		seen[name] = struct{}{}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return request, io.ErrUnexpectedEOF
		}
		switch name {
		case "amount_fen":
			if err := json.Unmarshal(raw, &request.AmountFen); err != nil {
				return request, err
			}
		case "payment_type":
			if err := json.Unmarshal(raw, &request.PaymentType); err != nil {
				return request, err
			}
		default:
			return request, io.ErrUnexpectedEOF
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return request, io.ErrUnexpectedEOF
	}
	if _, err := decoder.Token(); err != io.EOF {
		return request, io.ErrUnexpectedEOF
	}
	if _, ok := seen["amount_fen"]; !ok {
		return request, io.ErrUnexpectedEOF
	}
	if _, ok := seen["payment_type"]; !ok {
		return request, io.ErrUnexpectedEOF
	}
	return request, nil
}
