package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/stretchr/testify/require"
)

type accountErrorAlertRepo struct {
	targetedAlertTestRepo
	events []*OpsAlertEvent
}

func (r *accountErrorAlertRepo) GetLatestAlertEvent(context.Context, int64) (*OpsAlertEvent, error) {
	if len(r.events) == 0 {
		return nil, nil
	}
	return r.events[len(r.events)-1], nil
}

func (r *accountErrorAlertRepo) CreateAlertEvent(ctx context.Context, event *OpsAlertEvent) (*OpsAlertEvent, error) {
	if event.SupersedesEventID > 0 {
		if err := r.UpdateAlertEventStatus(ctx, event.SupersedesEventID, OpsAlertStatusResolved, &event.FiredAt); err != nil {
			return nil, err
		}
	}
	event.ID = int64(len(r.events) + 1)
	r.events = append(r.events, event)
	r.event = event
	return event, nil
}

func (r *accountErrorAlertRepo) UpdateAlertEventStatus(_ context.Context, id int64, status string, resolvedAt *time.Time) error {
	event := r.events[id-1]
	event.Status, event.ResolvedAt = status, resolvedAt
	r.event = nil
	return nil
}

func newAccountErrorAlertEvaluator(metric string, accounts map[int64]*AccountAvailability) (*OpsAlertEvaluatorService, *accountErrorAlertRepo) {
	rule := &OpsAlertRule{ID: 7, Name: "Account errors", Enabled: true, Severity: "P1", MetricType: metric,
		Operator: ">=", Threshold: 1, SustainedMinutes: 1, NotifyFeishu: true,
		Filters: map[string]any{"platform": PlatformOpenAI, "group_id": int64(12), "region": "test"}}
	repo := &accountErrorAlertRepo{targetedAlertTestRepo: targetedAlertTestRepo{rule: rule}}
	ops := &OpsService{opsRepo: repo, getAccountAvailability: func(context.Context, string, *int64) (*OpsAccountAvailability, error) {
		return &OpsAccountAvailability{Accounts: accounts}, nil
	}}
	return NewOpsAlertEvaluatorService(ops, repo, nil, nil, nil, nil, &targetedAlertTestSender{}), repo
}

func TestOpsAlertAccountErrorEscalationLifecycle(t *testing.T) {
	t.Setenv(runtimegate.StateFileEnv, "")
	for _, metric := range []string{"account_error_count", "account_error_ratio"} {
		t.Run(metric, func(t *testing.T) {
			accounts := map[int64]*AccountAvailability{
				6:  {AccountID: 6, AccountName: "Earlier account", HasError: true},
				99: {AccountID: 99, AccountName: "New account"},
			}
			evaluator, repo := newAccountErrorAlertEvaluator(metric, accounts)
			repo.rule.CooldownMinutes = 10

			evaluator.evaluateOnce(time.Minute)
			require.Len(t, repo.events, 1)
			first := repo.events[0]
			require.Equal(t, OpsAlertStatusFiring, first.Status)
			require.Equal(t, []int64{6}, first.Dimensions["matched_account_ids"])
			require.Equal(t, int64(6), first.Dimensions["latest_account_id"])
			require.Equal(t, "Earlier account", first.Dimensions["latest_account_name"])

			encoded, err := json.Marshal(first.Dimensions)
			require.NoError(t, err)
			first.Dimensions = nil
			require.NoError(t, json.Unmarshal(encoded, &first.Dimensions))
			require.Contains(t, buildOpsAlertFeishuText(repo.rule, first), "Earlier account (#6)")

			evaluator.evaluateOnce(time.Minute)
			require.Len(t, repo.events, 1)
			accounts[99].HasError = true
			evaluator.evaluateOnce(time.Minute)
			require.Len(t, repo.events, 1, "cooldown must not replace the firing event")
			require.Equal(t, OpsAlertStatusFiring, first.Status)

			first.FiredAt = first.FiredAt.Add(-11 * time.Minute)
			repo.silenced = true
			evaluator.evaluateOnce(time.Minute)
			require.Len(t, repo.events, 1, "scoped silence must not replace the firing event")
			require.Equal(t, OpsAlertStatusFiring, first.Status)

			repo.silenced = false
			evaluator.evaluateOnce(time.Minute)
			require.Len(t, repo.events, 2)
			second := repo.events[1]
			require.Equal(t, OpsAlertStatusResolved, first.Status)
			require.Equal(t, second.FiredAt, *first.ResolvedAt)
			require.Equal(t, OpsAlertStatusFiring, second.Status)
			require.Equal(t, []int64{6, 99}, second.Dimensions["matched_account_ids"])
			require.Equal(t, int64(99), second.Dimensions["latest_account_id"])
			require.Equal(t, "New account", second.Dimensions["latest_account_name"])
			require.Equal(t, []OpsAlertDeliveryTarget{{Channel: OpsAlertDeliveryFeishu, Target: OpsAlertFeishuTarget}}, second.DeliveryPlan)

			repo.rule.CooldownMinutes = 0
			evaluator.evaluateOnce(time.Minute)
			require.Len(t, repo.events, 2, "an unchanged set must not refire")
			accounts[99].HasError = false
			evaluator.evaluateOnce(time.Minute)
			require.Len(t, repo.events, 2, "a recovery must not refire")
			require.Equal(t, OpsAlertStatusFiring, second.Status)
			accounts[6].HasError = false
			evaluator.evaluateOnce(time.Minute)
			require.Len(t, repo.events, 2)
			require.Equal(t, OpsAlertStatusResolved, second.Status)
			require.Nil(t, repo.event)
		})
	}
}

