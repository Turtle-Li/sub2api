package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"go.uber.org/zap"
)

const (
	defaultBatchImageQueueRecoveryLimit   = 100
	maxBatchImageQueueRecoveryLimit       = 1000
	defaultBatchImageQueueRecoveryLockTTL = 5 * time.Minute
)

// BatchImageQueueRecoveryService reconciles durable provider-submitted jobs
// back into Redis when a Redis reset or process crash has lost all queue
// membership. It never submits work to a provider; normal workers resume the
// existing Get/OpenResult/index/settlement flow after a successful repair.
type BatchImageQueueRecoveryService struct {
	Repo    BatchImageQueueRecoveryRepository
	Queue   BatchImageQueueEnsurer
	Limit   int
	LockTTL time.Duration

	cursorMu    sync.Mutex
	cursorID    int64
	passUpperID int64
}

// ReconcileProviderSubmittedOnce scans only provider-submitted nonterminal
// work, then asks Redis to atomically ensure each job is reachable. A standby
// generation must not scan or write shared queue state.
func (s *BatchImageQueueRecoveryService) ReconcileProviderSubmittedOnce(ctx context.Context) (int, error) {
	if s == nil || s.Repo == nil || s.Queue == nil || !runtimegate.SharedWorkAllowed() {
		return 0, nil
	}
	limit := s.Limit
	if limit <= 0 {
		limit = defaultBatchImageQueueRecoveryLimit
	} else if limit > maxBatchImageQueueRecoveryLimit {
		limit = maxBatchImageQueueRecoveryLimit
	}

	afterID, throughID, err := s.recoveryWindow(ctx)
	if err != nil || throughID == 0 {
		return 0, err
	}
	jobs, err := s.Repo.ListProviderSubmittedBatchImageJobsForQueueRecovery(ctx, afterID, throughID, limit)
	if err != nil {
		return 0, err
	}

	recovered := 0
	var lastErr error
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return recovered, err
		}
		if !isProviderSubmittedQueueRecoveryEligible(job) {
			continue
		}

		restored, err := s.ensureEligibleEnqueued(ctx, job.BatchID)
		if err != nil {
			lastErr = err
			logger.L().Warn("batch_image.queue_recovery_ensure_failed",
				zap.String("batch_id", job.BatchID),
				zap.Error(err),
			)
			continue
		}
		if !restored {
			continue
		}

		recovered++
		// The event intentionally omits provider identifiers, user content, and
		// raw queue state. The job relation already supplies audit correlation.
		if err := s.Repo.AppendBatchImageEvent(ctx, job.BatchID, "provider_queue_recovered", map[string]any{
			"source": "provider_submitted_reconciliation",
		}); err != nil {
			logger.L().Warn("batch_image.queue_recovery_event_failed",
				zap.String("batch_id", job.BatchID),
				zap.Error(err),
			)
		}
	}
	s.advanceRecoveryCursor(jobs, afterID, throughID, limit)
	return recovered, lastErr
}

// ensureEligibleEnqueued takes the worker's per-job lock before performing a
// fresh durable eligibility read and the Redis repair. A worker holds this same
// lock through provider processing, terminal persistence, and Ack, so a row
// selected just before completion cannot be reintroduced after that Ack.
func (s *BatchImageQueueRecoveryService) ensureEligibleEnqueued(ctx context.Context, batchID string) (restored bool, err error) {
	lockTTL := s.LockTTL
	if lockTTL <= 0 {
		lockTTL = defaultBatchImageQueueRecoveryLockTTL
	}
	lock, acquired, err := s.Queue.TryAcquireJobLock(ctx, batchID, lockTTL)
	if err != nil || !acquired {
		return false, err
	}
	if lock == nil {
		return false, fmt.Errorf("batch image queue recovery acquired a nil job lock")
	}
	defer func() {
		if releaseErr := lock.Release(context.WithoutCancel(ctx)); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()

	eligible, err := s.Repo.IsProviderSubmittedBatchImageJobQueueRecoveryEligible(ctx, batchID)
	if err != nil || !eligible {
		return false, err
	}
	return s.Queue.EnsureEnqueued(ctx, batchID)
}

// recoveryWindow fixes an upper bound for each complete pass. Without that
// snapshot, a continuously full table can keep moving the tail forever and a
// row whose Redis repair failed once would never be visited again.
func (s *BatchImageQueueRecoveryService) recoveryWindow(ctx context.Context) (afterID, throughID int64, err error) {
	s.cursorMu.Lock()
	afterID = s.cursorID
	throughID = s.passUpperID
	s.cursorMu.Unlock()
	if throughID > 0 {
		return afterID, throughID, nil
	}

	throughID, err = s.Repo.MaxProviderSubmittedBatchImageJobIDForQueueRecovery(ctx)
	if err != nil || throughID <= 0 {
		return 0, 0, err
	}

	s.cursorMu.Lock()
	defer s.cursorMu.Unlock()
	// Runtime invokes reconciliation serially, but preserve a coherent window
	// if a diagnostic caller overlaps two calls.
	if s.passUpperID == 0 {
		s.cursorID = 0
		s.passUpperID = throughID
	}
	return s.cursorID, s.passUpperID, nil
}

func (s *BatchImageQueueRecoveryService) advanceRecoveryCursor(jobs []*BatchImageJob, afterID, throughID int64, limit int) {
	nextID := int64(0)
	for _, job := range jobs {
		if job != nil && job.ID > nextID {
			nextID = job.ID
		}
	}
	s.cursorMu.Lock()
	defer s.cursorMu.Unlock()
	if s.passUpperID != throughID || s.cursorID != afterID {
		return
	}
	if len(jobs) >= limit && nextID > afterID && nextID < throughID {
		s.cursorID = nextID
		return
	}
	// Reaching the fixed upper bound, or receiving a short/invalid page, ends
	// the pass. The next reconciliation starts again at zero with a fresh upper
	// bound, so every transient EnsureEnqueued failure is revisited.
	s.cursorID = 0
	s.passUpperID = 0
}

func isProviderSubmittedQueueRecoveryEligible(job *BatchImageJob) bool {
	if job == nil || strings.TrimSpace(batchImageDerefString(job.ProviderJobName)) == "" {
		return false
	}
	switch job.Status {
	case BatchImageJobStatusSubmitted, BatchImageJobStatusRunning, BatchImageJobStatusIndexing, BatchImageJobStatusSettling:
		return true
	default:
		return false
	}
}
