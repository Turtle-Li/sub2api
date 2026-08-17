package securityaudit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	guardCircuitSharedKeyPrefix         = "sub2api:prompt_guard:circuit:"
	guardCircuitSharedStateTTL          = 24 * time.Hour
	guardCircuitSharedOperationTTL      = 150 * time.Millisecond
	guardCircuitSharedCacheTTL          = 250 * time.Millisecond
	guardCircuitSharedProbeLease        = guardCircuitProbeTimeoutMax + guardCircuitProbeInterval
	guardCircuitSharedReconcileInterval = 500 * time.Millisecond
)

type sharedGuardCircuitRecord struct {
	State               GuardCircuitState
	ConsecutiveFailures int
	OpenedAt            time.Time
	NextProbeAt         time.Time
	LastProbeAt         time.Time
	Fence               uint64
}

type sharedGuardCircuitProbeResult uint8

const (
	sharedGuardCircuitProbeMissing sharedGuardCircuitProbeResult = iota
	sharedGuardCircuitProbeAcquired
	sharedGuardCircuitProbeBusy
	sharedGuardCircuitProbeRecovered
)

// sharedGuardCircuitStore is deliberately scoped to async scheduling and
// recovery. It stores only a hashed endpoint generation and operational state.
type sharedGuardCircuitStore interface {
	Read(ctx context.Context, key string) (sharedGuardCircuitRecord, bool, error)
	Open(ctx context.Context, key string, record sharedGuardCircuitRecord) (uint64, error)
	TryBeginProbe(ctx context.Context, key, leaseID string, now, leaseUntil time.Time) (sharedGuardCircuitProbeResult, error)
	FinishProbe(ctx context.Context, key, leaseID string, record sharedGuardCircuitRecord, success bool) (bool, error)
}

type sharedCircuitCacheEntry struct {
	record     sharedGuardCircuitRecord
	found      bool
	observedAt time.Time
}

// sharedLocalOpen tracks one local circuit interval. A successful Redis write
// returns a monotonic server-side fence; only a closed marker at that fence or
// later proves a recovery happened after this local failure was published.
type sharedLocalOpen struct {
	generation  uint64
	openedAt    time.Time
	nextProbeAt time.Time
	fence       uint64
	publishing  bool
}

// sharedGuardCircuit prevents every blue-green consumer of the durable async
// queue from calling the same failed Guard endpoint. It intentionally is not a
// blocking request dependency: synchronous evaluations retain their local
// circuit and fail-closed error contract.
type sharedGuardCircuit struct {
	store      sharedGuardCircuitStore
	clock      Clock
	instanceID string
	sequence   atomic.Uint64

	cacheMu sync.Mutex
	cache   map[string]sharedCircuitCacheEntry

	localMu    sync.Mutex
	localOpens map[string]sharedLocalOpen
}

func newSharedGuardCircuit(store sharedGuardCircuitStore, clock Clock) *sharedGuardCircuit {
	if store == nil {
		return nil
	}
	if clock == nil {
		clock = realClock{}
	}
	return &sharedGuardCircuit{
		store:      store,
		clock:      clock,
		instanceID: newSharedCircuitInstanceID(),
		cache:      make(map[string]sharedCircuitCacheEntry),
		localOpens: make(map[string]sharedLocalOpen),
	}
}

func newSharedCircuitInstanceID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err == nil {
		return hex.EncodeToString(bytes)
	}
	return strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
}

func (c *sharedGuardCircuit) Allows(ctx context.Context, cfg ActiveConfig, endpoint ActiveEndpoint) (bool, error) {
	return c.allows(ctx, cfg, endpoint, true)
}

// AllowsFresh bypasses the short read cache immediately before dispatch. It
// bounds Redis fan-out during idle polling while ensuring a claimed job cannot
// reach the model because this process has an old closed-state cache entry.
func (c *sharedGuardCircuit) AllowsFresh(ctx context.Context, cfg ActiveConfig, endpoint ActiveEndpoint) (bool, error) {
	return c.allows(ctx, cfg, endpoint, false)
}

func (c *sharedGuardCircuit) allows(ctx context.Context, cfg ActiveConfig, endpoint ActiveEndpoint, allowCache bool) (bool, error) {
	if c == nil || c.store == nil {
		return true, nil
	}
	record, found, err := c.read(ctx, guardCircuitSharedKey(cfg, endpoint), allowCache)
	if err != nil {
		return false, err
	}
	return !found || record.State == GuardCircuitClosed, nil
}

