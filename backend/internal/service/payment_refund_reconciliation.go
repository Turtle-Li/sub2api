package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/google/uuid"
)

const (
	paymentRefundReconciliationConcurrency = 4
	// Claim at most one immediately executable wave. Claiming work that sits
	// behind the semaphore can let its lease expire before the provider call
	// starts, allowing another process to reclaim the same external operation.
	paymentRefundReconciliationBatchSize      = paymentRefundReconciliationConcurrency
	paymentRefundReconciliationPollInterval   = 15 * time.Second
	paymentRefundReconciliationLease          = time.Minute
	paymentRefundReconciliationAttemptTimeout = 20 * time.Second
	paymentRefundReconciliationReleaseTimeout = 3 * time.Second
	paymentRefundReconciliationMaximumBackoff = 10 * time.Minute
	paymentRefundReconciliationInitialBackoff = 5 * time.Second
)

// PaymentRefundReconciliationCandidate is a leased reviewed refund attempt.
// It carries only durable identifiers; provider request bodies are rebuilt by
// the existing UnifiedRefund implementation from its persisted attempt.
type PaymentRefundReconciliationCandidate struct {
	ProductRefundNo string
	OrderID         int64
	Attempts        int
}

// PaymentRefundReconciliationStats is safe operational state. It deliberately
// contains no user data, channel response body, request key, or payment id.
// EntitlementReservedReviewedPending includes every reviewed reservation that
// is still held, including a terminal/manual anomaly: treating only ordinary
// PENDING rows as safe would allow rollback to strand a local entitlement.
type PaymentRefundReconciliationStats struct {
	// Old runtimes cannot fulfill quantity/auto-use promises in reset-card v2 orders.
	UnsettledResetCardPurchaseCount    int64
	EntitlementReservedReviewedPending int64
	AutomaticallyReconciledPending     int64
	OldestCreatedAt                    *time.Time
	MaxAttempts                        int
	LastError                          string
}

// PaymentRefundRollbackReadiness is the narrow release gate contract exposed
// through the monitor-token-only internal endpoint. A negative count means the
// underlying accounting state could not be read and is therefore unsafe.
type PaymentRefundRollbackReadiness struct {
	UnsettledResetCardPurchaseCount         int64 `json:"unsettled_reset_card_purchase_count,omitempty"`
	Ready                                   bool  `json:"ready"`
	EntitlementReservedReviewedPendingCount int64 `json:"entitlement_reserved_reviewed_pending_count"`
}

// PaymentRefundReconciliationHealth is for in-process observability. The
// public monitor endpoint returns only the safe readiness count above.
type PaymentRefundReconciliationHealth struct {
	Running                            bool
	Processed                          uint64
	Failures                           uint64
	EntitlementReservedReviewedPending int64
	AutomaticallyReconciledPending     int64
	OldestLag                          time.Duration
	MaxAttempts                        int
	LastError                          string
	StatsError                         string
}

// PaymentRefundReconciliationStore owns durable multi-instance claims. It
// never changes entitlement balances, subscription terms, order status or
// provider state; those are finalized atomically by PaymentService.
type PaymentRefundReconciliationStore interface {
	ClaimReviewedEntitlementReservations(context.Context, string, int, time.Duration) ([]PaymentRefundReconciliationCandidate, error)
	CompleteClaim(context.Context, string, string, time.Duration) error
	RetryClaim(context.Context, string, string, time.Duration, time.Time, string) error
	Stats(context.Context) (PaymentRefundReconciliationStats, error)
}

// PaymentRefundReconciliationService reconciles only server-reviewed unified
// refunds that still hold a local entitlement reservation. Repeating a claim
// cannot create a second channel refund: PaymentService reuses the durable
// product_refund_no and idempotency key, while its terminal transaction owns
// the exactly-once entitlement capture or release.
type PaymentRefundReconciliationService struct {
	paymentSvc *PaymentService
	store      PaymentRefundReconciliationStore
	workerID   string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	start  sync.Once
	stop   sync.Once

	running   atomic.Bool
	processed atomic.Uint64
	failures  atomic.Uint64
	lastError atomic.Value
}

func NewPaymentRefundReconciliationService(paymentSvc *PaymentService, store PaymentRefundReconciliationStore) *PaymentRefundReconciliationService {
	ctx, cancel := context.WithCancel(context.Background())
	service := &PaymentRefundReconciliationService{
		paymentSvc: paymentSvc,
		store:      store,
		workerID:   uuid.NewString(),
		ctx:        ctx,
		cancel:     cancel,
	}
	service.lastError.Store("")
	return service
}

func ProvidePaymentRefundReconciliationService(paymentSvc *PaymentService, store PaymentRefundReconciliationStore) *PaymentRefundReconciliationService {
	service := NewPaymentRefundReconciliationService(paymentSvc, store)
	service.Start()
	return service
}

