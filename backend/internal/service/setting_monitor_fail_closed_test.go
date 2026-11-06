package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
)

type monitorRuntimeSettingsRepo struct {
	SettingRepository
	values map[string]string
	err    error
}

func (r monitorRuntimeSettingsRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return r.values, r.err
}

func TestChannelMonitorRuntimeReadFailureNeverProbes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings *SettingService
	}{
		{"nil service", nil}, {"nil repository", &SettingService{}},
		{"read failure", &SettingService{settingRepo: monitorRuntimeSettingsRepo{err: errors.New("unavailable")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := tc.settings.GetChannelMonitorRuntime(context.Background())
			require.False(t, rt.Enabled)
			require.False(t, rt.ActiveProbesAllowed())
			require.False(t, rt.PassiveAggregationAllowed())
			svc := NewChannelMonitorService(nil, nil)
			svc.SetRuntimeReader(tc.settings)
			_, err := svc.RunCheck(context.Background(), 1)
			require.ErrorIs(t, err, ErrChannelMonitorDisabled)
		})
	}
}

func TestChannelMonitorRuntimeHealthyDefaultsAndModes(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		enabled    bool
		probes     bool
	}{
		{"missing defaults", "", true, true}, {"explicit v1", "v1", true, true},
		{"v3 passive", "v3", true, false}, {"disabled v1", "v1", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{}
			if tc.mode != "" {
				values[SettingKeyChannelMonitorMode] = tc.mode
				if tc.enabled {
					values[SettingKeyChannelMonitorEnabled] = "true"
				} else {
					values[SettingKeyChannelMonitorEnabled] = "false"
				}
			}
			settings := &SettingService{settingRepo: monitorRuntimeSettingsRepo{values: values}}
			rt := settings.GetChannelMonitorRuntime(context.Background())
			require.Equal(t, tc.enabled, rt.Enabled)
			require.Equal(t, tc.probes, rt.ActiveProbesAllowed())
			if tc.mode == "v3" {
				svc := NewChannelMonitorService(nil, nil)
				svc.SetRuntimeReader(settings)
				_, err := svc.RunCheck(context.Background(), 1)
				require.ErrorIs(t, err, ErrChannelMonitorActiveProbesRetired)
			}
		})
	}
}
