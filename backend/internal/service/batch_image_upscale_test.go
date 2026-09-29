//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type noopBatchImageUpscaleWriteFencer struct{}

func (noopBatchImageUpscaleWriteFencer) BeginBatchImageUpscaleWrite(context.Context, string) (BatchImageUpscaleWritePermit, error) {
	return noopBatchImageUpscaleWritePermit{}, nil
}

type lockingBatchImageUpscaleWriteFencer struct {
	mu     sync.Mutex
	status string
}

type lockingBatchImageUpscaleWritePermit struct {
	fencer *lockingBatchImageUpscaleWriteFencer
	once   sync.Once
}

func (f *lockingBatchImageUpscaleWriteFencer) BeginBatchImageUpscaleWrite(context.Context, string) (BatchImageUpscaleWritePermit, error) {
	f.mu.Lock()
	if f.status != BatchImageJobStatusIndexing {
		f.mu.Unlock()
		return nil, ErrBatchImageIndexStateConflict
	}
	return &lockingBatchImageUpscaleWritePermit{fencer: f}, nil
}

func (p *lockingBatchImageUpscaleWritePermit) Release() error {
	p.once.Do(func() { p.fencer.mu.Unlock() })
	return nil
}

func (f *lockingBatchImageUpscaleWriteFencer) terminalCleanup(cleanup func() error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = BatchImageJobStatusCancelled
	return cleanup()
}

type pausedBatchImageUpscalePutStore struct {
	*batchImageUpscaleTestStore
	putStarted chan struct{}
	resumePut  chan struct{}
	startOnce  sync.Once
}

func (s *pausedBatchImageUpscalePutStore) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	s.startOnce.Do(func() { close(s.putStarted) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.resumePut:
	}
	return s.batchImageUpscaleTestStore.Put(ctx, key, contentType, body, size)
}

func TestBatchImageUpscaleObjectKey_DeterministicAndSafe(t *testing.T) {
	cfg := &config.Config{BatchImage: config.BatchImageConfig{DeliveryCOSPrefix: "batch-image/delivery"}}
	customID := "tenant/../../not-a-path"

	first, err := batchImageUpscaleObjectKey(cfg, "imgbatch_key", customID, 0, ".PNG")
	require.NoError(t, err)
	second, err := batchImageUpscaleObjectKey(cfg, "imgbatch_key", customID, 0, "png")
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.True(t, strings.HasPrefix(first, "batch-image/delivery/upscaled/imgbatch_key/"))
	require.True(t, strings.HasSuffix(first, "/00.png"))
	require.NotContains(t, first, customID)
	require.NotContains(t, first, "..")

	differentID, err := batchImageUpscaleObjectKey(cfg, "imgbatch_key", "another-item", 0, "png")
	require.NoError(t, err)
	require.NotEqual(t, first, differentID)

	_, err = batchImageUpscaleObjectKey(cfg, "../escape", "item", 0, "png")
	require.Error(t, err, "batch IDs must not be able to traverse outside the delivery prefix")
	_, err = batchImageUpscaleObjectKey(cfg, "imgbatch_key", "item", -1, "png")
	require.Error(t, err)
	_, err = batchImageUpscaleObjectKey(cfg, "imgbatch_key", "item", 0, "svg")
	require.Error(t, err)
	_, err = batchImageUpscaleObjectKey(&config.Config{BatchImage: config.BatchImageConfig{DeliveryCOSPrefix: "../delivery"}}, "imgbatch_key", "item", 0, "png")
	require.Error(t, err)
}

func TestIsBatchImageUpscaledItem_Marker(t *testing.T) {
	marker := " " + batchImageUpscaleMarker + " "
	normalSource := "provider/results/job-1.jsonl"

	require.True(t, isBatchImageUpscaledItem(&BatchImageItem{ProviderSourceObject: &marker}))
	require.False(t, isBatchImageUpscaledItem(nil))
	require.False(t, isBatchImageUpscaledItem(&BatchImageItem{}))
	require.False(t, isBatchImageUpscaledItem(&BatchImageItem{ProviderSourceObject: &normalSource}))
}

