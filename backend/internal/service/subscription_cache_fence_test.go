//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

var errSubscriptionCacheMiss = errors.New("subscription cache miss")

// fencedSubscriptionCacheStub is an in-memory model of the Redis generation
// contract. It embeds the broad BillingCache stub so these tests focus only on
// subscription authorization behavior.
type fencedSubscriptionCacheStub struct {
	billingCacheMissStub

	mu              sync.Mutex
	fence           int64
	data            *SubscriptionCacheData
	invalidateErr   error
	publishErr      error
	fenceErr        error
	dropPublishes   bool
	invalidateCalls int
	handlers        []func(string)
}

func (s *fencedSubscriptionCacheStub) GetSubscriptionCache(_ context.Context, _, _ int64) (*SubscriptionCacheData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		return nil, errSubscriptionCacheMiss
	}
	copy := *s.data
	return &copy, nil
}

func (s *fencedSubscriptionCacheStub) SetSubscriptionCache(ctx context.Context, userID, groupID int64, data *SubscriptionCacheData) error {
	fence, err := s.CaptureSubscriptionCacheFence(ctx, userID, groupID)
	if err != nil {
		return err
	}
	_, err = s.SetSubscriptionCacheIfFence(ctx, userID, groupID, data, fence)
	return err
}

func (s *fencedSubscriptionCacheStub) InvalidateSubscriptionCache(_ context.Context, _, _ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateCalls++
	if s.invalidateErr != nil {
		return s.invalidateErr
	}
	s.fence++
	s.data = nil
	return nil
}

func (s *fencedSubscriptionCacheStub) CaptureSubscriptionCacheFence(_ context.Context, _, _ int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fenceErr != nil {
		return 0, s.fenceErr
	}
	return s.fence, nil
}

func (s *fencedSubscriptionCacheStub) SubscriptionCacheFenceCurrent(_ context.Context, _, _, fence int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fenceErr != nil {
		return false, s.fenceErr
	}
	return s.fence == fence, nil
}

func (s *fencedSubscriptionCacheStub) SetSubscriptionCacheIfFence(_ context.Context, _, _ int64, data *SubscriptionCacheData, fence int64) (bool, error) {
	if data == nil {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fence != fence {
		return false, nil
	}
	copy := *data
	s.data = &copy
	return true, nil
}

func (s *fencedSubscriptionCacheStub) PublishSubscriptionCacheInvalidation(_ context.Context, cacheKey string) error {
	s.mu.Lock()
	if s.publishErr != nil {
		err := s.publishErr
		s.mu.Unlock()
		return err
	}
	if s.dropPublishes {
		s.mu.Unlock()
		return nil
	}
	handlers := append([]func(string){}, s.handlers...)
	s.mu.Unlock()
	for _, handler := range handlers {
		handler(cacheKey)
	}
	return nil
}

func (s *fencedSubscriptionCacheStub) SubscribeSubscriptionCacheInvalidation(_ context.Context, handler func(string)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers = append(s.handlers, handler)
	return nil
}

func (s *fencedSubscriptionCacheStub) cachedSubscription() *SubscriptionCacheData {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		return nil
	}
	copy := *s.data
	return &copy
}

func (s *fencedSubscriptionCacheStub) invalidationCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.invalidateCalls
}

type blockingSubscriptionCacheRepo struct {
	userSubRepoNoop

	mu         sync.Mutex
	current    *UserSubscription
	calls      int
	blockFirst bool
	started    chan struct{}
	release    chan struct{}
}

