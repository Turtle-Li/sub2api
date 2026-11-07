package service

import (
	"context"
	"time"
)

const (
	ChannelMonitorV3GroupWindowMinutes = 90
	ChannelMonitorV3GroupMinRequests   = 20
	ChannelMonitorV3GroupMinProbes     = 6
	channelMonitorV3GroupCells         = 18
	channelMonitorV3GroupInterval      = 5 * time.Minute
)

type ChannelMonitorV3GroupCell struct {
	Start  time.Time `json:"start"`
	Status string    `json:"status"`
	Source string    `json:"source"`
}

// Published metrics contain no traffic volumes. Real requests and inexpensive
// representative checks always retain independent denominators.
type ChannelMonitorV3GroupStatus struct {
	Status             string                      `json:"status"`
	Source             string                      `json:"source"`
	CheckedAt          *time.Time                  `json:"checked_at"`
	ProbeEnabled       bool                        `json:"probe_enabled"`
	ProbeModel         string                      `json:"probe_model,omitempty"`
	RequestSuccessRate *float64                    `json:"request_success_rate"`
	ProbeSuccessRate   *float64                    `json:"probe_success_rate"`
	CacheReadRate      *float64                    `json:"cache_read_rate"`
	TTFTP50Ms          *int64                      `json:"ttft_p50_ms"`
	RequestSampleState string                      `json:"request_sample_state"`
	ProbeSampleState   string                      `json:"probe_sample_state"`
	WindowMinutes      int                         `json:"window_minutes"`
	MinRequests        int                         `json:"min_requests"`
	Cells              []ChannelMonitorV3GroupCell `json:"cells"`
}

