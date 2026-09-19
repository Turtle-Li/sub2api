package service

import (
	"context"
	"strconv"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
)

type missingUnifiedPaymentOrderResult uint8

const (
	missingUnifiedPaymentOrderNotHandled missingUnifiedPaymentOrderResult = iota
	missingUnifiedPaymentOrderRetry
	missingUnifiedPaymentOrderClosed
)

// reconcileMissingUnifiedPaymentOrder repairs the narrow create/persist seam
// where an older Sub2 binary left a UnifiedPay order without its central UUID.
// It never creates or closes a remote order. A local expiration is permitted
// only after a scoped central absence and every historical reset-card fence.
func (s *PaymentService) reconcileMissingUnifiedPaymentOrder(ctx context.Context, order *dbent.PaymentOrder) missingUnifiedPaymentOrderResult {
	if !missingUnifiedPaymentOrderCandidate(order) {
		return missingUnifiedPaymentOrderNotHandled
	}
	if s == nil || s.entClient == nil || s.unifiedPayment == nil || !s.unifiedPayment.Enabled() {
		return missingUnifiedPaymentOrderRetry
	}

	snapshot := psOrderProviderSnapshot(order)
	if !missingUnifiedPaymentOrderScopeMatchesGateway(snapshot, s.unifiedPayment) {
		return missingUnifiedPaymentOrderRetry
	}
	now := s.resetCardCurrentTime().UTC()
	if order.OrderType == payment.OrderTypeResetCard {
		dispatch, _, err := resetCardDispatchFromOrder(order)
		if err != nil || resetCardDispatchActive(dispatch, now) {
			return missingUnifiedPaymentOrderRetry
		}
	}

	lookup, err := s.unifiedPayment.LookupPaymentOrderByProductOrderNo(ctx, order.OutTradeNo)
	if err != nil || lookup == nil {
		return missingUnifiedPaymentOrderRetry
	}
	if lookup.Found {
		if !missingUnifiedPaymentOrderMatchesLookup(order, lookup) {
			return missingUnifiedPaymentOrderRetry
		}
		return s.bindMissingUnifiedPaymentOrder(ctx, order, lookup.PaymentOrderID)
	}
	if !missingUnifiedPaymentOrderMayExpire(order, now) {
		return missingUnifiedPaymentOrderRetry
	}

	safeExpiry := now.Add(-2 * unifiedpay.MaximumClockSkew)
	changed, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(order.ID),
		paymentorder.StatusEQ(OrderStatusPending),
		paymentorder.UpdatedAtEQ(order.UpdatedAt),
		paymentorder.ExpiresAtLTE(safeExpiry),
		paymentorder.PaidAtIsNil(),
	).SetStatus(OrderStatusExpired).Save(ctx)
	if err != nil || changed != 1 {
		return missingUnifiedPaymentOrderRetry
	}
	order.Status = OrderStatusExpired
	s.writeAuditLog(ctx, order.ID, "RESET_CARD_UNIFIED_ORDER_ABSENT_EXPIRED", payment.TypeUnifiedPay, map[string]any{
		"reason": "scoped central lookup confirmed absence after the expiry safety window",
	})
	return missingUnifiedPaymentOrderClosed
}

func missingUnifiedPaymentOrderCandidate(order *dbent.PaymentOrder) bool {
	if order == nil || order.Status != OrderStatusPending || !paymentOrderUsesUnifiedPay(order) ||
		strings.TrimSpace(order.PaymentTradeNo) != "" {
		return false
	}
	snapshot := psOrderProviderSnapshot(order)
	return snapshot != nil && snapshot.ProviderKey == payment.TypeUnifiedPay && snapshot.PaymentOrderID == ""
}