func TestBatchImageResultIndexer_StoresSingle2KAnd4KResultsWithMarker(t *testing.T) {
	for _, tt := range []struct {
		name  string
		size  string
		scale int
	}{
		{name: "2K", size: "2K", scale: 2},
		{name: "4K", size: "4K", scale: 4},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upscaleAPI := newBatchImageUpscaleTestAPI()
			t.Cleanup(upscaleAPI.Close)
			cfg, upscaler := newBatchImageUpscaleTestService(upscaleAPI.URL(), "batch-image/delivery")
			store := newBatchImageUpscaleTestStore()
			source := batchImageUpscaleTestPNG(3, 2)
			outputRef := "provider/results/output.jsonl"
			job := &BatchImageJob{
				BatchID:           "imgbatch_" + strings.ToLower(tt.size),
				Model:             "gemini-2.5-flash-image",
				ImageSize:         tt.size,
				ProviderOutputRef: &outputRef,
			}
			provider := &fakeProcessorProvider{result: string(batchImageUpscaleTestResultLine("image_"+strings.ToLower(tt.size), source)) + "\n"}
			repo := newFakeBatchImageRepository()

			result, err := (&BatchImageResultIndexer{
				Repo:         repo,
				Config:       cfg,
				Upscaler:     upscaler,
				UpscaleStore: store,
			}).Index(context.Background(), job, provider, &Account{})
			require.NoError(t, err)
			require.Equal(t, &BatchImageIndexResult{SuccessCount: 1, TotalCount: 1}, result)
			require.Len(t, repo.items[job.BatchID], 1)

			item := repo.items[job.BatchID][0]
			require.Equal(t, BatchImageItemStatusSuccess, item.Status)
			require.Equal(t, batchImageUpscaleMarker, batchImageDerefString(item.ProviderSourceObject))
			require.Equal(t, "image/png", batchImageDerefString(item.MimeType))
			require.Equal(t, "png", batchImageDerefString(item.FileExtension))
			require.Equal(t, 1, item.ImageCount)

			key, err := batchImageUpscaleObjectKey(cfg, job.BatchID, item.CustomID, 0, "png")
			require.NoError(t, err)
			stored, ok := store.objects[key]
			require.True(t, ok)
			require.Equal(t, "image/png", stored.contentType)
			decoded, _, err := image.DecodeConfig(bytes.NewReader(stored.data))
			require.NoError(t, err)
			require.Equal(t, 3*tt.scale, decoded.Width)
			require.Equal(t, 2*tt.scale, decoded.Height)
			require.Equal(t, []int{tt.scale}, upscaleAPI.SubmittedScales())
			require.Empty(t, store.deletes)
		})
	}
}

func TestBatchImageResultIndexerSkipsMiniWhenOutputAlreadyMeetsRequestedTier(t *testing.T) {
	upscaleAPI := newBatchImageUpscaleTestAPI()
	t.Cleanup(upscaleAPI.Close)
	cfg, upscaler := newBatchImageUpscaleTestService(upscaleAPI.URL(), "batch-image/delivery")
	store := newBatchImageUpscaleTestStore()
	source := batchImageUpscaleTestPNG(2048, 1024)
	outputRef := "provider/results/output.jsonl"
	job := &BatchImageJob{
		BatchID:           "imgbatch_native_2k",
		Model:             "future-image-3",
		ImageSize:         "2K",
		ProviderOutputRef: &outputRef,
	}
	provider := &fakeProcessorProvider{result: string(batchImageUpscaleTestResultLine("image_native_2k", source)) + "\n"}
	repo := newFakeBatchImageRepository()

	result, err := (&BatchImageResultIndexer{
		Repo: repo, Config: cfg, Upscaler: upscaler, UpscaleStore: store,
	}).Index(context.Background(), job, provider, &Account{})
	require.NoError(t, err)
	require.Equal(t, &BatchImageIndexResult{SuccessCount: 1, TotalCount: 1}, result)
	require.Empty(t, upscaleAPI.SubmittedScales(), "actual 2K bytes must bypass Mini regardless of model name")
	item := repo.items[job.BatchID][0]
	key, err := batchImageUpscaleObjectKey(cfg, job.BatchID, item.CustomID, 0, "png")
	require.NoError(t, err)
	stored := store.objects[key]
	decoded, _, err := image.DecodeConfig(bytes.NewReader(stored.data))
	require.NoError(t, err)
	require.Equal(t, 2048, decoded.Width)
	require.Equal(t, 1024, decoded.Height)
}

