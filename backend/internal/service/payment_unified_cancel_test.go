//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestUnifiedCancelKeepsPendingWhenUpstreamCannotBeConfirmed(t *testing.T) {
	for _, runtimeEnabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "gateway unavailable", true: "query unavailable"}[runtimeEnabled], func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentOrderLifecycleTestClient(t)
			user, err := client.User.Create().SetEmail("unified-cancel@example.test").SetPasswordHash("test-only").SetUsername("cancel-test").Save(ctx)
			require.NoError(t, err)
			order, err := client.PaymentOrder.Create().SetUserID(user.ID).SetUserEmail(user.Email).SetUserName(user.Username).
				SetAmount(1).SetPayAmount(1).SetFeeRate(0).SetRechargeCode("UNIFIED-CANCEL").SetOutTradeNo("sub2_unified_cancel").
				SetPaymentType(payment.TypeWxpay).SetPaymentTradeNo("").SetProviderKey(payment.TypeUnifiedPay).
				SetProviderSnapshot(map[string]any{"schema_version": 2, "provider_key": payment.TypeUnifiedPay, "payment_order_id": "11111111-2222-4333-8444-555555555555"}).
				SetOrderType(payment.OrderTypeBalance).SetStatus(OrderStatusPending).SetExpiresAt(time.Now().Add(time.Hour)).
				SetClientIP("127.0.0.1").SetSrcHost("localhost").Save(ctx)
			require.NoError(t, err)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, http.MethodGet, r.Method)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			svc := &PaymentService{entClient: client}
			if runtimeEnabled {
				svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)
			}
			for _, target := range []string{OrderStatusCancelled, OrderStatusExpired} {
				_, err = svc.cancelCore(ctx, order, target, "test", "test cancellation")
				require.Error(t, err)
				persisted, readErr := client.PaymentOrder.Get(ctx, order.ID)
				require.NoError(t, readErr)
				require.Equal(t, OrderStatusPending, persisted.Status)
			}
			if runtimeEnabled {
				require.Equal(t, 2, calls)
			} else {
				require.Zero(t, calls)
			}
		})
	}
}