// Open publishes an endpoint quarantine before an async worker can claim more
// work. A running caller already has a model failure; the bounded write makes
// the stop-scheduling signal visible to draining peers promptly.
func (c *sharedGuardCircuit) Open(ctx context.Context, cfg ActiveConfig, endpoint ActiveEndpoint) error {
	if c == nil || c.store == nil {
		return nil
	}
	now := c.clock.Now()
	record := sharedGuardCircuitRecord{
		State:               GuardCircuitOpen,
		ConsecutiveFailures: guardCircuitFailureThreshold,
		OpenedAt:            now,
		NextProbeAt:         now.Add(guardCircuitOpenDuration),
	}
	key := guardCircuitSharedKey(cfg, endpoint)
	writeCtx, cancel := guardCircuitSharedContext(ctx)
	defer cancel()
	fence, err := c.store.Open(writeCtx, key, record)
	if err != nil {
		return err
	}
	record.Fence = fence
	c.putCache(key, record, true, now)
	return nil
}

// ReportLocalOpen is used by synchronous request handling. It never waits for
// the shared store, so a Redis incident cannot add latency to a caller request.
// Registering the local interval first lets the heartbeat retry a failed write
// without mistaking an older shared closed marker for a recovery.
func (c *sharedGuardCircuit) ReportLocalOpen(cfg ActiveConfig, endpoint ActiveEndpoint, local GuardCircuitSnapshot) {
	if c == nil || c.store == nil {
		return
	}
	key, event, ok := c.noteLocalOpen(cfg, endpoint, local)
	if !ok {
		return
	}
	go func() {
		ctx, cancel := guardCircuitSharedContext(context.Background())
		defer cancel()
		_ = c.publishLocalOpen(ctx, key, event)
	}()
}

// PublishLocalOpen gives async workers the same retry-safe local event protocol
// as the request path while retaining their bounded synchronous Redis write.
func (c *sharedGuardCircuit) PublishLocalOpen(ctx context.Context, cfg ActiveConfig, endpoint ActiveEndpoint, local GuardCircuitSnapshot) error {
	if c == nil || c.store == nil {
		return nil
	}
	key, event, ok := c.noteLocalOpen(cfg, endpoint, local)
	if !ok {
		return nil
	}
	return c.publishLocalOpen(ctx, key, event)
}

// ReconcileLocalOpen retries publication after a transient shared-store error
// without extending the local cooldown. It only accepts a closed shared record
// after the local failure has a server-side fence, preventing stale closed
// markers from reopening a newer local circuit.
func (c *sharedGuardCircuit) ReconcileLocalOpen(ctx context.Context, cfg ActiveConfig, endpoint ActiveEndpoint, local GuardCircuitSnapshot) (recovered bool, err error) {
	if c == nil || c.store == nil || local.State != GuardCircuitOpen {
		return false, nil
	}
	key, event, ok := c.noteLocalOpen(cfg, endpoint, local)
	if !ok {
		return false, nil
	}
	if event.fence == 0 {
		return false, c.publishLocalOpen(ctx, key, event)
	}
	record, found, err := c.read(ctx, key, false)
	if err != nil {
		return false, err
	}
	if !found || record.Fence < event.fence {
		c.clearLocalFence(key, event)
		return false, c.publishLocalOpen(ctx, key, event)
	}
	if record.State == GuardCircuitClosed && c.removeLocalOpen(key, event) {
		c.putCache(key, record, true, c.clock.Now())
		return true, nil
	}
	return false, nil
}

func (c *sharedGuardCircuit) noteLocalOpen(cfg ActiveConfig, endpoint ActiveEndpoint, local GuardCircuitSnapshot) (string, sharedLocalOpen, bool) {
	if c == nil || local.State != GuardCircuitOpen {
		return "", sharedLocalOpen{}, false
	}
	key := guardCircuitSharedKey(cfg, endpoint)
	now := c.clock.Now()
	openedAt := timeValue(local.OpenedAt)
	if openedAt.IsZero() {
		openedAt = now
	}
	nextProbeAt := timeValue(local.NextProbeAt)
	if nextProbeAt.IsZero() {
		nextProbeAt = openedAt.Add(guardCircuitOpenDuration)
	}
	event := sharedLocalOpen{generation: local.Generation, openedAt: openedAt, nextProbeAt: nextProbeAt}
	c.localMu.Lock()
	defer c.localMu.Unlock()
	if existing, found := c.localOpens[key]; found && sameSharedLocalOpen(existing, event) {
		return key, existing, true
	}
	c.localOpens[key] = event
	return key, event, true
}

