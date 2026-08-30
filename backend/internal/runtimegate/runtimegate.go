// Package runtimegate provides the generation-level claim gate used during
// blue/green and multi-node draining. It does not elect a permanent primary;
// active generations still coordinate each task through shared leases/claims.
package runtimegate

import (
	"os"
	"strings"
	"sync/atomic"
)

const (
	StateFileEnv = "SUB2API_BACKGROUND_STATE_FILE"
	StateActive  = "active"
)

var processActive atomic.Bool

func init() {
	processActive.Store(true)
}

// SetProcessActive prevents this generation from acquiring new shared work.
// In-flight work keeps its own context/lease policy and may finish gracefully.
func SetProcessActive(active bool) {
	processActive.Store(active)
}

// SharedWorkAllowed reads the deployment-owned generation state on every claim
// boundary. Unconfigured legacy deployments remain active; once a state file is
// configured, missing/invalid content fails closed.
func SharedWorkAllowed() bool {
	if !processActive.Load() {
		return false
	}
	path := strings.TrimSpace(os.Getenv(StateFileEnv))
	if path == "" {
		return true
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64 {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == StateActive
}
