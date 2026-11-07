package service

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/sync/singleflight"
)

const studioThumbnailEdge = 480
const studioThumbnailMaxBytes = 32 << 20
const studioThumbnailCacheBytes = 32 << 20
const studioThumbnailCacheEntries = 256

// Bound source decoding across users as well as the per-user download limiter.
var studioThumbnailDecoders = make(chan struct{}, 2)

type batchImageThumbnailValue struct {
	key  string
	data []byte
}

// Process-local and bounded by both bytes and entry count. Successful outputs
// are immutable; ownership and output deletion are rechecked before every hit.
type batchImageThumbnailCache struct {
	mu       sync.Mutex
	entries  map[string]*list.Element
	order    list.List
	size     int
	flight   singleflight.Group
	backfill singleflight.Group
}

func (c *batchImageThumbnailCache) get(key string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry := c.entries[key]; entry != nil {
		c.order.MoveToFront(entry)
		return entry.Value.(batchImageThumbnailValue).data
	}
	return nil
}

func (c *batchImageThumbnailCache) put(key string, data []byte) {
	if len(data) > studioThumbnailCacheBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]*list.Element)
	}
	if c.entries[key] != nil {
		return
	}
	c.entries[key] = c.order.PushFront(batchImageThumbnailValue{key: key, data: data})
	c.size += len(data)
	for c.size > studioThumbnailCacheBytes || len(c.entries) > studioThumbnailCacheEntries {
		entry := c.order.Back()
		value := entry.Value.(batchImageThumbnailValue)
		delete(c.entries, value.key)
		c.size -= len(value.data)
		c.order.Remove(entry)
	}
}

func (s *BatchImageDownloadService) ItemThumbnail(ctx context.Context, owner BatchImageOwner, batchID, customID string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	job, err := s.thumbnailJob(ctx, owner, batchID)
	if err != nil {
		return nil, err
	}
	item, err := s.Repo.GetBatchImageItemForDownload(ctx, job.BatchID, customID)
	if err != nil {
		return nil, err
	}
	if item.Status != BatchImageItemStatusSuccess || item.ImageCount < 1 {
		return nil, ErrBatchImageItemFailed
	}
	previews := newBatchImageThumbnailService(s.Repo, s.DeliveryStore, s.Config)
	if previews == nil {
		return nil, ErrBatchImageDeliveryNotConfigured
	}
	if _, err := previews.Repo.BatchImageThumbnailRetryAt(ctx, batchID); err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%d/%d/%s/%s/thumbnail-v1", owner.UserID, owner.APIKeyID, job.BatchID, customID)
	if data := s.thumbnails.get(key); data != nil {
		return data, nil
	}
	// The durable object is authoritative; eviction and restart only reread JPEGs.
	resultCh := s.thumbnails.flight.DoChan(key, func() (any, error) {
		// Shared work has its own deadline. Closing one card must not cancel
		// another reader's batch backfill or consume its failure cooldown.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		if data := s.thumbnails.get(key); data != nil {
			return data, nil
		}
		thumbnail, err := previews.Open(ctx, batchID, customID)
		if errors.Is(err, ErrStudioThumbnailObjectMissing) {
			_, err, _ = s.thumbnails.backfill.Do(batchID, func() (any, error) {
				return nil, s.backfillThumbnails(ctx, owner, job, customID, previews)
			})
			// A different corrupt item must not hide a successfully stored cover.
			if ready, readErr := previews.Open(ctx, batchID, customID); readErr == nil {
				thumbnail, err = ready, nil
			} else if err == nil {
				err = readErr
			}
		}
		if err != nil {
			return nil, err
		}
		if _, err := s.thumbnailJob(ctx, owner, batchID); err != nil {
			return nil, err
		}
		if _, err := previews.Repo.BatchImageThumbnailRetryAt(ctx, batchID); err != nil {
			return nil, err
		}
		s.thumbnails.put(key, thumbnail)
		return thumbnail, nil
	})
	select {
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		data, ok := result.Val.([]byte)
		if !ok {
			return nil, ErrBatchImageDownloadFailed
		}
		return data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *BatchImageDownloadService) thumbnailJob(ctx context.Context, owner BatchImageOwner, batchID string) (*BatchImageJob, error) {
	job, err := s.getCompletedJob(ctx, owner, batchID)
	if err != nil {
		return nil, err
	}
	if job.UserDeletedAt != nil {
		return nil, ErrBatchImageJobNotFound
	}
	if job.OutputDeletedAt != nil || (job.OutputExpiresAt != nil && !job.OutputExpiresAt.After(time.Now())) {
		return nil, ErrBatchImageOutputDeleted
	}
	return job, nil
}

func encodeStudioThumbnail(data []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width) > 40_000_000/int64(config.Height) {
		return nil, fmt.Errorf("image thumbnail source exceeds pixel limit")
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	w, h := config.Width, config.Height
	if longest := max(w, h); longest > studioThumbnailEdge {
		w = max(1, w*studioThumbnailEdge/longest)
		h = max(1, h*studioThumbnailEdge/longest)
	}
	output := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(output, output.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	xdraw.ApproxBiLinear.Scale(output, output.Bounds(), source, source.Bounds(), draw.Over, nil)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, output, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}