func sameSharedLocalOpen(left, right sharedLocalOpen) bool {
	if left.generation != 0 || right.generation != 0 {
		return left.generation != 0 && left.generation == right.generation
	}
	return left.openedAt.Equal(right.openedAt)
}

func (c *sharedGuardCircuit) publishLocalOpen(ctx context.Context, key string, event sharedLocalOpen) error {
	if c == nil || c.store == nil {
		return nil
	}
	c.localMu.Lock()
	current, found := c.localOpens[key]
	if !found || !sameSharedLocalOpen(current, event) {
		c.localMu.Unlock()
		return nil
	}
	if current.publishing {
		c.localMu.Unlock()
		return nil
	}
	current.publishing = true
	c.localOpens[key] = current
	now := c.clock.Now()
	remaining := current.nextProbeAt.Sub(now)
	if remaining < 0 {
		remaining = 0
	}
	record := sharedGuardCircuitRecord{
		State:               GuardCircuitOpen,
		ConsecutiveFailures: guardCircuitFailureThreshold,
		OpenedAt:            now,
		NextProbeAt:         now.Add(remaining),
	}
	c.localMu.Unlock()
	writeCtx, cancel := guardCircuitSharedContext(ctx)
	fence, err := c.store.Open(writeCtx, key, record)
	cancel()
	published := false
	c.localMu.Lock()
	if current, found = c.localOpens[key]; found && sameSharedLocalOpen(current, event) {
		current.publishing = false
		if err == nil {
			current.fence = fence
			record.Fence = fence
			published = true
		}
		c.localOpens[key] = current
	}
	c.localMu.Unlock()
	if err != nil {
		return err
	}
	if published {
		c.putCache(key, record, true, now)
	}
	return nil
}

func (c *sharedGuardCircuit) clearLocalFence(key string, event sharedLocalOpen) {
	if c == nil {
		return
	}
	c.localMu.Lock()
	if current, found := c.localOpens[key]; found && sameSharedLocalOpen(current, event) {
		current.fence = 0
		c.localOpens[key] = current
	}
	c.localMu.Unlock()
}

func (c *sharedGuardCircuit) removeLocalOpen(key string, event sharedLocalOpen) bool {
	if c == nil {
		return false
	}
	c.localMu.Lock()
	defer c.localMu.Unlock()
	current, found := c.localOpens[key]
	if !found || !sameSharedLocalOpen(current, event) {
		return false
	}
	delete(c.localOpens, key)
	return true
}

func (c *sharedGuardCircuit) removeLocalOpenGeneration(key string, generation uint64) {
	if c == nil || generation == 0 {
		return
	}
	c.localMu.Lock()
	if current, found := c.localOpens[key]; found && current.generation == generation {
		delete(c.localOpens, key)
	}
	c.localMu.Unlock()
}

func (c *sharedGuardCircuit) TryBeginProbe(ctx context.Context, cfg ActiveConfig, endpoint ActiveEndpoint) (sharedGuardCircuitProbeResult, string, error) {
	if c == nil || c.store == nil {
		return sharedGuardCircuitProbeMissing, "", nil
	}
	now := c.clock.Now()
	leaseID := c.instanceID + ":" + strconv.FormatUint(c.sequence.Add(1), 10)
	probeCtx, cancel := guardCircuitSharedContext(ctx)
	defer cancel()
	result, err := c.store.TryBeginProbe(probeCtx, guardCircuitSharedKey(cfg, endpoint), leaseID, now, now.Add(guardCircuitSharedProbeLease))
	if err != nil {
		return sharedGuardCircuitProbeMissing, "", err
	}
	if result == sharedGuardCircuitProbeAcquired {
		record := sharedGuardCircuitRecord{State: GuardCircuitHalfOpen, ConsecutiveFailures: guardCircuitFailureThreshold, LastProbeAt: now}
		c.putCache(guardCircuitSharedKey(cfg, endpoint), record, true, now)
		return result, leaseID, nil
	}
	if result == sharedGuardCircuitProbeRecovered {
		c.putCache(guardCircuitSharedKey(cfg, endpoint), sharedGuardCircuitRecord{State: GuardCircuitClosed}, true, now)
	}
	return result, "", nil
}

