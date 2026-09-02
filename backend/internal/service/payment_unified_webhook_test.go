//go:build unit

package service

import (
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	"github.com/stretchr/testify/require"
)

func TestValidateUnifiedPaymentEventSemantics(t *testing.T) {
	transactionID := "alipay_trade_001"
	base := unifiedpay.WebhookEvent{Resource: unifiedpay.PaymentOrderResource{
		AmountFen: 1234, PaidAmountFen: 1234, ChannelTransactionID: &transactionID,
	}}
	tests := []struct {
		name      string
		eventType string
		status    string
		refund    *unifiedpay.WebhookRefundResource
		wantError string
	}{
		{name: "paid", eventType: unifiedpay.EventPaymentPaid, status: unifiedpay.StatusPaid},
		{name: "late payment", eventType: unifiedpay.EventPaymentPaidAfterClose, status: unifiedpay.StatusPaidAfterClose},
		{name: "confirmation pending", eventType: unifiedpay.EventPaymentConfirmationPending, status: unifiedpay.StatusConfirmationPending},
		{name: "paid event status mismatch", eventType: unifiedpay.EventPaymentPaid, status: unifiedpay.StatusClosed, wantError: "invalid_paid_event"},
		{name: "late payment status mismatch", eventType: unifiedpay.EventPaymentPaidAfterClose, status: unifiedpay.StatusPaid, wantError: "invalid_paid_after_close_event"},
		{name: "refund type mismatch", eventType: unifiedpay.EventRefundSucceeded, status: unifiedpay.StatusRefunded,
			refund: &unifiedpay.WebhookRefundResource{AmountFen: 1234, Status: unifiedpay.RefundStatusFailed}, wantError: "invalid_refund_succeeded_event"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := base
			event.EventType = test.eventType
			event.Resource.Status = test.status
			event.Refund = test.refund
			err := validateUnifiedPaymentEventSemantics(event)
			if test.wantError == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, test.wantError)
			}
		})
	}
}

func TestValidateUnifiedPaymentEventOrderUsesExactFenAndScope(t *testing.T) {
	order := &dbent.PaymentOrder{
		PayAmount: 12.34, OrderType: payment.OrderTypeBalance,
		ProviderSnapshot: map[string]any{
			"schema_version": 2, "provider_key": payment.TypeUnifiedPay,
			"payment_order_id": "11111111-2222-4333-8444-555555555555",
			"environment":      "sandbox", "organization_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			"product_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "app_id": "app.sub2.sandbox",
		},
	}
	event := unifiedpay.WebhookEvent{
		Environment: unifiedpay.EnvironmentSandbox, OrganizationID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		ProductID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", AppID: "app.sub2.sandbox",
		Resource: unifiedpay.PaymentOrderResource{
			PaymentOrderID: "11111111-2222-4333-8444-555555555555", OrderType: payment.OrderTypeBalance,
			AmountFen: 1234, Currency: payment.DefaultPaymentCurrency, PaymentMethod: unifiedpay.PaymentMethodAlipay,
		},
	}
	svc := &PaymentService{}
	require.NoError(t, svc.validateUnifiedPaymentEventOrder(order, event))

	delete(order.ProviderSnapshot, "payment_order_id")
	require.NoError(t, svc.validateUnifiedPaymentEventOrder(order, event), "a signed event may repair the create/binding race")
	order.ProviderSnapshot["payment_order_id"] = "99999999-2222-4333-8444-555555555555"
	require.EqualError(t, svc.validateUnifiedPaymentEventOrder(order, event), "payment_order_id_mismatch")
	order.ProviderSnapshot["payment_order_id"] = event.Resource.PaymentOrderID

	event.Resource.AmountFen = 1235
	require.EqualError(t, svc.validateUnifiedPaymentEventOrder(order, event), "amount_mismatch")

	event.Resource.AmountFen = 1234
	event.AppID = "app.foreign.sandbox"
	require.EqualError(t, svc.validateUnifiedPaymentEventOrder(order, event), "app_mismatch")
}
