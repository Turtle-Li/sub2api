package service

import (
	"context"
	"fmt"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"strings"
)

var ErrChannelMonitorInvalidMode = infraerrors.BadRequest("CHANNEL_MONITOR_INVALID_MODE", "invalid monitor mode")

func (s *SettingService) SetChannelMonitorMode(ctx context.Context, mode string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case ChannelMonitorModeV1, ChannelMonitorModeV2, ChannelMonitorModeV3:
	default:
		return "", ErrChannelMonitorInvalidMode
	}
	if err := s.settingRepo.Set(ctx, SettingKeyChannelMonitorMode, normalized); err != nil {
		return "", fmt.Errorf("set channel monitor mode: %w", err)
	}
	if s.onUpdate != nil {
		s.onUpdate() // injected public settings carry the mode
	}
	s.notifyChannelMonitorRuntimeListeners()
	return normalized, nil
}

func (r ChannelMonitorRuntime) V2Active() bool { return r.Enabled && r.Mode == ChannelMonitorModeV2 }
func (r ChannelMonitorRuntime) V3Active() bool { return r.Enabled && r.Mode == ChannelMonitorModeV3 }

func channelMonitorCollectionEnabled(runtime ChannelMonitorRuntime, v2Enabled bool) bool {
	return runtime.PassiveAggregationAllowed() && (runtime.V3Active() || v2Enabled)
}
func ProvideChannelMonitorV3Service(repo ChannelMonitorV3Repository, groups GroupRepository) *ChannelMonitorV3Service {
	return NewChannelMonitorV3Service(repo, groups)
}
