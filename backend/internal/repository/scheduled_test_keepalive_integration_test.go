//go:build integration

package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func keepAliveFixture(t *testing.T) (*scheduledTestPlanRepository, *service.ScheduledTestPlan, time.Time) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	a := mustCreateAccount(t, testEntClient(t), &service.Account{
		Name: "keepalive-" + uuid.NewString(), Type: service.AccountTypeAPIKey,
		Extra: map[string]any{"quota_daily_request_limit": 5, "quota_daily_used": 2},
	})
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET created_at = $2 WHERE id = $1`, a.ID, now.Add(-96*time.Hour))
	require.NoError(t, err)
	repo := &scheduledTestPlanRepository{db: integrationDB}
	due := now.Add(-time.Minute)
	plan, err := repo.Create(ctx, &service.ScheduledTestPlan{
		AccountID: a.ID, ModelID: "small-model",
		CronExpression: "0 * * * *", Enabled: true, MaxResults: 10, IdleHours: 48, NextRunAt: &due,
	})
	require.NoError(t, err)
	return repo, plan, now
}

func TestScheduledKeepAlive_StorageActivityAndUniqueness(t *testing.T) {
	ctx := context.Background()
	repo, plan, now := keepAliveFixture(t)
	require.Equal(t, 48, plan.IdleHours)
	require.WithinDuration(t, now.Add(-96*time.Hour), *plan.LastActivityAt, time.Microsecond)
	due, err := repo.ListDue(ctx, now)
	require.NoError(t, err)
	dueIDs := make([]int64, 0, len(due))
	for _, candidate := range due {
		dueIDs = append(dueIDs, candidate.ID)
	}
	require.Contains(t, dueIDs, plan.ID)
	_, err = repo.Create(ctx, &service.ScheduledTestPlan{
		AccountID: plan.AccountID, IdleHours: 24,
		ModelID: "another-model", Enabled: true, MaxResults: 10,
	})
	require.Error(t, err, "database must fence concurrent enabled keep-alive creation")
	ordinary, err := repo.Create(ctx, &service.ScheduledTestPlan{
		AccountID: plan.AccountID, Enabled: true,
		ModelID: "ordinary-model", CronExpression: "0 * * * *", MaxResults: 10,
	})
	require.NoError(t, err)
	require.Zero(t, ordinary.IdleHours, "legacy plans remain unconditional")
	ordinarySuccess := now.Add(-12 * time.Hour)
	require.NoError(t, repo.UpdateAfterRun(ctx, ordinary, ordinarySuccess, now.Add(time.Hour), true))
	loaded, err := repo.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.WithinDuration(t, ordinarySuccess, *loaded.LastActivityAt, time.Microsecond)
	claimed, err := repo.ClaimIdleRun(ctx, plan, now, now.Add(6*time.Hour))
	require.NoError(t, err)
	require.False(t, claimed, "another model's successful test makes this account active")
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET last_used_at = $2 WHERE id = $1`, plan.AccountID, now.Add(-time.Hour))
	require.NoError(t, err)
	loaded, err = repo.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.WithinDuration(t, now.Add(-time.Hour), *loaded.LastActivityAt, time.Microsecond)
	loaded.IdleHours, loaded.Enabled = 24, false
	updated, err := repo.Update(ctx, loaded)
	require.NoError(t, err)
	require.Equal(t, 24, updated.IdleHours)
	require.False(t, updated.Enabled)
	plans, err := repo.ListByAccountID(ctx, plan.AccountID)
	require.NoError(t, err)
	require.Len(t, plans, 2)
	var used int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT COALESCE((extra->>'quota_daily_request_used')::int, 0) FROM accounts WHERE id=$1`, plan.AccountID).Scan(&used))
	require.Zero(t, used, "ordinary scheduled tests keep their previous quota semantics")
}

func TestScheduledKeepAlive_ClaimIsExclusiveDurableAndFenced(t *testing.T) {
	ctx := context.Background()
	repo, plan, now := keepAliveFixture(t)
	const workers = 8
	claims, failures := make(chan bool, workers), make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := repo.ClaimIdleRun(ctx, plan, now, now.Add(6*time.Hour))
			claims <- claimed
			failures <- err
		}()
	}
	wg.Wait()
	close(claims)
	close(failures)
	count := 0
	for claimed := range claims {
		if claimed {
			count++
		}
	}
	for err := range failures {
		require.NoError(t, err)
	}
	require.Equal(t, 1, count)
	loaded, err := repo.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.WithinDuration(t, now, *loaded.LastRunAt, time.Microsecond)
	require.NoError(t, repo.AdvanceNextRun(ctx, plan.ID, plan.NextRunAt, now.Add(time.Minute)))
	loaded, err = repo.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.WithinDuration(t, now.Add(6*time.Hour), *loaded.NextRunAt, time.Microsecond,
		"a stale skip cannot overwrite a persisted attempt")
	loaded.NextRunAt = &now
	loaded, err = repo.Update(ctx, loaded)
	require.NoError(t, err)
	claimed, err := repo.ClaimIdleRun(ctx, loaded, now.Add(5*time.Hour), now.Add(11*time.Hour))
	require.NoError(t, err)
	require.False(t, claimed, "editing the next check cannot bypass an unconfirmed attempt's cooldown")
	claimed, err = repo.ClaimIdleRun(ctx, loaded, now.Add(6*time.Hour), now.Add(12*time.Hour))
	require.NoError(t, err)
	require.True(t, claimed)

	for _, change := range []string{"edit", "disable", "normal use", "account paused"} {
		t.Run(change, func(t *testing.T) {
			r, stale, at := keepAliveFixture(t)
			switch change {
			case "edit":
				current := *stale
				current.IdleHours = 24
				_, err = r.Update(ctx, &current)
			case "disable":
				_, err = integrationDB.ExecContext(ctx, `UPDATE scheduled_test_plans SET enabled=false WHERE id=$1`, stale.ID)
			case "normal use":
				_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET last_used_at=$2 WHERE id=$1`, stale.AccountID, at)
			case "account paused":
				_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET schedulable=false WHERE id=$1`, stale.AccountID)
			}
			require.NoError(t, err)
			claimed, err := r.ClaimIdleRun(ctx, stale, at, at.Add(6*time.Hour))
			require.NoError(t, err)
			require.False(t, claimed, "recheck current data before the network request")
		})
	}
}

func TestScheduledKeepAlive_SuccessDeduplicatesQuotaAndSurvivesPruning(t *testing.T) {
	ctx := context.Background()
	repo, plan, now := keepAliveFixture(t)
	next := now.Add(6 * time.Hour)
	claimed, err := repo.ClaimIdleRun(ctx, plan, now, next)
	require.NoError(t, err)
	require.True(t, claimed)
	plan.UpdatedAt = now
	for i := 0; i < 2; i++ {
		require.NoError(t, repo.UpdateAfterRun(ctx, plan, now, next, true))
	}
	results := NewScheduledTestResultRepository(integrationDB)
	_, err = results.Create(ctx, &service.ScheduledTestResult{PlanID: plan.ID, Status: "success", StartedAt: now, FinishedAt: now})
	require.NoError(t, err)
	require.NoError(t, results.PruneOldResults(ctx, plan.ID, 0))
	loaded, err := repo.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.WithinDuration(t, now, *loaded.LastSuccessAt, time.Microsecond)
	require.WithinDuration(t, now, *loaded.LastActivityAt, time.Microsecond)
	var used, moneyUsed int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT (extra->>'quota_daily_request_used')::int, (extra->>'quota_daily_used')::int FROM accounts WHERE id=$1`,
		plan.AccountID).Scan(&used, &moneyUsed))
	require.Equal(t, 1, used, "a completion replay cannot count the upstream call twice")
	require.Equal(t, 2, moneyUsed, "synthetic tests do not debit account monetary usage")
	failedAt := now.Add(6 * time.Hour)
	require.NoError(t, repo.UpdateAfterRun(ctx, loaded, failedAt, failedAt.Add(6*time.Hour), false))
	loaded, err = repo.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.WithinDuration(t, now, *loaded.LastSuccessAt, time.Microsecond, "failure is not activity")
}

