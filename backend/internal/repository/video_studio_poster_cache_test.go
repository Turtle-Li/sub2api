package repository

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestVideoStudioPosterCacheLeaseAndExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	rd := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rd.Close() })
	c := NewGatewayCache(rd).(service.VideoStudioPosterCache)
	ctx := context.Background()
	won, err := c.ClaimStudioPoster(ctx, "one", "leaseA", time.Minute)
	require.NoError(t, err)
	require.True(t, won)
	won, err = c.ClaimStudioPoster(ctx, "one", "leaseB", time.Minute)
	require.NoError(t, err)
	require.False(t, won)
	p := bytes.Repeat([]byte{9}, 512)
	require.NoError(t, c.SaveStudioPoster(ctx, "one", "leaseB", p, time.Hour))
	s, err := c.StudioPosterSizes(ctx, []string{"one", "other"})
	require.NoError(t, err)
	require.Equal(t, []int64{int64(len("leaseA")), 0}, s)
	mr.FastForward(time.Minute)
	won, err = c.ClaimStudioPoster(ctx, "one", "leaseB", time.Minute)
	require.NoError(t, err)
	require.True(t, won)
	require.NoError(t, c.SaveStudioPoster(ctx, "one", "leaseA", p, time.Hour))
	require.NoError(t, c.SaveStudioPoster(ctx, "one", "leaseB", p, time.Hour))
	frame, err := c.StudioPosterBytes(ctx, "one")
	require.NoError(t, err)
	require.Equal(t, p, frame)
	mr.FastForward(time.Hour)
	s, err = c.StudioPosterSizes(ctx, []string{"one"})
	require.NoError(t, err)
	require.Equal(t, []int64{0}, s)
}
