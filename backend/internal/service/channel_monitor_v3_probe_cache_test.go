package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3ProbeCacheRequiresNativeNumericDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name, usage string
		input, read int64
		known       bool
	}{
		{"OpenAI", `{"prompt_tokens_details":{"cached_tokens":8}}`, 10, 8, true},
		{"Responses", `{"input_tokens_details":{"cached_tokens":8}}`, 10, 8, true},
		{"Anthropic", `{"cache_read_input_tokens":8}`, 10, 8, true},
		{"DeepSeek", `{"prompt_cache_hit_tokens":8}`, 10, 8, true},
		{"flat cached", `{"cached_tokens":8}`, 10, 8, true},
		{"flat cache read", `{"cache_read_tokens":8}`, 10, 8, true},
		{"cold", `{"prompt_tokens_details":{"cached_tokens":0}}`, 10, 0, true},
		{"absent", `{"prompt_tokens":10}`, 10, 0, false},
		{"null", `{"cached_tokens":null}`, 10, 0, false},
		{"string", `{"cached_tokens":"8"}`, 10, 8, false},
		{"negative", `{"cached_tokens":-1}`, 10, 0, false},
		{"fraction", `{"cached_tokens":8.5}`, 10, 8, false},
		{"over input", `{"cached_tokens":11}`, 10, 11, false},
		{"oversized", `{"cached_tokens":1e30}`, 10, 0, false},
		{"no input", `{"cached_tokens":0}`, 0, 0, false},
		{"normalization mismatch", `{"cached_tokens":8}`, 10, 7, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a native receipt and the gateway's normalized usage bucket.
			body := []byte("data: {\"usage\":" + tc.usage + "}\n\ndata: [DONE]\n\n")
			// When presence and numeric validity are checked.
			out := channelMonitorV3ProbeCacheReadTokens(body, tc.input, tc.read)
			// Then an omitted/invalid field never becomes a measured cold hit.
			if tc.known {
				require.Equal(t, &tc.read, out)
			} else {
				require.Nil(t, out)
			}
		})
	}
}

func TestChannelMonitorV3ProbeCacheUsesQualifiedSuccessfulFinalReceipts(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		change      func(*groupProbeRepoFake)
		rate        *float64
	}{
		{name: "token weighted instead of mean percent or paid retry input", state: "ready", rate: probeCacheRatePtr(100 * 1300.0 / 1500)},
		{name: "cold hits remain in the denominator", state: "ready", rate: probeCacheRatePtr(0), change: func(r *groupProbeRepoFake) {
			for i := range r.results {
				r.results[i].CacheReadTokens = probeTimingPtr(0)
			}
		}},
		{name: "missing is unknown", state: "none", change: func(r *groupProbeRepoFake) {
			for i := range r.results {
				r.results[i].CacheInputTokens, r.results[i].CacheReadTokens = nil, nil
			}
		}},
		{name: "five samples", state: "insufficient", change: func(r *groupProbeRepoFake) { r.results[0].CacheReadTokens = nil }},
		{name: "failed cache is excluded", state: "insufficient", change: func(r *groupProbeRepoFake) { r.results[0].Outcome = "failure" }},
		{name: "pending cache is excluded", state: "insufficient", change: func(r *groupProbeRepoFake) { r.results[0].Outcome = "pending" }},
		{name: "negative cache is excluded", state: "insufficient", change: func(r *groupProbeRepoFake) { r.results[0].CacheReadTokens = probeTimingPtr(-1) }},
		{name: "oversized cache is excluded", state: "insufficient", change: func(r *groupProbeRepoFake) { r.results[0].CacheReadTokens = probeTimingPtr(101) }},
		{name: "zero input is excluded", state: "insufficient", change: func(r *groupProbeRepoFake) { r.results[0].CacheInputTokens = probeTimingPtr(0) }},
		{name: "previous model excluded", state: "none", change: func(r *groupProbeRepoFake) {
			for i := range r.results {
				r.results[i].Model = "old-choice"
			}
		}},
		{name: "disabled", state: "disabled", change: func(r *groupProbeRepoFake) { r.config.GroupProbe.Enabled = false }},
		{name: "expired", state: "stale", change: func(r *groupProbeRepoFake) {
			for i := range r.results {
				r.results[i].Slot = r.results[i].Slot.Add(-20 * time.Minute)
				at := r.results[i].FinishedAt.Add(-20 * time.Minute)
				r.results[i].FinishedAt = &at
			}
		}},
		{name: "outside rolling window", state: "insufficient", change: func(r *groupProbeRepoFake) { r.results[0].Slot = r.results[0].Slot.Add(-4 * time.Hour) }},
		{name: "future completion", state: "insufficient", change: func(r *groupProbeRepoFake) { at := r.results[0].Slot.Add(time.Hour); r.results[0].FinishedAt = &at }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given six native cache receipts, separate from cumulative retry usage.
			r, _, _ := groupProbeFixture()
			now := time.Date(2026, 11, 8, 12, 2, 0, 0, time.UTC)
			for i := range 6 {
				slot := now.Truncate(10 * time.Minute).Add(-time.Duration(i) * 10 * time.Minute)
				finished := slot.Add(time.Second)
				input, read := int64(100), int64(80)
				if i == 5 {
					input, read = 1000, 900
				}
				r.results = append(r.results, ChannelMonitorV3GroupProbeResult{GroupID: 4, Model: "gpt-mini", Slot: slot, FinishedAt: &finished,
					Outcome: "success", InputTokens: 99999, CacheInputTokens: &input, CacheReadTokens: &read})
			}
			if tc.change != nil {
				tc.change(r)
			}
			svc := NewChannelMonitorV3Service(r, nil)
			svc.now = func() time.Time { return now }
			// When public aggregate metrics are built.
			out, err := svc.PlazaStatuses(context.Background(), []int64{4}, false)
			require.NoError(t, err)
			// Then cache uses its own qualification, source and denominator.
			require.Equal(t, tc.state, out[4].ProbeCacheState)
			require.Equal(t, "group_probe", out[4].ProbeCacheScope)
			require.Equal(t, "weighted_tokens", out[4].ProbeCacheStatistic)
			require.Equal(t, tc.rate, out[4].ProbeCacheReadRate)
			require.Nil(t, out[4].CachePeakHourRate, "synthetic checks cannot enter real-traffic cache")
			data, err := json.Marshal(out[4])
			require.NoError(t, err)
			for _, private := range []string{"cache_input_tokens", "cache_read_tokens", "account_id", "probe_model", "gpt-mini"} {
				require.NotContains(t, string(data), private)
			}
		})
	}
}

func probeCacheRatePtr(value float64) *float64 { return &value }