func TestOpsAlertAccountErrorEscalationLegacyCount(t *testing.T) {
	t.Setenv(runtimegate.StateFileEnv, "")
	accounts := map[int64]*AccountAvailability{6: {HasError: true}, 99: {AccountName: "New account"}}
	evaluator, repo := newAccountErrorAlertEvaluator("account_error_count", accounts)
	legacy := &OpsAlertEvent{ID: 1, RuleID: repo.rule.ID, Status: OpsAlertStatusFiring, FiredAt: time.Now(), MetricValue: float64Ptr(1)}
	repo.event, repo.events = legacy, []*OpsAlertEvent{legacy}

	evaluator.evaluateOnce(time.Minute)
	require.Len(t, repo.events, 1)
	accounts[99].HasError = true
	evaluator.evaluateOnce(time.Minute)
	require.Len(t, repo.events, 2)
	require.Equal(t, OpsAlertStatusResolved, legacy.Status)
	require.Equal(t, []int64{6, 99}, repo.event.Dimensions["matched_account_ids"])
	require.Equal(t, int64(99), repo.event.Dimensions["latest_account_id"])
}

func TestOpsAlertAccountErrorEscalationUsesHighestNewID(t *testing.T) {
	accounts := map[int64]*AccountAvailability{
		6: {HasError: true}, 40: {HasError: true, AccountName: "Highest new"}, 99: {HasError: true},
	}
	metric := computeScopedAccountStatusMetric("account_error_ratio", accounts, nil, time.Now())
	require.Equal(t, int64(99), metric.latestErrorAccountID)
	for _, previousIDs := range []any{
		[]int{99}, []int64{99}, []any{json.Number("99")}, []any{float64(99)}, []any{int64(99)},
	} {
		active := &OpsAlertEvent{Dimensions: map[string]any{"matched_account_ids": previousIDs}}
		id, escalate := metric.accountErrorEscalation("account_error_ratio", active)
		require.True(t, escalate, "%T", previousIDs)
		require.Equal(t, int64(40), id, "%T", previousIDs)
		updated := metric
		updated.latestErrorAccountID = id
		require.Equal(t, "Highest new", updated.dimensions("", nil, nil)["latest_account_name"])
	}
	_, escalate := metric.accountErrorEscalation("account_error_ratio", &OpsAlertEvent{MetricValue: float64Ptr(50)})
	require.False(t, escalate, "legacy ratio can increase without a newly errored account")
}

func TestComputeScopedAccountErrorMetricPreservesPredicateAndScope(t *testing.T) {
	now := time.Now()
	expired := now.Add(-time.Minute)
	accounts := map[int64]*AccountAvailability{
		6: {HasError: true, AccountName: "Earlier"}, 99: {HasError: true, AccountName: "Latest"},
		100: {HasError: true, TempUnschedulableUntil: &expired}, 101: {HasError: true}, 102: nil,
	}
	scope := []int64{100, 99, 6, 6, 102, 103}
	for rule, want := range map[string]float64{"account_error_count": 2, "account_error_ratio": 200.0 / 3} {
		metric := computeScopedAccountStatusMetric(rule, accounts, scope, now)
		require.InDelta(t, want, metric.value, 1e-9, rule)
		ids := metric.dimensions("", nil, nil)
		require.Equal(t, []int64{6, 99}, ids["matched_account_ids"])
		require.Equal(t, int64(99), ids["latest_account_id"])
		require.Equal(t, "Latest", ids["latest_account_name"])
	}
}
