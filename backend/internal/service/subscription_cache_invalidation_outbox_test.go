package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type subscriptionInvalidationRepoStub struct {
	mu         sync.Mutex
	events     []SubscriptionCacheInvalidationEvent
	claimLimit int
	scheduled  []int64
	deleted    []int64
	retried    []int64
	retryError string
	stats      AuthCacheInvalidationOutboxStats
}

func (r *subscriptionInvalidationRepoStub) Claim(_ context.Context, _ string, limit int, _ time.Duration) ([]SubscriptionCacheInvalidationEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claimLimit = limit
	return append([]SubscriptionCacheInvalidationEvent(nil), r.events...), nil
}

func (r *subscriptionInvalidationRepoStub) DeleteClaimed(_ context.Context, id int64, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = append(r.deleted, id)
	return nil
}

func (r *subscriptionInvalidationRepoStub) ScheduleSecondPass(_ context.Context, id int64, _ string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scheduled = append(r.scheduled, id)
	return nil
}

func (r *subscriptionInvalidationRepoStub) RetryClaimed(_ context.Context, id int64, _ string, _ time.Time, lastError string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retried = append(r.retried, id)
	r.retryError = lastError
	return nil
}

func (r *subscriptionInvalidationRepoStub) Stats(context.Context) (AuthCacheInvalidationOutboxStats, error) {
	return r.stats, nil
}

type subscriptionAuthorizationInvalidatorStub struct {
	mu    sync.Mutex
	calls [][2]int64
	err   error
}

func (s *subscriptionAuthorizationInvalidatorStub) EnsureSubscriptionAuthorizationCachesInvalidated(_ context.Context, userID, groupID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, [2]int64{userID, groupID})
	return s.err
}

func TestSubscriptionCacheInvalidationWorker_UsesTwoPassDelivery(t *testing.T) {
	repo := &subscriptionInvalidationRepoStub{}
	invalidator := &subscriptionAuthorizationInvalidatorStub{}
	worker := NewSubscriptionCacheInvalidationWorker(repo, invalidator)

	worker.processEvent(context.Background(), SubscriptionCacheInvalidationEvent{
		ID: 1, UserID: 7, GroupID: 9, Stage: 0,
	})
	require.Equal(t, [][2]int64{{7, 9}}, invalidator.calls)
	require.Equal(t, []int64{1}, repo.scheduled)
	require.Empty(t, repo.deleted)

	worker.processEvent(context.Background(), SubscriptionCacheInvalidationEvent{
		ID: 1, UserID: 7, GroupID: 9, Stage: 1,
	})
	require.Equal(t, [][2]int64{{7, 9}, {7, 9}}, invalidator.calls)
	require.Equal(t, []int64{1}, repo.deleted)
	require.Equal(t, uint64(2), worker.Health(context.Background()).Processed)
}

func TestSubscriptionCacheInvalidationWorker_RetriesFailureAndInvalidTarget(t *testing.T) {
	for _, tc := range []struct {
		name        string
		event       SubscriptionCacheInvalidationEvent
		invalidator *subscriptionAuthorizationInvalidatorStub
		errorText   string
	}{
		{
			name:        "cache failure",
			event:       SubscriptionCacheInvalidationEvent{ID: 2, UserID: 7, GroupID: 9},
			invalidator: &subscriptionAuthorizationInvalidatorStub{err: errors.New("redis unavailable")},
			errorText:   "redis unavailable",
		},
		{
			name:        "invalid durable payload",
			event:       SubscriptionCacheInvalidationEvent{ID: 3},
			invalidator: &subscriptionAuthorizationInvalidatorStub{},
			errorText:   "invalid subscription cache target",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &subscriptionInvalidationRepoStub{}
			worker := NewSubscriptionCacheInvalidationWorker(repo, tc.invalidator)
			worker.processEvent(context.Background(), tc.event)
			require.Equal(t, []int64{tc.event.ID}, repo.retried)
			require.Contains(t, repo.retryError, tc.errorText)
			require.Empty(t, repo.scheduled)
			require.Equal(t, uint64(1), worker.Health(context.Background()).Failures)
		})
	}
}

func TestSubscriptionCacheInvalidationWorker_BoundedBatchAndHealth(t *testing.T) {
	oldest := time.Now().Add(-time.Minute)
	repo := &subscriptionInvalidationRepoStub{stats: AuthCacheInvalidationOutboxStats{
		Pending: 3, OldestCreatedAt: &oldest, MaxAttempts: 2, LastError: "redis down",
	}}
	worker := NewSubscriptionCacheInvalidationWorker(repo, &subscriptionAuthorizationInvalidatorStub{})
	require.NoError(t, worker.processBatch(context.Background()))
	require.Equal(t, authInvalidationBatchSize, repo.claimLimit)
	health := worker.Health(context.Background())
	require.Equal(t, int64(3), health.Pending)
	require.Equal(t, 2, health.MaxAttempts)
	require.Equal(t, "redis down", health.LastError)
	require.GreaterOrEqual(t, health.OldestLag, time.Minute)
}
