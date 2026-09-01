//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/stretchr/testify/require"
)

type recordingBatchImageQueueRecoveryRepo struct {
	jobs             []*BatchImageJob
	maxIDFn          func() int64
	listFn           func(afterID, throughID int64, limit int) []*BatchImageJob
	maxIDErr         error
	listErr          error
	eligibleFn       func(batchID string) bool
	maxIDCalls       int
	listCalls        int
	cursors          []int64
	upperBounds      []int64
	limits           []int
	eligibilityCalls []string
	events           []recordingBatchImageQueueRecoveryEvent
}

type recordingBatchImageQueueRecoveryEvent struct {
	batchID   string
	eventType string
	payload   any
}

func (r *recordingBatchImageQueueRecoveryRepo) MaxProviderSubmittedBatchImageJobIDForQueueRecovery(context.Context) (int64, error) {
	r.maxIDCalls++
	if r.maxIDErr != nil {
		return 0, r.maxIDErr
	}
	if r.maxIDFn != nil {
		return r.maxIDFn(), nil
	}
	var maxID int64
	for _, job := range r.jobs {
		if job != nil && job.ID > maxID {
			maxID = job.ID
		}
	}
	if maxID == 0 && len(r.jobs) > 0 {
		return 1, nil
	}
	return maxID, nil
}

func (r *recordingBatchImageQueueRecoveryRepo) ListProviderSubmittedBatchImageJobsForQueueRecovery(_ context.Context, afterID, throughID int64, limit int) ([]*BatchImageJob, error) {
	r.listCalls++
	r.cursors = append(r.cursors, afterID)
	r.upperBounds = append(r.upperBounds, throughID)
	r.limits = append(r.limits, limit)
	if r.listErr != nil {
		return nil, r.listErr
	}
	if r.listFn != nil {
		return r.listFn(afterID, throughID, limit), nil
	}
	return r.jobs, nil
}

func (r *recordingBatchImageQueueRecoveryRepo) AppendBatchImageEvent(_ context.Context, batchID, eventType string, payload any) error {
	r.events = append(r.events, recordingBatchImageQueueRecoveryEvent{batchID: batchID, eventType: eventType, payload: payload})
	return nil
}

func (r *recordingBatchImageQueueRecoveryRepo) IsProviderSubmittedBatchImageJobQueueRecoveryEligible(_ context.Context, batchID string) (bool, error) {
	r.eligibilityCalls = append(r.eligibilityCalls, batchID)
	if r.eligibleFn != nil {
		return r.eligibleFn(batchID), nil
	}
	for _, job := range r.jobs {
		if job != nil && job.BatchID == batchID {
			return isProviderSubmittedQueueRecoveryEligible(job), nil
		}
	}
	return true, nil
}

type recordingBatchImageQueueEnsurer struct {
	restored   map[string]bool
	ensureFn   func(batchID string) (bool, error)
	calls      []string
	lockCalls  []string
	lockDenied map[string]bool
	released   int
	ensured    chan struct{}
}

type recordingBatchImageQueueRecoveryLock struct {
	queue   *recordingBatchImageQueueEnsurer
	batchID string
}

func (l *recordingBatchImageQueueRecoveryLock) Release(context.Context) error {
	l.queue.released++
	return nil
}

func (l *recordingBatchImageQueueRecoveryLock) Ack(context.Context) error { return nil }

func (l *recordingBatchImageQueueRecoveryLock) RequeueAfter(context.Context, time.Duration) error {
	return nil
}

func (l *recordingBatchImageQueueRecoveryLock) EnsureEnqueued(ctx context.Context) (bool, error) {
	return l.queue.EnsureEnqueued(ctx, l.batchID)
}

func (q *recordingBatchImageQueueEnsurer) TryAcquireJobLock(_ context.Context, batchID string, _ time.Duration) (BatchImageJobLock, bool, error) {
	q.lockCalls = append(q.lockCalls, batchID)
	if q.lockDenied[batchID] {
		return nil, false, nil
	}
	return &recordingBatchImageQueueRecoveryLock{queue: q, batchID: batchID}, true, nil
}

