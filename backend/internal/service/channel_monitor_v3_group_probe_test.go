package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type groupProbeRepoFake struct {
	*monitorV3RepoFake
	mu         sync.Mutex
	claims     map[time.Time]map[int64]bool
	results    []ChannelMonitorV3GroupProbeResult
	failConfig bool
	queried    []int64
}

func (r *groupProbeRepoFake) GetConfig(ctx context.Context) (*ChannelMonitorV3Config, error) {
	if r.failConfig {
		return nil, errors.New("unavailable")
	}
	return r.monitorV3RepoFake.GetConfig(ctx)
}
func (r *groupProbeRepoFake) ClaimGroupProbe(_ context.Context, target ChannelMonitorV3GroupProbeTarget, slot time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claims == nil {
		r.claims = map[time.Time]map[int64]bool{}
	}
	if r.claims[slot] == nil {
		r.claims[slot] = map[int64]bool{}
	}
	if r.claims[slot][target.GroupID] {
		return 0, nil
	}
	r.claims[slot][target.GroupID] = true
	return int64(len(r.results) + 1), nil
}
func (r *groupProbeRepoFake) FinishGroupProbe(_ context.Context, p ChannelMonitorV3GroupProbeResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, p)
	return nil
}
func (r *groupProbeRepoFake) GroupProbes(_ context.Context, ids []int64, from, to time.Time) ([]ChannelMonitorV3GroupProbeResult, error) {
	r.queried = append([]int64(nil), ids...)
	out := []ChannelMonitorV3GroupProbeResult{}
	for _, p := range r.results {
		for _, id := range ids {
			if p.GroupID == id && !p.Slot.Before(from) && p.Slot.Before(to) {
				out = append(out, p)
				break
			}
		}
	}
	return out, nil
}
func (*groupProbeRepoFake) PruneGroupProbes(context.Context, time.Time) error { return nil }

type groupProbeRuntime struct{ enabled bool }

func (r groupProbeRuntime) GetChannelMonitorRuntime(context.Context) ChannelMonitorRuntime {
	return ChannelMonitorRuntime{Enabled: r.enabled, Mode: ChannelMonitorModeV3}
}

type groupProbeExec struct {
	calls  int
	cancel context.CancelFunc
}

func (e *groupProbeExec) Check(_ context.Context, _ *Group, _ string) ChannelMonitorV3GroupProbeResult {
	e.calls++
	if e.cancel != nil {
		e.cancel()
	}
	return ChannelMonitorV3GroupProbeResult{Outcome: "success"}
}

func groupProbeFixture() (*groupProbeRepoFake, *ChannelMonitorV3GroupProber, *groupProbeExec) {
	base := &monitorV3RepoFake{config: monitorV3Config()}
	base.config.GroupProbe = ChannelMonitorV3GroupProbeConfig{Enabled: true, Groups: []ChannelMonitorV3GroupProbeTarget{{GroupID: 4, Model: "gpt-mini", Enabled: true}}}
	repo := &groupProbeRepoFake{monitorV3RepoFake: base}
	exec := &groupProbeExec{}
	p := &ChannelMonitorV3GroupProber{repo: repo, probes: repo, groups: monitorV3GroupsFake{4: {ID: 4, Platform: PlatformOpenAI, Status: StatusActive}}, settings: groupProbeRuntime{true}, executor: exec}
	return repo, p, exec
}

func TestChannelMonitorV3GroupProbeClaimsAtMostOneAttempt(t *testing.T) {
	repo, p, exec := groupProbeFixture()
	now := time.Date(2026, 11, 7, 2, 12, 0, 0, time.UTC)
	p.RunDue(context.Background(), now)
	p.RunDue(context.Background(), now.Add(time.Minute))
	require.Equal(t, 1, exec.calls)
	require.Len(t, repo.results, 1)
	p.RunDue(context.Background(), now.Add(10*time.Minute))
	require.Equal(t, 2, exec.calls)
	repo.config.GroupProbe.Enabled = false
	p.RunDue(context.Background(), now.Add(20*time.Minute))
	require.Equal(t, 2, exec.calls)
	repo.config.GroupProbe.Enabled = true
	repo.failConfig = true
	p.RunDue(context.Background(), now.Add(30*time.Minute))
	require.Equal(t, 2, exec.calls)
}