func TestBatchImageUpscaleWriteFenceSerializesTerminalCleanupAndRejectsPostCleanupPut(t *testing.T) {
	upscaleAPI := newBatchImageUpscaleTestAPI()
	t.Cleanup(upscaleAPI.Close)
	cfg, upscaler := newBatchImageUpscaleTestService(upscaleAPI.URL(), "batch-image/delivery")
	baseStore := newBatchImageUpscaleTestStore()
	store := &pausedBatchImageUpscalePutStore{
		batchImageUpscaleTestStore: baseStore,
		putStarted:                 make(chan struct{}),
		resumePut:                  make(chan struct{}),
	}
	fencer := &lockingBatchImageUpscaleWriteFencer{status: BatchImageJobStatusIndexing}
	job := &BatchImageJob{BatchID: "imgbatch_fenced_put", Model: "gemini-2.5-flash-image", ImageSize: "2K"}
	const customID = "item_fenced_put"
	line := batchImageUpscaleTestResultLine(customID, batchImageUpscaleTestPNG(2, 2))
	key, err := batchImageUpscaleObjectKey(cfg, job.BatchID, customID, 0, "png")
	require.NoError(t, err)

	firstResult := make(chan error, 1)
	go func() {
		_, _, _, _, callErr := upscaleBatchImageResultLine(context.Background(), cfg, upscaler, store, fencer, job, line)
		firstResult <- callErr
	}()
	select {
	case <-store.putStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("COS Put did not start")
	}

	cleanupDone := make(chan error, 1)
	go func() {
		cleanupDone <- fencer.terminalCleanup(func() error {
			return baseStore.Delete(context.Background(), []string{key})
		})
	}()
	select {
	case <-cleanupDone:
		t.Fatal("terminal cleanup crossed an active transactional Put fence")
	case <-time.After(50 * time.Millisecond):
	}

	close(store.resumePut)
	require.NoError(t, <-firstResult)
	select {
	case cleanupErr := <-cleanupDone:
		require.NoError(t, cleanupErr)
	case <-time.After(2 * time.Second):
		t.Fatal("terminal cleanup did not resume after Put released the fence")
	}
	require.Empty(t, baseStore.objects)
	require.Len(t, baseStore.puts, 1)

	_, _, _, _, err = upscaleBatchImageResultLine(context.Background(), cfg, upscaler, store, fencer, job, line)
	require.ErrorIs(t, err, ErrBatchImageIndexStateConflict)
	require.Len(t, baseStore.puts, 1, "a stale worker must be rejected before its post-cleanup Put")
	require.Empty(t, baseStore.objects)
}

func TestBatchImageResultIndexer_GivesEachItemItsOwnJobDeadline(t *testing.T) {
	upscaleAPI := newBatchImageUpscaleTestAPI()
	upscaleAPI.resultDelay = 600 * time.Millisecond
	t.Cleanup(upscaleAPI.Close)
	cfg, upscaler := newBatchImageUpscaleTestService(upscaleAPI.URL(), "batch-image/delivery")
	upscaler.cfg.JobTimeoutSeconds = 1
	store := newBatchImageUpscaleTestStore()
	repo := newFakeBatchImageRepository()
	job := &BatchImageJob{BatchID: "imgbatch_shared_deadline", Model: "gemini-2.5-flash-image", ImageSize: "2K"}
	repo.items[job.BatchID] = []CreateBatchImageItemParams{
		{JobID: job.BatchID, CustomID: "first", Status: BatchImageItemStatusPending},
		{JobID: job.BatchID, CustomID: "second", Status: BatchImageItemStatusPending},
	}
	source := batchImageUpscaleTestPNG(2, 2)
	provider := &fakeProcessorProvider{result: strings.Join([]string{
		string(batchImageUpscaleTestResultLine("first", source)),
		string(batchImageUpscaleTestResultLine("second", source)),
	}, "\n") + "\n"}
	started := time.Now()

	result, err := (&BatchImageResultIndexer{
		Repo: repo, Config: cfg, Upscaler: upscaler, UpscaleStore: store,
	}).Index(context.Background(), job, provider, &Account{})
	require.NoError(t, err)
	require.Equal(t, &BatchImageIndexResult{SuccessCount: 2, FailCount: 0, TotalCount: 2}, result)
	require.Less(t, time.Since(started), 2500*time.Millisecond)
	require.Len(t, store.objects, 2)
	require.Equal(t, []int{2, 2}, upscaleAPI.SubmittedScales())
}

func TestUpscaleBatchImageResultLine_RejectsSurplusImagesBeforeSideEffects(t *testing.T) {
	upscaleAPI := newBatchImageUpscaleTestAPI()
	t.Cleanup(upscaleAPI.Close)
	cfg, upscaler := newBatchImageUpscaleTestService(upscaleAPI.URL(), "batch-image/delivery")
	store := newBatchImageUpscaleTestStore()
	source := batchImageUpscaleTestPNG(2, 3)
	job := &BatchImageJob{BatchID: "imgbatch_surplus", Model: "gemini-2.5-flash-image", ImageSize: "2K"}
	line := batchImageUpscaleTestResultLine("two-images", source, source)

	_, _, count, keys, err := upscaleBatchImageResultLine(context.Background(), cfg, upscaler, store, noopBatchImageUpscaleWriteFencer{}, job, line)
	require.Error(t, err)
	var upscaleErr *ImageUpscaleError
	require.ErrorAs(t, err, &upscaleErr)
	require.Equal(t, "SOURCE_RESULT_INVALID", upscaleErr.Code)
	require.Zero(t, count)
	require.Nil(t, keys)
	require.Empty(t, store.puts)
	require.Empty(t, store.deletes)
	require.Empty(t, store.objects)
	require.Empty(t, upscaleAPI.SubmittedScales())
}

