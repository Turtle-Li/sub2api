//go:build integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/stretchr/testify/require"
)

type localCancellationDirectCloseProvider struct {
	queryRefs   []string
	closeRefs   []string
	closeErrors []error
}

func (*localCancellationDirectCloseProvider) Name() string { return "local-cancellation-direct-close" }

func (*localCancellationDirectCloseProvider) ProviderKey() string { return payment.TypeAlipay }

func (*localCancellationDirectCloseProvider) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeAlipay}
}

func (*localCancellationDirectCloseProvider) CreatePayment(context.Context, payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	return nil, errors.New("unexpected provider create")
}

func (p *localCancellationDirectCloseProvider) QueryOrder(_ context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	p.queryRefs = append(p.queryRefs, tradeNo)
	return &payment.QueryOrderResponse{TradeNo: tradeNo, Status: payment.ProviderStatusPending}, nil
}

func (*localCancellationDirectCloseProvider) VerifyNotification(context.Context, string, map[string]string) (*payment.PaymentNotification, error) {
	return nil, errors.New("unexpected provider notification verification")
}

func (*localCancellationDirectCloseProvider) Refund(context.Context, payment.RefundRequest) (*payment.RefundResponse, error) {
	return nil, errors.New("unexpected provider refund")
}

func (p *localCancellationDirectCloseProvider) CancelPayment(_ context.Context, tradeNo string) error {
	p.closeRefs = append(p.closeRefs, tradeNo)
	if len(p.closeErrors) == 0 {
		return nil
	}
	err := p.closeErrors[0]
	p.closeErrors = p.closeErrors[1:]
	return err
}

