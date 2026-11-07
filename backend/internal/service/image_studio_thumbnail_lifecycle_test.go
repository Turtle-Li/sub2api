//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageStudioThumbnailIndexerEagerlyPersistsBeforeFirstListRead(t *testing.T) {
	svc, repo, store, provider, _ := persistentThumbnailFixture(t)
	job := repo.jobs["imgbatch_download"]
	job.Status = BatchImageJobStatusIndexing
	indexer := &BatchImageResultIndexer{Repo: repo, Thumbnails: newBatchImageThumbnailService(repo, store, svc.Config)}
	result, err := indexer.Index(t.Context(), job, provider, &Account{})
	require.NoError(t, err)
	require.Equal(t, 2, result.SuccessCount)
	require.Equal(t, 1, result.FailCount)
	require.EqualValues(t, 1, provider.reads.Load())
	sourceBytes := provider.bytesRead.Load()
	job.Status = BatchImageJobStatusCompleted
	cold := &BatchImageDownloadService{Repo: repo, DeliveryStore: store, Config: svc.Config}
	for _, id := range []string{"cover/../001", "ok_2"} {
		preview, err := cold.ItemThumbnail(t.Context(), testBatchImageOwner(), job.BatchID, id)
		require.NoError(t, err)
		require.NotEmpty(t, preview)
	}
	require.EqualValues(t, 1, provider.reads.Load(), "list reads must not open provider results")
	require.Equal(t, sourceBytes, provider.bytesRead.Load())
	t.Logf("eager index source bytes=%d; first list on fresh process: additional original opens=0, bytes=0", sourceBytes)
	require.Nil(t, job.ActualCost, "preview processing never settles a job")
}

type failingThumbnailStore struct{ *thumbnailObjectStoreTest }

func (s *failingThumbnailStore) Put(context.Context, string, string, io.Reader, int64) error {
	return errors.New("storage temporarily unavailable")
}

func TestImageStudioThumbnailFailureDoesNotFailGenerationAndRetryIsBounded(t *testing.T) {
	svc, repo, store, provider, _ := persistentThumbnailFixture(t)
	job := repo.jobs["imgbatch_download"]
	job.Status = BatchImageJobStatusIndexing
	failing := &failingThumbnailStore{store}
	indexer := &BatchImageResultIndexer{Repo: repo, Thumbnails: newBatchImageThumbnailService(repo, failing, svc.Config)}
	result, err := indexer.Index(t.Context(), job, provider, &Account{})
	require.NoError(t, err)
	require.Equal(t, 2, result.SuccessCount)
	job.Status = BatchImageJobStatusCompleted
	svc.DeliveryStore = failing
	_, err = svc.ItemThumbnail(t.Context(), testBatchImageOwner(), job.BatchID, "ok_2")
	require.Error(t, err)
	reads := provider.reads.Load()
	_, err = svc.ItemThumbnail(t.Context(), testBatchImageOwner(), job.BatchID, "ok_2")
	require.ErrorIs(t, err, ErrStudioThumbnailDeferred)
	require.Equal(t, reads, provider.reads.Load(), "cooldown must survive new reader state")
	svc.DeliveryStore = store
	past := time.Now().Add(-time.Minute)
	repo.retryAt = &past
	_, err = svc.ItemThumbnail(t.Context(), testBatchImageOwner(), job.BatchID, "ok_2")
	require.NoError(t, err)
}

func TestImageStudioThumbnailDeletionFencesLateWriterAndRemovesCloudObjects(t *testing.T) {
	svc, repo, store, _, source := persistentThumbnailFixture(t)
	job := repo.jobs["imgbatch_download"]
	previews := newBatchImageThumbnailService(repo, store, svc.Config)
	require.NoError(t, previews.Persist(t.Context(), job, "cover/../001", source))
	cleanup := &BatchImageCleanupService{Repo: repo, DeliveryStore: store, Config: svc.Config}
	require.NoError(t, cleanup.cleanupThumbnails(t.Context(), job))
	require.True(t, repo.disabled)
	require.Empty(t, store.objects)
	require.ErrorIs(t, previews.Persist(t.Context(), job, "cover/../001", source), ErrBatchImageOutputDeleted)
	require.Empty(t, store.objects)
}

