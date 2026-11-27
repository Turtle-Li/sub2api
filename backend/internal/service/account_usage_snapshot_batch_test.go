package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

type stubMultiStartWindowUsageRepo struct {
	UsageLogRepository
	batchCalls  atomic.Int32
	singleCalls atomic.Int32
	lastStarts  map[int64]time.Time
	statsByID   map[int64]*usagestats.AccountStats
}

func (r *stubMultiStartWindowUsageRepo) GetAccountWindowStats(_ context.Context, accountID int64, _ time.Time) (*usagestats.AccountStats, error) {
	r.singleCalls.Add(1)
	if st, ok := r.statsByID[accountID]; ok {
		return st, nil
	}
	return &usagestats.AccountStats{}, nil
}

func (r *stubMultiStartWindowUsageRepo) GetAccountWindowStatsByStartsBatch(_ context.Context, startByAccount map[int64]time.Time) (map[int64]*usagestats.AccountStats, error) {
	r.batchCalls.Add(1)
	r.lastStarts = make(map[int64]time.Time, len(startByAccount))
	out := make(map[int64]*usagestats.AccountStats, len(startByAccount))
	for id, st := range startByAccount {
		r.lastStarts[id] = st
		if stats, ok := r.statsByID[id]; ok {
			out[id] = stats
		} else {
			out[id] = &usagestats.AccountStats{}
		}
	}
	return out, nil
}

func TestAccountUsageServiceGetAccountWindowStatsBatchByStartsCachesAndBatches(t *testing.T) {
	repo := &stubMultiStartWindowUsageRepo{
		statsByID: map[int64]*usagestats.AccountStats{
			11: {Requests: 3, Tokens: 1000, Cost: 1.5, StandardCost: 1.2, UserCost: 1.8},
			22: {Requests: 0, Tokens: 0, Cost: 0, StandardCost: 0, UserCost: 0},
		},
	}
	svc := NewAccountUsageService(nil, repo, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)

	now := time.Now().UTC().Truncate(time.Minute)
	starts := map[int64]time.Time{
		11: now.Add(-2 * time.Hour),
		22: now.Add(-4 * time.Hour),
	}

	first, err := svc.GetAccountWindowStatsBatchByStarts(context.Background(), starts)
	require.NoError(t, err)
	require.Equal(t, int32(1), repo.batchCalls.Load(), "should issue one batched multi-start query")
	require.Equal(t, int32(0), repo.singleCalls.Load(), "should not fall back to per-account queries")
	require.InDelta(t, 1.2, first[11].StandardCost, 1e-9)
	require.Equal(t, int64(0), first[22].Requests)

	// Second call within TTL must be served entirely from cache (0 DB hits).
	second, err := svc.GetAccountWindowStatsBatchByStarts(context.Background(), starts)
	require.NoError(t, err)
	require.Equal(t, int32(1), repo.batchCalls.Load(), "cache hit should avoid hitting database again")
	require.InDelta(t, 1.2, second[11].StandardCost, 1e-9)
	require.Equal(t, int64(0), second[22].Requests)
}

type stubSnapshotAccountRepo struct {
	AccountRepository
	accounts            map[int64]*Account
	blockGetByID        chan struct{}
	blockUpdateExtra    chan struct{}
	getByIDStarted      chan int64
	stripCredsOnGetByID bool
	activeWorkers       atomic.Int32
	maxWorkers          atomic.Int32
	clearErrorCalls     atomic.Int32
	updateExtraErr      error
	updateExtraHit      atomic.Int32
}

