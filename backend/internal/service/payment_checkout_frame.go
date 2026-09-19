package service

import (
	"context"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
)

const paymentOrderCheckoutFrameURLSnapshotKey = "checkout_frame_url"

func paymentOrderCheckoutFrameURLFromProviderResponse(selection *payment.InstanceSelection, paymentType string, response *payment.CreatePaymentResponse) string {
	if selection == nil || response == nil || strings.TrimSpace(selection.ProviderKey) != payment.TypeUnifiedPay ||
		payment.GetBasePaymentType(paymentType) != payment.TypeAlipay {
		return ""
	}
	return unifiedpay.AlipayEmbeddedCheckoutFrameURL(response.CheckoutFrameURL)
}

// paymentOrderCheckoutFrameURLFromSnapshot revalidates persisted display data
// before it reaches an authenticated caller. Older rows simply have no value.
func paymentOrderCheckoutFrameURLFromSnapshot(order *dbent.PaymentOrder) string {
	if order == nil || order.Status != OrderStatusPending || !order.ExpiresAt.After(time.Now()) ||
		!paymentOrderUsesUnifiedPay(order) || payment.GetBasePaymentType(order.PaymentType) != payment.TypeAlipay {
		return ""
	}
	return unifiedpay.AlipayEmbeddedCheckoutFrameURL(psSnapshotStringValue(order.ProviderSnapshot[paymentOrderCheckoutFrameURLSnapshotKey]))
}

// hydrateCheckoutFrameURL fills a missing historical frame from the signed,
// product-scoped central API. The read is deliberately best-effort: the
// persisted PayURL and QRCode remain the existing fallback, and no order is
// created or mutated during recovery.
func (s *PaymentService) hydrateCheckoutFrameURL(ctx context.Context, order *dbent.PaymentOrder, response *CreateOrderResponse) {
	if response == nil || response.CheckoutFrameURL != "" {
		return
	}
	if frameURL := paymentOrderCheckoutFrameURLFromSnapshot(order); frameURL != "" {
		response.CheckoutFrameURL = frameURL
		return
	}
	if order == nil || s == nil || s.unifiedPayment == nil || !s.unifiedPayment.Enabled() ||
		order.Status != OrderStatusPending || !order.ExpiresAt.After(time.Now()) ||
		!paymentOrderUsesUnifiedPay(order) || payment.GetBasePaymentType(order.PaymentType) != payment.TypeAlipay {
		return
	}
	paymentOrderID := paymentOrderQueryReference(order, s.unifiedPayment)
	if paymentOrderID == "" {
		return
	}
	frameURL, err := s.unifiedPayment.RecoverAlipayCheckoutFrameURL(ctx, paymentOrderID, order.OutTradeNo)
	if err == nil {
		response.CheckoutFrameURL = frameURL
	}
}
