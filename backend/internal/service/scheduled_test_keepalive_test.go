//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type keepAlivePlanRepoStub struct {
	ScheduledTestPlanRepository
	created  bool
	plans    []*ScheduledTestPlan
	claimed  bool
	claims   int
	advanced int
	updates  int
	success  bool
	nextRun  time.Time
}

func (r *keepAlivePlanRepoStub) Create(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	r.created = true
	return plan, nil
}

func (r *keepAlivePlanRepoStub) ListByAccountID(context.Context, int64) ([]*ScheduledTestPlan, error) {
	return r.plans, nil
}

func (r *keepAlivePlanRepoStub) Update(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	return plan, nil
}

func (r *keepAlivePlanRepoStub) ClaimIdleRun(_ context.Context, _ *ScheduledTestPlan, _ time.Time, next time.Time) (bool, error) {
	r.claims++
	r.nextRun = next
	return r.claimed, nil
}

func (r *keepAlivePlanRepoStub) AdvanceNextRun(context.Context, int64, *time.Time, time.Time) error {
	r.advanced++
	return nil
}

func (r *keepAlivePlanRepoStub) UpdateAfterRun(_ context.Context, _ *ScheduledTestPlan, _ time.Time, next time.Time, success bool) error {
	r.updates++
	r.success = success
	r.nextRun = next
	return nil
}

func TestScheduledTestService_KeepAliveValidationAndLegacyMode(t *testing.T) {
	ctx := context.Background()
	existing := &ScheduledTestPlan{ID: 1, Enabled: true, IdleHours: 48}
	repo := &keepAlivePlanRepoStub{plans: []*ScheduledTestPlan{existing}}
	svc := NewScheduledTestService(repo, nil)
	plan := &ScheduledTestPlan{AccountID: 1, ModelID: " ", CronExpression: "0 * * * *", Enabled: true, IdleHours: 48}
	_, err := svc.CreatePlan(ctx, plan)
	require.ErrorContains(t, err, "explicit model")
	plan.ModelID = "small-model"
	_, err = svc.CreatePlan(ctx, plan)
	require.ErrorContains(t, err, "only one enabled")
	plan.Enabled = false
	created, err := svc.CreatePlan(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, 48, created.IdleHours)
	require.NotNil(t, created.NextRunAt)
	plan.ID, plan.Enabled = 1, true
	_, err = svc.UpdatePlan(ctx, plan)
	require.NoError(t, err, "editing the same plan must be allowed")
	plan.IdleHours, plan.ModelID = 0, ""
	_, err = svc.CreatePlan(ctx, plan)
	require.NoError(t, err, "legacy tests keep their default-model behavior")
}