func TestUpscaleBatchImageResultLine_ExpiredOperationDoesNotSubmit(t *testing.T) {
	upscaleAPI := newBatchImageUpscaleTestAPI()
	t.Cleanup(upscaleAPI.Close)
	cfg, upscaler := newBatchImageUpscaleTestService(upscaleAPI.URL(), "batch-image/delivery")
	store := newBatchImageUpscaleTestStore()
	job := &BatchImageJob{BatchID: "imgbatch_expired", Model: "gemini-2.5-flash-image", ImageSize: "2K"}
	line := batchImageUpscaleTestResultLine("expired", batchImageUpscaleTestPNG(2, 3))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, count, keys, err := upscaleBatchImageResultLine(ctx, cfg, upscaler, store, noopBatchImageUpscaleWriteFencer{}, job, line)
	upscaleErr := requireImageUpscaleError(t, err, "JOB_TIMEOUT")
	require.ErrorIs(t, upscaleErr, context.Canceled)
	require.Zero(t, count)
	require.Nil(t, keys)
	require.Empty(t, store.objects)
	require.Empty(t, upscaleAPI.SubmittedScales())
}

func TestUpscaleBatchImageResultLine_BoundsBlockingCOSPut(t *testing.T) {
	upscaleAPI := newBatchImageUpscaleTestAPI()
	t.Cleanup(upscaleAPI.Close)
	cfg, upscaler := newBatchImageUpscaleTestService(upscaleAPI.URL(), "batch-image/delivery")
	store := newBatchImageUpscaleTestStore()
	store.blockPut = true
	job := &BatchImageJob{BatchID: "imgbatch_blocking_put", Model: "gemini-2.5-flash-image", ImageSize: "2K"}
	line := batchImageUpscaleTestResultLine("blocking-put", batchImageUpscaleTestPNG(2, 3))
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, _, _, _, err := upscaleBatchImageResultLine(ctx, cfg, upscaler, store, noopBatchImageUpscaleWriteFencer{}, job, line)
	require.Error(t, err)
	var upscaleErr *ImageUpscaleError
	require.ErrorAs(t, err, &upscaleErr)
	require.Equal(t, "STORE_FAILED", upscaleErr.Code)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Empty(t, store.objects)
}

func TestBatchImageResultIndexer_OverwritesDeterministicOrphanOnReindex(t *testing.T) {
	upscaleAPI := newBatchImageUpscaleTestAPI()
	t.Cleanup(upscaleAPI.Close)
	cfg, upscaler := newBatchImageUpscaleTestService(upscaleAPI.URL(), "batch-image/delivery")
	store := newBatchImageUpscaleTestStore()
	repo := newFakeBatchImageRepository()
	job := &BatchImageJob{BatchID: "imgbatch_recover", Model: "gemini-2.5-flash-image", ImageSize: "2K"}
	customID := "image_recover"
	repo.items[job.BatchID] = []CreateBatchImageItemParams{{
		JobID: job.BatchID, CustomID: customID, Status: BatchImageItemStatusPending,
	}}
	orphanKey, err := batchImageUpscaleObjectKey(cfg, job.BatchID, customID, 0, "png")
	require.NoError(t, err)
	store.objects[orphanKey] = batchImageUpscaleTestStoreObject{data: []byte("orphan"), contentType: "image/png"}
	provider := &fakeProcessorProvider{result: string(batchImageUpscaleTestResultLine(customID, batchImageUpscaleTestPNG(2, 2))) + "\n"}

	result, err := (&BatchImageResultIndexer{
		Repo: repo, Config: cfg, Upscaler: upscaler, UpscaleStore: store,
	}).Index(context.Background(), job, provider, &Account{})
	require.NoError(t, err)
	require.Equal(t, &BatchImageIndexResult{SuccessCount: 1, TotalCount: 1}, result)
	require.Empty(t, store.deletes)
	require.Contains(t, store.objects, orphanKey)
	require.NotEqual(t, []byte("orphan"), store.objects[orphanKey].data)
	require.Equal(t, 1, repo.replaceCalls)
}

func TestDeleteBatchImageUpscaleKeys_RetriesTransientFailures(t *testing.T) {
	store := newBatchImageUpscaleTestStore()
	store.deleteFailures = batchImageUpscaleDeleteAttempts - 1
	store.objects["deterministic-key"] = batchImageUpscaleTestStoreObject{data: []byte("orphan")}

	err := deleteBatchImageUpscaleKeys(context.Background(), store, []string{"deterministic-key"})
	require.NoError(t, err)
	require.Equal(t, batchImageUpscaleDeleteAttempts, store.deleteCalls)
	require.NotContains(t, store.objects, "deterministic-key")
}

