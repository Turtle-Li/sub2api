//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSettingService_GetPublicSettings_ExposesDesktopAppDownloadSources(t *testing.T) {
	custom := DefaultDesktopAppDownloadSources()
	custom.ClaudeWindowsArm64URL = "https://downloads.example.com/claude-arm64.exe"
	payload, err := json.Marshal(custom)
	require.NoError(t, err)

	repo := &settingPublicRepoStub{
		values: map[string]string{
			SettingKeyDesktopAppDownloadSources: string(payload),
		},
	}
	svc := NewSettingService(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, custom, settings.DesktopAppDownloadSources)
}
