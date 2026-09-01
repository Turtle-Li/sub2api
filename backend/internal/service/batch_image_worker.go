package service

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"go.uber.org/zap"
)

const (
	defaultBatchImageWorkerLockTTL             = 5 * time.Minute
	defaultBatchImageWorkerLockConflictDelay   = 5 * time.Second
	defaultBatchImageWorkerErrorRetryDelay     = time.Minute
	defaultBatchImageWorkerRequeueDelay        = 30 * time.Second
	defaultBatchImageWorkerDelayedPollInterval = 5 * time.Second
	defaultBatchImageWorkerRecoveryInterval    = 5 * time.Minute
	defaultBatchImageWorkerStaleActiveAfter    = 10 * time.Minute
	defaultBatchImageWorkerDelayedMoveLimit    = 100
	defaultBatchImageWorkerRecoverLimit        = 100
	defaultBatchImageWorkerErrorBackoff        = time.Second
	defaultBatchImageWorkerReserveBlockTimeout = 5 * time.Second
)

type BatchImageProcessor interface {
	Process(ctx context.Context, batchID string) (BatchImageProcessResult, error)
}

type BatchImageProcessResult struct {
	RequeueAfter time.Duration
	Terminal     bool
}

type BatchImageWorkerOptions struct {
	ReserveBlockTimeout time.Duration
	JobLockTTL          time.Duration
	LockConflictDelay   time.Duration
	DefaultRequeueDelay time.Duration
	ErrorRetryDelay     time.Duration
	ErrorBackoff        time.Duration
	DelayedPollInterval time.Duration
	RecoveryInterval    time.Duration
	StaleActiveAfter    time.Duration
	DelayedMoveLimit    int
	RecoverLimit        int
}

type BatchImageWorker struct {
	queue     BatchImageQueue
	processor BatchImageProcessor
	opts      BatchImageWorkerOptions
}

func NewBatchImageWorker(queue BatchImageQueue, processor BatchImageProcessor, opts BatchImageWorkerOptions) *BatchImageWorker {
	return &BatchImageWorker{
		queue:     queue,
		processor: processor,
		opts:      normalizeBatchImageWorkerOptions(opts),
	}
}

func NewBatchImageWorkerOptionsFromConfig(cfg *config.Config) BatchImageWorkerOptions {
	if cfg == nil {
		return normalizeBatchImageWorkerOptions(BatchImageWorkerOptions{})
	}
	return normalizeBatchImageWorkerOptions(BatchImageWorkerOptions{
		JobLockTTL:          time.Duration(cfg.BatchImage.JobLockTTLSeconds) * time.Second,
		LockConflictDelay:   time.Duration(cfg.BatchImage.LockConflictDelaySeconds) * time.Second,
		DefaultRequeueDelay: time.Duration(cfg.BatchImage.DefaultRequeueDelaySeconds) * time.Second,
		ErrorRetryDelay:     time.Duration(cfg.BatchImage.ErrorRetryDelaySeconds) * time.Second,
		DelayedPollInterval: time.Duration(cfg.BatchImage.DelayedMoverIntervalSeconds) * time.Second,
		RecoveryInterval:    time.Duration(cfg.BatchImage.RecoveryIntervalSeconds) * time.Second,
		StaleActiveAfter:    time.Duration(cfg.BatchImage.StaleActiveAfterSeconds) * time.Second,
		DelayedMoveLimit:    cfg.BatchImage.DelayedMoveLimit,
		RecoverLimit:        cfg.BatchImage.RecoverLimit,
	})
}

