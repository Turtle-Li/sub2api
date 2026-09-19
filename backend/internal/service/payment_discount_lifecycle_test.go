//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

type discountUncertainProvider struct {
	paymentOrderLifecycleQueryProvider
	cancelError error
}

func (p *discountUncertainProvider) CancelPayment(context.Context, string) error {
	return p.cancelError
}

func TestPaymentDiscountUnknownGatewayStateRetainsReservation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		response    *payment.QueryOrderResponse
		cancelError error
	}{
		{name: "empty query"},
		{name: "manual review", response: &payment.QueryOrderResponse{Status: payment.ProviderStatusPending, Metadata: map[string]string{"needs_manual_review": "true"}}},
		{name: "cancel failed", response: &payment.QueryOrderResponse{Status: payment.ProviderStatusPending}, cancelError: errors.New("gateway timeout")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newUnifiedRefundSQLiteClient(t)
			installPaymentDiscountTestSchema(t, client)
			user, err := client.User.Create().SetEmail("uncertain@example.test").SetPasswordHash("test").Save(ctx)
			require.NoError(t, err)
			provider := &discountUncertainProvider{paymentOrderLifecycleQueryProvider: paymentOrderLifecycleQueryProvider{resp: tc.response}, cancelError: tc.cancelError}
			registry := payment.NewRegistry()
			registry.Register(provider)
			svc := &PaymentService{entClient: client, registry: registry, providersLoaded: true}
			coupon, err := svc.SavePaymentDiscountCode(ctx, user.ID, 0, PaymentDiscountCodeInput{Code: "SAFE2026", DiscountType: "fixed", DiscountValue: "10", Currency: "CNY", MaxUses: 1, StartsAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour), Enabled: true}, 0)
			require.NoError(t, err)
			order, err := newDiscountIntegrationOrder(t, svc, user, coupon.Code, "a")
			require.NoError(t, err)
			require.Equal(t, checkPaidResultUnconfirmed, svc.checkPaid(ctx, order))
			require.Equal(t, 1, provider.queryCalls)
			uses, total, err := svc.ListPaymentDiscountUses(ctx, coupon.ID, 1, 20)
			require.NoError(t, err)
			require.Equal(t, 1, total)
			require.Equal(t, "reserved", uses[0].Status)
			persisted, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusPending, persisted.Status)
		})
	}
}
