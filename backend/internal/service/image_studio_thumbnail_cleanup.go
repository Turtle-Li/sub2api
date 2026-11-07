package service

import "context"

func (s *BatchImageCleanupService) cleanupThumbnails(ctx context.Context, job *BatchImageJob) error {
	repo, ok := s.Repo.(BatchImageThumbnailRepository)
	if !ok {
		return nil
	}
	// Durable before external deletion: late workers cannot recreate previews.
	if err := repo.DisableBatchImageThumbnails(ctx, job.BatchID); err != nil {
		return err
	}
	previews := newBatchImageThumbnailService(s.Repo, s.DeliveryStore, s.Config)
	if previews == nil {
		return nil
	}
	const pageSize = 500
	for offset := 0; ; offset += pageSize {
		items, err := s.Repo.ListBatchImageItems(ctx, job.BatchID, BatchImageItemFilter{Limit: pageSize, Offset: offset})
		if err != nil {
			return err
		}
		keys := make([]string, 0, len(items))
		for _, item := range items {
			key, err := studioThumbnailObjectKey(s.Config, job.BatchID, item.CustomID)
			if err != nil {
				return err
			}
			keys = append(keys, key)
		}
		if len(keys) > 0 {
			if err := previews.Store.Delete(ctx, keys); err != nil {
				return err
			}
		}
		if len(items) < pageSize {
			return nil
		}
	}
}
