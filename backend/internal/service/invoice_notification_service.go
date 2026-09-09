package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/google/uuid"
)

const (
	invoiceNotificationRecoveryInterval = time.Minute
	invoiceNotificationRecoveryTimeout  = 35 * time.Second
	invoiceNotificationRecoveryLease    = time.Minute
	invoiceNotificationRecoveryLockKey  = "invoice:notification:leader"
)

// InvoiceNotificationService owns only asynchronous invoice delivery recovery.
// It is deliberately separate from the payment expiry/fulfillment lifecycle:
// SMTP or Feishu latency must never delay payment reconciliation, expiry, or
// entitlement recovery.
type InvoiceNotificationService struct {
	paymentSvc *PaymentService
	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
	interval   time.Duration

	stopCh    chan struct{}
	stopOnce  sync.Once
	startOnce sync.Once
	wg        sync.WaitGroup
	runCtx    context.Context
	runCancel context.CancelFunc
}

func NewInvoiceNotificationService(paymentSvc *PaymentService, lockCache LeaderLockCache, db *sql.DB) *InvoiceNotificationService {
	runCtx, runCancel := context.WithCancel(context.Background())
	return &InvoiceNotificationService{
		paymentSvc: paymentSvc,
		lockCache:  lockCache,
		db:         db,
		instanceID: uuid.NewString(),
		interval:   invoiceNotificationRecoveryInterval,
		stopCh:     make(chan struct{}),
		runCtx:     runCtx,
		runCancel:  runCancel,
	}
}

func (s *InvoiceNotificationService) Start() {
	if s == nil || s.paymentSvc == nil || s.interval <= 0 {
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(s.interval)
			defer ticker.Stop()
			s.runScheduledPass()
			for {
				select {
				case <-ticker.C:
					s.runScheduledPass()
				case <-s.stopCh:
					return
				}
			}
		}()
	})
}

func (s *InvoiceNotificationService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.runCancel != nil {
			s.runCancel()
		}
		close(s.stopCh)
	})
	s.wg.Wait()
}

func (s *InvoiceNotificationService) runScheduledPass() {
	parent := s.runCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, invoiceNotificationRecoveryTimeout)
	defer cancel()
	if err := s.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		slog.Warn("invoice notification recovery pass incomplete", "error_kind", invoiceDeliveryErrorKind(err))
	}
}

// RunOnce keeps automatic retries in the deployment's active generation and
// under a shared leader lease. It is safe to call directly in focused tests.
func (s *InvoiceNotificationService) RunOnce(ctx context.Context) error {
	if s == nil || s.paymentSvc == nil || !runtimegate.SharedWorkAllowed() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	leaseCtx, release, acquired := tryAcquireSingletonLeaderLock(
		ctx,
		s.lockCache,
		s.db,
		invoiceNotificationRecoveryLockKey,
		s.instanceID,
		invoiceNotificationRecoveryLease,
	)
	if !acquired {
		return nil
	}
	defer release()
	if !runtimegate.SharedWorkAllowed() {
		return nil
	}
	_, err := s.paymentSvc.RecoverInvoiceNotifications(leaseCtx)
	return err
}
