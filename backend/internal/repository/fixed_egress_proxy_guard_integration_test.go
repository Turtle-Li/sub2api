//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type fixedEgressRaceResult struct {
	op  string
	err error
}

func runFixedEgressRace(t *testing.T, leftName string, left func(context.Context) error, rightName string, right func(context.Context) error) []fixedEgressRaceResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	results := make(chan fixedEgressRaceResult, 2)
	run := func(name string, fn func(context.Context) error) {
		ready <- struct{}{}
		<-start
		results <- fixedEgressRaceResult{op: name, err: fn(ctx)}
	}
	go run(leftName, left)
	go run(rightName, right)
	<-ready
	<-ready
	close(start)

	out := make([]fixedEgressRaceResult, 0, 2)
	for len(out) < 2 {
		select {
		case result := <-results:
			out = append(out, result)
		case <-ctx.Done():
			t.Fatalf("fixed-egress race did not complete: %v", ctx.Err())
		}
	}
	return out
}

func fixedEgressRaceProxy(t *testing.T, name string) *service.Proxy {
	t.Helper()
	return mustCreateProxy(t, integrationEntClient, &service.Proxy{
		Name:         name,
		Protocol:     "socks5h",
		Host:         "100.80.10.114",
		Port:         1080,
		Status:       service.StatusActive,
		FallbackMode: service.FallbackModeNone,
	})
}

func assertExactlyOneFixedEgressRaceSuccess(t *testing.T, results []fixedEgressRaceResult) fixedEgressRaceResult {
	t.Helper()
	var success *fixedEgressRaceResult
	for i := range results {
		if results[i].err == nil {
			require.Nil(t, success, "both raced operations committed: %#v", results)
			success = &results[i]
		}
	}
	require.NotNil(t, success, "neither raced operation committed: %#v", results)
	return *success
}

func TestFixedEgressProxyCASAndIdentityMutationSerialize(t *testing.T) {
	ctx := context.Background()
	proxy := fixedEgressRaceProxy(t, "fixed-egress-cas-race")
	parent := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:     "fixed-egress-cas-race-parent",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Status:   service.StatusActive,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", parent.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE payload @> jsonb_build_object('account_ids', jsonb_build_array($1::bigint))", parent.ID)
		_ = integrationEntClient.Account.DeleteOneID(parent.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(ctx)
	})

	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	proxyRepo := newProxyRepositoryWithSQL(integrationEntClient, integrationDB)
	mutated := *proxy
	mutated.Host = "proxy.example"

	results := runFixedEgressRace(t,
		"cas", func(ctx context.Context) error {
			_, err := accountRepo.CompareAndSwapOpenAIOAuthProxy(ctx, []int64{parent.ID}, 0, proxy)
			return err
		},
		"proxy-update", func(ctx context.Context) error {
			return proxyRepo.Update(ctx, &mutated)
		},
	)
	success := assertExactlyOneFixedEgressRaceSuccess(t, results)

	var (
		boundProxyID sql.NullInt64
		host         string
	)
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT proxy_id FROM accounts WHERE id = $1", parent.ID).Scan(&boundProxyID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT host FROM proxies WHERE id = $1", proxy.ID).Scan(&host))
	if success.op == "cas" {
		require.True(t, boundProxyID.Valid)
		require.Equal(t, proxy.ID, boundProxyID.Int64)
		require.Equal(t, "100.80.10.114", host)
	} else {
		require.False(t, boundProxyID.Valid)
		require.Equal(t, "proxy.example", host)
	}
	for _, result := range results {
		if result.op != success.op {
			require.Error(t, result.err)
			require.Contains(t, []string{"FIXED_EGRESS_PROXY_IDENTITY_IMMUTABLE", "ACCOUNT_PROXY_COMPARE_AND_SET_FAILED", "FIXED_EGRESS_PROXY_INVALID"}, errors.Reason(result.err))
		}
	}
}

