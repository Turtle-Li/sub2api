package service

import (
	"context"
	"sort"
	"time"
)

func (s *ChannelMonitorV3Service) Incidents(ctx context.Context, viewer ChannelMonitorV3Viewer, page, pageSize int) (*ChannelMonitorV3IncidentPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > channelMonitorV3IncidentPage {
		pageSize = 20
	}
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	visible, groupIDs, err := s.visibleComponents(ctx, viewer)
	if err != nil {
		return nil, err
	}
	all, first, err := s.incidentRuns(ctx, *cfg, visible, groupIDs, viewer.Admin)
	if err != nil {
		return nil, err
	}
	result := &ChannelMonitorV3IncidentPage{Items: []ChannelMonitorV3Incident{}, Page: page, PageSize: pageSize, Since: first}
	sort.SliceStable(all, func(i, j int) bool {
		if (all[i].EndedAt == nil) != (all[j].EndedAt == nil) {
			return all[i].EndedAt == nil
		}
		return all[i].StartedAt.After(all[j].StartedAt)
	})
	result.Total = len(all)
	if page-1 > len(all)/pageSize {
		return result, nil
	}
	from := min((page-1)*pageSize, len(all))
	result.Items = all[from:min(from+pageSize, len(all))]
	return result, nil
}

func channelMonitorV3Runs(cfg ChannelMonitorV3Config, component ChannelMonitorV3Component, facts *ChannelMonitorV3Facts, first time.Time, count int, interval time.Duration, now time.Time, admin bool) []ChannelMonitorV3Incident {
	return channelMonitorV3RunsFromCells(component, channelMonitorV3Cells(cfg, component, facts, first, count, interval, true), interval, now, admin)
}

// channelMonitorV3RunsFromCells turns down slots into incidents. Quiet slots
// (too little traffic) prove nothing either way. Only an observed non-down
// slot resolves an incident; silence never establishes recovery.
// A run red for less than ChannelMonitorV3IncidentMinDuration is a blip, not
// an incident. A run whose last down slot is that recent is still ongoing.
func channelMonitorV3RunsFromCells(component ChannelMonitorV3Component, cells []*ChannelMonitorV3Cell, interval time.Duration, now time.Time, admin bool) []ChannelMonitorV3Incident {
	runs := []ChannelMonitorV3Incident{}
	var run *ChannelMonitorV3Incident
	var redUntil time.Time // end of the run's last down slot
	categories := map[string]int{}
	closeRun := func(recoveredAt *time.Time) {
		if run == nil {
			return
		}
		// The newest down slot only counts up to now while it is still filling up.
		redEnd := redUntil
		if redEnd.After(now) {
			redEnd = now
		}
		if redEnd.Sub(run.StartedAt) >= ChannelMonitorV3IncidentMinDuration {
			run.EndedAt = recoveredAt
			run.TopError = channelMonitorV3TopError(channelMonitorV3Counts(categories))
			if !admin {
				run.Requests, run.Errors = 0, 0
			}
			runs = append(runs, *run)
		}
		run = nil
		categories = map[string]int{}
	}
	for _, cell := range cells {
		if cell == nil || cell.Status == ChannelMonitorV3StatusInsufficient || cell.Status == ChannelMonitorV3StatusUnknown {
			continue
		}
		if run != nil && cell.Status != ChannelMonitorV3StatusDown {
			recoveredAt := cell.Start
			closeRun(&recoveredAt)
		}
		if cell.Status != ChannelMonitorV3StatusDown {
			continue
		}
		if run == nil {
			run = &ChannelMonitorV3Incident{ComponentID: component.ID, ComponentName: component.Name, StartedAt: cell.Start}
		}
		run.DownSlots++
		redUntil = cell.Start.Add(interval)
		run.Requests += cell.Requests
		run.Errors += cell.Errors
		if cell.TopError != "" {
			categories[cell.TopError]++
		}
	}
	closeRun(nil)
	return runs
}

func channelMonitorV3Counts(in map[string]int) map[string]int64 {
	out := make(map[string]int64, len(in))
	for key, value := range in {
		out[key] = int64(value)
	}
	return out
}

func (s *ChannelMonitorV3Service) incidentRuns(ctx context.Context, cfg ChannelMonitorV3Config, visible []ChannelMonitorV3Component, groupIDs []int64, admin bool) ([]ChannelMonitorV3Incident, time.Time, error) {
	now := s.now().UTC()
	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	first := now.Add(-ChannelMonitorV3History).Truncate(interval).Add(interval)
	count := int(now.Truncate(interval).Sub(first)/interval) + 1

	if len(visible) == 0 {
		return []ChannelMonitorV3Incident{}, first, nil
	}
	// Down depends on error rates only, so the history skips latency histograms.
	facts, err := s.repo.SlotFacts(ctx, groupIDs, first, first.Add(time.Duration(count)*interval), interval, false)
	if err != nil {
		return nil, first, err
	}
	all := []ChannelMonitorV3Incident{}
	for _, component := range visible {
		all = append(all, channelMonitorV3Runs(cfg, component, facts, first, count, interval, now, admin)...)
	}
	return all, first, nil
}
