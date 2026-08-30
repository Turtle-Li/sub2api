package runtimegate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSharedWorkAllowedLegacyAndConfiguredStates(t *testing.T) {
	SetProcessActive(true)
	t.Cleanup(func() { SetProcessActive(true) })
	t.Setenv(StateFileEnv, "")
	require.True(t, SharedWorkAllowed())

	statePath := filepath.Join(t.TempDir(), "background-state")
	t.Setenv(StateFileEnv, statePath)
	require.False(t, SharedWorkAllowed(), "configured missing file must fail closed")
	require.NoError(t, os.WriteFile(statePath, []byte("standby\n"), 0o600))
	require.False(t, SharedWorkAllowed())
	require.NoError(t, os.WriteFile(statePath, []byte("active\n"), 0o600))
	require.True(t, SharedWorkAllowed())

	SetProcessActive(false)
	require.False(t, SharedWorkAllowed())
}