// PlazaStatuses accepts only IDs that the caller has already authorized for
// model-plaza publication. It never discovers groups or expands viewer scope.
func (s *ChannelMonitorV3Service) PlazaStatuses(ctx context.Context, groupIDs []int64, anonymous bool) (map[int64]*ChannelMonitorV3GroupStatus, error) {
	out := map[int64]*ChannelMonitorV3GroupStatus{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if anonymous && !cfg.PublicEnabled {
		return out, nil
	}
	now := s.now().UTC()
	interval := channelMonitorV3GroupInterval
	last := now.Truncate(interval)
	first := last.Add(-time.Duration(channelMonitorV3GroupCells-1) * interval)
	facts, err := s.repo.SlotFacts(ctx, groupIDs, first, last.Add(interval), interval, true)
	if err != nil {
		return nil, err
	}
	through, err := s.repo.DataThrough(ctx)
	if err != nil {
		return nil, err
	}
	passiveFresh := through != nil && !through.After(now) && now.Sub(*through) <= 10*time.Minute
	var probes []ChannelMonitorV3GroupProbeResult
	if repo, ok := s.repo.(ChannelMonitorV3GroupProbeRepository); ok {
		probes, err = repo.GroupProbes(ctx, groupIDs, first, now.Add(time.Nanosecond))
		if err != nil {
			return nil, err
		}
	}
	targets := map[int64]ChannelMonitorV3GroupProbeTarget{}
	for _, target := range cfg.GroupProbe.Groups {
		targets[target.GroupID] = target
	}
	byGroup := map[int64][]ChannelMonitorV3GroupProbeResult{}
	for _, probe := range probes {
		byGroup[probe.GroupID] = append(byGroup[probe.GroupID], probe)
	}
	for _, id := range groupIDs {
		target := targets[id]
		item := &ChannelMonitorV3GroupStatus{Status: ChannelMonitorV3StatusUnknown, Source: "none", ProbeEnabled: cfg.GroupProbe.Enabled && target.Enabled, ProbeModel: target.Model,
			RequestSampleState: "none", ProbeSampleState: "none", WindowMinutes: ChannelMonitorV3GroupWindowMinutes, MinRequests: max(ChannelMonitorV3GroupMinRequests, cfg.MinRequests), Cells: make([]ChannelMonitorV3GroupCell, channelMonitorV3GroupCells)}
		component := ChannelMonitorV3Component{GroupID: id}
		total := channelMonitorV3Accumulate(*cfg, component, facts, time.Time{}, 0)[0]
		if total.counted() > 0 {
			item.RequestSampleState = "insufficient"
		}
		if !passiveFresh {
			item.RequestSampleState = "stale"
		} else if total.counted() >= int64(item.MinRequests) {
			item.RequestSampleState = "ready"
			rate := 100 * float64(total.success) / float64(total.counted())
			item.RequestSuccessRate = &rate
			if input := total.input + total.cacheWrite + total.cacheRead; input > 0 {
				cache := 100 * float64(total.cacheRead) / float64(input)
				item.CacheReadRate = &cache
			}
			var latencySamples int64
			for _, n := range total.ttft {
				latencySamples += n
			}
			if latencySamples >= int64(item.MinRequests) {
				item.TTFTP50Ms = channelMonitorV3P50(total.ttft)
			}
		}
		cells := channelMonitorV3Cells(*cfg, component, facts, first, channelMonitorV3GroupCells, interval, false)
		for i := range item.Cells {
			item.Cells[i] = ChannelMonitorV3GroupCell{Start: first.Add(time.Duration(i) * interval), Status: ChannelMonitorV3StatusUnknown, Source: "none"}
			if cells[i] != nil {
				item.Cells[i].Status, item.Cells[i].Source = cells[i].Status, "requests"
			}
		}
		var successes, count int
		for _, probe := range byGroup[id] {
			if probe.Model != target.Model || probe.FinishedAt == nil || probe.FinishedAt.After(now) || (probe.Outcome != "success" && probe.Outcome != "failure") {
				continue
			}
			count++
			state := ChannelMonitorV3StatusDown
			if probe.Outcome == "success" {
				successes++
				state = ChannelMonitorV3StatusOperational
			}
			index := int(probe.Slot.Sub(first) / interval)
			if probe.Slot.Before(first) || index < 0 || index >= len(item.Cells) {
				continue
			}
			if item.Cells[index].Source != "requests" || item.Cells[index].Status == ChannelMonitorV3StatusInsufficient {
				item.Cells[index].Status, item.Cells[index].Source = state, "probe"
			}
		}
		if count > 0 {
			item.ProbeSampleState = "insufficient"
		}
		if !item.ProbeEnabled {
			item.ProbeSampleState = "disabled"
		} else if count >= ChannelMonitorV3GroupMinProbes {
			rate := 100 * float64(successes) / float64(count)
			item.ProbeSuccessRate, item.ProbeSampleState = &rate, "ready"
		}
		for i := len(item.Cells) - 1; i >= 0; i-- {
			cell := item.Cells[i]
			if cell.Source == "none" || cell.Status == ChannelMonitorV3StatusInsufficient {
				continue
			}
			if now.Sub(cell.Start) > 15*time.Minute {
				break
			}
			if cell.Source == "requests" && !passiveFresh || cell.Source == "probe" && !item.ProbeEnabled {
				continue
			}
			item.Status, item.Source = cell.Status, cell.Source
			at := cell.Start
			item.CheckedAt = &at
			if cell.Source == "probe" {
				for _, probe := range byGroup[id] {
					if probe.Model == target.Model && probe.Slot.Equal(cell.Start) && probe.FinishedAt != nil && !probe.FinishedAt.After(now) {
						item.CheckedAt = probe.FinishedAt
					}
				}
			}
			break
		}
		// A fresh check remains useful when the real-traffic collector is stale.
		if item.Source == "none" && item.ProbeEnabled {
			for _, probe := range byGroup[id] {
				if probe.Model != target.Model || probe.FinishedAt == nil || now.Sub(*probe.FinishedAt) > 15*time.Minute || probe.FinishedAt.After(now) || (probe.Outcome != "success" && probe.Outcome != "failure") {
					continue
				}
				if item.CheckedAt != nil && !probe.FinishedAt.After(*item.CheckedAt) {
					continue
				}
				item.Source, item.CheckedAt = "probe", probe.FinishedAt
				item.Status = ChannelMonitorV3StatusDown
				if probe.Outcome == "success" {
					item.Status = ChannelMonitorV3StatusOperational
				}
			}
		}
		out[id] = item
	}
	return out, nil
}
