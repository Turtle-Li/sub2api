package repository

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newAuthorizationFenceCacheForTest(t *testing.T) (*concurrencyCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	cache, ok := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	require.True(t, ok)
	cache.EnableUserConcurrencyAuthorizationFence()
	return cache, server
}

func markAuthorizationFenceReady(t *testing.T, ctx context.Context, cache *concurrencyCache) {
	t.Helper()
	token, err := cache.BeginUserConcurrencyAuthorizationFenceReconcile(ctx)
	require.NoError(t, err)
	ready, err := cache.FinishUserConcurrencyAuthorizationFenceReconcile(ctx, token)
	require.NoError(t, err)
	require.True(t, ready)
}

func TestUserConcurrencyAuthorizationCeilingCASRejectsStaleWidenAndAllowsNewerRestore(t *testing.T) {
	ctx := context.Background()
	cache, _ := newAuthorizationFenceCacheForTest(t)
	userID := int64(72001)

	applied, err := cache.SetUserConcurrencyAuthorizationCeiling(ctx, userID,
		service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 8, Revision: 1})
	require.NoError(t, err)
	require.True(t, applied)

	// A newly reserved refund recomputes the durable cap to 3. An old release
	// callback that had read 8 must not reopen admission to 8.
	applied, err = cache.SetUserConcurrencyAuthorizationCeiling(ctx, userID,
		service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 3, Revision: 2})
	require.NoError(t, err)
	require.True(t, applied)
	applied, err = cache.SetUserConcurrencyAuthorizationCeiling(ctx, userID,
		service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 8, Revision: 1})
	require.NoError(t, err)
	require.False(t, applied)

	ceiling, err := cache.rdb.HGet(ctx, userAuthorizationCeilingKey(userID), "ceiling").Int()
	require.NoError(t, err)
	require.Equal(t, 3, ceiling)
	revision, err := cache.rdb.HGet(ctx, userAuthorizationCeilingKey(userID), "revision").Int64()
	require.NoError(t, err)
	require.Equal(t, int64(2), revision)

	markAuthorizationFenceReady(t, ctx, cache)
	for _, requestID := range []string{"one", "two", "three"} {
		acquired, err := cache.AcquireUserSlot(ctx, userID, 8, requestID)
		require.NoError(t, err)
		require.True(t, acquired)
	}
	acquired, err := cache.AcquireUserSlot(ctx, userID, 8, "four-blocked")
	require.NoError(t, err)
	require.False(t, acquired)

	// A later legitimate SET/DELTA/purchase produces a greater durable
	// revision. It restores the higher cap; this is not a permanent min-only
	// clamp left behind by the refund.
	applied, err = cache.SetUserConcurrencyAuthorizationCeiling(ctx, userID,
		service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 8, Revision: 3})
	require.NoError(t, err)
	require.True(t, applied)
	acquired, err = cache.AcquireUserSlot(ctx, userID, 8, "four-allowed")
	require.NoError(t, err)
	require.True(t, acquired)
}

func TestUserConcurrencyAuthorizationCeilingPreservesUnlimitedZero(t *testing.T) {
	ctx := context.Background()
	cache, _ := newAuthorizationFenceCacheForTest(t)
	userID := int64(72002)
	_, err := cache.SetUserConcurrencyAuthorizationCeiling(ctx, userID,
		service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 0, Revision: 1})
	require.NoError(t, err)
	markAuthorizationFenceReady(t, ctx, cache)

	acquired, err := cache.AcquireUserSlot(ctx, userID, 0, "unlimited")
	require.NoError(t, err)
	require.True(t, acquired, "zero remains the existing unlimited semantic")
}

type authorizationFenceRaceCache struct {
	*concurrencyCache
	server         *miniredis.Miniredis
	userID         int64
	newer          service.UserConcurrencyAuthorizationFenceProjection
	flushBeforeOld bool
	fired          bool
}

func (c *authorizationFenceRaceCache) SetUserConcurrencyAuthorizationCeiling(
	ctx context.Context,
	userID int64,
	projection service.UserConcurrencyAuthorizationFenceProjection,
) (bool, error) {
	if !c.fired && userID == c.userID && projection.Revision < c.newer.Revision {
		c.fired = true
		_, err := c.concurrencyCache.SetUserConcurrencyAuthorizationCeiling(ctx, c.userID, c.newer)
		if err != nil {
			return false, err
		}
		if c.flushBeforeOld {
			c.server.FlushAll()
		}
	}
	return c.concurrencyCache.SetUserConcurrencyAuthorizationCeiling(ctx, userID, projection)
}