func TestFixedEgressProxyCreateAndIdentityMutationSerialize(t *testing.T) {
	ctx := context.Background()
	proxy := fixedEgressRaceProxy(t, "fixed-egress-create-race")
	account := &service.Account{
		Name:        "fixed-egress-create-race-parent",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		ProxyID:     &proxy.ID,
	}
	t.Cleanup(func() {
		if account.ID > 0 {
			_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
			_ = integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx)
		}
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(ctx)
	})

	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	proxyRepo := newProxyRepositoryWithSQL(integrationEntClient, integrationDB)
	mutated := *proxy
	mutated.Host = "proxy.example"

	results := runFixedEgressRace(t,
		"create", func(ctx context.Context) error { return accountRepo.Create(ctx, account) },
		"proxy-update", func(ctx context.Context) error { return proxyRepo.Update(ctx, &mutated) },
	)
	success := assertExactlyOneFixedEgressRaceSuccess(t, results)

	var host string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT host FROM proxies WHERE id = $1", proxy.ID).Scan(&host))
	if success.op == "create" {
		require.Greater(t, account.ID, int64(0))
		require.Equal(t, "100.80.10.114", host)
	} else {
		require.Zero(t, account.ID)
		require.Equal(t, "proxy.example", host)
	}
	for _, result := range results {
		if result.op != success.op {
			require.Error(t, result.err)
			require.Contains(t, []string{"FIXED_EGRESS_PROXY_IDENTITY_IMMUTABLE", "FIXED_EGRESS_PROXY_INVALID"}, errors.Reason(result.err))
		}
	}
}

func TestFixedEgressDisabledParentCreationWaitsForProxyIdentityLock(t *testing.T) {
	for _, accountType := range []string{service.AccountTypeOAuth, service.AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			ctx := context.Background()
			proxy := fixedEgressRaceProxy(t, "fixed-egress-disabled-create-lock-"+accountType)
			account := &service.Account{
				Name:        "fixed-egress-disabled-create-lock-parent-" + accountType,
				Platform:    service.PlatformOpenAI,
				Type:        accountType,
				Status:      service.StatusDisabled,
				Schedulable: false,
				ProxyID:     &proxy.ID,
			}
			t.Cleanup(func() {
				if account.ID > 0 {
					_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
					_ = integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx)
				}
				_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(ctx)
			})

			lockTx, err := integrationDB.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = lockTx.Rollback() }()
			var lockedID int64
			require.NoError(t, lockTx.QueryRowContext(ctx,
				"SELECT id FROM proxies WHERE id = $1 FOR NO KEY UPDATE", proxy.ID).Scan(&lockedID))
			require.Equal(t, proxy.ID, lockedID)

			createCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			defer cancel()
			repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
			startedAt := time.Now()
			err = repo.Create(createCtx, account)

			require.Error(t, err, "creation must wait for the proxy identity lock")
			require.GreaterOrEqual(t, time.Since(startedAt), 400*time.Millisecond,
				"creation returned before waiting on the held proxy identity lock")
			require.Contains(t, err.Error(), "cancel")
			require.Zero(t, account.ID)
		})
	}
}

