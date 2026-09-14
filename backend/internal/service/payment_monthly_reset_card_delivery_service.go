package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/google/uuid"
)

const (
	paymentMonthlyResetCardDeliveryLeaderLockKey = "payment:monthly-reset-cards:leader"
	paymentMonthlyResetCardDeliveryLeaderLockTTL = 2 * time.Minute
	paymentMonthlyResetCardDeliveryInterval      = time.Minute
	paymentMonthlyResetCardDeliveryBatchSize     = 100
)

type paymentMonthlyResetCardDeliveryCandidate struct {
	ScheduleID     int64
	PaymentOrderID int64
}

// PaymentMonthlyResetCardDeliveryService is a DB-backed periodic issuer. It
// never contacts a payment provider and does not use cache operations inside a
// transaction; every issue is fenced by the schedule/issuance ledger instead.
type PaymentMonthlyResetCardDeliveryService struct {
	entClient     *dbent.Client
	configService *PaymentConfigService
	interval      time.Duration
	lockCache     LeaderLockCache
	db            *sql.DB
	instanceID    string
	now           func() time.Time
	ctx           context.Context
	cancel        context.CancelFunc
	stopCh        chan struct{}
	startOnce     sync.Once
	stopOnce      sync.Once
	wg            sync.WaitGroup
}

func NewPaymentMonthlyResetCardDeliveryService(entClient *dbent.Client, configService *PaymentConfigService, interval time.Duration) *PaymentMonthlyResetCardDeliveryService {
	workerCtx, cancel := context.WithCancel(context.Background())
	return &PaymentMonthlyResetCardDeliveryService{
		entClient:     entClient,
		configService: configService,
		interval:      interval,
		instanceID:    uuid.NewString(),
		now:           time.Now,
		ctx:           workerCtx,
		cancel:        cancel,
		stopCh:        make(chan struct{}),
	}
}

func (s *PaymentMonthlyResetCardDeliveryService) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache = lockCache
	s.db = db
}

func (s *PaymentMonthlyResetCardDeliveryService) Start() {
	if s == nil || s.entClient == nil || s.configService == nil || s.interval <= 0 {
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(s.interval)
			defer ticker.Stop()
			_ = s.RunOnce(s.ctx)
			for {
				select {
				case <-s.stopCh:
					return
				case <-ticker.C:
					_ = s.RunOnce(s.ctx)
				}
			}
		}()
	})
}

func (s *PaymentMonthlyResetCardDeliveryService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.stopCh != nil {
			close(s.stopCh)
		}
	})
	s.wg.Wait()
}

// RunOnce is exported for focused operational and integration tests. Local
// standby mode or a peer lease yields a harmless no-op. The private monthly
// rollout switch gates new plan/order admission only: a paid schedule must keep
// running after an operator closes that gate.
func (s *PaymentMonthlyResetCardDeliveryService) RunOnce(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.entClient == nil || s.configService == nil || !runtimegate.SharedWorkAllowed() {
		return nil
	}
	jobCtx, cancel := context.WithTimeout(ctx, paymentMonthlyResetCardDeliveryLeaderLockTTL)
	defer cancel()
	leaseCtx, release, acquired := tryAcquireSingletonLeaderLock(
		jobCtx,
		s.lockCache,
		s.db,
		paymentMonthlyResetCardDeliveryLeaderLockKey,
		s.instanceID,
		paymentMonthlyResetCardDeliveryLeaderLockTTL,
	)
	if !acquired {
		return nil
	}
	defer release()

	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	candidates, err := s.dueCandidates(leaseCtx, now)
	if err != nil {
		return err
	}
	var result error
	for _, candidate := range candidates {
		if err := leaseCtx.Err(); err != nil {
			return errors.Join(result, err)
		}
		if err := s.processCandidate(leaseCtx, candidate, now); err != nil {
			result = errors.Join(result, err)
			slog.Warn("monthly reset-card delivery candidate failed", "scheduleID", candidate.ScheduleID, "orderID", candidate.PaymentOrderID, "error", err)
		}
	}
	return result
}

