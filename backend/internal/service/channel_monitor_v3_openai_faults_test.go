package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIGroupStatusCountsOnlyAttributedFaults(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 31, 0, 0, time.UTC)
	last := now.Truncate(10 * time.Minute)
	for _, tc := range []struct {
		name, platform       string
		ok, raw, faults      int64
		missing              bool
		probe, state, source string
		rate                 float64
	}{
		{"inputs and latency do not degrade", PlatformOpenAI, 25, 8, 0, false, "", "operational", "requests", 100},
		{"excluded-only slot remains unobserved", PlatformOpenAI, 0, 2, 0, false, "", "collecting", "none", -1},
		{"excluded-only slot can show an independent probe", PlatformOpenAI, 0, 2, 0, false, "success", "operational", "probe", -1},
		{"upstream auth not ignored by diagnostic config", PlatformOpenAI, 18, 8, 2, false, "success", "degraded", "requests", 90},
		{"one attributable outage is a warning not a full outage", PlatformOpenAI, 0, 1, 1, false, "success", "errors_observed", "requests", -1},
		{"two final faults meet exact red threshold", PlatformOpenAI, 8, 2, 2, false, "success", "down", "requests", -1},
		{"unverifiable legacy errors suppress rate and green", PlatformOpenAI, 50, 2, 0, true, "success", "unknown", "requests", -1},
		{"known fault with incomplete coverage stays visible", PlatformOpenAI, 50, 2, 1, true, "success", "errors_observed", "requests", -1},
		{"probe can establish actual failure despite coverage gap", PlatformOpenAI, 50, 2, 0, true, "failure", "down", "probe", -1},
		{"other platforms retain configured latency and ignored categories", PlatformAnthropic, 25, 8, 0, true, "", "degraded", "requests", 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _, _ := groupProbeFixture()
			svc := NewChannelMonitorV3Service(repo, nil)
			svc.now = func() time.Time { return now }
			repo.through = &last
			repo.slotFacts = ChannelMonitorV3Facts{Metrics: []ChannelMonitorV3Fact{{GroupID: 4, Slot: last, Success: tc.ok, Errors: tc.raw, OpenAIFaults: tc.faults, OpenAICoverageMissing: tc.missing}}, Errors: []ChannelMonitorV3ErrorFact{{GroupID: 4, Slot: last, Category: "authentication", Errors: tc.raw}}, Latency: []ChannelMonitorV3LatencyFact{{GroupID: 4, Slot: last, UpperBound: 30000, Count: tc.ok}}}
			if tc.probe != "" {
				repo.results = []ChannelMonitorV3GroupProbeResult{{GroupID: 4, Model: "gpt-mini", Slot: last, FinishedAt: &now, Outcome: tc.probe}}
			}
			out, err := svc.PlazaStatuses(context.Background(), []Group{{ID: 4, Platform: tc.platform}}, false)
			require.NoError(t, err)
			got := out[4]
			cell := got.Cells[len(got.Cells)-1]
			require.Equal(t, tc.state, cell.Status)
			require.Equal(t, tc.source, cell.Source)
			if tc.rate < 0 {
				require.Nil(t, got.RequestSuccessRate)
			} else {
				require.NotNil(t, got.RequestSuccessRate)
				require.InDelta(t, tc.rate, *got.RequestSuccessRate, 0.0001)
			}
			if tc.ok >= 20 && !tc.missing {
				require.NotNil(t, got.TTFTP50Ms)
				require.EqualValues(t, 30000, *got.TTFTP50Ms)
			}
		})
	}
}