func (c *sharedGuardCircuit) FinishProbe(ctx context.Context, cfg ActiveConfig, endpoint ActiveEndpoint, leaseID string, probeErr error) error {
	return c.finishProbe(ctx, cfg, endpoint, leaseID, probeErr, 0)
}

// FinishLocalProbe records a recovery probe that this process owned for a
// known local circuit generation. The generation guard prevents a late probe
// completion from deleting a newer local failure for the same endpoint.
func (c *sharedGuardCircuit) FinishLocalProbe(ctx context.Context, cfg ActiveConfig, endpoint ActiveEndpoint, leaseID string, probeErr error, localGeneration uint64) error {
	if leaseID == "" {
		if probeErr == nil {
			c.removeLocalOpenGeneration(guardCircuitSharedKey(cfg, endpoint), localGeneration)
		}
		return nil
	}
	return c.finishProbe(ctx, cfg, endpoint, leaseID, probeErr, localGeneration)
}

func (c *sharedGuardCircuit) finishProbe(ctx context.Context, cfg ActiveConfig, endpoint ActiveEndpoint, leaseID string, probeErr error, localGeneration uint64) error {
	if c == nil || c.store == nil || leaseID == "" {
		return nil
	}
	now := c.clock.Now()
	record := sharedGuardCircuitRecord{
		State:               GuardCircuitOpen,
		ConsecutiveFailures: guardCircuitFailureThreshold,
		OpenedAt:            now,
		NextProbeAt:         now.Add(guardCircuitOpenDuration),
		LastProbeAt:         now,
	}
	if probeErr == nil {
		record = sharedGuardCircuitRecord{State: GuardCircuitClosed, LastProbeAt: now}
	}
	key := guardCircuitSharedKey(cfg, endpoint)
	finishCtx, cancel := guardCircuitSharedContext(ctx)
	defer cancel()
	updated, err := c.store.FinishProbe(finishCtx, key, leaseID, record, probeErr == nil)
	if err != nil {
		return err
	}
	if !updated {
		if probeErr == nil {
			c.removeLocalOpenGeneration(key, localGeneration)
		}
		return nil
	}
	if probeErr == nil {
		c.putCache(key, record, true, now)
		c.removeLocalOpenGeneration(key, localGeneration)
		return nil
	}
	c.putCache(key, record, true, now)
	return nil
}

// PruneInactive removes process-local cache and publication state for target
// generations that no longer exist in the configuration. Disabled endpoints
// remain represented here so their open fence survives a temporary disable and
// is still available if the identical generation is re-enabled.
func (c *sharedGuardCircuit) PruneInactive(cfg ActiveConfig) {
	if c == nil {
		return
	}
	configured := make(map[string]struct{}, len(cfg.Endpoints))
	for _, endpoint := range cfg.Endpoints {
		configured[guardCircuitSharedKey(cfg, endpoint)] = struct{}{}
	}
	c.cacheMu.Lock()
	for key := range c.cache {
		if _, ok := configured[key]; !ok {
			delete(c.cache, key)
		}
	}
	c.cacheMu.Unlock()
	c.localMu.Lock()
	for key := range c.localOpens {
		if _, ok := configured[key]; !ok {
			delete(c.localOpens, key)
		}
	}
	c.localMu.Unlock()
}

func (c *sharedGuardCircuit) Overlay(ctx context.Context, cfg ActiveConfig, snapshots map[string]GuardCircuitSnapshot) (map[string]GuardCircuitSnapshot, error) {
	if c == nil || c.store == nil {
		return snapshots, nil
	}
	if snapshots == nil {
		snapshots = make(map[string]GuardCircuitSnapshot, len(cfg.EnabledEndpoints()))
	}
	for _, endpoint := range cfg.EnabledEndpoints() {
		record, found, err := c.read(ctx, guardCircuitSharedKey(cfg, endpoint), true)
		if err != nil {
			return snapshots, err
		}
		if !found {
			continue
		}
		// A shared closed marker is not sufficient to clear an in-process open
		// interval unless ReconcileLocalOpen has validated its fence. Keep the
		// local state visible until that heartbeat performs the transition.
		if local, exists := snapshots[endpoint.ID]; exists && local.State != GuardCircuitClosed && record.State == GuardCircuitClosed {
			continue
		}
		snapshots[endpoint.ID] = sharedGuardCircuitSnapshot(record)
	}
	return snapshots, nil
}