func TestLocalCancellationPostgresWorkerClosesDirectOrderDurably(t *testing.T) {
	client, db, ctx := newLocalCancellationPostgresFixture(t)
	t.Setenv(runtimegate.StateFileEnv, "")
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })

	user := createLocalCancellationPostgresUser(t, ctx, client, "direct-close")
	// Deliberately leave provider_key unset to exercise the narrow legacy registry
	// fallback. Production orders use a pinned provider instance or snapshot.
	order := createLocalCancellationPostgresOrder(t, ctx, client, user, localCancellationPostgresOrderInput{})
	provider := &localCancellationDirectCloseProvider{
		closeErrors: []error{errors.New("provider close temporarily unavailable")},
	}
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := &PaymentService{entClient: client, registry: registry, providersLoaded: true}

	result, err := svc.CancelOrder(ctx, order.ID, user.ID)
	require.NoError(t, err)
	require.Equal(t, checkPaidResultCancelled, result)
	require.Empty(t, provider.queryRefs, "CancelOrder must not query the provider inline")
	require.Empty(t, provider.closeRefs, "CancelOrder must not close upstream inline")

	processed, err := svc.ReconcileLocalCancellationWork(ctx, "direct-close-worker")
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Equal(t, []string{order.OutTradeNo}, provider.queryRefs)
	require.Equal(t, []string{order.OutTradeNo}, provider.closeRefs)

	var status, lastError string
	var attempts int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT status, attempts, COALESCE(last_error, '')
		FROM payment_local_cancellation_work
		WHERE order_id = $1 AND work_kind = 'CLOSE'`, order.ID).Scan(&status, &attempts, &lastError))
	require.Equal(t, localCancellationWorkPending, status)
	require.Equal(t, 1, attempts)
	require.Contains(t, lastError, "provider close temporarily unavailable")

	// A restart or later worker retry must use the original row, call the same
	// provider reference, and complete only after upstream close acceptance.
	_, err = db.ExecContext(ctx, `
		UPDATE payment_local_cancellation_work
		SET available_at = NOW() - INTERVAL '1 second'
		WHERE order_id = $1 AND work_kind = 'CLOSE'`, order.ID)
	require.NoError(t, err)
	processed, err = svc.ReconcileLocalCancellationWork(ctx, "direct-close-worker-retry")
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Equal(t, []string{order.OutTradeNo, order.OutTradeNo}, provider.queryRefs)
	require.Equal(t, []string{order.OutTradeNo, order.OutTradeNo}, provider.closeRefs)

	var workCount int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM payment_local_cancellation_work
		WHERE order_id = $1 AND work_kind = 'CLOSE'`, order.ID).Scan(&workCount))
	require.Equal(t, 1, workCount)
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT status, attempts, COALESCE(last_error, '')
		FROM payment_local_cancellation_work
		WHERE order_id = $1 AND work_kind = 'CLOSE'`, order.ID).Scan(&status, &attempts, &lastError))
	require.Equal(t, localCancellationWorkCompleted, status)
	require.Equal(t, 1, attempts)
	require.Empty(t, lastError)

	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCancelled, stored.Status)
}

func TestLocalCancellationPostgresWorkerCompletesCancelledMissingUnifiedCloseAfterSafeAbsence(t *testing.T) {
	client, db, ctx := newLocalCancellationPostgresFixture(t)
	t.Setenv(runtimegate.StateFileEnv, "")
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })

	now := time.Now().UTC().Truncate(time.Second)
	snapshot := localCancellationUnifiedProviderSnapshot()
	delete(snapshot, "payment_order_id")
	user := createLocalCancellationPostgresUser(t, ctx, client, "missing-unified-close")
	order := createLocalCancellationPostgresOrder(t, ctx, client, user, localCancellationPostgresOrderInput{
		ProviderKey: payment.TypeUnifiedPay, ProviderSnapshot: snapshot, Status: OrderStatusCancelled,
	})
	order, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetExpiresAt(now.Add(-2*unifiedpay.MaximumClockSkew - time.Second)).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, ensureLocalCancellationCloseWorkTx(ctx, client, order))

	lookupCalls := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookupCalls++
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/payment-orders?product_order_no="+order.OutTradeNo, r.RequestURI)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"error": "not_found", "request_id": "missing-unified-close-lookup", "retryable": false,
		}))
	}))
	defer gateway.Close()

	svc := &PaymentService{entClient: client, resetCardNow: func() time.Time { return now }}
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, gateway.URL), nil)
	processed, err := svc.ReconcileLocalCancellationWork(ctx, "missing-unified-close-worker")
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Equal(t, 1, lookupCalls)

	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCancelled, stored.Status)
	require.Nil(t, stored.PaidAt)
	require.Empty(t, stored.PaymentTradeNo)
	require.Empty(t, psOrderProviderSnapshot(stored).PaymentOrderID)
	var workStatus string
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT status FROM payment_local_cancellation_work
		WHERE order_id = $1 AND work_kind = 'CLOSE'`, order.ID).Scan(&workStatus))
	require.Equal(t, localCancellationWorkCompleted, workStatus)
}

