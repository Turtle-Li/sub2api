//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestBatchImageCleanupService_DeleteOutputsForOwner(t *testing.T) {
	ctx := context.Background()

	t.Run("deletes completed output and returns public dto", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()

		got, err := svc.DeleteOutputsForOwner(ctx, testBatchImageOwner(), "imgbatch_cleanup")
		require.NoError(t, err)
		require.Equal(t, "output_deleted", got.Status)
		require.NotNil(t, got.OutputDeletedAt)
		require.Equal(t, []CleanupTarget{CleanupTargetOutput}, provider.cleanupTargets)
		require.NotNil(t, repo.jobs["imgbatch_cleanup"].OutputDeletedAt)
		require.Equal(t, BatchImageJobStatusOutputDeleted, repo.jobs["imgbatch_cleanup"].Status)
		body := mustJSON(t, got)
		requireBatchImagePublicJSONHasNoInternals(t, body)
	})

	t.Run("rejects high-resolution deletion during stale-write fence", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()
		job := repo.jobs["imgbatch_cleanup"]
		job.ImageSize = "2K"
		now := time.Now()
		job.FinishedAt = &now

		_, err := svc.DeleteOutputsForOwner(ctx, testBatchImageOwner(), job.BatchID)
		require.ErrorIs(t, err, ErrBatchImageOutputDeleteNotReady)
		require.Nil(t, job.OutputDeletedAt)
		require.Empty(t, provider.cleanupTargets)
	})

	t.Run("repeated delete is idempotent", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()
		deletedAt := time.Now()
		repo.jobs["imgbatch_cleanup"].Status = BatchImageJobStatusOutputDeleted
		repo.jobs["imgbatch_cleanup"].OutputDeletedAt = &deletedAt

		got, err := svc.DeleteOutputsForOwner(ctx, testBatchImageOwner(), "imgbatch_cleanup")
		require.NoError(t, err)
		require.Equal(t, "output_deleted", got.Status)
		require.Empty(t, provider.cleanupTargets)
	})

	t.Run("not completed returns not ready", func(t *testing.T) {
		svc, repo, _ := newTestBatchImageCleanupService()
		repo.jobs["imgbatch_cleanup"].Status = BatchImageJobStatusRunning

		_, err := svc.DeleteOutputsForOwner(ctx, testBatchImageOwner(), "imgbatch_cleanup")
		require.ErrorIs(t, err, ErrBatchImageOutputDeleteNotReady)
	})

	t.Run("non owner returns not found", func(t *testing.T) {
		svc, _, _ := newTestBatchImageCleanupService()
		_, err := svc.DeleteOutputsForOwner(ctx, BatchImageOwner{UserID: 11, APIKeyID: 999}, "imgbatch_cleanup")
		require.ErrorIs(t, err, ErrBatchImageJobNotFound)
	})

	t.Run("provider not found is success", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()
		provider.cleanupErr = infraerrors.New(404, "PROVIDER_NOT_FOUND", "provider file not found: gs://hidden")

		got, err := svc.DeleteOutputsForOwner(ctx, testBatchImageOwner(), "imgbatch_cleanup")
		require.NoError(t, err)
		require.Equal(t, "output_deleted", got.Status)
		require.NotNil(t, repo.jobs["imgbatch_cleanup"].OutputDeletedAt)
	})

	t.Run("provider transient error is sanitized and records failure", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()
		provider.cleanupErr = errors.New("temporary cleanup failed for gs://secret-output")

		_, err := svc.DeleteOutputsForOwner(ctx, testBatchImageOwner(), "imgbatch_cleanup")
		require.ErrorIs(t, err, ErrBatchImageProviderCleanupFailed)
		require.Equal(t, "BATCH_IMAGE_PROVIDER_CLEANUP_FAILED", infraerrors.Reason(err))
		require.NotContains(t, infraerrors.Message(err), "gs://")
		require.Equal(t, "BATCH_IMAGE_PROVIDER_CLEANUP_FAILED", batchImageDerefString(repo.jobs["imgbatch_cleanup"].LastErrorCode))
		require.Equal(t, "upstream provider operation failed", batchImageDerefString(repo.jobs["imgbatch_cleanup"].LastErrorMessage))
	})

	t.Run("unsafe cleanup path is not swallowed", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()
		provider.cleanupErr = ErrBatchImageProviderUnsafeCleanupPath

		_, err := svc.DeleteOutputsForOwner(ctx, testBatchImageOwner(), "imgbatch_cleanup")
		require.ErrorIs(t, err, ErrBatchImageCleanupUnsafePath)
		require.Equal(t, "BATCH_IMAGE_CLEANUP_UNSAFE_PATH", batchImageDerefString(repo.jobs["imgbatch_cleanup"].LastErrorCode))
	})
}

