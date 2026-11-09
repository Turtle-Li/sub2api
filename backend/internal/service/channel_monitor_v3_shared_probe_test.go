package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type sharedProbeRepoFake struct {
	*groupProbeRepoFake
	pools   map[int64][]int64
	poolErr error
}

func (r *sharedProbeRepoFake) GroupProbePools(context.Context, []int64) (map[int64][]int64, error) {
	return r.pools, r.poolErr
}

func sharedProbeFixture() (*sharedProbeRepoFake, *ChannelMonitorV3Service, time.Time) {
	r, _, _ := groupProbeFixture()
	listed := true
	r.config.GroupProbe.Groups = []ChannelMonitorV3GroupProbeTarget{
		{GroupID: 4, Model: "deepseek-small", Enabled: true, Listed: &listed, DisplayName: "Upstream check"},
		{GroupID: 5, Model: "kimi-saved", Listed: &listed, SourceGroupID: 4},
	}
	now := time.Date(2026, 11, 9, 8, 2, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		slot := now.Truncate(ChannelMonitorV3ProbeInterval).Add(-time.Duration(i) * ChannelMonitorV3ProbeInterval)
		finished := slot.Add(time.Second)
		r.results = append(r.results, ChannelMonitorV3GroupProbeResult{GroupID: 4, Model: "deepseek-small", Slot: slot, FinishedAt: &finished,
			Outcome: "success", LatencyMs: 1000, FirstTokenMs: probeTimingPtr(200), InputTokens: 100, OutputTokens: 1,
			CacheInputTokens: probeTimingPtr(100), CacheReadTokens: probeTimingPtr(97)})
	}
	repo := &sharedProbeRepoFake{groupProbeRepoFake: r, pools: map[int64][]int64{4: {246}, 5: {246}}}
	s := NewChannelMonitorV3Service(repo, monitorV3GroupsFake{
		4: {ID: 4, Name: "DeepSeek", Platform: PlatformDeepseek, Status: StatusActive},
		5: {ID: 5, Name: "Kimi", Platform: PlatformKimi, Status: StatusActive},
	})
	s.now = func() time.Time { return now }
	return repo, s, now
}

func TestChannelMonitorV3SharedProbeUsesOneRealCheck(t *testing.T) {
	r, s, _ := sharedProbeFixture()
	out, err := s.PlazaStatuses(context.Background(), []int64{4, 5}, false)
	require.NoError(t, err)
	require.Equal(t, "shared_probe", out[5].Source)
	require.Equal(t, ChannelMonitorV3StatusOperational, out[5].Status)
	require.Equal(t, "shared_probe", out[5].Cells[len(out[5].Cells)-1].Source)
	require.NotNil(t, out[5].ProbeSuccessRate)
	require.InDelta(t, 100.0, *out[5].ProbeSuccessRate, 1e-9)
	require.Len(t, r.results, 6, "inherited observations must not create paid-attempt rows")
}

func TestChannelMonitorV3SharedProbeNeverBorrowsCacheOrLatency(t *testing.T) {
	r, s, now := sharedProbeFixture()
	r.requestCache = map[int64][]ChannelMonitorV3RequestCache{
		4: {{HourStart: now.Truncate(time.Hour).Add(-time.Hour), Requests: 30, InputTokens: 3, CacheReadTokens: 97}},
		5: {{HourStart: now.Truncate(time.Hour).Add(-time.Hour), Requests: 30, InputTokens: 20, CacheReadTokens: 80}},
	}
	out, err := s.PlazaStatuses(context.Background(), []int64{4, 5}, false)
	require.NoError(t, err)
	require.InDelta(t, 80.0, *out[5].CachePeakHourRate, 1e-9)
	require.Nil(t, out[5].ProbeCacheReadRate)
	require.Nil(t, out[5].ProbeCacheVerifiedAt)
	require.Nil(t, out[5].ProbeTTFTP50Ms)
	require.Nil(t, out[5].ProbeLatencyP50Ms)
}