func TestImageStudioThumbnailExpiresEvenWithWarmMemoryCache(t *testing.T) {
	svc, repo, store, _, source := persistentThumbnailFixture(t)
	job := repo.jobs["imgbatch_download"]
	require.NoError(t, newBatchImageThumbnailService(repo, store, svc.Config).Persist(t.Context(), job, "cover/../001", source))
	_, err := svc.ItemThumbnail(t.Context(), testBatchImageOwner(), job.BatchID, "cover/../001")
	require.NoError(t, err)
	expired := time.Now().Add(-time.Second)
	job.OutputExpiresAt = &expired
	_, err = svc.ItemThumbnail(t.Context(), testBatchImageOwner(), job.BatchID, "cover/../001")
	require.ErrorIs(t, err, ErrBatchImageOutputDeleted)
}

func TestImageStudioThumbnailSharedBackfillLeaseDoesNotReadOriginalOnContention(t *testing.T) {
	svc, _, _, provider, _ := persistentThumbnailFixture(t)
	cache := &fakeLeaderLockCache{owners: map[string]string{"image-studio-thumbnails:imgbatch_download": "peer"}}
	svc.thumbnailLock = newSingletonJobLock(cache, nil, "image-studio-thumbnails", 2*time.Minute)
	_, err := svc.ItemThumbnail(t.Context(), testBatchImageOwner(), "imgbatch_download", "cover/../001")
	require.ErrorIs(t, err, ErrStudioThumbnailPending)
	require.Zero(t, provider.reads.Load())
	require.NoError(t, cache.ReleaseLeaderLock(t.Context(), "image-studio-thumbnails:imgbatch_download", "peer"))
	_, err = svc.ItemThumbnail(t.Context(), testBatchImageOwner(), "imgbatch_download", "cover/../001")
	require.NoError(t, err)
	require.EqualValues(t, 1, provider.reads.Load())
}

func TestImageStudioThumbnailUpscaleUsesFinalPixelsAndNeedsNoExtraSourceRead(t *testing.T) {
	_, repo, store, provider, _ := persistentThumbnailFixture(t)
	api := newBatchImageUpscaleTestAPI()
	t.Cleanup(api.Close)
	cfg, upscaler := newBatchImageUpscaleTestService(api.URL(), "batch-image/delivery")
	job := repo.jobs["imgbatch_download"]
	job.Status, job.ImageSize = BatchImageJobStatusIndexing, "2K"
	provider.result = string(batchImageUpscaleTestResultLine("cover/../001", batchImageUpscaleTestPNG(16, 8)))
	previews := newBatchImageThumbnailService(repo, store, cfg)
	indexer := &BatchImageResultIndexer{Repo: repo, Config: cfg, Upscaler: upscaler, UpscaleStore: store, Thumbnails: previews}
	result, err := indexer.Index(t.Context(), job, provider, &Account{})
	require.NoError(t, err)
	require.Equal(t, 1, result.SuccessCount)
	key, err := batchImageUpscaleObjectKey(cfg, job.BatchID, "cover/../001", 0, "png")
	require.NoError(t, err)
	expected, err := encodeStudioThumbnail(store.objects[key].data)
	require.NoError(t, err)
	actual, err := previews.Open(t.Context(), job.BatchID, "cover/../001")
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	require.EqualValues(t, 1, provider.reads.Load())
}

func TestImageStudioThumbnailSkipsEagerWorkNearAuthoritativeDeadline(t *testing.T) {
	svc, repo, store, provider, _ := persistentThumbnailFixture(t)
	job := repo.jobs["imgbatch_download"]
	job.Status = BatchImageJobStatusIndexing
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	indexer := &BatchImageResultIndexer{Repo: repo, Thumbnails: newBatchImageThumbnailService(repo, store, svc.Config)}
	result, err := indexer.Index(ctx, job, provider, &Account{})
	require.NoError(t, err)
	require.Equal(t, 2, result.SuccessCount)
	require.Empty(t, store.objects, "leave the final deadline window for authoritative work")
	require.NoError(t, ctx.Err())
}
