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
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	"github.com/stretchr/testify/require"
)

const missingUnifiedPaymentOrderID = "11111111-2222-4333-8444-555555555555"

func TestReconcileMissingUnifiedPaymentOrderExpiresOnlyVerifiedHistoricalResetCard(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	client := newPaymentOrderLifecycleTestClient(t)
	order := newMissingUnifiedPaymentOrder(t, client, now.Add(-2*unifiedpay.MaximumClockSkew-time.Minute))
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		require.Equal(t, http.MethodGet, request.Method)
		require.Equal(t, "/v1/payment-orders?product_order_no="+order.OutTradeNo, request.RequestURI)
		writeMissingUnifiedPaymentOrderNotFound(t, writer)
	}))
	defer server.Close()

	svc := &PaymentService{entClient: client, resetCardNow: func() time.Time { return now }}
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)
	result := svc.reconcileMissingUnifiedPaymentOrder(ctx, order)
	require.Equal(t, missingUnifiedPaymentOrderClosed, result)
	require.Equal(t, 1, requests)

	persisted, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusExpired, persisted.Status)
	require.Equal(t, 12.34, persisted.Amount)
	require.Equal(t, 12.34, persisted.PayAmount)
	require.Nil(t, persisted.PaidAt)
	require.Empty(t, persisted.PaymentTradeNo)
	require.NotContains(t, persisted.ProductSnapshot, "payment_discount")
}

