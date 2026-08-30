package service

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/google/uuid"
)

// LeaderLockCache provides cross-instance mutual exclusion for periodic background
// jobs. It is implemented in the repository layer (Redis-backed) so the service
// layer never depends on Redis directly. Release is a compare-and-delete keyed by
// owner so a stale holder can never delete a peer's lock.
type LeaderLockCache interface {
	// TryAcquireLeaderLock sets key=owner with the given TTL iff key is absent.
	// It returns true when the caller becomes the owner.
	TryAcquireLeaderLock(ctx context.Context, key, owner string, ttl time.Duration) (bool, error)
	// ReleaseLeaderLock deletes key iff it is still owned by owner.
	ReleaseLeaderLock(ctx context.Context, key, owner string) error
}

type renewableLeaderLockCache interface {
	RenewLeaderLock(ctx context.Context, key, owner string, ttl time.Duration) (bool, error)
}

type singletonJobLock struct {
	cache LeaderLockCache
	db    *sql.DB
	key   string
	owner string
	ttl   time.Duration
}

func newSingletonJobLock(cache LeaderLockCache, db *sql.DB, key string, ttl time.Duration) *singletonJobLock {
	return &singletonJobLock{cache: cache, db: db, key: key, owner: uuid.NewString(), ttl: ttl}
}

func (lock *singletonJobLock) try(ctx context.Context) (context.Context, func(), bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if lock == nil {
		if !runtimegate.SharedWorkAllowed() {
			return ctx, nil, false
		}
		return ctx, func() {}, true
	}
	return lock.tryKey(ctx, lock.key)
}

func (lock *singletonJobLock) trySuffix(ctx context.Context, suffix string) (context.Context, func(), bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if lock == nil {
		if !runtimegate.SharedWorkAllowed() {
			return ctx, nil, false
		}
		return ctx, func() {}, true
	}
	return lock.tryKey(ctx, lock.key+":"+suffix)
}

func (lock *singletonJobLock) acquisitionOwner() string {
	// Each acquisition needs its own fencing token. If a previous lease expires
	// and a later cycle re-acquires the same key, the stale cycle's deferred
	// release must not be able to delete the newer lease.
	return lock.owner + ":" + uuid.NewString()
}

func (lock *singletonJobLock) tryKey(ctx context.Context, key string) (context.Context, func(), bool) {
	if !runtimegate.SharedWorkAllowed() {
		return ctx, nil, false
	}
	return acquireSingletonLease(ctx, lock.cache, lock.db, key, lock.acquisitionOwner(), lock.ttl)
}

func acquireSingletonLease(
	ctx context.Context,
	cache LeaderLockCache,
	db *sql.DB,
	key string,
	owner string,
	ttl time.Duration,
) (context.Context, func(), bool) {
	if cache != nil {
		acquired, err := cache.TryAcquireLeaderLock(ctx, key, owner, ttl)
		// Redis is the authoritative fence whenever it is configured. Falling
		// through from a Redis error to PostgreSQL is split-brain unsafe under a
		// partial partition, so fail closed instead. We also deliberately avoid
		// taking a second session advisory lock here: doing so pins one SQL pool
		// connection per concurrent background job for the full job duration.
		if err != nil || !acquired {
			return ctx, nil, false
		}
		leaseCtx, releaseCache := startRenewableCacheLease(ctx, cache, key, owner, ttl)
		if !runtimegate.SharedWorkAllowed() {
			releaseCache()
			return ctx, nil, false
		}
		return leaseCtx, releaseCache, true
	}

	if db != nil {
		release, acquired, err := tryAcquireDBAdvisoryLockWithError(ctx, db, hashAdvisoryLockID(key))
		if err != nil {
			return ctx, nil, false
		}
		if acquired && !runtimegate.SharedWorkAllowed() {
			release()
			return ctx, nil, false
		}
		return ctx, release, acquired
	}
	if !runtimegate.SharedWorkAllowed() {
		return ctx, nil, false
	}
	return ctx, func() {}, true
}

func startRenewableCacheLease(
	parent context.Context,
	cache LeaderLockCache,
	key string,
	owner string,
	ttl time.Duration,
) (context.Context, func()) {
	leaseCtx, cancel := context.WithCancel(parent)
	stopRenewal := make(chan struct{})
	renewalDone := make(chan struct{})
	if renewer, ok := cache.(renewableLeaderLockCache); ok {
		go renewSingletonJobLease(leaseCtx, cancel, renewer, key, owner, ttl, stopRenewal, renewalDone)
	} else {
		close(renewalDone)
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(stopRenewal)
			<-renewalDone
			cancel()
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer releaseCancel()
			_ = cache.ReleaseLeaderLock(releaseCtx, key, owner)
		})
	}
	return leaseCtx, release
}

func renewSingletonJobLease(
	ctx context.Context,
	cancel context.CancelFunc,
	renewer renewableLeaderLockCache,
	key string,
	owner string,
	ttl time.Duration,
	stop <-chan struct{},
	done chan<- struct{},
) {
	defer close(done)
	interval := ttl / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			renewCtx, renewCancel := context.WithTimeout(context.Background(), 2*time.Second)
			owned, err := renewer.RenewLeaderLock(renewCtx, key, owner, ttl)
			renewCancel()
			if err != nil || !owned {
				cancel()
				return
			}
		}
	}
}

// tryAcquireSingletonLeaderLock provides best-effort single-flight execution of a
// periodic background job across multiple instances. A configured Redis lease
// is authoritative. PostgreSQL advisory locking is used only when Redis is not
// configured; Redis failures never fall through to a different backend.
//
// Semantics:
//   - acquired      -> returns a lease context, non-nil release func, and true;
//     callers should use the lease context and defer release.
//   - held by peer  -> returns the input context, nil, and false; callers skip.
//   - no backend    -> when neither the cache nor a DB is configured (e.g. unit
//     tests, or a single-instance deployment without Redis) it runs without
//     gating, returning a no-op release and true, so the job is never silently
//     starved.
//
// Redis leases renew while the job owns them and cancel the returned context if
// renewal fails or ownership is lost. Every acquisition has a unique fencing
// owner, so a stale release cannot delete a successor lease. The TTL is a short
// crash-recovery bound; leadership is re-contested every cycle rather than
// pinned to one instance.
func tryAcquireSingletonLeaderLock(ctx context.Context, cache LeaderLockCache, db *sql.DB, key, owner string, ttl time.Duration) (context.Context, func(), bool) {
	if !runtimegate.SharedWorkAllowed() {
		return ctx, nil, false
	}
	if ctx == nil {
		ctx = context.Background()
	}

	return acquireSingletonLease(ctx, cache, db, key, owner+":"+uuid.NewString(), ttl)
}