func (r *stubSnapshotAccountRepo) UpdateExtra(ctx context.Context, _ int64, _ map[string]any) error {
	r.updateExtraHit.Add(1)
	if r.blockUpdateExtra != nil {
		select {
		case <-r.blockUpdateExtra:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.updateExtraErr
}

func (r *stubSnapshotAccountRepo) ClearError(_ context.Context, id int64) error {
	r.clearErrorCalls.Add(1)
	if acc, ok := r.accounts[id]; ok {
		acc.Status = StatusActive
		acc.ErrorMessage = ""
	}
	return nil
}

func (r *stubSnapshotAccountRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	cur := r.activeWorkers.Add(1)
	for {
		prev := r.maxWorkers.Load()
		if cur <= prev || r.maxWorkers.CompareAndSwap(prev, cur) {
			break
		}
	}
	defer r.activeWorkers.Add(-1)

	if r.getByIDStarted != nil {
		select {
		case r.getByIDStarted <- id:
		default:
		}
	}
	if r.blockGetByID != nil {
		select {
		case <-r.blockGetByID:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	acc, ok := r.accounts[id]
	if !ok {
		return nil, ErrAccountNotFound
	}
	clone := *acc
	if r.stripCredsOnGetByID {
		clone.Credentials = nil
	}
	return &clone, nil
}

func (r *stubSnapshotAccountRepo) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	out := make([]*Account, 0, len(ids))
	for _, id := range ids {
		if acc, ok := r.accounts[id]; ok {
			clone := *acc
			out = append(out, &clone)
		}
	}
	return out, nil
}

func TestAccountUsageServiceGetUsageBatchSnapshotOnlyNeverBlocksOnSlowOpenAIOrAntigravity(t *testing.T) {
	now := time.Now().UTC()
	staleAt := now.Add(-2 * time.Hour).Format(time.RFC3339)
	accountRepo := &stubSnapshotAccountRepo{
		accounts: map[int64]*Account{
			101: {
				ID:       101,
				Name:     "openai-stale",
				Platform: PlatformOpenAI,
				Type:     AccountTypeOAuth,
				Status:   StatusActive,
				Extra: map[string]any{
					"codex_5h_used_percent":  42.0,
					"codex_7d_used_percent":  18.0,
					"codex_usage_updated_at": staleAt,
				},
			},
		},
	}
	usageRepo := &stubMultiStartWindowUsageRepo{
		statsByID: map[int64]*usagestats.AccountStats{
			101: {Requests: 5, Tokens: 5000, Cost: 0.5, StandardCost: 0.5, UserCost: 0.5},
		},
	}
	svc := NewAccountUsageService(accountRepo, usageRepo, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)

	start := time.Now()
	usageMap, errMap, err := svc.GetUsageBatchWithOptions(context.Background(), []int64{101}, UsageBatchOptions{
		Force:        false,
		SnapshotOnly: true,
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Empty(t, errMap)
	require.Less(t, elapsed, 200*time.Millisecond, "snapshot_only batch must return immediately")
	require.NotNil(t, usageMap[101])
	require.NotNil(t, usageMap[101].FiveHour)
	require.InDelta(t, 42.0, usageMap[101].FiveHour.Utilization, 1e-6)
	require.NotNil(t, usageMap[101].FiveHour.WindowStats)
	require.Equal(t, int64(5), usageMap[101].FiveHour.WindowStats.Requests)

	// Repeating snapshot_only within 1 minute should hit windowStatsCache and not hit usage_logs again.
	callsBefore := usageRepo.singleCalls.Load()
	_, _, err = svc.GetUsageBatchWithOptions(context.Background(), []int64{101}, UsageBatchOptions{
		Force:        false,
		SnapshotOnly: true,
	})
	require.NoError(t, err)
	require.Equal(t, callsBefore, usageRepo.singleCalls.Load(), "repeated snapshot reads within cache TTL must not re-query usage_logs")
}

func TestAccountUsageServiceAntigravityStaleWhileRevalidate(t *testing.T) {
	blockCh := make(chan struct{})
	startedCh := make(chan int64, 256)

	accounts := map[int64]*Account{
		202: {
			ID:           202,
			Name:         "ag-stale-error",
			Platform:     PlatformAntigravity,
			Type:         AccountTypeOAuth,
			Status:       StatusError,
			ErrorMessage: "401 unauthenticated",
			Credentials:  map[string]any{"access_token": "tok-202"},
		},
	}
	allIDs := []int64{202}
	for id := int64(203); id <= 207; id++ {
		accounts[id] = &Account{
			ID:          id,
			Name:        "ag-cold",
			Platform:    PlatformAntigravity,
			Type:        AccountTypeOAuth,
			Status:      StatusActive,
			Credentials: map[string]any{"access_token": "tok"},
		}
		allIDs = append(allIDs, id)
	}

	accountRepo := &stubSnapshotAccountRepo{
		accounts:            accounts,
		blockGetByID:        blockCh,
		getByIDStarted:      startedCh,
		stripCredsOnGetByID: true,
	}
	cache := NewUsageCache()
	staleReset := time.Now().Add(2 * time.Hour)
	staleUsage := &UsageInfo{
		Source: "active",
		AntigravityQuota: map[string]*AntigravityModelQuota{
			"claude-opus-4-6": {Utilization: 35, ResetTime: staleReset.Format(time.RFC3339)},
		},
	}
	// Seed expired cache entry (older than apiCacheTTL).
	cache.antigravityCache.Store(int64(202), &antigravityUsageCache{
		usageInfo: staleUsage,
		timestamp: time.Now().Add(-10 * time.Minute),
	})

	svc := NewAccountUsageService(accountRepo, nil, nil, nil, &AntigravityQuotaFetcher{}, nil, nil, nil, cache, nil, nil)

	// End-to-end SnapshotOnly batch for 6 accounts (3 batches of 2 workers) must return
	// immediately (<100ms) and admit all 6 into the bounded queue as "refreshing".
	start := time.Now()
	usageMap, errMap, err := svc.GetUsageBatchWithOptions(context.Background(), allIDs, UsageBatchOptions{
		Force:        false,
		SnapshotOnly: true,
	})
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.Empty(t, errMap)
	require.Less(t, elapsed, 100*time.Millisecond, "SnapshotOnly must never block on slow Antigravity background refresh")

	// Stale entry returns previous quota with Source="snapshot" and SnapshotRefreshState="refreshing" (true SWR).
	require.NotNil(t, usageMap[202])
	require.Equal(t, "snapshot", usageMap[202].Source)
	require.Equal(t, snapshotRefreshStateRefreshing, usageMap[202].SnapshotRefreshState)
	require.NotNil(t, usageMap[202].AntigravityQuota["claude-opus-4-6"])
	require.Equal(t, 35, usageMap[202].AntigravityQuota["claude-opus-4-6"].Utilization)

	// All 5 remaining cold accounts (including the 3rd batch: 206, 207) are queued as "refreshing", not dropped.
	for id := int64(203); id <= 207; id++ {
		require.NotNil(t, usageMap[id])
		require.Equal(t, "snapshot", usageMap[id].Source)
		require.Equal(t, snapshotRefreshStateRefreshing, usageMap[id].SnapshotRefreshState)
		require.Empty(t, usageMap[id].AntigravityQuota)
	}

	// Wait for the first 2 workers to start, then release so all 3 batches drain cleanly.
	for range 2 {
		select {
		case <-startedCh:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("expected first 2 background refresh workers to start")
		}
	}
	require.LessOrEqual(t, accountRepo.maxWorkers.Load(), int32(bgUsageRefreshMaxConc), "concurrency must stay <= 2")

	// Fill bounded queue completely while workers are blocked to confirm overflow returns "deferred".
	var deferredSeen bool
	for extraID := int64(1000); extraID < 1000+bgUsageRefreshQueueSize+5; extraID++ {
		if svc.scheduleBackgroundUsageRefresh(extraID, false) == snapshotRefreshStateDeferred {
			deferredSeen = true
			break
		}
	}
	require.True(t, deferredSeen, "saturated bounded queue must return deferred without blocking")

	// Restricted caller must NOT schedule background refresh even when cache is stale.
	require.Empty(t, svc.scheduleBackgroundUsageRefresh(999, true))
	_, inFlight := cache.bgRefreshInFlight.Load(int64(999))
	require.False(t, inFlight, "restricted principal must never schedule detached background refresh")

	close(blockCh)

	// Wait for all 6 initial accounts (3 batches) to be picked up by the bounded worker pool.
	seen := make(map[int64]bool, 6)
	deadline := time.After(2 * time.Second)
	for len(seen) < 4 {
		select {
		case id := <-startedCh:
			if id >= 202 && id <= 207 {
				seen[id] = true
			}
		case <-deadline:
			t.Fatalf("expected all 3 batches to drain; saw remaining=%v", seen)
		}
	}
	require.LessOrEqual(t, accountRepo.maxWorkers.Load(), int32(bgUsageRefreshMaxConc), "worker concurrency must never exceed 2")
	require.Equal(t, int32(0), accountRepo.clearErrorCalls.Load(), "background snapshot refresh must never mutate account error status")
	require.Equal(t, StatusError, accountRepo.accounts[202].Status)
}

func TestOpenAIExpiredResetWindowBucketsToMinuteAndBackgroundSkipsUsageLogs(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute).Add(20 * time.Second)
	expiredReset := now.Add(-15 * time.Minute)
	futureReset := now.Add(2 * time.Hour)

	startExpired, rollingExpired := codexWindowStatsQueryStart(&UsageProgress{ResetsAt: &expiredReset}, 5*time.Hour, now)
	require.True(t, rollingExpired, "expired ResetsAt must be treated as rolling and bucketed to minute")
	require.Equal(t, now.Add(-5*time.Hour), startExpired)

	startFuture, rollingFuture := codexWindowStatsQueryStart(&UsageProgress{ResetsAt: &futureReset}, 5*time.Hour, now)
	require.False(t, rollingFuture, "active future ResetsAt uses exact fixed window start")
	require.Equal(t, futureReset.Add(-5*time.Hour), startFuture)

	// Verify refreshOpenAIUsageSnapshot incurs 0 usageLogRepo queries.
	usageRepo := &stubMultiStartWindowUsageRepo{
		statsByID: map[int64]*usagestats.AccountStats{
			401: {Requests: 2, Tokens: 100, Cost: 0.1, StandardCost: 0.1, UserCost: 0.1},
		},
	}
	svc := NewAccountUsageService(nil, usageRepo, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)
	_, err := svc.refreshOpenAIUsageSnapshot(context.Background(), &Account{
		ID:       401,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Extra: map[string]any{
			"codex_5h_used_percent":  10.0,
			"codex_7d_used_percent":  5.0,
			"codex_usage_updated_at": now.Format(time.RFC3339),
		},
	}, false, true)
	require.NoError(t, err)
	require.Equal(t, int32(0), usageRepo.singleCalls.Load(), "background OpenAI refresh must not query usage_logs")
	require.Equal(t, int32(0), usageRepo.batchCalls.Load(), "background OpenAI refresh must not batch-query usage_logs")

	// Interleaving test: simulate GetByIDs reading stale Extra BEFORE background finishes,
	// then background commits newer Extra to openAISnapshotExtra and clears bgRefreshInFlight
	// BEFORE getOpenAIUsageSnapshot inspects inFlight.
	cache := NewUsageCache()
	cache.openAIProbeCache.Store(int64(402), now)
	cache.bgRefreshInFlight.Store(int64(402), struct{}{})
	inFlightSvc := NewAccountUsageService(nil, usageRepo, nil, nil, nil, nil, nil, nil, cache, nil, nil)

	staleAccountReadEarlier := &Account{
		ID:       402,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Extra: map[string]any{
			"codex_5h_used_percent":  42.0,
			"codex_7d_used_percent":  18.0,
			"codex_usage_updated_at": now.Add(-2 * time.Hour).Format(time.RFC3339),
		},
	}

	// While inFlight is set, snapshot reports "refreshing" despite probe throttle timestamp.
	snapWhileRunning, err := inFlightSvc.getOpenAIUsageSnapshot(context.Background(), staleAccountReadEarlier)
	require.NoError(t, err)
	require.Equal(t, snapshotRefreshStateRefreshing, snapWhileRunning.SnapshotRefreshState)

	// Background finishes: writes openAISnapshotExtra BEFORE clearing bgRefreshInFlight.
	inFlightSvc.cacheOpenAISnapshotExtra(402, map[string]any{
		"codex_5h_used_percent":  77.0,
		"codex_7d_used_percent":  25.0,
		"codex_usage_updated_at": now.Format(time.RFC3339),
	})
	cache.bgRefreshInFlight.Delete(int64(402))

	// Even though the caller passes staleAccountReadEarlier (from an earlier GetByIDs),
	// getOpenAIUsageSnapshot merges openAISnapshotExtra and returns the fresh 77% quota.
	snapAfterInterleave, err := inFlightSvc.getOpenAIUsageSnapshot(context.Background(), staleAccountReadEarlier)
	require.NoError(t, err)
	require.Empty(t, snapAfterInterleave.SnapshotRefreshState)
	require.NotNil(t, snapAfterInterleave.FiveHour)
	require.InDelta(t, 77.0, snapAfterInterleave.FiveHour.Utilization, 1e-6,
		"interleaved reader with stale DB Extra must receive freshly persisted quota once inFlight clears")

	// Verify cloneAntigravityUsageForRead never mutates the shared cached FiveHour pointer.
	resetAt := time.Now().Add(90 * time.Second)
	shared := &UsageInfo{
		Source:   "active",
		FiveHour: &UsageProgress{ResetsAt: &resetAt, RemainingSeconds: 9999},
	}
	cloned := cloneAntigravityUsageForRead(shared, "snapshot", snapshotRefreshStateRefreshing)
	require.Equal(t, "active", shared.Source)
	require.Equal(t, 9999, shared.FiveHour.RemainingSeconds, "shared cached FiveHour must not be mutated")
	require.Equal(t, "snapshot", cloned.Source)
	require.Equal(t, snapshotRefreshStateRefreshing, cloned.SnapshotRefreshState)
	require.LessOrEqual(t, cloned.FiveHour.RemainingSeconds, 91)
}

func TestOpenAIThrottleRecheckCPRPersistFailureAndGrokCooldown(t *testing.T) {
	now := time.Now().UTC()

	// 1. OpenAI true interleaving via testHookBeforeOpenAIProbeDueCheck:
	// Request A initially observes inFlight=false AND empty openAISnapshotExtra,
	// then BEFORE A checks isOpenAIProbeDue, Request B sets the throttle timestamp.
	staleOpenAI := &Account{
		ID:       501,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_enabled": true,
			"codex_5h_used_percent":                        20.0,
			"codex_7d_used_percent":                        10.0,
			"codex_usage_updated_at":                       now.Add(-2 * time.Hour).Format(time.RFC3339),
		},
	}

	// Case 1a: B intervenes mid-call, writes throttle + enters bgRefreshInFlight.
	// Without the throttle-skip re-check branch, snap1 would have empty SnapshotRefreshState.
	cacheA := NewUsageCache()
	svcA := NewAccountUsageService(nil, nil, nil, nil, nil, nil, nil, nil, cacheA, nil, nil)
	svcA.testHookBeforeOpenAIProbeDueCheck = func() {
		cacheA.openAIProbeCache.Store(int64(501), now)
		cacheA.bgRefreshInFlight.Store(int64(501), struct{}{})
	}
	snap1, err := svcA.getOpenAIUsageSnapshot(context.Background(), staleOpenAI)
	require.NoError(t, err)
	require.Equal(t, snapshotRefreshStateRefreshing, snap1.SnapshotRefreshState,
		"mid-call concurrent refresh start must be caught by throttle-skip re-check")

	// Case 1b: B intervenes mid-call, writes throttle + publishes fresh extra + clears bgRefreshInFlight.
	// Without the throttle-skip re-check branch, snap2 would return stale 20% instead of 88%.
	cacheB := NewUsageCache()
	svcB := NewAccountUsageService(nil, nil, nil, nil, nil, nil, nil, nil, cacheB, nil, nil)
	svcB.testHookBeforeOpenAIProbeDueCheck = func() {
		cacheB.openAIProbeCache.Store(int64(501), now)
		svcB.cacheOpenAISnapshotExtra(501, map[string]any{
			"codex_5h_used_percent":  88.0,
			"codex_7d_used_percent":  30.0,
			"codex_usage_updated_at": now.Format(time.RFC3339),
		})
	}
	snap2, err := svcB.getOpenAIUsageSnapshot(context.Background(), staleOpenAI)
	require.NoError(t, err)
	require.Empty(t, snap2.SnapshotRefreshState)
	require.NotNil(t, snap2.FiveHour)
	require.InDelta(t, 88.0, snap2.FiveHour.Utilization, 1e-6,
		"mid-call concurrent refresh completion must be merged by throttle-skip re-check")

	// 2. CPR UpdateExtra failure must NOT publish unpersisted snapshot to openAISnapshotExtra.
	keys := []map[string]any{
		{"id": "key_1", "prefix": "sk-cpr-tri", "groups": []map[string]any{{"id": "grp_1"}}},
	}
	srv := newCPRTestServer(t, keys, nil, cprTestQuota(now.Format(time.RFC3339), 64, false, "—", "—"))
	useCPRQuotaSettings(t, srv.URL, cprTestAdminKey)
	cprAccount := newCPRTestAccount(9501, srv.URL)
	resetCPRQuotaState(cprAccount.ID)
	t.Cleanup(func() { resetCPRQuotaState(cprAccount.ID) })

	failingRepo := &stubSnapshotAccountRepo{
		accounts:       map[int64]*Account{cprAccount.ID: cprAccount},
		updateExtraErr: errors.New("db write failed"),
	}
	cprCache := NewUsageCache()
	cprSvc := NewAccountUsageService(failingRepo, nil, nil, nil, nil, nil, nil, nil, cprCache, nil, nil)
	_, err = cprSvc.refreshOpenAIUsageSnapshot(context.Background(), cprAccount, true, true)
	require.NoError(t, err)
	require.Equal(t, int32(1), failingRepo.updateExtraHit.Load())
	_, cachedAfterFail := cprCache.openAISnapshotExtra.Load(cprAccount.ID)
	require.False(t, cachedAfterFail, "failed CPR UpdateExtra must never publish unpersisted snapshot to cache")

	// When UpdateExtra succeeds, only the persisted CPR updates are cached (not unrelated account.Extra keys).
	cprAccount.Extra["unrelated_secret_marker"] = "must-not-be-copied"
	okRepo := &stubSnapshotAccountRepo{accounts: map[int64]*Account{cprAccount.ID: cprAccount}}
	cprSvcOK := NewAccountUsageService(okRepo, nil, nil, nil, nil, nil, nil, nil, cprCache, nil, nil)
	_, err = cprSvcOK.refreshOpenAIUsageSnapshot(context.Background(), cprAccount, true, true)
	require.NoError(t, err)
	rawCached, cachedAfterOK := cprCache.openAISnapshotExtra.Load(cprAccount.ID)
	require.True(t, cachedAfterOK, "persisted CPR sync must populate openAISnapshotExtra")
	entry, ok := rawCached.(*openAISnapshotExtraCache)
	require.True(t, ok)
	_, leakedUnrelated := entry.updates["unrelated_secret_marker"]
	require.False(t, leakedUnrelated, "only persisted CPR update keys should be cached")

	// 3. Grok cooldown & interleaving:
	// 3a. In cooldown without new persisted billing -> do NOT enqueue or report refreshing.
	grokCache := NewUsageCache()
	grokCache.grokProbeCache.Store(int64(601), now)
	grokAccount := &Account{
		ID:       601,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Extra:    map[string]any{},
	}
	grokRepo := &stubSnapshotAccountRepo{accounts: map[int64]*Account{601: grokAccount}}
	grokSvc := NewAccountUsageService(
		grokRepo, nil, nil, nil, nil, NewGrokQuotaFetcher(), &GrokQuotaService{}, nil, grokCache, nil, nil,
	)
	grokSnap, err := grokSvc.getGrokUsageSnapshot(context.Background(), grokAccount)
	require.NoError(t, err)
	require.Empty(t, grokSnap.SnapshotRefreshState, "Grok in cooldown must not report refreshing")
	_, grokEnqueued := grokCache.bgRefreshInFlight.Load(int64(601))
	require.False(t, grokEnqueued, "Grok in cooldown must not occupy background queue")

	// 3b. Interleaving gate: Request A read stale Account.Extra (empty), then mid-call
	// background B sets cooldown + persists fresh billing to grokSnapshotExtra + clears inFlight.
	// Request A must return the fresh billing instead of caching stale empty usage.
	grokCacheInterleave := NewUsageCache()
	grokSvcInterleave := NewAccountUsageService(
		grokRepo, nil, nil, nil, nil, NewGrokQuotaFetcher(), &GrokQuotaService{}, nil, grokCacheInterleave, nil, nil,
	)
	grokSvcInterleave.testHookBeforeGrokProbeDueCheck = func() {
		grokCacheInterleave.grokProbeCache.Store(int64(601), now)
		grokSvcInterleave.cacheGrokSnapshotBilling(601, &xai.BillingSummary{
			Plan:             "SuperGrok",
			UpdatedAt:        now.Format(time.RFC3339),
			WeeklyStatusCode: 200,
		})
	}
	grokInterleaved, err := grokSvcInterleave.getGrokUsageSnapshot(context.Background(), grokAccount)
	require.NoError(t, err)
	require.Empty(t, grokInterleaved.SnapshotRefreshState)
	require.NotNil(t, grokInterleaved.GrokBilling, "interleaved Grok reader must merge freshly persisted billing")
	require.Equal(t, "SuperGrok", grokInterleaved.GrokBilling.Plan)
}

type grok403UpstreamStub struct {
	started chan struct{}
	release chan struct{}
}

func (u *grok403UpstreamStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	if u.started != nil {
		select {
		case u.started <- struct{}{}:
		default:
		}
	}
	if u.release != nil {
		<-u.release
	}
	return &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"error":"forbidden"}`)),
		Request:    req,
	}, nil
}

func (u *grok403UpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func TestAntigravityEndToEndDeadlineAndGrok403PersistedSnapshotPublish(t *testing.T) {
	// 1. Antigravity getAntigravityUsage must honor remaining ctx deadline (including prior GetByID time),
	// verified offline against a local blocking test server (zero live Google requests).
	var agHits atomic.Int32
	unblockAG := make(chan struct{})
	slowAGSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agHits.Add(1)
		select {
		case <-r.Context().Done():
		case <-unblockAG:
		}
	}))
	defer slowAGSrv.Close()
	defer close(unblockAG)
	origAGBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	antigravity.BaseURLs = []string{slowAGSrv.URL}
	t.Cleanup(func() { antigravity.BaseURLs = origAGBaseURLs })
	t.Setenv(antigravityForwardBaseURLEnv, "")

	agAccount := &Account{
		ID:          701,
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Credentials: map[string]any{"access_token": "tok-deadline-test"},
	}
	svc := NewAccountUsageService(nil, nil, nil, nil, &AntigravityQuotaFetcher{}, nil, nil, nil, NewUsageCache(), nil, nil)

	// Simulate a worker budget of 120ms where GetByID already consumed 40ms before entering getAntigravityUsage.
	slotStart := time.Now()
	slotCtx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	time.Sleep(40 * time.Millisecond)

	degraded, err := svc.getAntigravityUsage(slotCtx, agAccount, false)
	slotElapsed := time.Since(slotStart)
	require.NoError(t, err)
	require.NotNil(t, degraded)
	require.GreaterOrEqual(t, agHits.Load(), int32(1), "test must hit local blocking server offline")
	require.Less(t, slotElapsed, 500*time.Millisecond,
		"getAntigravityUsage must respect caller ctx deadline instead of resetting a fresh 30s wait")

	// 2. Grok dual-403 billing failure still persists a 403 billing snapshot and publishes it
	// to grokSnapshotExtra before bgRefreshInFlight clears, while ProbeBilling preserves (nil, err).
	oldOKTime := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	grokAcc := &Account{
		ID:          702,
		Name:        "grok-403-test",
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "grok-oauth-token",
			"refresh_token": "grok-refresh-token",
			"expires_at":    time.Now().Add(2 * grokTokenRefreshSkew).UTC().Format(time.RFC3339),
		},
		Extra: map[string]any{
			grokBillingExtraKey: &xai.BillingSummary{
				Plan:             "SuperGrok",
				StatusCode:       http.StatusOK,
				WeeklyStatusCode: http.StatusOK,
				UpdatedAt:        oldOKTime,
			},
		},
	}
	staleReaderCopy := *grokAcc
	staleReaderCopy.Extra = cloneExtraMap(grokAcc.Extra)

	repo := &stubSnapshotAccountRepo{
		accounts: map[int64]*Account{grokAcc.ID: grokAcc},
	}
	grokQuotaSvc := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), &grok403UpstreamStub{}, nil)

	// Active ProbeBilling preserves original (nil, err) contract on dual-403.
	activeRes, activeErr := grokQuotaSvc.ProbeBilling(context.Background(), grokAcc.ID)
	require.Error(t, activeErr)
	require.Nil(t, activeRes)

	// Background snapshot path receives persisted 403 billing and publishes it to grokSnapshotExtra.
	snapRes, snapErr := grokQuotaSvc.ProbeBillingSnapshotOnly(context.Background(), grokAcc.ID)
	require.Error(t, snapErr)
	require.NotNil(t, snapRes)
	require.True(t, snapRes.Persisted)
	require.NotNil(t, snapRes.Billing)
	require.Equal(t, http.StatusForbidden, snapRes.Billing.StatusCode)

	usageCache := NewUsageCache()
	usageSvc := NewAccountUsageService(repo, nil, nil, nil, nil, NewGrokQuotaFetcher(), grokQuotaSvc, nil, usageCache, nil, nil)
	usageSvc.testHookBeforeGrokProbeDueCheck = func() {
		usageCache.grokProbeCache.Store(grokAcc.ID, time.Now())
		usageSvc.cacheGrokSnapshotBilling(grokAcc.ID, snapRes.Billing)
	}
	interleaved, err := usageSvc.getGrokUsageSnapshot(context.Background(), &staleReaderCopy)
	require.NoError(t, err)
	require.Empty(t, interleaved.SnapshotRefreshState)
	require.NotNil(t, interleaved.GrokBilling)
	require.Equal(t, http.StatusForbidden, interleaved.GrokBilling.StatusCode,
		"concurrent reader with stale OK Extra must observe persisted 403 billing instead of caching stale OK state")
}

func TestOpenAISyncPersistDeadlineGrokPostTimeoutPublishAndOpenAIFailedProbeHint(t *testing.T) {
	// 1. OpenAI persistOpenAICodexProbeSnapshotSync must honor caller (worker) ctx deadline
	// instead of detaching with WithoutCancel + a fresh 5s window.
	blockUpdate := make(chan struct{})
	defer close(blockUpdate)
	blockingRepo := &stubSnapshotAccountRepo{
		blockUpdateExtra: blockUpdate,
	}
	svc := NewAccountUsageService(blockingRepo, nil, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)
	workerCtx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	start := time.Now()
	persisted := svc.persistOpenAICodexProbeSnapshotSync(workerCtx, 801, map[string]any{"codex_5h_used_percent": 50.0})
	require.False(t, persisted, "timed-out sync OpenAI persist must return false so caller does not clear probe error")
	require.Less(t, time.Since(start), 300*time.Millisecond,
		"sync OpenAI persist must honor worker ctx deadline instead of opening a detached 5s timeout")

	// 1b. CPR upstream success + UpdateExtra timeout/failure AND CPR upstream failure both
	// publish quota_refresh_failed so snapshot re-read without reloading the account row alerts the UI.
	now := time.Now().UTC()
	oldCPRTime := now.Add(-2 * time.Hour).Format(time.RFC3339)
	keys := []map[string]any{
		{"id": "key_1", "prefix": "sk-cpr-tri", "groups": []map[string]any{{"id": "grp_1"}}},
	}
	cprSrv := newCPRTestServer(t, keys, nil, cprTestQuota(now.Format(time.RFC3339), 64, false, "—", "—"))
	useCPRQuotaSettings(t, cprSrv.URL, cprTestAdminKey)

	// Case 1b-i: CPR upstream succeeds, UpdateExtra fails -> snapshot re-read surfaces quota_refresh_failed.
	cprPersistFailAcc := newCPRTestAccount(9801, cprSrv.URL)
	cprPersistFailAcc.Extra["codex_5h_used_percent"] = 25.0
	cprPersistFailAcc.Extra[cprQuotaSyncedAtExtra] = oldCPRTime
	staleCPRRow := *cprPersistFailAcc
	staleCPRRow.Extra = cloneExtraMap(cprPersistFailAcc.Extra)
	resetCPRQuotaState(cprPersistFailAcc.ID)
	t.Cleanup(func() { resetCPRQuotaState(cprPersistFailAcc.ID) })

	cprFailRepo := &stubSnapshotAccountRepo{
		accounts:       map[int64]*Account{cprPersistFailAcc.ID: cprPersistFailAcc},
		updateExtraErr: context.DeadlineExceeded,
	}
	cprCache := NewUsageCache()
	cprSvc := NewAccountUsageService(cprFailRepo, nil, nil, nil, nil, nil, nil, nil, cprCache, nil, nil)
	_, err := cprSvc.refreshOpenAIUsageSnapshot(context.Background(), cprPersistFailAcc, false, true)
	require.NoError(t, err)
	cprSnapAfterPersistFail, err := cprSvc.getOpenAIUsageSnapshot(context.Background(), &staleCPRRow)
	require.NoError(t, err)
	require.Equal(t, errorCodeQuotaRefreshFailed, cprSnapAfterPersistFail.ErrorCode,
		"CPR persist failure must surface quota_refresh_failed on subsequent snapshot read")
	require.InDelta(t, 25.0, cprSnapAfterPersistFail.FiveHour.Utilization, 1e-6)

	// Case 1b-ii: CPR upstream probe fails (HTTP 401) -> snapshot re-read without reloading
	// the account row still surfaces quota_refresh_failed alongside old 25% quota.
	cpr401Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":40101,"message":"invalid admin api key"}`))
	}))
	defer cpr401Srv.Close()
	useCPRQuotaSettings(t, cpr401Srv.URL, cprTestAdminKey)
	cprProbeFailAcc := newCPRTestAccount(9802, cpr401Srv.URL)
	cprProbeFailAcc.Extra["codex_5h_used_percent"] = 25.0
	cprProbeFailAcc.Extra[cprQuotaSyncedAtExtra] = oldCPRTime
	staleCPRRow2 := *cprProbeFailAcc
	staleCPRRow2.Extra = cloneExtraMap(cprProbeFailAcc.Extra)
	resetCPRQuotaState(cprProbeFailAcc.ID)
	t.Cleanup(func() { resetCPRQuotaState(cprProbeFailAcc.ID) })

	cprOKRepo := &stubSnapshotAccountRepo{accounts: map[int64]*Account{cprProbeFailAcc.ID: cprProbeFailAcc}}
	cprSvc2 := NewAccountUsageService(cprOKRepo, nil, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)
	_, err = cprSvc2.refreshOpenAIUsageSnapshot(context.Background(), cprProbeFailAcc, false, true)
	require.NoError(t, err)
	cprSnapAfterProbeFail, err := cprSvc2.getOpenAIUsageSnapshot(context.Background(), &staleCPRRow2)
	require.NoError(t, err)
	require.Equal(t, errorCodeQuotaRefreshFailed, cprSnapAfterProbeFail.ErrorCode,
		"CPR upstream failure must surface quota_refresh_failed without reloading account list")
	require.InDelta(t, 25.0, cprSnapAfterProbeFail.FiveHour.Utilization, 1e-6)

	// 2. Grok real worker timeout + immediate handoff (WITHOUT waiting for <-started):
	// Even with an already-canceled worker ctx, ProbeBillingSnapshotOnly registers billingInFlight
	// synchronously BEFORE returning, so there is zero gap between worker exit and flight startup.
	oldOKTime := now.Add(-2 * time.Hour).Format(time.RFC3339)
	grokAcc := &Account{
		ID:          802,
		Name:        "grok-timeout-commit",
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "grok-oauth-token",
			"refresh_token": "grok-refresh-token",
			"expires_at":    time.Now().Add(2 * grokTokenRefreshSkew).UTC().Format(time.RFC3339),
		},
		Extra: map[string]any{
			grokBillingExtraKey: &xai.BillingSummary{
				Plan:             "SuperGrok",
				StatusCode:       http.StatusOK,
				WeeklyStatusCode: http.StatusOK,
				UpdatedAt:        oldOKTime,
			},
		},
	}
	staleCopy := *grokAcc
	staleCopy.Extra = cloneExtraMap(grokAcc.Extra)

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	upstream := &grok403UpstreamStub{started: started, release: release}
	grokRepo := &stubSnapshotAccountRepo{accounts: map[int64]*Account{grokAcc.ID: grokAcc}}
	grokQuotaSvc := NewGrokQuotaService(grokRepo, nil, NewGrokTokenProvider(grokRepo, nil), upstream, nil)
	grokCache := NewUsageCache()
	grokUsageSvc := NewAccountUsageService(grokRepo, nil, nil, nil, nil, NewGrokQuotaFetcher(), grokQuotaSvc, nil, grokCache, nil, nil)

	// Pass an already-expired ctx so the worker returns immediately BEFORE the goroutine runs:
	// IsBillingProbeInFlight must ALREADY be true with zero handoff gap!
	grokCache.grokProbeCache.Store(grokAcc.ID, time.Now())
	expiredCtx, expiredCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expiredCancel()
	_, workerErr := grokQuotaSvc.ProbeBillingSnapshotOnly(expiredCtx, grokAcc.ID)
	require.ErrorIs(t, workerErr, context.DeadlineExceeded)
	require.True(t, grokQuotaSvc.IsBillingProbeInFlight(grokAcc.ID),
		"billingInFlight must be registered synchronously before returning on canceled ctx")

	// Immediately read page snapshot WITHOUT waiting on <-started: MUST see "refreshing".
	midSnap, err := grokUsageSvc.getGrokUsageSnapshot(context.Background(), &staleCopy)
	require.NoError(t, err)
	require.Equal(t, snapshotRefreshStateRefreshing, midSnap.SnapshotRefreshState)

	// Wait for flight to reach upstream, then release -> commits 403 via hook, clears billingInFlight.
	<-started
	close(release)
	require.Eventually(t, func() bool {
		return !grokQuotaSvc.IsBillingProbeInFlight(grokAcc.ID)
	}, 2*time.Second, 10*time.Millisecond)

	// Page read with stale OK Extra now observes the persisted 403 billing (no silent stale OK cache).
	afterSnap, err := grokUsageSvc.getGrokUsageSnapshot(context.Background(), &staleCopy)
	require.NoError(t, err)
	require.Empty(t, afterSnap.SnapshotRefreshState)
	require.NotNil(t, afterSnap.GrokBilling)
	require.Equal(t, http.StatusForbidden, afterSnap.GrokBilling.StatusCode)

	// 3. OpenAI failed background probe publishes short-lived error so cooldown snapshot re-read
	// preserves old quota while explicitly flagging quota_refresh_failed.
	oaCache := NewUsageCache()
	oaSvc := NewAccountUsageService(nil, nil, nil, nil, nil, nil, nil, nil, oaCache, nil, nil)
	oaAccount := &Account{
		ID:       803,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		// Missing access_token forces probeOpenAICodexSnapshot to fail cleanly without network.
		Credentials: map[string]any{},
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_enabled": true,
			"codex_5h_used_percent":                        33.0,
			"codex_7d_used_percent":                        12.0,
			"codex_usage_updated_at":                       oldOKTime,
		},
	}
	failedRefresh, err := oaSvc.refreshOpenAIUsageSnapshot(context.Background(), oaAccount, false, true)
	require.NoError(t, err)
	require.Equal(t, errorCodeQuotaRefreshFailed, failedRefresh.ErrorCode)

	// Subsequent snapshot read hits cooldown, returns old 33% quota AND surfaces quota_refresh_failed.
	cooldownSnap, err := oaSvc.getOpenAIUsageSnapshot(context.Background(), oaAccount)
	require.NoError(t, err)
	require.Empty(t, cooldownSnap.SnapshotRefreshState)
	require.NotNil(t, cooldownSnap.FiveHour)
	require.InDelta(t, 33.0, cooldownSnap.FiveHour.Utilization, 1e-6)
	require.Equal(t, errorCodeQuotaRefreshFailed, cooldownSnap.ErrorCode)
	require.NotEmpty(t, cooldownSnap.Error)
}