func TestChannelMonitorV3GroupProbeShutdownIsUnknown(t *testing.T) {
	repo, p, exec := groupProbeFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec.cancel = cancel
	p.RunDue(ctx, time.Now())
	require.Len(t, repo.results, 1)
	require.Equal(t, "cancelled", repo.results[0].Outcome)
}

func TestChannelMonitorV3GroupProbeMinimalPayloadAndResponseValidation(t *testing.T) {
	claude := channelMonitorV3ProbeBody(PlatformAnthropic, "claude-haiku")
	require.Equal(t, int64(1), gjson.GetBytes(claude, "max_tokens").Int())
	openai := channelMonitorV3ProbeBody(PlatformOpenAI, "gpt-mini")
	require.Equal(t, int64(16), gjson.GetBytes(openai, "max_completion_tokens").Int())
	require.False(t, gjson.GetBytes(openai, "tools").Exists())
	for _, body := range []string{
		`data: {"choices":[{"delta":{"content":"1"}}]}` + "\n" + `data: {"choices":[{"finish_reason":"length"}]}`,
		`data: {"type":"content_block_delta","delta":{"text":"1"}}` + "\n" + `data: {"type":"message_stop"}`,
	} {
		require.True(t, channelMonitorV3ProbeResponseOK([]byte(body)), body)
	}
	for _, body := range []string{"", `data: [DONE]`, `data: {"choices":[{"delta":{"content":"1"}}]}`,
		`data: {"type":"response.output_text.delta","delta":"1"}` + "\n" + `data: {"type":"response.failed"}`,
	} {
		require.False(t, channelMonitorV3ProbeResponseOK([]byte(body)), body)
	}
}

func TestChannelMonitorV3PlazaSeparatesProbeAndRequestRates(t *testing.T) {
	repo, _, _ := groupProbeFixture()
	now := time.Date(2026, 11, 7, 2, 12, 0, 0, time.UTC)
	finished := now.Add(-time.Minute)
	repo.through = &finished
	repo.totals = ChannelMonitorV3Facts{Metrics: []ChannelMonitorV3Fact{{GroupID: 4, Success: 3, Errors: 1}}}
	repo.results = []ChannelMonitorV3GroupProbeResult{{GroupID: 4, Model: "gpt-mini", Slot: now.Add(-2 * time.Minute), FinishedAt: &finished, Outcome: "success"}, {GroupID: 4, Model: "gpt-mini", Slot: now.Add(-12 * time.Minute), FinishedAt: &finished, Outcome: "failure"}, {GroupID: 9, Model: "private", Slot: now, FinishedAt: &finished, Outcome: "failure"}}
	svc := NewChannelMonitorV3Service(repo, nil)
	svc.now = func() time.Time { return now }
	out, err := svc.PlazaStatuses(context.Background(), []int64{4}, false)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, 75.0, *out[4].RequestSuccessRate)
	require.Equal(t, 50.0, *out[4].ProbeSuccessRate)
	require.Equal(t, "probe", out[4].Source)
	require.Equal(t, "operational", out[4].Status)
	require.Equal(t, []int64{4}, repo.queried)
	repo.config.GroupProbe.Enabled = false
	out, err = svc.PlazaStatuses(context.Background(), []int64{4}, false)
	require.NoError(t, err)
	require.Equal(t, "unknown", out[4].Status)
	repo.config.PublicEnabled = false
	out, err = svc.PlazaStatuses(context.Background(), []int64{4}, true)
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestChannelMonitorV3GroupProbeConfigurationBoundaries(t *testing.T) {
	_, p, _ := groupProbeFixture()
	svc := NewChannelMonitorV3Service(nil, p.groups)
	valid := ChannelMonitorV3GroupProbeConfig{Enabled: true, Groups: []ChannelMonitorV3GroupProbeTarget{{GroupID: 4, Model: "gpt-mini", Enabled: true}}}
	require.NoError(t, svc.validateGroupProbes(context.Background(), &valid))
	valid.Groups[0].Model = "gpt-image-2"
	require.Error(t, svc.validateGroupProbes(context.Background(), &valid))
	valid.Groups[0].Model = "gpt-mini"
	valid.Groups = append(valid.Groups, valid.Groups[0])
	require.Error(t, svc.validateGroupProbes(context.Background(), &valid))
	valid.Groups = valid.Groups[:1]
	valid.Enabled = false
	valid.Groups[0].GroupID = 999
	require.NoError(t, svc.validateGroupProbes(context.Background(), &valid), "turning the global switch off must remain possible after group removal")
}

