package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	// paymentFulfillmentRecoveryRetryDelay bounds automatic retries of a
	// durable failure. The expiry worker runs every minute, so this keeps an
	// order from being retried more often than once per sweep interval.
	paymentFulfillmentRecoveryRetryDelay = time.Minute
	// paymentFulfillmentRecoveryLimit bounds a single leader pass. The oldest
	// candidates are selected first, so a continuously failing order cannot
	// cause an unbounded scan or starve older work behind it.
	paymentFulfillmentRecoveryLimit = 100
	// paymentFulfillmentRecoveryConcurrency bounds entitlement side effects and
	// database load within the periodic worker.
	paymentFulfillmentRecoveryConcurrency = 4
)

var errPaymentFulfillmentRecoveryRefundFenced = errors.New("payment fulfillment recovery is blocked by refund manual review")

// RecoverPendingPaymentOrderFulfillments retries durable paid orders whose
// normal callback-side fulfillment did not complete. It uses
// executeRecoveryFulfillment and the shared balance/subscription executors,
// with a recovery-specific transactional refund-fence-and-lease acquirer.
// This avoids admin retry audit attribution while retaining the existing
// status/version lease CAS.
//
// The caller owns the context deadline. In production this is the bounded
// payment-order expiry job context; a canceled context stops scheduling new
// candidates and is passed through to every in-flight fulfillment.
func (s *PaymentService) RecoverPendingPaymentOrderFulfillments(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s == nil || s.entClient == nil {
		return 0, errors.New("payment fulfillment recovery requires an order store")
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	retryBefore := now.Add(-paymentFulfillmentRecoveryRetryDelay)
	staleLeaseBefore := now.Add(-paymentFulfillmentLeaseDuration)
	orders, err := s.entClient.PaymentOrder.Query().
		Where(
			paymentorder.PaidAtNotNil(),
			paymentFulfillmentRecoveryNoRefundReview(),
			paymentorder.Or(
				paymentorder.And(
					paymentorder.StatusIn(OrderStatusPaid, OrderStatusFailed),
					paymentorder.UpdatedAtLTE(retryBefore),
				),
				paymentorder.And(
					paymentorder.StatusEQ(OrderStatusRecharging),
					paymentorder.UpdatedAtLTE(staleLeaseBefore),
				),
			),
		).
		Order(dbent.Asc(paymentorder.FieldUpdatedAt), dbent.Asc(paymentorder.FieldID)).
		Limit(paymentFulfillmentRecoveryLimit).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("list payment fulfillment recovery candidates: %w", err)
	}

	var (
		wg        sync.WaitGroup
		sem       = make(chan struct{}, paymentFulfillmentRecoveryConcurrency)
		mu        sync.Mutex
		recovered int
		failures  []error
	)
	recordFailure := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		failures = append(failures, err)
		mu.Unlock()
	}

launchLoop:
	for _, order := range orders {
		// Avoid scheduling a new goroutine when cancellation is already known.
		// An in-flight worker always receives the same context.
		if err := ctx.Err(); err != nil {
			recordFailure(err)
			break launchLoop
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			recordFailure(ctx.Err())
			break launchLoop
		}
		// Cancellation can race with the semaphore select above. Return the slot
		// and stop before a new worker is created when it arrived in that window.
		if err := ctx.Err(); err != nil {
			<-sem
			recordFailure(err)
			break launchLoop
		}

		orderID := order.ID
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			if err := s.executeRecoveryFulfillment(ctx, orderID); err != nil {
				if errors.Is(err, errPaymentFulfillmentRecoveryRefundFenced) {
					// The transactional claim path re-read the authoritative fence
					// while holding the same order lock as the refund writer.
					slog.Warn("payment fulfillment recovery skipped order under refund manual review", "orderID", orderID)
					return
				}
				// A concurrent callback, administrator retry, or recovery worker owns
				// the order when the lease CAS reports a conflict. It is not a failed
				// recovery attempt and must not cancel fair processing of other rows.
				if infraerrors.Reason(err) == "CONFLICT" {
					slog.Debug("payment fulfillment recovery lost concurrent lease", "orderID", orderID)
					return
				}
				err = fmt.Errorf("recover payment fulfillment for order %d: %w", orderID, err)
				slog.Warn("payment fulfillment recovery failed", "orderID", orderID, "error", err)
				recordFailure(err)
				return
			}

			mu.Lock()
			recovered++
			mu.Unlock()
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	return recovered, errors.Join(failures...)
}