type grok200UpstreamStub struct {
	HTTPUpstream
}

func (u *grok200UpstreamStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	payload := `{"config":{"billingPeriodStart":"2026-07-01T00:00:00Z","billingPeriodEnd":"2026-08-01T00:00:00Z","monthlyLimit":{"val":4000}}}`
	if req != nil && req.URL != nil && req.URL.RawQuery == "format=credits" {
		payload = `{"config":{"currentPeriod":{"type":"WEEKLY","start":"2026-07-09T03:25:00Z","end":"2026-07-16T03:25:00Z"},"creditUsagePercent":40}}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(payload)),
	}, nil
}

func TestCodexV10CPRNoWindowGrokMonotonicAndPersistFail(t *testing.T) {
	now := time.Now().UTC()
	oldTime := now.Add(-2 * time.Hour).Format(time.RFC3339)

	// 1. CPR returns HTTP 200 with empty windows OR non-Codex-only windows:
	// Must NOT mark active, must NOT advance cpr_quota_synced_at, must record error,
	// and cooldown snapshot re-read must surface quota_refresh_failed alongside old 25%.
	for idx, tc := range []struct {
		name    string
		windows []map[string]any
	}{
		{
			name:    "empty_windows",
			windows: []map[string]any{},
		},
		{
			name: "non_codex_windows_only",
			windows: []map[string]any{
				{"key": "codex_bengalfox:18000s", "limitId": "codex_bengalfox", "windowSeconds": 18000, "usedPercent": 88.0, "limitReached": false, "resetAtDisplay": "—"},
			},
		},
	} {
		t.Run("cpr_"+tc.name, func(t *testing.T) {
			keys := []map[string]any{
				{"id": "key_1", "prefix": "sk-cpr-tri", "groups": []map[string]any{{"id": "grp_1"}}},
			}
			quotaPayload := map[string]any{
				"refreshedAt":      now.Format(time.RFC3339),
				"limitReached":     false,
				"rateLimitedUntil": nil,
				"windows":          tc.windows,
			}
			cprSrv := newCPRTestServer(t, keys, nil, quotaPayload)
			useCPRQuotaSettings(t, cprSrv.URL, cprTestAdminKey)

			accID := int64(9901 + idx)
			acc := newCPRTestAccount(accID, cprSrv.URL)
			acc.Extra["codex_5h_used_percent"] = 25.0
			acc.Extra[cprQuotaSyncedAtExtra] = oldTime
			staleRow := *acc
			staleRow.Extra = cloneExtraMap(acc.Extra)
			resetCPRQuotaState(acc.ID)
			t.Cleanup(func() { resetCPRQuotaState(acc.ID) })

			repo := &stubSnapshotAccountRepo{accounts: map[int64]*Account{acc.ID: acc}}
			svc := NewAccountUsageService(repo, nil, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)

			refreshed, err := svc.refreshOpenAIUsageSnapshot(context.Background(), acc, false, true)
			require.NoError(t, err)
			require.NotEqual(t, "active", refreshed.Source)
			require.Equal(t, errorCodeQuotaRefreshFailed, refreshed.ErrorCode)
			require.Equal(t, oldTime, acc.Extra[cprQuotaSyncedAtExtra],
				"empty/non-codex CPR response must not advance cpr_quota_synced_at")
			require.NotEmpty(t, acc.Extra[cprQuotaLastErrorExtra])

			snap, err := svc.getOpenAIUsageSnapshot(context.Background(), &staleRow)
			require.NoError(t, err)
			require.Empty(t, snap.SnapshotRefreshState)
			require.NotNil(t, snap.FiveHour)
			require.InDelta(t, 25.0, snap.FiveHour.Utilization, 1e-6)
			require.Equal(t, errorCodeQuotaRefreshFailed, snap.ErrorCode)
			require.NotEmpty(t, snap.Error)
		})
	}

	// 2. Grok snapshot billing cache monotonicity: an older billing observation must never
	// overwrite a newer persisted billing observation (e.g., 200 from an earlier flight vs newer 403).
	monoSvc := NewAccountUsageService(nil, nil, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)
	newer403 := &xai.BillingSummary{
		Plan:             "SuperGrok",
		StatusCode:       http.StatusForbidden,
		WeeklyStatusCode: http.StatusForbidden,
		UpdatedAt:        now.Format(time.RFC3339Nano),
	}
	older200 := &xai.BillingSummary{
		Plan:             "SuperGrok",
		StatusCode:       http.StatusOK,
		WeeklyStatusCode: http.StatusOK,
		UpdatedAt:        now.Add(-10 * time.Second).Format(time.RFC3339Nano),
	}
	newest200 := &xai.BillingSummary{
		Plan:             "SuperGrok",
		StatusCode:       http.StatusOK,
		WeeklyStatusCode: http.StatusOK,
		UpdatedAt:        now.Add(10 * time.Second).Format(time.RFC3339Nano),
	}
	monoSvc.cacheGrokSnapshotBilling(9910, newer403)
	monoSvc.cacheGrokSnapshotBilling(9910, older200)
	cached, err := grokBillingSnapshotFromExtra(monoSvc.mergeCachedGrokSnapshotExtra(9910, nil, time.Now()))
	require.NoError(t, err)
	require.NotNil(t, cached)
	require.Equal(t, http.StatusForbidden, cached.StatusCode,
		"older Grok billing observation must not overwrite newer persisted 403")
	monoSvc.cacheGrokSnapshotBilling(9910, newest200)
	cached, err = grokBillingSnapshotFromExtra(monoSvc.mergeCachedGrokSnapshotExtra(9910, nil, time.Now()))
	require.NoError(t, err)
	require.NotNil(t, cached)
	require.Equal(t, http.StatusOK, cached.StatusCode,
		"newer Grok billing observation must advance cache forward")

	// 3. Grok UpdateExtra persist failure (both dual-403 and 200 upstream):
	// Must NOT publish uncommitted billing into grokSnapshotExtra, MUST publish grokProbeError,
	// and cooldown snapshot re-read must surface quota_refresh_failed alongside old 200 billing.
	for idx, tc := range []struct {
		name     string
		upstream HTTPUpstream
		wantErr  bool
	}{
		{name: "dual_403_persist_fail", upstream: &grok403UpstreamStub{}, wantErr: true},
		{name: "ok_200_persist_fail", upstream: &grok200UpstreamStub{}, wantErr: false},
	} {
		t.Run("grok_"+tc.name, func(t *testing.T) {
			accID := int64(9921 + idx)
			grokAcc := &Account{
				ID:          accID,
				Name:        "grok-persist-fail",
				Platform:    PlatformGrok,
				Type:        AccountTypeOAuth,
				Status:      StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Credentials: map[string]any{
					"access_token":  "grok-oauth-token",
					"refresh_token": "grok-refresh-token",
					"expires_at":    now.Add(2 * grokTokenRefreshSkew).Format(time.RFC3339),
				},
				Extra: map[string]any{
					grokBillingExtraKey: &xai.BillingSummary{
						Plan:             "SuperGrok",
						StatusCode:       http.StatusOK,
						WeeklyStatusCode: http.StatusOK,
						UpdatedAt:        oldTime,
					},
				},
			}
			staleCopy := *grokAcc
			staleCopy.Extra = cloneExtraMap(grokAcc.Extra)

			repo := &stubSnapshotAccountRepo{
				accounts:       map[int64]*Account{grokAcc.ID: grokAcc},
				updateExtraErr: context.DeadlineExceeded,
			}
			grokQuotaSvc := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), tc.upstream, nil)
			cache := NewUsageCache()
			usageSvc := NewAccountUsageService(repo, nil, nil, nil, nil, NewGrokQuotaFetcher(), grokQuotaSvc, nil, cache, nil, nil)

			// Simulate cooldown already set by background refresh entry.
			cache.grokProbeCache.Store(grokAcc.ID, time.Now())
			res, err := grokQuotaSvc.ProbeBillingSnapshotOnly(context.Background(), grokAcc.ID)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, res)
			} else {
				require.NoError(t, err)
				require.NotNil(t, res)
				require.False(t, res.Persisted)
			}

			// Uncommitted billing must NOT be published into grokSnapshotExtra.
			require.Nil(t, usageSvc.mergeCachedGrokSnapshotExtra(grokAcc.ID, nil, time.Now()))

			// Cooldown snapshot re-read must return old 200 billing AND explicitly flag quota_refresh_failed.
			snap, err := usageSvc.getGrokUsageSnapshot(context.Background(), &staleCopy)
			require.NoError(t, err)
			require.Empty(t, snap.SnapshotRefreshState)
			require.NotNil(t, snap.GrokBilling)
			require.Equal(t, http.StatusOK, snap.GrokBilling.StatusCode)
			require.Equal(t, errorCodeQuotaRefreshFailed, snap.ErrorCode)
			require.NotEmpty(t, snap.Error)
		})
	}
}

type grokTimeoutUpstreamStub struct {
	HTTPUpstream
}

func (u *grokTimeoutUpstreamStub) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return nil, context.DeadlineExceeded
}

func TestCodexV11CPRPersistedErrForcePersistFailGrokTimeoutAndSameSecondOrder(t *testing.T) {
	now := time.Now().UTC()
	oldTime := now.Add(-2 * time.Hour).Format(time.RFC3339)

	// 1. P2-1: Gateway sync writes persisted cpr_quota_last_error (200+empty windows OR 401),
	// and a separate AccountUsageService with an empty in-memory error cache still alerts on snapshot read.
	t.Run("cpr_gateway_persisted_error_empty_windows_and_401", func(t *testing.T) {
		keys := []map[string]any{
			{"id": "key_1", "prefix": "sk-cpr-tri", "groups": []map[string]any{{"id": "grp_1"}}},
		}
		emptyQuota := map[string]any{
			"refreshedAt": now.Format(time.RFC3339),
			"windows":     []map[string]any{},
		}
		emptySrv := newCPRTestServer(t, keys, nil, emptyQuota)
		useCPRQuotaSettings(t, emptySrv.URL, cprTestAdminKey)

		accEmpty := newCPRTestAccount(9951, emptySrv.URL)
		accEmpty.Extra["codex_5h_used_percent"] = 28.0
		accEmpty.Extra[cprQuotaSyncedAtExtra] = oldTime
		resetCPRQuotaState(accEmpty.ID)
		t.Cleanup(func() { resetCPRQuotaState(accEmpty.ID) })

		repoEmpty := &stubSnapshotAccountRepo{accounts: map[int64]*Account{accEmpty.ID: accEmpty}}
		// Simulate gateway calling syncCPRQuotaSnapshot directly (no AccountUsageService in-memory probe error).
		_, err := syncCPRQuotaSnapshot(context.Background(), repoEmpty, accEmpty, false)
		require.NoError(t, err)
		require.NotEmpty(t, accEmpty.Extra[cprQuotaLastErrorExtra])

		freshPageSvc := NewAccountUsageService(repoEmpty, nil, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)
		snapEmpty, err := freshPageSvc.getOpenAIUsageSnapshot(context.Background(), accEmpty)
		require.NoError(t, err)
		require.Empty(t, snapEmpty.SnapshotRefreshState)
		require.InDelta(t, 28.0, snapEmpty.FiveHour.Utilization, 1e-6)
		require.Equal(t, errorCodeQuotaRefreshFailed, snapEmpty.ErrorCode)
		require.NotEmpty(t, snapEmpty.Error)

		// Now simulate gateway hitting 401 -> advances cpr_quota_synced_at and persists cpr_quota_last_error.
		srv401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":40101,"message":"invalid admin key"}`))
		}))
		defer srv401.Close()
		useCPRQuotaSettings(t, srv401.URL, cprTestAdminKey)

		acc401 := newCPRTestAccount(9952, srv401.URL)
		acc401.Extra["codex_5h_used_percent"] = 42.0
		acc401.Extra[cprQuotaSyncedAtExtra] = oldTime
		resetCPRQuotaState(acc401.ID)
		t.Cleanup(func() { resetCPRQuotaState(acc401.ID) })

		repo401 := &stubSnapshotAccountRepo{accounts: map[int64]*Account{acc401.ID: acc401}}
		_, err = syncCPRQuotaSnapshot(context.Background(), repo401, acc401, false)
		require.Error(t, err)

		freshPageSvc2 := NewAccountUsageService(repo401, nil, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)
		snap401, err := freshPageSvc2.getOpenAIUsageSnapshot(context.Background(), acc401)
		require.NoError(t, err)
		require.Empty(t, snap401.SnapshotRefreshState)
		require.InDelta(t, 42.0, snap401.FiveHour.Utilization, 1e-6)
		require.Equal(t, errorCodeQuotaRefreshFailed, snap401.ErrorCode)
		require.NotEmpty(t, snap401.Error)
	})

	// 2. P2-2: Active force=true ("Refresh page quota") must NOT swallow UpdateExtra persist failures
	// for either CPR or Grok, and must NOT leak uncommitted CPR data in the returned UsageInfo.
	t.Run("force_refresh_surfaces_persist_failure_for_cpr_and_grok", func(t *testing.T) {
		keys := []map[string]any{
			{"id": "key_1", "prefix": "sk-cpr-tri", "groups": []map[string]any{{"id": "grp_1"}}},
		}
		cprSrv := newCPRTestServer(t, keys, nil, cprTestQuota(now.Format(time.RFC3339), 88.0, false, "—", "—"))
		useCPRQuotaSettings(t, cprSrv.URL, cprTestAdminKey)

		cprAcc := newCPRTestAccount(9953, cprSrv.URL)
		cprAcc.Extra["codex_5h_used_percent"] = 25.0
		cprAcc.Extra[cprQuotaSyncedAtExtra] = oldTime
		resetCPRQuotaState(cprAcc.ID)
		t.Cleanup(func() { resetCPRQuotaState(cprAcc.ID) })

		cprFailRepo := &stubSnapshotAccountRepo{
			accounts:       map[int64]*Account{cprAcc.ID: cprAcc},
			updateExtraErr: context.DeadlineExceeded,
		}
		cprSvc := NewAccountUsageService(cprFailRepo, nil, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)
		forcedCPR, err := cprSvc.getOpenAIUsage(context.Background(), cprAcc, true)
		require.NoError(t, err)
		require.NotEqual(t, "active", forcedCPR.Source)
		require.Equal(t, errorCodeQuotaRefreshFailed, forcedCPR.ErrorCode)
		require.NotEmpty(t, forcedCPR.Error)
		require.InDelta(t, 25.0, forcedCPR.FiveHour.Utilization, 1e-6,
			"force=true CPR persist failure must keep persisted 25% instead of uncommitted 88%")

		grokAcc := &Account{
			ID:          9954,
			Name:        "grok-force-persist-fail",
			Platform:    PlatformGrok,
			Type:        AccountTypeOAuth,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":  "grok-oauth-token",
				"refresh_token": "grok-refresh-token",
				"expires_at":    now.Add(2 * grokTokenRefreshSkew).Format(time.RFC3339),
			},
			Extra: map[string]any{
				grokBillingExtraKey: &xai.BillingSummary{
					Plan:             "SuperGrok",
					StatusCode:       http.StatusOK,
					WeeklyStatusCode: http.StatusOK,
					UpdatedAt:        oldTime,
				},
			},
		}
		grokFailRepo := &stubSnapshotAccountRepo{
			accounts:       map[int64]*Account{grokAcc.ID: grokAcc},
			updateExtraErr: context.DeadlineExceeded,
		}
		grokQuotaSvc := NewGrokQuotaService(grokFailRepo, nil, NewGrokTokenProvider(grokFailRepo, nil), &grok200UpstreamStub{}, nil)
		grokSvc := NewAccountUsageService(grokFailRepo, nil, nil, nil, nil, NewGrokQuotaFetcher(), grokQuotaSvc, nil, NewUsageCache(), nil, nil)

		forcedGrok, err := grokSvc.getGrokUsage(context.Background(), grokAcc, true)
		require.NoError(t, err)
		require.Equal(t, errorCodeQuotaRefreshFailed, forcedGrok.ErrorCode,
			"force=true Grok 200+UpdateExtra failure must explicitly surface quota_refresh_failed")
		require.NotEmpty(t, forcedGrok.Error)
	})

	// 3. P2-3: Grok dual-window timeout AND prepareProbe token failure must alert instead of clearing error.
	t.Run("grok_dual_timeout_and_prepare_probe_failure_alert", func(t *testing.T) {
		grokTimeoutAcc := &Account{
			ID:          9955,
			Name:        "grok-dual-timeout",
			Platform:    PlatformGrok,
			Type:        AccountTypeOAuth,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":  "grok-oauth-token",
				"refresh_token": "grok-refresh-token",
				"expires_at":    now.Add(2 * grokTokenRefreshSkew).Format(time.RFC3339),
			},
			Extra: map[string]any{
				grokBillingExtraKey: &xai.BillingSummary{
					Plan:             "SuperGrok",
					StatusCode:       http.StatusOK,
					WeeklyStatusCode: http.StatusOK,
					UpdatedAt:        oldTime,
				},
			},
		}
		staleCopy := *grokTimeoutAcc
		staleCopy.Extra = cloneExtraMap(grokTimeoutAcc.Extra)

		repo := &stubSnapshotAccountRepo{accounts: map[int64]*Account{grokTimeoutAcc.ID: grokTimeoutAcc}}
		grokQuotaSvc := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), &grokTimeoutUpstreamStub{}, nil)
		cache := NewUsageCache()
		usageSvc := NewAccountUsageService(repo, nil, nil, nil, nil, NewGrokQuotaFetcher(), grokQuotaSvc, nil, cache, nil, nil)

		cache.grokProbeCache.Store(grokTimeoutAcc.ID, time.Now())
		res, err := grokQuotaSvc.ProbeBillingSnapshotOnly(context.Background(), grokTimeoutAcc.ID)
		require.Error(t, err)
		require.NotNil(t, res)
		require.True(t, res.Persisted)
		require.Equal(t, 0, res.Billing.StatusCode)
		require.True(t, res.Billing.Partial)
		require.Len(t, res.Billing.FailedWindows, 2)

		// Snapshot read with stale row AND with updated DB row must BOTH surface quota_refresh_failed.
		snapStale, err := usageSvc.getGrokUsageSnapshot(context.Background(), &staleCopy)
		require.NoError(t, err)
		require.Equal(t, errorCodeQuotaRefreshFailed, snapStale.ErrorCode)
		require.NotEmpty(t, snapStale.Error)

		// Even a fresh service (simulating process restart) reading the persisted DB row must alert.
		persistedRow := *grokTimeoutAcc
		persistedRow.Extra = map[string]any{grokBillingExtraKey: res.Billing}
		restartedSvc := NewAccountUsageService(repo, nil, nil, nil, nil, NewGrokQuotaFetcher(), grokQuotaSvc, nil, NewUsageCache(), nil, nil)
		restartedSvc.cache.grokProbeCache.Store(grokTimeoutAcc.ID, time.Now())
		snapPersisted, err := restartedSvc.getGrokUsageSnapshot(context.Background(), &persistedRow)
		require.NoError(t, err)
		require.Equal(t, errorCodeQuotaRefreshFailed, snapPersisted.ErrorCode)
		require.NotEmpty(t, snapPersisted.Error)

		// prepareProbe failure (missing credentials) must also publish grokProbeError.
		grokNoTokenAcc := &Account{
			ID:          9956,
			Name:        "grok-missing-token",
			Platform:    PlatformGrok,
			Type:        AccountTypeOAuth,
			Status:      StatusActive,
			Credentials: map[string]any{},
			Extra: map[string]any{
				grokBillingExtraKey: &xai.BillingSummary{
					Plan:       "SuperGrok",
					StatusCode: http.StatusOK,
					UpdatedAt:  oldTime,
				},
			},
		}
		repoNoTok := &stubSnapshotAccountRepo{accounts: map[int64]*Account{grokNoTokenAcc.ID: grokNoTokenAcc}}
		grokQuotaSvcNoTok := NewGrokQuotaService(repoNoTok, nil, NewGrokTokenProvider(repoNoTok, nil), &grok200UpstreamStub{}, nil)
		cacheNoTok := NewUsageCache()
		usageSvcNoTok := NewAccountUsageService(repoNoTok, nil, nil, nil, nil, NewGrokQuotaFetcher(), grokQuotaSvcNoTok, nil, cacheNoTok, nil, nil)

		cacheNoTok.grokProbeCache.Store(grokNoTokenAcc.ID, time.Now())
		_, err = grokQuotaSvcNoTok.ProbeBillingSnapshotOnly(context.Background(), grokNoTokenAcc.ID)
		require.Error(t, err)
		snapNoTok, err := usageSvcNoTok.getGrokUsageSnapshot(context.Background(), grokNoTokenAcc)
		require.NoError(t, err)
		require.Equal(t, errorCodeQuotaRefreshFailed, snapNoTok.ErrorCode)
		require.NotEmpty(t, snapNoTok.Error)
	})

	// 4. P2-4: Same-second Grok commits (200 then 403 within identical RFC3339 second)
	// must be strictly ordered by ObservedVersion so concurrent readers with the older 200 DB row
	// adopt the newer cached 403 instead of discarding it on timestamp tie.
	t.Run("grok_same_second_commits_ordered_by_version", func(t *testing.T) {
		sameSecond := now.UTC().Format(time.RFC3339)
		b200 := xai.StampBillingSummary(&xai.BillingSummary{
			Plan:             "SuperGrok",
			WeeklyStatusCode: http.StatusOK,
		}, http.StatusOK, "billing_probe")
		b403 := xai.StampBillingSummary(&xai.BillingSummary{
			Plan:             "SuperGrok",
			WeeklyStatusCode: http.StatusForbidden,
			ObservedVersion:  b200.ObservedVersion,
		}, http.StatusForbidden, "billing_probe")
		// Force identical second-granularity timestamps to reproduce same-second race deterministically.
		b200.UpdatedAt, b200.FetchedAt = sameSecond, sameSecond
		b403.UpdatedAt, b403.FetchedAt = sameSecond, sameSecond
		require.Greater(t, b403.ObservedVersion, b200.ObservedVersion)

		svc := NewAccountUsageService(nil, nil, nil, nil, nil, NewGrokQuotaFetcher(), nil, nil, NewUsageCache(), nil, nil)
		svc.cacheGrokSnapshotBilling(9957, b403)
		// Late arrival of older same-second 200 must NOT overwrite newer 403.
		svc.cacheGrokSnapshotBilling(9957, b200)

		staleDBRowExtra := map[string]any{grokBillingExtraKey: b200}
		merged := svc.mergeCachedGrokSnapshotExtra(9957, staleDBRowExtra, time.Now())
		resolved, err := grokBillingSnapshotFromExtra(merged)
		require.NoError(t, err)
		require.NotNil(t, resolved)
		require.Equal(t, http.StatusForbidden, resolved.StatusCode,
			"concurrent reader holding same-second older 200 DB row must adopt newer cached 403")
	})
}

