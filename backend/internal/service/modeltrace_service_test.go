package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type modelTraceTestRepo struct {
	AccountRepository
	account   *Account
	reads     int
	onReload  func()
	updates   map[string]any
	updateErr error
	accounts  []Account
	listCalls int
}

func (r *modelTraceTestRepo) List(_ context.Context, params pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error) {
	r.listCalls++
	start := params.Offset()
	if start >= len(r.accounts) {
		return nil, nil, nil
	}
	end := min(start+params.Limit(), len(r.accounts))
	return r.accounts[start:end], &pagination.PaginationResult{Pages: (len(r.accounts) + params.Limit() - 1) / params.Limit()}, nil
}

func (r *modelTraceTestRepo) GetByID(context.Context, int64) (*Account, error) {
	r.reads++
	if r.reads == 2 && r.onReload != nil {
		r.onReload()
	}
	copy := *r.account
	return &copy, nil
}

func (r *modelTraceTestRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updates = updates
	return r.updateErr
}

type modelTraceTestSettings struct{ SettingRepository }

func (*modelTraceTestSettings) GetValue(context.Context, string) (string, error) {
	return `{"enabled":true,"probe_count":1,"interval_seconds":0,"reasoning_effort":"low","cron_expression":"*/30 * * * *"}`, nil
}

func TestModelTraceProbePreservesConcurrentConfig(t *testing.T) {
	for _, removed := range []bool{false, true} {
		t.Run(map[bool]string{false: "edited", true: "removed"}[removed], func(t *testing.T) {
			cfg := &ModelTraceAccountConfig{Models: map[string]*ModelTraceModelConfig{"gpt-5.4": {}}}
			repo := &modelTraceTestRepo{account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ID: 1, Extra: map[string]any{ModelTraceExtraKey: cfg, "other": "old"}}}
			repo.onReload = func() {
				cfg.Models["other"] = &ModelTraceModelConfig{}
				repo.account.Extra["other"] = "new"
				if removed {
					delete(cfg.Models, "gpt-5.4")
				}
			}
			svc := NewModelTraceService(repo, &modelTraceTestSettings{}, nil, nil)
			_, err := svc.ProbeModel(context.Background(), 1, "gpt-5.4")
			require.NoError(t, err)
			if removed {
				require.Nil(t, repo.updates)
				return
			}
			require.Len(t, repo.updates, 1)
			saved, ok := repo.updates[ModelTraceExtraKey].(*ModelTraceAccountConfig)
			require.True(t, ok)
			require.Contains(t, saved.Models, "other")
			require.NotNil(t, saved.Models["gpt-5.4"].Latest)
			require.NotEmpty(t, saved.Models["gpt-5.4"].Latest.Requests)
			require.NotEmpty(t, saved.Models["gpt-5.4"].Latest.Requests[0].Error)
		})
	}
}

func TestModelTraceProbeReportsSaveFailure(t *testing.T) {
	repo := &modelTraceTestRepo{account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ID: 1, Extra: map[string]any{ModelTraceExtraKey: &ModelTraceAccountConfig{Models: map[string]*ModelTraceModelConfig{"gpt-5.4": {}}}}}, updateErr: errors.New("write failed")}
	svc := NewModelTraceService(repo, &modelTraceTestSettings{}, nil, nil)
	_, err := svc.ProbeModel(context.Background(), 1, "gpt-5.4")
	require.ErrorContains(t, err, "write failed")
}

func TestModelTraceSchedules(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	now := time.Date(2026, 10, 12, 14, 30, 0, 0, loc)
	require.True(t, modelTraceScheduleDue("*/30 * * * *", now))
	require.False(t, modelTraceScheduleDue("0 * * * *", now))
	require.True(t, modelTraceScheduleDue("0 */2 * * *", now.Add(-30*time.Minute)))
	require.False(t, modelTraceScheduleDue("invalid", now))
	svc := NewModelTraceService(&modelTraceTestRepo{account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ID: 1}}, &modelTraceTestSettings{}, nil, nil)
	require.ErrorContains(t, svc.UpdateGlobalConfig(context.Background(), ModelTraceGlobalConfig{CronExpression: "invalid"}), "invalid cron")
}

type modelTraceStoredSettings struct {
	SettingRepository
	raw string
	err error
}

func (s *modelTraceStoredSettings) GetValue(context.Context, string) (string, error) {
	return s.raw, s.err
}

