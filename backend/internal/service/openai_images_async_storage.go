package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
)

type asyncImageTaskStorageContextKey struct{}

type asyncImageTaskStorageState struct {
	taskID string
	stored atomic.Bool
}

// WithAsyncImageTaskStorage binds an async task ID to the internal gateway
// request. High-resolution OpenAI image results then use the task ID for their
// first object-storage write, so the task finalizer does not upload them twice.
func WithAsyncImageTaskStorage(ctx context.Context, taskID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, asyncImageTaskStorageContextKey{}, &asyncImageTaskStorageState{
		taskID: strings.TrimSpace(taskID),
	})
}

func asyncImageTaskStorageID(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	state, _ := ctx.Value(asyncImageTaskStorageContextKey{}).(*asyncImageTaskStorageState)
	if state == nil || state.taskID == "" {
		return "", false
	}
	return state.taskID, true
}

func markAsyncImageTaskStorageComplete(ctx context.Context) {
	if ctx == nil {
		return
	}
	state, _ := ctx.Value(asyncImageTaskStorageContextKey{}).(*asyncImageTaskStorageState)
	if state != nil {
		state.stored.Store(true)
	}
}

// AsyncImageTaskStorageComplete reports whether the gateway already stored the
// final high-resolution result under the async task ID.
func AsyncImageTaskStorageComplete(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	state, _ := ctx.Value(asyncImageTaskStorageContextKey{}).(*asyncImageTaskStorageState)
	return state != nil && state.stored.Load()
}

func (s *OpenAIGatewayService) storeHighResolutionOpenAIImages(
	ctx context.Context,
	path string,
	imageCount int,
	body []byte,
) ([]byte, error) {
	taskID, asyncRequired := asyncImageTaskStorageID(ctx)
	if s == nil || s.imageStorageResolver == nil {
		if asyncRequired {
			return nil, imageUpscaleError("STORAGE_UNAVAILABLE", http.StatusServiceUnavailable, false, nil)
		}
		return body, nil
	}
	uploader, enabled := s.imageStorageResolver()
	if !enabled || uploader == nil {
		if asyncRequired {
			return nil, imageUpscaleError("STORAGE_UNAVAILABLE", http.StatusServiceUnavailable, false, nil)
		}
		return body, nil
	}
	// Mini may already have written the final bytes to the same object store via
	// a scoped PUT URL. Keep the URL and avoid pulling the large object back into
	// Sub2API just to upload it again.
	if imageDirectUploadEnabled(ctx) && validateStoredImageTaskResult(body) == nil {
		markAsyncImageTaskStorageComplete(ctx)
		return body, nil
	}

	resultID := newSynchronousImageResultID()
	if asyncRequired {
		resultID = taskID
	}
	storedBody, err := uploader.Rewrite(ctx, resultID, body)
	if err != nil {
		if asyncRequired {
			return nil, imageUpscaleError("STORAGE_FAILED", http.StatusBadGateway, false, err)
		}
		logImageStorageFallback(path, imageCount, err)
		return body, nil
	}
	if asyncRequired {
		if err := validateStoredImageTaskResult(storedBody); err != nil {
			return nil, imageUpscaleError("STORAGE_FAILED", http.StatusBadGateway, false, fmt.Errorf("validate stored image result: %w", err))
		}
		markAsyncImageTaskStorageComplete(ctx)
	}
	return storedBody, nil
}