func TestScheduledKeepAliveReady(t *testing.T) {
	now := time.Now().UTC()
	ptr := func(value time.Time) *time.Time { return &value }
	for _, tc := range []struct {
		name   string
		mutate func(*Account, *ScheduledTestPlan)
		ready  bool
	}{
		{"idle beyond threshold", func(*Account, *ScheduledTestPlan) {}, true},
		{"exact threshold", func(a *Account, _ *ScheduledTestPlan) { a.CreatedAt = now.Add(-48 * time.Hour) }, true},
		{"new account", func(a *Account, _ *ScheduledTestPlan) { a.CreatedAt = now.Add(-24 * time.Hour) }, false},
		{"recent business use", func(a *Account, _ *ScheduledTestPlan) { a.LastUsedAt = ptr(now.Add(-time.Hour)) }, false},
		{"recent scheduled success", func(_ *Account, p *ScheduledTestPlan) { p.LastSuccessAt = ptr(now.Add(-time.Hour)) }, false},
		{"another plan succeeded", func(_ *Account, p *ScheduledTestPlan) { p.LastActivityAt = ptr(now.Add(-time.Hour)) }, false},
		{"failed attempt cooling down", func(_ *Account, p *ScheduledTestPlan) { p.LastRunAt = ptr(now.Add(-5 * time.Hour)) }, false},
		{"cooldown elapsed", func(_ *Account, p *ScheduledTestPlan) { p.LastRunAt = ptr(now.Add(-6 * time.Hour)) }, true},
		{"disabled account", func(a *Account, _ *ScheduledTestPlan) { a.Schedulable = false }, false},
		{"error account", func(a *Account, _ *ScheduledTestPlan) { a.Status = "error" }, false},
		{"expired account", func(a *Account, _ *ScheduledTestPlan) {
			a.AutoPauseOnExpired = true
			a.ExpiresAt = ptr(now.Add(-time.Hour))
		}, false},
		{"upstream cooldown", func(a *Account, _ *ScheduledTestPlan) { a.RateLimitResetAt = ptr(now.Add(time.Hour)) }, false},
		{"daily calls exhausted", func(a *Account, _ *ScheduledTestPlan) {
			a.Extra = map[string]any{
				"quota_daily_request_limit": 1, "quota_daily_request_used": 1,
				"quota_daily_request_start": now.Format(time.RFC3339), "quota_daily_request_reset_at": now.Add(time.Hour).Format(time.RFC3339),
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Account{ID: 1, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, CreatedAt: now.Add(-72 * time.Hour)}
			p := &ScheduledTestPlan{IdleHours: 48}
			tc.mutate(a, p)
			require.Equal(t, tc.ready, scheduledKeepAliveReady(a, p, now))
		})
	}
}

type keepAliveReader struct{ account *Account }

func (r keepAliveReader) GetByID(context.Context, int64) (*Account, error) { return r.account, nil }

type keepAliveSlots struct {
	busy     bool
	acquired int
	released int
	maximum  int
}

func (s *keepAliveSlots) AcquireAccountSlot(_ context.Context, _ int64, maximum int) (*AcquireResult, error) {
	s.acquired++
	s.maximum = maximum
	return &AcquireResult{Acquired: !s.busy, ReleaseFunc: func() { s.released++ }}, nil
}

type keepAliveTester struct {
	status   string
	err      error
	calls    int
	deadline time.Duration
}

func (s *keepAliveTester) RunTestBackground(ctx context.Context, _ int64, _ string) (*ScheduledTestResult, error) {
	s.calls++
	if deadline, ok := ctx.Deadline(); ok {
		s.deadline = time.Until(deadline)
	}
	return &ScheduledTestResult{Status: s.status}, s.err
}

type keepAliveResults struct {
	ScheduledTestResultRepository
	saved  int
	stored *ScheduledTestResult
}

func (r *keepAliveResults) Create(_ context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	r.saved++
	r.stored = result
	return result, nil
}

func (*keepAliveResults) PruneOldResults(context.Context, int64, int) error { return nil }

func TestScheduledTestRunner_KeepAliveAdmissionAndOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		busy   bool
		recent bool
		lost   bool
		err    error
	}{
		{name: "success", status: "success"},
		{name: "upstream failure", status: "failed"},
		{name: "ambiguous error", err: errors.New("deadline exceeded")},
		{name: "busy account", busy: true},
		{name: "recent normal use", recent: true},
		{name: "another runner or config edit wins claim", lost: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			plan := &ScheduledTestPlan{ID: 1, AccountID: 1, ModelID: "small-model", IdleHours: 48, CronExpression: "* * * * *"}
			account := &Account{ID: 1, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 2, CreatedAt: now.Add(-72 * time.Hour)}
			if tc.recent {
				account.LastUsedAt = &now
			}
			repo := &keepAlivePlanRepoStub{claimed: !tc.lost}
			results := &keepAliveResults{}
			tester := &keepAliveTester{status: tc.status, err: tc.err}
			slots := &keepAliveSlots{busy: tc.busy}
			runner := &ScheduledTestRunnerService{
				planRepo: repo, scheduledSvc: NewScheduledTestService(repo, results),
				accountTestSvc: tester, accountRepo: keepAliveReader{account}, accountSlots: slots,
			}
			runner.runOnePlan(context.Background(), plan)
			if tc.busy || tc.recent {
				require.Zero(t, tester.calls)
				require.Equal(t, 1, repo.advanced)
				require.Zero(t, repo.claims)
			} else {
				require.Equal(t, 1, repo.claims)
				require.Equal(t, 2, slots.maximum)
				require.Equal(t, 1, slots.released)
				if tc.lost {
					require.Zero(t, tester.calls)
				} else {
					require.Equal(t, 1, tester.calls, "never retry an outbound attempt")
					require.InDelta(t, 90, tester.deadline.Seconds(), 2)
					require.False(t, repo.nextRun.Before(now.Add(6*time.Hour)), "cooldown must survive crashes")
				}
			}
			completed := !tc.busy && !tc.recent && !tc.lost
			require.Equal(t, completed, repo.updates == 1)
			require.Equal(t, completed, results.saved == 1)
			require.Equal(t, completed && tc.status == "success", repo.success)
		})
	}
}