func TestBatchImageCleanupService_CleanupUpscaledObjectsSweepsDeterministicRecoveryKeys(t *testing.T) {
	ctx := context.Background()
	svc, repo, provider := newTestBatchImageCleanupService()
	job := repo.jobs["imgbatch_cleanup"]
	job.ImageSize = "2K"
	job.ItemCount = 3
	job.SuccessCount = 2
	job.FailCount = 1
	svc.Config.BatchImage.DeliveryCOSPrefix = "batch-image/delivery"
	store := newBatchImageUpscaleObjectStoreTest()
	svc.DeliveryStore = store

	marker := batchImageUpscaleMarker
	normalSource := "provider/results/not-upscaled.jsonl"
	png := "image/png"
	pngExtension := "png"
	repo.items[job.BatchID] = []CreateBatchImageItemParams{
		{
			JobID:                job.BatchID,
			CustomID:             "marked-item",
			Status:               BatchImageItemStatusSuccess,
			ProviderSourceObject: &marker,
			MimeType:             &png,
			FileExtension:        &pngExtension,
			ImageCount:           1,
		},
		{
			JobID:                job.BatchID,
			CustomID:             "normal-success",
			Status:               BatchImageItemStatusSuccess,
			ProviderSourceObject: &normalSource,
			MimeType:             &png,
			FileExtension:        &pngExtension,
			ImageCount:           1,
		},
		{
			JobID:                job.BatchID,
			CustomID:             "marked-failure",
			Status:               BatchImageItemStatusFailed,
			ProviderSourceObject: &marker,
			MimeType:             &png,
			FileExtension:        &pngExtension,
			ImageCount:           1,
		},
	}

	markedFirstKey, err := batchImageUpscaleObjectKey(svc.Config, job.BatchID, "marked-item", 0, pngExtension)
	require.NoError(t, err)
	normalSuccessKey, err := batchImageUpscaleObjectKey(svc.Config, job.BatchID, "normal-success", 0, pngExtension)
	require.NoError(t, err)
	failedMarkedKey, err := batchImageUpscaleObjectKey(svc.Config, job.BatchID, "marked-failure", 0, pngExtension)
	require.NoError(t, err)
	store.setObject(markedFirstKey, png, []byte("first"))
	store.setObject(normalSuccessKey, png, []byte("normal"))
	store.setObject(failedMarkedKey, png, []byte("failed"))
	store.setObject("batch-image/delivery/unrelated.png", png, []byte("unrelated"))

	err = svc.CleanupOutput(ctx, job.BatchID, "ttl")
	require.NoError(t, err)
	require.Len(t, store.deletes, 1)
	require.Contains(t, store.deletes[0], markedFirstKey)
	require.Contains(t, store.deletes[0], normalSuccessKey)
	require.Contains(t, store.deletes[0], failedMarkedKey)
	require.NotContains(t, store.objects, markedFirstKey)
	require.NotContains(t, store.objects, normalSuccessKey)
	require.NotContains(t, store.objects, failedMarkedKey)
	require.Contains(t, store.objects, "batch-image/delivery/unrelated.png")
	require.Equal(t, []CleanupTarget{CleanupTargetOutput}, provider.cleanupTargets)
	require.NotNil(t, job.OutputDeletedAt)
}

func TestBatchImageCleanupService_UpscaleDeleteFailureRetriesWithoutMarkingDeleted(t *testing.T) {
	ctx := context.Background()
	svc, repo, provider := newTestBatchImageCleanupService()
	job := repo.jobs["imgbatch_cleanup"]
	job.ImageSize = "2K"
	svc.Config.BatchImage.DeliveryCOSPrefix = "batch-image/delivery"
	store := newBatchImageUpscaleObjectStoreTest()
	store.deleteErr = errors.New("temporary COS delete failure")
	svc.DeliveryStore = store
	repo.items[job.BatchID] = []CreateBatchImageItemParams{{
		JobID: job.BatchID, CustomID: "recoverable-item", Status: BatchImageItemStatusFailed,
	}}
	key, err := batchImageUpscaleObjectKey(svc.Config, job.BatchID, "recoverable-item", 0, "png")
	require.NoError(t, err)
	store.setObject(key, "image/png", []byte("recoverable"))

	err = svc.CleanupOutput(ctx, job.BatchID, "ttl")
	require.ErrorIs(t, err, ErrBatchImageProviderCleanupFailed)
	require.Equal(t, batchImageUpscaleDeleteAttempts, store.deleteCalls)
	require.Nil(t, job.OutputDeletedAt)
	require.Contains(t, store.objects, key)
	require.Empty(t, provider.cleanupTargets)

	store.deleteErr = nil
	err = svc.CleanupOutput(ctx, job.BatchID, "ttl-retry")
	require.NoError(t, err)
	require.Equal(t, batchImageUpscaleDeleteAttempts+1, store.deleteCalls)
	require.NotNil(t, job.OutputDeletedAt)
	require.NotContains(t, store.objects, key)
	require.Equal(t, []CleanupTarget{CleanupTargetOutput}, provider.cleanupTargets)
}