func newBlockingSubscriptionCacheRepo(sub *UserSubscription) *blockingSubscriptionCacheRepo {
	copy := *sub
	return &blockingSubscriptionCacheRepo{
		current:    &copy,
		blockFirst: true,
		started:    make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (r *blockingSubscriptionCacheRepo) GetActiveByUserIDAndGroupID(_ context.Context, userID, groupID int64) (*UserSubscription, error) {
	r.mu.Lock()
	r.calls++
	copy := *r.current
	block := r.blockFirst
	if block {
		r.blockFirst = false
		close(r.started)
	}
	r.mu.Unlock()
	if block {
		<-r.release
	}
	if copy.UserID != userID || copy.GroupID != groupID {
		return nil, ErrSubscriptionNotFound
	}
	return &copy, nil
}

func (r *blockingSubscriptionCacheRepo) replace(sub *UserSubscription) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *sub
	r.current = &copy
}

func (r *blockingSubscriptionCacheRepo) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func newSubscriptionCacheFenceTestService(t *testing.T, repo UserSubscriptionRepository, cache *fencedSubscriptionCacheStub) (*SubscriptionService, *BillingCacheService) {
	t.Helper()
	billing := NewBillingCacheService(cache, nil, repo, nil, nil, nil, &config.Config{}, nil)
	t.Cleanup(billing.Stop)
	svc := NewSubscriptionService(groupRepoNoop{}, repo, billing, nil, &config.Config{
		// Ristretto adds its internal value cost to each item. Use a realistic
		// capacity here so the focused fence test observes an actual L1 hit
		// instead of a policy rejection caused by a tiny test cache.
		SubscriptionCache: config.SubscriptionCacheConfig{L1Size: 1024, L1TTLSeconds: 60},
	})
	t.Cleanup(svc.Stop)
	return svc, billing
}

func TestSubscriptionL1CacheMissDoesNotReinsertPreRefundSnapshot(t *testing.T) {
	oldUpdatedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	old := &UserSubscription{
		ID: 11, UserID: 7, GroupID: 9, Status: SubscriptionStatusActive,
		StartsAt: oldUpdatedAt.Add(-24 * time.Hour), ExpiresAt: oldUpdatedAt.Add(30 * 24 * time.Hour), UpdatedAt: oldUpdatedAt,
	}
	updated := *old
	updated.ExpiresAt = old.ExpiresAt.Add(-20 * 24 * time.Hour)
	updated.UpdatedAt = oldUpdatedAt.Add(time.Second)

	repo := newBlockingSubscriptionCacheRepo(old)
	cache := &fencedSubscriptionCacheStub{}
	svc, _ := newSubscriptionCacheFenceTestService(t, repo, cache)

	resultCh := make(chan *UserSubscription, 1)
	errCh := make(chan error, 1)
	go func() {
		sub, err := svc.GetActiveSubscription(context.Background(), old.UserID, old.GroupID)
		resultCh <- sub
		errCh <- err
	}()

	select {
	case <-repo.started:
	case <-time.After(time.Second):
		t.Fatal("cache-miss DB read did not start")
	}

	// This models the refund transaction committing the shortened expiry while
	// an authorization cache miss still holds an old DB snapshot.
	repo.replace(&updated)
	require.NoError(t, svc.EnsureSubscriptionAuthorizationCachesInvalidated(context.Background(), old.UserID, old.GroupID))
	close(repo.release)

	var result *UserSubscription
	select {
	case err := <-errCh:
		require.NoError(t, err)
		result = <-resultCh
	case <-time.After(time.Second):
		t.Fatal("cache-miss lookup did not complete")
	}
	require.NotNil(t, result)
	require.Equal(t, updated.ExpiresAt, result.ExpiresAt)

	key := subCacheKey(old.UserID, old.GroupID)
	cached, ok := svc.subCacheL1.Get(key)
	if ok {
		cachedEntry, entryOK := cached.(*subscriptionL1CacheEntry)
		require.True(t, entryOK)
		require.Equal(t, updated.ExpiresAt, cachedEntry.subscription.ExpiresAt)
	}

	hit, err := svc.GetActiveSubscription(context.Background(), old.UserID, old.GroupID)
	require.NoError(t, err)
	require.Equal(t, updated.ExpiresAt, hit.ExpiresAt)
}

func TestSubscriptionL1FenceRejectsRemoteStaleHitWithoutPubSub(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	old := &UserSubscription{
		ID: 51, UserID: 37, GroupID: 39, Status: SubscriptionStatusActive,
		StartsAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(30 * 24 * time.Hour), UpdatedAt: now,
	}
	updated := *old
	updated.ExpiresAt = old.ExpiresAt.Add(-25 * 24 * time.Hour)
	updated.UpdatedAt = now.Add(time.Second)

	repo := newBlockingSubscriptionCacheRepo(old)
	repo.blockFirst = false
	cache := &fencedSubscriptionCacheStub{}
	svc, _ := newSubscriptionCacheFenceTestService(t, repo, cache)
	key := subCacheKey(old.UserID, old.GroupID)
	entry := &subscriptionL1CacheEntry{
		subscription: *old,
		sharedFence:  0,
		sharedFenced: true,
	}
	// Ristretto can reject a cold key during TinyLFU admission. Keep trying in
	// this focused test until the entry is actually resident, rather than
	// mistaking the cache policy for an authorization-path failure.
	require.Eventually(t, func() bool {
		_ = svc.subCacheL1.SetWithTTL(key, entry, 1, time.Minute)
		svc.subCacheL1.Wait()
		_, ok := svc.subCacheL1.Get(key)
		return ok
	}, time.Second, 10*time.Millisecond)

	// Before mutation, the L1 entry is valid and a hit does not query the DB.
	hit, err := svc.GetActiveSubscription(context.Background(), old.UserID, old.GroupID)
	require.NoError(t, err)
	require.Equal(t, old.ExpiresAt, hit.ExpiresAt)
	require.Zero(t, repo.callCount(), "a valid L1 hit must not query the DB")

	// Simulate another instance committing the refund and deleting Redis, but
	// deliberately do not deliver Pub/Sub to this process. The per-hit Redis
	// fence is the correctness barrier for that delivery gap.
	repo.replace(&updated)
	require.NoError(t, cache.InvalidateSubscriptionCache(context.Background(), old.UserID, old.GroupID))

	refreshed, err := svc.GetActiveSubscription(context.Background(), old.UserID, old.GroupID)
	require.NoError(t, err)
	require.Equal(t, updated.ExpiresAt, refreshed.ExpiresAt)
	require.GreaterOrEqual(t, repo.callCount(), 2, "stale fence forces an authoritative reload")
}

func TestSubscriptionL1FenceOutageDoesNotCreateUnfencedAuthorizationEntry(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	old := &UserSubscription{
		ID: 61, UserID: 47, GroupID: 49, Status: SubscriptionStatusActive,
		StartsAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(30 * 24 * time.Hour), UpdatedAt: now,
	}
	updated := *old
	updated.ExpiresAt = old.ExpiresAt.Add(-20 * 24 * time.Hour)
	updated.UpdatedAt = now.Add(time.Second)

	repo := newBlockingSubscriptionCacheRepo(old)
	repo.blockFirst = false
	cache := &fencedSubscriptionCacheStub{fenceErr: errors.New("redis fence unavailable")}
	remoteSvc, _ := newSubscriptionCacheFenceTestService(t, repo, cache)
	refundSvc, _ := newSubscriptionCacheFenceTestService(t, repo, cache)
	key := subCacheKey(old.UserID, old.GroupID)

	// A temporary Redis failure must make this DB-only. Treat this service as a
	// remote instance: the later successful publication will deliberately not
	// reach it, so an accidental unfenced L1 fill would still authorize old.
	result, err := remoteSvc.GetActiveSubscription(context.Background(), old.UserID, old.GroupID)
	require.NoError(t, err)
	require.Equal(t, old.ExpiresAt, result.ExpiresAt)
	remoteSvc.subCacheL1.Wait()
	_, cached := remoteSvc.subCacheL1.Get(key)
	require.False(t, cached, "fence-read outage must not create an unfenced L1 entry")
	readsBeforeRefund := repo.callCount()

	cache.mu.Lock()
	cache.fenceErr = nil
	cache.dropPublishes = true // model a remote L1 that misses an otherwise successful publish
	cache.mu.Unlock()
	repo.replace(&updated)
	require.NoError(t, refundSvc.EnsureSubscriptionAuthorizationCachesInvalidated(context.Background(), old.UserID, old.GroupID))

	refreshed, err := remoteSvc.GetActiveSubscription(context.Background(), old.UserID, old.GroupID)
	require.NoError(t, err)
	require.Equal(t, updated.ExpiresAt, refreshed.ExpiresAt)
	require.GreaterOrEqual(t, repo.callCount(), readsBeforeRefund+2, "remote service must reload after its DB-only outage read")
}

func TestSharedSubscriptionCacheMissDoesNotReinsertPreRefundSnapshot(t *testing.T) {
	oldUpdatedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	old := &UserSubscription{
		ID: 31, UserID: 17, GroupID: 19, Status: SubscriptionStatusActive,
		StartsAt: oldUpdatedAt.Add(-24 * time.Hour), ExpiresAt: oldUpdatedAt.Add(30 * 24 * time.Hour), UpdatedAt: oldUpdatedAt,
	}
	updated := *old
	updated.ExpiresAt = old.ExpiresAt.Add(-12 * 24 * time.Hour)
	updated.UpdatedAt = oldUpdatedAt.Add(time.Second)

	repo := newBlockingSubscriptionCacheRepo(old)
	cache := &fencedSubscriptionCacheStub{}
	_, billing := newSubscriptionCacheFenceTestService(t, repo, cache)

	resultCh := make(chan *subscriptionCacheData, 1)
	errCh := make(chan error, 1)
	go func() {
		data, err := billing.GetSubscriptionStatus(context.Background(), old.UserID, old.GroupID)
		resultCh <- data
		errCh <- err
	}()

	select {
	case <-repo.started:
	case <-time.After(time.Second):
		t.Fatal("shared-cache DB read did not start")
	}

	repo.replace(&updated)
	require.NoError(t, billing.InvalidateSubscription(context.Background(), old.UserID, old.GroupID))
	close(repo.release)

	var result *subscriptionCacheData
	select {
	case err := <-errCh:
		require.NoError(t, err)
		result = <-resultCh
	case <-time.After(time.Second):
		t.Fatal("shared-cache lookup did not complete")
	}
	require.NotNil(t, result)
	require.Equal(t, updated.ExpiresAt, result.ExpiresAt)

	cached := cache.cachedSubscription()
	require.NotNil(t, cached)
	require.Equal(t, updated.ExpiresAt, cached.ExpiresAt)
}

func TestEnsureSubscriptionAuthorizationCachesInvalidatedFailsWhenPublicationFails(t *testing.T) {
	cache := &fencedSubscriptionCacheStub{publishErr: errors.New("redis publish unavailable")}
	repo := newBlockingSubscriptionCacheRepo(&UserSubscription{
		ID: 41, UserID: 27, GroupID: 29, Status: SubscriptionStatusActive,
		StartsAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour), UpdatedAt: time.Now(),
	})
	svc, _ := newSubscriptionCacheFenceTestService(t, repo, cache)

	err := svc.EnsureSubscriptionAuthorizationCachesInvalidated(context.Background(), 27, 29)
	require.Error(t, err)
	require.Contains(t, err.Error(), "redis publish unavailable")
	require.Equal(t, 1, cache.invalidationCount(), "strict boundary must delete shared cache before reporting publication failure")
}

func TestEnsureSubscriptionAuthorizationCachesInvalidatedRequiresFencedSharedCache(t *testing.T) {
	repo := newBlockingSubscriptionCacheRepo(&UserSubscription{
		ID: 71, UserID: 57, GroupID: 59, Status: SubscriptionStatusActive,
		StartsAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour), UpdatedAt: time.Now(),
	})
	cache := &billingCacheMissStub{}
	billing := NewBillingCacheService(cache, nil, repo, nil, nil, nil, &config.Config{}, nil)
	t.Cleanup(billing.Stop)
	svc := NewSubscriptionService(groupRepoNoop{}, repo, billing, nil, &config.Config{
		SubscriptionCache: config.SubscriptionCacheConfig{L1Size: 1024, L1TTLSeconds: 60},
	})
	t.Cleanup(svc.Stop)

	err := svc.EnsureSubscriptionAuthorizationCachesInvalidated(context.Background(), 57, 59)
	require.ErrorIs(t, err, ErrSubscriptionCacheInvalidationUnavailable)
}
