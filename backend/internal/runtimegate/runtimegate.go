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
	StateStandby = "standby"
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
	info, err := os.Lstat(path) //nolint:gosec // G703: path is injected only by deployment as a fixed, root-validated, read-only runtime mount; it is not user or request input.
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64 {
		return false
	}
	data, err := os.ReadFile(path) //nolint:gosec // G703: path is injected only by deployment as a fixed, root-validated, read-only runtime mount; it is not user or request input.
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == StateActive
}

// DurableRecoveryWorkAllowed permits a deliberately narrow class of recovery
// jobs to finish work that was prepared durably before a process crash. It
// still stops during process shutdown, but it intentionally does not
// distinguish the valid active and standby states: a Caddy-active candidate is
// held in standby until a rollback-capable release commits, and must be able
// to clear a compatibility gate without manual provider queries. A configured
// missing, unsafe, or invalid state file remains fail-closed.
//
// Callers must own a database-authoritative multi-instance lease and a
// persisted external idempotency key. It is not a replacement for
// SharedWorkAllowed and must never be used for work that creates new business
// operations.
func DurableRecoveryWorkAllowed() bool {
	if !processActive.Load() {
		return false
	}
	path := strings.TrimSpace(os.Getenv(StateFileEnv))
	if path == "" {
		return true
	}
	info, err := os.Lstat(path) //nolint:gosec // G703: deployment injects this fixed runtime mount; it is not request input.
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64 {
		return false
	}
	data, err := os.ReadFile(path) //nolint:gosec // G703: deployment injects this fixed runtime mount; it is not request input.
	if err != nil {
		return false
	}
	switch strings.TrimSpace(string(data)) {
	case StateActive, StateStandby:
		return true
	default:
		return false
	}
}
