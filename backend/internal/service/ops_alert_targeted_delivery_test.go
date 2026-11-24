package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/stretchr/testify/require"
)

type targetedAlertTestRepo struct {
	OpsRepository
	rule     *OpsAlertRule
	event    *OpsAlertEvent
	match    OpsAccountUpstreamErrorMatch
	queryErr error
	filter   OpsAccountUpstreamErrorFilter
	resolved int
	silenced bool
	stale    bool
	claims   int
	starts   int
	delivery *OpsAlertDelivery
	outcomes []OpsAlertDeliveryOutcome
}

func (r *targetedAlertTestRepo) ListAlertRules(context.Context) ([]*OpsAlertRule, error) {
	return []*OpsAlertRule{r.rule}, nil
}
func (*targetedAlertTestRepo) GetLatestSystemMetrics(context.Context, int) (*OpsSystemMetricsSnapshot, error) {
	return nil, nil
}
func (r *targetedAlertTestRepo) CountAccountUpstreamErrors(_ context.Context, f OpsAccountUpstreamErrorFilter) (OpsAccountUpstreamErrorMatch, error) {
	r.filter = f
	return r.match, r.queryErr
}
func (r *targetedAlertTestRepo) GetActiveAlertEvent(context.Context, int64) (*OpsAlertEvent, error) {
	return r.event, nil
}
func (*targetedAlertTestRepo) GetLatestAlertEvent(context.Context, int64) (*OpsAlertEvent, error) {
	return nil, nil
}
func (r *targetedAlertTestRepo) UpdateAlertEventStatus(_ context.Context, _ int64, status string, _ *time.Time) error {
	if status == OpsAlertStatusResolved {
		r.resolved++
	}
	return nil
}
func (r *targetedAlertTestRepo) CreateAlertEvent(_ context.Context, event *OpsAlertEvent) (*OpsAlertEvent, error) {
	r.event = event
	return event, nil
}
func (*targetedAlertTestRepo) UpsertJobHeartbeat(context.Context, *OpsUpsertJobHeartbeatInput) error {
	return nil
}
func (r *targetedAlertTestRepo) GetAlertEventByID(context.Context, int64) (*OpsAlertEvent, error) {
	return r.event, nil
}
func (r *targetedAlertTestRepo) GetAlertRuleByID(context.Context, int64) (*OpsAlertRule, error) {
	return r.rule, nil
}
func (r *targetedAlertTestRepo) IsAlertSilenced(context.Context, int64, string, *int64, *string, time.Time) (bool, error) {
	return r.silenced, nil
}
func (r *targetedAlertTestRepo) ClaimAlertDelivery(_ context.Context, _ time.Time, _ string, _ time.Duration) (*OpsAlertDelivery, error) {
	r.claims++
	return r.delivery, nil
}
func (r *targetedAlertTestRepo) StartAlertDelivery(context.Context, *OpsAlertDelivery, time.Time) (bool, error) {
	r.starts++
	return !r.stale, nil
}
func (r *targetedAlertTestRepo) FinishAlertDelivery(_ context.Context, _ *OpsAlertDelivery, outcome OpsAlertDeliveryOutcome) (bool, error) {
	r.outcomes = append(r.outcomes, outcome)
	return !r.stale, nil
}

type targetedAlertTestSender struct {
	texts []string
	err   error
}

func (s *targetedAlertTestSender) SendText(_ context.Context, text string) error {
	s.texts = append(s.texts, text)
	return s.err
}

func TestOpsTargetedAlertEvaluatorUnknownDoesNotResolve(t *testing.T) {
	t.Setenv(runtimegate.StateFileEnv, "")
	rule := &OpsAlertRule{ID: 7, Name: "targeted", Enabled: true, Severity: "P1", MetricType: OpsAlertMetricAccountUpstreamError,
		Operator: ">=", Threshold: 1, WindowMinutes: 5, SustainedMinutes: 1, NotifyFeishu: true,
		Filters: map[string]any{"account_ids": []int64{248}, "upstream_status_codes": []int{402}}}
	repo := &targetedAlertTestRepo{rule: rule, event: &OpsAlertEvent{ID: 8}, queryErr: errors.New("query failed")}
	evaluator := NewOpsAlertEvaluatorService(&OpsService{opsRepo: repo}, repo, nil, nil, nil, nil, &targetedAlertTestSender{})
	evaluator.evaluateOnce(time.Minute)
	require.Zero(t, repo.resolved)
	require.Equal(t, []int64{248}, repo.filter.AccountIDs)
	require.Equal(t, []int{402}, repo.filter.UpstreamStatusCodes)
	require.Equal(t, 5*time.Minute, repo.filter.End.Sub(repo.filter.Start))
	repo.queryErr = nil
	evaluator.evaluateOnce(time.Minute)
	require.Equal(t, 1, repo.resolved, "confirmed zero is not an unknown query")

	repo.event = nil
	repo.match = OpsAccountUpstreamErrorMatch{Count: 3, LatestAccountID: 248, LatestUpstreamStatusCode: 402,
		LatestProviderCode: "insufficient_quota", LatestPlatform: PlatformOpenAI, MatchedAccountIDs: []int64{248}}
	evaluator.evaluateOnce(time.Minute)
	require.NotNil(t, repo.event)
	require.Equal(t, int64(248), repo.event.Dimensions["latest_account_id"])
	require.Equal(t, "insufficient_quota", repo.event.Dimensions["latest_provider_code"])
	require.Equal(t, []OpsAlertDeliveryTarget{{Channel: OpsAlertDeliveryFeishu, Target: OpsAlertFeishuTarget}}, repo.event.DeliveryPlan)
}

