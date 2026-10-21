package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type modelTracePolicyRepo struct {
	modelTraceContextRepo
	pauseCalls int
	pauseErr   error
}

func (r *modelTracePolicyRepo) SetSchedulable(_ context.Context, _ int64, value bool) error {
	r.pauseCalls++
	if r.pauseErr != nil {
		return r.pauseErr
	}
	r.account.Schedulable = value
	return nil
}

type modelTracePolicySender struct {
	messages []string
	err      error
}

func (s *modelTracePolicySender) SendText(_ context.Context, message string) error {
	s.messages = append(s.messages, message)
	return s.err
}

type modelTracePolicySettings struct{ modelTraceStoredSettings }

func (s *modelTracePolicySettings) Set(_ context.Context, _ string, value string) error {
	s.raw = value
	return nil
}

type modelTracePolicyFixture struct {
	svc    *ModelTraceService
	repo   *modelTracePolicyRepo
	sender *modelTracePolicySender
}

func newModelTracePolicyFixture(t *testing.T, cfg ModelTraceGlobalConfig) modelTracePolicyFixture {
	t.Helper()
	repo := &modelTracePolicyRepo{modelTraceContextRepo: modelTraceContextRepo{modelTraceTestRepo: modelTraceTestRepo{
		account: &Account{ID: 55, Name: "private-account-name", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
			Status: StatusActive, Schedulable: true,
			Credentials: map[string]any{"access_token": "private-token", "model_mapping": map[string]any{"public-gpt": "gpt-6.1-sol"}},
			Extra:       map[string]any{ModelTraceExtraKey: &ModelTraceAccountConfig{Models: map[string]*ModelTraceModelConfig{"public-gpt": {}}}}},
	}}}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	svc := NewModelTraceService(repo, &modelTracePolicySettings{modelTraceStoredSettings{raw: string(raw)}}, nil, nil)
	sender := &modelTracePolicySender{}
	svc.notificationSender = sender
	t.Cleanup(svc.Stop)
	return modelTracePolicyFixture{svc: svc, repo: repo, sender: sender}
}

func (f modelTracePolicyFixture) save(t *testing.T, detected string, at time.Time) *ModelTraceRecord {
	t.Helper()
	record, err := f.svc.saveProbeResult(context.Background(), 55, "public-gpt", &ModelTraceRecord{
		ExpectedModel: "gpt-6.1-sol", DetectedModel: detected, Probability: .95, TestedAt: at,
	})
	require.NoError(t, err)
	return record
}

func TestModelTracePolicyAttribution(t *testing.T) {
	cfg := ModelTraceGlobalConfig{AllowedDetections: defaultModelTraceAllowedDetections()}
	for _, test := range []struct{ expected, detected, probeError, verdict string }{
		{"gpt-6-astra", "gpt-6-astra", "", "match"},
		{"gpt-6.1-sol", "gpt-6-astra", "", "match"},
		{"gpt-6.1-sol", "gpt-5.6-luna", "", "mismatch"},
		{"gpt-6.1-sol", "gpt-6-sol", "", "mismatch"},
		{"gpt-5.6-luna", "gpt-5.6-luna", "", "match"},
		{"gpt-future", "gpt-5.6-luna", "", "uncovered"},
		{"gpt-6.1-sol", "gpt-5.6-luna", "incomplete samples", "inconclusive"},
		{"gpt-6.1-sol", "Unknown", "", "inconclusive"},
	} {
		t.Run(test.expected+"/"+test.detected+"/"+test.verdict, func(t *testing.T) {
			require.Equal(t, test.verdict, modelTraceMatchStatus(&ModelTraceRecord{
				ExpectedModel: test.expected, DetectedModel: test.detected, Probability: .7, Error: test.probeError,
			}, cfg))
		})
	}
}

func TestModelTracePolicyActionsUseMappedModel(t *testing.T) {
	for _, notify := range []bool{false, true} {
		for _, pause := range []bool{false, true} {
			f := newModelTracePolicyFixture(t, ModelTraceGlobalConfig{NotifyOnMismatch: notify, PauseOnMismatch: pause})
			record := f.save(t, "gpt-5.6-luna", time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC))
			require.Equal(t, "mismatch", record.MatchStatus)
			require.Equal(t, pause, record.AccountPaused)
			require.Equal(t, !pause, f.repo.account.Schedulable)
			require.Equal(t, map[bool]int{false: 0, true: 1}[pause], f.repo.pauseCalls)
			require.Len(t, f.sender.messages, map[bool]int{false: 0, true: 1}[notify])
			if notify {
				require.Equal(t, "sent", record.NotificationStatus)
				require.Contains(t, f.sender.messages[0], "gpt-6.1-sol")
				require.NotContains(t, f.sender.messages[0], "private-account-name")
				require.NotContains(t, f.sender.messages[0], "private-token")
			}
			saved := DecodeModelTraceConfig(f.repo.account.Extra).Models["public-gpt"].Latest
			require.Equal(t, record.MatchStatus, saved.MatchStatus)
			require.Equal(t, record.NotificationStatus, saved.NotificationStatus)
		}
	}
}

