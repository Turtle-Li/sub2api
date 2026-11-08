package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3QuietGroupProbeMetrics(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		change      func(*groupProbeRepoFake)
		ready       bool
	}{
		{name: "fresh successful checks", state: "ready", ready: true},
		{name: "expired checks", state: "stale", change: func(r *groupProbeRepoFake) {
			for i := range r.results {
				r.results[i].Slot = r.results[i].Slot.Add(-20 * time.Minute)
				finished := r.results[i].FinishedAt.Add(-20 * time.Minute)
				r.results[i].FinishedAt = &finished
			}
		}},
		{name: "disabled checks", state: "disabled", change: func(r *groupProbeRepoFake) { r.config.GroupProbe.Enabled = false }},
		{name: "small sample", state: "insufficient", change: func(r *groupProbeRepoFake) { r.results = r.results[:5] }},
		{name: "previous model", state: "none", change: func(r *groupProbeRepoFake) {
			for i := range r.results {
				r.results[i].Model = "previous-choice"
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given an idle group with successful scheduled checks and real measured durations.
			r, _, _ := groupProbeFixture()
			now := time.Date(2026, 11, 8, 12, 2, 0, 0, time.UTC)
			for i := range 6 {
				slot := now.Truncate(10 * time.Minute).Add(-time.Duration(i) * 10 * time.Minute)
				finished := slot.Add(time.Second)
				r.results = append(r.results, ChannelMonitorV3GroupProbeResult{GroupID: 4, Model: "gpt-mini", Slot: slot,
					FinishedAt: &finished, Outcome: "success", LatencyMs: int64(i+1) * 1000})
			}
			if tc.change != nil {
				tc.change(r)
			}
			svc := NewChannelMonitorV3Service(r, nil)
			svc.now = func() time.Time { return now }
			// When the public group metrics are read.
			out, err := svc.PlazaStatuses(context.Background(), []int64{4}, false)
			require.NoError(t, err)
			data, err := json.Marshal(out[4])
			require.NoError(t, err)
			var metrics struct {
				Latency *int64 `json:"probe_latency_p50_ms"`
			}
			require.NoError(t, json.Unmarshal(data, &metrics))
			// Then only fresh, sufficient probes publish their own metrics, never user facts.
			require.Equal(t, tc.state, out[4].ProbeSampleState)
			require.Nil(t, out[4].RequestSuccessRate)
			require.Nil(t, out[4].TTFTP50Ms)
			if tc.ready {
				require.NotNil(t, metrics.Latency)
				require.EqualValues(t, 3000, *metrics.Latency)
				require.Equal(t, 100.0, *out[4].ProbeSuccessRate)
			} else {
				require.Nil(t, metrics.Latency)
				require.Nil(t, out[4].ProbeSuccessRate)
			}
		})
	}
}

func TestChannelMonitorV3ProbeTimingRequiresSuccessfulValidSamples(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]ChannelMonitorV3GroupProbeResult)
		ttft   *int64
		total  bool
		state  string
	}{
		{name: "native median", ttft: probeTimingPtr(300), total: true, state: "ready"},
		{name: "zero is measured", ttft: probeTimingPtr(0), total: true, state: "ready", change: func(p []ChannelMonitorV3GroupProbeResult) {
			for i := range p {
				p[i].FirstTokenMs = probeTimingPtr(0)
			}
		}},
		{name: "historical null", total: true, state: "ready", change: func(p []ChannelMonitorV3GroupProbeResult) { p[0].FirstTokenMs = nil }},
		{name: "negative native time", total: true, state: "ready", change: func(p []ChannelMonitorV3GroupProbeResult) { p[0].FirstTokenMs = probeTimingPtr(-1) }},
		{name: "native exceeds total", total: true, state: "ready", change: func(p []ChannelMonitorV3GroupProbeResult) { p[0].FirstTokenMs = probeTimingPtr(1001) }},
		{name: "failed timing is excluded", state: "ready", change: func(p []ChannelMonitorV3GroupProbeResult) { p[0].Outcome = "failure" }},
		{name: "pending round excluded", state: "insufficient", change: func(p []ChannelMonitorV3GroupProbeResult) { p[0].Outcome = "pending" }},
		{name: "cancelled round excluded", state: "insufficient", change: func(p []ChannelMonitorV3GroupProbeResult) { p[0].Outcome = "cancelled" }},
		{name: "future completion excluded", state: "insufficient", change: func(p []ChannelMonitorV3GroupProbeResult) { at := p[0].Slot.Add(time.Hour); p[0].FinishedAt = &at }},
		{name: "completion before slot excluded", state: "insufficient", change: func(p []ChannelMonitorV3GroupProbeResult) { at := p[0].Slot.Add(-time.Second); p[0].FinishedAt = &at }},
		{name: "outside window excluded", state: "insufficient", change: func(p []ChannelMonitorV3GroupProbeResult) { p[0].Slot = p[0].Slot.Add(-4 * time.Hour) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given six final scheduled receipts with native timing separate from duration.
			r, _, _ := groupProbeFixture()
			now := time.Date(2026, 11, 8, 12, 2, 0, 0, time.UTC)
			for i := range 6 {
				slot := now.Truncate(10 * time.Minute).Add(-time.Duration(i) * 10 * time.Minute)
				finished := slot.Add(time.Second)
				r.results = append(r.results, ChannelMonitorV3GroupProbeResult{GroupID: 4, Model: "gpt-mini", Slot: slot, FinishedAt: &finished,
					Outcome: "success", LatencyMs: int64(i+1) * 1000, FirstTokenMs: probeTimingPtr(int64(i+1) * 100)})
			}
			if tc.change != nil {
				tc.change(r.results)
			}
			svc := NewChannelMonitorV3Service(r, nil)
			svc.now = func() time.Time { return now }
			// When statistics are calculated, only complete valid samples qualify.
			out, err := svc.PlazaStatuses(context.Background(), []int64{4}, false)
			require.NoError(t, err)
			require.Equal(t, 1, out[4].ProbeMetricsVersion)
			require.Equal(t, tc.state, out[4].ProbeSampleState)
			require.Equal(t, tc.ttft, out[4].ProbeTTFTP50Ms)
			if tc.total {
				require.EqualValues(t, 3000, *out[4].ProbeLatencyP50Ms)
			} else {
				require.Nil(t, out[4].ProbeLatencyP50Ms)
			}
			require.Nil(t, out[4].TTFTP50Ms)
		})
	}
}

func probeTimingPtr(value int64) *int64 { return &value }