func TestOpsTargetedAccountStatusMetricsFilterTheSameSet(t *testing.T) {
	now := time.Now()
	until := now.Add(time.Hour)
	accounts := map[int64]*AccountAvailability{
		1: {HasError: true, IsRateLimited: true, IsOverloaded: true},
		2: {HasError: true, TempUnschedulableUntil: &until},
		3: {HasError: true, IsRateLimited: true, IsOverloaded: true},
	}
	for metric, want := range map[string]float64{"account_error_ratio": 50, "account_error_count": 1, "account_rate_limited_count": 1,
		"account_temp_unscheduled_count": 1, "overload_account_count": 1} {
		got := computeScopedAccountStatusMetric(metric, accounts, []int64{2, 1, 1, 1000}, now)
		require.True(t, got.available)
		require.Equal(t, want, got.value, metric)
	}
}

func TestOpsAlertOutboxWorkerPrivacyAndRetry(t *testing.T) {
	t.Setenv(runtimegate.StateFileEnv, "")
	now := time.Now().UTC()
	value := 6.0
	repo := &targetedAlertTestRepo{
		rule: &OpsAlertRule{ID: 11, Name: "Targeted balance", MetricType: OpsAlertMetricAccountUpstreamError, NotifyFeishu: true,
			Enabled: true, CustomMessage: "Check top-up guide https://proxy-user:proxy-pass@proxy.invalid:8888\nKey note\nAPI Key: sk-synthetic-private\nPrompt: private prompt"},
		event: &OpsAlertEvent{ID: 12, RuleID: 11, FiredAt: time.Date(2026, 11, 24, 3, 1, 2, 0, time.UTC), Severity: "P1",
			Description: "never send raw upstream response", MetricValue: &value, Dimensions: map[string]any{
				"platform": PlatformOpenAI, "latest_account_id": json.Number("248"), "latest_account_name": "Fake account",
				"latest_upstream_status_code": json.Number("402"), "latest_provider_code": "insufficient_quota",
				"upstream_error_detail": "private raw detail", "user_email": "customer@private.invalid", "prompt": "raw prompt",
			}},
		delivery: &OpsAlertDelivery{ID: 13, EventID: 12, ClaimToken: "test-token", CreatedAt: now,
			OpsAlertDeliveryTarget: OpsAlertDeliveryTarget{Channel: OpsAlertDeliveryFeishu, Target: OpsAlertFeishuTarget}},
	}
	sender := &targetedAlertTestSender{err: errors.New("credential-containing transport error")}
	evaluator := NewOpsAlertEvaluatorService(&OpsService{opsRepo: repo}, repo, nil, nil, nil, nil, sender)
	processed, err := evaluator.processAlertDelivery(context.Background())
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, 1, repo.starts)
	require.Equal(t, OpsAlertDeliveryPending, repo.outcomes[0].Status)
	require.Equal(t, "send_failed", repo.outcomes[0].ErrorCode)
	require.GreaterOrEqual(t, repo.outcomes[0].NextAttempt.Sub(now), 10*time.Second)
	require.Len(t, sender.texts, 1)
	for _, included := range []string{"Targeted balance", "2026-11-24 11:01:02", "#12", "Fake account (#248)", "402", "insufficient_quota", "Check top-up guide"} {
		require.Contains(t, sender.texts[0], included)
	}
	for _, excluded := range []string{"proxy-pass", "proxy.invalid", "sk-synthetic-private", "private prompt", "never send raw", "private raw detail", "customer@private.invalid", "raw prompt", "OvOOO"} {
		require.NotContains(t, sender.texts[0], excluded)
	}
	repo.delivery.AttemptCount = 4
	require.NoError(t, evaluator.dispatchAlertDelivery(context.Background(), repo.delivery, now))
	require.Equal(t, OpsAlertDeliveryFailed, repo.outcomes[1].Status)
	repo.delivery.AttemptCount = 1
	sender.err = nil
	require.NoError(t, evaluator.dispatchAlertDelivery(context.Background(), repo.delivery, now))
	require.Equal(t, OpsAlertDeliverySent, repo.outcomes[2].Status)

	repo.stale = true
	require.NoError(t, evaluator.dispatchAlertDelivery(context.Background(), repo.delivery, now))
	require.Len(t, sender.texts, 3, "a revoked lease must not send")
	repo.stale = false
	repo.delivery.CreatedAt = now.Add(-OpsAlertDeliveryLifetime)
	require.NoError(t, evaluator.dispatchAlertDelivery(context.Background(), repo.delivery, now))
	require.Equal(t, "delivery_budget_exhausted", repo.outcomes[3].ErrorCode)
}

