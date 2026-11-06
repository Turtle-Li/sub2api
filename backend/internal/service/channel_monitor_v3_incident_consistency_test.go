package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestChannelMonitorV3OngoingCountIndependentOfStrip(t *testing.T) {
	r := monitorV3StatusFixture()
	r.config.IntervalMinutes = 1
	r.slotFacts = ChannelMonitorV3Facts{}
	for back := 60; back >= 45; back-- {
		r.slotFacts.Metrics = append(r.slotFacts.Metrics, ChannelMonitorV3Fact{GroupID: 4, Slot: monitorV3Now.Truncate(time.Minute).Add(-time.Duration(back) * time.Minute), Errors: 10})
	}
	s := monitorV3Service(r)
	viewer := ChannelMonitorV3Viewer{AllowedGroups: map[int64]bool{4: true}}
	incidents, err := s.Incidents(context.Background(), viewer, 1, 20)
	require.NoError(t, err)
	require.Len(t, incidents.Items, 1)
	require.Nil(t, incidents.Items[0].EndedAt)
	past := monitorV3Now.Add(-2 * time.Hour)
	for _, end := range []*time.Time{nil, &past} {
		status, err := s.Status(context.Background(), viewer, end)
		require.NoError(t, err)
		require.Equal(t, 1, status.OpenIncidents)
	}
	status, err := s.Status(context.Background(), ChannelMonitorV3Viewer{}, nil)
	require.NoError(t, err)
	require.Zero(t, status.OpenIncidents)
}

func TestChannelMonitorV3RecoveryRequiresObservedTimestamp(t *testing.T) {
	start := monitorV3Now.Add(-time.Hour)
	cells := []*ChannelMonitorV3Cell{{Start: start, Status: ChannelMonitorV3StatusDown}, {Start: start.Add(5 * time.Minute), Status: ChannelMonitorV3StatusDown}, nil}
	runs := channelMonitorV3RunsFromCells(ChannelMonitorV3Component{}, cells, 5*time.Minute, monitorV3Now, true)
	require.Len(t, runs, 1)
	require.Nil(t, runs[0].EndedAt)
	cells = append(cells, &ChannelMonitorV3Cell{Start: monitorV3Now, Status: ChannelMonitorV3StatusOperational})
	runs = channelMonitorV3RunsFromCells(ChannelMonitorV3Component{}, cells, 5*time.Minute, monitorV3Now, true)
	require.Len(t, runs, 1)
	require.Equal(t, monitorV3Now, *runs[0].EndedAt)
}
