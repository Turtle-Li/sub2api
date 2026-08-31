//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/stretchr/testify/require"
)

type recordingBatchImageQueueRecoveryRepo struct {
	jobs      []*BatchImageJob
	listFn    func(afterID int64, limit int) []*BatchImageJob
	listCalls int
	cursors   []int64
	limits    []int
	events    []recordingBatchImageQueueRecoveryEvent
}

type recordingBatchImageQueueRecoveryEvent struct {
	batchID   string
	eventType string
	payload   any
}

func (r *recordingBatchImageQueueRecoveryRepo) ListProviderSubmittedBatchImageJobsForQueueRecovery(_ context.Context, afterID int64, limit int) ([]*BatchImageJob, error) {
	r.listCalls++
	r.cursors = append(r.cursors, afterID)
	r.limits = append(r.limits, limit)
	if r.listFn != nil {
		return r.listFn(afterID, limit), nil
	}
	return r.jobs, nil
}

func (r *recordingBatchImageQueueRecoveryRepo) AppendBatchImageEvent(_ context.Context, batchID, eventType string, payload any) error {
	r.events = append(r.events, recordingBatchImageQueueRecoveryEvent{batchID: batchID, eventType: eventType, payload: payload})
	return nil
}

type recordingBatchImageQueueEnsurer struct {
	restored map[string]bool
	calls    []string
	ensured  chan struct{}
}

func (q *recordingBatchImageQueueEnsurer) EnsureEnqueued(_ context.Context, batchID string) (bool, error) {
	q.calls = append(q.calls, batchID)
	if q.ensured != nil {
		select {
		case q.ensured <- struct{}{}:
		default:
		}
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
		listFn: func(afterID int64, _ int) []*BatchImageJob {
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

	// The empty page resets the cursor, so the next period starts a new fair
	// pass rather than permanently scanning only the oldest recovery window.
	require.Equal(t, []int64{0, first.ID, second.ID, 0}, repo.cursors)
	require.Equal(t, []string{first.BatchID, second.BatchID, first.BatchID}, queue.calls)
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
