//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3QuotaSnapshotIsBoundedAndContainsNoCredentials(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	group := mustCreateGroup(t, client, &service.Group{Name: "quota-" + uuid.NewString(), Platform: service.PlatformAnthropic})
	private := mustCreateGroup(t, client, &service.Group{Name: "quota-private-" + uuid.NewString(), Platform: service.PlatformAnthropic, IsExclusive: true})
	a := mustCreateAccount(t, client, &service.Account{Name: "quota-" + uuid.NewString(), Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth, Credentials: map[string]any{"access_token": "private-test-token"}, Extra: map[string]any{"unrelated_private": "hidden"}})
	mustBindAccountToGroup(t, client, a.ID, group.ID, 1)
	mustBindAccountToGroup(t, client, a.ID, private.ID, 1)
	reset := time.Now().UTC().Add(time.Hour)
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET session_window_end=$2,extra=extra || '{"session_window_utilization":1,"passive_usage_7d_utilization":0.5}'::jsonb WHERE id=$1`, a.ID, reset)
	require.NoError(t, err)
	repo := NewChannelMonitorV3Repository(integrationDB).(*channelMonitorV3Repository)
	rows, err := repo.GroupQuotaAccounts(ctx, []int64{group.ID, private.ID})
	require.NoError(t, err)
	require.Len(t, rows[group.ID], 1)
	require.Empty(t, rows[private.ID])
	got := rows[group.ID][0]
	require.Empty(t, got.Credentials)
	require.Empty(t, got.Name)
	require.Zero(t, got.ID)
	require.NotContains(t, got.Extra, "unrelated_private")
	require.Equal(t, 1.0, got.Extra["session_window_utilization"])
	require.WithinDuration(t, reset, *got.SessionWindowEnd, time.Microsecond)
	_, err = repo.GroupQuotaAccounts(ctx, nil)
	require.NoError(t, err)
}
