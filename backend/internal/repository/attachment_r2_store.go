package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// NewAttachmentR2StoreFactory builds the private S3-compatible adapter used by
// Attachment Gateway's independently persisted R2 configuration.
func NewAttachmentR2StoreFactory() service.AttachmentR2StoreFactory {
	return func(ctx context.Context, cfg *service.AttachmentR2Config) (service.AttachmentR2ObjectStore, error) {
		client, err := newS3Client(ctx, s3ClientParams{
			Endpoint:        cfg.Endpoint,
			Region:          cfg.Region,
			AccessKeyID:     cfg.AccessKeyID,
			SecretAccessKey: cfg.SecretAccessKey,
			ForcePathStyle:  cfg.ForcePathStyle,
		})
		if err != nil {
			return nil, err
		}
		return newS3ImageStorage(
			client,
			cfg.Bucket,
			"",
			time.Duration(cfg.PresignExpiryMinutes)*time.Minute,
		), nil
	}
}