func (s *PaymentMonthlyResetCardDeliveryService) dueCandidates(ctx context.Context, now time.Time) ([]paymentMonthlyResetCardDeliveryCandidate, error) {
	rows, err := s.entClient.QueryContext(ctx, `SELECT id, payment_order_id
		FROM subscription_reset_card_schedules
		WHERE status = 'active' AND next_due_at <= $1
		ORDER BY next_due_at ASC, id ASC
		LIMIT $2`, now.UTC(), paymentMonthlyResetCardDeliveryBatchSize)
	if err != nil {
		return nil, fmt.Errorf("list due monthly reset-card schedules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	candidates := make([]paymentMonthlyResetCardDeliveryCandidate, 0)
	for rows.Next() {
		var candidate paymentMonthlyResetCardDeliveryCandidate
		if err := rows.Scan(&candidate.ScheduleID, &candidate.PaymentOrderID); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return candidates, nil
}

type lockedMonthlyResetCardOrder struct {
	ID     int64
	UserID int64
	Status string
}

func loadMonthlyResetCardOrder(ctx context.Context, client *dbent.Client, orderID int64) (*lockedMonthlyResetCardOrder, error) {
	query := `SELECT id, user_id, status FROM payment_orders WHERE id = $1`
	if paymentAuditDialect(client) == "postgres" {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, orderID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	var order lockedMonthlyResetCardOrder
	if err := rows.Scan(&order.ID, &order.UserID, &order.Status); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &order, nil
}

func monthlyResetCardRefundPending(status string) bool {
	switch status {
	case OrderStatusRefundRequested, OrderStatusRefunding, OrderStatusRefundPending:
		return true
	default:
		return false
	}
}

func monthlyResetCardOrderCanIssue(status string) bool {
	switch status {
	case OrderStatusPaid, OrderStatusCompleted, OrderStatusPartiallyRefunded, OrderStatusRefundFailed:
		return true
	default:
		return false
	}
}

// monthlyResetCardOrderTerminatesSchedule deliberately contains only durable
// order terminal states. A paid fulfillment can temporarily move through
// RECHARGING or FAILED after its entitlement transaction commits; those
// states must preserve the due window for a later administrator retry.
func monthlyResetCardOrderTerminatesSchedule(status string) bool {
	switch status {
	case OrderStatusCancelled, OrderStatusExpired, OrderStatusRefunded:
		return true
	default:
		return false
	}
}

func monthlyResetCardRefundReservationExists(ctx context.Context, client *dbent.Client, orderID int64) (bool, error) {
	rows, err := client.QueryContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM unified_payment_refund_attempts
		WHERE order_id = $1 AND entitlement_reserved = TRUE
	)`, orderID)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, errors.New("monthly reset-card refund reservation query returned no row")
	}
	var exists bool
	if err := rows.Scan(&exists); err != nil {
		return false, err
	}
	return exists, rows.Err()
}

func (s *PaymentMonthlyResetCardDeliveryService) processCandidate(ctx context.Context, candidate paymentMonthlyResetCardDeliveryCandidate, now time.Time) error {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin monthly reset-card delivery tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	// Keep the lock order compatible with refund accounting and fulfillment:
	// payment order -> payment_subscription_grant -> user subscription ->
	// schedule -> issuance/grant.
	order, err := loadMonthlyResetCardOrder(txCtx, client, candidate.PaymentOrderID)
	if err != nil {
		return fmt.Errorf("lock monthly reset-card payment order: %w", err)
	}
	if order == nil {
		return nil
	}
	if monthlyResetCardRefundPending(order.Status) {
		// A reservation may be released after a failed refund. Leave the due
		// period untouched so the next safe cycle can resume it.
		return nil
	}

	grant, sub, grantErr := loadPaymentSubscriptionRefundState(txCtx, client, order.ID, true)
	schedule, scheduleErr := loadMonthlyResetCardSchedule(txCtx, client, candidate.ScheduleID, true)
	if scheduleErr != nil {
		return fmt.Errorf("lock monthly reset-card schedule: %w", scheduleErr)
	}
	if schedule == nil || schedule.PaymentOrderID != order.ID {
		return nil
	}
	if grantErr != nil {
		if errors.Is(grantErr, errRefundAccountingMissing) {
			if err := cancelMonthlyResetCardSchedule(txCtx, client, schedule, now); err != nil {
				return err
			}
			return tx.Commit()
		}
		return fmt.Errorf("lock monthly reset-card payment subscription grant: %w", grantErr)
	}
	if order.UserID != grant.UserID {
		if err := cancelMonthlyResetCardSchedule(txCtx, client, schedule, now); err != nil {
			return err
		}
		return tx.Commit()
	}
	if !monthlyResetCardOrderCanIssue(order.Status) {
		if !monthlyResetCardOrderTerminatesSchedule(order.Status) {
			// A recoverable fulfillment or refund-in-progress state is a pause,
			// not a missed issuance. Do not consume the due window or mutate the
			// schedule until the order reaches a state that may issue again.
			return nil
		}
		if err := cancelMonthlyResetCardSchedule(txCtx, client, schedule, now); err != nil {
			return err
		}
		return tx.Commit()
	}
	reserved, err := monthlyResetCardRefundReservationExists(txCtx, client, order.ID)
	if err != nil {
		// A missing or unreadable reservation table must be fail-closed. Do not
		// turn it into a skip or cancellation because a later safe retry may be
		// entitled to issue the current window.
		return fmt.Errorf("check monthly reset-card refund reservation: %w", err)
	}
	if reserved || grant.ReservedSeconds > 0 {
		return nil
	}
	if err := advanceMonthlyResetCardSchedule(txCtx, client, schedule, grant, sub, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit monthly reset-card delivery: %w", err)
	}
	return nil
}
