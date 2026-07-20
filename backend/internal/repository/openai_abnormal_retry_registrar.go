package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const openAIAbnormalRetryRedisTimeout = 75 * time.Millisecond

var openAIAbnormalRetryRedisRegisterScript = redis.NewScript(`
local body_bytes = tonumber(ARGV[1])
local ttl_ms = tonumber(ARGV[2])
local body_hash = ARGV[3]

local exact_val = redis.call("HMGET", KEYS[1], "count", "bytes")
local exact_count = tonumber(exact_val[1]) or 0
local exact_bytes = tonumber(exact_val[2]) or 0
exact_count = exact_count + 1
exact_bytes = exact_bytes + body_bytes
redis.call("HSET", KEYS[1], "count", exact_count, "bytes", exact_bytes)
redis.call("PEXPIRE", KEYS[1], ttl_ms)

local bucket_count = redis.call("HINCRBY", KEYS[2], "count", 1)
local bucket_bytes = redis.call("HINCRBY", KEYS[2], "bytes", body_bytes)
redis.call("PEXPIRE", KEYS[2], ttl_ms)

redis.call("PFADD", KEYS[3], body_hash)
redis.call("PEXPIRE", KEYS[3], ttl_ms)
local bucket_distinct = redis.call("PFCOUNT", KEYS[3])

return {exact_count, exact_bytes, bucket_count, bucket_bytes, bucket_distinct}
`)

type openAIAbnormalRetryRedisRegistrar struct {
	client *redis.Client
}

// NewOpenAIAbnormalRetryRegistrar provides the Redis-backed implementation of
// the service contract consumed by the OpenAI gateway handler.
func NewOpenAIAbnormalRetryRegistrar(client *redis.Client) service.OpenAIAbnormalRetryRegistrar {
	return &openAIAbnormalRetryRedisRegistrar{client: client}
}

func (r *openAIAbnormalRetryRedisRegistrar) Register(
	ctx context.Context,
	key string,
	bucketKey string,
	bodyHash string,
	bodyBytes int64,
	window time.Duration,
) (service.OpenAIAbnormalRetryRegistration, error) {
	if r == nil || r.client == nil {
		return service.OpenAIAbnormalRetryRegistration{}, errors.New("openai abnormal retry registrar: redis is unavailable")
	}
	if key == "" || bucketKey == "" || window <= 0 {
		return service.OpenAIAbnormalRetryRegistration{}, errors.New("openai abnormal retry registrar: invalid registration")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	redisCtx, cancel := context.WithTimeout(ctx, openAIAbnormalRetryRedisTimeout)
	defer cancel()

	raw, err := openAIAbnormalRetryRedisRegisterScript.Run(
		redisCtx,
		r.client,
		[]string{
			"openai:abretry:v1:exact:" + key,
			"openai:abretry:v1:bucket:" + bucketKey,
			"openai:abretry:v1:hll:" + bucketKey,
		},
		bodyBytes,
		int64(window/time.Millisecond),
		bodyHash,
	).Result()
	if err != nil {
		return service.OpenAIAbnormalRetryRegistration{}, fmt.Errorf("register openai abnormal retry state: %w", err)
	}

	count, err := redisScriptInt64At(raw, 0)
	if err != nil {
		return service.OpenAIAbnormalRetryRegistration{}, err
	}
	totalBytes, err := redisScriptInt64At(raw, 1)
	if err != nil {
		return service.OpenAIAbnormalRetryRegistration{}, err
	}
	bucketCount, err := redisScriptInt64At(raw, 2)
	if err != nil {
		return service.OpenAIAbnormalRetryRegistration{}, err
	}
	bucketBytes, err := redisScriptInt64At(raw, 3)
	if err != nil {
		return service.OpenAIAbnormalRetryRegistration{}, err
	}
	distinctHashes, err := redisScriptInt64At(raw, 4)
	if err != nil {
		return service.OpenAIAbnormalRetryRegistration{}, err
	}

	return service.OpenAIAbnormalRetryRegistration{
		Count:          count,
		TotalBytes:     totalBytes,
		BucketCount:    bucketCount,
		BucketBytes:    bucketBytes,
		DistinctHashes: distinctHashes,
	}, nil
}
