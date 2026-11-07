package service

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type ChannelMonitorV3Viewer struct {
	Anonymous bool
	Admin     bool
	// AllowedGroups are the groups the user may bind keys to.
	AllowedGroups map[int64]bool
	// GroupRates are the user's own multipliers, shown instead of the group's.
	GroupRates map[int64]float64
}

// canSee hides components of deleted or disabled groups from users; admins
// still see them, flagged as unavailable on the settings page.
func (v ChannelMonitorV3Viewer) canSee(c ChannelMonitorV3Component) bool {
	if v.Admin {
		return true
	}
	if c.GroupDeleted || (c.GroupStatus != "" && c.GroupStatus != StatusActive) || (v.Anonymous && c.GroupStatus != StatusActive) {
		return false
	}
	if v.Anonymous {
		return c.Visibility == ChannelMonitorV3VisibilityPublic
	}
	return c.Visibility == ChannelMonitorV3VisibilityPublic || v.AllowedGroups[c.GroupID]
}

type ChannelMonitorV3Repository interface {
	GetConfig(ctx context.Context) (*ChannelMonitorV3Config, error)
	// UpdateConfig returns nil when expectedVersion no longer matches.
	UpdateConfig(ctx context.Context, cfg ChannelMonitorV3Config, expectedVersion int, updatedBy int64) (*ChannelMonitorV3Config, error)

	ListCategories(ctx context.Context) ([]ChannelMonitorV3Category, error)
	CreateCategory(ctx context.Context, input ChannelMonitorV3CategoryInput) (*ChannelMonitorV3Category, error)
	// UpdateCategory and the other mutators return nil/false when the row is gone.
	UpdateCategory(ctx context.Context, id int64, input ChannelMonitorV3CategoryInput) (*ChannelMonitorV3Category, error)
	DeleteCategory(ctx context.Context, id int64) (bool, error)

	ListComponents(ctx context.Context) ([]ChannelMonitorV3Component, error)
	GetComponent(ctx context.Context, id int64) (*ChannelMonitorV3Component, error)
	CreateComponent(ctx context.Context, input ChannelMonitorV3ComponentInput) (*ChannelMonitorV3Component, error)
	UpdateComponent(ctx context.Context, id int64, input ChannelMonitorV3ComponentInput) (*ChannelMonitorV3Component, error)
	DeleteComponent(ctx context.Context, id int64) (bool, error)
	// Reorder assigns sort_order by position; unknown IDs are ignored.
	Reorder(ctx context.Context, categoryIDs, componentIDs []int64) error

	// SlotFacts sums V2's one-minute facts of the groups into interval slots of
	// [from, to); latency histograms are read only when asked for.
	SlotFacts(ctx context.Context, groupIDs []int64, from, to time.Time, interval time.Duration, withLatency bool) (*ChannelMonitorV3Facts, error)
	// RangeFacts sums V2's hourly rollups of the groups over [from, to), without slots or latency.
	RangeFacts(ctx context.Context, groupIDs []int64, from, to time.Time) (*ChannelMonitorV3Facts, error)
	// DataThrough is how far the passive aggregation has processed; nil before its first run.
	DataThrough(ctx context.Context) (*time.Time, error)
}

type channelMonitorV3Groups interface {
	GetByID(ctx context.Context, id int64) (*Group, error)
}

type ChannelMonitorV3Service struct {
	repo   ChannelMonitorV3Repository
	groups channelMonitorV3Groups
	now    func() time.Time
}

func NewChannelMonitorV3Service(repo ChannelMonitorV3Repository, groups channelMonitorV3Groups) *ChannelMonitorV3Service {
	return &ChannelMonitorV3Service{repo: repo, groups: groups, now: time.Now}
}

func (s *ChannelMonitorV3Service) Settings(ctx context.Context) (*ChannelMonitorV3Settings, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	categories, err := s.repo.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	components, err := s.repo.ListComponents(ctx)
	if err != nil {
		return nil, err
	}
	through, err := s.repo.DataThrough(ctx)
	if err != nil {
		return nil, err
	}
	return &ChannelMonitorV3Settings{Config: *cfg, Categories: categories, Components: components,
		ErrorCategories: ChannelMonitorV2ErrorCategories, DataThrough: through}, nil
}

func (s *ChannelMonitorV3Service) UpdateConfig(ctx context.Context, input ChannelMonitorV3Config, updatedBy int64) (*ChannelMonitorV3Config, error) {
	if err := normalizeChannelMonitorV3Config(&input); err != nil {
		return nil, err
	}
	if err := s.validateGroupProbes(ctx, &input.GroupProbe); err != nil {
		return nil, err
	}
	if input.FeaturedComponentID != nil {
		component, err := s.repo.GetComponent(ctx, *input.FeaturedComponentID)
		if err != nil {
			return nil, err
		}
		if component == nil {
			return nil, channelMonitorV3Invalid("featured component %d does not exist", *input.FeaturedComponentID)
		}
	}
	updated, err := s.repo.UpdateConfig(ctx, input, input.Version, updatedBy)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, ErrChannelMonitorV3Conflict
	}
	return updated, nil
}

func normalizeChannelMonitorV3Config(cfg *ChannelMonitorV3Config) error {
	if !slices.Contains(ChannelMonitorV3Intervals, cfg.IntervalMinutes) {
		return channelMonitorV3Invalid("interval must be one of 1, 2, 3, 5, 10, 15, 30 or 60 minutes")
	}
	if cfg.Cells < 30 || cfg.Cells > 120 {
		return channelMonitorV3Invalid("cells must be 30-120")
	}
	if _, ok := channelMonitorV3Ranges[cfg.AvailabilityRange]; !ok {
		return channelMonitorV3Invalid("availability range must be 24h, 7d or 30d")
	}
	if cfg.DownErrorRate <= 0 || cfg.DownErrorRate > 1 || cfg.DegradedErrorRate <= 0 || cfg.DegradedErrorRate > cfg.DownErrorRate {
		return channelMonitorV3Invalid("error rates must satisfy 0 < degraded <= down <= 1")
	}
	if !slices.Contains(ChannelMonitorV3TTFTThresholds, cfg.DegradedTTFTMs) {
		return channelMonitorV3Invalid("first-token threshold must be one of the latency buckets")
	}
	if cfg.MinRequests < 1 || cfg.MinRequests > 1000 {
		return channelMonitorV3Invalid("minimum requests must be 1-1000")
	}
	ignored := make([]string, 0, len(cfg.IgnoredErrorCategories))
	for _, category := range cfg.IgnoredErrorCategories {
		if !slices.Contains(ChannelMonitorV2ErrorCategories, category) {
			return channelMonitorV3Invalid("unknown error category %q", category)
		}
		if !slices.Contains(ignored, category) {
			ignored = append(ignored, category)
		}
	}
	cfg.IgnoredErrorCategories = ignored
	cfg.FooterNote = strings.TrimSpace(cfg.FooterNote)
	if utf8.RuneCountInString(cfg.FooterNote) > 500 {
		return channelMonitorV3Invalid("footer note must be at most 500 characters")
	}
	if cfg.FeaturedComponentID != nil && *cfg.FeaturedComponentID <= 0 {
		cfg.FeaturedComponentID = nil
	}
	return nil
}
