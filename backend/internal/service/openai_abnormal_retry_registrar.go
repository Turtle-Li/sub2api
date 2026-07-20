package service

import (
	"context"
	"time"
)

// OpenAIAbnormalRetryRegistration is the shared retry state returned by the
// infrastructure layer. Keeping this contract in service prevents handlers
// from depending directly on Redis.
type OpenAIAbnormalRetryRegistration struct {
	Count          int64
	TotalBytes     int64
	BucketCount    int64
	BucketBytes    int64
	DistinctHashes int64
}

// OpenAIAbnormalRetryRegistrar atomically records exact fingerprints and
// coarse body-size buckets for abnormal retry protection.
type OpenAIAbnormalRetryRegistrar interface {
	Register(
		ctx context.Context,
		key string,
		bucketKey string,
		bodyHash string,
		bodyBytes int64,
		window time.Duration,
	) (OpenAIAbnormalRetryRegistration, error)
}
