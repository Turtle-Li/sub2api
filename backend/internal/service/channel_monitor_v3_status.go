package service

import (
	"context"
	"slices"
	"time"
)

// visibleComponents returns the enabled components the viewer may see and their groups.
func (s *ChannelMonitorV3Service) visibleComponents(ctx context.Context, viewer ChannelMonitorV3Viewer) ([]ChannelMonitorV3Component, []int64, error) {
	if viewer.Anonymous {
		cfg, err := s.repo.GetConfig(ctx)
		if err != nil {
			return nil, nil, err
		}
		if !cfg.PublicEnabled {
			return nil, nil, ErrChannelMonitorV3NotFound
		}
	}
	components, err := s.repo.ListComponents(ctx)
	if err != nil {
		return nil, nil, err
	}
	visible := make([]ChannelMonitorV3Component, 0, len(components))
	groups := []int64{}
	for _, component := range components {
		if component.Enabled && viewer.canSee(component) {
			visible = append(visible, component)
			if !slices.Contains(groups, component.GroupID) {
				groups = append(groups, component.GroupID)
			}
		}
	}
	return visible, groups, nil
}

// Status builds the page for one viewer. end selects an older window by its
// last slot; nil or a future time means the latest window.
func (s *ChannelMonitorV3Service) Status(ctx context.Context, viewer ChannelMonitorV3Viewer, end *time.Time) (*ChannelMonitorV3Status, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	categories, err := s.repo.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	visible, groupIDs, err := s.visibleComponents(ctx, viewer)
	if err != nil {
		return nil, err
	}
	through, err := s.repo.DataThrough(ctx)
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	current := now.Truncate(interval)
	oldest := now.Add(-ChannelMonitorV3History).Truncate(interval).Add(interval)
	last := current
	if end != nil {
		if requested := end.UTC().Truncate(interval); requested.Before(current) {
			last = requested
		}
	}
	// Never page past the facts V2 still keeps.
	if minLast := oldest.Add(time.Duration(cfg.Cells-1) * interval); last.Before(minLast) {
		last = minLast
	}
	first := last.Add(-time.Duration(cfg.Cells-1) * interval)
	window := ChannelMonitorV3Window{Start: first, End: last.Add(interval), Latest: !last.Before(current), HasOlder: first.After(oldest)}

	rangeFrom := now.Add(-channelMonitorV3Ranges[cfg.AvailabilityRange]).Truncate(time.Hour)
	status := &ChannelMonitorV3Status{
		GeneratedAt: now, IntervalMinutes: cfg.IntervalMinutes, Cells: cfg.Cells,
		AvailabilityRange: cfg.AvailabilityRange, AvailabilitySince: rangeFrom,
		DownErrorRate: cfg.DownErrorRate, DegradedErrorRate: cfg.DegradedErrorRate, DegradedTTFTMs: cfg.DegradedTTFTMs,
		MinRequests: cfg.MinRequests, DataThrough: through, FooterNote: cfg.FooterNote, Window: window,
		Categories: []ChannelMonitorV3CategoryStatus{},
	}
	if len(visible) == 0 {
		return status, nil
	}

	facts, err := s.repo.SlotFacts(ctx, groupIDs, first, window.End, interval, true)
	if err != nil {
		return nil, err
	}
	totals, err := s.repo.RangeFacts(ctx, groupIDs, rangeFrom, now)
	if err != nil {
		return nil, err
	}
	var latestFacts *ChannelMonitorV3Facts
	latestFirst := current.Add(-time.Duration(cfg.Cells-1) * interval)
	if !window.Latest {
		latestFacts, err = s.repo.SlotFacts(ctx, groupIDs, latestFirst, current.Add(interval), interval, true)
		if err != nil {
			return nil, err
		}
	}

	byCategory := map[int64][]ChannelMonitorV3ComponentStatus{}
	for _, component := range visible {
		item := s.componentStatus(*cfg, component, facts, totals, first, interval, viewer)
		if !window.Latest {
			live := s.componentStatus(*cfg, component, latestFacts, totals, latestFirst, interval, viewer)
			item.Status, item.LastDataAt = live.Status, live.LastDataAt
		}
		if through == nil || now.Sub(*through) > 10*time.Minute {
			item.Status = ChannelMonitorV3StatusUnknown
		}
		if cfg.FeaturedComponentID != nil && *cfg.FeaturedComponentID == component.ID {
			featured := item
			if !window.Latest {
				// The featured card always shows the current window.
				if latestFacts == nil {
					if latestFacts, err = s.repo.SlotFacts(ctx, []int64{component.GroupID}, latestFirst, current.Add(interval), interval, true); err != nil {
						return nil, err
					}
				}
				featured = s.componentStatus(*cfg, component, latestFacts, totals, latestFirst, interval, viewer)
			}
			status.Featured = &featured
			if through == nil || now.Sub(*through) > 10*time.Minute {
				status.Featured.Status = ChannelMonitorV3StatusUnknown
			}
		}
		if component.CategoryID != nil {
			byCategory[*component.CategoryID] = append(byCategory[*component.CategoryID], item)
		}
	}
	runs, _, err := s.incidentRuns(ctx, *cfg, visible, groupIDs, false)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if run.EndedAt == nil {
			status.OpenIncidents++
		}
	}
	for _, category := range categories {
		items := byCategory[category.ID]
		if len(items) == 0 {
			continue
		}
		status.Categories = append(status.Categories, ChannelMonitorV3CategoryStatus{
			ID: category.ID, Name: category.Name, Description: category.Description,
			Availability: channelMonitorV3MeanAvailability(items), Components: items,
		})
	}
	return status, nil
}

func (s *ChannelMonitorV3Service) componentStatus(cfg ChannelMonitorV3Config, component ChannelMonitorV3Component, facts, totals *ChannelMonitorV3Facts, first time.Time, interval time.Duration, viewer ChannelMonitorV3Viewer) ChannelMonitorV3ComponentStatus {
	item := ChannelMonitorV3ComponentStatus{
		ID: component.ID, Name: component.Name, Description: component.Description, Model: component.Model,
		Status: ChannelMonitorV3StatusUnknown,
		Cells:  channelMonitorV3Cells(cfg, component, facts, first, cfg.Cells, interval, viewer.Admin),
	}
	for i := len(item.Cells) - 1; i >= 0; i-- {
		cell := item.Cells[i]
		if cell == nil {
			continue
		}
		if item.LastDataAt == nil {
			at := cell.Start
			item.LastDataAt = &at
		}
		if cell.Status != ChannelMonitorV3StatusInsufficient {
			if s.now().UTC().Sub(cell.Start) <= channelMonitorV3Freshness(interval) {
				item.Status = cell.Status
			}
			break
		}
	}
	total := channelMonitorV3Accumulate(cfg, component, totals, time.Time{}, 0)[0]
	if counted := total.counted(); counted > 0 {
		availability := float64(total.success) * 100 / float64(counted)
		item.Availability = &availability
		if viewer.Admin {
			item.Requests = counted
		}
	}
	if component.ShowMultiplier && !viewer.Anonymous {
		rate := component.GroupRateMultiplier
		if custom, ok := viewer.GroupRates[component.GroupID]; ok {
			rate = custom
		}
		item.Multiplier = &rate
	}
	if viewer.Admin {
		item.GroupID = component.GroupID
	}
	return item
}

func channelMonitorV3Freshness(interval time.Duration) time.Duration {
	if interval*2 > 10*time.Minute {
		return interval * 2
	}
	return 10 * time.Minute
}
