package repository

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestOpenAIAbnormalRetryRegistrarSharesStateAcrossInstances(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	registrar := NewOpenAIAbnormalRetryRegistrar(rdb)

	window := time.Minute
	bodyBytes := int64(15 * 1000 * 1000)
	first, err := registrar.Register(context.Background(), "same-fingerprint", "same-bucket", "hash-1", bodyBytes, window)
	require.NoError(t, err)
	require.Equal(t, int64(1), first.Count)
	require.Equal(t, bodyBytes, first.TotalBytes)
	require.Equal(t, int64(1), first.BucketCount)
	require.Equal(t, bodyBytes, first.BucketBytes)
	require.Equal(t, int64(1), first.DistinctHashes)

	second, err := registrar.Register(context.Background(), "same-fingerprint", "same-bucket", "hash-1", bodyBytes, window)
	require.NoError(t, err)
	require.Equal(t, int64(2), second.Count)
	require.Equal(t, bodyBytes*2, second.TotalBytes)
	require.Equal(t, int64(2), second.BucketCount)
	require.Equal(t, bodyBytes*2, second.BucketBytes)
	require.Equal(t, int64(1), second.DistinctHashes)
	require.Positive(t, mr.TTL("openai:abretry:v1:exact:same-fingerprint"))
}

func TestOpenAIAbnormalRetryRegistrarTracksBucketCardinality(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	registrar := NewOpenAIAbnormalRetryRegistrar(rdb)

	bodyBytes := int64(15 * 1000 * 1000)
	var gotCount int64
	var gotDistinct int64
	for i := 0; i < 4; i++ {
		registration, err := registrar.Register(
			context.Background(),
			"fingerprint-"+strconv.Itoa(i),
			"api-path-le_15mb",
			"hash-"+strconv.Itoa(i),
			bodyBytes,
			time.Minute,
		)
		require.NoError(t, err)
		require.Equal(t, int64(1), registration.Count)
		require.Equal(t, bodyBytes, registration.TotalBytes)
		gotCount = registration.BucketCount
		gotDistinct = registration.DistinctHashes
	}
	require.Equal(t, int64(4), gotCount)
	require.Equal(t, int64(4), gotDistinct)
}