func TestFixedEgressProxyCreateWithAccountGroupsAndIdentityMutationSerialize(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	proxy := fixedEgressRaceProxy(t, fmt.Sprintf("fixed-egress-create-groups-race-%d", suffix))
	group := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name:     fmt.Sprintf("fixed-egress-create-groups-race-%d", suffix),
		Platform: service.PlatformOpenAI,
	})
	account := &service.Account{
		Name:        fmt.Sprintf("fixed-egress-create-groups-race-parent-%d", suffix),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{},
		Extra:       map[string]any{},
		ProxyID:     &proxy.ID,
	}
	t.Cleanup(func() {
		if account.ID > 0 {
			_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
			_, _ = integrationDB.ExecContext(ctx, "DELETE FROM account_groups WHERE account_id = $1", account.ID)
			_ = integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx)
		}
		_ = integrationEntClient.Group.DeleteOneID(group.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(ctx)
	})

	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	proxyRepo := newProxyRepositoryWithSQL(integrationEntClient, integrationDB)
	mutated := *proxy
	mutated.Host = "proxy.example"

	results := runFixedEgressRace(t,
		"create-with-account-groups", func(ctx context.Context) error {
			return accountRepo.CreateWithAccountGroups(ctx, account, []service.AccountGroup{{GroupID: group.ID, Priority: 37}})
		},
		"proxy-update", func(ctx context.Context) error {
			return proxyRepo.Update(ctx, &mutated)
		},
	)
	success := assertExactlyOneFixedEgressRaceSuccess(t, results)

	var host string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT host FROM proxies WHERE id = $1", proxy.ID).Scan(&host))
	if success.op == "create-with-account-groups" {
		require.Greater(t, account.ID, int64(0))
		require.Equal(t, "100.80.10.114", host)

		var (
			boundProxyID sql.NullInt64
			priority     int
			outboxCount  int
		)
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT proxy_id FROM accounts WHERE id = $1", account.ID).Scan(&boundProxyID))
		require.True(t, boundProxyID.Valid)
		require.Equal(t, proxy.ID, boundProxyID.Int64)
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT priority FROM account_groups WHERE account_id = $1 AND group_id = $2", account.ID, group.ID).Scan(&priority))
		require.Equal(t, 37, priority)
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1", account.ID).Scan(&outboxCount))
		require.Equal(t, 1, outboxCount)
	} else {
		require.Zero(t, account.ID)
		require.Equal(t, "proxy.example", host)

		var accountCount, groupBindingCount, outboxCount int
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM accounts WHERE name = $1", account.Name).Scan(&accountCount))
		require.Zero(t, accountCount)
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM account_groups ag JOIN accounts a ON a.id = ag.account_id WHERE a.name = $1", account.Name).Scan(&groupBindingCount))
		require.Zero(t, groupBindingCount)
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id IN (SELECT id FROM accounts WHERE name = $1)", account.Name).Scan(&outboxCount))
		require.Zero(t, outboxCount)
	}

	for _, result := range results {
		if result.op != success.op {
			require.Error(t, result.err)
			require.Contains(t, []string{"FIXED_EGRESS_PROXY_IDENTITY_IMMUTABLE", "FIXED_EGRESS_PROXY_INVALID"}, errors.Reason(result.err))
		}
	}
}