func TestCodexV12LosslessVersionRoundTripGrokFirstFailureAndRestrictedCPR(t *testing.T) {
	now := time.Now().UTC()
	oldTime := now.Add(-2 * time.Hour).Format(time.RFC3339)

	// 1. P2-1: ObservedVersion must survive Ent's JSONB -> map[string]any round-trip losslessly
	// even for adjacent nanosecond-scale int64 values (+1) that float64 would round away.
	t.Run("grok_observed_version_lossless_json_roundtrip", func(t *testing.T) {
		sameSecond := now.Format(time.RFC3339)
		baseVer := now.UnixNano()
		// Guarantee value exceeds float64 53-bit exact integer limit (~9e15).
		require.Greater(t, baseVer, int64(1)<<53)

		b200 := &xai.BillingSummary{
			Plan:             "SuperGrok",
			StatusCode:       http.StatusOK,
			WeeklyStatusCode: http.StatusOK,
			FetchedAt:        sameSecond,
			UpdatedAt:        sameSecond,
			ObservedVersion:  baseVer,
		}
		b403 := &xai.BillingSummary{
			Plan:             "SuperGrok",
			StatusCode:       http.StatusForbidden,
			WeeklyStatusCode: http.StatusForbidden,
			FetchedAt:        sameSecond,
			UpdatedAt:        sameSecond,
			ObservedVersion:  baseVer + 1,
		}

		// Simulate Ent decoding PostgreSQL jsonb column into map[string]any (numbers -> float64, strings -> string).
		roundTripExtra := func(b *xai.BillingSummary) map[string]any {
			rawBytes, err := json.Marshal(map[string]any{grokBillingExtraKey: b})
			require.NoError(t, err)
			var decoded map[string]any
			require.NoError(t, json.Unmarshal(rawBytes, &decoded))
			billingMap, ok := decoded[grokBillingExtraKey].(map[string]any)
			require.True(t, ok)
			_, isString := billingMap["observed_version"].(string)
			require.True(t, isString, "observed_version must be JSON-string encoded to avoid float64 precision loss")
			return decoded
		}

		svc := NewAccountUsageService(nil, nil, nil, nil, nil, NewGrokQuotaFetcher(), nil, nil, NewUsageCache(), nil, nil)

		// Case A: Older 200 in DB (JSON round-tripped), newer +1 403 in cache -> adopts cached 403.
		svc.cacheGrokSnapshotBilling(9961, b403)
		mergedA := svc.mergeCachedGrokSnapshotExtra(9961, roundTripExtra(b200), time.Now())
		resA, err := grokBillingSnapshotFromExtra(mergedA)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, resA.StatusCode)
		require.Equal(t, b403.ObservedVersion, resA.ObservedVersion)

		// Case B: Newer +1 403 in DB (JSON round-tripped), older 200 in cache -> keeps DB 403.
		svc.cacheGrokSnapshotBilling(9962, b200)
		mergedB := svc.mergeCachedGrokSnapshotExtra(9962, roundTripExtra(b403), time.Now())
		resB, err := grokBillingSnapshotFromExtra(mergedB)
		require.NoError(t, err)
		require.Equal(t, http.StatusForbidden, resB.StatusCode)
		require.Equal(t, b403.ObservedVersion, resB.ObservedVersion)
	})

	// 2. P2-2: First Grok refresh failure (no prior billing snapshot -> BuildUsageInfo sets quota_unknown)
	// must NOT be masked by quota_unknown on either force=true UpdateExtra failure or prepareProbe failure.
	t.Run("grok_first_refresh_failure_overrides_quota_unknown", func(t *testing.T) {
		// Case A: force=true with 200 upstream + UpdateExtra failure on fresh account (empty Extra).
		freshGrok := &Account{
			ID:          9963,
			Name:        "grok-fresh-persist-fail",
			Platform:    PlatformGrok,
			Type:        AccountTypeOAuth,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":  "grok-oauth-token",
				"refresh_token": "grok-refresh-token",
				"expires_at":    now.Add(2 * grokTokenRefreshSkew).Format(time.RFC3339),
			},
			Extra: map[string]any{},
		}
		repoFail := &stubSnapshotAccountRepo{
			accounts:       map[int64]*Account{freshGrok.ID: freshGrok},
			updateExtraErr: context.DeadlineExceeded,
		}
		quotaSvcFail := NewGrokQuotaService(repoFail, nil, NewGrokTokenProvider(repoFail, nil), &grok200UpstreamStub{}, nil)
		usageSvcFail := NewAccountUsageService(repoFail, nil, nil, nil, nil, NewGrokQuotaFetcher(), quotaSvcFail, nil, NewUsageCache(), nil, nil)

		forcedUsage, err := usageSvcFail.getGrokUsage(context.Background(), freshGrok, true)
		require.NoError(t, err)
		require.Equal(t, errorCodeQuotaRefreshFailed, forcedUsage.ErrorCode,
			"force=true persist failure on first probe must override quota_unknown")
		require.NotEmpty(t, forcedUsage.Error)
		require.Equal(t, "unknown_until_first_response", forcedUsage.GrokQuotaSnapshotState)

		// Case B: prepareProbe failure on fresh account (empty Extra + missing credentials) -> snapshot read alerts.
		freshNoCreds := &Account{
			ID:          9964,
			Name:        "grok-fresh-no-creds",
			Platform:    PlatformGrok,
			Type:        AccountTypeOAuth,
			Status:      StatusActive,
			Credentials: map[string]any{},
			Extra:       map[string]any{},
		}
		repoNoCreds := &stubSnapshotAccountRepo{accounts: map[int64]*Account{freshNoCreds.ID: freshNoCreds}}
		quotaSvcNoCreds := NewGrokQuotaService(repoNoCreds, nil, NewGrokTokenProvider(repoNoCreds, nil), &grok200UpstreamStub{}, nil)
		cacheNoCreds := NewUsageCache()
		usageSvcNoCreds := NewAccountUsageService(repoNoCreds, nil, nil, nil, nil, NewGrokQuotaFetcher(), quotaSvcNoCreds, nil, cacheNoCreds, nil, nil)

		cacheNoCreds.grokProbeCache.Store(freshNoCreds.ID, time.Now())
		_, err = quotaSvcNoCreds.ProbeBillingSnapshotOnly(context.Background(), freshNoCreds.ID)
		require.Error(t, err)

		snapNoCreds, err := usageSvcNoCreds.getGrokUsageSnapshot(context.Background(), freshNoCreds)
		require.NoError(t, err)
		require.Equal(t, errorCodeQuotaRefreshFailed, snapNoCreds.ErrorCode,
			"prepareProbe failure on first probe must override quota_unknown")
		require.NotEmpty(t, snapNoCreds.Error)
		require.Equal(t, "unknown_until_first_response", snapNoCreds.GrokQuotaSnapshotState)
	})

	// 3. P2-3: Stale CPR account with persisted cpr_quota_last_error and NO active throttle (needsRefresh && probeDue):
	// restricted admin does not schedule a background refresh, so snapshot read returns empty SnapshotRefreshState
	// AND must still surface quota_refresh_failed instead of dropping the persisted error.
	t.Run("cpr_persisted_error_preserved_when_stale_probe_due_and_restricted", func(t *testing.T) {
		cprBase := "http://cpr-restricted-test.local:18080"
		useCPRQuotaSettings(t, cprBase, cprTestAdminKey)

		acc := newCPRTestAccount(9965, cprBase)
		acc.Extra["codex_5h_used_percent"] = 28.0
		acc.Extra[cprQuotaSyncedAtExtra] = oldTime
		acc.Extra[cprQuotaLastErrorExtra] = "cpr quota response contained no valid codex window"
		resetCPRQuotaState(acc.ID) // Ensure isCPRQuotaSyncDue == true (not throttled)
		t.Cleanup(func() { resetCPRQuotaState(acc.ID) })

		repo := &stubSnapshotAccountRepo{accounts: map[int64]*Account{acc.ID: acc}}
		svc := NewAccountUsageService(repo, nil, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)

		restrictedCtx := adminaccess.WithPrincipal(context.Background(), &adminaccess.Principal{
			KeyID:  "restricted-key",
			Policy: adminaccess.Policy{FullAccess: false},
		})
		snap, err := svc.getOpenAIUsageSnapshot(restrictedCtx, acc)
		require.NoError(t, err)
		require.Empty(t, snap.SnapshotRefreshState, "restricted admin must not schedule detached background refresh")
		require.NotNil(t, snap.FiveHour)
		require.InDelta(t, 28.0, snap.FiveHour.Utilization, 1e-6)
		require.Equal(t, errorCodeQuotaRefreshFailed, snap.ErrorCode,
			"persisted cpr_quota_last_error must still alert when needsRefresh && probeDue skips background scheduling")
		require.NotEmpty(t, snap.Error)
	})
}
