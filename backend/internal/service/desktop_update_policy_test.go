//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizeDesktopUpdatePolicy(t *testing.T) {
	t.Parallel()

	policy, err := NormalizeDesktopUpdatePolicy(DesktopUpdatePolicy{
		LatestVersion:           " 1.4.0 ",
		MinimumSupportedVersion: "1.2.0",
		EnforcementEnabled:      true,
		EnforceAfter:            "2026-07-28T12:00:00+08:00",
		Reason:                  " 修复额度计算问题 ",
		ManualDownloadURL:       "https://download.example.com/tt-switch",
	})

	require.NoError(t, err)
	require.Equal(t, DesktopUpdatePolicySchemaVersion, policy.SchemaVersion)
	require.Equal(t, "1.4.0", policy.LatestVersion)
	require.Equal(t, "1.2.0", policy.MinimumSupportedVersion)
	require.Equal(t, "2026-07-28T04:00:00Z", policy.EnforceAfter)
	require.Equal(t, "修复额度计算问题", policy.Reason)
}

func TestNormalizeDesktopUpdatePolicyRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		policy DesktopUpdatePolicy
	}{
		{
			name: "force without minimum",
			policy: DesktopUpdatePolicy{
				LatestVersion:      "1.2.0",
				EnforcementEnabled: true,
			},
		},
		{
			name: "minimum above latest",
			policy: DesktopUpdatePolicy{
				LatestVersion:           "1.1.0",
				MinimumSupportedVersion: "1.2.0",
			},
		},
		{
			name: "invalid semver",
			policy: DesktopUpdatePolicy{
				LatestVersion: "1.2",
			},
		},
		{
			name: "insecure download",
			policy: DesktopUpdatePolicy{
				ManualDownloadURL: "http://download.example.com/app",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := NormalizeDesktopUpdatePolicy(tt.policy)
			require.Error(t, err)
		})
	}
}

func TestDesktopUpdatePolicyStatusFor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 28, 5, 0, 0, 0, time.UTC)
	policy := DesktopUpdatePolicy{
		SchemaVersion:           DesktopUpdatePolicySchemaVersion,
		LatestVersion:           "1.4.0",
		MinimumSupportedVersion: "1.2.0",
		EnforcementEnabled:      true,
		EnforceAfter:            "2026-07-28T04:00:00Z",
		Reason:                  "安全修复",
	}

	old := policy.StatusFor("1.1.9", now)
	require.True(t, old.UpdateAvailable)
	require.True(t, old.UpdateRequired)

	supported := policy.StatusFor("1.2.0", now)
	require.True(t, supported.UpdateAvailable)
	require.False(t, supported.UpdateRequired)

	current := policy.StatusFor("1.4.0", now)
	require.False(t, current.UpdateAvailable)
	require.False(t, current.UpdateRequired)

	malformed := policy.StatusFor("unknown", now)
	require.True(t, malformed.UpdateRequired)
}

func TestDesktopUpdatePolicyWaitsForScheduledEnforcement(t *testing.T) {
	t.Parallel()
	policy := DesktopUpdatePolicy{
		SchemaVersion:           DesktopUpdatePolicySchemaVersion,
		LatestVersion:           "2.0.0",
		MinimumSupportedVersion: "2.0.0",
		EnforcementEnabled:      true,
		EnforceAfter:            "2026-08-01T00:00:00Z",
	}

	status := policy.StatusFor(
		"1.0.0",
		time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC),
	)
	require.True(t, status.UpdateAvailable)
	require.False(t, status.UpdateRequired)
}

func TestDesktopClientVersion(t *testing.T) {
	t.Parallel()

	version, ok := DesktopClientVersion("", "TT-Switch/1.2.3")
	require.True(t, ok)
	require.Equal(t, "1.2.3", version)

	version, ok = DesktopClientVersion("2.0.0", "browser")
	require.True(t, ok)
	require.Equal(t, "2.0.0", version)

	_, ok = DesktopClientVersion("", "Mozilla/5.0")
	require.False(t, ok)

	version, ok = DesktopClientVersion("", "TT-Switch/")
	require.True(t, ok)
	require.Empty(t, version)
}
