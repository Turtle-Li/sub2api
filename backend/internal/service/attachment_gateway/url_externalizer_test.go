package attachment_gateway

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type recordingObjectStorage struct {
	mu    sync.Mutex
	calls int
	keys  []string
	err   error
	url   string
}

type ensuringObjectStorage struct {
	recordingObjectStorage
	uploaded bool
}

type dynamicRecordingObjectStorage struct {
	recordingObjectStorage
	ready   bool
	version uint64
}

type blockingVersionedObjectStorage struct {
	mu      sync.Mutex
	version uint64
	calls   int
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *dynamicRecordingObjectStorage) Ready(context.Context) bool { return s.ready }
func (s *dynamicRecordingObjectStorage) CacheVersion() uint64       { return s.version }

func (s *blockingVersionedObjectStorage) Ready(context.Context) bool { return true }

func (s *blockingVersionedObjectStorage) CacheVersion() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

func (s *blockingVersionedObjectStorage) Save(_ context.Context, key, _ string, _ []byte) (string, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	s.once.Do(func() { close(s.started) })
	<-s.release
	return "https://r2.example.test/" + key + "?signature=versioned", nil
}

func (s *blockingVersionedObjectStorage) setVersion(version uint64) {
	s.mu.Lock()
	s.version = version
	s.mu.Unlock()
}

func (s *blockingVersionedObjectStorage) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *ensuringObjectStorage) Ensure(_ context.Context, key, _ string, _ []byte) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.keys = append(s.keys, key)
	return "https://r2.example.test/" + key + "?signature=fresh", s.uploaded, nil
}

func (s *recordingObjectStorage) Save(_ context.Context, key, _ string, _ []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.keys = append(s.keys, key)
	if s.err != nil {
		return "", s.err
	}
	if s.url != "" {
		return s.url, nil
	}
	return "https://r2.example.test/" + key + "?signature=private", nil
}

func (s *recordingObjectStorage) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func testInlineImageBody(data []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(data)
	return []byte(`{"model":"gpt-test","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/webp;base64,` + encoded + `"}]}]}`)
}

func TestURLExternalizerDisabledIsExactNoOp(t *testing.T) {
	store := &recordingObjectStorage{}
	externalizer, err := NewURLExternalizer(URLConfig{}, store)
	require.NoError(t, err)

	body := testInlineImageBody([]byte("image"))
	result := externalizer.Externalize(context.Background(), body)

	require.Same(t, &body[0], &result.Body[0])
	require.Equal(t, 0, store.callCount())
	require.False(t, result.Metrics.Enabled)
}

func TestURLExternalizerPublishesOnceAndReusesHashURL(t *testing.T) {
	store := &recordingObjectStorage{}
	externalizer, err := NewURLExternalizer(URLConfig{
		Enabled:              true,
		MinBodyBytes:         1,
		ObjectPrefix:         "attachments/",
		URLCacheTTL:          time.Hour,
		MaxImageBytes:        1024,
		MaxImagesPerRequest:  4,
		MaxConcurrentUploads: 1,
	}, store)
	require.NoError(t, err)

	body := testInlineImageBody([]byte("same-optimized-image"))
	first := externalizer.Externalize(context.Background(), body)
	second := externalizer.Externalize(context.Background(), body)

	require.Equal(t, 1, store.callCount())
	require.NotEqual(t, string(body), string(first.Body))
	require.Contains(t, string(first.Body), `"image_url":"https://r2.example.test/attachments/`)
	require.NotContains(t, string(first.Body), "data:image")
	require.Equal(t, first.Body, second.Body)
	require.Equal(t, 1, first.Metrics.UploadCount)
	require.Equal(t, 0, first.Metrics.CacheHits)
	require.Equal(t, 0, second.Metrics.UploadCount)
	require.Equal(t, 1, second.Metrics.CacheHits)
	require.Less(t, len(first.Body), len(body)+200)

	store.mu.Lock()
	require.Len(t, store.keys, 1)
	require.True(t, strings.HasPrefix(store.keys[0], "attachments/"))
	require.True(t, strings.HasSuffix(store.keys[0], ".webp"))
	store.mu.Unlock()
}