func TestFixedEgressBulkProxyMutationAndAccountConversionSerialize(t *testing.T) {
	ctx := context.Background()
	fixedProxy := fixedEgressRaceProxy(t, "fixed-egress-bulk-conversion-fixed")
	unsafeProxy := mustCreateProxy(t, integrationEntClient, &service.Proxy{
		Name:         "fixed-egress-bulk-conversion-unsafe",
		Protocol:     "http",
		Host:         "198.51.100.10",
		Port:         8080,
		Status:       service.StatusActive,
		FallbackMode: service.FallbackModeNone,
	})
	account := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:     "fixed-egress-bulk-conversion-account",
		Platform: service.PlatformAnthropic,
		Type:     service.AccountTypeAPIKey,
		Status:   service.StatusActive,
		ProxyID:  &fixedProxy.ID,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE payload @> jsonb_build_object('account_ids', jsonb_build_array($1::bigint))", account.ID)
		_ = integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(unsafeProxy.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(fixedProxy.ID).Exec(ctx)
	})

	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	converted := *account
	converted.Platform = service.PlatformOpenAI
	converted.Type = service.AccountTypeOAuth
	converted.Status = service.StatusActive
	converted.ParentAccountID = nil
	converted.ProxyID = &fixedProxy.ID

	results := runFixedEgressRace(t,
		"bulk-proxy", func(ctx context.Context) error {
			_, err := accountRepo.BulkUpdate(ctx, []int64{account.ID}, service.AccountBulkUpdate{ProxyID: &unsafeProxy.ID})
			return err
		},
		"convert-openai-oauth", func(ctx context.Context) error {
			return accountRepo.Update(ctx, &converted)
		},
	)
	for _, result := range results {
		if result.op == "convert-openai-oauth" {
			require.NoError(t, result.err)
			continue
		}
		if result.err != nil {
			require.Equal(t, "FIXED_EGRESS_CAS_REQUIRED", errors.Reason(result.err))
		}
	}

	var (
		platform    string
		accountType string
		proxyID     sql.NullInt64
	)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT platform, type, proxy_id
		FROM accounts
		WHERE id = $1`, account.ID).Scan(&platform, &accountType, &proxyID))
	require.Equal(t, service.PlatformOpenAI, platform)
	require.Equal(t, service.AccountTypeOAuth, accountType)
	require.True(t, proxyID.Valid)
	require.Equal(t, fixedProxy.ID, proxyID.Int64)
}

func TestFixedEgressShadowCreateAndCASNeverPersistStaleParentProxy(t *testing.T) {
	ctx := context.Background()
	firstProxy := fixedEgressRaceProxy(t, "fixed-egress-shadow-cas-first")
	secondProxy := fixedEgressRaceProxy(t, "fixed-egress-shadow-cas-second")
	parent := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:     "fixed-egress-shadow-cas-parent",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Status:   service.StatusActive,
		ProxyID:  &firstProxy.ID,
	})
	shadow := &service.Account{
		Name:            "fixed-egress-shadow-cas-shadow",
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeOAuth,
		Status:          service.StatusActive,
		Credentials:     map[string]any{"model_mapping": map[string]any{}},
		Extra:           map[string]any{},
		ParentAccountID: &parent.ID,
		QuotaDimension:  service.QuotaDimensionSpark,
	}
	t.Cleanup(func() {
		if shadow.ID > 0 {
			_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", shadow.ID)
			_ = integrationEntClient.Account.DeleteOneID(shadow.ID).Exec(ctx)
		}
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", parent.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE payload @> jsonb_build_object('account_ids', jsonb_build_array($1::bigint))", parent.ID)
		_ = integrationEntClient.Account.DeleteOneID(parent.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(secondProxy.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(firstProxy.ID).Exec(ctx)
	})

	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	results := runFixedEgressRace(t,
		"create-shadow", func(ctx context.Context) error {
			return accountRepo.CreateOpenAIOAuthShadow(ctx, parent.ID, &firstProxy.ID, shadow)
		},
		"cas", func(ctx context.Context) error {
			_, err := accountRepo.CompareAndSwapOpenAIOAuthProxy(ctx, []int64{parent.ID}, firstProxy.ID, secondProxy)
			return err
		},
	)
	for _, result := range results {
		if result.op == "cas" {
			require.NoError(t, result.err)
		} else if result.err != nil {
			require.ErrorIs(t, result.err, service.ErrAccountProxyCASConflict)
		}
	}

	var parentProxyID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT proxy_id FROM accounts WHERE id = $1", parent.ID).Scan(&parentProxyID))
	require.Equal(t, secondProxy.ID, parentProxyID)
	if shadow.ID == 0 {
		var shadowCount int
		require.NoError(t, integrationDB.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM accounts
			WHERE parent_account_id = $1 AND quota_dimension = $2 AND deleted_at IS NULL`, parent.ID, service.QuotaDimensionSpark).Scan(&shadowCount))
		require.Zero(t, shadowCount)
		return
	}
	var shadowProxyID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT proxy_id FROM accounts WHERE id = $1", shadow.ID).Scan(&shadowProxyID))
	require.Equal(t, secondProxy.ID, shadowProxyID)
}

