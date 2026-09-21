//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func resumeTestOrder(t *testing.T, client *dbent.Client) *dbent.PaymentOrder {
	t.Helper()
	ctx := context.Background()
	user, err := client.User.Create().SetEmail("resume@example.test").SetPasswordHash("test").SetUsername("resume").Save(ctx)
	require.NoError(t, err)
	order, err := client.PaymentOrder.Create().SetUserID(user.ID).SetUserEmail(user.Email).SetUserName(user.Username).
		SetAmount(120).SetPayAmount(120).SetFeeRate(0).SetRechargeCode("RESUME-TEST").SetOutTradeNo("sub2_resume_checkout").
		SetPaymentType(payment.TypeAlipay).SetPaymentTradeNo("").SetProviderKey(payment.TypeUnifiedPay).
		SetProviderSnapshot(map[string]any{"schema_version": 2, "provider_key": payment.TypeUnifiedPay, "payment_order_id": "11111111-2222-4333-8444-555555555555"}).
		SetOrderType(payment.OrderTypeBalance).SetStatus(OrderStatusPending).SetExpiresAt(time.Now().Add(time.Hour)).
		SetPayURL("https://pay.example.test/checkout/original").SetClientIP("127.0.0.1").SetSrcHost("localhost").Save(ctx)
	require.NoError(t, err)
	return order
}

func TestResumeExistingCheckoutAndCancellation(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	order := resumeTestOrder(t, client)
	state := "PENDING_PAYMENT"
	queries, closes := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			closes++
			state = "PAYMENT_CONFIRMATION_PENDING"
		} else {
			queries++
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"environment": "sandbox", "organization_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "product_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "app_id": "app.sub2.sandbox",
			"payment_order_id": "11111111-2222-4333-8444-555555555555", "product_order_no": order.OutTradeNo, "order_type": "balance", "amount_fen": 12000, "paid_amount_fen": 0, "currency": "CNY", "payment_method": "alipay", "status": state, "created_at": order.CreatedAt, "expires_at": order.ExpiresAt,
		}))
	}))
	defer server.Close()
	svc := &PaymentService{entClient: client}
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)
	_, err := svc.ResumeOrder(ctx, order.ID, order.UserID+1)
	require.Error(t, err)
	require.Zero(t, queries)
	resumed, err := svc.ResumeOrder(ctx, order.ID, order.UserID)
	require.NoError(t, err)
	require.Equal(t, order.ID, resumed.OrderID)
	require.Equal(t, *order.PayURL, resumed.PayURL)
	require.True(t, order.ExpiresAt.Equal(resumed.ExpiresAt))
	require.Zero(t, closes)
	result, err := svc.CancelOrder(ctx, order.ID, order.UserID)
	require.NoError(t, err)
	require.Equal(t, "cancellation_requested", result)
	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPending, stored.Status)
	pending, err := svc.paymentOrderCancellationPendingIDs(ctx, []int64{order.ID})
	require.NoError(t, err)
	require.True(t, pending[order.ID])
	_, err = svc.ResumeOrder(ctx, order.ID, order.UserID)
	require.Equal(t, "PAYMENT_CANCELLATION_PENDING", infraerrors.Reason(err))
	require.Zero(t, closes, "cancellation is deferred to the background reconciler")
	count, err := client.PaymentOrder.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	state = "CLOSED"
	_, err = svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, order.UserID)
	require.NoError(t, err)
	stored, err = client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCancelled, stored.Status)
	_, err = svc.ResumeOrder(ctx, order.ID, order.UserID)
	require.Error(t, err)
}

