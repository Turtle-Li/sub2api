//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModelTraceMonitorsPreserveStoredSchedulingState(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	want := make(map[int64]bool)
	for _, schedulable := range []bool{true, false} {
		account, err := tx.Client().Account.Create().
			SetName("modeltrace-scheduling-fixture").
			SetPlatform(service.PlatformOpenAI).
			SetType(service.AccountTypeAPIKey).
			SetCredentials(map[string]any{}).
			SetExtra(map[string]any{service.ModelTraceExtraKey: map[string]any{
				"models": map[string]any{"gpt-6.1-sol": map[string]any{}},
			}}).
			SetSchedulable(schedulable).
			Save(ctx)
		require.NoError(t, err)
		want[account.ID] = schedulable
	}

	monitors, err := service.NewModelTraceService(repo, nil, nil, nil).ListMonitors(ctx)
	require.NoError(t, err)
	got := make(map[int64]bool, len(monitors))
	for _, monitor := range monitors {
		got[monitor.AccountID] = monitor.Schedulable
	}
	require.Equal(t, want, got)
}