func TestChannelMonitorV3SharedProbePreservesOwnFreshFailure(t *testing.T) {
	r, s, now := sharedProbeFixture()
	r.config.MinRequests = 20
	through := now.Add(-time.Minute)
	r.through = &through
	r.slotFacts.Metrics = []ChannelMonitorV3Fact{{GroupID: 5, Slot: now.Truncate(ChannelMonitorV3ProbeInterval).Add(-ChannelMonitorV3ProbeInterval), Errors: 1}}
	out, err := s.PlazaStatuses(context.Background(), []int64{4, 5}, false)
	require.NoError(t, err)
	require.Equal(t, "requests", out[5].Source)
	require.Equal(t, "errors_observed", out[5].Status, "a newer success on another model must not clear this model's failure")
}

func TestChannelMonitorV3SharedProbePropagatesSourceFailure(t *testing.T) {
	r, s, _ := sharedProbeFixture()
	r.results[0].Outcome = "failure"
	out, err := s.PlazaStatuses(context.Background(), []int64{4, 5}, false)
	require.NoError(t, err)
	require.Equal(t, ChannelMonitorV3StatusDown, out[5].Status)
	require.Equal(t, "shared_probe", out[5].Source)
	require.Equal(t, "Upstream check", out[5].SharedProbeSourceName)
}

func TestChannelMonitorV3SharedProbeRunnerNeverSubmitsFollower(t *testing.T) {
	r, p, exec := groupProbeFixture()
	// A malformed configuration still cannot start a follower's independent check.
	for id := int64(5); id < 17; id++ {
		r.config.GroupProbe.Groups = append(r.config.GroupProbe.Groups, ChannelMonitorV3GroupProbeTarget{GroupID: id, Model: "retained", Enabled: true, SourceGroupID: 4})
	}
	p.groups = monitorV3GroupsFake{4: {ID: 4, Platform: PlatformOpenAI, Status: StatusActive}, 5: {ID: 5, Platform: PlatformKimi, Status: StatusActive}}
	p.RunDue(context.Background(), time.Now())
	require.Equal(t, 1, exec.calls)
	require.Len(t, r.results, 1)
	require.Equal(t, int64(4), r.results[0].GroupID)
}

func TestChannelMonitorV3SharedProbeConfigAcceptsMatchingPools(t *testing.T) {
	r, s, _ := sharedProbeFixture()
	require.NoError(t, s.validateGroupProbes(context.Background(), &r.config.GroupProbe))
	require.Equal(t, "kimi-saved", r.config.GroupProbe.Groups[1].Model)
	require.False(t, r.config.GroupProbe.Groups[1].Enabled)
}

func TestChannelMonitorV3SharedProbeRemovedReferenceCannotBlockSave(t *testing.T) {
	r, s, _ := sharedProbeFixture()
	hidden := false
	r.config.GroupProbe.Groups[1].Listed = &hidden
	r.config.GroupProbe.Groups[0].SourceGroupID = 6
	r.config.GroupProbe.Groups[0].Enabled = false
	r.config.GroupProbe.Groups = append(r.config.GroupProbe.Groups, ChannelMonitorV3GroupProbeTarget{GroupID: 6, Enabled: false})
	require.NoError(t, s.validateGroupProbes(context.Background(), &r.config.GroupProbe))
}

func TestChannelMonitorV3SharedProbeRetainsRevokedBindingWithoutBlockingSave(t *testing.T) {
	r, s, _ := sharedProbeFixture()
	r.pools[5] = []int64{247}
	input := r.config.GroupProbe
	input.Groups = append([]ChannelMonitorV3GroupProbeTarget(nil), input.Groups...)
	input.Groups[1].DisplayName = "Renamed"
	require.NoError(t, s.validateGroupProbes(context.Background(), &input))
	out, err := s.PlazaStatuses(context.Background(), []int64{4, 5}, false)
	require.NoError(t, err)
	require.False(t, out[5].ProbeEnabled)
	require.Equal(t, ChannelMonitorV3StatusUnknown, out[5].Status)
}