func TestFixedEgressProxyDeleteAndCASSerialize(t *testing.T) {
	ctx := context.Background()
	proxy := fixedEgressRaceProxy(t, "fixed-egress-delete-cas-race")
	parent := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:     "fixed-egress-delete-cas-parent",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Status:   service.StatusActive,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", parent.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE payload @> jsonb_build_object('account_ids', jsonb_build_array($1::bigint))", parent.ID)
		_ = integrationEntClient.Account.DeleteOneID(parent.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(ctx)
	})

	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	proxyRepo := newProxyRepositoryWithSQL(integrationEntClient, integrationDB)
	results := runFixedEgressRace(t,
		"cas", func(ctx context.Context) error {
			_, err := accountRepo.CompareAndSwapOpenAIOAuthProxy(ctx, []int64{parent.ID}, 0, proxy)
			return err
		},
		"delete", func(ctx context.Context) error { return proxyRepo.Delete(ctx, proxy.ID) },
	)
	success := assertExactlyOneFixedEgressRaceSuccess(t, results)

	var parentProxyID sql.NullInt64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT proxy_id FROM accounts WHERE id = $1", parent.ID).Scan(&parentProxyID))
	if success.op == "cas" {
		require.True(t, parentProxyID.Valid)
		require.Equal(t, proxy.ID, parentProxyID.Int64)
		_, err := proxyRepo.GetByID(ctx, proxy.ID)
		require.NoError(t, err)
	} else {
		require.False(t, parentProxyID.Valid)
		_, err := proxyRepo.GetByID(ctx, proxy.ID)
		require.ErrorIs(t, err, service.ErrProxyNotFound)
	}
	for _, result := range results {
		if result.op != success.op {
			require.Error(t, result.err)
			require.Contains(t, []string{"PROXY_IN_USE", "FIXED_EGRESS_PROXY_INVALID"}, errors.Reason(result.err))
		}
	}
}

