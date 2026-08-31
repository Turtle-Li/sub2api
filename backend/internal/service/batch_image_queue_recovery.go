package service

import (
	"context"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"go.uber.org/zap"
)

const defaultBatchImageQueueRecoveryLimit = 100

// BatchImageQueueRecoveryService reconciles durable provider-submitted jobs
// back into Redis when a Redis reset or process crash has lost all queue
// membership. It never submits work to a provider; normal workers resume the
// existing Get/OpenResult/index/settlement flow after a successful repair.
type BatchImageQueueRecoveryService struct {
	Repo  BatchImageQueueRecoveryRepository
	Queue BatchImageQueueEnsurer
	Limit int

	cursorMu sync.Mutex
	cursorID int64
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
	}

	jobs, err := s.Repo.ListProviderSubmittedBatchImageJobsForQueueRecovery(ctx, s.recoveryCursor(), limit)
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

		restored, err := s.Queue.EnsureEnqueued(ctx, job.BatchID)
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
	s.advanceRecoveryCursor(jobs, limit)
	return recovered, lastErr
}

func (s *BatchImageQueueRecoveryService) recoveryCursor() int64 {
	s.cursorMu.Lock()
	defer s.cursorMu.Unlock()
	return s.cursorID
}

func (s *BatchImageQueueRecoveryService) advanceRecoveryCursor(jobs []*BatchImageJob, limit int) {
	nextID := int64(0)
	if len(jobs) >= limit {
		for _, job := range jobs {
			if job != nil && job.ID > nextID {
				nextID = job.ID
			}
		}
	}
	s.cursorMu.Lock()
	s.cursorID = nextID
	s.cursorMu.Unlock()
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
