//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type paymentRefundReconciliationStoreStub struct {
	mu          sync.Mutex
	batches     [][]PaymentRefundReconciliationCandidate
	completed   []string
	retried     []paymentRefundReconciliationRetry
	stats       PaymentRefundReconciliationStats
	claimErr    error
	completeErr error
	retryErr    error
	statsErr    error
}

type paymentRefundReconciliationRetry struct {
	productRefundNo string
	attempts        int
	availableAt     time.Time
	lastError       string
}

func (s *paymentRefundReconciliationStoreStub) ClaimReviewedEntitlementReservations(_ context.Context, _ string, _ int, _ time.Duration) ([]PaymentRefundReconciliationCandidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if len(s.batches) == 0 {
		return nil, nil
	}
	batch := append([]PaymentRefundReconciliationCandidate(nil), s.batches[0]...)
	s.batches = s.batches[1:]
	return batch, nil
}

func (s *paymentRefundReconciliationStoreStub) CompleteClaim(_ context.Context, productRefundNo, _ string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completeErr != nil {
		return s.completeErr
	}
	s.completed = append(s.completed, productRefundNo)
	return nil
}

func (s *paymentRefundReconciliationStoreStub) RetryClaim(_ context.Context, productRefundNo, _ string, _ time.Duration, availableAt time.Time, lastError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retryErr != nil {
		return s.retryErr
	}
	attempts := 0
	if len(s.retried) > 0 {
		attempts = s.retried[len(s.retried)-1].attempts + 1
	}
	s.retried = append(s.retried, paymentRefundReconciliationRetry{
		productRefundNo: productRefundNo,
		attempts:        attempts,
		availableAt:     availableAt,
		lastError:       lastError,
	})
	return nil
}

func (s *paymentRefundReconciliationStoreStub) Stats(context.Context) (PaymentRefundReconciliationStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats, s.statsErr
}

func TestPaymentRefundReconciliationReplaysReservedAttemptAndFinalizesOnce(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	backgroundState := filepath.Join(t.TempDir(), "background-state")
	require.NoError(t, os.WriteFile(backgroundState, []byte("active\n"), 0o600))
	t.Setenv(runtimegate.StateFileEnv, backgroundState)
	require.True(t, runtimegate.SharedWorkAllowed())
	require.True(t, runtimegate.DurableRecoveryWorkAllowed())
	ctx := context.Background()
	svc, order := newReviewedBalanceRefundFixture(t, 100, 0.1)
	review, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "reconciliation test")
	require.NoError(t, err)
	attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.NoError(t, err)
	// A release may move this generation to standby after the reviewed attempt
	// is durable. New reservations are then rejected, while the lease-owned
	// recovery path must finish this exact idempotent provider operation.
	require.NoError(t, os.WriteFile(backgroundState, []byte("standby\n"), 0o600))
	require.False(t, runtimegate.SharedWorkAllowed())
	require.True(t, runtimegate.DurableRecoveryWorkAllowed())

	var posts int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/refund-requests", r.URL.Path)
		posts++
		writePaymentRefundReconciliationResponse(t, w, r, attempt, unifiedpay.RefundStatusSucceeded, false)
	}))
	defer provider.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, provider.URL), nil)

	store := &paymentRefundReconciliationStoreStub{batches: [][]PaymentRefundReconciliationCandidate{
		{{ProductRefundNo: attempt.ProductRefundNo, OrderID: order.ID}},
		{{ProductRefundNo: attempt.ProductRefundNo, OrderID: order.ID}},
	}}
	worker := NewPaymentRefundReconciliationService(svc, store)
	t.Cleanup(worker.Stop)
	require.NoError(t, worker.RunOnce(ctx))
	afterFirst, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedpay.RefundStatusSucceeded, afterFirst.Status)
	require.False(t, afterFirst.EntitlementReserved)
	require.NoError(t, worker.RunOnce(ctx), "a stale second lease must observe terminal persistence")
	require.Equal(t, 1, posts, "the durable request is submitted once after terminal finalization")

	persisted, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedpay.RefundStatusSucceeded, persisted.Status)
	require.False(t, persisted.EntitlementReserved)
	funding, user := loadReviewedBalanceRefundState(t, svc, order)
	require.True(t, funding.ReservedPaid.IsZero())
	require.True(t, funding.RefundedPaid.Equal(decimalRequire("0.10")))
	require.Zero(t, user.Balance)
	require.Len(t, store.completed, 2, "each acquired lease is acknowledged, but only the first may finalize")
}