func TestDeleteBatchImageUpscaleKeys_ReturnsTypedErrorAfterRetryExhaustion(t *testing.T) {
	store := newBatchImageUpscaleTestStore()
	store.deleteFailures = batchImageUpscaleDeleteAttempts
	store.objects["deterministic-key"] = batchImageUpscaleTestStoreObject{data: []byte("orphan")}

	err := deleteBatchImageUpscaleKeys(context.Background(), store, []string{"deterministic-key"})
	require.ErrorIs(t, err, ErrBatchImageUpscaleCleanupFailed)
	require.Equal(t, batchImageUpscaleDeleteAttempts, store.deleteCalls)
	require.Contains(t, store.objects, "deterministic-key")
}

func TestBatchImageResultIndexer_DoesNotUpscale1K(t *testing.T) {
	source := batchImageUpscaleTestPNG(2, 2)
	outputRef := "provider/results/output.jsonl"
	job := &BatchImageJob{
		BatchID:           "imgbatch_1k",
		Model:             "gemini-2.5-flash-image",
		ImageSize:         "1K",
		ProviderOutputRef: &outputRef,
	}
	repo := newFakeBatchImageRepository()
	provider := &fakeProcessorProvider{result: string(batchImageUpscaleTestResultLine("image_1k", source)) + "\n"}

	result, err := (&BatchImageResultIndexer{Repo: repo}).Index(context.Background(), job, provider, &Account{})
	require.NoError(t, err)
	require.Equal(t, &BatchImageIndexResult{SuccessCount: 1, TotalCount: 1}, result)
	require.False(t, batchImageJobRequiresUpscale(job))
	require.Len(t, repo.items[job.BatchID], 1)

	item := repo.items[job.BatchID][0]
	require.Equal(t, BatchImageItemStatusSuccess, item.Status)
	require.Equal(t, outputRef, batchImageDerefString(item.ProviderSourceObject))
	require.NotEqual(t, batchImageUpscaleMarker, batchImageDerefString(item.ProviderSourceObject))
	require.Equal(t, "image/png", batchImageDerefString(item.MimeType))
	require.Equal(t, "png", batchImageDerefString(item.FileExtension))
	require.Equal(t, 1, item.ImageCount)
}

func TestBatchImageSubmitValidationAllowsConfiguredGeminiImage25Upscale(t *testing.T) {
	service := &BatchImagePublicService{Config: &config.Config{ImageUpscale: config.ImageUpscaleConfig{
		Enabled:          true,
		BaseURL:          "https://upscale.example.test",
		APIKeyVaultRef:   "vault://secret/data/infrastructure/upscale#api_key",
		VaultAgentSocket: imageUpscaleVaultSocket,
	}}}
	request := validBatchImageSubmitRequest()
	request.Provider = BatchImageProviderGeminiAPI
	request.Model = "gemini-2.5-flash-image"
	request.ImageSize = "4K"

	normalized, err := service.validateSubmitRequest(request)
	require.NoError(t, err)
	require.Equal(t, "4K", normalized.ImageSize)
	require.Equal(t, BatchImageProviderGeminiAPI, normalized.Provider)
}

func TestBatchImageSubmitHighResolutionDoesNotFallbackToVertex(t *testing.T) {
	service, repo, _, gemini, vertex := newTestBatchImagePublicService(true)
	service.Config.ImageUpscale = config.ImageUpscaleConfig{
		Enabled:          true,
		BaseURL:          "https://upscale.example.test",
		APIKeyVaultRef:   "vault://secret/data/infrastructure/upscale#api_key",
		VaultAgentSocket: imageUpscaleVaultSocket,
	}
	service.ProviderRegistry = NewBatchImageProviderRegistry(vertex)
	request := validBatchImageSubmitRequest()
	request.Provider = ""
	request.Model = "gemini-2.5-flash-image"
	request.ImageSize = "2K"

	_, err := service.Submit(context.Background(), testBatchImageOwner(), request, "")
	require.ErrorIs(t, err, ErrBatchImageNoAccountAvailable)
	require.Empty(t, repo.jobs)
	require.Empty(t, gemini.submits)
	require.Empty(t, vertex.submits)
}