func (q *recordingBatchImageQueueEnsurer) EnsureEnqueued(_ context.Context, batchID string) (bool, error) {
	q.calls = append(q.calls, batchID)
	if q.ensured != nil {
		select {
		case q.ensured <- struct{}{}:
		default:
		}
	}
	if q.ensureFn != nil {
		return q.ensureFn(batchID)
	}
	return q.restored[batchID], nil
}

func TestBatchImageQueueRecoveryService_ReconcilesOnlyProviderSubmittedNonterminalJobs(t *testing.T) {
	providerJob := "providers/jobs/123"
	blankProviderJob := "  "
	repo := &recordingBatchImageQueueRecoveryRepo{jobs: []*BatchImageJob{
		{BatchID: "imgbatch_submitted", Status: BatchImageJobStatusSubmitted, ProviderJobName: &providerJob},
		{BatchID: "imgbatch_running", Status: BatchImageJobStatusRunning, ProviderJobName: &providerJob},
		{BatchID: "imgbatch_indexing", Status: BatchImageJobStatusIndexing, ProviderJobName: &providerJob},
		{BatchID: "imgbatch_settling", Status: BatchImageJobStatusSettling, ProviderJobName: &providerJob},
		{BatchID: "imgbatch_created", Status: BatchImageJobStatusCreated, ProviderJobName: &providerJob},
		{BatchID: "imgbatch_completed", Status: BatchImageJobStatusCompleted, ProviderJobName: &providerJob},
		{BatchID: "imgbatch_missing_provider", Status: BatchImageJobStatusSubmitted},
		{BatchID: "imgbatch_blank_provider", Status: BatchImageJobStatusSubmitted, ProviderJobName: &blankProviderJob},
		nil,
	}}
	queue := &recordingBatchImageQueueEnsurer{restored: map[string]bool{
		"imgbatch_submitted": true,
		"imgbatch_running":   true,
		"imgbatch_indexing":  true,
		"imgbatch_settling":  true,
	}}

	recovered, err := (&BatchImageQueueRecoveryService{Repo: repo, Queue: queue, Limit: 17}).ReconcileProviderSubmittedOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 4, recovered)
	require.Equal(t, []int64{0}, repo.cursors)
	require.Equal(t, []int{17}, repo.limits)
	require.Equal(t, []string{"imgbatch_submitted", "imgbatch_running", "imgbatch_indexing", "imgbatch_settling"}, queue.calls)
	require.Len(t, repo.events, 4)
	for _, event := range repo.events {
		require.Equal(t, "provider_queue_recovered", event.eventType)
		require.Equal(t, map[string]any{"source": "provider_submitted_reconciliation"}, event.payload)
	}
}

func TestBatchImageQueueRecoveryService_RotatesBoundedDBPages(t *testing.T) {
	providerJob := "providers/jobs/123"
	first := &BatchImageJob{ID: 11, BatchID: "imgbatch_first", Status: BatchImageJobStatusSubmitted, ProviderJobName: &providerJob}
	second := &BatchImageJob{ID: 22, BatchID: "imgbatch_second", Status: BatchImageJobStatusRunning, ProviderJobName: &providerJob}
	repo := &recordingBatchImageQueueRecoveryRepo{
		jobs: []*BatchImageJob{first, second},
		listFn: func(afterID, _ int64, _ int) []*BatchImageJob {
			switch afterID {
			case 0:
				return []*BatchImageJob{first}
			case first.ID:
				return []*BatchImageJob{second}
			default:
				return nil
			}
		},
	}
	queue := &recordingBatchImageQueueEnsurer{restored: map[string]bool{}}
	svc := &BatchImageQueueRecoveryService{Repo: repo, Queue: queue, Limit: 1}

	for range 4 {
		_, err := svc.ReconcileProviderSubmittedOnce(context.Background())
		require.NoError(t, err)
	}

	// Reaching the fixed pass upper bound resets immediately; no extra empty
	// query is required before the next fair pass starts at zero.
	require.Equal(t, []int64{0, first.ID, 0, first.ID}, repo.cursors)
	require.Equal(t, []int64{second.ID, second.ID, second.ID, second.ID}, repo.upperBounds)
	require.Equal(t, []string{first.BatchID, second.BatchID, first.BatchID, second.BatchID}, queue.calls)
	require.Equal(t, 2, repo.maxIDCalls)
}