func TestProxyDeleteRejectsFallbackOriginReference(t *testing.T) {
	ctx := context.Background()
	origin := fixedEgressRaceProxy(t, "proxy-delete-origin-reference")
	backup := fixedEgressRaceProxy(t, "proxy-delete-origin-backup")
	account := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:     "proxy-delete-origin-account",
		Platform: service.PlatformAnthropic,
		Type:     service.AccountTypeAPIKey,
		Status:   service.StatusActive,
		ProxyID:  &backup.ID,
	})
	t.Cleanup(func() {
		_ = integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(backup.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(origin.ID).Exec(ctx)
	})
	require.NoError(t, integrationEntClient.Account.UpdateOneID(account.ID).SetProxyFallbackOriginID(origin.ID).Exec(ctx))

	proxyRepo := newProxyRepositoryWithSQL(integrationEntClient, integrationDB)
	count, err := proxyRepo.CountAccountsByProxyID(ctx, origin.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	err = proxyRepo.Delete(ctx, origin.ID)

	require.ErrorIs(t, err, service.ErrProxyInUse)
}

func TestFixedEgressProxyStatusChangeEvictsParentAndShadowSchedulerCache(t *testing.T) {
	ctx := context.Background()
	proxy := fixedEgressRaceProxy(t, "fixed-egress-status-cache-eviction")
	parent := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:        "fixed-egress-status-cache-parent",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeSetupToken,
		Status:      service.StatusActive,
		Schedulable: true,
		ProxyID:     &proxy.ID,
	})
	shadow := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:            "fixed-egress-status-cache-shadow",
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeSetupToken,
		Status:          service.StatusActive,
		Schedulable:     true,
		ProxyID:         &proxy.ID,
		ParentAccountID: &parent.ID,
		QuotaDimension:  service.QuotaDimensionSpark,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE payload @> jsonb_build_object('account_ids', jsonb_build_array($1::bigint))", parent.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE payload @> jsonb_build_object('account_ids', jsonb_build_array($1::bigint))", shadow.ID)
		_ = integrationEntClient.Account.DeleteOneID(shadow.ID).Exec(ctx)
		_ = integrationEntClient.Account.DeleteOneID(parent.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(ctx)
	})

	cache := &schedulerCacheRecorder{accounts: map[int64]*service.Account{
		parent.ID: parent,
		shadow.ID: shadow,
	}}
	repo := newProxyRepositoryWithSQL(integrationEntClient, integrationDB, cache)
	updated := *proxy
	updated.Status = service.StatusDisabled

	require.NoError(t, repo.Update(ctx, &updated))
	require.ElementsMatch(t, []int64{parent.ID, shadow.ID}, cache.deleteIDs)
	require.NotContains(t, cache.accounts, parent.ID)
	require.NotContains(t, cache.accounts, shadow.ID)

	var outboxCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM scheduler_outbox
		WHERE event_type = $1
		  AND payload @> jsonb_build_object('account_ids', jsonb_build_array($2::bigint))
		  AND payload @> jsonb_build_object('account_ids', jsonb_build_array($3::bigint))
	`, service.SchedulerOutboxEventAccountBulkChanged, parent.ID, shadow.ID).Scan(&outboxCount))
	require.Equal(t, 1, outboxCount)
}

func TestFixedEgressProxyExpiryOwnTransactionEvictsParentAndShadowSchedulerCache(t *testing.T) {
	ctx := context.Background()
	past := time.Now().Add(-time.Hour)
	proxy := mustCreateProxy(t, integrationEntClient, &service.Proxy{
		Name:         fmt.Sprintf("fixed-egress-expiry-cache-eviction-%d", time.Now().UnixNano()),
		Protocol:     "socks5h",
		Host:         "100.80.10.114",
		Port:         1080,
		Status:       service.StatusActive,
		FallbackMode: service.FallbackModeNone,
		ExpiresAt:    &past,
	})
	require.NoError(t, integrationEntClient.Proxy.UpdateOneID(proxy.ID).
		SetExpiresAt(past).
		SetFallbackMode(service.FallbackModeNone).
		Exec(ctx))
	parent := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:        fmt.Sprintf("fixed-egress-expiry-cache-parent-%d", time.Now().UnixNano()),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		ProxyID:     &proxy.ID,
	})
	shadow := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:            fmt.Sprintf("fixed-egress-expiry-cache-shadow-%d", time.Now().UnixNano()),
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeOAuth,
		Status:          service.StatusActive,
		Schedulable:     true,
		ProxyID:         &proxy.ID,
		ParentAccountID: &parent.ID,
		QuotaDimension:  service.QuotaDimensionSpark,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id IN ($1, $2)", parent.ID, shadow.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE payload @> jsonb_build_object('account_ids', jsonb_build_array($1::bigint))", parent.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE payload @> jsonb_build_object('account_ids', jsonb_build_array($1::bigint))", shadow.ID)
		_ = integrationEntClient.Account.DeleteOneID(shadow.ID).Exec(ctx)
		_ = integrationEntClient.Account.DeleteOneID(parent.ID).Exec(ctx)
		_ = integrationEntClient.Proxy.DeleteOneID(proxy.ID).Exec(ctx)
	})

	cache := &schedulerCacheRecorder{accounts: map[int64]*service.Account{
		parent.ID: parent,
		shadow.ID: shadow,
	}}
	repo := newProxyRepositoryWithSQL(integrationEntClient, integrationDB, cache)

	affectedIDs, err := repo.sweepOneExpiredProxy(ctx, proxy.ID, nil, false, time.Now())

	require.NoError(t, err)
	require.ElementsMatch(t, []int64{parent.ID, shadow.ID}, affectedIDs)
	require.ElementsMatch(t, []int64{parent.ID, shadow.ID}, cache.deleteIDs)
	require.NotContains(t, cache.accounts, parent.ID)
	require.NotContains(t, cache.accounts, shadow.ID)
	got, err := repo.GetByID(ctx, proxy.ID)
	require.NoError(t, err)
	require.Equal(t, service.StatusExpired, got.Status)
}