func TestUserConcurrencyAuthorizationFenceReconcileNeverClearsConcurrentNewerProjection(t *testing.T) {
	ctx := context.Background()
	base, server := newAuthorizationFenceCacheForTest(t)
	userID := int64(72003)
	old := service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 8, Revision: 1}
	newer := service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 3, Revision: 2}
	race := &authorizationFenceRaceCache{concurrencyCache: base, server: server, userID: userID, newer: newer}
	svc := service.NewConcurrencyService(race)

	err := svc.ConfigureUserConcurrencyAuthorizationFence(
		func(context.Context) (map[int64]service.UserConcurrencyAuthorizationFenceProjection, error) {
			if race.fired {
				return map[int64]service.UserConcurrencyAuthorizationFenceProjection{userID: newer}, nil
			}
			return map[int64]service.UserConcurrencyAuthorizationFenceProjection{userID: old}, nil
		},
		func(context.Context, int64) (service.UserConcurrencyAuthorizationFenceProjection, bool, error) {
			return newer, true, nil
		},
	)
	require.NoError(t, err)
	require.True(t, race.fired)

	ceiling, err := base.rdb.HGet(ctx, userAuthorizationCeilingKey(userID), "ceiling").Int()
	require.NoError(t, err)
	require.Equal(t, 3, ceiling, "the stale full snapshot must not clear a newly reserved cap")
	ready, err := base.UserConcurrencyAuthorizationFenceReady(ctx)
	require.NoError(t, err)
	require.True(t, ready)
}

func TestUserConcurrencyAuthorizationFenceRedisRestartCannotPublishStaleSnapshot(t *testing.T) {
	ctx := context.Background()
	base, server := newAuthorizationFenceCacheForTest(t)
	userID := int64(72004)
	old := service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 8, Revision: 1}
	newer := service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 3, Revision: 2}
	race := &authorizationFenceRaceCache{
		concurrencyCache: base,
		server:           server,
		userID:           userID,
		newer:            newer,
		flushBeforeOld:   true,
	}
	svc := service.NewConcurrencyService(race)

	err := svc.ConfigureUserConcurrencyAuthorizationFence(
		func(context.Context) (map[int64]service.UserConcurrencyAuthorizationFenceProjection, error) {
			if race.fired {
				return map[int64]service.UserConcurrencyAuthorizationFenceProjection{userID: newer}, nil
			}
			return map[int64]service.UserConcurrencyAuthorizationFenceProjection{userID: old}, nil
		},
		func(context.Context, int64) (service.UserConcurrencyAuthorizationFenceProjection, bool, error) {
			return newer, true, nil
		},
	)
	require.NoError(t, err)
	require.True(t, race.fired)

	ceiling, err := base.rdb.HGet(ctx, userAuthorizationCeilingKey(userID), "ceiling").Int()
	require.NoError(t, err)
	require.Equal(t, 3, ceiling)
	revision, err := base.rdb.HGet(ctx, userAuthorizationCeilingKey(userID), "revision").Int64()
	require.NoError(t, err)
	require.Equal(t, int64(2), revision)
	ready, err := base.UserConcurrencyAuthorizationFenceReady(ctx)
	require.NoError(t, err)
	require.True(t, ready, "a lost reconcile token must force a fresh projection before ready")
}

func TestUserConcurrencyAuthorizationCeilingAppliesToLiveLeaseWhenStoredCapIsUnlimited(t *testing.T) {
	ctx := context.Background()
	cache, _ := newAuthorizationFenceCacheForTest(t)
	userID := int64(72005)
	_, err := cache.SetUserConcurrencyAuthorizationCeiling(ctx, userID,
		service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 1, Revision: 1})
	require.NoError(t, err)
	markAuthorizationFenceReady(t, ctx, cache)

	// The auth snapshot can still say zero (unlimited). The strict Lua path
	// must apply the held-refund ceiling to Live turns before that early-out.
	acquired, err := cache.AcquireLiveLease(ctx, 1, 0, userID, 0, 1, "live-one", false)
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = cache.AcquireLiveLease(ctx, 1, 0, userID, 0, 2, "live-two", false)
	require.NoError(t, err)
	require.False(t, acquired)
}