func TestReconcileMissingUnifiedPaymentOrderKeepsPendingWhenRecoveryIsNotSafe(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		expires  func(time.Time) time.Time
		prepare  func(*dbent.PaymentOrder, time.Time) map[string]any
		response func(http.ResponseWriter, *dbent.PaymentOrder, time.Time)
		wantCall int
	}{
		{
			name:    "case distinct historical app scope",
			expires: func(now time.Time) time.Time { return now.Add(-11 * time.Minute) },
			prepare: func(order *dbent.PaymentOrder, _ time.Time) map[string]any {
				snapshot := clonePaymentOrderSnapshot(order.ProviderSnapshot)
				snapshot["app_id"] = "app.Sub2.sandbox"
				return snapshot
			},
			response: func(writer http.ResponseWriter, _ *dbent.PaymentOrder, _ time.Time) {
				t.Errorf("case-distinct original app must not query the current app scope")
				writeMissingUnifiedPaymentOrderNotFound(t, writer)
			},
			wantCall: 0,
		},
		{
			name:    "query error",
			expires: func(now time.Time) time.Time { return now.Add(-11 * time.Minute) },
			response: func(writer http.ResponseWriter, _ *dbent.PaymentOrder, _ time.Time) {
				writer.WriteHeader(http.StatusServiceUnavailable)
			},
			wantCall: 1,
		},
		{
			name:    "foreign central scope",
			expires: func(now time.Time) time.Time { return now.Add(-11 * time.Minute) },
			response: func(writer http.ResponseWriter, order *dbent.PaymentOrder, now time.Time) {
				writeMissingUnifiedPaymentOrderLookup(t, writer, order, now, "app.foreign.sandbox", order.OrderType, 1234, unifiedpay.PaymentMethodAlipay, unifiedpay.StatusPendingPayment)
			},
			wantCall: 1,
		},
		{
			name:    "active dispatch lease",
			expires: func(now time.Time) time.Time { return now.Add(-11 * time.Minute) },
			prepare: func(order *dbent.PaymentOrder, now time.Time) map[string]any {
				snapshot := clonePaymentOrderSnapshot(order.ProviderSnapshot)
				leaseUntil := now.Add(time.Minute)
				snapshot[resetCardDispatchSnapshotKey] = resetCardDispatch{Version: resetCardDispatchVersion, Generation: 1, ClaimToken: "active-lease", LeaseExpiresAt: &leaseUntil, NeedsReconcile: true}
				return snapshot
			},
			response: func(writer http.ResponseWriter, _ *dbent.PaymentOrder, _ time.Time) {
				t.Fatalf("active dispatch lease must not trigger a central lookup")
			},
			wantCall: 0,
		},
		{
			name:    "expiry safety window has not elapsed",
			expires: func(now time.Time) time.Time { return now.Add(-time.Minute) },
			response: func(writer http.ResponseWriter, _ *dbent.PaymentOrder, _ time.Time) {
				writeMissingUnifiedPaymentOrderNotFound(t, writer)
			},
			wantCall: 1,
		},
		{
			name:    "stored checkout url",
			expires: func(now time.Time) time.Time { return now.Add(-11 * time.Minute) },
			prepare: func(order *dbent.PaymentOrder, _ time.Time) map[string]any {
				return order.ProviderSnapshot
			},
			response: func(writer http.ResponseWriter, _ *dbent.PaymentOrder, _ time.Time) {
				writeMissingUnifiedPaymentOrderNotFound(t, writer)
			},
			wantCall: 1,
		},
		{
			name:    "coupon snapshot",
			expires: func(now time.Time) time.Time { return now.Add(-11 * time.Minute) },
			response: func(writer http.ResponseWriter, _ *dbent.PaymentOrder, _ time.Time) {
				writeMissingUnifiedPaymentOrderNotFound(t, writer)
			},
			wantCall: 1,
		},
		{
			name:    "remote amount mismatch",
			expires: func(now time.Time) time.Time { return now.Add(-11 * time.Minute) },
			response: func(writer http.ResponseWriter, order *dbent.PaymentOrder, now time.Time) {
				writeMissingUnifiedPaymentOrderLookup(t, writer, order, now, "app.sub2.sandbox", order.OrderType, 1235, unifiedpay.PaymentMethodAlipay, unifiedpay.StatusPendingPayment)
			},
			wantCall: 1,
		},
		{
			name:    "remote method mismatch",
			expires: func(now time.Time) time.Time { return now.Add(-11 * time.Minute) },
			response: func(writer http.ResponseWriter, order *dbent.PaymentOrder, now time.Time) {
				writeMissingUnifiedPaymentOrderLookup(t, writer, order, now, "app.sub2.sandbox", order.OrderType, 1234, unifiedpay.PaymentMethodWechatPay, unifiedpay.StatusPendingPayment)
			},
			wantCall: 1,
		},
		{
			name:    "remote order type mismatch",
			expires: func(now time.Time) time.Time { return now.Add(-11 * time.Minute) },
			response: func(writer http.ResponseWriter, order *dbent.PaymentOrder, now time.Time) {
				writeMissingUnifiedPaymentOrderLookup(t, writer, order, now, "app.sub2.sandbox", payment.OrderTypeBalance, 1234, unifiedpay.PaymentMethodAlipay, unifiedpay.StatusPendingPayment)
			},
			wantCall: 1,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)
			client := newPaymentOrderLifecycleTestClient(t)
			order := newMissingUnifiedPaymentOrder(t, client, testCase.expires(now))
			if testCase.name == "stored checkout url" {
				updated, err := client.PaymentOrder.UpdateOneID(order.ID).SetPayURL("https://pay.example.test/checkout/original").Save(ctx)
				require.NoError(t, err)
				order = updated
			}
			if testCase.name == "coupon snapshot" {
				product := clonePaymentOrderSnapshot(order.ProductSnapshot)
				product["payment_discount"] = map[string]any{"code": "NO-RESET-COUPON"}
				updated, err := client.PaymentOrder.UpdateOneID(order.ID).SetProductSnapshot(product).Save(ctx)
				require.NoError(t, err)
				order = updated
			}
			if testCase.prepare != nil {
				snapshot := testCase.prepare(order, now)
				updated, err := client.PaymentOrder.UpdateOneID(order.ID).SetProviderSnapshot(snapshot).Save(ctx)
				require.NoError(t, err)
				order = updated
			}

			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls++
				testCase.response(writer, order, now)
			}))
			defer server.Close()

			svc := &PaymentService{entClient: client, resetCardNow: func() time.Time { return now }}
			svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)
			result := svc.reconcileMissingUnifiedPaymentOrder(ctx, order)
			require.Equal(t, missingUnifiedPaymentOrderRetry, result)
			require.Equal(t, testCase.wantCall, calls)
			persisted, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusPending, persisted.Status)
			require.Nil(t, persisted.PaidAt)
			require.Empty(t, persisted.PaymentTradeNo)
		})
	}
}