func TestBatchImageQueueRecoveryService_NormalizesOversizedLimitBeforeCursorAccounting(t *testing.T) {
	providerJob := "providers/jobs/123"
	jobs := make([]*BatchImageJob, maxBatchImageQueueRecoveryLimit)
	for index := range jobs {
		jobs[index] = &BatchImageJob{
			ID:              int64(index + 1),
			BatchID:         fmt.Sprintf("imgbatch_limit_%d", index+1),
			Status:          BatchImageJobStatusSubmitted,
			ProviderJobName: &providerJob,
		}
	}
	repo := &recordingBatchImageQueueRecoveryRepo{
		maxIDFn: func() int64 { return int64(maxBatchImageQueueRecoveryLimit * 2) },
		listFn: func(afterID, _ int64, _ int) []*BatchImageJob {
			if afterID == 0 {
				return jobs
			}
			return nil
		},
	}
	queue := &recordingBatchImageQueueEnsurer{restored: map[string]bool{}}
	svc := &BatchImageQueueRecoveryService{
		Repo: repo, Queue: queue, Limit: maxBatchImageQueueRecoveryLimit + 1,
	}

	_, err := svc.ReconcileProviderSubmittedOnce(context.Background())
	require.NoError(t, err)
	_, err = svc.ReconcileProviderSubmittedOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int{maxBatchImageQueueRecoveryLimit, maxBatchImageQueueRecoveryLimit}, repo.limits)
	require.Equal(t, []int64{0, int64(maxBatchImageQueueRecoveryLimit)}, repo.cursors)
}

func TestBatchImageQueueRecoveryService_FailedRepairIsRevisitedAfterFixedSnapshotPass(t *testing.T) {
	providerJob := "providers/jobs/123"
	jobs := make([]*BatchImageJob, 6)
	for index := range jobs {
		jobs[index] = &BatchImageJob{
			ID:              int64(index + 1),
			BatchID:         fmt.Sprintf("imgbatch_snapshot_%d", index+1),
			Status:          BatchImageJobStatusSubmitted,
			ProviderJobName: &providerJob,
		}
	}
	maxCalls := 0
	repo := &recordingBatchImageQueueRecoveryRepo{
		jobs: jobs,
		maxIDFn: func() int64 {
			maxCalls++
			if maxCalls == 1 {
				return 4
			}
			return 6
		},
		listFn: func(afterID, throughID int64, limit int) []*BatchImageJob {
			page := make([]*BatchImageJob, 0, limit)
			for _, job := range jobs {
				if job.ID > afterID && job.ID <= throughID {
					page = append(page, job)
					if len(page) == limit {
						break
					}
				}
			}
			return page
		},
	}
	firstAttempts := 0
	queue := &recordingBatchImageQueueEnsurer{
		restored: map[string]bool{},
		ensureFn: func(batchID string) (bool, error) {
			if batchID == jobs[0].BatchID {
				firstAttempts++
				if firstAttempts == 1 {
					return false, fmt.Errorf("temporary redis failure")
				}
			}
			return true, nil
		},
	}
	svc := &BatchImageQueueRecoveryService{Repo: repo, Queue: queue, Limit: 2}

	_, err := svc.ReconcileProviderSubmittedOnce(context.Background())
	require.ErrorContains(t, err, "temporary redis failure")
	_, err = svc.ReconcileProviderSubmittedOnce(context.Background())
	require.NoError(t, err)
	_, err = svc.ReconcileProviderSubmittedOnce(context.Background())
	require.NoError(t, err)

	// IDs 5 and 6 appeared while the first pass was full, but its fixed upper
	// bound remained 4. Reaching 4 reset the cursor, so failed ID 1 was retried
	// from zero instead of being permanently skipped behind a moving tail.
	require.Equal(t, []int64{0, 2, 0}, repo.cursors)
	require.Equal(t, []int64{4, 4, 6}, repo.upperBounds)
	require.Equal(t, 2, firstAttempts)
}

