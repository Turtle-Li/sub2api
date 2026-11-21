package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	batchImageUpscaleMarker         = "cos-upscaled:v1"
	batchImageUpscaleImagesPerLine  = 1
	batchImageUpscaleDeleteAttempts = 3
	batchImageUpscaleDeleteTimeout  = 30 * time.Second
	batchImageUpscalePutTimeout     = 2 * time.Minute
	batchImageUpscalePutMaxAttempts = 2
	batchImageUpscalePutRetryDelay  = 250 * time.Millisecond
	// BatchImageUpscaleCleanupGrace is defense-in-depth after the transactional
	// write fence has serialized every Put against terminal transitions.
	BatchImageUpscaleCleanupGrace = batchImageUpscalePutTimeout + 30*time.Second
)

var batchImageUpscaleExtensions = [...]string{"jpg", "png", "webp"}

// BatchImageUpscaleWriteFencer holds the durable job row lock from the last
// indexing-state check until the COS Put returns. Terminal transitions wait on
// the same row, so an expired Redis lease cannot resume a stale Put after
// cleanup has completed.
type BatchImageUpscaleWriteFencer interface {
	BeginBatchImageUpscaleWrite(ctx context.Context, batchID string) (BatchImageUpscaleWritePermit, error)
}

type BatchImageUpscaleWritePermit interface {
	Release() error
}

func batchImageJobRequiresUpscale(job *BatchImageJob) bool {
	if job != nil && isImageBridgeProvider(job.Provider) {
		return false
	}
	if job == nil {
		return false
	}
	_, required := RequestedImageUpscaleScale(job.ImageSize)
	return required
}

func batchImageUpscaleObjectKey(cfg *config.Config, batchID, customID string, imageIndex int, extension string) (string, error) {
	if cfg == nil || strings.TrimSpace(batchID) == "" || strings.TrimSpace(customID) == "" || imageIndex < 0 {
		return "", errors.New("invalid batch upscale object identity")
	}
	for _, character := range batchID {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return "", errors.New("invalid batch upscale batch id")
	}
	prefix := strings.Trim(strings.TrimSpace(cfg.BatchImage.DeliveryCOSPrefix), "/")
	if prefix == "" || strings.Contains(prefix, "..") {
		return "", errors.New("invalid batch upscale object prefix")
	}
	extension = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(extension)), ".")
	switch extension {
	case "png", "jpg", "webp":
	default:
		return "", errors.New("invalid batch upscale object extension")
	}
	digest := sha256.Sum256([]byte(customID))
	return path.Join(prefix, "upscaled", batchID, hex.EncodeToString(digest[:16]), fmt.Sprintf("%02d.%s", imageIndex, extension)), nil
}

func isBatchImageUpscaledItem(item *BatchImageItem) bool {
	return item != nil && item.ProviderSourceObject != nil && strings.TrimSpace(*item.ProviderSourceObject) == batchImageUpscaleMarker
}

// batchImageUpscaleRecoveryKeys derives the complete bounded object namespace.
// Submission persists every custom ID before provider work begins, retries
// overwrite deterministic keys, and terminal cleanup sweeps every allowed
// extension without ListBucket permission or a separate object journal.
func batchImageUpscaleRecoveryKeys(cfg *config.Config, batchID string, customIDs []string) ([]string, error) {
	customIDs = append([]string(nil), customIDs...)
	sort.Strings(customIDs)
	keys := make([]string, 0, len(customIDs)*batchImageUpscaleImagesPerLine*len(batchImageUpscaleExtensions))
	for _, customID := range customIDs {
		for index := 0; index < batchImageUpscaleImagesPerLine; index++ {
			for _, extension := range batchImageUpscaleExtensions {
				key, err := batchImageUpscaleObjectKey(cfg, batchID, customID, index, extension)
				if err != nil {
					return nil, err
				}
				keys = append(keys, key)
			}
		}
	}
	return keys, nil
}

func deleteBatchImageUpscaleKeys(ctx context.Context, store BatchImageUpscaleObjectStore, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	if store == nil {
		return ErrBatchImageUpscaleCleanupFailed
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, batchImageUpscaleDeleteTimeout)
	defer cancel()
	var lastErr error
	for attempt := 0; attempt < batchImageUpscaleDeleteAttempts; attempt++ {
		if err := store.Delete(cleanupCtx, keys); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt+1 < batchImageUpscaleDeleteAttempts {
			delay := time.Duration(1<<attempt) * 100 * time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-cleanupCtx.Done():
				timer.Stop()
				return ErrBatchImageUpscaleCleanupFailed.WithCause(cleanupCtx.Err())
			case <-timer.C:
			}
		}
	}
	return ErrBatchImageUpscaleCleanupFailed.WithCause(lastErr)
}

