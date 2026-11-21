//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type batchImageCheckpointMemory struct {
	entries map[[3]string]BatchImageResultCheckpoint
}

func newBatchImageCheckpointMemory() *batchImageCheckpointMemory {
	return &batchImageCheckpointMemory{entries: make(map[[3]string]BatchImageResultCheckpoint)}
}

func (m *batchImageCheckpointMemory) GetBatchImageCheckpoint(_ context.Context, job, id, stage string) (*BatchImageResultCheckpoint, error) {
	checkpoint, ok := m.entries[[3]string{job, id, stage}]
	if !ok {
		return nil, nil
	}
	checkpoint.Data = bytes.Clone(checkpoint.Data)
	return &checkpoint, nil
}

func (m *batchImageCheckpointMemory) ListBatchImageCheckpointIDs(_ context.Context, job, stage string) (map[string]struct{}, error) {
	ids := make(map[string]struct{})
	for key := range m.entries {
		if key[0] == job && key[2] == stage {
			ids[key[1]] = struct{}{}
		}
	}
	return ids, nil
}

func (m *batchImageCheckpointMemory) SaveBatchImageCheckpoint(ctx context.Context, job, id, stage string, checkpoint BatchImageResultCheckpoint) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	key := [3]string{job, id, stage}
	if _, exists := m.entries[key]; exists {
		return false, nil
	}
	checkpoint.Data = bytes.Clone(checkpoint.Data)
	m.entries[key] = checkpoint
	return true, nil
}

func (m *batchImageCheckpointMemory) BatchImageCheckpointCapacityAvailable(context.Context) (bool, error) {
	return true, nil
}

type checkpointIndexerRepo struct {
	*fakeBatchImageRepository
	*batchImageCheckpointMemory
}

type checkpointProviderReadFailure struct{ *fakeProcessorProvider }

func (p checkpointProviderReadFailure) OpenResult(context.Context, *BatchImageJob, *Account) (io.ReadCloser, string, error) {
	return nil, "", errors.New("the provider's source has expired")
}

func TestBatchImageRetainsFinalImageAcrossWorkerRebuildAndLostWriteReply(t *testing.T) {
	mini := newBatchImageUpscaleTestAPI()
	t.Cleanup(mini.Close)
	cfg, upscaler := newBatchImageUpscaleTestService(mini.URL(), "batch-image/delivery")
	repo := &checkpointIndexerRepo{newFakeBatchImageRepository(), newBatchImageCheckpointMemory()}
	wallet, groupID, accountID, holdAmount := int64(ImageStudioWalletOwnerSlot), int64(45), int64(249), 0.2
	job := &BatchImageJob{
		BatchID: "imgbatch_delivery_recovery", Status: BatchImageJobStatusIndexing,
		Provider: "fake", Model: "gpt-image-2.6-sunburst", ImageSize: "2K", ItemCount: 1,
		UserID: 111, APIKeyID: &wallet, StudioGroupID: &groupID, AccountID: &accountID, HoldAmount: &holdAmount,
		ProviderJobName: batchImageStringPtr("fake-job"),
	}
	repo.jobs[job.BatchID] = job
	repo.items[job.BatchID] = []CreateBatchImageItemParams{{JobID: job.BatchID, CustomID: "image-1", Status: BatchImageItemStatusPending}}
	baseProvider := &fakeProcessorProvider{result: string(batchImageUpscaleTestResultLine("image-1", batchImageUpscaleTestPNG(2, 3)))}
	billing := &fakeBatchImageBillingRepo{}
	cfg.BatchImage.OutputRetentionAfterTerminalHours = 360
	settlement := &BatchImageSettlementService{Repo: repo, BillingRepo: billing, Pricing: &fakeBatchImagePricingResolver{unitPrice: holdAmount}, Config: cfg}
	store := &scriptedBatchImageUpscalePutStore{batchImageUpscaleTestStore: newBatchImageUpscaleTestStore()}
	var attempts int
	var originalKey string
	var originalBytes []byte
	store.put = func(ctx context.Context, key, mime string, body io.Reader, size int64) error {
		attempts++
		payload, err := io.ReadAll(body)
		require.NoError(t, err)
		require.Len(t, payload, int(size))
		if attempts == 1 {
			originalKey, originalBytes = key, payload
			return errors.New("COS unavailable")
		}
		require.Equal(t, originalKey, key)
		require.Equal(t, originalBytes, payload)
		require.NoError(t, store.batchImageUpscaleTestStore.Put(ctx, key, mime, bytes.NewReader(payload), size))
		if attempts == 2 {
			return errors.New("reply lost after successful write")
		}
		return nil
	}
	for pass := range 3 {
		indexer := &BatchImageResultIndexer{Repo: repo, Config: cfg, Upscaler: upscaler, UpscaleStore: store}
		var provider BatchImageProvider = baseProvider
		if pass > 0 {
			indexer.Upscaler = nil
			provider = checkpointProviderReadFailure{baseProvider}
		}
		worker := &BatchImagePipelineProcessor{
			ProviderProcessor: &BatchImageProviderProcessor{Repo: repo, ProviderRegistry: NewBatchImageProviderRegistry(provider),
				AccountResolver: &fakeBatchImageAccountResolver{account: &Account{ID: accountID}}, Indexer: indexer, BillingRepo: billing},
			SettlementService: settlement,
		}
		result, err := worker.Process(t.Context(), job.BatchID)
		require.NoError(t, err)
		require.False(t, result.Terminal)
		require.Empty(t, billing.captures)
		require.Empty(t, billing.releases)
		if pass < 2 {
			require.Positive(t, result.RequeueAfter)
			require.Equal(t, BatchImageJobStatusIndexing, job.Status)
			require.Nil(t, job.OutputExpiresAt)
			require.Zero(t, repo.replaceCalls)
			checkpoint, getErr := repo.GetBatchImageCheckpoint(t.Context(), job.BatchID, "image-1", BatchImageCheckpointFinal)
			require.NoError(t, getErr)
			require.NotNil(t, checkpoint)
			require.Equal(t, originalBytes, checkpoint.Data)
			continue
		}
		require.Equal(t, BatchImageJobStatusSettling, job.Status)
		result, err = worker.Process(t.Context(), job.BatchID)
		require.NoError(t, err)
		require.True(t, result.Terminal)
	}
	require.Equal(t, BatchImageJobStatusCompleted, job.Status)
	settled, err := settlement.Settle(t.Context(), job.BatchID)
	require.NoError(t, err)
	require.True(t, settled.AlreadySettled)
	require.Len(t, billing.captures, 1)
	require.InDelta(t, holdAmount, billing.captures[0].ActualAmount, 1e-9)
	require.Equal(t, BatchImageCaptureRequestID(job.BatchID), billing.captures[0].RequestID)
	require.Equal(t, 360*time.Hour, job.OutputExpiresAt.Sub(*job.SettledAt))
	require.Equal(t, []int{2}, mini.SubmittedScales())
	require.Equal(t, 1, repo.replaceCalls)
	require.Len(t, store.objects, 1)
	require.Equal(t, originalBytes, store.objects[originalKey].data)
}

