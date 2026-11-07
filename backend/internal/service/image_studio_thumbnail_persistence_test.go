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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type thumbnailRepositoryTest struct {
	*fakeBatchImageRepository
	disabled bool
	retryAt  *time.Time
}

func (r *thumbnailRepositoryTest) BatchImageThumbnailRetryAt(context.Context, string) (*time.Time, error) {
	if r.disabled {
		return nil, ErrBatchImageOutputDeleted
	}
	return r.retryAt, nil
}
func (r *thumbnailRepositoryTest) BeginBatchImageThumbnailWrite(_ context.Context, id string) (BatchImageUpscaleWritePermit, error) {
	job := r.jobs[id]
	if r.disabled || job == nil || (job.Status != BatchImageJobStatusIndexing && job.Status != BatchImageJobStatusCompleted) {
		return nil, ErrBatchImageOutputDeleted
	}
	return noopBatchImageUpscaleWritePermit{}, nil
}
func (r *thumbnailRepositoryTest) DeferBatchImageThumbnailBackfill(_ context.Context, _ string, at time.Time) error {
	r.retryAt = &at
	return nil
}
func (r *thumbnailRepositoryTest) DisableBatchImageThumbnails(context.Context, string) error {
	r.disabled = true
	return nil
}

type thumbnailProviderTest struct {
	publicBatchImageProvider
	reads     atomic.Int32
	bytesRead atomic.Int64
}

type thumbnailObjectStoreTest struct {
	*batchImageUpscaleObjectStoreTest
}

func (s *thumbnailObjectStoreTest) Open(ctx context.Context, key string) (io.ReadCloser, int64, string, error) {
	r, size, mime, err := s.batchImageUpscaleObjectStoreTest.Open(ctx, key)
	if errors.Is(err, io.EOF) {
		err = ErrStudioThumbnailObjectMissing
	}
	return r, size, mime, err
}

func (p *thumbnailProviderTest) OpenResult(ctx context.Context, j *BatchImageJob, a *Account) (io.ReadCloser, string, error) {
	p.reads.Add(1)
	r, mime, err := p.publicBatchImageProvider.OpenResult(ctx, j, a)
	return &thumbnailSourceReaderTest{ReadCloser: r, count: &p.bytesRead}, mime, err
}

type thumbnailSourceReaderTest struct {
	io.ReadCloser
	count *atomic.Int64
}

func (r *thumbnailSourceReaderTest) Read(data []byte) (int, error) {
	n, err := r.ReadCloser.Read(data)
	r.count.Add(int64(n))
	return n, err
}

func persistentThumbnailFixture(t *testing.T) (*BatchImageDownloadService, *thumbnailRepositoryTest, *thumbnailObjectStoreTest, *thumbnailProviderTest, []byte) {
	t.Helper()
	svc, base, _, originalStore := newTestBatchImageUpscaledDownloadService()
	store := &thumbnailObjectStoreTest{originalStore}
	svc.DeliveryStore = store
	repo := &thumbnailRepositoryTest{fakeBatchImageRepository: base}
	repo.jobs["imgbatch_download"].ImageSize = "1K"
	svc.Repo = repo
	var source bytes.Buffer
	require.NoError(t, png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 1024, 512))))
	line := func(id string) string {
		return fmt.Sprintf(`{"key":%q,"response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"%s"}}]}}]}}`, id, base64.StdEncoding.EncodeToString(source.Bytes()))
	}
	provider := &thumbnailProviderTest{publicBatchImageProvider: publicBatchImageProvider{name: BatchImageProviderGeminiAPI, result: line("cover/../001") + "\n" + line("ok_2")}}
	svc.ProviderRegistry = NewBatchImageProviderRegistry(provider)
	return svc, repo, store, provider, source.Bytes()
}

func TestImageStudioThumbnailFreshProcessReadsStoredCoverWithoutOriginal(t *testing.T) {
	svc, repo, store, provider, source := persistentThumbnailFixture(t)
	writer := newBatchImageThumbnailService(repo, store, svc.Config)
	require.NotNil(t, writer)
	require.NoError(t, writer.Persist(t.Context(), repo.jobs["imgbatch_download"], "cover/../001", source))
	// Simulate a process restart: no LRU, provider, or provider account resolver.
	cold := &BatchImageDownloadService{Repo: repo, DeliveryStore: store, Config: svc.Config}
	got, err := cold.ItemThumbnail(t.Context(), testBatchImageOwner(), "imgbatch_download", "cover/../001")
	require.NoError(t, err)
	config, _, err := image.DecodeConfig(bytes.NewReader(got))
	require.NoError(t, err)
	require.Equal(t, 480, config.Width)
	require.Zero(t, provider.reads.Load())
	repo.disabled = true
	_, err = cold.ItemThumbnail(t.Context(), testBatchImageOwner(), "imgbatch_download", "cover/../001")
	require.ErrorIs(t, err, ErrBatchImageOutputDeleted)
}

func TestImageStudioThumbnailHistoricalBatchScannedOnceAcrossProcesses(t *testing.T) {
	svc, repo, store, provider, _ := persistentThumbnailFixture(t)
	first, err := svc.ItemThumbnail(t.Context(), testBatchImageOwner(), "imgbatch_download", "cover/../001")
	require.NoError(t, err)
	require.NotEmpty(t, first)
	require.EqualValues(t, 1, provider.reads.Load())
	sourceBytes := provider.bytesRead.Load()
	require.Positive(t, sourceBytes)
	// Another item on another process must already have a durable preview.
	cold := &BatchImageDownloadService{Repo: repo, DeliveryStore: store, Config: svc.Config}
	second, err := cold.ItemThumbnail(t.Context(), testBatchImageOwner(), "imgbatch_download", "ok_2")
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.EqualValues(t, 1, provider.reads.Load())
	require.Equal(t, sourceBytes, provider.bytesRead.Load())
	t.Logf("historical backfill: source opens=1, bytes=%d; next item on fresh process: additional source opens=0, bytes=0", sourceBytes)
}