func TestUserConcurrencyAuthorizationMutationMarkerRefreshesStaleAuthForRegularAndLiveAdmission(t *testing.T) {
	ctx := context.Background()
	cache, _ := newAuthorizationFenceCacheForTest(t)
	userID := int64(72006)
	initial := service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 8, Revision: 10}
	committedLower := service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 3, Revision: 11}
	current := initial

	svc := service.NewConcurrencyService(cache)
	require.NoError(t, svc.ConfigureUserConcurrencyAuthorizationFence(
		func(context.Context) (map[int64]service.UserConcurrencyAuthorizationFenceProjection, error) {
			return map[int64]service.UserConcurrencyAuthorizationFenceProjection{userID: current}, nil
		},
		func(context.Context, int64) (service.UserConcurrencyAuthorizationFenceProjection, bool, error) {
			return current, true, nil
		},
	))

	// An already-read API-key auth subject still carries 8. While the durable
	// mutation owns its marker, raw Redis admission must fail closed instead of
	// accepting that stale max.
	require.NoError(t, cache.BeginUserConcurrencyAuthorizationFenceMutation(ctx, userID, "commit-lower", 0))
	_, err := cache.AcquireUserSlot(ctx, userID, 8, "raw-stale-auth")
	require.ErrorIs(t, err, service.ErrUserConcurrencyAuthorizationFenceMutationInFlight)

	// This assignment models the committed PostgreSQL row/revision visible once
	// the writer's user lock is released. The normal HTTP/WS seam synchronizes
	// it before retrying its stale auth subject.
	current = committedLower
	regular := make([]*service.AcquireResult, 0, 3)
	for i := 0; i < 3; i++ {
		result, err := svc.AcquireUserSlot(ctx, userID, 8)
		require.NoError(t, err)
		require.True(t, result.Acquired)
		regular = append(regular, result)
	}
	blocked, err := svc.AcquireUserSlot(ctx, userID, 8)
	require.NoError(t, err)
	require.False(t, blocked.Acquired, "old auth max=8 must see committed ceiling=3")
	for _, result := range regular {
		result.ReleaseFunc()
	}

	// Live does not share the normal request implementation, but it must use
	// the same marker/revision resolver before its own Lua admission.
	committedLiveLower := service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 1, Revision: 12}
	require.NoError(t, cache.BeginUserConcurrencyAuthorizationFenceMutation(ctx, userID, "commit-live-lower", 0))
	current = committedLiveLower
	acquired, err := svc.AcquireLiveLeaseWithAuthorizationFence(ctx, 1, 0, userID, 8, 101, "live-one", false)
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = svc.AcquireLiveLeaseWithAuthorizationFence(ctx, 1, 0, userID, 8, 102, "live-two", false)
	require.NoError(t, err)
	require.False(t, acquired, "Live must also constrain a stale unlimited/high auth subject")
	require.NoError(t, cache.ReleaseLiveLease(ctx, 1, userID, 101, "live-one"))

	ceiling, err := cache.rdb.HGet(ctx, userAuthorizationCeilingKey(userID), "ceiling").Int()
	require.NoError(t, err)
	require.Equal(t, 1, ceiling)
	exists, err := cache.rdb.Exists(ctx, userAuthorizationFenceMutationKey(userID)).Result()
	require.NoError(t, err)
	require.Zero(t, exists, "the committed resolver must remove only the completed marker")
}