func TestPaymentRefundReconciliationKeepsUnknownReservationThenQueriesSameRequest(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	t.Setenv("SUB2API_BACKGROUND_STATE_FILE", "")
	ctx := context.Background()
	svc, order := newReviewedBalanceRefundFixture(t, 100, 0.1)
	review, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "unknown reconciliation test")
	require.NoError(t, err)
	attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.NoError(t, err)

	var methods []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		switch r.Method {
		case http.MethodPost:
			writePaymentRefundReconciliationResponse(t, w, r, attempt, unifiedpay.RefundStatusUnknown, false)
		case http.MethodGet:
			require.Equal(t, "/v1/refund-requests/cccccccc-cccc-4ccc-8ccc-cccccccccccc", r.URL.Path)
			writePaymentRefundReconciliationResponse(t, w, r, attempt, unifiedpay.RefundStatusSucceeded, false)
		default:
			t.Fatalf("unexpected request method %s", r.Method)
		}
	}))
	defer provider.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, provider.URL), nil)

	store := &paymentRefundReconciliationStoreStub{batches: [][]PaymentRefundReconciliationCandidate{
		{{ProductRefundNo: attempt.ProductRefundNo, OrderID: order.ID, Attempts: 0}},
		{{ProductRefundNo: attempt.ProductRefundNo, OrderID: order.ID, Attempts: 1}},
	}}
	worker := NewPaymentRefundReconciliationService(svc, store)
	t.Cleanup(worker.Stop)
	require.NoError(t, worker.RunOnce(ctx))

	pending, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedRefundPending, pending.Status)
	require.True(t, pending.EntitlementReserved, "unknown provider state must never release the local hold")
	require.Len(t, store.retried, 1)
	require.Equal(t, "provider confirmation pending", store.retried[0].lastError)
	require.True(t, store.retried[0].availableAt.After(time.Now().UTC().Add(-time.Second)))

	require.NoError(t, worker.RunOnce(ctx))
	finalized, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedpay.RefundStatusSucceeded, finalized.Status)
	require.False(t, finalized.EntitlementReserved)
	require.Equal(t, []string{http.MethodPost, http.MethodGet}, methods,
		"the second pass queries the persisted remote request instead of creating another refund")
}

func TestPaymentRefundRollbackReadinessFailsClosed(t *testing.T) {
	store := &paymentRefundReconciliationStoreStub{stats: PaymentRefundReconciliationStats{
		EntitlementReservedReviewedPending: 2,
	}}
	worker := NewPaymentRefundReconciliationService(nil, store)
	readiness, err := worker.RefundRollbackReadiness(context.Background())
	require.NoError(t, err)
	require.False(t, readiness.Ready)
	require.EqualValues(t, 2, readiness.EntitlementReservedReviewedPendingCount)

	store.stats = PaymentRefundReconciliationStats{}
	readiness, err = worker.RefundRollbackReadiness(context.Background())
	require.NoError(t, err)
	require.True(t, readiness.Ready)
	require.Zero(t, readiness.EntitlementReservedReviewedPendingCount)

	store.statsErr = context.DeadlineExceeded
	readiness, err = worker.RefundRollbackReadiness(context.Background())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, readiness.Ready)
	require.EqualValues(t, -1, readiness.EntitlementReservedReviewedPendingCount)
}

func TestPaymentRefundReconciliationRetryDelayIsBounded(t *testing.T) {
	require.LessOrEqual(t, paymentRefundReconciliationBatchSize, paymentRefundReconciliationConcurrency,
		"a reconciliation lease must not be claimed before a worker can start it")
	require.Equal(t, paymentRefundReconciliationInitialBackoff, paymentRefundReconciliationRetryDelay(0))
	require.Equal(t, paymentRefundReconciliationInitialBackoff, paymentRefundReconciliationRetryDelay(1))
	for _, attempt := range []int{2, 3, 8, 100} {
		require.LessOrEqual(t, paymentRefundReconciliationRetryDelay(attempt), paymentRefundReconciliationMaximumBackoff)
	}
}

func writePaymentRefundReconciliationResponse(t *testing.T, w http.ResponseWriter, request *http.Request, attempt *unifiedRefundAttempt, status string, manual bool) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	response := map[string]any{
		"environment":           attempt.Environment,
		"organization_id":       attempt.OrganizationID,
		"product_id":            attempt.ProductID,
		"refund_request_id":     "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		"payment_order_id":      attempt.PaymentOrderID,
		"product_refund_no":     attempt.ProductRefundNo,
		"channel_out_refund_no": "sandbox_sub2_refund_001",
		"amount_fen":            attempt.AmountFen,
		"currency":              payment.DefaultPaymentCurrency,
		"payment_method":        attempt.PaymentMethod,
		"status":                status,
		"needs_manual_review":   manual,
		"created_at":            now,
		"updated_at":            now,
	}
	if status == unifiedpay.RefundStatusSucceeded || status == unifiedpay.RefundStatusFailed {
		response["completed_at"] = now
	}
	w.Header().Set("Content-Type", "application/json")
	if request.Method == http.MethodPost {
		w.WriteHeader(http.StatusAccepted)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	require.NoError(t, json.NewEncoder(w).Encode(response))
}

func decimalRequire(value string) decimal.Decimal {
	return decimal.RequireFromString(value)
}