type batchImageUpscaledOutput struct {
	mimeType    string
	extension   string
	imageCount  int
	previewSize *BatchImagePreviewSize
}

type batchImageUpscaleDelivery struct {
	config      *config.Config
	upscaler    *ImageUpscaleService
	store       BatchImageUpscaleObjectStore
	writeFencer BatchImageUpscaleWriteFencer
	checkpoints BatchImageResultCheckpointStore
	thumbnails  *batchImageThumbnailCapture
}

func (d batchImageUpscaleDelivery) process(ctx, upscaleCtx context.Context, job *BatchImageJob, line []byte) (*batchImageUpscaledOutput, error) {
	if job == nil || d.store == nil || d.writeFencer == nil {
		return nil, imageUpscaleError("UNAVAILABLE", 0, false, nil)
	}
	if _, required := RequestedImageUpscaleScale(job.ImageSize); !required {
		return nil, errors.New("batch item does not require upscale")
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, imageUpscaleError("JOB_TIMEOUT", 0, true, ctxErr)
	}
	parts, err := ExtractBatchImagePartsFromResultLine(line)
	if err != nil || strings.TrimSpace(parts.CustomID) == "" {
		return nil, imageUpscaleError("SOURCE_RESULT_INVALID", 0, false, err)
	}
	if len(parts.Images) != batchImageUpscaleImagesPerLine {
		return nil, imageUpscaleError("SOURCE_RESULT_INVALID", 0, false,
			fmt.Errorf("result line contains %d images, expected %d", len(parts.Images), batchImageUpscaleImagesPerLine))
	}

	var checkpoint *BatchImageResultCheckpoint
	if d.checkpoints != nil {
		checkpoint, err = d.checkpoints.GetBatchImageCheckpoint(ctx, job.BatchID, parts.CustomID, BatchImageCheckpointFinal)
		if err != nil {
			return nil, batchImageDeliveryError("final_checkpoint", "IMAGE_FINAL_CHECKPOINT_READ_FAILED", err)
		}
	}
	if checkpoint == nil {
		if upscaleCtx.Err() != nil {
			return nil, imageUpscaleError("JOB_TIMEOUT", 0, true, upscaleCtx.Err())
		}
		if d.upscaler == nil || !d.upscaler.Active() {
			return nil, imageUpscaleError("UNAVAILABLE", 0, false, nil)
		}
		result, upscaleErr := d.makeResult(upscaleCtx, job, parts.Images[0].Base64Data)
		if upscaleErr != nil {
			return nil, upscaleErr
		}
		defer clearBytes(result.Data)
		checkpoint = &BatchImageResultCheckpoint{Data: result.Data, MimeType: result.MimeType, Width: result.Width, Height: result.Height}
		if d.checkpoints != nil {
			created, stageErr := checkpointBatchImageResult(ctx, d.checkpoints, job.BatchID, parts.CustomID, BatchImageCheckpointFinal, *checkpoint)
			if stageErr != nil {
				return nil, batchImageDeliveryError("final_checkpoint", "IMAGE_FINAL_CHECKPOINT_WRITE_FAILED", stageErr)
			}
			if !created {
				// A conflicting worker or an unacknowledged commit may have won.
				// Only the canonical checkpoint can be published.
				checkpoint, err = d.checkpoints.GetBatchImageCheckpoint(ctx, job.BatchID, parts.CustomID, BatchImageCheckpointFinal)
				if err != nil || checkpoint == nil {
					return nil, batchImageDeliveryError("final_checkpoint", "IMAGE_FINAL_CHECKPOINT_READ_FAILED", err)
				}
			}
		}
	}
	defer clearBytes(checkpoint.Data)
	return d.publish(ctx, job, parts.CustomID, checkpoint)
}

func (d batchImageUpscaleDelivery) makeResult(ctx context.Context, job *BatchImageJob, encoded string) (*ImageUpscaleResult, error) {
	normalized, err := prepareUpscaleSourceBase64(encoded, imageUpscaleMaxSourceBytes)
	if err != nil {
		return nil, err
	}
	source, err := decodePreparedUpscaleSourceBase64(normalized)
	if err != nil {
		return nil, err
	}
	width, height, mime, err := decodeUpscaleImageConfig(source)
	if err != nil {
		clearBytes(source)
		return nil, imageUpscaleError("INVALID_SOURCE_IMAGE", 0, false, err)
	}
	if scale, required := imageUpscaleScaleForDimensions(job.ImageSize, width, height); required {
		clearBytes(source)
		return d.upscaler.UpscaleBase64Batch(ctx, encoded, scale)
	}
	return &ImageUpscaleResult{Data: source, MimeType: mime, Width: width, Height: height, Scale: 1}, nil
}

