package securityaudit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"sync"
	"time"
)

const (
	guardCircuitFailureThreshold = 3
	guardCircuitFailureWindow    = 30 * time.Second
	guardCircuitOpenDuration     = 30 * time.Second
	guardCircuitProbeInterval    = 5 * time.Second
	guardCircuitProbeText        = "Hello"
	guardCircuitProbeTimeoutMax  = 3 * time.Second
	guardCircuitMaxEntries       = 256
)

type GuardCircuitState string

const (
	GuardCircuitClosed   GuardCircuitState = "closed"
	GuardCircuitOpen     GuardCircuitState = "open"
	GuardCircuitHalfOpen GuardCircuitState = "half_open"
)

// GuardCircuitSnapshot contains only operational state. It never includes a
// Guard token, request payload, or endpoint URL.
type GuardCircuitSnapshot struct {
	State               GuardCircuitState `json:"state"`
	ConsecutiveFailures int               `json:"consecutive_failures"`
	OpenedAt            *time.Time        `json:"opened_at,omitempty"`
	NextProbeAt         *time.Time        `json:"next_probe_at,omitempty"`
	LastProbeAt         *time.Time        `json:"last_probe_at,omitempty"`
	// Generation is an in-process fence for a single open interval. It is
	// deliberately omitted from the runtime API; the shared coordinator uses it
	// to avoid treating a closed Redis marker from an earlier interval as proof
	// that a newer local failure recovered.
	Generation uint64 `json:"-"`
}

type guardCircuitEntry struct {
	state           GuardCircuitState
	failures        int
	windowStartedAt time.Time
	openedAt        time.Time
	nextProbeAt     time.Time
	lastProbeAt     time.Time
	lastFailureAt   time.Time
	lastTouchedAt   time.Time
	generation      uint64
}

// GuardCircuit provides the request-local fast path shared by the synchronous
// and asynchronous Guard paths in one application process. The separate
// sharedGuardCircuit coordinates async consumers across blue-green containers;
// keeping this local path avoids adding a Redis read to every blocking request.
type GuardCircuit struct {
	mu       sync.Mutex
	clock    Clock
	entries  map[string]guardCircuitEntry
	sequence uint64
}

func newGuardCircuit(clock Clock) *GuardCircuit {
	if clock == nil {
		clock = realClock{}
	}
	return &GuardCircuit{clock: clock, entries: make(map[string]guardCircuitEntry)}
}

func (c *GuardCircuit) Allows(cfg ActiveConfig, endpoint ActiveEndpoint) bool {
	if c == nil {
		return true
	}
	now := c.clock.Now()
	key := guardCircuitKey(cfg, endpoint)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return true
	}
	entry.lastTouchedAt = now
	c.entries[key] = entry
	return entry.state == GuardCircuitClosed
}

func (c *GuardCircuit) HasAvailableEndpoint(cfg ActiveConfig) bool {
	for _, endpoint := range cfg.EnabledEndpoints() {
		if c.Allows(cfg, endpoint) {
			return true
		}
	}
	return false
}

// PruneInactive removes retired target generations and closed disabled entries.
// An open or half-open target that is still represented by the configuration
// remains quarantined when temporarily disabled: re-enabling the identical
// target must still require a background recovery probe. A generation absent
// from the configuration was removed or replaced, so it cannot be selected or
// probed again and can be discarded rather than accumulating indefinitely.
func (c *GuardCircuit) PruneInactive(cfg ActiveConfig) {
	if c == nil {
		return
	}
	configured := make(map[string]struct{}, len(cfg.Endpoints))
	enabled := make(map[string]struct{}, len(cfg.EnabledEndpoints()))
	for _, endpoint := range cfg.Endpoints {
		key := guardCircuitKey(cfg, endpoint)
		configured[key] = struct{}{}
		if endpoint.Enabled {
			enabled[key] = struct{}{}
		}
	}
	now := c.clock.Now()
	c.mu.Lock()
	for key, entry := range c.entries {
		if _, ok := configured[key]; !ok {
			delete(c.entries, key)
			continue
		}
		if _, ok := enabled[key]; !ok && entry.state == GuardCircuitClosed {
			delete(c.entries, key)
		}
	}
	c.ensureCapacityLocked(now)
	c.mu.Unlock()
}

// RecordFailure counts only model endpoint failures that would otherwise be
// retried. Invalid model responses also trip the breaker because repeatedly
// calling a model that violates the response contract cannot make progress.
func (c *GuardCircuit) RecordFailure(cfg ActiveConfig, endpoint ActiveEndpoint, failure error) (tripped bool) {
	if c == nil || !guardCircuitFailure(failure) {
		return false
	}
	now := c.clock.Now()
	key := guardCircuitKey(cfg, endpoint)
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.entries[key]
	if !exists {
		entry.state = GuardCircuitClosed
	}
	if entry.state != GuardCircuitClosed {
		entry.lastTouchedAt = now
		c.entries[key] = entry
		return false
	}
	if entry.windowStartedAt.IsZero() || now.Before(entry.windowStartedAt) || now.Sub(entry.windowStartedAt) > guardCircuitFailureWindow {
		entry.failures = 0
		entry.windowStartedAt = now
	}
	entry.failures++
	entry.lastFailureAt = now
	entry.lastTouchedAt = now
	if entry.failures >= guardCircuitFailureThreshold {
		entry.state = GuardCircuitOpen
		entry.openedAt = now
		entry.nextProbeAt = now.Add(guardCircuitOpenDuration)
		c.sequence++
		entry.generation = c.sequence
		tripped = true
	}
	c.ensureCapacityLocked(now)
	c.entries[key] = entry
	return tripped
}

