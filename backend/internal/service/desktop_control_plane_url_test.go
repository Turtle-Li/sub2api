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
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty uses discovery origin", input: "  ", want: ""},
		{name: "https production", input: " https://account.example.com/base/ ", want: "https://account.example.com/base"},
		{name: "loopback http", input: "http://127.0.0.1:8080/", want: "http://127.0.0.1:8080"},
		{name: "localhost http", input: "http://localhost:3000", want: "http://localhost:3000"},
		{name: "reject production http", input: "http://account.example.com", wantErr: true},
		{name: "reject credentials", input: "https://user:pass@example.com", wantErr: true},
		{name: "reject query", input: "https://example.com?target=other", wantErr: true},
		{name: "reject relative", input: "/api/v1", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeDesktopControlPlaneURL(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestDesktopControlPlaneURLPersistsNormalizedValue(t *testing.T) {
	repo := &bmUpdateRepoStub{}
	service := NewSettingService(repo, &config.Config{})

	err := service.UpdateSettings(context.Background(), &SystemSettings{
		DesktopControlPlaneURL: " https://account.example.com/base/ ",
	})

	require.NoError(t, err)
	require.Equal(
		t,
		"https://account.example.com/base",
		repo.updates[SettingKeyDesktopControlPlaneURL],
	)
}

func TestDesktopControlPlaneURLRejectsInsecureProductionValue(t *testing.T) {
	repo := &bmUpdateRepoStub{}
	service := NewSettingService(repo, &config.Config{})

	err := service.UpdateSettings(context.Background(), &SystemSettings{
		DesktopControlPlaneURL: "http://account.example.com",
	})

	require.Error(t, err)
	require.Nil(t, repo.updates)
}