func TestModelTraceDefaultDisabled(t *testing.T) {
	for _, settings := range []SettingRepository{
		nil,
		&modelTraceStoredSettings{},
		&modelTraceStoredSettings{raw: "invalid"},
		&modelTraceStoredSettings{err: errors.New("unavailable")},
		&modelTraceStoredSettings{raw: `{"probe_count":3}`},
	} {
		repo := &modelTraceTestRepo{}
		svc := NewModelTraceService(repo, settings, nil, nil)
		require.False(t, svc.GetGlobalConfig(context.Background()).Enabled)
		svc.RunScheduledCycle(context.Background())
		require.Zero(t, repo.listCalls)
	}
	svc := NewModelTraceService(nil, &modelTraceStoredSettings{raw: `{"enabled":true}`}, nil, nil)
	require.True(t, svc.GetGlobalConfig(context.Background()).Enabled)
}

func TestModelTraceListsOnlyAddedAccountsAcrossPages(t *testing.T) {
	accounts := make([]Account, 1002)
	for i := range accounts {
		accounts[i].ID = int64(i + 1)
	}
	accounts[1].Extra = map[string]any{ModelTraceExtraKey: &ModelTraceAccountConfig{Models: map[string]*ModelTraceModelConfig{"gpt-5.4": {}}}}
	accounts[1001].Extra = map[string]any{ModelTraceExtraKey: &ModelTraceAccountConfig{Models: map[string]*ModelTraceModelConfig{"last": {}}}}
	accounts[2].Extra = map[string]any{ModelTraceExtraKey: nil}
	repo := &modelTraceTestRepo{accounts: accounts}
	svc := NewModelTraceService(repo, nil, nil, nil)
	items, err := svc.ListMonitors(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, int64(1002), items[1].AccountID)
	require.Equal(t, 2, repo.listCalls)
}

func TestModelTraceRemovingMonitorDoesNotRecreateIt(t *testing.T) {
	cfg := &ModelTraceAccountConfig{Models: map[string]*ModelTraceModelConfig{"gpt-5.4": {}}}
	repo := &modelTraceTestRepo{account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ID: 1, Extra: map[string]any{ModelTraceExtraKey: cfg}}}
	svc := NewModelTraceService(repo, &modelTraceTestSettings{}, nil, nil)
	repo.onReload = func() { repo.account.Extra[ModelTraceExtraKey] = nil }
	_, err := svc.ProbeModel(context.Background(), 1, "gpt-5.4")
	require.NoError(t, err)
	require.Nil(t, repo.updates)
	_, err = svc.ProbeModel(context.Background(), 1, "gpt-5.4")
	require.ErrorContains(t, err, "not configured")
	require.NoError(t, svc.RemoveAccountMonitor(context.Background(), 1))
	require.Equal(t, map[string]any{ModelTraceExtraKey: nil}, repo.updates)
}

func TestModelTraceAccountLockWaitCanBeCanceled(t *testing.T) {
	svc := NewModelTraceService(nil, nil, nil, nil)
	unlock, err := svc.acquireAccountLock(context.Background(), 31)
	require.NoError(t, err)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, probeErr := svc.ProbeModel(ctx, 31, "gpt-5.4")
		done <- probeErr
	}()
	select {
	case probeErr := <-done:
		require.ErrorIs(t, probeErr, context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("probe continued waiting on the account lock after cancellation")
	}
	// An unrelated account does not wait for account 31.
	otherUnlock, err := svc.acquireAccountLock(context.Background(), 44)
	require.NoError(t, err)
	otherUnlock()
}

type modelTraceCancelSettings struct {
	modelTraceTestSettings
	cancel context.CancelFunc
}

func (s *modelTraceCancelSettings) GetValue(ctx context.Context, key string) (string, error) {
	s.cancel()
	return s.modelTraceTestSettings.GetValue(ctx, key)
}

type modelTraceContextRepo struct {
	modelTraceTestRepo
	readDeadlines []time.Time
}

func (r *modelTraceContextRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	r.readDeadlines = append(r.readDeadlines, deadline)
	return r.modelTraceTestRepo.GetByID(ctx, id)
}

func (r *modelTraceContextRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.modelTraceTestRepo.UpdateExtra(ctx, id, updates); err != nil {
		return err
	}
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	return nil
}

func TestModelTraceCanceledProbeSavesFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := &ModelTraceAccountConfig{Models: map[string]*ModelTraceModelConfig{"gpt-5.4": {}}}
	repo := &modelTraceContextRepo{modelTraceTestRepo: modelTraceTestRepo{account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ID: 31, Extra: map[string]any{ModelTraceExtraKey: cfg}}}}
	svc := NewModelTraceService(repo, &modelTraceCancelSettings{cancel: cancel}, nil, nil)
	record, err := svc.ProbeModel(ctx, 31, "gpt-5.4")
	require.NoError(t, err)
	require.Equal(t, context.Canceled.Error(), record.Error)
	require.Equal(t, "Unknown", record.DetectedModel)
	require.NotNil(t, repo.updates)
	saved := DecodeModelTraceConfig(repo.account.Extra).Models["gpt-5.4"].Latest
	require.NotNil(t, saved)
	require.Equal(t, record.Error, saved.Error)
	require.True(t, record.TestedAt.Equal(saved.TestedAt))
}