func (c *GuardCircuit) RecordSuccess(cfg ActiveConfig, endpoint ActiveEndpoint) {
	if c == nil {
		return
	}
	key := guardCircuitKey(cfg, endpoint)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return
	}
	// An in-flight foreground request can finish successfully after other
	// requests have opened this circuit. It must not erase the quarantine:
	// FinishProbe is the only recovery transition.
	if entry.state != GuardCircuitClosed {
		return
	}
	delete(c.entries, key)
}

// BeginProbe reserves the only half-open permit. Request paths intentionally
// never transition an open circuit: only this background recovery path can
// issue a probe request after the cooldown.
func (c *GuardCircuit) BeginProbe(cfg ActiveConfig, endpoint ActiveEndpoint) bool {
	if c == nil {
		return false
	}
	now := c.clock.Now()
	key := guardCircuitKey(cfg, endpoint)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || entry.state != GuardCircuitOpen || now.Before(entry.nextProbeAt) {
		return false
	}
	entry.state = GuardCircuitHalfOpen
	entry.lastProbeAt = now
	entry.lastTouchedAt = now
	c.entries[key] = entry
	return true
}

func (c *GuardCircuit) FinishProbe(cfg ActiveConfig, endpoint ActiveEndpoint, probeErr error) (recovered bool) {
	if c == nil {
		return false
	}
	now := c.clock.Now()
	key := guardCircuitKey(cfg, endpoint)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return false
	}
	if probeErr == nil {
		delete(c.entries, key)
		return true
	}
	entry.state = GuardCircuitOpen
	entry.failures = guardCircuitFailureThreshold
	entry.windowStartedAt = now
	entry.openedAt = now
	entry.nextProbeAt = now.Add(guardCircuitOpenDuration)
	entry.lastFailureAt = now
	entry.lastTouchedAt = now
	c.sequence++
	entry.generation = c.sequence
	c.entries[key] = entry
	return false
}

// MarkRecoveredByBackgroundProbe accepts a successful shared recovery probe
// from another application container. This is intentionally private to the
// service heartbeat path: foreground traffic must never clear a circuit.
func (c *GuardCircuit) MarkRecoveredByBackgroundProbe(cfg ActiveConfig, endpoint ActiveEndpoint) {
	if c == nil {
		return
	}
	key := guardCircuitKey(cfg, endpoint)
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

func (c *GuardCircuit) Snapshot(cfg ActiveConfig) map[string]GuardCircuitSnapshot {
	result := make(map[string]GuardCircuitSnapshot, len(cfg.EnabledEndpoints()))
	if c == nil {
		for _, endpoint := range cfg.EnabledEndpoints() {
			result[endpoint.ID] = GuardCircuitSnapshot{State: GuardCircuitClosed}
		}
		return result
	}
	now := c.clock.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, endpoint := range cfg.EnabledEndpoints() {
		key := guardCircuitKey(cfg, endpoint)
		entry, ok := c.entries[key]
		if !ok {
			result[endpoint.ID] = GuardCircuitSnapshot{State: GuardCircuitClosed}
			continue
		}
		entry.lastTouchedAt = now
		c.entries[key] = entry
		result[endpoint.ID] = GuardCircuitSnapshot{
			State:               entry.state,
			ConsecutiveFailures: entry.failures,
			OpenedAt:            circuitTimePointer(entry.openedAt),
			NextProbeAt:         circuitTimePointer(entry.nextProbeAt),
			LastProbeAt:         circuitTimePointer(entry.lastProbeAt),
			Generation:          entry.generation,
		}
	}
	return result
}

func guardCircuitFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var guardErr *GuardError
	if !errors.As(err, &guardErr) {
		return false
	}
	return guardErr.Code == ErrorCodeUnavailable || guardErr.Code == ErrorCodeInvalidResponse
}

func guardCircuitKey(_ ActiveConfig, endpoint ActiveEndpoint) string {
	value := []byte(endpoint.ID + "\x00" + endpoint.BaseURL + "\x00" + endpoint.Model + "\x00" +
		endpoint.Protocol + "\x00" + endpoint.Token + "\x00" + strconv.Itoa(endpoint.TimeoutMS) + "\x00" + strconv.Itoa(endpoint.InputLimit))
	digest := sha256.Sum256(value)
	// A config version changes for unrelated settings such as queue capacity or
	// group scope. It must not reopen a quarantined endpoint. The token stays
	// inside this hash so a credential rotation creates a new endpoint generation
	// without exposing the credential in memory snapshots, Redis keys, or logs.
	return hex.EncodeToString(digest[:])
}

func circuitTimePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func (c *GuardCircuit) ensureCapacityLocked(now time.Time) {
	if len(c.entries) < guardCircuitMaxEntries {
		return
	}
	for key, entry := range c.entries {
		if entry.state == GuardCircuitClosed && now.Sub(entry.lastTouchedAt) > guardCircuitFailureWindow {
			delete(c.entries, key)
		}
	}
	// The capacity is a cache target, not permission to forget a failure. Open
	// and half-open entries must remain until a background probe resolves them;
	// current configuration cardinality is separately bounded at validation.
}