func TestLocalCancellationPostgresWorkerRecoversReviewedUnifiedPaidAfterClose(t *testing.T) {
	client, db, ctx := newLocalCancellationPostgresFixture(t)
	t.Setenv(runtimegate.StateFileEnv, "")
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })

	snapshot := localCancellationUnifiedProviderSnapshot()
	paymentOrderID, ok := snapshot["payment_order_id"].(string)
	require.True(t, ok)
	require.NotEmpty(t, paymentOrderID)
	user := createLocalCancellationPostgresUser(t, ctx, client, "unified-query-late")
	order := createLocalCancellationPostgresOrder(t, ctx, client, user, localCancellationPostgresOrderInput{
		PayAmount:        19,
		ProviderKey:      payment.TypeUnifiedPay,
		ProviderSnapshot: snapshot,
		PaymentTradeNo:   paymentOrderID,
	})

	const channelTransactionID = "alipay_query_paid_after_close_001"
	const refundRequestID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	var refundCalls int
	var received localCancellationUnifiedRefundRequest
	now := time.Now().UTC().Truncate(time.Second)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/payment-orders/"+paymentOrderID:
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"environment": "sandbox", "organization_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
				"product_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "app_id": "app.sub2.sandbox",
				"payment_order_id": paymentOrderID, "product_order_no": order.OutTradeNo, "order_type": payment.OrderTypeBalance,
				"amount_fen": 1900, "paid_amount_fen": 1900, "refunded_amount_fen": 0,
				"reserved_refund_amount_fen": 0, "refundable_amount_fen": 1900,
				"currency": payment.DefaultPaymentCurrency, "payment_method": unifiedpay.PaymentMethodAlipay,
				"status": unifiedpay.StatusPaidAfterClose, "channel_out_trade_no": "sandbox_query_close_001",
				"channel_transaction_id": channelTransactionID, "paid_at": now,
				"needs_manual_review": true, "created_at": now.Add(-time.Hour), "updated_at": now, "expires_at": now.Add(time.Hour),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/refund-requests":
			refundCalls++
			require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
			require.Equal(t, localCancellationDirectRefundIdempotencyKey(order.ID), r.Header.Get(unifiedpay.HeaderIdempotencyKey))
			w.WriteHeader(http.StatusAccepted)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"environment": "sandbox", "organization_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
				"product_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "refund_request_id": refundRequestID,
				"payment_order_id": paymentOrderID, "product_refund_no": received.ProductRefundNo,
				"channel_out_refund_no": "sandbox_late_refund_001", "amount_fen": received.AmountFen,
				"currency": payment.DefaultPaymentCurrency, "payment_method": unifiedpay.PaymentMethodAlipay,
				"status": unifiedpay.RefundStatusUnknown, "needs_manual_review": true,
				"created_at": now, "updated_at": now,
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()

	svc := &PaymentService{entClient: client}
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, gateway.URL), nil)
	result, err := svc.CancelOrder(ctx, order.ID, user.ID)
	require.NoError(t, err)
	require.Equal(t, checkPaidResultCancelled, result)

	processed, err := svc.ReconcileLocalCancellationWork(ctx, "unified-paid-after-close-query-worker")
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCancelled, stored.Status)
	require.NotNil(t, stored.PaidAt)
	require.Equal(t, channelTransactionID, stored.PaymentTradeNo)
	require.Equal(t, 19.0, stored.PayAmount)

	attempt, err := loadUnifiedRefundAttempt(ctx, client, order.ID, "sub2_cancel_"+strconv.FormatInt(order.ID, 10))
	require.NoError(t, err)
	require.Equal(t, cancelLatePaymentRefundKind, attempt.RefundKind)
	require.Equal(t, int64(1900), attempt.AmountFen)
	require.Zero(t, attempt.BalanceAmountMinor)
	require.False(t, attempt.DeductBalance)
	require.False(t, attempt.EntitlementReserved)
	require.Equal(t, "service_not_delivered", attempt.ReasonCode)
	require.False(t, attempt.NeedsManualReview, "central payment review must not suppress the eligible automatic late-refund attempt")

	refundResult, err := svc.advanceUnifiedRefund(ctx, attempt)
	require.NoError(t, err)
	require.False(t, refundResult.Success)
	require.Contains(t, refundResult.Warning, "manual review")
	require.Equal(t, 1, refundCalls)
	require.Equal(t, paymentOrderID, received.PaymentOrderID)
	require.Equal(t, "sub2_cancel_"+strconv.FormatInt(order.ID, 10), received.ProductRefundNo)
	require.Equal(t, int64(1900), received.AmountFen)
	require.Equal(t, "service_not_delivered", received.ReasonCode)

	refreshedAttempt, err := loadUnifiedRefundAttempt(ctx, client, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedRefundPending, refreshedAttempt.Status)
	require.True(t, refreshedAttempt.NeedsManualReview, "an unsafe central refund response must remain a durable manual fence")
	require.Equal(t, refundRequestID, refreshedAttempt.RefundRequestID)

	var workStatus string
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT status FROM payment_local_cancellation_work
		WHERE order_id = $1 AND work_kind = 'CLOSE'`, order.ID).Scan(&workStatus))
	require.Equal(t, localCancellationWorkCompleted, workStatus)
}