func TestChannelMonitorV3PlazaFreshProbeSurvivesStaleCollector(t *testing.T) {
	repo, _, _ := groupProbeFixture()
	now := time.Date(2026, 11, 7, 2, 17, 0, 0, time.UTC)
	stale, finished := now.Add(-time.Hour), now.Add(-time.Minute)
	repo.through = &stale
	repo.slotFacts = ChannelMonitorV3Facts{Metrics: []ChannelMonitorV3Fact{{GroupID: 4, Slot: now.Truncate(10 * time.Minute), Success: 3}}}
	repo.results = []ChannelMonitorV3GroupProbeResult{{GroupID: 4, Model: "gpt-mini", Slot: now.Truncate(10 * time.Minute), FinishedAt: &finished, Outcome: "success"}}
	svc := NewChannelMonitorV3Service(repo, nil)
	svc.now = func() time.Time { return now }
	out, err := svc.PlazaStatuses(context.Background(), []int64{4}, false)
	require.NoError(t, err)
	require.Equal(t, "probe", out[4].Source)
	require.Equal(t, "operational", out[4].Status)
	repo.config.GroupProbe.Groups[0].Enabled = false
	out, err = svc.PlazaStatuses(context.Background(), []int64{4}, false)
	require.NoError(t, err)
	require.Equal(t, "unknown", out[4].Status)
}

func TestChannelMonitorV3ProbeNotificationTargetFence(t *testing.T) {
	repo, _, _ := groupProbeFixture()
	require.True(t, channelMonitorV3ProbeTargetEnabled(&repo.config, 4, "gpt-mini"))
	require.False(t, channelMonitorV3ProbeTargetEnabled(&repo.config, 4, "other-model"))
	repo.config.GroupProbe.Groups[0].Enabled = false
	require.False(t, channelMonitorV3ProbeTargetEnabled(&repo.config, 4, "gpt-mini"))
}

func TestChannelMonitorV3ProbeFailureAndRecoveryEmail(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	ctx := context.Background()
	smtp := startNotificationEmailTestSMTPServer(t)
	settings := newNotificationEmailMemorySettingRepo()
	require.NoError(t, settings.SetMultiple(ctx, smtp.settings()))
	cfg := defaultOpsEmailNotificationConfig()
	cfg.Alert.Enabled, cfg.Alert.IncludeResolvedAlerts, cfg.Alert.MinSeverity = true, true, "warning"
	cfg.Alert.Recipients = []string{"operator@test.invalid"}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, settings.Set(ctx, SettingKeyOpsEmailNotificationConfig, string(raw)))
	repo, p, _ := groupProbeFixture()
	p.ops = &OpsService{settingRepo: settings}
	p.email = NewNotificationEmailService(settings, NewEmailService(settings, nil))
	p.emailLimiter = newSlidingWindowLimiter(0, time.Hour)
	slot := time.Now().UTC().Truncate(ChannelMonitorV3ProbeInterval)
	failure := ChannelMonitorV3GroupProbeResult{GroupID: 4, Model: "gpt-mini", Slot: slot, Outcome: "failure"}
	p.notify(ctx, failure)
	require.EqualValues(t, 1, smtp.messageCount())
	p.notify(ctx, failure)
	require.EqualValues(t, 1, smtp.messageCount(), "the same group/outcome/hour must not send twice")
	repo.results = append(repo.results, failure)
	recovery := failure
	recovery.Slot, recovery.Outcome = slot.Add(ChannelMonitorV3ProbeInterval), "success"
	p.notify(ctx, recovery)
	require.EqualValues(t, 2, smtp.messageCount(), "a successful check after failure sends the existing recovery notice")
}
