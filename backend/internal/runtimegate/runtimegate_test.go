package runtimegate

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type markerFileInfo struct {
	mode fs.FileMode
	sys  any
}

func (i markerFileInfo) Name() string       { return "marker" }
func (i markerFileInfo) Size() int64        { return 0 }
func (i markerFileInfo) Mode() fs.FileMode  { return i.mode }
func (i markerFileInfo) ModTime() time.Time { return time.Time{} }
func (i markerFileInfo) IsDir() bool        { return false }
func (i markerFileInfo) Sys() any           { return i.sys }

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

func TestCurrencyCutoverCacheBypassRequiresRootOnlyRegularMarker(t *testing.T) {
	root := &syscall.Stat_t{Uid: 0}
	nonRoot := &syscall.Stat_t{Uid: 1}

	require.True(t, isRootOnlyRegularFile(markerFileInfo{mode: 0o600, sys: root}))
	require.False(t, isRootOnlyRegularFile(markerFileInfo{mode: 0o620, sys: root}), "group-writable marker must not control billing cache behavior")
	require.False(t, isRootOnlyRegularFile(markerFileInfo{mode: 0o600, sys: nonRoot}), "non-root owner must not control billing cache behavior")
	require.False(t, isRootOnlyRegularFile(markerFileInfo{mode: fs.ModeSymlink | 0o600, sys: root}), "symlinks must not be followed")
}

func TestCurrencyCutoverCacheBypassMarkerRemovalRestoresCacheUse(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	markerPresent := true
	checks := 0
	gate := newCurrencyCutoverCacheBypass(" /runtime/currency-cutover ", time.Second, func() time.Time { return now }, func(path string) bool {
		checks++
		require.Equal(t, "/runtime/currency-cutover", path)
		return markerPresent
	})

	require.True(t, gate.Enabled())
	markerPresent = false
	require.True(t, gate.Enabled(), "cached marker state is allowed for at most one second")
	require.Equal(t, 1, checks)

	now = now.Add(time.Second)
	require.False(t, gate.Enabled(), "removing the marker restores normal cache use")
	require.Equal(t, 2, checks)
}
