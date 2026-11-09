package service

import (
	"context"
	"slices"
	"time"
)

// Binding identities stay private. Equal URLs alone cannot prove equal account
// pools, authorization, quota or egress. Recheck bindings on every status read.
type channelMonitorV3ProbePoolReader interface {
	GroupProbePools(context.Context, []int64) (map[int64][]int64, error)
}

type channelMonitorV3SharedProbeSource struct {
	target ChannelMonitorV3GroupProbeTarget
	name   string
}

func channelMonitorV3SameProbePool(a, b []int64) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func (s *ChannelMonitorV3Service) validateSharedGroupProbes(ctx context.Context, cfg ChannelMonitorV3GroupProbeConfig) error {
	targets := make(map[int64]ChannelMonitorV3GroupProbeTarget, len(cfg.Groups))
	ids := make([]int64, 0, len(cfg.Groups))
	for _, t := range cfg.Groups {
		targets[t.GroupID] = t
		ids = append(ids, t.GroupID)
	}
	var active []ChannelMonitorV3GroupProbeTarget
	for _, t := range cfg.Groups {
		if t.SourceGroupID == 0 || !t.IsListed() {
			continue
		}
		source, exists := targets[t.SourceGroupID]
		if !exists || source.GroupID == t.GroupID || source.SourceGroupID != 0 {
			return channelMonitorV3Invalid("shared check source must be another independent configured group")
		}
		// Removing/pausing a source preserves the reference without starting a
		// replacement paid check. An inactive reference publishes no observation.
		if cfg.Enabled && t.IsListed() && source.IsListed() && source.Enabled {
			active = append(active, t)
		}
	}
	if len(active) == 0 {
		return nil
	}
	reader, ok := s.repo.(channelMonitorV3ProbePoolReader)
	if !ok {
		return channelMonitorV3Invalid("shared check pool lookup unavailable")
	}
	pools, err := reader.GroupProbePools(ctx, ids)
	if err != nil {
		return err
	}
	var previous *ChannelMonitorV3Config
	for _, t := range active {
		if !channelMonitorV3SameProbePool(pools[t.GroupID], pools[t.SourceGroupID]) {
			if previous == nil {
				previous, err = s.repo.GetConfig(ctx)
				if err != nil {
					return err
				}
			}
			// Preserve an already-active relationship after bindings are revoked.
			// Every status read still rejects it; unrelated saves must remain usable.
			if !channelMonitorV3SharedProbeWasActive(previous, t) {
				return channelMonitorV3Invalid("shared check group %d and source %d must use the same nonempty public account pool", t.GroupID, t.SourceGroupID)
			}
		}
	}
	return nil
}

func channelMonitorV3SharedProbeWasActive(cfg *ChannelMonitorV3Config, target ChannelMonitorV3GroupProbeTarget) bool {
	if cfg == nil || !cfg.GroupProbe.Enabled {
		return false
	}
	var follower, source bool
	for _, previous := range cfg.GroupProbe.Groups {
		follower = follower || previous.GroupID == target.GroupID && previous.IsListed() && !previous.Enabled && previous.SourceGroupID == target.SourceGroupID
		source = source || previous.GroupID == target.SourceGroupID && previous.IsListed() && previous.Enabled && previous.SourceGroupID == 0
	}
	return follower && source
}

func (s *ChannelMonitorV3Service) sharedProbeSources(ctx context.Context, cfg ChannelMonitorV3GroupProbeConfig, ids []int64) map[int64]channelMonitorV3SharedProbeSource {
	out := map[int64]channelMonitorV3SharedProbeSource{}
	if !cfg.Enabled || s.groups == nil {
		return out
	}
	targets := make(map[int64]ChannelMonitorV3GroupProbeTarget, len(cfg.Groups))
	visible := make(map[int64]bool, len(ids))
	for _, t := range cfg.Groups {
		targets[t.GroupID] = t
	}
	needed := false
	for _, id := range ids {
		visible[id] = true
		needed = needed || targets[id].SourceGroupID > 0
	}
	reader, ok := s.repo.(channelMonitorV3ProbePoolReader)
	if !needed || !ok {
		return out
	}
	pools, err := reader.GroupProbePools(ctx, ids)
	if err != nil {
		return out
	}
	for _, id := range ids {
		t := targets[id]
		source, exists := targets[t.SourceGroupID]
		if t.SourceGroupID <= 0 || t.Enabled || !t.IsListed() || !exists || source.GroupID == id || !visible[source.GroupID] || !source.IsListed() || !source.Enabled || source.SourceGroupID != 0 || !channelMonitorV3SameProbePool(pools[id], pools[source.GroupID]) {
			continue
		}
		group, err := s.groups.GetByID(ctx, source.GroupID)
		if err != nil || group == nil || group.Status != StatusActive || !channelMonitorV3ProbeGroupSupported(group) || !group.ModelAllowlist.Allows(source.Model) || source.Model == "" || channelMonitorV3ProbeMediaModel(source.Model) {
			continue
		}
		name := source.DisplayName
		if name == "" {
			name = group.Name
		}
		out[id] = channelMonitorV3SharedProbeSource{target: source, name: name}
	}
	return out
}

// A source model's success cannot clear a different model's observed failures.
func channelMonitorV3PreferOwnSharedErrors(item *ChannelMonitorV3GroupStatus, now time.Time, passiveFresh bool) {
	if !item.SharedProbe || !passiveFresh {
		return
	}
	for i := len(item.Cells) - 1; i >= 0; i-- {
		cell := item.Cells[i]
		if now.Sub(cell.Start) > 15*time.Minute {
			return
		}
		if cell.Source != "requests" {
			continue
		}
		if cell.Status == ChannelMonitorV3StatusDown || cell.Status == ChannelMonitorV3StatusDegraded || cell.Status == "errors_observed" {
			item.Status, item.Source = cell.Status, cell.Source
			at := cell.Start
			item.CheckedAt = &at
		}
		return
	}
}