func TestURLExternalizerUploadFailureFailsOpen(t *testing.T) {
	store := &recordingObjectStorage{err: errors.New("r2 unavailable")}
	externalizer, err := NewURLExternalizer(URLConfig{
		Enabled:              true,
		MinBodyBytes:         1,
		URLCacheTTL:          time.Minute,
		MaxImageBytes:        1024,
		MaxImagesPerRequest:  2,
		MaxConcurrentUploads: 1,
	}, store)
	require.NoError(t, err)

	body := testInlineImageBody([]byte("keep-inline-on-error"))
	result := externalizer.Externalize(context.Background(), body)

	require.Equal(t, body, result.Body)
	require.Equal(t, 1, result.Metrics.Errors)
	require.Equal(t, 0, result.Metrics.ExternalizedCount)
}

func TestURLExternalizerReusesExistingR2HashObjectAfterLocalCacheMiss(t *testing.T) {
	store := &ensuringObjectStorage{uploaded: false}
	externalizer, err := NewURLExternalizer(URLConfig{
		Enabled:              true,
		MinBodyBytes:         1,
		URLCacheTTL:          time.Minute,
		MaxImageBytes:        1024,
		MaxImagesPerRequest:  2,
		MaxConcurrentUploads: 1,
	}, store)
	require.NoError(t, err)

	result := externalizer.Externalize(context.Background(), testInlineImageBody([]byte("already-in-r2")))

	require.Equal(t, 1, store.callCount())
	require.Equal(t, 0, result.Metrics.UploadCount)
	require.Equal(t, 1, result.Metrics.CacheHits)
	require.Equal(t, 1, result.Metrics.ExternalizedCount)
}

func TestURLExternalizerRejectsNonHTTPSStorageURL(t *testing.T) {
	store := &recordingObjectStorage{url: "http://public.example.test/image.webp"}
	externalizer, err := NewURLExternalizer(URLConfig{
		Enabled:              true,
		MinBodyBytes:         1,
		URLCacheTTL:          time.Minute,
		MaxImageBytes:        1024,
		MaxImagesPerRequest:  2,
		MaxConcurrentUploads: 1,
	}, store)
	require.NoError(t, err)

	body := testInlineImageBody([]byte("private-image"))
	result := externalizer.Externalize(context.Background(), body)

	require.Equal(t, body, result.Body)
	require.Equal(t, 1, result.Metrics.Errors)
}

func TestURLExternalizerBelowBodyTriggerDoesNotUpload(t *testing.T) {
	store := &recordingObjectStorage{}
	externalizer, err := NewURLExternalizer(URLConfig{
		Enabled:              true,
		MinBodyBytes:         1 << 20,
		URLCacheTTL:          time.Minute,
		MaxImageBytes:        1024,
		MaxImagesPerRequest:  2,
		MaxConcurrentUploads: 1,
	}, store)
	require.NoError(t, err)

	body := testInlineImageBody([]byte("small"))
	result := externalizer.Externalize(context.Background(), body)

	require.Equal(t, body, result.Body)
	require.True(t, result.Metrics.SkippedBelowTrigger)
	require.Equal(t, 0, store.callCount())
}

func TestURLExternalizerSkipsWholeRequestWhenDynamicStorageIsUnavailable(t *testing.T) {
	store := &dynamicRecordingObjectStorage{ready: false}
	externalizer, err := NewURLExternalizer(URLConfig{
		Enabled:              true,
		MinBodyBytes:         1,
		URLCacheTTL:          time.Minute,
		MaxImageBytes:        1024,
		MaxImagesPerRequest:  2,
		MaxConcurrentUploads: 1,
	}, store)
	require.NoError(t, err)

	body := testInlineImageBody([]byte("not-uploaded"))
	result := externalizer.Externalize(context.Background(), body)

	require.Equal(t, body, result.Body)
	require.True(t, result.Metrics.StorageUnavailable)
	require.False(t, result.Metrics.StorageReady)
	require.Zero(t, result.Metrics.Errors)
	require.Equal(t, 0, store.callCount())
}