func TestChannelMonitorV3SharedProbeReactivationRechecksPools(t *testing.T) {
	for _, paused := range []string{"master", "source", "follower"} {
		t.Run(paused, func(t *testing.T) {
			r, s, _ := sharedProbeFixture()
			input := r.config.GroupProbe
			input.Groups = append([]ChannelMonitorV3GroupProbeTarget(nil), input.Groups...)
			r.pools[5] = nil
			switch paused {
			case "master":
				r.config.GroupProbe.Enabled = false
			case "source":
				r.config.GroupProbe.Groups[0].Enabled = false
			case "follower":
				hidden := false
				r.config.GroupProbe.Groups[1].Listed = &hidden
			}
			require.Error(t, s.validateGroupProbes(context.Background(), &input))
		})
	}
}

func TestChannelMonitorV3SharedProbeFailsClosed(t *testing.T) {
	for _, name := range []string{"different pool", "empty pool", "pool read error", "paused source", "removed source", "chain", "source not visible", "stale"} {
		t.Run(name, func(t *testing.T) {
			r, s, _ := sharedProbeFixture()
			ids := []int64{4, 5}
			switch name {
			case "different pool":
				r.pools[5] = []int64{247}
			case "empty pool":
				r.pools[5] = nil
			case "pool read error":
				r.poolErr = errors.New("unavailable")
			case "paused source":
				r.config.GroupProbe.Groups[0].Enabled = false
			case "removed source":
				hidden := false
				r.config.GroupProbe.Groups[0].Listed = &hidden
			case "chain":
				r.config.GroupProbe.Groups[0].SourceGroupID = 5
			case "source not visible":
				ids = []int64{5}
			case "stale":
				s.now = func() time.Time { return r.results[0].Slot.Add(20 * time.Minute) }
			}
			out, err := s.PlazaStatuses(context.Background(), ids, false)
			require.NoError(t, err)
			require.Equal(t, ChannelMonitorV3StatusUnknown, out[5].Status)
			require.Nil(t, out[5].ProbeSuccessRate)
		})
	}
}

func TestChannelMonitorV3SharedProbeConfigRejectsInvalidReferences(t *testing.T) {
	for _, name := range []string{"self", "chain", "missing", "different pool", "also independent", "negative"} {
		t.Run(name, func(t *testing.T) {
			r, s, _ := sharedProbeFixture()
			input := r.config.GroupProbe
			input.Groups = append([]ChannelMonitorV3GroupProbeTarget(nil), input.Groups...)
			r.config.GroupProbe.Groups[1].SourceGroupID = 0
			switch name {
			case "self":
				input.Groups[1].SourceGroupID = 5
			case "chain":
				input.Groups[0].SourceGroupID = 5
				input.Groups[0].Enabled = false
			case "missing":
				input.Groups[1].SourceGroupID = 99
			case "different pool":
				r.pools[5] = []int64{247}
			case "also independent":
				input.Groups[1].Enabled = true
			case "negative":
				input.Groups[1].SourceGroupID = -1
			}
			require.Error(t, s.validateGroupProbes(context.Background(), &input))
		})
	}
}

func TestChannelMonitorV3SharedProbeCannotContinueOldIndependentRetry(t *testing.T) {
	r, p, exec := groupProbeFixture()
	exec.steps = []ChannelMonitorV3GroupProbeResult{{Outcome: "failure", Category: "upstream_error"}}
	exec.after = func() { r.config.GroupProbe.Groups[0].SourceGroupID = 99 }
	p.RunDue(context.Background(), time.Now())
	require.Equal(t, 1, exec.calls, "a changed source invalidates the retry snapshot even before other flags change")
	require.Equal(t, "cancelled", r.results[0].Outcome)
}
