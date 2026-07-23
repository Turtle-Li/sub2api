//go:build unit

package repository

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBatchImageCOSPresignUsesPrivateBucketVirtualHost(t *testing.T) {
	store := ProvideBatchImageDeliveryObjectStore(&config.Config{
		BatchImage: config.BatchImageConfig{
			DeliveryEnabled:            true,
			DeliveryCOSEndpoint:        "https://cos.ap-shanghai.myqcloud.com",
			DeliveryCOSRegion:          "ap-shanghai",
			DeliveryCOSBucket:          "image-1309919944",
			DeliveryCOSAccessKeyID:     "AKIDEXAMPLE",
			DeliveryCOSSecretAccessKey: "test-secret-only",
		},
	})
	require.NotNil(t, store)
	signed, err := store.PresignPut(
		context.Background(),
		"sub2-batch-image/prod/imgbatch_0123456789abcdef0123456789abcdef/hash/0",
		time.Hour,
	)
	require.NoError(t, err)
	parsed, err := url.Parse(signed)
	require.NoError(t, err)
	require.Equal(t, "https", parsed.Scheme)
	require.Equal(t, "image-1309919944.cos.ap-shanghai.myqcloud.com", parsed.Host)
	require.Contains(t, parsed.Path, "/sub2-batch-image/prod/")
	require.NotEmpty(t, parsed.Query().Get("X-Amz-Signature"))
	require.NotContains(t, signed, "test-secret-only")
}
