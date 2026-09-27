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
)

const (
	batchImageUpscaleMarker         = "cos-upscaled:v1"
	batchImageUpscaleImagesPerLine  = 1
	batchImageUpscaleDeleteAttempts = 3
	batchImageUpscaleDeleteTimeout  = 30 * time.Second
	batchImageUpscalePutTimeout     = 2 * time.Minute
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
	if job == nil {
		return false
	}
	_, required := Image25UpscaleScale(job.Model, job.ImageSize)
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

func upscaleBatchImageResultLine(
	ctx context.Context,
	cfg *config.Config,
	upscaler *ImageUpscaleService,
	store BatchImageUpscaleObjectStore,
	writeFencer BatchImageUpscaleWriteFencer,
	job *BatchImageJob,
	line []byte,
) (mimeType, extension string, imageCount int, storedKeys []string, err error) {
	if job == nil || upscaler == nil || !upscaler.Active() || store == nil || writeFencer == nil {
		return "", "", 0, nil, imageUpscaleError("UNAVAILABLE", 0, false, nil)
	}
	scale, required := Image25UpscaleScale(job.Model, job.ImageSize)
	if !required {
		return "", "", 0, nil, errors.New("batch item does not require upscale")
	}
	parts, err := ExtractBatchImagePartsFromResultLine(line)
	if err != nil || strings.TrimSpace(parts.CustomID) == "" {
		return "", "", 0, nil, imageUpscaleError("SOURCE_RESULT_INVALID", 0, false, err)
	}
	if len(parts.Images) != batchImageUpscaleImagesPerLine {
		return "", "", 0, nil, imageUpscaleError(
			"SOURCE_RESULT_INVALID",
			0,
			false,
			fmt.Errorf("result line contains %d images, expected %d", len(parts.Images), batchImageUpscaleImagesPerLine),
		)
	}
	for index, imagePart := range parts.Images {
		result, upscaleErr := upscaler.UpscaleBase64(ctx, imagePart.Base64Data, scale)
		if upscaleErr != nil {
			err = upscaleErr
			break
		}
		ext := batchImageFileExtension(result.MimeType)
		key, keyErr := batchImageUpscaleObjectKey(cfg, job.BatchID, parts.CustomID, index, ext)
		if keyErr == nil {
			putCtx, cancelPut := context.WithTimeout(ctx, batchImageUpscalePutTimeout)
			permit, fenceErr := writeFencer.BeginBatchImageUpscaleWrite(putCtx, job.BatchID)
			if fenceErr != nil {
				if errors.Is(fenceErr, ErrBatchImageIndexStateConflict) {
					keyErr = fenceErr
				} else {
					keyErr = ErrBatchImageUpscaleWriteFenceFailed.WithCause(fenceErr)
				}
			} else if permit == nil {
				keyErr = ErrBatchImageUpscaleWriteFenceFailed
			} else {
				keyErr = store.Put(putCtx, key, result.MimeType, bytes.NewReader(result.Data), int64(len(result.Data)))
				if releaseErr := permit.Release(); keyErr == nil && releaseErr != nil {
					keyErr = ErrBatchImageUpscaleWriteFenceFailed.WithCause(releaseErr)
				}
			}
			cancelPut()
		}
		clearBytes(result.Data)
		if keyErr != nil {
			err = imageUpscaleError("STORE_FAILED", 0, true, keyErr)
			break
		}
		storedKeys = append(storedKeys, key)
		if mimeType == "" {
			mimeType = result.MimeType
			extension = ext
		}
		imageCount++
	}
	if err != nil {
		return "", "", 0, nil, err
	}
	return mimeType, extension, imageCount, storedKeys, nil
}

func batchImageUpscaleFailure(err error) (string, string) {
	code := "IMAGE_UPSCALE_FAILED"
	var upscaleErr *ImageUpscaleError
	if errors.As(err, &upscaleErr) && strings.TrimSpace(upscaleErr.Code) != "" {
		code = "IMAGE_UPSCALE_" + strings.TrimSpace(upscaleErr.Code)
	}
	return code, "image upscale failed"
}