func (d batchImageUpscaleDelivery) publish(ctx context.Context, job *BatchImageJob, customID string, checkpoint *BatchImageResultCheckpoint) (*batchImageUpscaledOutput, error) {
	if d.store == nil || d.writeFencer == nil {
		return nil, batchImageDeliveryError("final_archive", "IMAGE_FINAL_STORE_UNAVAILABLE", nil)
	}
	ext := batchImageFileExtension(checkpoint.MimeType)
	key, err := batchImageUpscaleObjectKey(d.config, job.BatchID, customID, 0, ext)
	if err == nil {
		err = writeBatchImageUpscaleObject(ctx, d.writeFencer, d.store, job.BatchID, key, &ImageUpscaleResult{
			Data: checkpoint.Data, MimeType: checkpoint.MimeType,
		})
	}
	if err != nil {
		if d.checkpoints != nil && !errors.Is(err, ErrBatchImageIndexStateConflict) {
			return nil, batchImageDeliveryError("final_archive", "IMAGE_UPSCALE_STORE_FAILED", err)
		}
		return nil, imageUpscaleError("STORE_FAILED", 0, true, err)
	}
	if d.thumbnails != nil {
		d.thumbnails.persistSource(ctx, job, customID, checkpoint.Data)
	}
	return &batchImageUpscaledOutput{
		mimeType: checkpoint.MimeType, extension: ext, imageCount: 1,
		previewSize: &BatchImagePreviewSize{Width: checkpoint.Width, Height: checkpoint.Height},
	}, nil
}

func writeBatchImageUpscaleObject(
	ctx context.Context, writeFencer BatchImageUpscaleWriteFencer, store BatchImageUpscaleObjectStore,
	batchID, key string, result *ImageUpscaleResult,
) error {
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		putCtx, cancelPut := context.WithTimeout(ctx, batchImageUpscalePutTimeout)
		permit, fenceErr := writeFencer.BeginBatchImageUpscaleWrite(putCtx, batchID)
		if fenceErr != nil || permit == nil {
			cancelPut()
			if errors.Is(fenceErr, ErrBatchImageIndexStateConflict) {
				return fenceErr
			}
			if fenceErr != nil {
				return ErrBatchImageUpscaleWriteFenceFailed.WithCause(fenceErr)
			}
			return ErrBatchImageUpscaleWriteFenceFailed
		}

		started := time.Now()
		putErr := store.Put(putCtx, key, result.MimeType, bytes.NewReader(result.Data), int64(len(result.Data)))
		elapsed := time.Since(started)
		writeDeadlineReached := errors.Is(putCtx.Err(), context.DeadlineExceeded)
		releaseErr := permit.Release()
		cancelPut()
		if releaseErr != nil {
			return ErrBatchImageUpscaleWriteFenceFailed.WithCause(releaseErr)
		}
		if putErr == nil {
			return nil
		}

		// The storage client handles ordinary retryable responses. A stalled write
		// can exhaust its context; retry the same bytes with a fresh deadline and
		// a new permit, never rerun generation or upscale.
		retrying := writeDeadlineReached && ctx.Err() == nil && attempt < batchImageUpscalePutMaxAttempts
		fields := []zap.Field{
			zap.String("batch_id", batchID), zap.Int("attempt", attempt),
			zap.Int("bytes", len(result.Data)), zap.Int64("elapsed_ms", elapsed.Milliseconds()),
			zap.Bool("write_deadline_reached", writeDeadlineReached), zap.Bool("retrying", retrying),
		}
		var diagnostic interface{ BatchImageStorageDiagnostic() string }
		if errors.As(putErr, &diagnostic) {
			fields = append(fields, zap.String("storage_error", diagnostic.BatchImageStorageDiagnostic()))
		}
		logger.L().Warn("batch_image.upscaled_object_write_failed", fields...)
		if !retrying {
			return putErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(batchImageUpscalePutRetryDelay):
		}
	}
}

func batchImageUpscaleFailure(err error) (string, string) {
	code := "IMAGE_UPSCALE_FAILED"
	var upscaleErr *ImageUpscaleError
	if errors.As(err, &upscaleErr) && strings.TrimSpace(upscaleErr.Code) != "" {
		code = "IMAGE_UPSCALE_" + strings.TrimSpace(upscaleErr.Code)
	}
	return code, "image upscale failed"
}

// Retry only errors known to precede both the Mini and any paid fallback call.
// Lost submit/results and fallback errors do not authorize a new upscale.
func safeBatchImageUpscaleRetry(err error) bool {
	var upscaleErr *ImageUpscaleError
	if !errors.As(err, &upscaleErr) {
		return false
	}
	switch upscaleErr.Code {
	case "UNAVAILABLE", "CREDENTIAL_UNAVAILABLE", "BACKPRESSURE", "QUEUE_TIMEOUT":
		return true
	default:
		return false
	}
}