func TestBatchImageSubmitHighResolutionPricesRequestedTierAndSendsProvider1K(t *testing.T) {
	service, repo, _, gemini, _ := newTestBatchImagePublicService(true)
	service.Config.ImageUpscale = config.ImageUpscaleConfig{
		Enabled:          true,
		BaseURL:          "https://upscale.example.test",
		APIKeyVaultRef:   "vault://secret/data/infrastructure/upscale#api_key",
		VaultAgentSocket: imageUpscaleVaultSocket,
	}
	pricing := &fakeBatchImagePricingResolver{
		unitPricesByImageSize: map[string]float64{"1K": 0.25, "2K": 0.50},
	}
	service.Pricing = pricing
	request := validBatchImageSubmitRequest()
	request.ImageSize = "2K"
	request.AspectRatio = "16:9"

	result, err := service.Submit(context.Background(), testBatchImageOwner(), request, "")
	require.NoError(t, err)
	require.Equal(t, "2K", pricing.lastImageSize)
	require.InDelta(t, 0.50, repo.jobs[result.ID].BaseUnitPrice, 1e-12)
	require.Equal(t, "2K", repo.jobs[result.ID].ImageSize)
	require.Len(t, gemini.submits, 1)
	require.Equal(t, "1K", gemini.submits[0].ImageSize)
	require.Equal(t, "16:9", gemini.submits[0].AspectRatio)
	require.True(t, gemini.submits[0].ExplicitImageConfig)
}

func TestBatchImageSubmitHighResolutionRejectsUnsupportedImage25AspectRatio(t *testing.T) {
	service, _, _, _, _ := newTestBatchImagePublicService(true)
	service.Config.ImageUpscale = config.ImageUpscaleConfig{
		Enabled:          true,
		BaseURL:          "https://upscale.example.test",
		APIKeyVaultRef:   "vault://secret/data/infrastructure/upscale#api_key",
		VaultAgentSocket: imageUpscaleVaultSocket,
	}
	request := validBatchImageSubmitRequest()
	request.ImageSize = "2K"
	request.AspectRatio = "1:4"

	_, err := service.validateSubmitRequest(request)
	require.ErrorIs(t, err, ErrBatchImageInvalidItems)
}

func TestBatchImageResultIndexer_LeavesDeterministicObjectForBoundedCleanupWhenReplaceFails(t *testing.T) {
	upscaleAPI := newBatchImageUpscaleTestAPI()
	t.Cleanup(upscaleAPI.Close)
	cfg, upscaler := newBatchImageUpscaleTestService(upscaleAPI.URL(), "batch-image/delivery")
	store := newBatchImageUpscaleTestStore()
	repo := newFakeBatchImageRepository()
	repo.replaceErr = fmt.Errorf("replace failed")
	job := &BatchImageJob{
		BatchID:   "imgbatch_replace_failure",
		Model:     "gemini-2.5-flash-image",
		ImageSize: "2K",
	}
	provider := &fakeProcessorProvider{result: string(batchImageUpscaleTestResultLine("image_replace_failure", batchImageUpscaleTestPNG(2, 2))) + "\n"}

	_, err := (&BatchImageResultIndexer{
		Repo:         repo,
		Config:       cfg,
		Upscaler:     upscaler,
		UpscaleStore: store,
	}).Index(context.Background(), job, provider, &Account{})
	require.EqualError(t, err, "replace failed")
	require.Len(t, store.puts, 1)
	require.Empty(t, store.deletes)
	require.Contains(t, store.objects, store.puts[0])
}

func TestBatchImageResultIndexer_StaleStateConflictDoesNotDeleteWinnerKey(t *testing.T) {
	upscaleAPI := newBatchImageUpscaleTestAPI()
	t.Cleanup(upscaleAPI.Close)
	cfg, upscaler := newBatchImageUpscaleTestService(upscaleAPI.URL(), "batch-image/delivery")
	store := newBatchImageUpscaleTestStore()
	repo := newFakeBatchImageRepository()
	customID := "image_winner"
	job := &BatchImageJob{
		BatchID: "imgbatch_stale_worker", Model: "gemini-2.5-flash-image", ImageSize: "2K",
	}
	repo.jobs[job.BatchID] = &BatchImageJob{BatchID: job.BatchID, Status: BatchImageJobStatusSettling}
	repo.items[job.BatchID] = []CreateBatchImageItemParams{{
		JobID: job.BatchID, CustomID: customID, Status: BatchImageItemStatusSuccess,
	}}
	winnerKey, err := batchImageUpscaleObjectKey(cfg, job.BatchID, customID, 0, "png")
	require.NoError(t, err)
	store.objects[winnerKey] = batchImageUpscaleTestStoreObject{data: []byte("winner"), contentType: "image/png"}
	provider := &fakeProcessorProvider{result: string(batchImageUpscaleTestResultLine(customID, batchImageUpscaleTestPNG(2, 2))) + "\n"}

	_, err = (&BatchImageResultIndexer{
		Repo: repo, Config: cfg, Upscaler: upscaler, UpscaleStore: store,
	}).Index(context.Background(), job, provider, &Account{})
	require.ErrorIs(t, err, ErrBatchImageIndexStateConflict)
	require.Empty(t, store.deletes)
	require.Contains(t, store.objects, winnerKey)
}

