package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/google/uuid"
)

const (
	feishuPaymentIncidentScanLimit       = 64
	feishuPaymentIncidentRecheckLimit    = 64
	feishuPaymentIncidentDeliveryLimit   = 4
	feishuPaymentIncidentInterval        = time.Minute
	feishuPaymentIncidentRunTimeout      = 40 * time.Second
	feishuPaymentIncidentLeaderLease     = time.Minute
	feishuPaymentIncidentDeliveryLease   = 30 * time.Second
	feishuPaymentIncidentDeliveryTimeout = 5 * time.Second
	feishuPaymentIncidentGateInterval    = 250 * time.Millisecond
	feishuPaymentIncidentPaidAge         = 10 * time.Minute
)

const feishuPaymentIncidentLeaderLockKey = "feishu:payment-incidents:leader"

// ErrFeishuPaymentDeliveryLeaseLost fences a sender that no longer owns the
// durable delivery. Callers must leave the delivery for its successor rather
// than trying to overwrite the newer claim.
var ErrFeishuPaymentDeliveryLeaseLost = errors.New("feishu payment delivery lease lost")

type FeishuPaymentIncidentKind string

const (
	FeishuPaymentIncidentRefundReview   FeishuPaymentIncidentKind = "REFUND_REVIEW"
	FeishuPaymentIncidentPaidIncomplete FeishuPaymentIncidentKind = "PAID_INCOMPLETE"
	FeishuPaymentIncidentTest           FeishuPaymentIncidentKind = "TEST_NOTIFICATION"
)

type FeishuPaymentDeliveryKind string

const (
	FeishuPaymentDeliveryOpen     FeishuPaymentDeliveryKind = "OPEN"
	FeishuPaymentDeliveryReminder FeishuPaymentDeliveryKind = "REMINDER"
	FeishuPaymentDeliveryResolved FeishuPaymentDeliveryKind = "RESOLVED"
	FeishuPaymentDeliveryTest     FeishuPaymentDeliveryKind = "TEST"
)

const (
	feishuPaymentDeliveryStatusPending    = "PENDING"
	feishuPaymentDeliveryStatusClaimed    = "CLAIMED"
	feishuPaymentDeliveryStatusDelivered  = "DELIVERED"
	feishuPaymentDeliveryStatusSuppressed = "SUPPRESSED"
)

// FeishuPaymentIncident is the minimal, non-sensitive incident view used by
// the lifecycle. It deliberately contains no customer identifiers, callback
// bodies, credentials, or provider response data.
type FeishuPaymentIncident struct {
	ID             string
	Key            string
	Kind           FeishuPaymentIncidentKind
	SubjectOrderID int64
	Generation     int
	Status         string
}

// FeishuPaymentDelivery is a claimed, ordered delivery. ClaimToken is a
// per-claim fencing token and must be supplied to each terminal transition.
type FeishuPaymentDelivery struct {
	ID             string
	IncidentID     string
	IncidentKey    string
	IncidentKind   FeishuPaymentIncidentKind
	SubjectOrderID int64
	Generation     int
	OpenedAt       time.Time
	Kind           FeishuPaymentDeliveryKind
	Sequence       int64
	Attempts       int
	ClaimToken     string
}

type FeishuPaymentRefundFenceState struct {
	Exists         bool
	Fenced         bool
	RefundTerminal bool
}

type FeishuPaymentOrderState struct {
	Exists bool
	Status string
	PaidAt *time.Time
}

// FeishuPaymentIncidentStore is intentionally separate from business outbox
// and payment-state writers. It only reads payment truth and writes its own
// incident/delivery tables.
type FeishuPaymentIncidentStore interface {
	ListRefundFenceCandidates(ctx context.Context, limit int) ([]int64, error)
	ListPaidIncompleteCandidates(ctx context.Context, before time.Time, limit int) ([]int64, error)
	LoadRefundFenceState(ctx context.Context, orderID int64) (FeishuPaymentRefundFenceState, error)
	LoadPaymentOrderState(ctx context.Context, orderID int64) (FeishuPaymentOrderState, error)
	Observe(ctx context.Context, kind FeishuPaymentIncidentKind, subjectOrderID int64, now time.Time) error
	TouchOpen(ctx context.Context, incidentID string, now time.Time) error
	ListOpenForRecheck(ctx context.Context, limit int) ([]FeishuPaymentIncident, error)
	Resolve(ctx context.Context, incidentID string, now time.Time) error
	ClaimDue(ctx context.Context, limit int, lease time.Duration) ([]FeishuPaymentDelivery, error)
	ClaimExact(ctx context.Context, deliveryID string, lease time.Duration) (*FeishuPaymentDelivery, error)
	CanSend(ctx context.Context, deliveryID, claimToken string) (bool, error)
	MarkDelivered(ctx context.Context, deliveryID, claimToken string) error
	MarkFailed(ctx context.Context, deliveryID, claimToken string, backoff time.Duration, errorCode string) error
	EnqueueTest(ctx context.Context, now time.Time) (string, error)
	DeliveryStatus(ctx context.Context, deliveryID string) (string, error)
}

