//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

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
