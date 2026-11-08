package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3ProbeHonorsClaudeCodeOnlyGroup(t *testing.T) {
	group := &Group{Platform: PlatformAnthropic, ClaudeCodeOnly: true}
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	body, headers, err := channelMonitorV3ProbeRequest(group, account, "owner-selected-haiku")
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "owner-selected-haiku", payload["model"])
	require.Equal(t, float64(1), payload["max_tokens"])
	require.Equal(t, true, payload["stream"])
	req, err := http.NewRequest(http.MethodPost, "/v1/messages", nil)
	require.NoError(t, err)
	req.Header = headers
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	upstream, wire, err := (&GatewayService{cfg: &config.Config{}}).buildUpstreamRequest(context.Background(), c, account, body, "test-token", "apikey", "owner-selected-haiku", true, false)
	require.NoError(t, err)
	require.True(t, isOfficialClaudeCLIUserAgent(getHeaderRaw(upstream.Header, "User-Agent")))
	require.Equal(t, "cli", getHeaderRaw(upstream.Header, "X-App"))
	require.NoError(t, json.Unmarshal(wire, &payload))
	require.Equal(t, "owner-selected-haiku", payload["model"])
	require.Equal(t, float64(1), payload["max_tokens"])
	for _, ordinary := range []*Group{{Platform: PlatformAnthropic}, {Platform: PlatformKimi, ClaudeCodeOnly: true}} {
		body, headers, err := channelMonitorV3ProbeRequest(ordinary, account, "selected")
		require.NoError(t, err)
		require.JSONEq(t, string(channelMonitorV3ProbeBody(ordinary.Platform, "selected")), string(body))
		require.Equal(t, "Sub2API-Availability-Probe", headers.Get("User-Agent"))
	}
	for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeServiceAccount} {
		body, headers, err := channelMonitorV3ProbeRequest(group, &Account{Type: kind}, "selected")
		require.NoError(t, err)
		require.JSONEq(t, string(channelMonitorV3ProbeBody(PlatformAnthropic, "selected")), string(body))
		require.Equal(t, "Sub2API-Availability-Probe", headers.Get("User-Agent"), "OAuth must retain the existing gateway mimic path")
	}
}

func TestChannelMonitorV3DenseHistoryKeepsThreeHourMetrics(t *testing.T) {
	now := time.Date(2026, 11, 8, 17, 25, 0, 0, time.UTC)
	old, recent := now.Truncate(10*time.Minute).Add(-6*time.Hour), now.Truncate(10*time.Minute)
	r := &groupProbeRepoFake{monitorV3RepoFake: &monitorV3RepoFake{config: monitorV3Config(), through: &now}}
	r.config.GroupProbe = ChannelMonitorV3GroupProbeConfig{Enabled: true, Groups: []ChannelMonitorV3GroupProbeTarget{{GroupID: 4, Model: "selected", Enabled: true}}}
	r.slotFacts = ChannelMonitorV3Facts{Metrics: []ChannelMonitorV3Fact{{GroupID: 4, Slot: old, Errors: 100}, {GroupID: 4, Slot: recent, Success: 30}}}
	input, read := int64(100), int64(10)
	r.results = []ChannelMonitorV3GroupProbeResult{{GroupID: 4, Model: "selected", Slot: old, FinishedAt: &old, Outcome: "success", CacheInputTokens: &input, CacheReadTokens: &read}}
	for i := 0; i < 6; i++ {
		at := recent.Add(-time.Duration(i) * 10 * time.Minute)
		r.results = append(r.results, ChannelMonitorV3GroupProbeResult{GroupID: 4, Model: "selected", Slot: at, FinishedAt: &at, Outcome: "success", LatencyMs: 100})
	}
	s := NewChannelMonitorV3Service(r, nil)
	s.now = func() time.Time { return now }
	out, err := s.PlazaStatuses(context.Background(), []int64{4}, false)
	require.NoError(t, err)
	item := out[4]
	require.Len(t, item.Cells, 72)
	require.Equal(t, 720, item.HistoryWindowMinutes)
	require.Equal(t, 180, item.WindowMinutes)
	require.Equal(t, 100.0, *item.RequestSuccessRate)
	require.Equal(t, 100.0, *item.ProbeSuccessRate)
	require.Equal(t, &old, item.ProbeCacheVerifiedAt)
	require.Nil(t, item.ProbeCacheReadRate, "old verification must not enter the 3h probe ratio")
}

func TestChannelMonitorV3QuotaNoticeUsesExplicitRelevantResetEvidence(t *testing.T) {
	now := time.Date(2026, 11, 8, 17, 25, 0, 0, time.UTC)
	soon, later, past := now.Add(30*time.Minute), now.Add(2*time.Hour), now.Add(-time.Minute)
	account := func() Account {
		return Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, SessionWindowEnd: &soon, Extra: map[string]any{"session_window_utilization": 1.0}}
	}
	for _, tc := range []struct {
		name     string
		accounts []Account
		state    string
		reset    *time.Time
	}{
		{"all exhausted", []Account{account()}, "exhausted", &soon},
		{"backup configured", []Account{account(), {Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}}, "limited", &soon},
		{"disabled is not quota", []Account{func() Account { a := account(); a.Schedulable = false; return a }()}, "", nil},
		{"expired window is not a block", []Account{func() Account { a := account(); a.SessionWindowEnd = &past; return a }()}, "", nil},
		{"rate limit without quota is not exhaustion", []Account{{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, RateLimitResetAt: &soon}}, "", nil},
		{"unknown reset has no countdown", []Account{func() Account { a := account(); a.SessionWindowEnd = nil; a.RateLimitResetAt = &soon; return a }()}, "exhausted", nil},
		{"unbounded old usage is not current evidence", []Account{func() Account { a := account(); a.SessionWindowEnd = nil; return a }()}, "", nil},
		{"all blocking windows must reset", []Account{func() Account {
			a := account()
			a.Extra["passive_usage_7d_utilization"] = 1.0
			a.Extra["passive_usage_7d_reset"] = later.Unix()
			return a
		}()}, "exhausted", &later},
		{"unknown blocking reset defeats prediction", []Account{func() Account { a := account(); a.Extra["passive_usage_7d_utilization"] = 1.0; return a }()}, "exhausted", nil},
		{"earliest recoverable account", []Account{account(), func() Account { a := account(); a.SessionWindowEnd = &later; return a }()}, "exhausted", &soon},
		{"pool quota is independent of check model", []Account{func() Account {
			a := account()
			a.Credentials = map[string]any{"model_mapping": map[string]any{"other": "other"}}
			return a
		}()}, "exhausted", &soon},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := &ChannelMonitorV3GroupStatus{}
			channelMonitorV3ApplyQuota(item, tc.accounts, now)
			require.Equal(t, tc.state, item.PoolQuotaState)
			require.Equal(t, tc.reset, item.PoolQuotaResetsAt)
			body, err := json.Marshal(item)
			require.NoError(t, err)
			require.NotContains(t, string(body), "session_window_utilization")
			require.NotContains(t, string(body), "model_mapping")
		})
	}
}