// FeishuPaymentIncidentSender is the narrow outbound boundary. Implementations
// must not expose webhook URLs or response bodies in their errors.
type FeishuPaymentIncidentSender interface {
	Send(ctx context.Context, delivery FeishuPaymentDelivery) error
}

// FeishuPaymentIncidentService independently discovers payment incidents and
// drains durable deliveries. It never joins payment fulfillment or expiry
// execution, so a notification outage cannot block financial workers.
type FeishuPaymentIncidentService struct {
	store  FeishuPaymentIncidentStore
	sender FeishuPaymentIncidentSender
	cfg    *config.Config

	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
	interval   time.Duration
	now        func() time.Time

	stopCh    chan struct{}
	stopOnce  sync.Once
	startOnce sync.Once
	wg        sync.WaitGroup
	runCtx    context.Context
	runCancel context.CancelFunc
}

func NewFeishuPaymentIncidentService(
	store FeishuPaymentIncidentStore,
	sender FeishuPaymentIncidentSender,
	cfg *config.Config,
	lockCache LeaderLockCache,
	db *sql.DB,
) *FeishuPaymentIncidentService {
	runCtx, runCancel := context.WithCancel(context.Background())
	return &FeishuPaymentIncidentService{
		store:      store,
		sender:     sender,
		cfg:        cfg,
		lockCache:  lockCache,
		db:         db,
		instanceID: uuid.NewString(),
		interval:   feishuPaymentIncidentInterval,
		now: func() time.Time {
			return time.Now().UTC().Truncate(time.Microsecond)
		},
		stopCh:    make(chan struct{}),
		runCtx:    runCtx,
		runCancel: runCancel,
	}
}

func (s *FeishuPaymentIncidentService) enabled() bool {
	return s != nil && s.cfg != nil && s.cfg.FeishuPaymentAlerts.Enabled && s.store != nil && s.sender != nil
}

