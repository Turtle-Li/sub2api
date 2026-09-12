package runtimegate

import (
	"io/fs"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// currencyCutoverCacheBypassCheckInterval bounds how long a removed marker can
// keep cache bypass active while avoiding a filesystem stat on every request.
const currencyCutoverCacheBypassCheckInterval = time.Second

// CurrencyCutoverCacheBypass reports whether a deployment-owned marker asks
// the process to bypass wallet-backed Redis caches during a currency cutover.
// It is deliberately independent from the background-work generation gate:
// request admission stays available while reads fall back to the database.
type CurrencyCutoverCacheBypass struct {
	markerPath string
	maxAge     time.Duration
	now        func() time.Time
	marker     func(string) bool

	mu          sync.Mutex
	lastChecked time.Time
	active      bool
}

// NewCurrencyCutoverCacheBypass creates a disabled gate for an empty path.
// A non-empty path is supplied only by deployment configuration and is checked
// at most once per second.
func NewCurrencyCutoverCacheBypass(markerPath string) *CurrencyCutoverCacheBypass {
	return newCurrencyCutoverCacheBypass(
		strings.TrimSpace(markerPath),
		currencyCutoverCacheBypassCheckInterval,
		time.Now,
		rootOnlyRegularMarkerExists,
	)
}

func newCurrencyCutoverCacheBypass(
	markerPath string,
	maxAge time.Duration,
	now func() time.Time,
	marker func(string) bool,
) *CurrencyCutoverCacheBypass {
	if maxAge < 0 {
		maxAge = 0
	}
	if now == nil {
		now = time.Now
	}
	if marker == nil {
		marker = rootOnlyRegularMarkerExists
	}
	return &CurrencyCutoverCacheBypass{
		markerPath: strings.TrimSpace(markerPath),
		maxAge:     maxAge,
		now:        now,
		marker:     marker,
	}
}

// Enabled returns true only while the configured marker is present and is a
// root-owned regular file that group/other users cannot modify.
func (g *CurrencyCutoverCacheBypass) Enabled() bool {
	if g == nil || g.markerPath == "" {
		return false
	}

	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.lastChecked.IsZero() && now.Before(g.lastChecked.Add(g.maxAge)) {
		return g.active
	}

	g.active = g.marker(g.markerPath)
	g.lastChecked = now
	return g.active
}

func rootOnlyRegularMarkerExists(path string) bool {
	info, err := os.Lstat(path) //nolint:gosec // G703: path comes only from deployment configuration, never a request.
	if err != nil {
		return false
	}
	return isRootOnlyRegularFile(info)
}

func isRootOnlyRegularFile(info fs.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0
}
