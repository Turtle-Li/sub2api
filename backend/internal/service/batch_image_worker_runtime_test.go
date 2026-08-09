//go:build unit

package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBatchImageWorkerRuntime_QueueDisabledDoesNotStart(t *testing.T) {
	queue := &blockingBatchImageRuntimeQueue{}
	runtime := NewBatchImageWorkerRuntime(
		NewBatchImageWorker(queue, &fakeBatchImageProcessor{}, BatchImageWorkerOptions{}),
		&config.Config{BatchImage: config.BatchImageConfig{QueueEnabled: false}},
	)

	runtime.Start()

	require.False(t, runtime.Running())
	require.Zero(t, queue.reserveCalls.Load())
	require.NotPanics(t, runtime.Stop)
}

func TestBatchImageWorkerRuntime_QueueEnabledStartsAndStops(t *testing.T) {
	queue := &blockingBatchImageRuntimeQueue{}
	processor := &fakeBatchImageProcessor{}
	runtime := NewBatchImageWorkerRuntime(
		NewBatchImageWorker(queue, processor, BatchImageWorkerOptions{
			DelayedPollInterval: time.Hour,
			RecoveryInterval:    time.Hour,
		}),
		&config.Config{BatchImage: config.BatchImageConfig{
			QueueEnabled:      true,
			WorkerConcurrency: 3,
		}},
	)

	runtime.Start()

	require.Eventually(t, func() bool {
		return runtime.Running() && queue.reserveCalls.Load() >= 3
	}, time.Second, 10*time.Millisecond)
	require.Empty(t, processor.processed)
	require.NotPanics(t, runtime.Stop)
	require.False(t, runtime.Running())
	require.NotPanics(t, runtime.Stop)
}

func TestBatchImageWorkerRuntime_BoundsParallelJobProcessing(t *testing.T) {
	queue := newParallelBatchImageRuntimeQueue(
		"imgbatch_1",
		"imgbatch_2",
		"imgbatch_3",
		"imgbatch_4",
	)
	processor := &blockingBatchImageRuntimeProcessor{
		release: make(chan struct{}),
	}
	runtime := NewBatchImageWorkerRuntime(
		NewBatchImageWorker(queue, processor, BatchImageWorkerOptions{
			DelayedPollInterval: time.Hour,
			RecoveryInterval:    time.Hour,
			JobLockTTL:          time.Hour,
		}),
		&config.Config{BatchImage: config.BatchImageConfig{
			QueueEnabled:      true,
			WorkerConcurrency: 3,
		}},
	)

	runtime.Start()
	require.Eventually(t, func() bool {
		return processor.started.Load() == 3 && processor.active.Load() == 3
	}, time.Second, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int64(3), processor.started.Load())
	require.Equal(t, int64(3), processor.peak.Load())

	close(processor.release)
	require.Eventually(t, func() bool {
		return processor.started.Load() == 4 && queue.ackCalls.Load() == 4
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, int64(3), processor.peak.Load())
	runtime.Stop()
}

type blockingBatchImageRuntimeQueue struct {
	reserveCalls atomic.Int64
}

type parallelBatchImageRuntimeQueue struct {
	jobs     chan string
	ackCalls atomic.Int64
}

func newParallelBatchImageRuntimeQueue(batchIDs ...string) *parallelBatchImageRuntimeQueue {
	queue := &parallelBatchImageRuntimeQueue{jobs: make(chan string, len(batchIDs))}
	for _, batchID := range batchIDs {
		queue.jobs <- batchID
	}
	return queue
}

func (q *parallelBatchImageRuntimeQueue) Enqueue(_ context.Context, batchID string) error {
	q.jobs <- batchID
	return nil
}

func (q *parallelBatchImageRuntimeQueue) Reserve(ctx context.Context, _ time.Duration) (ReservedBatchImageJob, error) {
	select {
	case batchID := <-q.jobs:
		return ReservedBatchImageJob{BatchID: batchID}, nil
	case <-ctx.Done():
		return ReservedBatchImageJob{}, ctx.Err()
	}
}

func (q *parallelBatchImageRuntimeQueue) RequeueAfter(_ context.Context, batchID string, _ time.Duration) error {
	q.jobs <- batchID
	return nil
}

func (q *parallelBatchImageRuntimeQueue) Ack(context.Context, string) error {
	q.ackCalls.Add(1)
	return nil
}

func (q *parallelBatchImageRuntimeQueue) Heartbeat(context.Context, string) error {
	return nil
}

func (q *parallelBatchImageRuntimeQueue) MoveDueDelayedToReady(context.Context, int) (int, error) {
	return 0, nil
}

func (q *parallelBatchImageRuntimeQueue) RecoverStaleActive(context.Context, time.Duration, int) (int, error) {
	return 0, nil
}

func (q *parallelBatchImageRuntimeQueue) TryAcquireJobLock(context.Context, string, time.Duration) (BatchImageJobLock, bool, error) {
	return noopBatchImageRuntimeLock{}, true, nil
}

type noopBatchImageRuntimeLock struct{}

func (noopBatchImageRuntimeLock) Release(context.Context) error { return nil }

type blockingBatchImageRuntimeProcessor struct {
	release chan struct{}
	started atomic.Int64
	active  atomic.Int64
	peak    atomic.Int64
}

func (p *blockingBatchImageRuntimeProcessor) Process(ctx context.Context, _ string) (BatchImageProcessResult, error) {
	p.started.Add(1)
	active := p.active.Add(1)
	for {
		peak := p.peak.Load()
		if active <= peak || p.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	defer p.active.Add(-1)
	select {
	case <-p.release:
		return BatchImageProcessResult{Terminal: true}, nil
	case <-ctx.Done():
		return BatchImageProcessResult{}, ctx.Err()
	}
}

func (q *blockingBatchImageRuntimeQueue) Enqueue(context.Context, string) error {
	return nil
}

func (q *blockingBatchImageRuntimeQueue) Reserve(ctx context.Context, _ time.Duration) (ReservedBatchImageJob, error) {
	q.reserveCalls.Add(1)
	<-ctx.Done()
	return ReservedBatchImageJob{}, ctx.Err()
}

func (q *blockingBatchImageRuntimeQueue) RequeueAfter(context.Context, string, time.Duration) error {
	return nil
}

func (q *blockingBatchImageRuntimeQueue) Ack(context.Context, string) error {
	return nil
}

func (q *blockingBatchImageRuntimeQueue) Heartbeat(context.Context, string) error {
	return nil
}

func (q *blockingBatchImageRuntimeQueue) MoveDueDelayedToReady(context.Context, int) (int, error) {
	return 0, nil
}

func (q *blockingBatchImageRuntimeQueue) RecoverStaleActive(context.Context, time.Duration, int) (int, error) {
	return 0, nil
}

func (q *blockingBatchImageRuntimeQueue) TryAcquireJobLock(context.Context, string, time.Duration) (BatchImageJobLock, bool, error) {
	return nil, false, nil
}