func TestURLExternalizerStorageVersionInvalidatesSignedURLCache(t *testing.T) {
	store := &dynamicRecordingObjectStorage{ready: true, version: 1}
	externalizer, err := NewURLExternalizer(URLConfig{
		Enabled:              true,
		MinBodyBytes:         1,
		URLCacheTTL:          time.Hour,
		MaxImageBytes:        1024,
		MaxImagesPerRequest:  2,
		MaxConcurrentUploads: 1,
	}, store)
	require.NoError(t, err)
	body := testInlineImageBody([]byte("versioned-image"))

	first := externalizer.Externalize(context.Background(), body)
	second := externalizer.Externalize(context.Background(), body)
	require.True(t, first.Metrics.StorageReady)
	require.Equal(t, 1, store.callCount())
	require.Equal(t, 1, second.Metrics.CacheHits)

	store.version = 2
	third := externalizer.Externalize(context.Background(), body)
	require.Equal(t, 2, store.callCount())
	require.Equal(t, 0, third.Metrics.CacheHits)
}

func TestURLExternalizerConfigChangeDuringUploadCannotRestoreStaleURLCache(t *testing.T) {
	store := &blockingVersionedObjectStorage{
		version: 1,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	externalizer, err := NewURLExternalizer(URLConfig{
		Enabled:              true,
		MinBodyBytes:         1,
		URLCacheTTL:          time.Hour,
		MaxImageBytes:        1024,
		MaxImagesPerRequest:  2,
		MaxConcurrentUploads: 1,
	}, store)
	require.NoError(t, err)
	body := testInlineImageBody([]byte("version-changed-during-upload"))
	resultCh := make(chan URLResult, 1)

	go func() {
		resultCh <- externalizer.Externalize(context.Background(), body)
	}()
	<-store.started
	store.setVersion(2)
	close(store.release)

	first := <-resultCh
	require.Equal(t, body, first.Body)
	require.Positive(t, first.Metrics.Errors)
	require.Equal(t, 1, store.callCount())

	second := externalizer.Externalize(context.Background(), body)
	require.NotEqual(t, body, second.Body)
	require.Equal(t, 0, second.Metrics.CacheHits)
	require.Equal(t, 2, store.callCount())
}

func TestURLExternalizerCacheNeverOutlivesPresignedURL(t *testing.T) {
	signedAt := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store := &recordingObjectStorage{url: "https://account.r2.cloudflarestorage.com/private/key.webp?X-Amz-Date=20260720T120000Z&X-Amz-Expires=300&X-Amz-Signature=test"}
	externalizer, err := NewURLExternalizer(URLConfig{
		Enabled:              true,
		MinBodyBytes:         1,
		URLCacheTTL:          time.Hour,
		MaxImageBytes:        1024,
		MaxImagesPerRequest:  2,
		MaxConcurrentUploads: 1,
	}, store)
	require.NoError(t, err)
	now := signedAt
	externalizer.now = func() time.Time { return now }
	body := testInlineImageBody([]byte("expiring-image"))

	externalizer.Externalize(context.Background(), body)
	now = signedAt.Add(3*time.Minute + 59*time.Second)
	hit := externalizer.Externalize(context.Background(), body)
	require.Equal(t, 1, hit.Metrics.CacheHits)
	require.Equal(t, 1, store.callCount())

	now = signedAt.Add(4*time.Minute + time.Second)
	miss := externalizer.Externalize(context.Background(), body)
	require.Equal(t, 0, miss.Metrics.CacheHits)
	require.Equal(t, body, miss.Body)
	require.Equal(t, 1, miss.Metrics.Errors)
	require.Equal(t, 2, store.callCount())
}
