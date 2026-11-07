package service

import (
	"context"
	"time"
)

type ChannelMonitorV3GroupCell struct {
	Start  time.Time `json:"start"`
	Status string    `json:"status"`
	Source string    `json:"source"`
}

// Deliberately separate denominators. An inexpensive representative check
// cannot certify every model in a group or manufacture real-user successes.
type ChannelMonitorV3GroupStatus struct {
	Status             string                      `json:"status"`
	Source             string                      `json:"source"`
	CheckedAt          *time.Time                  `json:"checked_at"`
	ProbeEnabled       bool                        `json:"probe_enabled"`
	ProbeModel         string                      `json:"probe_model,omitempty"`
	RequestSuccessRate *float64                    `json:"request_success_rate"`
	ProbeSuccessRate   *float64                    `json:"probe_success_rate"`
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
	interval := ChannelMonitorV3ProbeInterval
	last := now.Truncate(interval)
	first := last.Add(-35 * interval)
	facts, err := s.repo.SlotFacts(ctx, groupIDs, first, last.Add(interval), interval, false)
	if err != nil {
		return nil, err
	}
	totals, err := s.repo.RangeFacts(ctx, groupIDs, now.Add(-24*time.Hour).Truncate(time.Hour), now)
	if err != nil {
		return nil, err
	}
	through, err := s.repo.DataThrough(ctx)
	if err != nil {
		return nil, err
	}
	passiveFresh := through != nil && now.Sub(*through) <= 10*time.Minute
	var probes []ChannelMonitorV3GroupProbeResult
	if repo, ok := s.repo.(ChannelMonitorV3GroupProbeRepository); ok {
		probes, err = repo.GroupProbes(ctx, groupIDs, now.Add(-24*time.Hour), now.Add(time.Nanosecond))
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
		item := &ChannelMonitorV3GroupStatus{Status: ChannelMonitorV3StatusUnknown, Source: "none", ProbeEnabled: cfg.GroupProbe.Enabled && target.Enabled, ProbeModel: target.Model, Cells: make([]ChannelMonitorV3GroupCell, 36)}
		component := ChannelMonitorV3Component{GroupID: id}
		total := channelMonitorV3Accumulate(*cfg, component, totals, time.Time{}, 0)[0]
		if count := total.counted(); count > 0 {
			rate := 100 * float64(total.success) / float64(count)
			item.RequestSuccessRate = &rate
		}
		cells := channelMonitorV3Cells(*cfg, component, facts, first, 36, interval, false)
		for i := range item.Cells {
			item.Cells[i] = ChannelMonitorV3GroupCell{Start: first.Add(time.Duration(i) * interval), Status: ChannelMonitorV3StatusUnknown, Source: "none"}
			if cells[i] != nil {
				item.Cells[i].Status = cells[i].Status
				item.Cells[i].Source = "requests"
			}
		}
		var successes, count int
		for _, probe := range byGroup[id] {
			// Model changes do not lend the previous model's results to the new one.
			if probe.Model != target.Model || probe.FinishedAt == nil || (probe.Outcome != "success" && probe.Outcome != "failure") {
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
				item.Cells[index].Status = state
				item.Cells[index].Source = "probe"
			}
		}
		if count > 0 {
			rate := 100 * float64(successes) / float64(count)
			item.ProbeSuccessRate = &rate
		}
		for i := len(item.Cells) - 1; i >= 0; i-- {
			cell := item.Cells[i]
			if cell.Source == "none" || cell.Status == ChannelMonitorV3StatusInsufficient {
				continue
			}
			if now.Sub(cell.Start) > 15*time.Minute {
				break
			}
			if cell.Source == "requests" && !passiveFresh {
				continue
			}
			if cell.Source == "probe" && !item.ProbeEnabled {
				continue
			}
			item.Status, item.Source = cell.Status, cell.Source
			at := cell.Start
			item.CheckedAt = &at
			if cell.Source == "probe" {
				for _, probe := range byGroup[id] {
					if probe.Model == target.Model && probe.Slot.Equal(cell.Start) && probe.FinishedAt != nil {
						item.CheckedAt = probe.FinishedAt
					}
				}
			}
			break
		}
		// Historical request cells retain their own source even during a collector
		// outage. A fresh independent probe can still establish current readiness.
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
