package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/google/uuid"
)

type SubscriptionCacheInvalidationEvent struct {
	ID        int64
	UserID    int64
	GroupID   int64
	Attempts  int
	Stage     int
	CreatedAt time.Time
}

type SubscriptionCacheInvalidationOutboxRepository interface {
	Claim(ctx context.Context, workerID string, limit int, lease time.Duration) ([]SubscriptionCacheInvalidationEvent, error)
	DeleteClaimed(ctx context.Context, id int64, workerID string) error
	ScheduleSecondPass(ctx context.Context, id int64, workerID string, availableAt time.Time) error
	RetryClaimed(ctx context.Context, id int64, workerID string, availableAt time.Time, lastError string) error
	Stats(ctx context.Context) (AuthCacheInvalidationOutboxStats, error)
}

type subscriptionAuthorizationCacheInvalidator interface {
	EnsureSubscriptionAuthorizationCachesInvalidated(ctx context.Context, userID, groupID int64) error
}

// SubscriptionCacheInvalidationWorker consumes a dedicated outbox instead of
// extending auth_cache_invalidation_outbox. Keeping the schemas separate lets
// old binaries continue processing API-key invalidations during rolling deploys
// and after rollback while subscription invalidations wait for a compatible
// worker.
type SubscriptionCacheInvalidationWorker struct {
	repo        SubscriptionCacheInvalidationOutboxRepository
	invalidator subscriptionAuthorizationCacheInvalidator
	workerID    string
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	start       sync.Once
	stop        sync.Once
	running     atomic.Bool
	processed   atomic.Uint64
	failures    atomic.Uint64
	lastError   atomic.Value
}

func NewSubscriptionCacheInvalidationWorker(
	repo SubscriptionCacheInvalidationOutboxRepository,
	invalidator subscriptionAuthorizationCacheInvalidator,
) *SubscriptionCacheInvalidationWorker {
	ctx, cancel := context.WithCancel(context.Background())
	worker := &SubscriptionCacheInvalidationWorker{
		repo:        repo,
		invalidator: invalidator,
		workerID:    uuid.NewString(),
		ctx:         ctx,
		cancel:      cancel,
	}
	worker.lastError.Store("")
	return worker
}

func (w *SubscriptionCacheInvalidationWorker) Start() {
	if w == nil || w.repo == nil || w.invalidator == nil {
		return
	}
	w.start.Do(func() {
		w.running.Store(true)
		w.wg.Add(1)
		go w.run()
	})
}

func (w *SubscriptionCacheInvalidationWorker) Stop() {
	if w == nil {
		return
	}
	w.stop.Do(func() {
		w.cancel()
		w.wg.Wait()
		w.running.Store(false)
	})
}

func (w *SubscriptionCacheInvalidationWorker) run() {
	defer w.wg.Done()
	defer w.running.Store(false)
	ticker := time.NewTicker(authInvalidationPollInterval)
	defer ticker.Stop()
	for {
		if err := w.processBatch(w.ctx); err != nil && w.ctx.Err() == nil {
			w.recordFailure(err)
		}
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *SubscriptionCacheInvalidationWorker) processBatch(ctx context.Context) error {
	if !runtimegate.SharedWorkAllowed() {
		return nil
	}
	events, err := w.repo.Claim(ctx, w.workerID, authInvalidationBatchSize, authInvalidationLease)
	if err != nil {
		return fmt.Errorf("claim subscription cache invalidations: %w", err)
	}
	semaphore := make(chan struct{}, authInvalidationConcurrency)
	var wg sync.WaitGroup
	for i := range events {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case semaphore <- struct{}{}:
		}
		wg.Add(1)
		go func(event SubscriptionCacheInvalidationEvent) {
			defer wg.Done()
			defer func() { <-semaphore }()
			w.processEvent(ctx, event)
		}(events[i])
	}
	wg.Wait()
	return nil
}

func (w *SubscriptionCacheInvalidationWorker) processEvent(parent context.Context, event SubscriptionCacheInvalidationEvent) {
	var err error
	if event.UserID <= 0 || event.GroupID <= 0 {
		err = fmt.Errorf("invalid subscription cache target user=%d group=%d", event.UserID, event.GroupID)
	} else {
		ctx, cancel := context.WithTimeout(parent, authInvalidationRedisTimeout)
		err = w.invalidator.EnsureSubscriptionAuthorizationCachesInvalidated(ctx, event.UserID, event.GroupID)
		cancel()
	}
	if err != nil {
		w.recordFailure(err)
		retryAt := time.Now().UTC().Add(authInvalidationRetryDelay(event.Attempts + 1))
		retryCtx, retryCancel := context.WithTimeout(context.Background(), 2*time.Second)
		retryErr := w.repo.RetryClaimed(retryCtx, event.ID, w.workerID, retryAt, boundedAuthInvalidationError(err))
		retryCancel()
		if retryErr != nil {
			w.recordFailure(fmt.Errorf("release failed subscription cache invalidation %d: %w", event.ID, retryErr))
		}
		return
	}

	if event.Stage == 0 {
		nextCtx, nextCancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = w.repo.ScheduleSecondPass(nextCtx, event.ID, w.workerID, time.Now().UTC().Add(authInvalidationSafetyDelay))
		nextCancel()
		if err != nil {
			w.recordFailure(fmt.Errorf("schedule second subscription cache invalidation pass %d: %w", event.ID, err))
			return
		}
		w.processed.Add(1)
		w.lastError.Store("")
		return
	}

	ackCtx, ackCancel := context.WithTimeout(context.Background(), 2*time.Second)
	err = w.repo.DeleteClaimed(ackCtx, event.ID, w.workerID)
	ackCancel()
	if err != nil {
		w.recordFailure(fmt.Errorf("ack subscription cache invalidation %d: %w", event.ID, err))
		return
	}
	w.processed.Add(1)
	w.lastError.Store("")
}

func (w *SubscriptionCacheInvalidationWorker) recordFailure(err error) {
	if err == nil {
		return
	}
	w.failures.Add(1)
	w.lastError.Store(boundedAuthInvalidationError(err))
	slog.Warn("subscription cache invalidation outbox processing failed", "error", err)
}

func (w *SubscriptionCacheInvalidationWorker) Health(ctx context.Context) AuthCacheInvalidationHealth {
	health := AuthCacheInvalidationHealth{
		HealthySLA:  authInvalidationSafetyDelay + 5*time.Second,
		RecoverySLA: 6 * time.Minute,
	}
	if w == nil {
		return health
	}
	health.Running = w.running.Load()
	health.Processed = w.processed.Load()
	health.Failures = w.failures.Load()
	if value := w.lastError.Load(); value != nil {
		health.LastError, _ = value.(string)
	}
	if w.repo == nil {
		return health
	}
	stats, err := w.repo.Stats(ctx)
	if err != nil {
		health.StatsError = boundedAuthInvalidationError(err)
		return health
	}
	health.Pending = stats.Pending
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

func ProvideSubscriptionCacheInvalidationWorker(
	repo SubscriptionCacheInvalidationOutboxRepository,
	subscriptionService *SubscriptionService,
) *SubscriptionCacheInvalidationWorker {
	worker := NewSubscriptionCacheInvalidationWorker(repo, subscriptionService)
	worker.Start()
	return worker
}
