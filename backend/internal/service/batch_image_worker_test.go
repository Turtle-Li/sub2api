//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/stretchr/testify/require"
)

func TestBatchImageWorker_ProcessesJobOnce(t *testing.T) {
	queue := newFakeBatchImageQueue("imgbatch_worker_once")
	processor := &fakeBatchImageProcessor{}
	worker := NewBatchImageWorker(queue, processor, BatchImageWorkerOptions{ReserveBlockTimeout: time.Millisecond})

	require.NoError(t, worker.RunOnce(context.Background()))
	require.Equal(t, []string{"imgbatch_worker_once"}, processor.processed)
	require.Len(t, queue.requeued, 1)
	require.Equal(t, defaultBatchImageWorkerRequeueDelay, queue.requeued[0].delay)
	require.Equal(t, 1, queue.releaseCount)
}

func TestBatchImageWorker_RequeuesNonTerminalResultWithRequestedDelay(t *testing.T) {
	queue := newFakeBatchImageQueue("imgbatch_worker_requeue")
	processor := &fakeBatchImageProcessor{result: BatchImageProcessResult{RequeueAfter: 42 * time.Second}}
	worker := NewBatchImageWorker(queue, processor, BatchImageWorkerOptions{})

	require.NoError(t, worker.RunOnce(context.Background()))
	require.Len(t, queue.requeued, 1)
	require.Equal(t, "imgbatch_worker_requeue", queue.requeued[0].batchID)
	require.Equal(t, 42*time.Second, queue.requeued[0].delay)
	require.Empty(t, queue.acked)
}

func TestBatchImageWorker_AcksTerminalResult(t *testing.T) {
	queue := newFakeBatchImageQueue("imgbatch_worker_terminal")
	processor := &fakeBatchImageProcessor{result: BatchImageProcessResult{Terminal: true}}
	worker := NewBatchImageWorker(queue, processor, BatchImageWorkerOptions{})

	require.NoError(t, worker.RunOnce(context.Background()))
	require.Equal(t, []string{"imgbatch_worker_terminal"}, queue.acked)
	require.Empty(t, queue.requeued)
}

func TestBatchImageWorker_RequeuesOnProcessorError(t *testing.T) {
	queue := newFakeBatchImageQueue("imgbatch_worker_error")
	processor := &fakeBatchImageProcessor{err: errors.New("processor failed")}
	worker := NewBatchImageWorker(queue, processor, BatchImageWorkerOptions{ErrorRetryDelay: 7 * time.Second})

	require.NoError(t, worker.RunOnce(context.Background()))
	require.Len(t, queue.requeued, 1)
	require.Equal(t, 7*time.Second, queue.requeued[0].delay)
	require.Empty(t, queue.acked)
}

func TestBatchImageWorker_LeavesQueueUntouchedWhenJobLockNotAcquired(t *testing.T) {
	queue := newFakeBatchImageQueue("imgbatch_worker_locked")
	queue.lockAcquired = false
	processor := &fakeBatchImageProcessor{}
	worker := NewBatchImageWorker(queue, processor, BatchImageWorkerOptions{})

	require.NoError(t, worker.RunOnce(context.Background()))
	require.Empty(t, processor.processed)
	require.Empty(t, queue.requeued, "the current lock holder owns active queue membership")
	require.Empty(t, queue.acked)
}

func TestBatchImageWorker_LeavesQueueUntouchedWhenJobLockLookupFails(t *testing.T) {
	queue := newFakeBatchImageQueue("imgbatch_worker_lock_error")
	queue.lockErr = errors.New("redis unavailable")
	processor := &fakeBatchImageProcessor{}
	worker := NewBatchImageWorker(queue, processor, BatchImageWorkerOptions{})

	err := worker.RunOnce(context.Background())

	require.Error(t, err)
	require.Empty(t, processor.processed)
	require.Empty(t, queue.requeued)
	require.Empty(t, queue.acked)
}

func TestBatchImageWorker_RefusesJobLockWithoutRefreshSupport(t *testing.T) {
	queue := newFakeBatchImageQueue("imgbatch_worker_no_refresh")
	queue.lockSupportsRefresh = false
	processor := &fakeBatchImageProcessor{}
	worker := NewBatchImageWorker(queue, processor, BatchImageWorkerOptions{})

	err := worker.RunOnce(context.Background())

	require.ErrorIs(t, err, ErrBatchImageLockNotAcquired)
	require.Empty(t, processor.processed)
	require.Empty(t, queue.requeued, "without a token-aware fencer the worker must leave recovery to stale-active repair")
	require.Equal(t, 1, queue.releaseCount)
}

