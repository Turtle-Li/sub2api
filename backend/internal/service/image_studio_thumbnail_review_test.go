//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type waitingThumbnailProvider struct {
	*thumbnailProviderTest
	entered chan struct{}
	resume  chan struct{}
}

func (p *waitingThumbnailProvider) OpenResult(ctx context.Context, job *BatchImageJob, account *Account) (io.ReadCloser, string, error) {
	close(p.entered)
	select {
	case <-p.resume:
		return p.thumbnailProviderTest.OpenResult(ctx, job, account)
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
}

func TestImageStudioThumbnailCancelledReaderDoesNotCancelSharedBackfill(t *testing.T) {
	svc, _, _, provider, _ := persistentThumbnailFixture(t)
	waiting := &waitingThumbnailProvider{provider, make(chan struct{}), make(chan struct{})}
	svc.ProviderRegistry = NewBatchImageProviderRegistry(waiting)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := make(chan error, 1)
	go func() {
		_, err := svc.ItemThumbnail(ctx, testBatchImageOwner(), "imgbatch_download", "cover/../001")
		first <- err
	}()
	select {
	case <-waiting.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("backfill did not start")
	}
	cancel()
	select {
	case err := <-first:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled reader remained blocked")
	}
	close(waiting.resume)
	preview, err := svc.ItemThumbnail(t.Context(), testBatchImageOwner(), "imgbatch_download", "cover/../001")
	require.NoError(t, err)
	require.NotEmpty(t, preview)
	require.EqualValues(t, 1, provider.reads.Load())
}

func TestImageStudioThumbnailDownloadContentionDoesNotConsumeCooldown(t *testing.T) {
	svc, repo, _, _, _ := persistentThumbnailFixture(t)
	limiter := svc.Limiter.(*fakeBatchImageDownloadLimiter)
	limiter.deny = true
	_, err := svc.ItemThumbnail(t.Context(), testBatchImageOwner(), "imgbatch_download", "cover/../001")
	require.ErrorIs(t, err, ErrBatchImageDownloadLimited)
	require.Nil(t, repo.retryAt)
	limiter.deny = false
	_, err = svc.ItemThumbnail(t.Context(), testBatchImageOwner(), "imgbatch_download", "cover/../001")
	require.NoError(t, err)
}

func TestImageStudioThumbnailBackfillSkipsAcceptedBlankResultLines(t *testing.T) {
	svc, repo, _, provider, _ := persistentThumbnailFixture(t)
	provider.result = "\n \t\n" + provider.result + "\n\n"
	// Legacy high-resolution jobs without an upscale marker still use JSONL.
	repo.jobs["imgbatch_download"].ImageSize = "2K"
	preview, err := svc.ItemThumbnail(t.Context(), testBatchImageOwner(), "imgbatch_download", "ok_2")
	require.NoError(t, err)
	require.NotEmpty(t, preview)
	require.EqualValues(t, 1, provider.reads.Load())
}

func TestImageStudioThumbnailAcceptsBoundedLargeUpscaleResult(t *testing.T) {
	svc, repo, store, _, source := persistentThumbnailFixture(t)
	job := repo.jobs["imgbatch_download"]
	// A valid PNG with ancillary trailing bytes exercises the encoded-size cap
	// without allocating a full 25 MP image in the test runner.
	large := append(append([]byte{}, source...), make([]byte, studioThumbnailMaxBytes+1)...)
	previews := newBatchImageThumbnailService(repo, store, svc.Config)
	require.Error(t, previews.Persist(t.Context(), job, "ok_2", large))
	job.ImageSize = "4K"
	require.NoError(t, previews.Persist(t.Context(), job, "ok_2", large))
	preview, err := previews.Open(t.Context(), job.BatchID, "ok_2")
	require.NoError(t, err)
	require.Less(t, len(preview), studioThumbnailMaxStoredBytes)
}

type firstReadFailureThumbnailStore struct {
	*thumbnailObjectStoreTest
	failed bool
}

func (s *firstReadFailureThumbnailStore) Open(ctx context.Context, key string) (io.ReadCloser, int64, string, error) {
	if !s.failed {
		s.failed = true
		return nil, 0, "", errors.New("one original is unavailable")
	}
	return s.thumbnailObjectStoreTest.Open(ctx, key)
}

func TestImageStudioThumbnailUpscaleFailureDoesNotSkipOtherItems(t *testing.T) {
	svc, repo, store, _, source := persistentThumbnailFixture(t)
	job := repo.jobs["imgbatch_download"]
	job.ImageSize = "4K"
	items := make(map[string]*BatchImageItem)
	extension := "png"
	for _, id := range []string{"first", "second"} {
		key, err := batchImageUpscaleObjectKey(svc.Config, job.BatchID, id, 0, extension)
		require.NoError(t, err)
		store.setObject(key, "image/png", source)
		items[id] = &BatchImageItem{CustomID: id, FileExtension: &extension}
	}
	previews := newBatchImageThumbnailService(repo, &firstReadFailureThumbnailStore{thumbnailObjectStoreTest: store}, svc.Config)
	require.Error(t, svc.backfillUpscaledThumbnails(t.Context(), job, items, previews))
	stored := 0
	for key := range store.objects {
		if strings.Contains(key, "/thumbnails/v1/") {
			stored++
		}
	}
	require.Equal(t, 1, stored, "the second item must be attempted after the first read fails")
}