func TestModelTracePolicyCooldownRecoveryAndDeliveryRetry(t *testing.T) {
	f := newModelTracePolicyFixture(t, ModelTraceGlobalConfig{NotifyOnMismatch: true, NotificationCooldownMinutes: 60})
	at := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	f.sender.err = errors.New("transport failed")
	require.Equal(t, "failed", f.save(t, "gpt-5.6-luna", at).NotificationStatus)
	require.Nil(t, DecodeModelTraceConfig(f.repo.account.Extra).Models["public-gpt"].Alert)
	f.sender.err = nil
	require.Equal(t, "sent", f.save(t, "gpt-5.6-luna", at.Add(time.Minute)).NotificationStatus)
	require.Equal(t, "cooldown", f.save(t, "gpt-5.6-luna", at.Add(2*time.Minute)).NotificationStatus)
	require.Len(t, f.sender.messages, 2)
	require.Equal(t, "sent", f.save(t, "gpt-5.6-luna", at.Add(61*time.Minute)).NotificationStatus)
	require.Equal(t, "match", f.save(t, "gpt-6-astra", at.Add(62*time.Minute)).MatchStatus)
	require.Equal(t, "sent", f.save(t, "gpt-5.6-luna", at.Add(63*time.Minute)).NotificationStatus)
	require.Len(t, f.sender.messages, 4)
}

func TestModelTracePolicyDoesNotActOnRemovedOrStaleResults(t *testing.T) {
	for _, condition := range []string{"save_failed", "monitor_removed", "model_removed", "mapping_changed", "probe_error"} {
		t.Run(condition, func(t *testing.T) {
			f := newModelTracePolicyFixture(t, ModelTraceGlobalConfig{NotifyOnMismatch: true, PauseOnMismatch: true})
			record := &ModelTraceRecord{ExpectedModel: "gpt-6.1-sol", DetectedModel: "gpt-5.6-luna", Probability: .95}
			switch condition {
			case "save_failed":
				f.repo.updateErr = errors.New("save failed")
			case "monitor_removed":
				f.repo.account.Extra[ModelTraceExtraKey] = nil
			case "model_removed":
				f.repo.account.Extra[ModelTraceExtraKey] = &ModelTraceAccountConfig{Models: map[string]*ModelTraceModelConfig{}}
			case "mapping_changed":
				f.repo.account.Credentials["model_mapping"] = map[string]any{"public-gpt": "gpt-6-astra"}
			case "probe_error":
				record.Error = "incomplete samples: 1/3"
			}
			_, err := f.svc.saveProbeResult(context.Background(), 55, "public-gpt", record)
			if condition == "save_failed" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Zero(t, f.repo.pauseCalls)
			require.Empty(t, f.sender.messages)
		})
	}
}

func TestModelTracePolicyPauseFailureStillNotifies(t *testing.T) {
	f := newModelTracePolicyFixture(t, ModelTraceGlobalConfig{NotifyOnMismatch: true, PauseOnMismatch: true})
	f.repo.pauseErr = errors.New("db unavailable")
	record := f.save(t, "gpt-5.6-luna", time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC))
	require.False(t, record.AccountPaused)
	require.NotEmpty(t, record.ActionError)
	require.Equal(t, "sent", record.NotificationStatus)
	require.True(t, f.repo.account.Schedulable)
}

func TestModelTracePolicyConfigRoundTripAndBounds(t *testing.T) {
	settings := &modelTracePolicySettings{}
	svc := NewModelTraceService(nil, settings, nil, nil)
	t.Cleanup(svc.Stop)
	cfg := svc.GetGlobalConfig(context.Background())
	require.False(t, cfg.NotifyOnMismatch)
	require.False(t, cfg.PauseOnMismatch)
	require.Equal(t, []string{"gpt-6-astra"}, cfg.AllowedDetections["gpt-6.1-sol"])
	cfg.NotifyOnMismatch, cfg.PauseOnMismatch = true, true
	cfg.AllowedDetections = map[string][]string{" GPT-6.1-SOL ": {" GPT-6-ASTRA "}, " CUSTOM-SOL ": {" GPT-6-SOL ", "gpt-6-astra"}}
	require.NoError(t, svc.UpdateGlobalConfig(context.Background(), cfg))
	reloaded := svc.GetGlobalConfig(context.Background())
	require.True(t, reloaded.NotifyOnMismatch)
	require.Equal(t, map[string][]string{"gpt-6.1-sol": {"gpt-6-astra"}, "custom-sol": {"gpt-6-sol", "gpt-6-astra"}}, reloaded.AllowedDetections)
	cfg.AllowedDetections = map[string][]string{}
	require.NoError(t, svc.UpdateGlobalConfig(context.Background(), cfg))
	require.Empty(t, svc.GetGlobalConfig(context.Background()).AllowedDetections)
	for _, cooldown := range []int{-1, 1441} {
		cfg.NotificationCooldownMinutes = cooldown
		require.Error(t, svc.UpdateGlobalConfig(context.Background(), cfg))
	}
	cfg.NotificationCooldownMinutes = 60
	cfg.AllowedDetections = map[string][]string{"gpt-6.1-sol": {"unknown-fingerprint"}}
	require.Error(t, svc.UpdateGlobalConfig(context.Background(), cfg))
}