func TestOpsAlertOutboxWorkerSilenceRateLimitAndStandby(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "background-state")
	require.NoError(t, os.WriteFile(stateFile, []byte(runtimegate.StateStandby), 0600))
	t.Setenv(runtimegate.StateFileEnv, stateFile)
	now := time.Now().UTC()
	repo := &targetedAlertTestRepo{
		rule:  &OpsAlertRule{ID: 1, Enabled: true, NotifyFeishu: true},
		event: &OpsAlertEvent{ID: 2, RuleID: 1, Dimensions: map[string]any{"platform": PlatformOpenAI}},
		delivery: &OpsAlertDelivery{ID: 3, EventID: 2, CreatedAt: now,
			OpsAlertDeliveryTarget: OpsAlertDeliveryTarget{Channel: OpsAlertDeliveryFeishu, Target: OpsAlertFeishuTarget}},
	}
	sender := &targetedAlertTestSender{}
	evaluator := NewOpsAlertEvaluatorService(&OpsService{opsRepo: repo}, repo, nil, nil, nil, nil, sender)
	processed, err := evaluator.processAlertDelivery(context.Background())
	require.NoError(t, err)
	require.False(t, processed)
	require.Zero(t, repo.claims)
	require.NoError(t, os.WriteFile(stateFile, []byte(runtimegate.StateActive), 0600))
	repo.silenced = true
	_, err = evaluator.processAlertDelivery(context.Background())
	require.NoError(t, err)
	require.Equal(t, "silenced", repo.outcomes[0].ErrorCode)
	require.Equal(t, OpsAlertDeliverySkipped, repo.outcomes[0].Status)
	repo.silenced = false
	for range opsAlertFeishuRateLimit {
		allowed, err := evaluator.allowOpsAlertDelivery(context.Background(), OpsAlertDeliveryFeishu, opsAlertFeishuRateLimit, now)
		require.NoError(t, err)
		require.True(t, allowed)
	}
	_, err = evaluator.processAlertDelivery(context.Background())
	require.NoError(t, err)
	require.Equal(t, "rate_limited", repo.outcomes[1].ErrorCode)
	require.Equal(t, OpsAlertDeliveryPending, repo.outcomes[1].Status)
	require.Zero(t, repo.starts)
	require.Empty(t, sender.texts)
	allowed, err := evaluator.allowOpsAlertDelivery(context.Background(), OpsAlertDeliveryEmail, 1, now)
	require.NoError(t, err)
	require.True(t, allowed, "Feishu must not consume the email rate limit")
}

func TestOpsTargetedAlertEmailAudienceAndFeishuSwitches(t *testing.T) {
	cfg := defaultOpsEmailNotificationConfig()
	cfg.Alert.Enabled = true
	cfg.Alert.MinSeverity = "warning"
	cfg.Alert.Recipients = []string{"fallback@example.invalid"}
	rule := &OpsAlertRule{NotifyEmail: true, NotifyEmails: []string{"ops@example.invalid", "OPS@example.invalid"}}
	require.Equal(t, []string{"ops@example.invalid"}, opsAlertEmailRecipients(cfg, rule, "P0"))
	require.Empty(t, opsAlertEmailRecipients(cfg, rule, "P3"))
	rule.NotifyEmails = []string{"bad@example.invalid\r\nBcc: attacker@example.invalid"}
	require.Empty(t, opsAlertEmailRecipients(cfg, rule, "P1"), "do not fall back to the global audience after invalid data")
	rule.NotifyEmails = []string{}
	require.Equal(t, cfg.Alert.Recipients, opsAlertEmailRecipients(cfg, rule, "P1"))
	cfg.Alert.Enabled = false
	require.Empty(t, opsAlertEmailRecipients(cfg, rule, "P0"))
	rule.CustomMessage = "single-note"
	description := buildOpsAlertDescription(rule, 12, 5, "", nil)
	require.Equal(t, 1, strings.Count(description, rule.CustomMessage))
	require.Equal(t, 1, strings.Count(buildOpsAlertEmailBody(rule, &OpsAlertEvent{Description: description}), rule.CustomMessage))

	for _, ops := range []bool{false, true} {
		for _, pay := range []bool{false, true} {
			cfg := &config.Config{}
			cfg.Ops.FeishuAlertsEnabled = ops
			cfg.FeishuPaymentAlerts.Enabled = pay
			require.Equal(t, ops && pay, ProvideOpsAlertFeishuSender(cfg) != nil)
		}
	}
}