func normalizeBatchImageWorkerOptions(opts BatchImageWorkerOptions) BatchImageWorkerOptions {
	if opts.ReserveBlockTimeout <= 0 {
		opts.ReserveBlockTimeout = defaultBatchImageWorkerReserveBlockTimeout
	}
	if opts.JobLockTTL <= 0 {
		opts.JobLockTTL = defaultBatchImageWorkerLockTTL
	}
	if opts.LockConflictDelay <= 0 {
		opts.LockConflictDelay = defaultBatchImageWorkerLockConflictDelay
	}
	if opts.DefaultRequeueDelay <= 0 {
		opts.DefaultRequeueDelay = defaultBatchImageWorkerRequeueDelay
	}
	if opts.ErrorRetryDelay <= 0 {
		opts.ErrorRetryDelay = defaultBatchImageWorkerErrorRetryDelay
	}
	if opts.ErrorBackoff <= 0 {
		opts.ErrorBackoff = defaultBatchImageWorkerErrorBackoff
	}
	if opts.DelayedPollInterval <= 0 {
		opts.DelayedPollInterval = defaultBatchImageWorkerDelayedPollInterval
	}
	if opts.RecoveryInterval <= 0 {
		opts.RecoveryInterval = defaultBatchImageWorkerRecoveryInterval
	}
	if opts.StaleActiveAfter <= 0 {
		opts.StaleActiveAfter = defaultBatchImageWorkerStaleActiveAfter
	}
	if opts.DelayedMoveLimit <= 0 {
		opts.DelayedMoveLimit = defaultBatchImageWorkerDelayedMoveLimit
	}
	if opts.RecoverLimit <= 0 {
		opts.RecoverLimit = defaultBatchImageWorkerRecoverLimit
	}
	return opts
}

func (w *BatchImageWorker) Run(ctx context.Context) {
	if w == nil {
		return
	}
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			sleepOrDone(ctx, w.opts.ErrorBackoff)
		}
	}
}

func (w *BatchImageWorker) RunOnce(ctx context.Context) error {
	if w == nil || w.queue == nil || w.processor == nil {
		return nil
	}
	if !runtimegate.SharedWorkAllowed() {
		sleepOrDone(ctx, w.opts.ReserveBlockTimeout)
		return nil
	}

	reserved, err := w.queue.Reserve(ctx, w.opts.ReserveBlockTimeout)
	if errors.Is(err, ErrBatchImageQueueEmpty) {
		return nil
	}
	if err != nil {
		return err
	}
	// Reserve may block while a release drains this generation. Re-check after
	// Redis hands us a job so an inactive color never starts new provider work.
	if !runtimegate.SharedWorkAllowed() {
		// Reservation membership has no owner token. Mutating it without the job
		// lock could remove the active entry of a concurrent lock holder, so leave
		// it for stale-active recovery.
		return nil
	}

	lock, ok, err := w.queue.TryAcquireJobLock(ctx, reserved.BatchID, w.opts.JobLockTTL)
	if err != nil {
		return err
	}
	if !ok {
		// Another owner already holds the job lock. A raw RequeueAfter here would
		// delete that owner's active heartbeat anchor. The holder will finish the
		// job; if it does not, stale-active recovery is the safe retry path.
		return nil
	}
	defer func() {
		_ = lock.Release(ctx)
	}()
	refresher, ok := lock.(BatchImageJobLockRefresher)
	if !ok || refresher == nil {
		// A provider operation can outlive the initial lease. Processing without
		// refresh support would let a second worker or Cancel acquire the same job
		// while this worker is still producing side effects.
		return ErrBatchImageLockNotAcquired
	}
	queueFencer, ok := lock.(BatchImageJobLockQueueFencer)
	if !ok || queueFencer == nil {
		return ErrBatchImageLockNotAcquired
	}

	// 处理期间持续心跳：刷新 active zset 时间戳防止 stale 恢复把在处理的
	// job 重投给其他 worker，并延长锁 TTL。失锁时必须取消 processor，且
	// 旧 worker 不得再 Ack/Requeue；由新的持锁者或 recovery 接管。
	processCtx, processCancel := context.WithCancel(ctx)
	defer processCancel()
	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	heartbeatDone := make(chan error, 1)
	go w.runJobHeartbeat(heartbeatCtx, reserved.BatchID, refresher, processCancel, heartbeatDone)

	result, err := w.processor.Process(processCtx, reserved.BatchID)
	heartbeatCancel()
	heartbeatErr := <-heartbeatDone
	if heartbeatErr != nil {
		logger.L().Warn("batch_image.worker_lost_job_lock",
			zap.String("batch_id", reserved.BatchID),
			zap.Error(heartbeatErr),
		)
		return heartbeatErr
	}
	// Fence the terminal queue mutation with one final ownership check. This
	// catches a scheduler pause that outlived the lease just as Process returned
	// and won the select against the pending heartbeat tick.
	finalRefreshCtx, finalRefreshCancel := context.WithTimeout(ctx, w.heartbeatInterval())
	finalRefreshErr := refresher.Refresh(finalRefreshCtx, w.opts.JobLockTTL)
	finalRefreshCancel()
	if finalRefreshErr != nil {
		logger.L().Warn("batch_image.worker_lost_job_lock_before_queue_update",
			zap.String("batch_id", reserved.BatchID),
			zap.Error(finalRefreshErr),
		)
		return finalRefreshErr
	}
	if err != nil {
		logger.L().Warn("batch_image.worker_process_failed",
			zap.String("batch_id", reserved.BatchID),
			zap.Error(err),
		)
		return queueFencer.RequeueAfter(ctx, w.opts.ErrorRetryDelay)
	}
	if result.Terminal {
		return queueFencer.Ack(ctx)
	}
	delay := result.RequeueAfter
	if delay <= 0 {
		delay = w.opts.DefaultRequeueDelay
	}
	return queueFencer.RequeueAfter(ctx, delay)
}