func TestBatchImageProviderProcessor_FailedIndexSchedulesAndCleansDeterministicKeys(t *testing.T) {
	upscaleAPI := newBatchImageUpscaleTestAPI()
	t.Cleanup(upscaleAPI.Close)
	cfg, upscaler := newBatchImageUpscaleTestService(upscaleAPI.URL(), "batch-image/delivery")
	store := newBatchImageUpscaleObjectStoreTest()
	repo := newFakeBatchImageRepository()
	repo.replaceErr = errors.New("injected metadata commit failure")
	accountID := int64(101)
	apiKeyID := int64(22)
	outputRef := "provider/results/output.jsonl"
	customID := "recover-after-failed-index"
	job := &BatchImageJob{
		BatchID: "imgbatch_failed_index_cleanup", UserID: 11, APIKeyID: &apiKeyID,
		AccountID: &accountID, Provider: BatchImageProviderGeminiAPI,
		Model: "gemini-2.5-flash-image", ImageSize: "2K", Status: BatchImageJobStatusIndexing,
		ProviderOutputRef: &outputRef, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	repo.jobs[job.BatchID] = job
	repo.items[job.BatchID] = []CreateBatchImageItemParams{{
		JobID: job.BatchID, CustomID: customID, Status: BatchImageItemStatusPending,
	}}
	provider := &fakeProcessorProvider{result: string(batchImageUpscaleTestResultLine(customID, batchImageUpscaleTestPNG(2, 2))) + "\n"}
	processor := &BatchImageProviderProcessor{
		Repo: repo,
		Indexer: &BatchImageResultIndexer{
			Repo: repo, Config: cfg, Upscaler: upscaler, UpscaleStore: store,
		},
	}

	processResult, err := processor.indexAndSettle(context.Background(), job, provider, &Account{ID: accountID})
	require.NoError(t, err)
	require.True(t, processResult.Terminal)
	require.Equal(t, BatchImageJobStatusFailed, job.Status)
	require.NotNil(t, job.OutputExpiresAt)
	require.False(t, job.OutputExpiresAt.Before(job.FinishedAt.Add(BatchImageUpscaleCleanupGrace)))
	key, err := batchImageUpscaleObjectKey(cfg, job.BatchID, customID, 0, "png")
	require.NoError(t, err)
	require.Contains(t, store.objects, key)

	cleanupProvider := &publicBatchImageProvider{name: BatchImageProviderGeminiAPI}
	cleanup := &BatchImageCleanupService{
		Repo: repo, ProviderRegistry: NewBatchImageProviderRegistry(cleanupProvider),
		AccountResolver: &fakeBatchImageAccountResolver{account: &Account{ID: accountID}},
		DeliveryStore:   store, Config: cfg,
	}
	cleanupResult, err := cleanup.RunOnce(context.Background(), job.OutputExpiresAt.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, 1, cleanupResult.OutputCleaned)
	require.NotNil(t, job.OutputDeletedAt)
	require.NotContains(t, store.objects, key)
	require.Equal(t, []CleanupTarget{CleanupTargetOutput}, cleanupProvider.cleanupTargets)
}

type batchImageUpscaleTestStoreObject struct {
	data        []byte
	contentType string
}

type batchImageUpscaleTestStore struct {
	objects        map[string]batchImageUpscaleTestStoreObject
	puts           []string
	deletes        [][]string
	deleteCalls    int
	deleteFailures int
	blockPut       bool
}

func newBatchImageUpscaleTestStore() *batchImageUpscaleTestStore {
	return &batchImageUpscaleTestStore{objects: make(map[string]batchImageUpscaleTestStoreObject)}
}

func (s *batchImageUpscaleTestStore) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	if s.blockPut {
		<-ctx.Done()
		return ctx.Err()
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return fmt.Errorf("put size mismatch: got %d, want %d", len(data), size)
	}
	s.objects[key] = batchImageUpscaleTestStoreObject{data: data, contentType: contentType}
	s.puts = append(s.puts, key)
	return nil
}

func (s *batchImageUpscaleTestStore) Open(_ context.Context, key string) (io.ReadCloser, int64, string, error) {
	object, ok := s.objects[key]
	if !ok {
		return nil, 0, "", fmt.Errorf("object %q not found", key)
	}
	return io.NopCloser(bytes.NewReader(object.data)), int64(len(object.data)), object.contentType, nil
}

func (s *batchImageUpscaleTestStore) Delete(_ context.Context, keys []string) error {
	deleted := append([]string(nil), keys...)
	s.deletes = append(s.deletes, deleted)
	s.deleteCalls++
	if s.deleteFailures > 0 {
		s.deleteFailures--
		return errors.New("injected delete failure")
	}
	for _, key := range keys {
		delete(s.objects, key)
	}
	return nil
}