func TestBatchImageWorker_RequeuesWhenGenerationDrainsDuringReserve(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	queue := newFakeBatchImageQueue("imgbatch_worker_draining")
	queue.onReserve = func() { runtimegate.SetProcessActive(false) }
	processor := &fakeBatchImageProcessor{}
	worker := NewBatchImageWorker(queue, processor, BatchImageWorkerOptions{LockConflictDelay: 3 * time.Second})

	require.NoError(t, worker.RunOnce(context.Background()))
	require.Empty(t, processor.processed)
	require.Empty(t, queue.requeued, "a drained generation must not mutate an unfenced reservation")
	require.Empty(t, queue.acked)
	require.Zero(t, queue.releaseCount, "a drained generation must not acquire the per-job lock")
}

func TestNewBatchImageWorkerOptionsFromConfig_UsesFiniteReserveTimeout(t *testing.T) {
	opts := NewBatchImageWorkerOptionsFromConfig(nil)
	require.Equal(t, defaultBatchImageWorkerReserveBlockTimeout, opts.ReserveBlockTimeout)
	require.Positive(t, opts.ReserveBlockTimeout)
}

func TestBatchImageWorker_HeartbeatIntervalStaysBelowOneSecondLease(t *testing.T) {
	worker := NewBatchImageWorker(newFakeBatchImageQueue("imgbatch_worker_short_lease"), &fakeBatchImageProcessor{}, BatchImageWorkerOptions{
		JobLockTTL: time.Second,
	})

	require.Equal(t, time.Second/3, worker.heartbeatInterval())
	require.Less(t, worker.heartbeatInterval(), worker.opts.JobLockTTL)
}

func TestBatchImageWorker_LostJobLockCancelsProcessorWithoutQueueMutation(t *testing.T) {
	queue := newFakeBatchImageQueue("imgbatch_worker_lost_lock")
	queue.lockRefreshErr = ErrBatchImageLockNotAcquired
	processor := &fakeBatchImageProcessor{
		process: func(ctx context.Context, _ string) (BatchImageProcessResult, error) {
			<-ctx.Done()
			return BatchImageProcessResult{}, ctx.Err()
		},
	}
	worker := NewBatchImageWorker(queue, processor, BatchImageWorkerOptions{
		JobLockTTL:       30 * time.Millisecond,
		StaleActiveAfter: time.Second,
	})

	err := worker.RunOnce(context.Background())

	require.ErrorIs(t, err, ErrBatchImageLockNotAcquired)
	require.Equal(t, []string{"imgbatch_worker_lost_lock"}, processor.processed)
	require.Positive(t, queue.lockRefreshCalls)
	require.Empty(t, queue.acked)
	require.Empty(t, queue.requeued)
	require.Equal(t, 1, queue.releaseCount)
}

func TestBatchImageWorker_FinalOwnershipCheckPreventsAckAfterLeaseLoss(t *testing.T) {
	queue := newFakeBatchImageQueue("imgbatch_worker_lost_before_ack")
	queue.lockRefreshErr = ErrBatchImageLockNotAcquired
	processor := &fakeBatchImageProcessor{result: BatchImageProcessResult{Terminal: true}}
	worker := NewBatchImageWorker(queue, processor, BatchImageWorkerOptions{JobLockTTL: time.Second})

	err := worker.RunOnce(context.Background())

	require.ErrorIs(t, err, ErrBatchImageLockNotAcquired)
	require.Equal(t, []string{"imgbatch_worker_lost_before_ack"}, processor.processed)
	require.Equal(t, 1, queue.lockRefreshCalls)
	require.Empty(t, queue.acked)
	require.Empty(t, queue.requeued)
	require.Equal(t, 1, queue.releaseCount)
}

func TestBatchImageWorker_FencedQueueMutationRejectsLeaseLostAfterFinalRefresh(t *testing.T) {
	tests := []struct {
		name   string
		result BatchImageProcessResult
	}{
		{name: "ack", result: BatchImageProcessResult{Terminal: true}},
		{name: "requeue", result: BatchImageProcessResult{RequeueAfter: time.Minute}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := newFakeBatchImageQueue("imgbatch_worker_fence_" + tt.name)
			queue.lockFenceErr = ErrBatchImageLockNotAcquired
			processor := &fakeBatchImageProcessor{result: tt.result}
			worker := NewBatchImageWorker(queue, processor, BatchImageWorkerOptions{JobLockTTL: time.Second})

			err := worker.RunOnce(context.Background())

			require.ErrorIs(t, err, ErrBatchImageLockNotAcquired)
			require.Equal(t, 1, queue.lockRefreshCalls, "the final refresh succeeded before the token changed")
			require.Empty(t, queue.acked)
			require.Empty(t, queue.requeued)
			require.Equal(t, 1, queue.releaseCount)
		})
	}
}