func TestBatchImageQueueRecoveryService_QueryFailuresDoNotAdvanceSnapshot(t *testing.T) {
	providerJob := "providers/jobs/123"
	job := &BatchImageJob{
		ID: 9, BatchID: "imgbatch_query_retry", Status: BatchImageJobStatusSubmitted, ProviderJobName: &providerJob,
	}
	repo := &recordingBatchImageQueueRecoveryRepo{
		jobs:     []*BatchImageJob{job},
		maxIDErr: fmt.Errorf("max unavailable"),
	}
	queue := &recordingBatchImageQueueEnsurer{restored: map[string]bool{job.BatchID: true}}
	svc := &BatchImageQueueRecoveryService{Repo: repo, Queue: queue, Limit: 1}

	_, err := svc.ReconcileProviderSubmittedOnce(context.Background())
	require.ErrorContains(t, err, "max unavailable")
	require.Zero(t, repo.listCalls)

	repo.maxIDErr = nil
	repo.listErr = fmt.Errorf("page unavailable")
	_, err = svc.ReconcileProviderSubmittedOnce(context.Background())
	require.ErrorContains(t, err, "page unavailable")
	require.Equal(t, []int64{0}, repo.cursors)
	require.Equal(t, []int64{job.ID}, repo.upperBounds)

	// The failed list did not reset or advance the already-fixed pass. Retrying
	// uses the same upper bound without issuing a new MAX query.
	repo.listErr = nil
	_, err = svc.ReconcileProviderSubmittedOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int64{0, 0}, repo.cursors)
	require.Equal(t, []int64{job.ID, job.ID}, repo.upperBounds)
	require.Equal(t, 2, repo.maxIDCalls)
	require.Equal(t, []string{job.BatchID}, queue.calls)
}

func TestBatchImageQueueRecoveryService_RevalidatesUnderJobLockBeforeEnqueue(t *testing.T) {
	providerJob := "providers/jobs/123"
	job := &BatchImageJob{
		ID: 1, BatchID: "imgbatch_terminal_race", Status: BatchImageJobStatusSubmitted, ProviderJobName: &providerJob,
	}
	repo := &recordingBatchImageQueueRecoveryRepo{
		jobs:       []*BatchImageJob{job},
		eligibleFn: func(string) bool { return false },
	}
	queue := &recordingBatchImageQueueEnsurer{restored: map[string]bool{job.BatchID: true}}

	recovered, err := (&BatchImageQueueRecoveryService{Repo: repo, Queue: queue}).ReconcileProviderSubmittedOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, recovered)
	require.Equal(t, []string{job.BatchID}, queue.lockCalls)
	require.Equal(t, []string{job.BatchID}, repo.eligibilityCalls)
	require.Empty(t, queue.calls)
	require.Equal(t, 1, queue.released)
}

func TestBatchImageQueueRecoveryService_DoesNotRaceAWorkerHoldingTheJobLock(t *testing.T) {
	providerJob := "providers/jobs/123"
	job := &BatchImageJob{
		ID: 1, BatchID: "imgbatch_worker_owned", Status: BatchImageJobStatusRunning, ProviderJobName: &providerJob,
	}
	repo := &recordingBatchImageQueueRecoveryRepo{
		jobs: []*BatchImageJob{job},
		eligibleFn: func(string) bool {
			t.Fatal("eligibility must not be read without acquiring the worker lock")
			return false
		},
	}
	queue := &recordingBatchImageQueueEnsurer{
		restored:   map[string]bool{job.BatchID: true},
		lockDenied: map[string]bool{job.BatchID: true},
	}

	recovered, err := (&BatchImageQueueRecoveryService{Repo: repo, Queue: queue}).ReconcileProviderSubmittedOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, recovered)
	require.Empty(t, repo.eligibilityCalls)
	require.Empty(t, queue.calls)
}

func TestBatchImageQueueRecoveryService_StandbyDoesNotScanOrEnqueue(t *testing.T) {
	runtimegate.SetProcessActive(false)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })

	providerJob := "providers/jobs/123"
	repo := &recordingBatchImageQueueRecoveryRepo{jobs: []*BatchImageJob{{
		BatchID: "imgbatch_standby", Status: BatchImageJobStatusSubmitted, ProviderJobName: &providerJob,
	}}}
	queue := &recordingBatchImageQueueEnsurer{restored: map[string]bool{"imgbatch_standby": true}}

	recovered, err := (&BatchImageQueueRecoveryService{Repo: repo, Queue: queue}).ReconcileProviderSubmittedOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, recovered)
	require.Zero(t, repo.listCalls)
	require.Empty(t, queue.calls)
}