// BatchImageJobLockRefresher 是可选的锁续期能力；由具体锁实现按需提供。
type BatchImageJobLockRefresher interface {
	Refresh(ctx context.Context, ttl time.Duration) error
}

func (w *BatchImageWorker) heartbeatInterval() time.Duration {
	interval := w.opts.JobLockTTL
	if w.opts.StaleActiveAfter < interval {
		interval = w.opts.StaleActiveAfter
	}
	interval /= 3
	if interval <= 0 {
		interval = time.Nanosecond
	}
	return interval
}

func (w *BatchImageWorker) runJobHeartbeat(
	ctx context.Context,
	batchID string,
	refresher BatchImageJobLockRefresher,
	cancelProcess context.CancelFunc,
	done chan<- error,
) {
	interval := w.heartbeatInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case <-ticker.C:
			refreshCtx, refreshCancel := context.WithTimeout(ctx, interval)
			refreshErr := refresher.Refresh(refreshCtx, w.opts.JobLockTTL)
			refreshCancel()
			if refreshErr != nil {
				if ctx.Err() != nil {
					done <- nil
					return
				}
				logger.L().Warn("batch_image.worker_lock_refresh_failed",
					zap.String("batch_id", batchID),
					zap.Error(refreshErr),
				)
				cancelProcess()
				done <- refreshErr
				return
			}

			heartbeatCtx, heartbeatCancel := context.WithTimeout(ctx, interval)
			heartbeatErr := w.queue.Heartbeat(heartbeatCtx, batchID)
			heartbeatCancel()
			if heartbeatErr != nil && ctx.Err() == nil {
				logger.L().Warn("batch_image.worker_heartbeat_failed",
					zap.String("batch_id", batchID),
					zap.Error(heartbeatErr),
				)
			}
		}
	}
}

func (w *BatchImageWorker) MoveDueDelayedOnce(ctx context.Context) (int, error) {
	if w == nil || w.queue == nil {
		return 0, nil
	}
	if !runtimegate.SharedWorkAllowed() {
		return 0, nil
	}
	return w.queue.MoveDueDelayedToReady(ctx, w.opts.DelayedMoveLimit)
}

func (w *BatchImageWorker) RunDelayedMover(ctx context.Context) {
	if w == nil {
		return
	}
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		moved, _ := w.MoveDueDelayedOnce(ctx)
		if moved > 0 {
			continue
		}
		sleepOrDone(ctx, w.opts.DelayedPollInterval)
	}
}

func (w *BatchImageWorker) RecoverStaleActiveOnce(ctx context.Context) (int, error) {
	if w == nil || w.queue == nil {
		return 0, nil
	}
	if !runtimegate.SharedWorkAllowed() {
		return 0, nil
	}
	return w.queue.RecoverStaleActive(ctx, w.opts.StaleActiveAfter, w.opts.RecoverLimit)
}

func (w *BatchImageWorker) RunStaleActiveRecovery(ctx context.Context) {
	if w == nil {
		return
	}
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		_, _ = w.RecoverStaleActiveOnce(ctx)
		sleepOrDone(ctx, w.opts.RecoveryInterval)
	}
}

func sleepOrDone(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