func TestReconcileMissingUnifiedPaymentOrderBindsExistingRemoteOrderWithoutChangingFunds(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	client := newPaymentOrderLifecycleTestClient(t)
	order := newMissingUnifiedPaymentOrder(t, client, now.Add(-11*time.Minute))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeMissingUnifiedPaymentOrderLookup(t, writer, order, now, "app.sub2.sandbox", order.OrderType, 1234, unifiedpay.PaymentMethodAlipay, unifiedpay.StatusPaid)
	}))
	defer server.Close()

	svc := &PaymentService{entClient: client, resetCardNow: func() time.Time { return now }}
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)
	result := svc.reconcileMissingUnifiedPaymentOrder(ctx, order)
	require.Equal(t, missingUnifiedPaymentOrderNotHandled, result)

	persisted, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPending, persisted.Status)
	require.Equal(t, missingUnifiedPaymentOrderID, persisted.PaymentTradeNo)
	require.Equal(t, missingUnifiedPaymentOrderID, psOrderProviderSnapshot(persisted).PaymentOrderID)
	require.Equal(t, 12.34, persisted.Amount)
	require.Equal(t, 12.34, persisted.PayAmount)
	require.Nil(t, persisted.PaidAt)
	require.NotContains(t, persisted.ProductSnapshot, "payment_discount")
}

func newMissingUnifiedPaymentOrder(t *testing.T, client *dbent.Client, expiresAt time.Time) *dbent.PaymentOrder {
	t.Helper()
	ctx := context.Background()
	user, err := client.User.Create().SetEmail("missing-unified@example.test").SetPasswordHash("test-only").SetUsername("missing-unified").Save(ctx)
	require.NoError(t, err)
	snapshot := map[string]any{
		"schema_version":             2,
		"provider_key":               payment.TypeUnifiedPay,
		"currency":                   payment.DefaultPaymentCurrency,
		"environment":                "sandbox",
		"organization_id":            "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"product_id":                 "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		"app_id":                     "app.sub2.sandbox",
		resetCardDispatchSnapshotKey: resetCardDispatch{Version: resetCardDispatchVersion},
	}
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).SetUserEmail(user.Email).SetUserName(user.Username).
		SetAmount(12.34).SetPayAmount(12.34).SetFeeRate(0).SetRechargeCode("MISSING-UNIFIED").
		SetOutTradeNo("sub2_missing_unified_order").SetPaymentType(payment.TypeAlipay).SetPaymentTradeNo("").
		SetProviderKey(payment.TypeUnifiedPay).SetProviderSnapshot(snapshot).
		SetProductSnapshot(map[string]any{"kind": "reset_card"}).
		SetOrderType(payment.OrderTypeResetCard).SetStatus(OrderStatusPending).SetExpiresAt(expiresAt).
		SetClientIP("127.0.0.1").SetSrcHost("localhost").Save(ctx)
	require.NoError(t, err)
	return order
}

func writeMissingUnifiedPaymentOrderNotFound(t *testing.T, writer http.ResponseWriter) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusNotFound)
	require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
		"error": "not_found", "request_id": "request.lookup.000001", "retryable": false,
	}))
}