func TestOpenAIGroupStatusWindowsAndFreshness(t *testing.T) {
	repo, _, _ := groupProbeFixture()
	svc := NewChannelMonitorV3Service(repo, nil)
	now := time.Date(2026, 1, 1, 12, 31, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	last := now.Truncate(10 * time.Minute)
	repo.through = &last
	repo.slotFacts.Metrics = []ChannelMonitorV3Fact{{GroupID: 4, Slot: last, Success: 24, Errors: 3}, {GroupID: 4, Slot: last.Add(-3 * time.Hour), Errors: 5, OpenAIFaults: 5}, {GroupID: 8, Slot: last, Errors: 100, OpenAIFaults: 100}}
	groups := []Group{{ID: 4, Platform: PlatformOpenAI}}
	out, err := svc.PlazaStatuses(context.Background(), groups, false)
	require.NoError(t, err)
	require.NotNil(t, out[4].RequestSuccessRate)
	require.Equal(t, 100.0, *out[4].RequestSuccessRate)
	require.Equal(t, "down", out[4].Cells[len(out[4].Cells)-19].Status)
	stale := now.Add(-11 * time.Minute)
	repo.through = &stale
	out, err = svc.PlazaStatuses(context.Background(), groups, false)
	require.NoError(t, err)
	require.Nil(t, out[4].RequestSuccessRate)
	require.Equal(t, "stale", out[4].RequestSampleState)
	require.Equal(t, "unknown", out[4].Status)
}

// Sanitized group totals from the 2026-11-20 16:58:34 UTC snapshot. Feed both
// scoring policies the same final traffic and independent probe observations.
func TestOpenAIGroupStatusReplays72Slots(t *testing.T) {
	repo, _, _ := groupProbeFixture()
	now := time.Date(2026, 11, 20, 16, 58, 34, 0, time.UTC)
	first := time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)
	svc := NewChannelMonitorV3Service(repo, nil)
	svc.now = func() time.Time { return now }
	repo.through = &now
	successes := [...]int64{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 5, 0, 0, 0, 0, 0, 0,
		0, 2, 28, 27, 15, 12, 25, 10, 8, 31, 12, 0, 17, 0, 0, 0, 0, 10,
		6, 24, 12, 3, 0, 2, 16, 23, 13, 2, 0, 0, 2, 10, 18, 11, 2, 25,
		17, 0, 3, 15, 20, 31, 6, 17, 49, 49, 22, 6, 0, 0, 0, 0, 0, 0,
	}
	errors := map[int]int64{27: 3, 36: 1, 37: 1, 38: 1, 39: 1, 40: 1, 43: 1, 59: 1, 61: 1, 64: 1}
	slow := map[int]int64{30: 15000, 45: 15000, 52: 30000, 56: 30000, 60: 30000, 65: 15000}
	for i, success := range successes {
		slot := first.Add(time.Duration(i) * 10 * time.Minute)
		finished := slot.Add(time.Second)
		repo.results = append(repo.results, ChannelMonitorV3GroupProbeResult{GroupID: 4, Model: "gpt-mini", Slot: slot, FinishedAt: &finished, Outcome: "success"})
		if success+errors[i] == 0 {
			continue
		}
		fact := ChannelMonitorV3Fact{GroupID: 4, Slot: slot, Success: success, Errors: errors[i]}
		if i == 27 {
			fact.OpenAIFaults = 2 // confirmed provider 429s; the third was a body timeout
		}
		repo.slotFacts.Metrics = append(repo.slotFacts.Metrics, fact)
		if bound := slow[i]; bound > 0 {
			repo.slotFacts.Latency = append(repo.slotFacts.Latency, ChannelMonitorV3LatencyFact{GroupID: 4, Slot: slot, UpperBound: bound, Count: success})
		}
	}
	for _, tc := range []struct {
		name, platform, slot40source string
		policy                       channelMonitorV3ScorePolicy
		red, yellow, green           int
		faults                       int64
		rate3h, rate12h              float64
	}{
		{"legacy rules control", PlatformAnthropic, "requests", channelMonitorV3Diagnostic, 2, 10, 60, 12, 98.74, 98.06},
		{"GPT attributable faults", PlatformOpenAI, "probe", channelMonitorV3OpenAIFaults, 0, 1, 71, 2, 100, 99.67},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := svc.PlazaStatuses(context.Background(), []Group{{ID: 4, Platform: tc.platform}}, false)
			require.NoError(t, err)
			card := out[4]
			require.Len(t, card.Cells, 72)
			colors := map[string]int{}
			for _, cell := range card.Cells {
				colors[cell.Status]++
			}
			require.Equal(t, tc.red, colors[ChannelMonitorV3StatusDown])
			require.Equal(t, tc.yellow, colors[ChannelMonitorV3StatusDegraded])
			require.Equal(t, tc.green, colors[ChannelMonitorV3StatusOperational])
			require.Equal(t, ChannelMonitorV3StatusDegraded, card.Cells[27].Status)
			require.Equal(t, "requests", card.Cells[27].Source)
			require.Equal(t, tc.slot40source, card.Cells[40].Source)
			require.NotNil(t, card.RequestSuccessRate)
			require.InDelta(t, tc.rate3h, *card.RequestSuccessRate, 0.005)
			require.NotNil(t, card.ProbeSuccessRate)
			require.Equal(t, 100.0, *card.ProbeSuccessRate)
			total := tc.policy.accumulate(repo.config, ChannelMonitorV3Component{GroupID: 4}, &repo.slotFacts, time.Time{}, 0)[0]
			require.EqualValues(t, 608, total.success)
			require.Equal(t, tc.faults, total.errors)
			require.InDelta(t, tc.rate12h, 100*float64(total.success)/float64(total.counted()), 0.005)
		})
	}
}
