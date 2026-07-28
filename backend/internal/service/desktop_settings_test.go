//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNormalizeDesktopControlPlaneURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "empty uses request origin", raw: "  ", want: ""},
		{name: "https origin", raw: " https://Control.Example.com/ ", want: "https://Control.Example.com"},
		{name: "loopback http", raw: "http://127.0.0.1:8080/", want: "http://127.0.0.1:8080"},
		{name: "reject public http", raw: "http://control.example.com", wantErr: true},
		{name: "reject path", raw: "https://control.example.com/api/v1", wantErr: true},
		{name: "reject credentials", raw: "https://user:pass@control.example.com", wantErr: true},
		{name: "reject query", raw: "https://control.example.com?tenant=one", wantErr: true},
		{name: "reject fragment", raw: "https://control.example.com/#desktop", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeDesktopControlPlaneURL(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSettingServiceDesktopConsoleSettingsPersistence(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})
	updated, err := svc.UpdateDesktopConsoleSettings(context.Background(), DesktopConsoleSettings{
		ControlPlaneURL: " https://control.example.com/ ",
		Promotions: []DesktopPromotion{{
			ID:        "turtle-chat",
			Title:     "Turtle Chat",
			TargetURL: "https://chat.example.com",
			Icon:      "chat",
			Surfaces:  []string{"discover"},
			Enabled:   true,
		}},
		UpdatePolicy: DesktopUpdatePolicy{
			LatestVersion:           "1.4.0",
			MinimumSupportedVersion: "1.2.0",
			EnforcementEnabled:      true,
		},
	})

	require.NoError(t, err)
	require.Equal(t, DesktopConsoleSchemaVersion, updated.SchemaVersion)
	require.Equal(t, "https://control.example.com", repo.updates[SettingKeyDesktopControlPlaneURL])
	require.JSONEq(t, `[{"id":"turtle-chat","title":"Turtle Chat","summary":"","target_url":"https://chat.example.com","cta_label":"打开","icon":"chat","surfaces":["discover"],"enabled":true,"sort_order":0}]`, repo.updates[SettingKeyDesktopPromotions])
	require.JSONEq(t, `{"schema_version":1,"latest_version":"1.4.0","minimum_supported_version":"1.2.0","enforcement_enabled":true}`, repo.updates[SettingKeyDesktopUpdatePolicy])
	require.Len(t, repo.updates, 3)
}

func TestSettingServiceDesktopControlPlaneURLPersistence(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})
	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		DesktopControlPlaneURL: " https://control.example.com/ ",
	})

	require.NoError(t, err)
	require.Equal(t, "https://control.example.com", repo.updates[SettingKeyDesktopControlPlaneURL])
}

func TestSettingServiceDesktopControlPlaneURLRejectsInsecurePublicURL(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})
	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		DesktopControlPlaneURL: "http://control.example.com",
	})

	require.Error(t, err)
	require.Nil(t, repo.updates)
}

func TestSettingServiceDesktopConsoleRejectsInsecurePublicURL(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})
	_, err := svc.UpdateDesktopConsoleSettings(context.Background(), DesktopConsoleSettings{
		ControlPlaneURL: "http://control.example.com",
	})

	require.Error(t, err)
	require.Nil(t, repo.updates)
}