func TestScheduledTestRunner_LegacyPlanNeedsNoIdleAdmission(t *testing.T) {
	repo, results := &keepAlivePlanRepoStub{}, &keepAliveResults{}
	tester := &keepAliveTester{status: "success"}
	runner := &ScheduledTestRunnerService{planRepo: repo, scheduledSvc: NewScheduledTestService(repo, results), accountTestSvc: tester}
	runner.runOnePlan(context.Background(), &ScheduledTestPlan{ID: 1, CronExpression: "0 * * * *"})
	require.Equal(t, 1, tester.calls)
	require.Zero(t, repo.claims)
	require.True(t, repo.success)
}

func TestScheduledTestRunner_KeepAliveUsesNativeAccountProbe(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			account := &Account{
				ID: 501, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
				Schedulable: true, CreatedAt: time.Now().Add(-72 * time.Hour), Concurrency: 1,
				Credentials: map[string]any{"api_key": "test-key", "base_url": "https://relay.example.com/v1", "api_protocol": APIProtocolResponses},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
			}
			accountRepo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
			body := "data: {\"type\":\"response.completed\"}\n\n"
			if status != http.StatusOK {
				body = `{"error":{"message":"upstream rejected probe"}}`
			}
			upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(status, body)}}
			probe := &AccountTestService{accountRepo: accountRepo, httpUpstream: upstream, cfg: &config.Config{}}
			plans, results, slots := &keepAlivePlanRepoStub{claimed: true}, &keepAliveResults{}, &keepAliveSlots{}
			runner := &ScheduledTestRunnerService{
				planRepo: plans, scheduledSvc: NewScheduledTestService(plans, results),
				accountRepo: accountRepo, accountTestSvc: probe, accountSlots: slots,
			}
			runner.runOnePlan(context.Background(), &ScheduledTestPlan{
				ID: 1, AccountID: account.ID, ModelID: "low-cost-model",
				IdleHours: 48, CronExpression: "0 * * * *", MaxResults: 50,
			})
			require.Len(t, upstream.requests, 1)
			payload, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			require.Equal(t, "low-cost-model", gjson.GetBytes(payload, "model").String())
			require.False(t, gjson.GetBytes(payload, "instructions").Exists())
			require.Equal(t, 1, slots.released)
			require.Equal(t, status == http.StatusOK, plans.success)
			require.NotNil(t, results.stored)
			if status == http.StatusOK {
				require.Equal(t, "success", results.stored.Status)
			} else {
				require.Equal(t, "failed", results.stored.Status)
				require.Contains(t, results.stored.ErrorMessage, "403")
			}
		})
	}
}

func TestScheduledTestService_RejectsInvalidKeepAliveThreshold(t *testing.T) {
	for _, hours := range []int{-1, 8761} {
		repo := &keepAlivePlanRepoStub{}
		svc := NewScheduledTestService(repo, nil)
		_, err := svc.CreatePlan(context.Background(), &ScheduledTestPlan{
			AccountID: 1, ModelID: "test-model", CronExpression: "0 * * * *", IdleHours: hours,
		})
		require.Error(t, err)
		require.False(t, repo.created, "invalid thresholds must not be persisted")
	}
}