func (c *sharedGuardCircuit) read(ctx context.Context, key string, allowCache bool) (sharedGuardCircuitRecord, bool, error) {
	if c == nil || c.store == nil {
		return sharedGuardCircuitRecord{}, false, nil
	}
	now := c.clock.Now()
	c.cacheMu.Lock()
	entry, cached := c.cache[key]
	c.cacheMu.Unlock()
	if allowCache && cached && !entry.observedAt.IsZero() && !now.Before(entry.observedAt) && now.Sub(entry.observedAt) <= guardCircuitSharedCacheTTL {
		return entry.record, entry.found, nil
	}
	readCtx, cancel := guardCircuitSharedContext(ctx)
	defer cancel()
	record, found, err := c.store.Read(readCtx, key)
	if err != nil {
		return sharedGuardCircuitRecord{}, false, err
	}
	c.putCache(key, record, found, now)
	return record, found, nil
}

func (c *sharedGuardCircuit) putCache(key string, record sharedGuardCircuitRecord, found bool, observedAt time.Time) {
	if c == nil {
		return
	}
	c.cacheMu.Lock()
	c.cache[key] = sharedCircuitCacheEntry{record: record, found: found, observedAt: observedAt}
	c.cacheMu.Unlock()
}

func guardCircuitSharedContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, guardCircuitSharedOperationTTL)
}

func guardCircuitSharedKey(cfg ActiveConfig, endpoint ActiveEndpoint) string {
	return guardCircuitSharedKeyPrefix + guardCircuitKey(cfg, endpoint)
}

func sharedGuardCircuitSnapshot(record sharedGuardCircuitRecord) GuardCircuitSnapshot {
	return GuardCircuitSnapshot{
		State:               record.State,
		ConsecutiveFailures: record.ConsecutiveFailures,
		OpenedAt:            circuitTimePointer(record.OpenedAt),
		NextProbeAt:         circuitTimePointer(record.NextProbeAt),
		LastProbeAt:         circuitTimePointer(record.LastProbeAt),
	}
}

func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}

type redisSharedGuardCircuitStore struct {
	client *redis.Client
}

func newRedisSharedGuardCircuitStore(client *redis.Client) sharedGuardCircuitStore {
	// Keep an unavailable store as a real coordinator boundary instead of
	// returning nil. Async admission treats its read error as fail-closed, so a
	// missing Redis client cannot silently bypass the shared circuit and consume
	// jobs that cannot read their Redis payload.
	return &redisSharedGuardCircuitStore{client: client}
}

func (s *redisSharedGuardCircuitStore) Read(ctx context.Context, key string) (sharedGuardCircuitRecord, bool, error) {
	if s == nil || s.client == nil {
		return sharedGuardCircuitRecord{}, false, errors.New("prompt audit shared circuit store unavailable")
	}
	values, err := s.client.HGetAll(ctx, key).Result()
	if err != nil {
		return sharedGuardCircuitRecord{}, false, err
	}
	if len(values) == 0 {
		return sharedGuardCircuitRecord{}, false, nil
	}
	record, err := decodeSharedGuardCircuitRecord(values)
	if err != nil {
		return sharedGuardCircuitRecord{}, false, err
	}
	return record, true, nil
}

func (s *redisSharedGuardCircuitStore) Open(ctx context.Context, key string, record sharedGuardCircuitRecord) (uint64, error) {
	if s == nil || s.client == nil {
		return 0, errors.New("prompt audit shared circuit store unavailable")
	}
	fence, err := openSharedGuardCircuitScript.Run(ctx, s.client, []string{key}, strconv.Itoa(record.ConsecutiveFailures), sharedCircuitCooldownMilliseconds(record), guardCircuitSharedStateTTL.Milliseconds()).Int64()
	if err != nil {
		return 0, err
	}
	if fence <= 0 {
		return 0, errors.New("prompt audit shared circuit fence invalid")
	}
	return uint64(fence), nil
}

