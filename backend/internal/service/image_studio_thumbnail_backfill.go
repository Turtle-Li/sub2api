package service

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"time"
)

var studioThumbnailBackfills = make(chan struct{}, 2)

func (s *BatchImageDownloadService) backfillThumbnails(ctx context.Context, owner BatchImageOwner, job *BatchImageJob, requested string, previews *BatchImageThumbnailService) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	select {
	case studioThumbnailBackfills <- struct{}{}:
		defer func() { <-studioThumbnailBackfills }()
	case <-ctx.Done():
		return ctx.Err()
	}
	ctx, release, acquired := s.thumbnailLock.trySuffix(ctx, job.BatchID)
	if !acquired {
		return ErrStudioThumbnailPending
	}
	defer release()
	// A peer or an earlier request may already have completed this batch.
	if _, err := previews.Open(ctx, job.BatchID, requested); err == nil {
		return nil
	} else if !errors.Is(err, ErrStudioThumbnailObjectMissing) {
		return err
	}
	retryAt, err := previews.Repo.BatchImageThumbnailRetryAt(ctx, job.BatchID)
	if err != nil {
		return err
	}
	if retryAt != nil && retryAt.After(time.Now()) {
		return ErrStudioThumbnailDeferred
	}
	permit, err := s.acquirePermit(ctx, owner.UserID, "item")
	if err != nil {
		return err
	}
	if permit != nil {
		defer permit.Release(context.WithoutCancel(ctx))
	}
	if err := previews.Repo.DeferBatchImageThumbnailBackfill(ctx, job.BatchID, time.Now().Add(2*time.Minute)); err != nil {
		return err
	}
	missing, err := s.missingThumbnailItems(ctx, job, previews)
	if err != nil || len(missing) == 0 {
		return err
	}
	upscaled := make(map[string]*BatchImageItem)
	for id, item := range missing {
		if isBatchImageUpscaledItem(item) {
			upscaled[id] = item
			delete(missing, id)
		}
	}
	upscaleErr := s.backfillUpscaledThumbnails(ctx, job, upscaled, previews)
	if len(missing) == 0 {
		return upscaleErr
	}
	provider, account, err := s.providerAndAccount(ctx, job)
	if err != nil {
		return err
	}
	r, _, err := provider.OpenResult(ctx, job, account)
	if err != nil {
		return ErrBatchImageResultMissing
	}
	defer r.Close()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), batchImageDownloadScannerMaxLineBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts, err := ExtractBatchImagePartsFromResultLine([]byte(line))
		if err != nil {
			return ErrBatchImageDownloadFailed
		}
		if missing[parts.CustomID] == nil || len(parts.Images) == 0 {
			continue
		}
		image := parts.Images[0]
		if err := persistThumbnailReader(ctx, previews, job, parts.CustomID, base64.NewDecoder(base64.StdEncoding, strings.NewReader(image.Base64Data))); err != nil {
			continue // Other successful items still get durable covers.
		}
		delete(missing, parts.CustomID)
	}
	if scanner.Err() != nil || len(missing) != 0 {
		return ErrBatchImageDownloadFailed
	}
	return upscaleErr
}

func (s *BatchImageDownloadService) missingThumbnailItems(ctx context.Context, job *BatchImageJob, previews *BatchImageThumbnailService) (map[string]*BatchImageItem, error) {
	missing := make(map[string]*BatchImageItem)
	const pageSize = 500
	for offset := 0; ; offset += pageSize {
		items, err := s.Repo.ListBatchImageItems(ctx, job.BatchID, BatchImageItemFilter{Status: BatchImageItemStatusSuccess, Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.ImageCount < 1 {
				continue
			}
			if _, err := previews.Open(ctx, job.BatchID, item.CustomID); errors.Is(err, ErrStudioThumbnailObjectMissing) {
				missing[item.CustomID] = item
			} else if err != nil {
				return nil, err
			}
		}
		if len(items) < pageSize {
			return missing, nil
		}
	}
}

func (s *BatchImageDownloadService) backfillUpscaledThumbnails(ctx context.Context, job *BatchImageJob, items map[string]*BatchImageItem, previews *BatchImageThumbnailService) error {
	var failures error
	for _, item := range items {
		key, err := batchImageUpscaleObjectKey(s.Config, job.BatchID, item.CustomID, 0, batchImageDerefString(item.FileExtension))
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		r, _, _, err := previews.Store.Open(ctx, key)
		if err != nil {
			failures = errors.Join(failures, ErrBatchImageResultMissing)
			continue
		}
		err = persistThumbnailReader(ctx, previews, job, item.CustomID, r)
		_ = r.Close()
		if err != nil {
			failures = errors.Join(failures, err)
		}
	}
	return failures
}

func persistThumbnailReader(ctx context.Context, previews *BatchImageThumbnailService, job *BatchImageJob, customID string, r io.Reader) error {
	source, err := io.ReadAll(io.LimitReader(r, studioThumbnailSourceLimit(job)+1))
	if err != nil {
		return err
	}
	return previews.Persist(ctx, job, customID, source)
}