func TestBatchImageQueueRecoveryService_ResumedProviderJobUsesGetAndOpenResultWithoutSubmit(t *testing.T) {
	ctx := context.Background()
	accountID := int64(10)
	providerJob := "providers/jobs/123"
	job := &BatchImageJob{
		BatchID:         "imgbatch_queue_recovery_processor",
		Status:          BatchImageJobStatusSubmitted,
		Provider:        "fake",
		AccountID:       &accountID,
		ProviderJobName: &providerJob,
	}

	recoveryRepo := &recordingBatchImageQueueRecoveryRepo{jobs: []*BatchImageJob{job}}
	recoveryQueue := &recordingBatchImageQueueEnsurer{restored: map[string]bool{job.BatchID: true}}
	recovered, err := (&BatchImageQueueRecoveryService{Repo: recoveryRepo, Queue: recoveryQueue}).ReconcileProviderSubmittedOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)

	processorRepo := newFakeBatchImageRepository()
	processorRepo.jobs[job.BatchID] = job
	provider := &fakeProcessorProvider{
		status: &BatchProviderStatus{InternalState: BatchProviderStateSucceeded, RawState: "SUCCEEDED", ProviderOutputRef: "files/output"},
		result: `{"key":"ok","response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + batchImageTestData + `"}}]}}]}}` + "\n",
	}

	result, err := newTestBatchImageProcessor(processorRepo, provider).Process(ctx, job.BatchID)
	require.NoError(t, err)
	require.False(t, result.Terminal)
	require.True(t, provider.getCalled)
	require.True(t, provider.openResultCalled)
	require.Zero(t, provider.submitCalls)
}

func TestBatchImageWorkerRuntime_RunQueueRecoverySkipsStandbyGeneration(t *testing.T) {
	runtimegate.SetProcessActive(false)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })

	providerJob := "providers/jobs/123"
	repo := &recordingBatchImageQueueRecoveryRepo{jobs: []*BatchImageJob{{
		BatchID: "imgbatch_runtime_standby", Status: BatchImageJobStatusSubmitted, ProviderJobName: &providerJob,
	}}}
	queue := &recordingBatchImageQueueEnsurer{restored: map[string]bool{"imgbatch_runtime_standby": true}}
	runtime := &BatchImageWorkerRuntime{
		worker:        NewBatchImageWorker(nil, nil, BatchImageWorkerOptions{RecoveryInterval: time.Millisecond}),
		queueRecovery: &BatchImageQueueRecoveryService{Repo: repo, Queue: queue},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runtime.runQueueRecovery(ctx)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	require.Zero(t, repo.listCalls)
	require.Empty(t, queue.calls)
}

func TestBatchImageWorkerRuntime_RunQueueRecoveryReconcilesProviderSubmittedJob(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })

	providerJob := "providers/jobs/123"
	repo := &recordingBatchImageQueueRecoveryRepo{jobs: []*BatchImageJob{{
		BatchID: "imgbatch_runtime_recovery", Status: BatchImageJobStatusSubmitted, ProviderJobName: &providerJob,
	}}}
	queue := &recordingBatchImageQueueEnsurer{
		restored: map[string]bool{"imgbatch_runtime_recovery": true},
		ensured:  make(chan struct{}, 1),
	}
	runtime := &BatchImageWorkerRuntime{
		worker:        NewBatchImageWorker(nil, nil, BatchImageWorkerOptions{RecoveryInterval: time.Hour}),
		queueRecovery: &BatchImageQueueRecoveryService{Repo: repo, Queue: queue},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runtime.runQueueRecovery(ctx)
		close(done)
	}()
	select {
	case <-queue.ensured:
	case <-time.After(time.Second):
		t.Fatal("queue recovery did not run")
	}
	cancel()
	<-done

	require.Equal(t, []string{"imgbatch_runtime_recovery"}, queue.calls)
	require.Len(t, repo.events, 1)
}
