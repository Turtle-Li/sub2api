package service

import (
	"context"
	"errors"
	"sort"
	"time"
)

const (
	ChannelMonitorV3GroupWindowMinutes = 180
	ChannelMonitorV3GroupMinRequests   = 20
	ChannelMonitorV3GroupMinProbes     = 6
	channelMonitorV3GroupCells         = 18
	channelMonitorV3GroupInterval      = ChannelMonitorV3ProbeInterval
)

type ChannelMonitorV3GroupCell struct {
	Start  time.Time `json:"start"`
	Status string    `json:"status"`
	Source string    `json:"source"`
}

// Published metrics contain no traffic volumes. Real requests and inexpensive
// scheduled checks always retain independent denominators.
type ChannelMonitorV3GroupStatus struct {
	Status             string                      `json:"status"`
	Source             string                      `json:"source"`
	CheckedAt          *time.Time                  `json:"checked_at"`
	ProbeEnabled       bool                        `json:"probe_enabled"`
	ProbeModel         string                      `json:"-"`
	DisplayName        string                      `json:"-"`
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
// public status publication. It never discovers groups or expands viewer scope.
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
			DisplayName: target.DisplayName, RequestSampleState: "none", ProbeSampleState: "none", WindowMinutes: ChannelMonitorV3GroupWindowMinutes, MinRequests: max(ChannelMonitorV3GroupMinRequests, cfg.MinRequests), Cells: make([]ChannelMonitorV3GroupCell, channelMonitorV3GroupCells)}
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
		observations := channelMonitorV3Accumulate(*cfg, component, facts, first, channelMonitorV3GroupCells, interval)
		for i := range item.Cells {
			item.Cells[i] = ChannelMonitorV3GroupCell{Start: first.Add(time.Duration(i) * interval), Status: ChannelMonitorV3StatusUnknown, Source: "none"}
			if cells[i] != nil {
				item.Cells[i].Status, item.Cells[i].Source = cells[i].Status, "requests"
				if cells[i].Status == ChannelMonitorV3StatusInsufficient {
					item.Cells[i].Status = "observed"
					if observations[i].errors > 0 {
						item.Cells[i].Status = "errors_observed"
					}
				}
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
		if item.Cells[len(item.Cells)-1].Source == "none" {
			item.Cells[len(item.Cells)-1].Status = "collecting"
		}
		out[id] = item
	}
	return out, nil
}

// Selection owns publication independently of the scheduled-check switch.
// Active public groups are the candidates, never an implicit publication list.
func (s *ChannelMonitorV3Service) PublicStatusGroups(ctx context.Context, anonymous bool) ([]Group, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if anonymous && !cfg.PublicEnabled {
		return nil, ErrChannelMonitorV3NotFound
	}
	selected := make(map[int64]bool, len(cfg.GroupProbe.Groups))
	for _, target := range cfg.GroupProbe.Groups {
		selected[target.GroupID] = target.IsListed()
	}
	catalog, ok := s.groups.(interface {
		ListActive(context.Context) ([]Group, error)
	})
	if !ok {
		return nil, errors.New("status group catalog unavailable")
	}
	groups, err := catalog.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	public := make([]Group, 0, len(groups))
	for _, g := range groups {
		if selected[g.ID] && g.Status == StatusActive && !g.IsExclusive {
			public = append(public, g)
		}
	}
	sort.SliceStable(public, func(i, j int) bool {
		if public[i].Platform != public[j].Platform {
			return public[i].Platform < public[j].Platform
		}
		return public[i].Name < public[j].Name
	})
	return public, nil
}
