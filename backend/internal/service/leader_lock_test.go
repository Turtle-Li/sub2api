package service

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/stretchr/testify/require"
)

// fakeLeaderLockCache is an in-memory LeaderLockCache for unit tests. It models the
// compare-and-delete release semantics of the real Redis-backed implementation.
type fakeLeaderLockCache struct {
	mu         sync.Mutex
	owners     map[string]string
	acquireErr error
	onAcquire  func()
}

type losingRenewLeaderLockCache struct {
	*fakeLeaderLockCache
	renewed chan struct{}
	once    sync.Once
}

func (f *losingRenewLeaderLockCache) RenewLeaderLock(_ context.Context, _, _ string, _ time.Duration) (bool, error) {
	f.once.Do(func() { close(f.renewed) })
	return false, nil
}

func (f *fakeLeaderLockCache) TryAcquireLeaderLock(_ context.Context, key, owner string, _ time.Duration) (bool, error) {
	if f.acquireErr != nil {
		return false, f.acquireErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owners == nil {
		f.owners = map[string]string{}
	}
	if _, held := f.owners[key]; held {
		return false, nil
	}
	f.owners[key] = owner
	if f.onAcquire != nil {
		f.onAcquire()
	}
	return true, nil
}

func (f *fakeLeaderLockCache) ReleaseLeaderLock(_ context.Context, key, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owners[key] == owner {
		delete(f.owners, key)
	}
	return nil
}

func (f *fakeLeaderLockCache) heldBy(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.owners[key]
}

func TestTryAcquireSingletonLeaderLock_NoBackendRunsUngated(t *testing.T) {
	_, release, ok := tryAcquireSingletonLeaderLock(context.Background(), nil, nil, "k", "inst", time.Minute)
	require.True(t, ok)
	require.NotNil(t, release)
	require.NotPanics(t, release)
}

func TestTryAcquireSingletonLeaderLock_ContendedThenReleased(t *testing.T) {
	cache := &fakeLeaderLockCache{}
	ctx := context.Background()
	const key = "leader:test:contended"

	_, releaseA, ok := tryAcquireSingletonLeaderLock(ctx, cache, nil, key, "A", time.Minute)
	require.True(t, ok, "first instance should acquire")
	require.Contains(t, cache.heldBy(key), "A:")

	_, _, okB := tryAcquireSingletonLeaderLock(ctx, cache, nil, key, "B", time.Minute)
	require.False(t, okB, "peer must be locked out while the lock is held")

	releaseA()
	require.Empty(t, cache.heldBy(key), "release must free the lock")

	_, releaseB, okB := tryAcquireSingletonLeaderLock(ctx, cache, nil, key, "B", time.Minute)
	require.True(t, okB, "peer should acquire after the holder releases")
	releaseB()
}

// A per-node fallback to a different lock backend is split-brain unsafe: a peer
// may still own Redis while this node owns only PostgreSQL. Cache errors must
// fail closed instead of running under an independent fence.
func TestTryAcquireSingletonLeaderLock_CacheErrorFailsClosed(t *testing.T) {
	cache := &fakeLeaderLockCache{acquireErr: context.DeadlineExceeded}
	_, release, ok := tryAcquireSingletonLeaderLock(context.Background(), cache, nil, "k", "inst", time.Minute)
	require.False(t, ok)
	require.Nil(t, release)
}

func TestTryAcquireSingletonLeaderLock_DrainDuringAcquisitionReleasesFence(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	cache := &fakeLeaderLockCache{onAcquire: func() { runtimegate.SetProcessActive(false) }}

	_, release, ok := tryAcquireSingletonLeaderLock(context.Background(), cache, nil, "drain:race", "inst", time.Minute)
	require.False(t, ok)
	require.Nil(t, release)
	require.Empty(t, cache.heldBy("drain:race"))
}

func TestTryAcquireSingletonLeaderLock_RedisDoesNotPinPostgresConnection(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cache := &fakeLeaderLockCache{}

	_, release, ok := tryAcquireSingletonLeaderLock(context.Background(), cache, db, "redis:fence", "inst", time.Minute)
	require.True(t, ok)
	require.NotEmpty(t, cache.heldBy("redis:fence"))
	release()
	require.Empty(t, cache.heldBy("redis:fence"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTryAcquireSingletonLeaderLock_PostgresFallbackWhenRedisUnconfigured(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	mock.ExpectExec("SELECT pg_advisory_unlock").WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	_, release, ok := tryAcquireSingletonLeaderLock(context.Background(), nil, db, "postgres:fallback", "inst", time.Minute)
	require.True(t, ok)
	require.NotNil(t, release)
	release()
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSingletonJobLockRecontendsWithUniqueFencingOwnerAndRuntimeGate(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	statePath := filepath.Join(t.TempDir(), "background-state")
	t.Setenv(runtimegate.StateFileEnv, statePath)
	require.NoError(t, os.WriteFile(statePath, []byte("standby\n"), 0o600))

	cache := &fakeLeaderLockCache{}
	lock := newSingletonJobLock(cache, nil, "scheduled:test", time.Minute)
	_, _, ok := lock.try(context.Background())
	require.False(t, ok, "standby generation must not acquire new shared work")
	require.Empty(t, cache.heldBy("scheduled:test"))

	require.NoError(t, os.WriteFile(statePath, []byte("active\n"), 0o600))
	_, releaseFirst, ok := lock.try(context.Background())
	require.True(t, ok)
	firstOwner := cache.heldBy("scheduled:test")
	require.NotEmpty(t, firstOwner)
	releaseFirst()

	_, releaseSecond, ok := lock.try(context.Background())
	require.True(t, ok)
	secondOwner := cache.heldBy("scheduled:test")
	require.NotEmpty(t, secondOwner)
	require.NotEqual(t, firstOwner, secondOwner, "every acquisition needs a new fencing token")
	releaseSecond()
}

func TestSingletonJobLock_LostRenewalCancelsLeaseContext(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	t.Setenv(runtimegate.StateFileEnv, "")

	cache := &losingRenewLeaderLockCache{
		fakeLeaderLockCache: &fakeLeaderLockCache{},
		renewed:             make(chan struct{}),
	}
	lock := newSingletonJobLock(cache, nil, "renew:test", 10*time.Millisecond)
	leaseCtx, release, acquired := lock.try(context.Background())
	require.True(t, acquired)
	defer release()

	select {
	case <-cache.renewed:
	case <-time.After(2 * time.Second):
		t.Fatal("lease renewal was not attempted")
	}
	select {
	case <-leaseCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("lost lease did not cancel the job context")
	}
}

func TestSubscriptionExpiryService_ReminderSkipsScanWhenNotLeader(t *testing.T) {
	cache := &fakeLeaderLockCache{}
	// A peer already holds the reminder leader lock.
	_, _ = cache.TryAcquireLeaderLock(context.Background(), subscriptionExpiryReminderLeaderLockKey, "peer", time.Minute)

	repo := &subscriptionExpiryRepoStub{}
	settingRepo := &subscriptionExpirySettingRepoStub{values: map[string]string{SettingKeySMTPHost: "smtp.example.com"}}
	svc := NewSubscriptionExpiryService(repo, time.Minute)
	svc.SetSettingRepository(settingRepo)
	svc.SetNotificationEmailService(NewNotificationEmailService(settingRepo, NewEmailService(settingRepo, nil)))
	svc.SetLeaderLock(cache, nil)

	svc.sendExpiryReminders(context.Background())

	require.Zero(t, repo.listCalls, "non-leader must not scan active subscriptions")
}

func TestSubscriptionExpiryService_ReminderScansWhenLeader(t *testing.T) {
	repo := &subscriptionExpiryRepoStub{}
	settingRepo := &subscriptionExpirySettingRepoStub{values: map[string]string{SettingKeySMTPHost: "smtp.example.com"}}
	svc := NewSubscriptionExpiryService(repo, time.Minute)
	svc.SetSettingRepository(settingRepo)
	svc.SetNotificationEmailService(NewNotificationEmailService(settingRepo, NewEmailService(settingRepo, nil)))
	svc.SetLeaderLock(&fakeLeaderLockCache{}, nil)

	svc.sendExpiryReminders(context.Background())

	require.Equal(t, 1, repo.listCalls, "leader should scan active subscriptions once")
}

// Single-instance correctness: the lock is released at the end of each cycle, so
// the same instance must re-acquire it and run on every subsequent cycle (no
// self-lockout). Covers both the cache-backed path and the no-backend path.
func TestSubscriptionExpiryService_ReminderRunsEveryCycleSingleInstance(t *testing.T) {
	cases := map[string]LeaderLockCache{
		"with_cache": &fakeLeaderLockCache{},
		"no_backend": nil,
	}
	for name, cache := range cases {
		t.Run(name, func(t *testing.T) {
			repo := &subscriptionExpiryRepoStub{}
			settingRepo := &subscriptionExpirySettingRepoStub{values: map[string]string{SettingKeySMTPHost: "smtp.example.com"}}
			svc := NewSubscriptionExpiryService(repo, time.Minute)
			svc.SetSettingRepository(settingRepo)
			svc.SetNotificationEmailService(NewNotificationEmailService(settingRepo, NewEmailService(settingRepo, nil)))
			svc.SetLeaderLock(cache, nil)

			// Three consecutive cycles, mimicking the ticker loop.
			svc.sendExpiryReminders(context.Background())
			svc.sendExpiryReminders(context.Background())
			svc.sendExpiryReminders(context.Background())

			require.Equal(t, 3, repo.listCalls, "single instance must run every cycle")
		})
	}
}
