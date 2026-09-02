//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type schedulerCachePostCommitInterleaving struct {
	service.SchedulerCache

	mu                    sync.Mutex
	accounts              map[int64]*service.Account
	firstOperation        chan string
	releaseFirstOperation chan struct{}
	firstMu               sync.Mutex
	firstOperationSent    bool
	releaseOnce           sync.Once
}

var _ service.SchedulerCache = (*schedulerCachePostCommitInterleaving)(nil)

func newSchedulerCachePostCommitInterleaving(accounts map[int64]*service.Account) *schedulerCachePostCommitInterleaving {
	return &schedulerCachePostCommitInterleaving{
		accounts:              accounts,
		firstOperation:        make(chan string, 1),
		releaseFirstOperation: make(chan struct{}),
	}
}

func (c *schedulerCachePostCommitInterleaving) waitForFirstOperation(kind string) {
	c.firstMu.Lock()
	if c.firstOperationSent {
		c.firstMu.Unlock()
		return
	}
	c.firstOperationSent = true
	c.firstMu.Unlock()
	c.firstOperation <- kind
	<-c.releaseFirstOperation
}

func (c *schedulerCachePostCommitInterleaving) releaseFirst() {
	c.releaseOnce.Do(func() { close(c.releaseFirstOperation) })
}

func (c *schedulerCachePostCommitInterleaving) SetAccount(_ context.Context, account *service.Account) error {
	c.waitForFirstOperation("set")
	if account == nil {
		return nil
	}
	snapshot := *account
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accounts[account.ID] = &snapshot
	return nil
}

func (c *schedulerCachePostCommitInterleaving) DeleteAccount(_ context.Context, accountID int64) error {
	c.waitForFirstOperation("delete")
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.accounts, accountID)
	return nil
}

func (c *schedulerCachePostCommitInterleaving) containsAccount(accountID int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.accounts[accountID]
	return ok
}

func TestCompareAndSwapOpenAIOAuthProxyAcceptsNullCredentialsAndUpdatesShadows(t *testing.T) {
	ctx := context.Background()
	proxy := mustCreateProxy(t, integrationEntClient, &service.Proxy{
		Name:         "fixed-egress-cas-null-credentials",
		Protocol:     "socks5h",
		Host:         "100.80.10.114",
		Port:         1080,
		Status:       service.StatusActive,
		FallbackMode: service.FallbackModeNone,
	})
	parent := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:     "fixed-egress-cas-parent",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Status:   service.StatusActive,
	})
	shadow := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:            "fixed-egress-cas-shadow",
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeOAuth,
		Status:          service.StatusActive,
		ParentAccountID: &parent.ID,
		QuotaDimension:  service.QuotaDimensionSpark,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `
			DELETE FROM scheduler_outbox
			WHERE event_type = $1
			  AND payload @> jsonb_build_object('account_ids', jsonb_build_array($2::bigint))
		`, service.SchedulerOutboxEventAccountBulkChanged, parent.ID)
		_ = integrationEntClient.Account.DeleteOneID(shadow.ID).Exec(ctx)
		_ = integrationEntClient.Account.DeleteOneID(parent.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(ctx)
	})

	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	updated, err := repo.CompareAndSwapOpenAIOAuthProxy(ctx, []int64{parent.ID}, 0, proxy)
	require.NoError(t, err)
	require.Equal(t, []int64{parent.ID}, updated)

	var parentProxyID, shadowProxyID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT proxy_id FROM accounts WHERE id = $1", parent.ID).Scan(&parentProxyID))
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT proxy_id FROM accounts WHERE id = $1", shadow.ID).Scan(&shadowProxyID))
	require.Equal(t, proxy.ID, parentProxyID)
	require.Equal(t, proxy.ID, shadowProxyID)
}

func TestCompareAndSwapOpenAIOAuthProxySupportsOpenAISetupTokenParent(t *testing.T) {
	ctx := context.Background()
	proxy := mustCreateProxy(t, integrationEntClient, &service.Proxy{
		Name:         "fixed-egress-cas-setup-token",
		Protocol:     "socks5h",
		Host:         "100.81.60.44",
		Port:         1080,
		Status:       service.StatusActive,
		FallbackMode: service.FallbackModeNone,
	})
	parent := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:     "fixed-egress-cas-setup-token-parent",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeSetupToken,
		Status:   service.StatusActive,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `
			DELETE FROM scheduler_outbox
			WHERE event_type = $1
			  AND payload @> jsonb_build_object('account_ids', jsonb_build_array($2::bigint))
		`, service.SchedulerOutboxEventAccountBulkChanged, parent.ID)
		_ = integrationEntClient.Account.DeleteOneID(parent.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(ctx)
	})

	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	updated, err := repo.CompareAndSwapOpenAIOAuthProxy(ctx, []int64{parent.ID}, 0, proxy)
	require.NoError(t, err)
	require.Equal(t, []int64{parent.ID}, updated)

	var parentProxyID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT proxy_id FROM accounts WHERE id = $1", parent.ID).Scan(&parentProxyID))
	require.Equal(t, proxy.ID, parentProxyID)
}

