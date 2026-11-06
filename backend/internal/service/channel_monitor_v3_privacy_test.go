package service

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type monitorV3GroupsFake map[int64]Group

func (g monitorV3GroupsFake) GetByID(_ context.Context, id int64) (*Group, error) {
	v, ok := g[id]
	if !ok {
		return nil, nil
	}
	return &v, nil
}

func TestChannelMonitorV3PublicOptInAndSanitization(t *testing.T) {
	r := monitorV3StatusFixture()
	s := monitorV3Service(r)
	viewer := ChannelMonitorV3Viewer{Anonymous: true, AllowedGroups: map[int64]bool{4: true}}
	_, err := s.Status(context.Background(), viewer, nil)
	require.ErrorIs(t, err, ErrChannelMonitorV3NotFound)
	_, err = s.Incidents(context.Background(), viewer, 1, 20)
	require.ErrorIs(t, err, ErrChannelMonitorV3NotFound)
	r.config.PublicEnabled = true
	for i := range r.components {
		r.components[i].GroupStatus = StatusActive
	}
	status, err := s.Status(context.Background(), viewer, nil)
	require.NoError(t, err)
	require.Len(t, status.Categories, 1)
	require.Len(t, status.Categories[0].Components, 1)
	require.Nil(t, status.Featured.Multiplier)
	data, err := json.Marshal(status)
	require.NoError(t, err)
	for _, secret := range []string{"group_id", "multiplier", "requests", "errors", "Codex", "Exclusive"} {
		require.NotContains(t, string(data), "\""+secret+"\"")
	}
}

func TestChannelMonitorV3StaleAndHistoricalCurrentUnknown(t *testing.T) {
	r := monitorV3StatusFixture()
	s := monitorV3Service(r)
	old := monitorV3Now.Add(-time.Hour)
	r.through = &old
	status, err := s.Status(context.Background(), ChannelMonitorV3Viewer{Admin: true}, nil)
	require.NoError(t, err)
	require.Equal(t, ChannelMonitorV3StatusUnknown, status.Featured.Status)
	r.through = nil
	status, err = s.Status(context.Background(), ChannelMonitorV3Viewer{Admin: true}, &old)
	require.NoError(t, err)
	require.Equal(t, ChannelMonitorV3StatusUnknown, status.Featured.Status)
}

func TestChannelMonitorV3CollectorIgnoresDisabledV2View(t *testing.T) {
	require.True(t, channelMonitorCollectionEnabled(ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV3}, false))
	require.False(t, channelMonitorCollectionEnabled(ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV2}, false))
	require.False(t, channelMonitorCollectionEnabled(ChannelMonitorRuntime{Enabled: false, Mode: ChannelMonitorModeV3}, true))
}

func TestChannelMonitorV3IncidentPageOverflow(t *testing.T) {
	s := monitorV3Service(monitorV3StatusFixture())
	page, err := s.Incidents(context.Background(), ChannelMonitorV3Viewer{Admin: true}, int(^uint(0)>>1), 20)
	require.NoError(t, err)
	require.Empty(t, page.Items)
}