var openSharedGuardCircuitScript = redis.NewScript(`
-- Redis 3.2-4.x compat: opt into effects replication so redis.call('TIME')
-- remains valid under script replication.
redis.replicate_commands()
local redis_time = redis.call('TIME')
local now = tonumber(redis_time[1]) * 1000 + math.floor(tonumber(redis_time[2]) / 1000)
local fence = redis.call('HINCRBY', KEYS[1], 'fence', 1)
redis.call('HSET', KEYS[1], 'state', 'open', 'consecutive_failures', ARGV[1], 'opened_at_unix_ms', now, 'next_probe_at_unix_ms', now + tonumber(ARGV[2]), 'last_probe_at_unix_ms', '0')
redis.call('HDEL', KEYS[1], 'lease_id', 'lease_until_unix_ms')
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return fence
`)

var beginSharedGuardCircuitProbeScript = redis.NewScript(`
-- Redis 3.2-4.x compat: opt into effects replication so redis.call('TIME')
-- remains valid under script replication.
redis.replicate_commands()
local state = redis.call('HGET', KEYS[1], 'state')
if not state then
  return 0
end
local redis_time = redis.call('TIME')
local now = tonumber(redis_time[1]) * 1000 + math.floor(tonumber(redis_time[2]) / 1000)
if state == 'open' then
  local next_probe_at = tonumber(redis.call('HGET', KEYS[1], 'next_probe_at_unix_ms') or '0')
  if next_probe_at > now then
    return 2
  end
elseif state == 'half_open' then
  local lease_until = tonumber(redis.call('HGET', KEYS[1], 'lease_until_unix_ms') or '0')
  if lease_until > now then
    return 2
  end
else
  if state == 'closed' then
    return 3
  end
  return 0
end
redis.call('HSET', KEYS[1], 'state', 'half_open', 'lease_id', ARGV[1], 'lease_until_unix_ms', now + tonumber(ARGV[2]), 'last_probe_at_unix_ms', now)
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return 1
`)

func (s *redisSharedGuardCircuitStore) TryBeginProbe(ctx context.Context, key, leaseID string, now, leaseUntil time.Time) (sharedGuardCircuitProbeResult, error) {
	if s == nil || s.client == nil {
		return sharedGuardCircuitProbeMissing, errors.New("prompt audit shared circuit store unavailable")
	}
	leaseDuration := leaseUntil.Sub(now)
	if leaseDuration <= 0 {
		leaseDuration = guardCircuitSharedProbeLease
	}
	result, err := beginSharedGuardCircuitProbeScript.Run(ctx, s.client, []string{key}, leaseID, leaseDuration.Milliseconds(), guardCircuitSharedStateTTL.Milliseconds()).Int()
	if err != nil {
		return sharedGuardCircuitProbeMissing, err
	}
	switch result {
	case 0:
		return sharedGuardCircuitProbeMissing, nil
	case 1:
		return sharedGuardCircuitProbeAcquired, nil
	case 2:
		return sharedGuardCircuitProbeBusy, nil
	case 3:
		return sharedGuardCircuitProbeRecovered, nil
	default:
		return sharedGuardCircuitProbeMissing, fmt.Errorf("prompt audit shared circuit probe result invalid")
	}
}

var finishSharedGuardCircuitProbeScript = redis.NewScript(`
-- Redis 3.2-4.x compat: opt into effects replication so redis.call('TIME')
-- remains valid under script replication.
redis.replicate_commands()
local state = redis.call('HGET', KEYS[1], 'state')
local lease_id = redis.call('HGET', KEYS[1], 'lease_id')
if state ~= 'half_open' or lease_id ~= ARGV[1] then
  return 0
end
if ARGV[2] == '1' then
	local redis_time = redis.call('TIME')
	local now = tonumber(redis_time[1]) * 1000 + math.floor(tonumber(redis_time[2]) / 1000)
	redis.call('HSET', KEYS[1], 'state', 'closed', 'consecutive_failures', '0', 'opened_at_unix_ms', '0', 'next_probe_at_unix_ms', '0', 'last_probe_at_unix_ms', now)
	redis.call('HDEL', KEYS[1], 'lease_id', 'lease_until_unix_ms')
	redis.call('PEXPIRE', KEYS[1], ARGV[5])
	return 1
end
local redis_time = redis.call('TIME')
local now = tonumber(redis_time[1]) * 1000 + math.floor(tonumber(redis_time[2]) / 1000)
redis.call('HSET', KEYS[1], 'state', 'open', 'consecutive_failures', ARGV[3], 'opened_at_unix_ms', now, 'next_probe_at_unix_ms', now + tonumber(ARGV[4]), 'last_probe_at_unix_ms', now)
redis.call('HDEL', KEYS[1], 'lease_id', 'lease_until_unix_ms')
redis.call('PEXPIRE', KEYS[1], ARGV[5])
return 1
`)

