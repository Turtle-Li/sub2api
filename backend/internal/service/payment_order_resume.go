package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const paymentCancellationRequestedAction = "PAYMENT_CANCELLATION_REQUESTED"

// The audit records local cancellation intent, not upstream acceptance, closure
// or a payment fact. The projection is scoped to owned IDs and pending status.
func (s *PaymentService) recordPaymentCancellationPending(ctx context.Context, orderID int64, operator string) error {
	ids, err := s.paymentOrderCancellationPendingIDs(ctx, []int64{orderID})
	if err != nil {
		return err
	}
	if ids[orderID] {
		return nil
	}
	_, err = s.entClient.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(orderID, 10)).
		SetAction(paymentCancellationRequestedAction).SetOperator(operator).SetDetail(`{"local_cancellation_requested":true}`).Save(ctx)
	if err != nil {
		return fmt.Errorf("record payment cancellation request: %w", err)
	}
	return nil
}

func (s *PaymentService) paymentOrderCancellationPendingIDs(ctx context.Context, orderIDs []int64) (map[int64]bool, error) {
	result := make(map[int64]bool)
	if len(orderIDs) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(orderIDs))
	for _, id := range orderIDs {
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	rows, err := s.entClient.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDIn(ids...), paymentauditlog.ActionEQ(paymentCancellationRequestedAction)).Select(paymentauditlog.FieldOrderID).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		id, err := strconv.ParseInt(row.OrderID, 10, 64)
		if err == nil {
			result[id] = true
		}
	}
	return result, nil
}

// ResumeOrder retrieves persisted launch data for exactly one authenticated
// user's order. It never calls CreatePayment, extends a deadline, or changes
// price/plan/coupon. Query uncertainty is not permission to expose checkout.
func (s *PaymentService) ResumeOrder(ctx context.Context, orderID, userID int64) (*CreateOrderResponse, error) {
	order, err := s.GetOrder(ctx, orderID, userID)
	if err != nil {
		return nil, err
	}
	if order.Status != OrderStatusPending {
		return nil, infraerrors.Conflict("INVALID_STATUS", "order is no longer awaiting payment")
	}
	pending, err := s.paymentOrderCancellationPendingIDs(ctx, []int64{order.ID})
	if err != nil {
		return nil, err
	}
	if pending[order.ID] {
		return nil, infraerrors.Conflict("PAYMENT_CANCELLATION_PENDING", "payment cancellation is being confirmed")
	}
	if !order.ExpiresAt.After(time.Now()) {
		_, err = s.cancelCore(ctx, order, OrderStatusExpired, "system", "order expired before checkout resume")
		if err != nil {
			return nil, err
		}
		return nil, infraerrors.Conflict("ORDER_EXPIRED", "order checkout has expired")
	}
	// This endpoint supports the persisted QR/redirect methods. Ephemeral card
	// secrets are deliberately not regenerated from a historical order.
	if base := payment.GetBasePaymentType(order.PaymentType); base != payment.TypeAlipay && base != payment.TypeWxpay {
		return nil, infraerrors.Conflict("CHECKOUT_UNAVAILABLE", "this payment method cannot resume its checkout")
	}
	switch s.checkPaidWithOptions(ctx, order, checkPaidOptions{requireConfirmed: true}) {
	case checkPaidResultAlreadyPaid, checkPaidResultCancelled:
		return nil, infraerrors.Conflict("INVALID_STATUS", "order is no longer awaiting payment")
	case checkPaidResultUnconfirmed:
		return nil, infraerrors.ServiceUnavailable("PAYMENT_CONFIRMATION_PENDING", "payment state is being confirmed")
	}
	// Reload after the external read, and re-check cancellation acceptance and
	// the deadline to fence a concurrent callback/cancel before returning data.
	order, err = s.GetOrder(ctx, orderID, userID)
	if err != nil {
		return nil, err
	}
	pending, err = s.paymentOrderCancellationPendingIDs(ctx, []int64{order.ID})
	if err != nil {
		return nil, err
	}
	if pending[order.ID] && order.Status == OrderStatusPending {
		return nil, infraerrors.Conflict("PAYMENT_CANCELLATION_PENDING", "payment cancellation is being confirmed")
	}
	if order.Status != OrderStatusPending {
		return nil, infraerrors.Conflict("INVALID_STATUS", "order is no longer awaiting payment")
	}
	if !order.ExpiresAt.After(time.Now()) {
		return nil, infraerrors.Conflict("ORDER_EXPIRED", "order checkout has expired")
	}
	result := buildResetCardOrderResponse(order)
	if strings.TrimSpace(result.PayURL) == "" && strings.TrimSpace(result.QRCode) == "" && result.JSAPI == nil {
		return nil, infraerrors.Conflict("CHECKOUT_UNAVAILABLE", "original checkout is unavailable")
	}
	return result, nil
}