type checkpointArchiveStore struct {
	*fakeOpenAIImageBatchStore
	archiveWrites int
}

func (s *checkpointArchiveStore) Put(ctx context.Context, key, mime string, body io.Reader, size int64) error {
	if strings.HasSuffix(key, "/output/results.jsonl") {
		s.archiveWrites++
		if s.archiveWrites <= 2 {
			return errors.New("COS result archive unavailable")
		}
	}
	return s.fakeOpenAIImageBatchStore.Put(ctx, key, mime, body, size)
}

func TestBatchImageRetainsProviderResultAcrossArchiveFailuresAndCancellation(t *testing.T) {
	ctx := t.Context()
	store := &checkpointArchiveStore{fakeOpenAIImageBatchStore: newFakeOpenAIImageBatchStore()}
	executor := &fakeOpenAIImageBatchExecutor{}
	checkpoints := newBatchImageCheckpointMemory()
	provider := NewOpenAIImagesBatchProvider(store, executor, testOpenAIImageBatchObjectPrefix)
	provider.checkpoints = checkpoints
	account := &Account{ID: 249, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	job := &BatchImageJob{BatchID: "imgbatch_aaaaaa6789abcdef0123456789abcdef", ItemCount: 2}
	input := BatchImageInput{BatchID: job.BatchID, Model: "gpt-image-2.6-sunburst", Items: []BatchImageInputItem{
		{CustomID: "first", Prompt: "one image"}, {CustomID: "second", Prompt: "do not submit after cancel"},
	}}
	created, err := provider.Submit(ctx, job, account, input)
	require.NoError(t, err)
	job.ProviderJobName, job.ProviderInputRef = &created.ProviderJobName, &created.ProviderInputRef
	_, err = provider.Advance(ctx, job, account)
	require.NoError(t, err)
	staged, err := checkpoints.GetBatchImageCheckpoint(ctx, job.BatchID, "first", BatchImageCheckpointSource)
	require.NoError(t, err)
	require.NotNil(t, staged)
	resultPut, err := store.Exists(ctx, provider.itemResultKey(job.BatchID, 0))
	require.NoError(t, err)
	require.False(t, resultPut, "recovery must not depend on a successful item-level COS write")
	require.NoError(t, provider.PersistCancelIntent(ctx, job))
	for attempt := range 3 {
		provider = NewOpenAIImagesBatchProvider(store, executor, testOpenAIImageBatchObjectPrefix)
		provider.checkpoints = checkpoints
		status, err := provider.Advance(ctx, job, account)
		if attempt < 2 {
			var failure *batchImageDeliveryFailure
			require.ErrorAs(t, err, &failure)
			require.Equal(t, "provider_archive", failure.stage)
			require.Nil(t, status)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, BatchProviderStateSucceeded, status.InternalState)
		job.ProviderOutputRef = &status.ProviderOutputRef
		body, _, err := provider.OpenResult(ctx, job, account)
		require.NoError(t, err)
		payload, err := io.ReadAll(body)
		require.NoError(t, err)
		require.NoError(t, body.Close())
		require.True(t, bytes.HasPrefix(payload, staged.Data))
		lines := bytes.Split(bytes.TrimSpace(payload), []byte("\n"))
		require.Len(t, lines, 2)
		cancelled, err := ParseBatchImageResultLine(lines[1], 2)
		require.NoError(t, err)
		require.Equal(t, "second", cancelled.CustomID)
		require.Equal(t, BatchImageParsedStatusFailed, cancelled.Status)
	}
	require.Len(t, executor.requests, 1, "neither archive retries nor cancellation may send another paid request")
}
