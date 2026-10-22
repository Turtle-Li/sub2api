package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestModelTraceAccountTogglePreservesConfiguration(t *testing.T) {
	now := time.Now().UTC()
	cfg := &ModelTraceAccountConfig{LastRunAt: &now, Models: map[string]*ModelTraceModelConfig{
		"gpt-5.4": {Latest: &ModelTraceRecord{DetectedModel: "gpt-5.4"}, History: []ModelTraceRecord{{DetectedModel: "old"}}},
	}}
	repo := &modelTraceTestRepo{account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Schedulable: true, Extra: map[string]any{ModelTraceExtraKey: cfg}}}
	svc := NewModelTraceService(repo, &modelTraceTestSettings{}, nil, nil)
	t.Cleanup(svc.Stop)
	require.True(t, DecodeModelTraceConfig(repo.account.Extra).IsEnabled())
	for _, enabled := range []bool{false, true} {
		require.NoError(t, svc.UpdateAccountConfig(context.Background(), 1, UpdateAccountModelTraceRequest{Enabled: &enabled}))
		saved := DecodeModelTraceConfig(repo.updates)
		require.Equal(t, enabled, saved.IsEnabled())
		require.Equal(t, cfg.Models, saved.Models)
		require.Equal(t, cfg.LastRunAt, saved.LastRunAt)
		require.True(t, repo.account.Schedulable)
		repo.account.Extra = repo.updates
	}
}

func TestModelTracePausedAccountBlocksProbeAndSchedule(t *testing.T) {
	enabled := false
	cfg := &ModelTraceAccountConfig{Enabled: &enabled, Models: map[string]*ModelTraceModelConfig{"gpt-5.4": {}}}
	repo := &modelTraceTestRepo{account: &Account{ID: 1, Status: StatusActive, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{ModelTraceExtraKey: cfg}}}
	repo.accounts = []Account{*repo.account}
	svc := NewModelTraceService(repo, &modelTraceStoredSettings{raw: `{"enabled":true,"cron_expression":"* * * * *"}`}, nil, nil)
	t.Cleanup(svc.Stop)
	_, err := svc.SubmitProbe(context.Background(), 1, "gpt-5.4")
	require.ErrorContains(t, err, "paused")
	_, err = svc.ProbeModel(context.Background(), 1, "gpt-5.4")
	require.ErrorContains(t, err, "paused")
	svc.RunScheduledCycle(context.Background())
	require.Positive(t, repo.listCalls)
	svc.runAccountCycle(context.Background(), repo.account, cfg)
	require.Nil(t, repo.updates)
	items, err := svc.ListMonitors(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.False(t, items[0].Enabled)
	require.Contains(t, items[0].Models, "gpt-5.4")
	require.NoError(t, svc.UpdateAccountConfig(context.Background(), 1, UpdateAccountModelTraceRequest{Models: []string{"gpt-5.4", "gpt-6-sol"}}))
	require.False(t, DecodeModelTraceConfig(repo.updates).IsEnabled())
}

func TestModelTracePauseStopsFurtherSamples(t *testing.T) {
	enabled := false
	cfg := &ModelTraceAccountConfig{Models: map[string]*ModelTraceModelConfig{"gpt-5.4": {}}}
	repo := &modelTraceTestRepo{account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{ModelTraceExtraKey: cfg}}}
	repo.onReload = func() { cfg.Enabled = &enabled }
	svc := NewModelTraceService(repo, &modelTraceTestSettings{}, nil, nil)
	t.Cleanup(svc.Stop)
	record, err := svc.ProbeModel(context.Background(), 1, "gpt-5.4")
	require.NoError(t, err)
	require.Len(t, record.Requests, 1)
	require.Contains(t, record.Error, "paused")
	require.False(t, DecodeModelTraceConfig(repo.updates).IsEnabled())
}

func TestModelTraceQueuedProbeRechecksAccountSwitch(t *testing.T) {
	cfg := &ModelTraceAccountConfig{Models: map[string]*ModelTraceModelConfig{"gpt-5.4": {}}}
	repo := &modelTraceTestRepo{account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{ModelTraceExtraKey: cfg}}}
	svc := NewModelTraceService(repo, &modelTraceTestSettings{}, nil, nil)
	t.Cleanup(svc.Stop)
	unlock, err := svc.acquireAccountLock(context.Background(), 1)
	require.NoError(t, err)
	_, err = svc.SubmitProbe(context.Background(), 1, "gpt-5.4")
	require.NoError(t, err)
	enabled := false
	cfg.Enabled = &enabled
	unlock()
	require.Eventually(t, func() bool {
		svc.tasksMu.Lock()
		defer svc.tasksMu.Unlock()
		task := svc.tasks[modelTraceTaskKey{1, "gpt-5.4"}]
		return task.Status == "failed" && strings.Contains(task.Error, "account monitoring is paused")
	}, time.Second, time.Millisecond)
	require.Nil(t, repo.updates)
}

func TestModelTracePausedAccountRetainsLateResultWithoutActions(t *testing.T) {
	f := newModelTracePolicyFixture(t, ModelTraceGlobalConfig{NotifyOnMismatch: true, PauseOnMismatch: true})
	enabled := false
	require.NoError(t, f.svc.UpdateAccountConfig(context.Background(), 55, UpdateAccountModelTraceRequest{Enabled: &enabled}))
	record := f.save(t, "gpt-5.6-luna", time.Now())
	require.Equal(t, "mismatch", record.MatchStatus)
	require.Zero(t, f.repo.pauseCalls)
	require.Empty(t, f.sender.messages)
	saved := DecodeModelTraceConfig(f.repo.account.Extra)
	require.False(t, saved.IsEnabled())
	require.NotNil(t, saved.Models["public-gpt"].Latest)
}