func TestCompareAndSwapOpenAIOAuthProxyMigratesErrorParentWithoutActivatingIt(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	oldProxy := mustCreateProxy(t, integrationEntClient, &service.Proxy{
		Name:         fmt.Sprintf("fixed-egress-error-migration-old-%d", suffix),
		Protocol:     "http",
		Host:         "100.67.153.111",
		Port:         7890,
		Status:       service.StatusActive,
		FallbackMode: service.FallbackModeNone,
	})
	newProxy := fixedEgressRaceProxy(t, fmt.Sprintf("fixed-egress-error-migration-new-%d", suffix))
	parent := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:         fmt.Sprintf("fixed-egress-error-migration-parent-%d", suffix),
		Platform:     service.PlatformOpenAI,
		Type:         service.AccountTypeOAuth,
		Status:       service.StatusError,
		ErrorMessage: "token revoked",
		Schedulable:  false,
		ProxyID:      &oldProxy.ID,
	})
	shadow := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:            fmt.Sprintf("fixed-egress-error-migration-shadow-%d", suffix),
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeOAuth,
		Status:          service.StatusError,
		ErrorMessage:    "parent token revoked",
		Schedulable:     false,
		ProxyID:         &oldProxy.ID,
		ParentAccountID: &parent.ID,
		QuotaDimension:  service.QuotaDimensionSpark,
	})
	_, err := integrationDB.ExecContext(ctx,
		"UPDATE accounts SET schedulable = false WHERE id IN ($1, $2)", parent.ID, shadow.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `
			DELETE FROM scheduler_outbox
			WHERE account_id IN ($1, $2)
			   OR payload @> jsonb_build_object('account_ids', jsonb_build_array($1::bigint))
			   OR payload @> jsonb_build_object('account_ids', jsonb_build_array($2::bigint))
		`, parent.ID, shadow.ID)
		_ = integrationEntClient.Account.DeleteOneID(shadow.ID).Exec(context.Background())
		_ = integrationEntClient.Account.DeleteOneID(parent.ID).Exec(context.Background())
		_ = integrationEntClient.Proxy.DeleteOneID(oldProxy.ID).Exec(context.Background())
		_ = integrationEntClient.Proxy.DeleteOneID(newProxy.ID).Exec(context.Background())
	})

	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	updated, err := repo.CompareAndSwapOpenAIOAuthProxy(ctx, []int64{parent.ID}, oldProxy.ID, newProxy)
	require.NoError(t, err)
	require.Equal(t, []int64{parent.ID}, updated)

	for _, accountID := range []int64{parent.ID, shadow.ID} {
		var proxyID int64
		var status string
		var schedulable bool
		require.NoError(t, integrationDB.QueryRowContext(ctx,
			"SELECT proxy_id, status, schedulable FROM accounts WHERE id = $1", accountID).
			Scan(&proxyID, &status, &schedulable))
		require.Equal(t, newProxy.ID, proxyID)
		require.Equal(t, service.StatusError, status, "egress migration must not reactivate a revoked account")
		require.False(t, schedulable)
	}
}