func writeMissingUnifiedPaymentOrderLookup(t *testing.T, writer http.ResponseWriter, order *dbent.PaymentOrder, now time.Time, appID, orderType string, amountFen int64, method, status string) {
	t.Helper()
	response := map[string]any{
		"environment": "sandbox", "organization_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "product_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "app_id": appID,
		"payment_order_id": missingUnifiedPaymentOrderID, "product_order_no": order.OutTradeNo, "order_type": orderType,
		"amount_fen": amountFen, "paid_amount_fen": int64(0), "refunded_amount_fen": int64(0), "reserved_refund_amount_fen": int64(0), "refundable_amount_fen": int64(0),
		"currency": payment.DefaultPaymentCurrency, "payment_method": method, "status": status,
		"created_at": now.Add(-time.Hour), "updated_at": now, "expires_at": now.Add(time.Hour),
	}
	if status == unifiedpay.StatusPaid {
		response["paid_amount_fen"] = amountFen
		response["refundable_amount_fen"] = amountFen
		response["channel_transaction_id"] = "central-paid-transaction"
		response["paid_at"] = now.Add(-time.Minute)
	}
	writer.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(writer).Encode(response))
}

func TestReconcileMissingUnifiedPaymentOrderBindsCancelledLostCreateResponseForProductOrders(t *testing.T) {
	for _, orderType := range []string{payment.OrderTypeBalance, payment.OrderTypeSubscription} {
		t.Run(orderType, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)
			client := newPaymentOrderLifecycleTestClient(t)
			order := newCancelledMissingUnifiedPaymentOrder(t, client, orderType, now)
			require.NoError(t, ensureLocalCancellationCloseWorkTx(ctx, client, order))

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				require.Equal(t, http.MethodGet, request.Method)
				require.Equal(t, "/v1/payment-orders?product_order_no="+order.OutTradeNo, request.RequestURI)
				writeMissingUnifiedPaymentOrderLookup(t, writer, order, now, "app.sub2.sandbox", orderType, 1234, unifiedpay.PaymentMethodAlipay, unifiedpay.StatusPendingPayment)
			}))
			defer server.Close()

			svc := &PaymentService{entClient: client, resetCardNow: func() time.Time { return now }}
			svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)
			require.Equal(t, missingUnifiedPaymentOrderNotHandled, svc.reconcileMissingUnifiedPaymentOrder(ctx, order))

			persisted, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusCancelled, persisted.Status)
			require.Nil(t, persisted.PaidAt)
			require.Equal(t, missingUnifiedPaymentOrderID, persisted.PaymentTradeNo)
			require.Equal(t, missingUnifiedPaymentOrderID, psOrderProviderSnapshot(persisted).PaymentOrderID)
			require.Empty(t, persisted.PayURL)
			require.Empty(t, persisted.QrCode)
			require.Empty(t, persisted.QrCodeImg)

			require.Equal(t, 1, localCancellationWorkCount(t, ctx, client, order.ID, localCancellationWorkClose))
			rows, err := client.QueryContext(ctx, `
				SELECT status, provider_key, payment_trade_no
				FROM payment_local_cancellation_work
				WHERE order_id = $1 AND work_kind = $2`, order.ID, localCancellationWorkClose)
			require.NoError(t, err)
			defer func() { _ = rows.Close() }()
			require.True(t, rows.Next())
			var status, providerKey, tradeNo string
			require.NoError(t, rows.Scan(&status, &providerKey, &tradeNo))
			require.NoError(t, rows.Err())
			require.Equal(t, localCancellationWorkPending, status)
			require.Equal(t, payment.TypeUnifiedPay, providerKey)
			require.Equal(t, missingUnifiedPaymentOrderID, tradeNo)
		})
	}
}

func TestReconcileMissingUnifiedPaymentOrderDoesNotGuessCancelledRemoteIDWhenAbsent(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	client := newPaymentOrderLifecycleTestClient(t)
	order := newCancelledMissingUnifiedPaymentOrder(t, client, payment.OrderTypeBalance, now)
	require.NoError(t, ensureLocalCancellationCloseWorkTx(ctx, client, order))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeMissingUnifiedPaymentOrderNotFound(t, writer)
	}))
	defer server.Close()

	svc := &PaymentService{entClient: client, resetCardNow: func() time.Time { return now }}
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)
	require.Equal(t, missingUnifiedPaymentOrderRetry, svc.reconcileMissingUnifiedPaymentOrder(ctx, order))

	persisted, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCancelled, persisted.Status)
	require.Empty(t, persisted.PaymentTradeNo)
	require.Empty(t, psOrderProviderSnapshot(persisted).PaymentOrderID)
	rows, err := client.QueryContext(ctx, `
		SELECT payment_trade_no FROM payment_local_cancellation_work
		WHERE order_id = $1 AND work_kind = $2`, order.ID, localCancellationWorkClose)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	require.True(t, rows.Next())
	var tradeNo string
	require.NoError(t, rows.Scan(&tradeNo))
	require.Empty(t, tradeNo)
}