func (s *FeishuPaymentIncidentService) Start() {
	if !s.enabled() || s.interval <= 0 {
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

func (s *FeishuPaymentIncidentService) Stop() {
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

func (s *FeishuPaymentIncidentService) runScheduledPass() {
	parent := s.runCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, feishuPaymentIncidentRunTimeout)
	defer cancel()
	if err := s.RunOnce(ctx); err != nil {
		// Store and sender errors deliberately stay generic. In particular, never
		// interpolate a webhook URL or provider response into the process log.
		slog.Warn("feishu payment incident pass incomplete", "error", err)
	}
}

// RunOnce is the normal, runtime-gated background pass. The existing shared
// work gate preserves the active-owner/standby deployment boundary before a
// lease is acquired, and each outbound send rechecks it before transport.
func (s *FeishuPaymentIncidentService) RunOnce(ctx context.Context) error {
	if !s.enabled() {
		return nil
	}
	return s.withBackgroundLease(ctx, func(leaseCtx context.Context) error {
		now := s.currentTime()
		discoverErr := s.discover(leaseCtx, now)
		// Discovery failure must not strand already durable alerts. Delivery is
		// independently attempted in the same bounded owner lease.
		deliveryErr := s.deliverDue(leaseCtx, false)
		return errors.Join(discoverErr, deliveryErr)
	})
}

func (s *FeishuPaymentIncidentService) withBackgroundLease(ctx context.Context, fn func(context.Context) error) error {
	if s == nil || fn == nil || !s.enabled() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	leaseCtx, release, acquired := tryAcquireSingletonLeaderLock(
		ctx,
		s.lockCache,
		s.db,
		feishuPaymentIncidentLeaderLockKey,
		s.instanceID,
		feishuPaymentIncidentLeaderLease,
	)
	if !acquired {
		return nil
	}
	defer release()
	// The leader helper fences lease loss. The deployment-owned state file can
	// also change while this bounded pass is scanning, so propagate that loss to
	// every DB call and any in-flight HTTP request rather than waiting for the
	// next scheduled pass to notice standby.
	gatedCtx, cancel := context.WithCancel(leaseCtx)
	stopGateWatch := make(chan struct{})
	gateWatchDone := make(chan struct{})
	go func() {
		defer close(gateWatchDone)
		ticker := time.NewTicker(feishuPaymentIncidentGateInterval)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				cancel()
				return
			case <-stopGateWatch:
				return
			case <-ticker.C:
				if !runtimegate.SharedWorkAllowed() {
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		close(stopGateWatch)
		cancel()
		<-gateWatchDone
	}()
	err := fn(gatedCtx)
	if gatedCtx.Err() != nil {
		return nil
	}
	return err
}

func (s *FeishuPaymentIncidentService) discover(ctx context.Context, now time.Time) error {
	var failures []error

	refundCandidates, err := s.store.ListRefundFenceCandidates(ctx, feishuPaymentIncidentScanLimit)
	if err != nil {
		failures = append(failures, fmt.Errorf("list refund review candidates: %w", err))
	} else {
		for _, orderID := range refundCandidates {
			if err := feishuPaymentIncidentPassContext(ctx); err != nil {
				return err
			}
			if err := s.observeRefundFence(ctx, orderID, now); err != nil {
				failures = append(failures, fmt.Errorf("observe refund review candidate: %w", err))
			}
		}
	}

	paidCandidates, err := s.store.ListPaidIncompleteCandidates(ctx, now.Add(-feishuPaymentIncidentPaidAge), feishuPaymentIncidentScanLimit)
	if err != nil {
		failures = append(failures, fmt.Errorf("list paid incomplete candidates: %w", err))
	} else {
		for _, orderID := range paidCandidates {
			if err := feishuPaymentIncidentPassContext(ctx); err != nil {
				return err
			}
			if err := s.observePaidIncomplete(ctx, orderID, now); err != nil {
				failures = append(failures, fmt.Errorf("observe paid incomplete candidate: %w", err))
			}
		}
	}

	open, err := s.store.ListOpenForRecheck(ctx, feishuPaymentIncidentRecheckLimit)
	if err != nil {
		failures = append(failures, fmt.Errorf("list open payment incidents: %w", err))
	} else {
		for _, incident := range open {
			if err := feishuPaymentIncidentPassContext(ctx); err != nil {
				return err
			}
			if err := s.recheckOpen(ctx, incident, now); err != nil {
				failures = append(failures, fmt.Errorf("recheck payment incident: %w", err))
			}
		}
	}

	return errors.Join(failures...)
}

func feishuPaymentIncidentPassContext(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if !runtimegate.SharedWorkAllowed() {
		return context.Canceled
	}
	return nil
}

func (s *FeishuPaymentIncidentService) observeRefundFence(ctx context.Context, orderID int64, now time.Time) error {
	state, err := s.store.LoadRefundFenceState(ctx, orderID)
	if err != nil {
		return err
	}
	if !state.Exists || !state.Fenced || state.RefundTerminal {
		return nil
	}
	return s.store.Observe(ctx, FeishuPaymentIncidentRefundReview, orderID, now)
}

func (s *FeishuPaymentIncidentService) observePaidIncomplete(ctx context.Context, orderID int64, now time.Time) error {
	state, err := s.store.LoadPaymentOrderState(ctx, orderID)
	if err != nil {
		return err
	}
	if !isFeishuPaidIncomplete(state, now) {
		return nil
	}
	return s.store.Observe(ctx, FeishuPaymentIncidentPaidIncomplete, orderID, now)
}

func (s *FeishuPaymentIncidentService) recheckOpen(ctx context.Context, incident FeishuPaymentIncident, now time.Time) error {
	switch incident.Kind {
	case FeishuPaymentIncidentRefundReview:
		state, err := s.store.LoadRefundFenceState(ctx, incident.SubjectOrderID)
		if err != nil {
			// A failed source read is not evidence that an immutable review fence
			// disappeared. Keep the incident open for a later safe recheck.
			return err
		}
		if state.Exists && state.RefundTerminal {
			return s.store.Resolve(ctx, incident.ID, now)
		}
		// Even if an old mutable flag was cleared or a candidate page did not
		// contain this order, immutable evidence must continue alerting until a
		// definitive refund terminal state is known.
		return s.store.TouchOpen(ctx, incident.ID, now)
	case FeishuPaymentIncidentPaidIncomplete:
		state, err := s.store.LoadPaymentOrderState(ctx, incident.SubjectOrderID)
		if err != nil {
			return err
		}
		if state.Exists && isFeishuKnownPaymentResolution(state.Status) {
			return s.store.Resolve(ctx, incident.ID, now)
		}
		// REFUND_PENDING, candidate omission, and any unknown status deliberately
		// remain OPEN; none proves fulfillment or a definitive refund result.
		return s.store.TouchOpen(ctx, incident.ID, now)
	default:
		return fmt.Errorf("unknown feishu payment incident kind %q", incident.Kind)
	}
}

func isFeishuPaidIncomplete(state FeishuPaymentOrderState, now time.Time) bool {
	if !state.Exists || state.PaidAt == nil || state.PaidAt.After(now.Add(-feishuPaymentIncidentPaidAge)) {
		return false
	}
	switch state.Status {
	case OrderStatusPaid, OrderStatusFailed, OrderStatusRecharging:
		return true
	default:
		return false
	}
}

func isFeishuKnownPaymentResolution(status string) bool {
	switch status {
	case OrderStatusCompleted, OrderStatusRefunded, OrderStatusRefundFailed:
		return true
	default:
		return false
	}
}

func (s *FeishuPaymentIncidentService) deliverDue(ctx context.Context, allowStandbyTest bool) error {
	if !allowStandbyTest && !runtimegate.SharedWorkAllowed() {
		return nil
	}
	deliveries, err := s.store.ClaimDue(ctx, feishuPaymentIncidentDeliveryLimit, feishuPaymentIncidentDeliveryLease)
	if err != nil {
		return fmt.Errorf("claim feishu payment deliveries: %w", err)
	}
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		failures []error
	)
	for _, delivery := range deliveries {
		delivery := delivery
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.deliverClaim(ctx, delivery, allowStandbyTest); err != nil {
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return errors.Join(failures...)
}

func (s *FeishuPaymentIncidentService) deliverClaim(ctx context.Context, delivery FeishuPaymentDelivery, allowStandbyTest bool) error {
	if !allowStandbyTest && !runtimegate.SharedWorkAllowed() {
		return nil
	}
	canSend, err := s.store.CanSend(ctx, delivery.ID, delivery.ClaimToken)
	if err != nil {
		return fmt.Errorf("recheck feishu payment delivery: %w", err)
	}
	if !canSend {
		return nil
	}
	if !allowStandbyTest && !runtimegate.SharedWorkAllowed() {
		return nil
	}
	sendCtx, cancel := context.WithTimeout(ctx, feishuPaymentIncidentDeliveryTimeout)
	err = s.sender.Send(sendCtx, delivery)
	cancel()
	if err != nil {
		backoff := feishuPaymentIncidentBackoff(delivery)
		if markErr := s.store.MarkFailed(ctx, delivery.ID, delivery.ClaimToken, backoff, "feishu_delivery_failed"); markErr != nil {
			return fmt.Errorf("record failed feishu payment delivery: %w", markErr)
		}
		return fmt.Errorf("send feishu payment delivery: %w", err)
	}
	if err := s.store.MarkDelivered(ctx, delivery.ID, delivery.ClaimToken); err != nil {
		return fmt.Errorf("mark feishu payment delivery delivered: %w", err)
	}
	return nil
}

func feishuPaymentIncidentBackoff(delivery FeishuPaymentDelivery) time.Duration {
	// Claim attempts are persisted and never capped. The delay, rather than the
	// retry count, is capped so an outage cannot silently drop an operator alert.
	attempt := delivery.Attempts
	if attempt < 1 {
		attempt = 1
	}
	// 5s * 2^13 is already above the one-hour ceiling. Bounding the exponent
	// also prevents an artificial test value from overflowing time.Duration.
	if attempt > 13 {
		attempt = 13
	}
	delay := 5 * time.Second * time.Duration(1<<(attempt-1))
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

func (s *FeishuPaymentIncidentService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC().Truncate(time.Microsecond)
	}
	return time.Now().UTC().Truncate(time.Microsecond)
}

// SendTestNotification creates a closed synthetic incident and waits until its
// own durable delivery is acknowledged. It intentionally does not run payment
// scans or mutate payment tables. This owner-invoked smoke path is allowed on a
// standby generation; normal background discovery and sends remain gated.
func (s *FeishuPaymentIncidentService) SendTestNotification(ctx context.Context) error {
	if !s.enabled() {
		return errors.New("feishu payment incident notifications are disabled")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deliveryID, err := s.store.EnqueueTest(ctx, s.currentTime())
	if err != nil {
		return fmt.Errorf("enqueue feishu test notification: %w", err)
	}

	for {
		status, err := s.store.DeliveryStatus(ctx, deliveryID)
		if err != nil {
			return fmt.Errorf("read feishu test notification status: %w", err)
		}
		switch status {
		case feishuPaymentDeliveryStatusDelivered:
			return nil
		case feishuPaymentDeliveryStatusSuppressed:
			return errors.New("feishu test notification was suppressed")
		}

		claimed, err := s.store.ClaimExact(ctx, deliveryID, feishuPaymentIncidentDeliveryLease)
		if err != nil {
			return fmt.Errorf("claim feishu test notification: %w", err)
		}
		if claimed != nil {
			if err := s.deliverClaim(ctx, *claimed, true); err != nil {
				if errors.Is(err, ErrFeishuPaymentDeliveryLeaseLost) {
					continue
				}
				return err
			}
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