func missingUnifiedPaymentOrderScopeMatchesGateway(snapshot *paymentOrderProviderSnapshot, gateway *unifiedpay.Gateway) bool {
	if snapshot == nil || gateway == nil {
		return false
	}
	scope := gateway.ScopeMetadata()
	return snapshot.ProviderKey == payment.TypeUnifiedPay &&
		strings.EqualFold(snapshot.Currency, payment.DefaultPaymentCurrency) &&
		missingUnifiedPaymentOrderScopeValueMatches(snapshot.Environment, scope["environment"]) &&
		missingUnifiedPaymentOrderScopeValueMatches(snapshot.OrganizationID, scope["organization_id"]) &&
		missingUnifiedPaymentOrderScopeValueMatches(snapshot.ProductID, scope["product_id"]) &&
		missingUnifiedPaymentOrderScopeValueMatches(snapshot.AppID, scope["app_id"])
}

func missingUnifiedPaymentOrderScopeValueMatches(original, current string) bool {
	return strings.TrimSpace(original) != "" &&
		strings.TrimSpace(current) != "" &&
		original == current
}

func missingUnifiedPaymentOrderMatchesLookup(order *dbent.PaymentOrder, lookup *unifiedpay.PaymentOrderLookup) bool {
	if order == nil || lookup == nil || !lookup.Found || lookup.PaymentOrderID == "" ||
		lookup.ProductOrderNo != order.OutTradeNo || lookup.OrderType != order.OrderType ||
		lookup.Currency != payment.DefaultPaymentCurrency {
		return false
	}
	expectedMethod, supported := unifiedpay.PaymentMethodForPaymentType(order.PaymentType)
	if !supported || lookup.PaymentMethod != expectedMethod {
		return false
	}
	expectedAmountFen, err := payment.AmountToMinorUnit(strconv.FormatFloat(order.PayAmount, 'f', -1, 64), payment.DefaultPaymentCurrency)
	return err == nil && lookup.AmountFen == expectedAmountFen
}

func (s *PaymentService) bindMissingUnifiedPaymentOrder(ctx context.Context, order *dbent.PaymentOrder, paymentOrderID string) missingUnifiedPaymentOrderResult {
	if order == nil || strings.TrimSpace(paymentOrderID) == "" {
		return missingUnifiedPaymentOrderRetry
	}
	snapshot := clonePaymentOrderSnapshot(order.ProviderSnapshot)
	if snapshot == nil {
		return missingUnifiedPaymentOrderRetry
	}
	snapshot["payment_order_id"] = paymentOrderID
	changed, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(order.ID),
		paymentorder.StatusEQ(OrderStatusPending),
		paymentorder.PaidAtIsNil(),
		paymentorder.UpdatedAtEQ(order.UpdatedAt),
	).SetProviderSnapshot(snapshot).SetPaymentTradeNo(paymentOrderID).Save(ctx)
	if err != nil || changed != 1 {
		return missingUnifiedPaymentOrderRetry
	}
	reloaded, err := s.entClient.PaymentOrder.Get(ctx, order.ID)
	if err != nil {
		return missingUnifiedPaymentOrderRetry
	}
	*order = *reloaded
	return missingUnifiedPaymentOrderNotHandled
}

func missingUnifiedPaymentOrderMayExpire(order *dbent.PaymentOrder, now time.Time) bool {
	if order == nil || order.OrderType != payment.OrderTypeResetCard || order.Status != OrderStatusPending ||
		order.PaidAt != nil || paymentOrderHasDiscount(order) ||
		strings.TrimSpace(order.PaymentTradeNo) != "" || order.ExpiresAt.After(now.Add(-2*unifiedpay.MaximumClockSkew)) {
		return false
	}
	if strings.TrimSpace(psStringValue(order.PayURL)) != "" ||
		strings.TrimSpace(psStringValue(order.QrCode)) != "" ||
		strings.TrimSpace(psStringValue(order.QrCodeImg)) != "" {
		return false
	}
	_, checkoutPresent, _ := resetCardCheckoutFromOrder(order)
	return !checkoutPresent
}
