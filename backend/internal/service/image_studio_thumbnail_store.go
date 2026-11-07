package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrStudioThumbnailPending = infraerrors.New(http.StatusServiceUnavailable, "IMAGE_STUDIO_THUMBNAIL_PENDING", "thumbnail is being prepared; retry shortly")
var ErrStudioThumbnailDeferred = infraerrors.New(http.StatusConflict, "IMAGE_STUDIO_THUMBNAIL_COOLDOWN", "thumbnail temporarily unavailable; refresh later")
var ErrStudioThumbnailObjectMissing = errors.New("image studio thumbnail object is missing")

// The row lock serializes each object write with the durable deletion fence.
type BatchImageThumbnailRepository interface {
	BatchImageThumbnailRetryAt(context.Context, string) (*time.Time, error)
	BeginBatchImageThumbnailWrite(context.Context, string) (BatchImageUpscaleWritePermit, error)
	DeferBatchImageThumbnailBackfill(context.Context, string, time.Time) error
	DisableBatchImageThumbnails(context.Context, string) error
}

type BatchImageThumbnailService struct {
	Repo   BatchImageThumbnailRepository
	Store  BatchImageUpscaleObjectStore
	Config *config.Config
}

func newBatchImageThumbnailService(repo BatchImageRepository, store BatchImageDeliveryObjectStore, cfg *config.Config) *BatchImageThumbnailService {
	r, _ := repo.(BatchImageThumbnailRepository)
	s, _ := store.(BatchImageUpscaleObjectStore)
	if r == nil || s == nil || cfg == nil || strings.TrimSpace(cfg.BatchImage.DeliveryCOSPrefix) == "" {
		return nil
	}
	return &BatchImageThumbnailService{Repo: r, Store: s, Config: cfg}
}

func studioThumbnailObjectKey(cfg *config.Config, batchID, customID string) (string, error) {
	key, err := batchImageUpscaleObjectKey(cfg, batchID, customID, 0, "jpg")
	if err != nil {
		return "", err
	}
	// Reuse the validated namespace and hashed custom ID, with a distinct version.
	prefix := strings.Trim(strings.TrimSpace(cfg.BatchImage.DeliveryCOSPrefix), "/")
	return prefix + "/thumbnails/v1/" + strings.TrimPrefix(key, prefix+"/upscaled/"), nil
}

func (s *BatchImageThumbnailService) Open(ctx context.Context, batchID, customID string) ([]byte, error) {
	key, err := studioThumbnailObjectKey(s.Config, batchID, customID)
	if err != nil {
		return nil, err
	}
	r, size, mime, err := s.Store.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if size > studioThumbnailMaxStoredBytes || normalizeBatchImageArchiveContentType(mime) != "image/jpeg" {
		return nil, ErrBatchImageDownloadFailed
	}
	data, err := io.ReadAll(io.LimitReader(r, studioThumbnailMaxStoredBytes+1))
	if err != nil || len(data) == 0 || len(data) > studioThumbnailMaxStoredBytes {
		return nil, ErrBatchImageDownloadFailed
	}
	return data, nil
}

func (s *BatchImageThumbnailService) Persist(ctx context.Context, job *BatchImageJob, customID string, source []byte) error {
	if s == nil {
		return nil
	}
	if int64(len(source)) > studioThumbnailSourceLimit(job) {
		return ErrBatchImageDownloadFailed
	}
	select {
	case studioThumbnailDecoders <- struct{}{}:
		defer func() { <-studioThumbnailDecoders }()
	case <-ctx.Done():
		return ctx.Err()
	}
	data, err := encodeStudioThumbnail(source)
	if err != nil {
		return err
	}
	key, err := studioThumbnailObjectKey(s.Config, job.BatchID, customID)
	if err != nil {
		return err
	}
	putCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	permit, err := s.Repo.BeginBatchImageThumbnailWrite(putCtx, job.BatchID)
	if err != nil {
		return err
	}
	if permit == nil {
		return ErrBatchImageDownloadFailed
	}
	err = s.Store.Put(putCtx, key, "image/jpeg", bytes.NewReader(data), int64(len(data)))
	return errors.Join(err, permit.Release())
}

const studioThumbnailMaxStoredBytes = 1 << 20

func studioThumbnailSourceLimit(job *BatchImageJob) int64 {
	if batchImageJobRequiresUpscale(job) {
		// Match the upscaler's bounded result size; decoding still checks 40 MP.
		return 128 << 20
	}
	return studioThumbnailMaxBytes
}
