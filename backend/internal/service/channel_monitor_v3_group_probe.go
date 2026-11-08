package service

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

const ChannelMonitorV3ProbeInterval = 10 * time.Minute

type ChannelMonitorV3GroupProbeConfig struct {
	Enabled bool                               `json:"enabled"`
	Groups  []ChannelMonitorV3GroupProbeTarget `json:"groups"`
}

type ChannelMonitorV3GroupProbeTarget struct {
	GroupID     int64  `json:"group_id"`
	Model       string `json:"model"`
	Enabled     bool   `json:"enabled"`
	DisplayName string `json:"display_name,omitempty"`
	Listed      *bool  `json:"listed,omitempty"`
}

// Legacy checked targets were the owner's selection. Blank/paused legacy rows
// must not publish the entire catalog when upgrading to explicit publication.
func (t ChannelMonitorV3GroupProbeTarget) IsListed() bool {
	if t.Listed != nil {
		return *t.Listed
	}
	return t.Enabled
}

func (t ChannelMonitorV3GroupProbeTarget) sameCheck(other ChannelMonitorV3GroupProbeTarget) bool {
	return t.GroupID == other.GroupID && t.Model == other.Model && t.Enabled == other.Enabled &&
		t.DisplayName == other.DisplayName && t.IsListed() == other.IsListed()
}

type ChannelMonitorV3GroupProbeResult struct {
	ID                   int64
	GroupID              int64
	Model                string
	Slot                 time.Time
	FinishedAt           *time.Time
	Outcome              string
	Category             string
	LatencyMs            int64
	FirstTokenMs         *int64
	AccountID            int64
	InputTokens          int64
	OutputTokens         int64
	AttemptCount         int
	MissingUsageAttempts int
}

// A persisted claim owns the bounded retry sequence. Never reclaim an unfinished
// slot: its upstream submissions may already have consumed quota.
type ChannelMonitorV3GroupProbeRepository interface {
	ClaimGroupProbe(context.Context, ChannelMonitorV3GroupProbeTarget, time.Time) (int64, error)
	RecordGroupProbeAttempt(context.Context, ChannelMonitorV3GroupProbeResult) error
	FinishGroupProbe(context.Context, ChannelMonitorV3GroupProbeResult) error
	GroupProbes(context.Context, []int64, time.Time, time.Time) ([]ChannelMonitorV3GroupProbeResult, error)
	PruneGroupProbes(context.Context, time.Time) error
}

func (s *ChannelMonitorV3Service) validateGroupProbes(ctx context.Context, cfg *ChannelMonitorV3GroupProbeConfig) error {
	if cfg.Groups == nil {
		cfg.Groups = []ChannelMonitorV3GroupProbeTarget{}
	}
	if len(cfg.Groups) > 1000 {
		return channelMonitorV3Invalid("too many status groups")
	}
	seen := map[int64]bool{}
	enabled := 0
	for i := range cfg.Groups {
		target := &cfg.Groups[i]
		target.Model = strings.TrimSpace(target.Model)
		target.DisplayName = strings.TrimSpace(target.DisplayName)
		if target.GroupID <= 0 || len(target.Model) > 200 || seen[target.GroupID] || utf8.RuneCountInString(target.DisplayName) > 100 {
			return channelMonitorV3Invalid("status groups must be unique and display names at most 100 characters")
		}
		seen[target.GroupID] = true
		if target.Enabled {
			enabled++
			if !target.IsListed() || target.Model == "" || enabled > 10 {
				return channelMonitorV3Invalid("at most ten checks may be enabled, each with an exact text model")
			}
		}
		if !cfg.Enabled || !target.Enabled {
			continue
		}
		if s.groups == nil {
			return channelMonitorV3Invalid("group lookup unavailable")
		}
		group, err := s.groups.GetByID(ctx, target.GroupID)
		if err != nil {
			return err
		}
		if group == nil || group.Status != StatusActive || !channelMonitorV3ProbeGroupSupported(group) || !group.ModelAllowlist.Allows(target.Model) || channelMonitorV3ProbeMediaModel(target.Model) {
			return channelMonitorV3Invalid("probe group or text model is unavailable")
		}
	}
	return nil
}

func channelMonitorV3ProbeGroupSupported(group *Group) bool {
	if group == nil || group.IsExclusive {
		return false
	}
	return group.Platform == PlatformOpenAI || group.Platform == PlatformAnthropic || domain.IsCNProviderPlatform(group.Platform)
}

func channelMonitorV3ProbeMediaModel(model string) bool {
	model = strings.ToLower(model)
	for _, part := range []string{"image", "video", "audio", "tts", "whisper", "embedding", "realtime", "sora"} {
		if strings.Contains(model, part) {
			return true
		}
	}
	return false
}