func newCancelledMissingUnifiedPaymentOrder(t *testing.T, client *dbent.Client, orderType string, now time.Time) *dbent.PaymentOrder {
	t.Helper()
	order := newMissingUnifiedPaymentOrder(t, client, now.Add(time.Hour))
	updated, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetOrderType(orderType).
		SetStatus(OrderStatusCancelled).
		SetOutTradeNo("sub2_cancelled_missing_unified_" + orderType).
		SetProductSnapshot(map[string]any{"kind": orderType}).
		ClearPayURL().ClearQrCode().ClearQrCodeImg().
		Save(context.Background())
	require.NoError(t, err)
	return updated
}

func TestReconcileMissingUnifiedPaymentOrderCancelledAbsenceUsesOriginalDeadline(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		orderType  string
		expiresAt  func(time.Time) time.Time
		statusCode int
		want       missingUnifiedPaymentOrderResult
	}{
		{
			name:       "before safe deadline retries",
			orderType:  payment.OrderTypeBalance,
			expiresAt:  func(now time.Time) time.Time { return now.Add(-2*unifiedpay.MaximumClockSkew + time.Second) },
			statusCode: http.StatusNotFound,
			want:       missingUnifiedPaymentOrderRetry,
		},
		{
			name:       "balance safe absence closes durable work",
			orderType:  payment.OrderTypeBalance,
			expiresAt:  func(now time.Time) time.Time { return now.Add(-2*unifiedpay.MaximumClockSkew - time.Second) },
			statusCode: http.StatusNotFound,
			want:       missingUnifiedPaymentOrderClosed,
		},
		{
			name:       "subscription safe absence closes durable work",
			orderType:  payment.OrderTypeSubscription,
			expiresAt:  func(now time.Time) time.Time { return now.Add(-2*unifiedpay.MaximumClockSkew - time.Second) },
			statusCode: http.StatusNotFound,
			want:       missingUnifiedPaymentOrderClosed,
		},
		{
			name:       "unknown lookup retries after safe deadline",
			orderType:  payment.OrderTypeBalance,
			expiresAt:  func(now time.Time) time.Time { return now.Add(-2*unifiedpay.MaximumClockSkew - time.Second) },
			statusCode: http.StatusServiceUnavailable,
			want:       missingUnifiedPaymentOrderRetry,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)
			client := newPaymentOrderLifecycleTestClient(t)
			order := newCancelledMissingUnifiedPaymentOrder(t, client, testCase.orderType, now)
			updated, err := client.PaymentOrder.UpdateOneID(order.ID).SetExpiresAt(testCase.expiresAt(now)).Save(ctx)
			require.NoError(t, err)
			order = updated
			require.NoError(t, ensureLocalCancellationCloseWorkTx(ctx, client, order))

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if testCase.statusCode == http.StatusNotFound {
					writeMissingUnifiedPaymentOrderNotFound(t, writer)
					return
				}
				writer.WriteHeader(testCase.statusCode)
			}))
			defer server.Close()
			svc := &PaymentService{entClient: client, resetCardNow: func() time.Time { return now }}
			svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)
			require.Equal(t, testCase.want, svc.reconcileMissingUnifiedPaymentOrder(ctx, order))

			persisted, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusCancelled, persisted.Status)
			require.Nil(t, persisted.PaidAt)
			require.Empty(t, persisted.PaymentTradeNo)
			require.Empty(t, psOrderProviderSnapshot(persisted).PaymentOrderID)
			require.Equal(t, 1, localCancellationWorkCount(t, ctx, client, order.ID, localCancellationWorkClose))
		})
	}
}
