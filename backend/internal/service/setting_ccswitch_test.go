//go:build unit

package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSettingService_CCSwitchCodexModelReads(t *testing.T) {
	for _, tt := range []struct {
		name   string
		values map[string]string
		want   string
	}{
		{"missing", nil, "gpt-6.2-sol"},
		{"empty", map[string]string{SettingKeyCCSwitchCodexModel: ""}, "gpt-6.2-sol"},
		{"stored", map[string]string{SettingKeyCCSwitchCodexModel: "gpt-6.1-sol"}, "gpt-6.1-sol"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewSettingService(&settingGetAllRepoStub{values: tt.values}, &config.Config{})
			all, err := svc.GetAllSettings(context.Background())
			require.NoError(t, err)
			require.Equal(t, tt.want, all.CCSwitchCodexModel)
			publicService := NewSettingService(&settingPublicRepoStub{values: tt.values}, &config.Config{})
			public, err := publicService.GetPublicSettings(context.Background())
			require.NoError(t, err)
			require.Equal(t, tt.want, public.CCSwitchCodexModel)
			bootstrap, err := publicService.GetPublicSettingsForInjection(context.Background())
			require.NoError(t, err)
			encoded, err := json.Marshal(bootstrap)
			require.NoError(t, err)
			require.Contains(t, string(encoded), `"ccswitch_codex_model":"`+tt.want+`"`)
		})
	}
}

func TestSettingService_CCSwitchCodexModelWrite(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
		want  string
	}{
		{"reset", "  ", "gpt-6.2-sol"},
		{"trim", " gpt-6.1-sol ", "gpt-6.1-sol"},
		{"qualified", "org/model:preview_1.0", "org/model:preview_1.0"},
		{"newline", "bad\nmodel", ""},
		{"too-long", strings.Repeat("a", 101), ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &settingUpdateRepoStub{}
			svc := NewSettingService(repo, &config.Config{})
			err := svc.UpdateSettings(context.Background(), &SystemSettings{CCSwitchCodexModel: tt.value})
			if tt.want == "" {
				require.Error(t, err)
				require.Nil(t, repo.updates)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, repo.updates[SettingKeyCCSwitchCodexModel])
		})
	}
}