func TestModelTraceScheduledModelsHaveIndependentBoundedDeadlines(t *testing.T) {
	cfg := &ModelTraceAccountConfig{Models: map[string]*ModelTraceModelConfig{"gpt-5.4": {}, "gpt-5.6-sol": {}}}
	repo := &modelTraceContextRepo{modelTraceTestRepo: modelTraceTestRepo{account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ID: 31, Status: StatusActive, Extra: map[string]any{ModelTraceExtraKey: cfg}}}}
	svc := NewModelTraceService(repo, &modelTraceTestSettings{}, nil, nil)
	svc.runAccountCycle(context.Background(), repo.account, cfg)
	saved := DecodeModelTraceConfig(repo.account.Extra)
	require.NotNil(t, saved.Models["gpt-5.4"].Latest)
	require.NotNil(t, saved.Models["gpt-5.6-sol"].Latest)
	require.Len(t, repo.readDeadlines, 12)
	for _, start := range []int{0, 6} {
		require.True(t, repo.readDeadlines[start].IsZero(), "account cycle must not impose a shared model deadline")
		require.WithinDuration(t, time.Now().Add(modelTraceTimeout), repo.readDeadlines[start+1], time.Second, "each model probe must have its own bounded deadline")
		for _, offset := range []int{2, 3, 4} {
			require.Equal(t, repo.readDeadlines[start+1], repo.readDeadlines[start+offset], "switch rechecks must retain the model deadline")
		}
		require.WithinDuration(t, time.Now().Add(5*time.Second), repo.readDeadlines[start+5], time.Second)
	}
}

func TestModelTraceSubmittedTaskSurvivesRequestCancellation(t *testing.T) {
	cfg := &ModelTraceAccountConfig{Models: map[string]*ModelTraceModelConfig{"gpt-5.4": {}}}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ID: 31, Extra: map[string]any{ModelTraceExtraKey: cfg}}
	repo := &modelTraceContextRepo{modelTraceTestRepo: modelTraceTestRepo{account: account, accounts: []Account{*account}}}
	svc := NewModelTraceService(repo, &modelTraceTestSettings{}, nil, nil)
	defer svc.Stop()
	unlock, err := svc.acquireAccountLock(context.Background(), 31)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	task, err := svc.SubmitProbe(ctx, 31, "gpt-5.4")
	require.NoError(t, err)
	require.Equal(t, "queued", task.Status)
	duplicate, err := svc.SubmitProbe(ctx, 31, "gpt-5.4")
	require.NoError(t, err)
	require.Equal(t, task.ID, duplicate.ID)
	items, err := svc.ListMonitors(ctx)
	require.NoError(t, err)
	require.Equal(t, task.ID, items[0].Models["gpt-5.4"].Task.ID)
	cancel() // Closing the browser must not cancel the accepted task.
	unlock()
	var finished ModelTraceTask
	require.Eventually(t, func() bool {
		svc.tasksMu.Lock()
		defer svc.tasksMu.Unlock()
		finished = svc.tasks[modelTraceTaskKey{31, "gpt-5.4"}]
		return finished.Status == "failed" || finished.Status == "completed"
	}, time.Second, time.Millisecond)
	// The test intentionally has no upstream service; its failure is not HTTP cancellation.
	require.Equal(t, "failed", finished.Status)
	require.NotEqual(t, context.Canceled.Error(), finished.Error)
	require.NotNil(t, repo.updates)
	require.NotNil(t, DecodeModelTraceConfig(repo.account.Extra).Models["gpt-5.4"].Latest)
}

// A stale account editor must neither restore a removed monitor nor roll back
// probe history written after the editor loaded the account.
func TestUpdateAccountKeepsServerOwnedModelTraceState(t *testing.T) {
	for _, stored := range []any{nil, map[string]any{"models": map[string]any{"gpt-5.4": map[string]any{}}}} {
		account := &Account{ID: 212, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
			Credentials: map[string]any{"api_key": "sk-1"}, Extra: map[string]any{}}
		if stored != nil {
			account.Extra[ModelTraceExtraKey] = stored
		}
		repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
		svc := &adminServiceImpl{accountRepo: repo}
		updated, err := svc.UpdateAccount(context.Background(), account.ID, &UpdateAccountInput{
			Extra: map[string]any{ModelTraceExtraKey: map[string]any{"models": map[string]any{"stale": map[string]any{}}}, "custom": "value"},
		})
		require.NoError(t, err)
		require.Equal(t, stored, updated.Extra[ModelTraceExtraKey])
		require.Equal(t, "value", updated.Extra["custom"])
	}
	_, discarded := duplicateAccountDiscardedExtraKeys[ModelTraceExtraKey]
	require.True(t, discarded)
}