// acquireRecoveryPaymentFulfillmentLease establishes the automatic-recovery
// linearization boundary. It holds the payment order row lock used by the
// unified-refund writer while it revalidates durable paid eligibility, reads
// the refund manual-review fence, and records the fulfillment lease CAS. The
// transaction commits before any entitlement side effect runs.
func (s *PaymentService) acquireRecoveryPaymentFulfillmentLease(ctx context.Context, candidate *dbent.PaymentOrder) (*dbent.PaymentOrder, *paymentFulfillmentLease, error) {
	if candidate == nil {
		return nil, nil, infraerrors.BadRequest("INVALID_STATUS", "nil payment order")
	}
	if s == nil || s.entClient == nil {
		return nil, nil, errors.New("payment fulfillment recovery requires an order store")
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin payment fulfillment recovery claim: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	txCtx := dbent.NewTxContext(ctx, tx)
	txClient := tx.Client()
	locked, err := lockUnifiedRefundOrder(txCtx, txClient, candidate.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("lock payment fulfillment recovery order %d: %w", candidate.ID, err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !isPaymentFulfillmentRecoveryClaimable(locked, now) {
		return locked, nil, infraerrors.Conflict("CONFLICT", "order is no longer eligible for automatic fulfillment recovery")
	}

	needsReview, err := unifiedRefundOrderNeedsReview(txCtx, txClient, locked.ID)
	if err != nil {
		// The authoritative fence could not be read under the same lock. Roll
		// back and leave the financial order untouched for a later safe retry.
		return nil, nil, fmt.Errorf("read refund review fence for payment order %d: %w", locked.ID, err)
	}
	if needsReview {
		return locked, nil, errPaymentFulfillmentRecoveryRefundFenced
	}

	lease, err := acquirePaymentFulfillmentLeaseWithClient(txCtx, txClient, locked)
	if err != nil {
		return nil, nil, err
	}
	if lease == nil {
		return locked, nil, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit payment fulfillment recovery claim: %w", err)
	}
	committed = true
	return locked, lease, nil
}

func isPaymentFulfillmentRecoveryClaimable(order *dbent.PaymentOrder, now time.Time) bool {
	if order == nil || order.PaidAt == nil || psIsRefundStatus(order.Status) {
		return false
	}
	switch order.Status {
	case OrderStatusPaid, OrderStatusFailed:
		return !order.UpdatedAt.After(now.Add(-paymentFulfillmentRecoveryRetryDelay))
	case OrderStatusRecharging:
		return !order.UpdatedAt.After(now.Add(-paymentFulfillmentLeaseDuration))
	default:
		return false
	}
}

// paymentFulfillmentRecoveryNoRefundReview keeps refund-fenced rows out of
// the bounded candidate window. Filtering them after LIMIT would let 100 old
// manual-review rows starve later paid orders forever. The exact tables and
// actions are the durable authority used by unifiedRefundOrderNeedsReview;
// errors from either table consequently fail the entire scan closed.
func paymentFulfillmentRecoveryNoRefundReview() predicate.PaymentOrder {
	return predicate.PaymentOrder(func(s *entsql.Selector) {
		s.Where(entsql.P(func(b *entsql.Builder) {
			b.WriteString(`NOT EXISTS (
				SELECT 1 FROM unified_payment_refund_attempts AS refund_attempt
				WHERE refund_attempt.order_id = `).
				Ident(s.C(paymentorder.FieldID)).
				WriteString(` AND refund_attempt.needs_manual_review = TRUE
			) AND NOT EXISTS (
				SELECT 1 FROM unified_payment_refund_events AS refund_event
				WHERE refund_event.order_id = `).
				Ident(s.C(paymentorder.FieldID)).
				WriteString(` AND refund_event.action IN ('UNIFIED_REFUND_UNCORRELATED', 'UNIFIED_PAYMENT_EVENT_REJECTED')
			)`)
		}))
	})
}