func TestResumeOrderHydratesTrustedCentralAlipayFrame(t *testing.T) {
	trustedFrame := "https://openapi.alipay.com/gateway.do?biz_content=%7B%22qr_pay_mode%22%3A%224%22%2C%22qrcode_width%22%3A%22224%22%7D&method=alipay.trade.page.pay&sign=test-sign&sign_type=RSA2"
	for _, testCase := range []struct {
		name, status, frame, want string
	}{
		{name: "pending trusted", status: "PENDING_PAYMENT", frame: trustedFrame, want: trustedFrame},
		{name: "pending malicious", status: "PENDING_PAYMENT", frame: "https://attacker.example/frame"},
		{name: "created state", status: "CREATED", frame: trustedFrame},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentOrderLifecycleTestClient(t)
			order := resumeTestOrder(t, client)
			requests, createRequests := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests++
				if request.Method == http.MethodPost && request.URL.Path == "/v1/payment-orders" {
					createRequests++
				}
				require.Equal(t, http.MethodGet, request.Method)
				require.Equal(t, "/v1/payment-orders/11111111-2222-4333-8444-555555555555", request.URL.Path)
				writer.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
					"environment": "sandbox", "organization_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "product_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "app_id": "app.sub2.sandbox",
					"payment_order_id": "11111111-2222-4333-8444-555555555555", "product_order_no": order.OutTradeNo, "order_type": "balance", "amount_fen": 12000, "paid_amount_fen": 0, "currency": "CNY", "payment_method": "alipay", "status": testCase.status,
					"checkout_frame_url": testCase.frame, "created_at": order.CreatedAt, "expires_at": order.ExpiresAt,
				}))
			}))
			defer server.Close()

			svc := &PaymentService{entClient: client}
			svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)
			response, err := svc.ResumeOrder(ctx, order.ID, order.UserID)
			require.NoError(t, err)
			require.Equal(t, testCase.want, response.CheckoutFrameURL)
			require.Equal(t, 2, requests, "resume queries payment state and separately obtains its current frame")
			require.Zero(t, createRequests, "checkout recovery must not create another financial order")
		})
	}
}

func TestResumeCheckoutFailsClosedOnUnknownAndMissingLaunch(t *testing.T) {
	for _, tc := range []struct {
		name, status                           string
		unavailable, review, noLaunch, expired bool
	}{
		{name: "upstream unavailable", unavailable: true},
		{name: "confirmation pending", status: "PAYMENT_CONFIRMATION_PENDING"},
		{name: "manual review", status: "PENDING_PAYMENT", review: true},
		{name: "no stored checkout", status: "PENDING_PAYMENT", noLaunch: true},
		{name: "expiry requests close", status: "PENDING_PAYMENT", expired: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentOrderLifecycleTestClient(t)
			order := resumeTestOrder(t, client)
			if tc.noLaunch {
				var err error
				order, err = client.PaymentOrder.UpdateOneID(order.ID).ClearPayURL().Save(ctx)
				require.NoError(t, err)
			}
			if tc.expired {
				var err error
				order, err = client.PaymentOrder.UpdateOneID(order.ID).SetExpiresAt(time.Now().Add(-time.Minute)).Save(ctx)
				require.NoError(t, err)
			}
			closes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NotEqual(t, "/v1/payment-orders", r.URL.Path, "resume must never create another order")
				if tc.unavailable {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				state := tc.status
				if r.Method == http.MethodPost {
					closes++
					state = "PAYMENT_CONFIRMATION_PENDING"
				}
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"environment": "sandbox", "organization_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "product_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "app_id": "app.sub2.sandbox",
					"payment_order_id": "11111111-2222-4333-8444-555555555555", "product_order_no": order.OutTradeNo, "order_type": "balance", "amount_fen": 12000, "paid_amount_fen": 0, "currency": "CNY", "payment_method": "alipay", "status": state, "needs_manual_review": tc.review, "created_at": order.CreatedAt, "expires_at": order.ExpiresAt,
				}))
			}))
			defer server.Close()
			svc := &PaymentService{entClient: client}
			svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)
			result, err := svc.ResumeOrder(ctx, order.ID, order.UserID)
			require.Error(t, err)
			require.Nil(t, result)
			stored, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusPending, stored.Status)
			require.True(t, order.ExpiresAt.Equal(stored.ExpiresAt))
			if tc.expired {
				require.Equal(t, 1, closes)
			} else {
				require.Zero(t, closes)
			}
		})
	}
}

func TestCancellationIntentFencesResumeBeforeProviderCall(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	order := resumeTestOrder(t, client)
	svc := &PaymentService{entClient: client}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		pending, err := svc.paymentOrderCancellationPendingIDs(ctx, []int64{order.ID})
		require.NoError(t, err)
		require.True(t, pending[order.ID], "cancel intent must be durable before any provider interaction")
		if pending[order.ID] {
			resumed, err := svc.ResumeOrder(ctx, order.ID, order.UserID)
			require.Nil(t, resumed)
			require.Equal(t, "PAYMENT_CANCELLATION_PENDING", infraerrors.Reason(err))
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)
	result, err := svc.CancelOrder(ctx, order.ID, order.UserID)
	require.NoError(t, err)
	require.Equal(t, "cancellation_requested", result)
	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPending, stored.Status)
	_, err = svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, order.UserID)
	require.NoError(t, err)
	require.Equal(t, 1, calls, "verification retries a persisted cancellation before checkout expiry")
}