type fakeBatchImageQueue struct {
	reserved            ReservedBatchImageJob
	lockAcquired        bool
	lockErr             error
	lockSupportsRefresh bool
	lockRefreshErr      error
	lockRefreshCalls    int
	lockFenceErr        error
	acked               []string
	requeued            []fakeBatchImageRequeue
	releaseCount        int
	onReserve           func()
}

type fakeBatchImageRequeue struct {
	batchID string
	delay   time.Duration
}

func newFakeBatchImageQueue(batchID string) *fakeBatchImageQueue {
	return &fakeBatchImageQueue{
		reserved:            ReservedBatchImageJob{BatchID: batchID},
		lockAcquired:        true,
		lockSupportsRefresh: true,
	}
}

func (q *fakeBatchImageQueue) Enqueue(context.Context, string) error {
	return nil
}

func (q *fakeBatchImageQueue) Reserve(context.Context, time.Duration) (ReservedBatchImageJob, error) {
	if q.onReserve != nil {
		q.onReserve()
	}
	return q.reserved, nil
}

func (q *fakeBatchImageQueue) RequeueAfter(_ context.Context, batchID string, delay time.Duration) error {
	q.requeued = append(q.requeued, fakeBatchImageRequeue{batchID: batchID, delay: delay})
	return nil
}

func (q *fakeBatchImageQueue) Ack(_ context.Context, batchID string) error {
	q.acked = append(q.acked, batchID)
	return nil
}

func (q *fakeBatchImageQueue) Heartbeat(context.Context, string) error {
	return nil
}

func (q *fakeBatchImageQueue) MoveDueDelayedToReady(context.Context, int) (int, error) {
	return 0, nil
}

func (q *fakeBatchImageQueue) RecoverStaleActive(context.Context, time.Duration, int) (int, error) {
	return 0, nil
}

func (q *fakeBatchImageQueue) TryAcquireJobLock(context.Context, string, time.Duration) (BatchImageJobLock, bool, error) {
	if q.lockErr != nil {
		return nil, false, q.lockErr
	}
	if !q.lockAcquired {
		return nil, false, nil
	}
	if !q.lockSupportsRefresh {
		return nonRefreshingBatchImageLock{release: func() { q.releaseCount++ }}, true, nil
	}
	return fakeBatchImageLock{
		queue:   q,
		batchID: q.reserved.BatchID,
		release: func() { q.releaseCount++ },
		refresh: func() error {
			q.lockRefreshCalls++
			return q.lockRefreshErr
		},
	}, true, nil
}

type nonRefreshingBatchImageLock struct {
	release func()
}

func (l nonRefreshingBatchImageLock) Release(context.Context) error {
	if l.release != nil {
		l.release()
	}
	return nil
}

type fakeBatchImageLock struct {
	queue   *fakeBatchImageQueue
	batchID string
	release func()
	refresh func() error
}

func (l fakeBatchImageLock) Release(context.Context) error {
	if l.release != nil {
		l.release()
	}
	return nil
}

func (l fakeBatchImageLock) Refresh(context.Context, time.Duration) error {
	if l.refresh != nil {
		return l.refresh()
	}
	return nil
}

func (l fakeBatchImageLock) Ack(ctx context.Context) error {
	if l.queue.lockFenceErr != nil {
		return l.queue.lockFenceErr
	}
	return l.queue.Ack(ctx, l.batchID)
}

func (l fakeBatchImageLock) RequeueAfter(ctx context.Context, delay time.Duration) error {
	if l.queue.lockFenceErr != nil {
		return l.queue.lockFenceErr
	}
	return l.queue.RequeueAfter(ctx, l.batchID, delay)
}

func (l fakeBatchImageLock) EnsureEnqueued(context.Context) (bool, error) {
	if l.queue.lockFenceErr != nil {
		return false, l.queue.lockFenceErr
	}
	return false, nil
}

type fakeBatchImageProcessor struct {
	result    BatchImageProcessResult
	err       error
	processed []string
	process   func(context.Context, string) (BatchImageProcessResult, error)
}

func (p *fakeBatchImageProcessor) Process(ctx context.Context, batchID string) (BatchImageProcessResult, error) {
	p.processed = append(p.processed, batchID)
	if p.process != nil {
		return p.process(ctx, batchID)
	}
	return p.result, p.err
}