func TestBatchImageCleanupService_InputOutputAndWorker(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	t.Run("input cleanup marks input only", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()

		err := svc.CleanupInput(ctx, "imgbatch_cleanup")
		require.NoError(t, err)
		require.Equal(t, []CleanupTarget{CleanupTargetInput}, provider.cleanupTargets)
		require.NotNil(t, repo.jobs["imgbatch_cleanup"].InputDeletedAt)
		require.Equal(t, BatchImageJobStatusCompleted, repo.jobs["imgbatch_cleanup"].Status)

		err = svc.CleanupInput(ctx, "imgbatch_cleanup")
		require.NoError(t, err)
		require.Len(t, provider.cleanupTargets, 1)
	})

	t.Run("output cleanup for failed job keeps status", func(t *testing.T) {
		svc, repo, _ := newTestBatchImageCleanupService()
		repo.jobs["imgbatch_cleanup"].Status = BatchImageJobStatusFailed

		err := svc.CleanupOutput(ctx, "imgbatch_cleanup", "ttl")
		require.NoError(t, err)
		require.Equal(t, BatchImageJobStatusFailed, repo.jobs["imgbatch_cleanup"].Status)
		require.NotNil(t, repo.jobs["imgbatch_cleanup"].OutputDeletedAt)
	})

	t.Run("worker processes due jobs and continues after failure", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()
		provider.cleanupErr = nil
		old := now.Add(-48 * time.Hour)
		expired := now.Add(-time.Minute)
		future := now.Add(time.Hour)
		repo.jobs["imgbatch_cleanup"].FinishedAt = &old
		repo.jobs["imgbatch_cleanup"].OutputExpiresAt = &expired
		repo.jobs["imgbatch_running"] = cleanupTestJob("imgbatch_running", BatchImageJobStatusRunning)
		repo.jobs["imgbatch_running"].FinishedAt = &old
		repo.jobs["imgbatch_running"].OutputExpiresAt = &expired
		repo.jobs["imgbatch_future"] = cleanupTestJob("imgbatch_future", BatchImageJobStatusCompleted)
		repo.jobs["imgbatch_future"].FinishedAt = &old
		repo.jobs["imgbatch_future"].OutputExpiresAt = &future

		result, err := svc.RunOnce(ctx, now)
		require.NoError(t, err)
		require.Equal(t, 2, result.InputCleaned)
		require.Equal(t, 1, result.OutputCleaned)
		require.Equal(t, BatchImageJobStatusRunning, repo.jobs["imgbatch_running"].Status)
		require.Nil(t, repo.jobs["imgbatch_future"].OutputDeletedAt)
		require.NotContains(t, strings.Join(repo.events["imgbatch_running"], ","), "cleanup")
	})
}

func TestBatchImageCleanupService_OpenAIDeterministicCleanupSurvivesDeletedAccount(t *testing.T) {
	ctx := context.Background()
	repo := newFakeBatchImageRepository()
	store := newFakeOpenAIImageBatchStore()
	provider := NewOpenAIImagesBatchProvider(store, nil, testOpenAIImageBatchObjectPrefix)
	accountID := int64(404)
	batchID := "imgbatch_deleted_account_cleanup"
	now := time.Now().Add(-48 * time.Hour)
	job := &BatchImageJob{
		BatchID:           batchID,
		AccountID:         &accountID,
		Provider:          BatchImageProviderOpenAI,
		Model:             "gpt-image-2.5-sunburst",
		ImageSize:         "1K",
		Status:            BatchImageJobStatusCompleted,
		ProviderJobName:   batchImageStringPtr(batchImageOpenAIProviderJobPrefix + batchID),
		ProviderInputRef:  batchImageStringPtr(provider.inputKey(batchID)),
		ProviderOutputRef: batchImageStringPtr(provider.outputKey(batchID)),
		ItemCount:         1,
		SuccessCount:      1,
		FinishedAt:        &now,
	}
	repo.jobs[batchID] = job

	objects := map[string][]byte{
		provider.inputKey(batchID):          []byte(`{"version":1}`),
		provider.outputKey(batchID):         []byte("result\n"),
		provider.cancelKey(batchID):         []byte("cancelled"),
		provider.itemAttemptKey(batchID, 0): []byte("started"),
		provider.itemResultKey(batchID, 0):  []byte("item\n"),
	}
	for key, payload := range objects {
		require.NoError(t, store.Put(ctx, key, "application/octet-stream", strings.NewReader(string(payload)), int64(len(payload))))
	}

	svc := &BatchImageCleanupService{
		Repo:             repo,
		ProviderRegistry: NewBatchImageProviderRegistry(provider),
		AccountResolver:  &fakeBatchImageAccountResolver{err: ErrBatchImageJobNotFound},
	}
	require.NoError(t, svc.CleanupInput(ctx, batchID))
	require.NoError(t, svc.CleanupOutput(ctx, batchID, "expired"))
	require.NotNil(t, job.InputDeletedAt)
	require.NotNil(t, job.OutputDeletedAt)
	for key := range objects {
		exists, err := store.Exists(ctx, key)
		require.NoError(t, err)
		require.False(t, exists, "cleanup left object %s", key)
	}
}

