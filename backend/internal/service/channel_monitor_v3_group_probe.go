package service

import (
	"context"
	"strings"
	"time"
)

const ChannelMonitorV3ProbeInterval = 10 * time.Minute

type ChannelMonitorV3GroupProbeConfig struct {
	Enabled bool                               `json:"enabled"`
	Groups  []ChannelMonitorV3GroupProbeTarget `json:"groups"`
}

type ChannelMonitorV3GroupProbeTarget struct {
	GroupID int64  `json:"group_id"`
	Model   string `json:"model"`
	Enabled bool   `json:"enabled"`
}

type ChannelMonitorV3GroupProbeResult struct {
	ID           int64
	GroupID      int64
	Model        string
	Slot         time.Time
	FinishedAt   *time.Time
	Outcome      string
	Category     string
	LatencyMs    int64
	AccountID    int64
	InputTokens  int64
	OutputTokens int64
}

// The persisted claim is also the attempt limit. Never reclaim an unfinished
// slot: its upstream submission may already have consumed quota.
type ChannelMonitorV3GroupProbeRepository interface {
	ClaimGroupProbe(context.Context, ChannelMonitorV3GroupProbeTarget, time.Time) (int64, error)
	FinishGroupProbe(context.Context, ChannelMonitorV3GroupProbeResult) error
	GroupProbes(context.Context, []int64, time.Time, time.Time) ([]ChannelMonitorV3GroupProbeResult, error)
	PruneGroupProbes(context.Context, time.Time) error
}

func (s *ChannelMonitorV3Service) validateGroupProbes(ctx context.Context, cfg *ChannelMonitorV3GroupProbeConfig) error {
	if cfg.Groups == nil {
		cfg.Groups = []ChannelMonitorV3GroupProbeTarget{}
	}
	if len(cfg.Groups) > 10 {
		return channelMonitorV3Invalid("at most ten probe groups may be configured")
	}
	seen := map[int64]bool{}
	for i := range cfg.Groups {
		target := &cfg.Groups[i]
		target.Model = strings.TrimSpace(target.Model)
		if target.GroupID <= 0 || target.Model == "" || len(target.Model) > 200 || seen[target.GroupID] {
			return channelMonitorV3Invalid("each probe requires a unique group and an exact text model")
		}
		seen[target.GroupID] = true
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
	return group != nil && (group.Platform == PlatformOpenAI || group.Platform == PlatformAnthropic)
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
