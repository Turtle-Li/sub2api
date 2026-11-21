package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const studioPosterPrefix = "video_studio:poster:v1:"

var _ service.VideoStudioPosterCache = (*gatewayCache)(nil)

func (c *gatewayCache) ClaimStudioPoster(ctx context.Context, key, lease string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, nil
	}
	return c.rdb.SetNX(ctx, studioPosterPrefix+key, lease, ttl).Result()
}

var saveStudioPoster = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
return redis.call('PSETEX', KEYS[1], ARGV[3], ARGV[2])`)

func (c *gatewayCache) SaveStudioPoster(ctx context.Context, key, lease string, image []byte, ttl time.Duration) error {
	if ttl.Milliseconds() <= 0 || len(image) > service.StudioPosterMaxBytes {
		return nil
	}
	return saveStudioPoster.Run(ctx, c.rdb, []string{studioPosterPrefix + key}, lease, image, ttl.Milliseconds()).Err()
}

func (c *gatewayCache) StudioPosterSizes(ctx context.Context, keys []string) ([]int64, error) {
	p := c.rdb.Pipeline()
	cmds := make([]*redis.IntCmd, len(keys))
	for i, k := range keys {
		cmds[i] = p.StrLen(ctx, studioPosterPrefix+k)
	}
	if _, err := p.Exec(ctx); err != nil {
		return nil, err
	}
	out := make([]int64, len(keys))
	for i, cmd := range cmds {
		out[i] = cmd.Val()
	}
	return out, nil
}

func (c *gatewayCache) StudioPosterBytes(ctx context.Context, key string) ([]byte, error) {
	b, err := c.rdb.GetRange(ctx, studioPosterPrefix+key, 0, service.StudioPosterMaxBytes).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return b, err
}