func TestUserConcurrencyAuthorizationMutationRollbackRestoresProjectionWithoutFalseLowerCeiling(t *testing.T) {
	ctx := context.Background()
	cache, _ := newAuthorizationFenceCacheForTest(t)
	userID := int64(72007)
	current := service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 8, Revision: 20}

	svc := service.NewConcurrencyService(cache)
	require.NoError(t, svc.ConfigureUserConcurrencyAuthorizationFence(
		func(context.Context) (map[int64]service.UserConcurrencyAuthorizationFenceProjection, error) {
			return map[int64]service.UserConcurrencyAuthorizationFenceProjection{userID: current}, nil
		},
		func(context.Context, int64) (service.UserConcurrencyAuthorizationFenceProjection, bool, error) {
			return current, true, nil
		},
	))

	mutation, err := svc.BeginUserConcurrencyAuthorizationFenceMutation(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, mutation)

	// The database update never commits, so the durable resolver still returns
	// revision 20/ceiling 8. A rollback completion must remove its own marker
	// only after publishing that durable value; it may not leave a provisional
	// lower cap in Redis.
	require.NoError(t, mutation.RolledBack(ctx))

	results := make([]*service.AcquireResult, 0, 8)
	for i := 0; i < 8; i++ {
		result, err := svc.AcquireUserSlot(ctx, userID, 8)
		require.NoError(t, err)
		require.True(t, result.Acquired)
		results = append(results, result)
	}
	blocked, err := svc.AcquireUserSlot(ctx, userID, 8)
	require.NoError(t, err)
	require.False(t, blocked.Acquired)
	for _, result := range results {
		result.ReleaseFunc()
	}

	ceiling, err := cache.rdb.HGet(ctx, userAuthorizationCeilingKey(userID), "ceiling").Int()
	require.NoError(t, err)
	require.Equal(t, 8, ceiling)
	exists, err := cache.rdb.Exists(ctx, userAuthorizationFenceMutationKey(userID)).Result()
	require.NoError(t, err)
	require.Zero(t, exists)
}