func TestCompareAndSwapOpenAIOAuthProxyReusesOuterTransaction(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	proxy := fixedEgressRaceProxy(t, fmt.Sprintf("fixed-egress-cas-outer-tx-proxy-%d", suffix))
	parent := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:        fmt.Sprintf("fixed-egress-cas-outer-tx-parent-%d", suffix),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
	})
	shadow := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:            fmt.Sprintf("fixed-egress-cas-outer-tx-shadow-%d", suffix),
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeOAuth,
		Status:          service.StatusActive,
		Schedulable:     true,
		ParentAccountID: &parent.ID,
		QuotaDimension:  service.QuotaDimensionSpark,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `
			DELETE FROM scheduler_outbox
			WHERE payload @> jsonb_build_object('account_ids', jsonb_build_array($1::bigint))
		`, parent.ID)
		_ = integrationEntClient.Account.DeleteOneID(shadow.ID).Exec(context.Background())
		_ = integrationEntClient.Account.DeleteOneID(parent.ID).Exec(context.Background())
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(context.Background())
	})

	outerTx, err := integrationEntClient.Tx(ctx)
	require.NoError(t, err)
	txCtx := dbent.NewTxContext(ctx, outerTx)
	cache := &schedulerCacheRecorder{}
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, cache)
	updated, err := repo.CompareAndSwapOpenAIOAuthProxy(txCtx, []int64{parent.ID}, 0, proxy)
	require.NoError(t, err)
	require.Equal(t, []int64{parent.ID}, updated)
	require.Empty(t, cache.deleteIDs, "an outer transaction must not publish cache retirement before commit")
	require.NoError(t, outerTx.Rollback())

	for _, accountID := range []int64{parent.ID, shadow.ID} {
		var proxyID sql.NullInt64
		require.NoError(t, integrationDB.QueryRowContext(ctx,
			"SELECT proxy_id FROM accounts WHERE id = $1", accountID).Scan(&proxyID))
		require.False(t, proxyID.Valid, "outer rollback must restore the original proxy binding")
	}
	var outboxCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM scheduler_outbox
		WHERE payload @> jsonb_build_object('account_ids', jsonb_build_array($1::bigint))
	`, parent.ID).Scan(&outboxCount))
	require.Zero(t, outboxCount, "outer rollback must remove the CAS outbox event")
}

func TestCompareAndSwapOpenAIOAuthProxyPostCommitEvictionCannotResurrectNewerDelete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	suffix := time.Now().UnixNano()
	proxy := mustCreateProxy(t, integrationEntClient, &service.Proxy{
		Name:         fmt.Sprintf("fixed-egress-cas-post-commit-cache-%d", suffix),
		Protocol:     "socks5h",
		Host:         "100.80.10.114",
		Port:         1080,
		Status:       service.StatusActive,
		FallbackMode: service.FallbackModeNone,
	})
	parent := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:        fmt.Sprintf("fixed-egress-cas-post-commit-parent-%d", suffix),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
	})
	cache := newSchedulerCachePostCommitInterleaving(map[int64]*service.Account{parent.ID: parent})
	t.Cleanup(func() {
		cache.releaseFirst()
		_, _ = integrationDB.ExecContext(context.Background(), `
			DELETE FROM scheduler_outbox
			WHERE account_id = $1
			   OR payload @> jsonb_build_object('account_ids', jsonb_build_array($1::bigint))
		`, parent.ID)
		_ = integrationEntClient.Account.DeleteOneID(parent.ID).Exec(context.Background())
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(context.Background())
	})

	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, cache)
	casDone := make(chan error, 1)
	go func() {
		_, err := repo.CompareAndSwapOpenAIOAuthProxy(ctx, []int64{parent.ID}, 0, proxy)
		casDone <- err
	}()

	var firstOperation string
	select {
	case firstOperation = <-cache.firstOperation:
	case <-ctx.Done():
		t.Fatalf("CAS did not reach its post-commit scheduler-cache operation: %v", ctx.Err())
	}

	deleteErr := repo.Delete(ctx, parent.ID)
	cache.releaseFirst()
	require.NoError(t, deleteErr)

	select {
	case err := <-casDone:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatalf("CAS did not complete after cache operation was released: %v", ctx.Err())
	}

	require.Equal(t, "delete", firstOperation, "a fixed-egress CAS must evict, not repopulate, after commit")
	require.False(t, cache.containsAccount(parent.ID), "a delayed CAS cache operation must not restore an account deleted by a newer commit")
}

func TestSchedulerSnapshotSyncEvictsFixedEgressAccountsAndSetsOrdinaryAccounts(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	proxy := fixedEgressRaceProxy(t, fmt.Sprintf("fixed-egress-snapshot-sync-proxy-%d", suffix))
	parent := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:        fmt.Sprintf("fixed-egress-snapshot-sync-parent-%d", suffix),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		ProxyID:     &proxy.ID,
	})
	shadow := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:            fmt.Sprintf("fixed-egress-snapshot-sync-shadow-%d", suffix),
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeOAuth,
		Status:          service.StatusActive,
		Schedulable:     true,
		ProxyID:         &proxy.ID,
		ParentAccountID: &parent.ID,
		QuotaDimension:  service.QuotaDimensionSpark,
	})
	ordinary := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:        fmt.Sprintf("ordinary-snapshot-sync-account-%d", suffix),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = ANY($1)", pq.Array([]int64{parent.ID, shadow.ID, ordinary.ID}))
		_ = integrationEntClient.Account.DeleteOneID(shadow.ID).Exec(ctx)
		_ = integrationEntClient.Account.DeleteOneID(parent.ID).Exec(ctx)
		_ = integrationEntClient.Account.DeleteOneID(ordinary.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(ctx)
	})

	assertCacheActions := func(t *testing.T, cache *schedulerCacheRecorder) {
		t.Helper()
		require.ElementsMatch(t, []int64{parent.ID, shadow.ID}, cache.deleteIDs)
		require.Len(t, cache.setAccounts, 1)
		require.Equal(t, ordinary.ID, cache.setAccounts[0].ID)
	}

	t.Run("single", func(t *testing.T) {
		cache := &schedulerCacheRecorder{}
		repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, cache)
		repo.syncSchedulerAccountSnapshot(ctx, parent.ID)
		repo.syncSchedulerAccountSnapshot(ctx, shadow.ID)
		repo.syncSchedulerAccountSnapshot(ctx, ordinary.ID)
		assertCacheActions(t, cache)
	})

	t.Run("batch", func(t *testing.T) {
		cache := &schedulerCacheRecorder{}
		repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, cache)
		repo.syncSchedulerAccountSnapshots(ctx, []int64{parent.ID, shadow.ID, ordinary.ID})
		assertCacheActions(t, cache)
	})
}

func TestProxyDisableRetiresFixedEgressSchedulerSnapshotAgainstStaleOrdinaryWriter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	suffix := time.Now().UnixNano()
	proxy := mustCreateProxy(t, integrationEntClient, &service.Proxy{
		Name:         fmt.Sprintf("fixed-egress-proxy-retire-%d", suffix),
		Protocol:     "socks5h",
		Host:         "100.80.10.114",
		Port:         1080,
		Status:       service.StatusActive,
		FallbackMode: service.FallbackModeNone,
	})
	account := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:        fmt.Sprintf("fixed-egress-proxy-retire-account-%d", suffix),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		ProxyID:     &proxy.ID,
	})
	id := strconv.FormatInt(account.ID, 10)
	schedulerKeys := []string{
		schedulerAccountKey(id),
		schedulerAccountMetaKey(id),
		schedulerAccountRetiredKey(id),
		schedulerLastUsedKey(id),
	}
	require.NoError(t, integrationRedis.Del(ctx, schedulerKeys...).Err())
	t.Cleanup(func() {
		_ = integrationRedis.Del(context.Background(), schedulerKeys...).Err()
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_ = integrationEntClient.Account.DeleteOneID(account.ID).Exec(context.Background())
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(context.Background())
	})
	cache, ok := newSchedulerCacheWithChunkSizes(integrationRedis, defaultSchedulerSnapshotMGetChunkSize, defaultSchedulerSnapshotWriteChunkSize).(*schedulerCache)
	require.True(t, ok)

	staleOrdinary := &service.Account{
		ID:       account.ID,
		Name:     "pre-disable-ordinary-snapshot",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Status:   service.StatusActive,
	}
	require.NoError(t, cache.SetAccount(ctx, staleOrdinary))
	cachedBeforeDisable, err := cache.GetAccount(ctx, account.ID)
	require.NoError(t, err)
	require.NotNil(t, cachedBeforeDisable)

	writerStarted := make(chan struct{})
	releaseWriter := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		close(writerStarted)
		<-releaseWriter
		writerDone <- cache.SetAccount(ctx, staleOrdinary)
	}()
	select {
	case <-writerStarted:
	case <-ctx.Done():
		t.Fatalf("stale writer did not capture its pre-disable snapshot: %v", ctx.Err())
	}

	proxy.Status = service.StatusDisabled
	proxyRepo := newProxyRepositoryWithSQL(integrationEntClient, integrationDB, cache)
	require.NoError(t, proxyRepo.Update(ctx, proxy))

	require.Equal(t, int64(1), cache.rdb.Exists(ctx, schedulerAccountRetiredKey(id)).Val(), "proxy disable must establish the account retirement fence")
	_, err = cache.rdb.Get(ctx, schedulerAccountKey(id)).Bytes()
	require.ErrorIs(t, err, redis.Nil)
	metaBefore, err := cache.rdb.Get(ctx, schedulerAccountMetaKey(id)).Bytes()
	require.NoError(t, err)
	var metadata service.Account
	require.NoError(t, json.Unmarshal(metaBefore, &metadata))
	require.Equal(t, service.AccountTypeOAuth, metadata.Type, "the proxy invalidator must retain current protected metadata")

	close(releaseWriter)
	select {
	case err := <-writerDone:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatalf("stale writer did not complete: %v", ctx.Err())
	}

	_, err = cache.rdb.Get(ctx, schedulerAccountKey(id)).Bytes()
	require.ErrorIs(t, err, redis.Nil, "the delayed ordinary writer must not resurrect the full fixed-egress payload")
	metaAfter, err := cache.rdb.Get(ctx, schedulerAccountMetaKey(id)).Bytes()
	require.NoError(t, err)
	require.Equal(t, metaBefore, metaAfter, "the delayed ordinary writer must not overwrite protected metadata")
}
