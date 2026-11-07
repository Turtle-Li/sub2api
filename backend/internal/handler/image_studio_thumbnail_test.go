//go:build unit

package handler

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type thumbnailHTTPRepository struct {
	service.BatchImageRepository
	disabled bool
	checks   int
}

func (r *thumbnailHTTPRepository) GetBatchImageJobForDownload(_ context.Context, user, key int64, id string) (*service.BatchImageJob, error) {
	r.checks++
	if user != 1 || key != 3 {
		return nil, service.ErrBatchImageJobNotFound
	}
	return &service.BatchImageJob{BatchID: id, Status: service.BatchImageJobStatusCompleted}, nil
}
func (r *thumbnailHTTPRepository) GetBatchImageItemForDownload(context.Context, string, string) (*service.BatchImageItem, error) {
	return &service.BatchImageItem{Status: service.BatchImageItemStatusSuccess, ImageCount: 1}, nil
}
func (r *thumbnailHTTPRepository) BatchImageThumbnailRetryAt(context.Context, string) (*time.Time, error) {
	if r.disabled {
		return nil, service.ErrBatchImageOutputDeleted
	}
	return nil, nil
}
func (*thumbnailHTTPRepository) BeginBatchImageThumbnailWrite(context.Context, string) (service.BatchImageUpscaleWritePermit, error) {
	panic("stored thumbnails must not write or read original results")
}
func (*thumbnailHTTPRepository) DeferBatchImageThumbnailBackfill(context.Context, string, time.Time) error {
	panic("stored thumbnails must not backfill")
}
func (*thumbnailHTTPRepository) DisableBatchImageThumbnails(context.Context, string) error {
	return nil
}

type thumbnailHTTPStore struct {
	service.BatchImageDeliveryObjectStore
	data  []byte
	reads int
}

func (s *thumbnailHTTPStore) Open(_ context.Context, _ string) (io.ReadCloser, int64, string, error) {
	s.reads++
	return io.NopCloser(bytes.NewReader(s.data)), int64(len(s.data)), "image/jpeg", nil
}
func (*thumbnailHTTPStore) Put(context.Context, string, string, io.Reader, int64) error {
	panic("GET must only read stored thumbnails")
}
func (*thumbnailHTTPStore) Delete(context.Context, []string) error { return nil }

func TestImageStudioThumbnailHTTPPrivateCacheAndAuthenticatedRevalidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var encoded bytes.Buffer
	require.NoError(t, jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 480, 240)), nil))
	repo := &thumbnailHTTPRepository{}
	store := &thumbnailHTTPStore{data: encoded.Bytes()}
	download := &service.BatchImageDownloadService{Repo: repo, DeliveryStore: store, Config: &config.Config{BatchImage: config.BatchImageConfig{DeliveryCOSPrefix: "private/batches"}}}
	h := NewBatchImageHandler(nil, download, nil)
	router := gin.New()
	router.GET("/batches/:id/items/:custom_id/thumbnail", func(c *gin.Context) {
		if c.GetHeader("Authorization") == "Bearer session" {
			key := int64(3)
			if c.GetHeader("X-API-Key-ID") == "4" {
				key = 4
			}
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: key, UserID: 1})
		}
		h.ItemThumbnail(c)
	})
	request := func(auth, key, etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/batches/imgbatch_http/items/one/thumbnail", nil)
		req.Header.Set("Authorization", auth)
		req.Header.Set("X-API-Key-ID", key)
		req.Header.Set("If-None-Match", etag)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	first := request("Bearer session", "3", "")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	require.Equal(t, encoded.Bytes(), first.Body.Bytes())
	require.Equal(t, "private, max-age=300", first.Header().Get("Cache-Control"))
	require.Equal(t, "Authorization, X-API-Key-ID", first.Header().Get("Vary"))
	require.Equal(t, "nosniff", first.Header().Get("X-Content-Type-Options"))
	etag := first.Header().Get("ETag")
	require.NotEmpty(t, etag)
	second := request("Bearer session", "3", `"old", W/`+etag)
	require.Equal(t, http.StatusNotModified, second.Code)
	require.Empty(t, second.Body.Bytes())
	require.Equal(t, 1, store.reads)
	require.GreaterOrEqual(t, repo.checks, 2)
	require.Equal(t, http.StatusUnauthorized, request("", "3", etag).Code)
	require.Equal(t, http.StatusNotFound, request("Bearer session", "4", etag).Code)
	repo.disabled = true
	deleted := request("Bearer session", "3", etag)
	require.Equal(t, http.StatusGone, deleted.Code, deleted.Body.String())
	require.Equal(t, "private, no-store", deleted.Header().Get("Cache-Control"))
	require.Empty(t, deleted.Header().Get("ETag"))
	require.Equal(t, 1, store.reads)
}