func (s *PaymentRefundReconciliationService) Start() {
	if s == nil || s.paymentSvc == nil || s.store == nil {
		return
	}
	s.start.Do(func() {
		s.running.Store(true)
		s.wg.Add(1)
		go s.run()
	})
}

func (s *PaymentRefundReconciliationService) Stop() {
	if s == nil {
		return
	}
	s.stop.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.wg.Wait()
		s.running.Store(false)
	})
}

func (s *PaymentRefundReconciliationService) run() {
	defer s.wg.Done()
	defer s.running.Store(false)
	ticker := time.NewTicker(paymentRefundReconciliationPollInterval)
	defer ticker.Stop()
	for {
		if err := s.RunOnce(s.ctx); err != nil && s.ctx.Err() == nil {
			s.recordFailure(err)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce is intentionally exposed for focused tests and operational probing.
// Reviewed refund recovery may run on a local-standby candidate: it only
// resumes an already-reserved operation using a durable lease and persisted
// provider idempotency key, so it does not compete to create new work.
func (s *PaymentRefundReconciliationService) RunOnce(ctx context.Context) error {
	if s == nil || s.paymentSvc == nil || s.store == nil || !runtimegate.DurableRecoveryWorkAllowed() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	candidates, err := s.store.ClaimReviewedEntitlementReservations(
		ctx, s.workerID, paymentRefundReconciliationBatchSize, paymentRefundReconciliationLease,
	)
	if err != nil {
		return fmt.Errorf("claim reviewed refund reconciliations: %w", err)
	}
	if len(candidates) == 0 {
		return nil
	}

	semaphore := make(chan struct{}, paymentRefundReconciliationConcurrency)
	errs := make(chan error, len(candidates))
	var wg sync.WaitGroup
	for i := range candidates {
		candidate := candidates[i]
		select {
		case <-ctx.Done():
			wg.Wait()
			return errors.Join(ctx.Err(), joinPaymentRefundReconciliationErrors(errs))
		case semaphore <- struct{}{}:
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-semaphore }()
			if err := s.reconcileCandidate(ctx, candidate); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	return joinPaymentRefundReconciliationErrors(errs)
}

func joinPaymentRefundReconciliationErrors(errs <-chan error) error {
	var joined error
	for {
		select {
		case err := <-errs:
			if err != nil {
				joined = errors.Join(joined, err)
			}
		default:
			return joined
		}
	}
}

func (s *PaymentRefundReconciliationService) reconcileCandidate(parent context.Context, candidate PaymentRefundReconciliationCandidate) error {
	if !runtimegate.DurableRecoveryWorkAllowed() {
		return s.completeClaim(candidate.ProductRefundNo)
	}
	attempt, err := loadUnifiedRefundAttempt(parent, s.paymentSvc.entClient, candidate.OrderID, candidate.ProductRefundNo)
	if errors.Is(err, sql.ErrNoRows) {
		return s.completeClaim(candidate.ProductRefundNo)
	}
	if err != nil {
		return s.retryClaim(candidate, err)
	}
	if !isReviewedRefundReconciliationEligible(attempt) {
		return s.completeClaim(candidate.ProductRefundNo)
	}

	requestCtx, cancel := context.WithTimeout(parent, paymentRefundReconciliationAttemptTimeout)
	_, advanceErr := s.paymentSvc.advanceUnifiedRefund(requestCtx, attempt)
	cancel()

	// Always reload after the provider boundary. A concurrent signed webhook may
	// have finalized the same attempt while this worker was waiting for the
	// response; the persisted state, not the response shape, decides the lease.
	refreshed, loadErr := loadUnifiedRefundAttempt(parent, s.paymentSvc.entClient, candidate.OrderID, candidate.ProductRefundNo)
	if errors.Is(loadErr, sql.ErrNoRows) {
		return s.completeClaim(candidate.ProductRefundNo)
	}
	if loadErr != nil {
		if advanceErr != nil {
			return s.retryClaim(candidate, errors.Join(advanceErr, loadErr))
		}
		return s.retryClaim(candidate, loadErr)
	}
	if !isReviewedRefundReconciliationEligible(refreshed) {
		return s.completeClaim(candidate.ProductRefundNo)
	}
	if advanceErr != nil {
		return s.retryClaim(candidate, advanceErr)
	}
	return s.retryClaim(candidate, errPaymentRefundConfirmationPending)
}

func isReviewedRefundReconciliationEligible(attempt *unifiedRefundAttempt) bool {
	if attempt == nil || attempt.Status != unifiedRefundPending || !attempt.EntitlementReserved || attempt.NeedsManualReview {
		return false
	}
	if attempt.QuoteRevision == "" {
		return false
	}
	return attempt.RefundKind == refundReviewKindBalance || attempt.RefundKind == refundReviewKindSubscription
}

var errPaymentRefundConfirmationPending = errors.New("provider confirmation pending")

func (s *PaymentRefundReconciliationService) completeClaim(productRefundNo string) error {
	ctx, cancel := context.WithTimeout(context.Background(), paymentRefundReconciliationReleaseTimeout)
	defer cancel()
	if err := s.store.CompleteClaim(ctx, productRefundNo, s.workerID, paymentRefundReconciliationLease); err != nil {
		return fmt.Errorf("complete reviewed refund reconciliation %s: %w", productRefundNo, err)
	}
	s.processed.Add(1)
	s.lastError.Store("")
	return nil
}

func (s *PaymentRefundReconciliationService) retryClaim(candidate PaymentRefundReconciliationCandidate, cause error) error {
	delay := paymentRefundReconciliationRetryDelay(candidate.Attempts + 1)
	ctx, cancel := context.WithTimeout(context.Background(), paymentRefundReconciliationReleaseTimeout)
	defer cancel()
	if err := s.store.RetryClaim(
		ctx,
		candidate.ProductRefundNo,
		s.workerID,
		paymentRefundReconciliationLease,
		time.Now().UTC().Add(delay),
		boundedPaymentRefundReconciliationError(cause),
	); err != nil {
		return fmt.Errorf("retry reviewed refund reconciliation %s: %w", candidate.ProductRefundNo, err)
	}
	if !errors.Is(cause, errPaymentRefundConfirmationPending) {
		s.recordFailure(cause)
	}
	return nil
}

func paymentRefundReconciliationRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	delay := paymentRefundReconciliationInitialBackoff * time.Duration(1<<(attempt-1))
	if delay > paymentRefundReconciliationMaximumBackoff {
		return paymentRefundReconciliationMaximumBackoff
	}
	return delay
}

func boundedPaymentRefundReconciliationError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 1024 {
		return message[:1024]
	}
	return message
}

func (s *PaymentRefundReconciliationService) recordFailure(err error) {
	if s == nil || err == nil {
		return
	}
	s.failures.Add(1)
	s.lastError.Store(boundedPaymentRefundReconciliationError(err))
	slog.Warn("reviewed payment refund reconciliation failed", "error", err)
}

func (s *PaymentRefundReconciliationService) Health(ctx context.Context) PaymentRefundReconciliationHealth {
	health := PaymentRefundReconciliationHealth{}
	if s == nil {
		return health
	}
	health.Running = s.running.Load()
	health.Processed = s.processed.Load()
	health.Failures = s.failures.Load()
	if value := s.lastError.Load(); value != nil {
		health.LastError, _ = value.(string)
	}
	if s.store == nil {
		health.StatsError = "payment refund reconciliation store unavailable"
		return health
	}
	stats, err := s.store.Stats(ctx)
	if err != nil {
		health.StatsError = boundedPaymentRefundReconciliationError(err)
		return health
	}
	health.EntitlementReservedReviewedPending = stats.EntitlementReservedReviewedPending
	health.AutomaticallyReconciledPending = stats.AutomaticallyReconciledPending
	health.MaxAttempts = stats.MaxAttempts
	if health.LastError == "" {
		health.LastError = stats.LastError
	}
	if stats.OldestCreatedAt != nil {
		health.OldestLag = time.Since(*stats.OldestCreatedAt)
		if health.OldestLag < 0 {
			health.OldestLag = 0
		}
	}
	return health
}

// RefundRollbackReadiness returns a fail-closed release gate. All reviewed
// reservations count, including a terminal provider result whose local
// entitlement release failed and therefore needs manual intervention. New reset
// purchase promises must also settle before an older runtime can take over.
func (s *PaymentRefundReconciliationService) RefundRollbackReadiness(ctx context.Context) (PaymentRefundRollbackReadiness, error) {
	if s == nil || s.store == nil {
		return PaymentRefundRollbackReadiness{EntitlementReservedReviewedPendingCount: -1}, errors.New("payment refund reconciliation store unavailable")
	}
	stats, err := s.store.Stats(ctx)
	if err != nil {
		return PaymentRefundRollbackReadiness{EntitlementReservedReviewedPendingCount: -1}, err
	}
	return PaymentRefundRollbackReadiness{
		Ready:                                   stats.EntitlementReservedReviewedPending == 0 && stats.UnsettledResetCardPurchaseCount == 0,
		UnsettledResetCardPurchaseCount:         stats.UnsettledResetCardPurchaseCount,
		EntitlementReservedReviewedPendingCount: stats.EntitlementReservedReviewedPending,
	}, nil
}
