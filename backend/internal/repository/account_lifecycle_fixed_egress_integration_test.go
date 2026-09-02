//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func waitForCascadeDeleteParentLock(t *testing.T, ctx context.Context) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := integrationDB.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE pid <> pg_backend_pid()
				  AND wait_event_type = 'Lock'
				  AND query LIKE '%sub2api-cascade-delete-parent-lock%'
			)
		`).Scan(&waiting)
		require.NoError(t, err)
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cascade delete never reached the parent row lock")
}

func TestAccountDeleteSerializesConcurrentShadowCreation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	suffix := time.Now().UnixNano()
	proxy := fixedEgressRaceProxy(t, fmt.Sprintf("delete-shadow-race-proxy-%d", suffix))
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	parent := &service.Account{
		Name:        fmt.Sprintf("delete-shadow-race-parent-%d", suffix),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{},
		Extra:       map[string]any{},
		ProxyID:     &proxy.ID,
	}
	require.NoError(t, repo.Create(ctx, parent))
	shadow := &service.Account{
		Name:            fmt.Sprintf("delete-shadow-race-shadow-%d", suffix),
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeOAuth,
		Status:          service.StatusActive,
		Schedulable:     true,
		Credentials:     map[string]any{},
		Extra:           map[string]any{},
		ParentAccountID: &parent.ID,
		QuotaDimension:  service.QuotaDimensionSpark,
	}
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(),
			"DELETE FROM scheduler_outbox WHERE account_id = $1 OR account_id = $2", parent.ID, shadow.ID)
		if shadow.ID > 0 {
			_ = integrationEntClient.Account.DeleteOneID(shadow.ID).Exec(context.Background())
		}
		_ = integrationEntClient.Account.DeleteOneID(parent.ID).Exec(context.Background())
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(context.Background())
	})

	blocker, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	blockerOpen := true
	t.Cleanup(func() {
		if blockerOpen {
			_ = blocker.Rollback()
		}
	})
	var lockedParentID int64
	require.NoError(t, blocker.QueryRowContext(ctx,
		"SELECT id FROM accounts WHERE id = $1 FOR UPDATE", parent.ID).Scan(&lockedParentID))
	require.Equal(t, parent.ID, lockedParentID)

	deleteResult := make(chan error, 1)
	go func() { deleteResult <- repo.Delete(ctx, parent.ID) }()
	waitForCascadeDeleteParentLock(t, ctx)

	createResult := make(chan error, 1)
	go func() {
		createResult <- repo.CreateOpenAIOAuthShadow(ctx, parent.ID, &proxy.ID, shadow)
	}()
	select {
	case err := <-createResult:
		t.Fatalf("shadow creation bypassed the held parent lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	require.NoError(t, blocker.Commit())
	blockerOpen = false
	require.NoError(t, <-deleteResult)
	require.Error(t, <-createResult)

	var liveParents, liveShadows int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM accounts WHERE id = $1 AND deleted_at IS NULL", parent.ID).Scan(&liveParents))
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM accounts WHERE parent_account_id = $1 AND deleted_at IS NULL", parent.ID).Scan(&liveShadows))
	require.Zero(t, liveParents)
	require.Zero(t, liveShadows)
}

func TestAccountDeleteCascadesExistingShadowsAndInvalidatesBoth(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	proxy := fixedEgressRaceProxy(t, fmt.Sprintf("delete-shadow-cascade-proxy-%d", suffix))
	cache := &schedulerCacheRecorder{}
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, cache)
	parent := &service.Account{
		Name:        fmt.Sprintf("delete-shadow-cascade-parent-%d", suffix),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{},
		Extra:       map[string]any{},
		ProxyID:     &proxy.ID,
	}
	require.NoError(t, repo.Create(ctx, parent))
	shadow := &service.Account{
		Name:            fmt.Sprintf("delete-shadow-cascade-shadow-%d", suffix),
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeOAuth,
		Status:          service.StatusActive,
		Schedulable:     true,
		Credentials:     map[string]any{},
		Extra:           map[string]any{},
		ParentAccountID: &parent.ID,
		QuotaDimension:  service.QuotaDimensionSpark,
	}
	require.NoError(t, repo.CreateOpenAIOAuthShadow(ctx, parent.ID, &proxy.ID, shadow))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx,
			"DELETE FROM scheduler_outbox WHERE account_id = $1 OR account_id = $2", parent.ID, shadow.ID)
		_ = integrationEntClient.Account.DeleteOneID(shadow.ID).Exec(ctx)
		_ = integrationEntClient.Account.DeleteOneID(parent.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(ctx)
	})
	_, err := integrationDB.ExecContext(ctx,
		"DELETE FROM scheduler_outbox WHERE account_id = $1 OR account_id = $2", parent.ID, shadow.ID)
	require.NoError(t, err)

	require.NoError(t, repo.Delete(ctx, parent.ID))

	var liveAccounts int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM accounts
		WHERE id = ANY($1) AND deleted_at IS NULL
	`, pq.Array([]int64{parent.ID, shadow.ID})).Scan(&liveAccounts))
	require.Zero(t, liveAccounts)
	require.ElementsMatch(t, []int64{parent.ID, shadow.ID}, cache.deleteIDs)

	var outboxAccounts int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT account_id)
		FROM scheduler_outbox
		WHERE account_id = ANY($1)
	`, pq.Array([]int64{parent.ID, shadow.ID})).Scan(&outboxAccounts))
	require.Equal(t, 2, outboxAccounts)
}

func TestClearErrorValidatesReservedFixedEgressParent(t *testing.T) {
	for _, accountType := range []string{service.AccountTypeOAuth, service.AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			ctx := context.Background()
			suffix := time.Now().UnixNano()
			proxy := mustCreateProxy(t, integrationEntClient, &service.Proxy{
				Name:         fmt.Sprintf("clear-error-invalid-proxy-%s-%d", accountType, suffix),
				Protocol:     "http",
				Host:         "198.51.100.20",
				Port:         8080,
				Status:       service.StatusActive,
				FallbackMode: service.FallbackModeNone,
			})
			repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, &schedulerCacheRecorder{})
			account := &service.Account{
				Name:         fmt.Sprintf("clear-error-invalid-account-%s-%d", accountType, suffix),
				Platform:     service.PlatformOpenAI,
				Type:         accountType,
				Status:       service.StatusError,
				ErrorMessage: "reserved while proxy is invalid",
				Schedulable:  true,
				Credentials:  map[string]any{},
				Extra:        map[string]any{},
				ProxyID:      &proxy.ID,
			}
			require.NoError(t, repo.Create(ctx, account))
			t.Cleanup(func() {
				_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
				_ = integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx)
				_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(ctx)
			})

			err := repo.ClearError(ctx, account.ID)
			require.ErrorIs(t, err, service.ErrFixedEgressProxyInvalid)
			var status, errorMessage string
			require.NoError(t, integrationDB.QueryRowContext(ctx,
				"SELECT status, error_message FROM accounts WHERE id = $1", account.ID).
				Scan(&status, &errorMessage))
			require.Equal(t, service.StatusError, status)
			require.Equal(t, "reserved while proxy is invalid", errorMessage)
		})
	}
}

func TestClearErrorActivatesCompliantFixedEgressParent(t *testing.T) {
	for _, accountType := range []string{service.AccountTypeOAuth, service.AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			ctx := context.Background()
			suffix := time.Now().UnixNano()
			proxy := fixedEgressRaceProxy(t, fmt.Sprintf("clear-error-valid-proxy-%s-%d", accountType, suffix))
			cache := &schedulerCacheRecorder{}
			repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, cache)
			account := &service.Account{
				Name:         fmt.Sprintf("clear-error-valid-account-%s-%d", accountType, suffix),
				Platform:     service.PlatformOpenAI,
				Type:         accountType,
				Status:       service.StatusError,
				ErrorMessage: "temporary error",
				Schedulable:  true,
				Credentials:  map[string]any{},
				Extra:        map[string]any{},
				ProxyID:      &proxy.ID,
			}
			require.NoError(t, repo.Create(ctx, account))
			t.Cleanup(func() {
				_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
				_ = integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx)
				_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(ctx)
			})

			require.NoError(t, repo.ClearError(ctx, account.ID))
			var status, errorMessage string
			require.NoError(t, integrationDB.QueryRowContext(ctx,
				"SELECT status, error_message FROM accounts WHERE id = $1", account.ID).
				Scan(&status, &errorMessage))
			require.Equal(t, service.StatusActive, status)
			require.Empty(t, errorMessage)
		})
	}
}

func TestClearErrorWaitsForProxyMutationAndRejectsDisabledEgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	suffix := time.Now().UnixNano()
	proxy := fixedEgressRaceProxy(t, fmt.Sprintf("clear-error-lock-proxy-%d", suffix))
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	account := &service.Account{
		Name:         fmt.Sprintf("clear-error-lock-account-%d", suffix),
		Platform:     service.PlatformOpenAI,
		Type:         service.AccountTypeOAuth,
		Status:       service.StatusError,
		ErrorMessage: "temporary error",
		Schedulable:  true,
		Credentials:  map[string]any{},
		Extra:        map[string]any{},
		ProxyID:      &proxy.ID,
	}
	require.NoError(t, repo.Create(ctx, account))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_ = integrationEntClient.Account.DeleteOneID(account.ID).Exec(context.Background())
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(context.Background())
	})

	mutation, err := integrationDB.BeginTx(ctx, &sql.TxOptions{})
	require.NoError(t, err)
	mutationOpen := true
	t.Cleanup(func() {
		if mutationOpen {
			_ = mutation.Rollback()
		}
	})
	var lockedProxyID int64
	require.NoError(t, mutation.QueryRowContext(ctx,
		"SELECT id FROM proxies WHERE id = $1 FOR NO KEY UPDATE", proxy.ID).Scan(&lockedProxyID))

	result := make(chan error, 1)
	go func() { result <- repo.ClearError(ctx, account.ID) }()
	select {
	case err := <-result:
		t.Fatalf("ClearError bypassed the held proxy identity lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	_, err = mutation.ExecContext(ctx, "UPDATE proxies SET status = $1 WHERE id = $2", service.StatusDisabled, proxy.ID)
	require.NoError(t, err)
	require.NoError(t, mutation.Commit())
	mutationOpen = false
	require.ErrorIs(t, <-result, service.ErrFixedEgressProxyInvalid)

	var status string
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT status FROM accounts WHERE id = $1", account.ID).Scan(&status))
	require.Equal(t, service.StatusError, status)
}
