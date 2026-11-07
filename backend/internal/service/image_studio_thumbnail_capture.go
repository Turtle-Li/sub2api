package service

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// One capture per indexing pass: storage failures cannot add a timeout per item.
// Time spent in provider generation/upscaling does not consume this budget.
type batchImageThumbnailCapture struct {
	service   *BatchImageThumbnailService
	parent    context.Context
	remaining time.Duration
}

func (c *batchImageThumbnailCapture) run(ctx context.Context, job *BatchImageJob, persist func(context.Context) error) {
	if c == nil || c.service == nil || c.remaining <= 0 {
		return
	}
	if c.parent != nil {
		ctx = c.parent
	}
	if ctx.Err() != nil {
		return
	}
	// Reserve the parent's final window for authoritative indexing/settlement.
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 30*time.Second {
		return
	}
	started := time.Now()
	previewCtx, cancel := context.WithTimeout(ctx, min(5*time.Second, c.remaining))
	err := persist(previewCtx)
	cancel()
	c.remaining -= time.Since(started)
	if err != nil {
		logger.L().Warn("image_studio.thumbnail_write_failed", zap.String("batch_id", job.BatchID))
	}
}

func (c *batchImageThumbnailCapture) persistSource(ctx context.Context, job *BatchImageJob, customID string, source []byte) {
	c.run(ctx, job, func(ctx context.Context) error { return c.service.Persist(ctx, job, customID, source) })
}

func (c *batchImageThumbnailCapture) persistLine(ctx context.Context, job *BatchImageJob, line []byte) {
	c.run(ctx, job, func(ctx context.Context) error {
		parts, err := ExtractBatchImagePartsFromResultLine(line)
		if err != nil || len(parts.Images) == 0 {
			return err
		}
		return persistThumbnailReader(ctx, c.service, job, parts.CustomID, base64.NewDecoder(base64.StdEncoding, strings.NewReader(parts.Images[0].Base64Data)))
	})
}
