package unifiedpay

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

const maximumRefundAmountFen int64 = 100_000_000

// CreateUnifiedRefund submits or safely replays one durable asynchronous
// unified-payment refund request. A 202 response is only a remote reservation;
// this method never turns an uncertain transport or validation outcome into a
// local FAILED result.
func (g *Gateway) CreateUnifiedRefund(ctx context.Context, request payment.UnifiedRefundRequest) (*payment.UnifiedRefundResource, error) {
	if !g.Enabled() {
		return nil, ErrDisabled
	}
	if g.client == nil {
		return nil, ErrInvalidConfiguration
	}
	input, err := newCreateRefundRequest(request)
	if err != nil {
		return nil, err
	}
	result, err := g.client.createRefund(ctx, request.IdempotencyKey, input)
	if err != nil {
		return nil, refundStateUnconfirmed(err)
	}
	if !matchesRefundExpectation(result, payment.UnifiedRefundExpectation{
		PaymentOrderID: request.PaymentOrderID, ProductRefundNo: request.ProductRefundNo, AmountFen: request.AmountFen,
	}) {
		return nil, refundStateUnconfirmed(ErrInvalidResponse)
	}
	return unifiedRefundResource(result), nil
}

// GetUnifiedRefund returns a remote refund resource only after binding it to
// the locally persisted refund-request id and immutable request semantics.
func (g *Gateway) GetUnifiedRefund(ctx context.Context, refundRequestID string, expected payment.UnifiedRefundExpectation) (*payment.UnifiedRefundResource, error) {
	if !g.Enabled() {
		return nil, ErrDisabled
	}
	if g.client == nil {
		return nil, ErrInvalidConfiguration
	}
	if !validUUID(refundRequestID) || !validUnifiedRefundExpectation(expected) {
		return nil, ErrInvalidRequest
	}
	result, err := g.client.getRefund(ctx, refundRequestID)
	if err != nil {
		return nil, refundStateUnconfirmed(err)
	}
	if !strings.EqualFold(result.RefundRequestID, refundRequestID) || !matchesRefundExpectation(result, expected) {
		return nil, refundStateUnconfirmed(ErrInvalidResponse)
	}
	return unifiedRefundResource(result), nil
}

func newCreateRefundRequest(request payment.UnifiedRefundRequest) (createRefundRequest, error) {
	if !validIdentifier(request.IdempotencyKey, 16, 128) {
		return createRefundRequest{}, ErrInvalidRequest
	}
	input := createRefundRequest{
		PaymentOrderID: request.PaymentOrderID, ProductRefundNo: request.ProductRefundNo,
		AmountFen: request.AmountFen, ReasonCode: request.ReasonCode,
	}
	if request.ReasonSummary != nil {
		summary := *request.ReasonSummary
		input.ReasonSummary = &summary
	}
	if !validCreateRefundRequest(input) {
		return createRefundRequest{}, ErrInvalidRequest
	}
	return input, nil
}

func validCreateRefundRequest(request createRefundRequest) bool {
	return validUUID(request.PaymentOrderID) && validIdentifier(request.ProductRefundNo, 6, 64) &&
		request.AmountFen >= 1 && request.AmountFen <= maximumRefundAmountFen &&
		validRefundReasonCode(request.ReasonCode) && validRefundReasonSummary(request.ReasonSummary)
}

func validUnifiedRefundExpectation(expected payment.UnifiedRefundExpectation) bool {
	return validUUID(expected.PaymentOrderID) && validIdentifier(expected.ProductRefundNo, 6, 64) &&
		expected.AmountFen >= 1 && expected.AmountFen <= maximumRefundAmountFen
}

func validRefundReasonCode(value string) bool {
	switch value {
	case "customer_request", "duplicate_charge", "service_not_delivered", "service_error", "other":
		return true
	default:
		return false
	}
}

func validRefundReasonSummary(value *string) bool {
	if value == nil {
		return true
	}
	return *value != "" && *value == strings.TrimSpace(*value) && utf8.ValidString(*value) &&
		utf8.RuneCountInString(*value) <= 240 && !strings.ContainsAny(*value, "\x00\r\n")
}

func validRefundResponse(result refundResponse) bool {
	if !validUUID(result.RefundRequestID) || !validUUID(result.PaymentOrderID) ||
		!validIdentifier(result.ProductRefundNo, 6, 64) || !validIdentifier(result.ChannelOutRefundNo, 9, 64) ||
		result.AmountFen < 1 || result.AmountFen > maximumRefundAmountFen || result.Currency != payment.DefaultPaymentCurrency ||
		!validPaymentMethod(result.PaymentMethod) || !validRefundStatus(result.Status) ||
		result.CreatedAt.IsZero() || result.UpdatedAt.IsZero() || !validOptionalRefundTime(result.CompletedAt) ||
		!validOptionalRefundText(result.ProviderRefundID, 160) || !validOptionalRefundText(result.ProviderStatus, 80) ||
		!validOptionalFailureCode(result.FailureCode) {
		return false
	}
	return true
}

func validRefundStatus(value string) bool {
	switch value {
	case RefundStatusApproved, RefundStatusProcessing, RefundStatusSucceeded, RefundStatusFailed, RefundStatusUnknown:
		return true
	default:
		return false
	}
}

func validOptionalRefundTime(value *time.Time) bool {
	return value == nil || !value.IsZero()
}

func validOptionalRefundText(value *string, maximum int) bool {
	return value == nil || (*value != "" && len(*value) <= maximum && utf8.ValidString(*value) &&
		*value == strings.TrimSpace(*value) && !strings.ContainsAny(*value, "\x00\r\n"))
}

func validOptionalFailureCode(value *string) bool {
	if value == nil || len(*value) == 0 || len(*value) > 120 {
		return value == nil
	}
	for i := 0; i < len(*value); i++ {
		c := (*value)[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == ':' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func matchesRefundExpectation(result *refundResponse, expected payment.UnifiedRefundExpectation) bool {
	return result != nil && strings.EqualFold(result.PaymentOrderID, expected.PaymentOrderID) &&
		result.ProductRefundNo == expected.ProductRefundNo && result.AmountFen == expected.AmountFen
}

func unifiedRefundResource(result *refundResponse) *payment.UnifiedRefundResource {
	if result == nil {
		return nil
	}
	return &payment.UnifiedRefundResource{
		RefundRequestID: result.RefundRequestID, PaymentOrderID: result.PaymentOrderID,
		ProductRefundNo: result.ProductRefundNo, ChannelOutRefundNo: result.ChannelOutRefundNo,
		AmountFen: result.AmountFen, Currency: result.Currency, PaymentMethod: result.PaymentMethod,
		Status: result.Status, ProviderRefundID: cloneRefundString(result.ProviderRefundID),
		ProviderStatus: cloneRefundString(result.ProviderStatus), FailureCode: cloneRefundString(result.FailureCode),
		NeedsManualReview: result.NeedsManualReview, CreatedAt: result.CreatedAt, UpdatedAt: result.UpdatedAt,
		CompletedAt: cloneRefundTime(result.CompletedAt),
	}
}

func cloneRefundString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneRefundTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func refundStateUnconfirmed(err error) error {
	return fmt.Errorf("%w: %v", ErrRefundStateUnconfirmed, err)
}

var _ payment.UnifiedRefundProvider = (*Gateway)(nil)