func TestUserConcurrencyAuthorizationFenceOldCompletionCannotClearNewerMarker(t *testing.T) {
	ctx := context.Background()
	cache, _ := newAuthorizationFenceCacheForTest(t)
	userID := int64(72008)
	projection := service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 8, Revision: 10}
	require.NoError(t, func() error {
		_, err := cache.SetUserConcurrencyAuthorizationCeiling(ctx, userID, projection)
		return err
	}())
	markAuthorizationFenceReady(t, ctx, cache)

	lookupEntered := make(chan struct{})
	releaseLookup := make(chan struct{})
	var entered atomic.Bool
	svc := service.NewConcurrencyService(cache)
	require.NoError(t, svc.ConfigureUserConcurrencyAuthorizationFence(
		func(context.Context) (map[int64]service.UserConcurrencyAuthorizationFenceProjection, error) {
			return map[int64]service.UserConcurrencyAuthorizationFenceProjection{userID: projection}, nil
		},
		func(lookupCtx context.Context, lookupUserID int64) (service.UserConcurrencyAuthorizationFenceProjection, bool, error) {
			if lookupUserID != userID {
				return service.UserConcurrencyAuthorizationFenceProjection{}, false, nil
			}
			if entered.CompareAndSwap(false, true) {
				close(lookupEntered)
			}
			select {
			case <-releaseLookup:
				return projection, true, nil
			case <-lookupCtx.Done():
				return service.UserConcurrencyAuthorizationFenceProjection{}, false, lookupCtx.Err()
			}
		},
	))

	// Writer A's completion callback captures A, then blocks on its durable
	// lookup. Writer B replaces the marker while A is between lookup and finish.
	require.NoError(t, cache.BeginUserConcurrencyAuthorizationFenceMutation(ctx, userID, "writer-a", 0))
	completionErr := make(chan error, 1)
	go func() {
		completionErr <- svc.SynchronizeUserConcurrencyAuthorizationFence(ctx, userID)
	}()
	select {
	case <-lookupEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("old completion did not reach durable lookup")
	}
	require.NoError(t, cache.BeginUserConcurrencyAuthorizationFenceMutation(ctx, userID, "writer-b", 0))
	close(releaseLookup)
	select {
	case err := <-completionErr:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("old completion did not finish")
	}

	marker, ok, err := cache.CaptureUserConcurrencyAuthorizationFenceMutation(ctx, userID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "writer-b", marker.Token)
	require.Equal(t, int64(11), marker.ExpectedRevision)

	// Both admission seams remain blocked by B until B's own completion.
	_, err = cache.AcquireUserSlot(ctx, userID, 8, "old-auth")
	require.ErrorIs(t, err, service.ErrUserConcurrencyAuthorizationFenceMutationInFlight)
	_, err = cache.AcquireLiveLease(ctx, 1, 8, userID, 8, 90008, "old-live-auth", false)
	require.ErrorIs(t, err, service.ErrUserConcurrencyAuthorizationFenceMutationInFlight)

	finished, err := cache.FinishUserConcurrencyAuthorizationFenceMutation(ctx, userID, "writer-b")
	require.NoError(t, err)
	require.True(t, finished)
	_, ok, err = cache.CaptureUserConcurrencyAuthorizationFenceMutation(ctx, userID)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestUserConcurrencyAuthorizationFenceReconcileCannotClearMarkerInstalledAfterCapture(t *testing.T) {
	ctx := context.Background()
	cache, _ := newAuthorizationFenceCacheForTest(t)
	userID := int64(72009)
	projection := service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 8, Revision: 10}
	_, err := cache.SetUserConcurrencyAuthorizationCeiling(ctx, userID, projection)
	require.NoError(t, err)
	markAuthorizationFenceReady(t, ctx, cache)

	var pauseNext atomic.Bool
	snapshotEntered := make(chan struct{})
	releaseSnapshot := make(chan struct{})
	var snapshotPaused atomic.Bool
	snapshot := func(snapshotCtx context.Context) (map[int64]service.UserConcurrencyAuthorizationFenceProjection, error) {
		if pauseNext.CompareAndSwap(true, false) && snapshotPaused.CompareAndSwap(false, true) {
			close(snapshotEntered)
			select {
			case <-releaseSnapshot:
			case <-snapshotCtx.Done():
				return nil, snapshotCtx.Err()
			}
		}
		return map[int64]service.UserConcurrencyAuthorizationFenceProjection{userID: projection}, nil
	}
	svc := service.NewConcurrencyService(cache)
	require.NoError(t, svc.ConfigureUserConcurrencyAuthorizationFence(snapshot,
		func(context.Context, int64) (service.UserConcurrencyAuthorizationFenceProjection, bool, error) {
			return projection, true, nil
		}))

	// Force the public admission path to enter full reconciliation. Reconcile
	// captures A before the snapshot; B is installed while that snapshot is
	// paused, so A's CAS must leave B in place.
	require.NoError(t, cache.rdb.Del(ctx, userAuthorizationFenceReadyKey).Err())
	require.NoError(t, cache.BeginUserConcurrencyAuthorizationFenceMutation(ctx, userID, "reconcile-a", 0))
	pauseNext.Store(true)
	acquireErr := make(chan error, 1)
	go func() {
		_, err := svc.AcquireUserSlot(ctx, userID, 8)
		acquireErr <- err
	}()
	select {
	case <-snapshotEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile did not reach paused snapshot")
	}
	require.NoError(t, cache.BeginUserConcurrencyAuthorizationFenceMutation(ctx, userID, "reconcile-b", 0))
	close(releaseSnapshot)

	select {
	case err := <-acquireErr:
		require.ErrorIs(t, err, service.ErrUserConcurrencyAuthorizationFenceMutationInFlight)
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile admission did not finish")
	}
	marker, ok, err := cache.CaptureUserConcurrencyAuthorizationFenceMutation(ctx, userID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "reconcile-b", marker.Token)
	require.Equal(t, int64(11), marker.ExpectedRevision)

	finished, err := cache.FinishUserConcurrencyAuthorizationFenceMutation(ctx, userID, "reconcile-b")
	require.NoError(t, err)
	require.True(t, finished)
}

func TestUserConcurrencyAuthorizationFenceMutationVersionCAS(t *testing.T) {
	ctx := context.Background()
	cache, _ := newAuthorizationFenceCacheForTest(t)
	userID := int64(72010)
	_, err := cache.SetUserConcurrencyAuthorizationCeiling(ctx, userID,
		service.UserConcurrencyAuthorizationFenceProjection{Ceiling: 8, Revision: 10})
	require.NoError(t, err)
	require.NoError(t, cache.BeginUserConcurrencyAuthorizationFenceMutation(ctx, userID, "cas-owner", 0))
	marker, ok, err := cache.CaptureUserConcurrencyAuthorizationFenceMutation(ctx, userID)
	require.NoError(t, err)
	require.True(t, ok)

	cleared, err := cache.ClearUserConcurrencyAuthorizationFenceMutationIfUnchanged(ctx, userID, marker, 10)
	require.NoError(t, err)
	require.False(t, cleared, "same revision means the writer may still be in flight")
	cleared, err = cache.ClearUserConcurrencyAuthorizationFenceMutationIfUnchanged(ctx, userID, marker, 11)
	require.NoError(t, err)
	require.True(t, cleared)
}