func TestScheduledKeepAlive_EditDuringRunPreservesScheduleAndCountsActualCall(t *testing.T) {
	ctx := context.Background()
	repo, executed, now := keepAliveFixture(t)
	claimed, err := repo.ClaimIdleRun(ctx, executed, now, now.Add(6*time.Hour))
	require.NoError(t, err)
	require.True(t, claimed)
	executed.UpdatedAt = now
	current, err := repo.GetByID(ctx, executed.ID)
	require.NoError(t, err)
	ownerNext := now.Add(24 * time.Hour)
	current.IdleHours, current.Enabled, current.NextRunAt = 0, false, &ownerNext
	_, err = repo.Update(ctx, current)
	require.NoError(t, err)
	require.NoError(t, repo.UpdateAfterRun(ctx, executed, now, now.Add(6*time.Hour), true))
	loaded, err := repo.GetByID(ctx, executed.ID)
	require.NoError(t, err)
	require.False(t, loaded.Enabled)
	require.Zero(t, loaded.IdleHours)
	require.WithinDuration(t, ownerNext, *loaded.NextRunAt, time.Microsecond)
	var used int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT (extra->>'quota_daily_request_used')::int FROM accounts WHERE id=$1`, executed.AccountID).Scan(&used))
	require.Equal(t, 1, used, "an edit after sending cannot erase the real keep-alive call")
}