func TestBatchImageSettlementOutputExpiration(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_expire")
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{}
	svc := &BatchImageSettlementService{
		Repo:        repo,
		BillingRepo: billing,
		Pricing:     &fakeBatchImagePricingResolver{unitPrice: 0.25},
		Config:      &config.Config{BatchImage: config.BatchImageConfig{OutputRetentionAfterTerminalHours: 5}},
	}

	_, err := svc.Settle(context.Background(), job.BatchID)
	require.NoError(t, err)
	require.NotNil(t, repo.jobs[job.BatchID].OutputExpiresAt)
	require.WithinDuration(t, time.Now().Add(5*time.Hour), *repo.jobs[job.BatchID].OutputExpiresAt, time.Minute)

	existing := time.Now().Add(time.Hour)
	second := testSettlingBatchImageJob("imgbatch_keep_expire")
	second.OutputExpiresAt = &existing
	repo.jobs[second.BatchID] = second
	_, err = svc.Settle(context.Background(), second.BatchID)
	require.NoError(t, err)
	require.Equal(t, existing, *repo.jobs[second.BatchID].OutputExpiresAt)
}

func TestBatchImageDownloadAfterOutputDeletedReturnsGone(t *testing.T) {
	svc, repo, _ := newTestBatchImageDownloadService()
	now := time.Now()
	repo.jobs["imgbatch_download"].Status = BatchImageJobStatusOutputDeleted
	repo.jobs["imgbatch_download"].OutputDeletedAt = &now

	stream, err := svc.OpenItemContent(context.Background(), testBatchImageOwner(), "imgbatch_download", "cover/../001", 0)
	require.Nil(t, stream)
	require.ErrorIs(t, err, ErrBatchImageOutputDeleted)

	var out strings.Builder
	result, err := svc.StreamZip(context.Background(), testBatchImageOwner(), "imgbatch_download", BatchImageZipOptions{}, &out)
	require.Nil(t, result)
	require.ErrorIs(t, err, ErrBatchImageOutputDeleted)
}

func newTestBatchImageCleanupService() (*BatchImageCleanupService, *fakeBatchImageRepository, *publicBatchImageProvider) {
	repo := newFakeBatchImageRepository()
	repo.jobs["imgbatch_cleanup"] = cleanupTestJob("imgbatch_cleanup", BatchImageJobStatusCompleted)
	provider := &publicBatchImageProvider{name: BatchImageProviderGeminiAPI}
	accountID := int64(101)
	svc := &BatchImageCleanupService{
		Repo:             repo,
		ProviderRegistry: NewBatchImageProviderRegistry(provider),
		AccountResolver:  &fakeBatchImageAccountResolver{account: &Account{ID: accountID, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}},
		Config:           &config.Config{BatchImage: config.BatchImageConfig{CleanupBatchSize: 10, InputRetentionAfterTerminalHours: 24}},
	}
	return svc, repo, provider
}

func cleanupTestJob(batchID, status string) *BatchImageJob {
	apiKeyID := int64(22)
	accountID := int64(101)
	now := time.Now().Add(-48 * time.Hour)
	return &BatchImageJob{
		BatchID:           batchID,
		UserID:            11,
		APIKeyID:          &apiKeyID,
		AccountID:         &accountID,
		Provider:          BatchImageProviderGeminiAPI,
		Model:             "gemini-2.5-flash-image",
		Status:            status,
		ProviderJobName:   batchImageStringPtr("providers/internal/job"),
		ProviderInputRef:  batchImageStringPtr("files/internal/input"),
		ProviderOutputRef: batchImageStringPtr("files/internal/output"),
		ItemCount:         1,
		SuccessCount:      1,
		CreatedAt:         now,
		UpdatedAt:         now,
		FinishedAt:        &now,
		SettledAt:         &now,
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