func (s *redisSharedGuardCircuitStore) FinishProbe(ctx context.Context, key, leaseID string, record sharedGuardCircuitRecord, success bool) (bool, error) {
	if s == nil || s.client == nil {
		return false, errors.New("prompt audit shared circuit store unavailable")
	}
	successValue := "0"
	if success {
		successValue = "1"
	}
	result, err := finishSharedGuardCircuitProbeScript.Run(ctx, s.client, []string{key}, leaseID, successValue,
		strconv.Itoa(record.ConsecutiveFailures), sharedCircuitCooldownMilliseconds(record), guardCircuitSharedStateTTL.Milliseconds()).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func encodeSharedGuardCircuitRecord(record sharedGuardCircuitRecord) map[string]any {
	return map[string]any{
		"state":                 string(record.State),
		"consecutive_failures":  record.ConsecutiveFailures,
		"opened_at_unix_ms":     unixMilliseconds(record.OpenedAt),
		"next_probe_at_unix_ms": unixMilliseconds(record.NextProbeAt),
		"last_probe_at_unix_ms": unixMilliseconds(record.LastProbeAt),
		"fence":                 record.Fence,
	}
}

func sharedCircuitCooldownMilliseconds(record sharedGuardCircuitRecord) int64 {
	cooldown := record.NextProbeAt.Sub(record.OpenedAt)
	if cooldown < 0 {
		return 0
	}
	return cooldown.Milliseconds()
}

func decodeSharedGuardCircuitRecord(values map[string]string) (sharedGuardCircuitRecord, error) {
	state := GuardCircuitState(values["state"])
	if state != GuardCircuitOpen && state != GuardCircuitHalfOpen && state != GuardCircuitClosed {
		return sharedGuardCircuitRecord{}, errors.New("prompt audit shared circuit state invalid")
	}
	failures, err := strconv.Atoi(values["consecutive_failures"])
	if err != nil || failures < 0 {
		return sharedGuardCircuitRecord{}, errors.New("prompt audit shared circuit failures invalid")
	}
	openedAt, err := parseUnixMilliseconds(values["opened_at_unix_ms"])
	if err != nil {
		return sharedGuardCircuitRecord{}, err
	}
	nextProbeAt, err := parseUnixMilliseconds(values["next_probe_at_unix_ms"])
	if err != nil {
		return sharedGuardCircuitRecord{}, err
	}
	lastProbeAt, err := parseUnixMilliseconds(values["last_probe_at_unix_ms"])
	if err != nil {
		return sharedGuardCircuitRecord{}, err
	}
	fence, err := parseSharedGuardCircuitFence(values["fence"])
	if err != nil {
		return sharedGuardCircuitRecord{}, err
	}
	return sharedGuardCircuitRecord{State: state, ConsecutiveFailures: failures, OpenedAt: openedAt, NextProbeAt: nextProbeAt, LastProbeAt: lastProbeAt, Fence: fence}, nil
}

func parseSharedGuardCircuitFence(value string) (uint64, error) {
	// A missing fence is an older record that cannot prove recovery for a newly
	// tracked local event. Reconciliation will safely overwrite it.
	if value == "" {
		return 0, nil
	}
	fence, err := strconv.ParseUint(value, 10, 64)
	if err != nil || fence == 0 {
		return 0, errors.New("prompt audit shared circuit fence invalid")
	}
	return fence, nil
}

func unixMilliseconds(value time.Time) string {
	if value.IsZero() {
		return "0"
	}
	return strconv.FormatInt(value.UTC().UnixNano()/int64(time.Millisecond), 10)
}

func parseUnixMilliseconds(value string) (time.Time, error) {
	if value == "" || value == "0" {
		return time.Time{}, nil
	}
	milliseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, errors.New("prompt audit shared circuit timestamp invalid")
	}
	return time.Unix(milliseconds/1000, (milliseconds%1000)*int64(time.Millisecond)).UTC(), nil
}