type batchImageUpscaleTestJob struct {
	width  int
	height int
	scale  int
	failed bool
}

type batchImageUpscaleTestAPI struct {
	server           *httptest.Server
	mu               sync.Mutex
	jobs             map[string]batchImageUpscaleTestJob
	submitCalls      int
	scales           []int
	failOnSubmission int
	resultDelay      time.Duration
}

func newBatchImageUpscaleTestAPI() *batchImageUpscaleTestAPI {
	api := &batchImageUpscaleTestAPI{jobs: make(map[string]batchImageUpscaleTestJob)}
	api.server = httptest.NewServer(http.HandlerFunc(api.handle))
	return api
}

func (a *batchImageUpscaleTestAPI) Close() {
	a.server.Close()
}

func (a *batchImageUpscaleTestAPI) URL() string {
	return a.server.URL
}

func (a *batchImageUpscaleTestAPI) SubmittedScales() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]int(nil), a.scales...)
}

func (a *batchImageUpscaleTestAPI) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/upscale":
		a.handleSubmit(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/jobs/") && strings.HasSuffix(r.URL.Path, "/result"):
		a.handleResult(w, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/jobs/"), "/result"))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/jobs/"):
		a.handleStatus(w, strings.TrimPrefix(r.URL.Path, "/v1/jobs/"))
	default:
		http.NotFound(w, r)
	}
}

func (a *batchImageUpscaleTestAPI) handleSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer func() { _ = file.Close() }()
	source, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	decoded, _, err := image.DecodeConfig(bytes.NewReader(source))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	scale, err := strconv.Atoi(r.FormValue("scale"))
	if err != nil || (scale != 2 && scale != 4) {
		http.Error(w, "invalid scale", http.StatusBadRequest)
		return
	}

	a.mu.Lock()
	a.submitCalls++
	submission := a.submitCalls
	jobID := fmt.Sprintf("job-%d", submission)
	a.jobs[jobID] = batchImageUpscaleTestJob{
		width:  decoded.Width,
		height: decoded.Height,
		scale:  scale,
		failed: a.failOnSubmission == submission,
	}
	a.scales = append(a.scales, scale)
	a.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = fmt.Fprintf(w, `{"job":{"id":%q}}`, jobID)
}

func (a *batchImageUpscaleTestAPI) handleStatus(w http.ResponseWriter, jobID string) {
	job, ok := a.job(jobID)
	if !ok {
		http.NotFound(w, nil)
		return
	}
	state := "completed"
	if job.failed {
		state = "failed"
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"job":{"state":%q}}`, state)
}

func (a *batchImageUpscaleTestAPI) handleResult(w http.ResponseWriter, jobID string) {
	job, ok := a.job(jobID)
	if !ok || job.failed {
		http.NotFound(w, nil)
		return
	}
	if a.resultDelay > 0 {
		time.Sleep(a.resultDelay)
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(batchImageUpscaleTestPNG(job.width*job.scale, job.height*job.scale))
}

func (a *batchImageUpscaleTestAPI) job(jobID string) (batchImageUpscaleTestJob, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	job, ok := a.jobs[jobID]
	return job, ok
}

func newBatchImageUpscaleTestService(baseURL, prefix string) (*config.Config, *ImageUpscaleService) {
	cfg := &config.Config{
		BatchImage: config.BatchImageConfig{DeliveryCOSPrefix: prefix},
		ImageUpscale: config.ImageUpscaleConfig{
			Enabled:               true,
			BaseURL:               baseURL,
			APIKeyVaultRef:        "secret/data/testing#token",
			VaultAgentSocket:      imageUpscaleVaultSocket,
			RequestTimeoutSeconds: 2,
			JobTimeoutSeconds:     2,
			PollIntervalMillis:    1,
			MaxConcurrent:         1,
			MaxQueue:              1,
		},
	}
	service := NewImageUpscaleService(cfg)
	service.loadAPIKey = func(context.Context) ([]byte, error) {
		return []byte("test-api-key"), nil
	}
	return cfg, service
}

func batchImageUpscaleTestResultLine(customID string, images ...[]byte) []byte {
	parts := make([]string, 0, len(images))
	for _, data := range images {
		parts = append(parts, fmt.Sprintf(`{"inlineData":{"mimeType":"image/png","data":%q}}`, base64.StdEncoding.EncodeToString(data)))
	}
	return []byte(fmt.Sprintf(`{"key":%q,"response":{"candidates":[{"content":{"parts":[%s]}}]}}`, customID, strings.Join(parts, ",")))
}

func batchImageUpscaleTestPNG(width, height int) []byte {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		panic(err)
	}
	return encoded.Bytes()
}
