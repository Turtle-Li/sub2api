package securityaudit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type manualCircuitClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *manualCircuitClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualCircuitClock) Advance(by time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(by)
	c.mu.Unlock()
}

type fakeSharedGuardCircuitStore struct {
	mu          sync.Mutex
	records     map[string]fakeSharedGuardCircuitRecord
	readErr     error
	openErr     error
	openStarted chan<- struct{}
	openRelease <-chan struct{}
	fence       uint64
}

type fakeSharedGuardCircuitRecord struct {
	record     sharedGuardCircuitRecord
	leaseID    string
	leaseUntil time.Time
}

func (s *fakeSharedGuardCircuitStore) Read(_ context.Context, key string) (sharedGuardCircuitRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr != nil {
		return sharedGuardCircuitRecord{}, false, s.readErr
	}
	record, ok := s.records[key]
	return record.record, ok, nil
}

func (s *fakeSharedGuardCircuitStore) Open(_ context.Context, key string, record sharedGuardCircuitRecord) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openStarted != nil {
		select {
		case s.openStarted <- struct{}{}:
		default:
		}
	}
	if s.openRelease != nil {
		<-s.openRelease
	}
	if s.openErr != nil {
		return 0, s.openErr
	}
	if s.records == nil {
		s.records = map[string]fakeSharedGuardCircuitRecord{}
	}
	s.fence++
	record.Fence = s.fence
	s.records[key] = fakeSharedGuardCircuitRecord{record: record}
	return record.Fence, nil
}

func (s *fakeSharedGuardCircuitStore) TryBeginProbe(_ context.Context, key, leaseID string, now, leaseUntil time.Time) (sharedGuardCircuitProbeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.records[key]
	if !ok {
		return sharedGuardCircuitProbeMissing, nil
	}
	if entry.record.State == GuardCircuitOpen && !now.Before(entry.record.NextProbeAt) {
		entry.record.State = GuardCircuitHalfOpen
		entry.record.LastProbeAt = now
		entry.leaseID, entry.leaseUntil = leaseID, leaseUntil
		s.records[key] = entry
		return sharedGuardCircuitProbeAcquired, nil
	}
	if entry.record.State == GuardCircuitHalfOpen && !now.Before(entry.leaseUntil) {
		entry.record.LastProbeAt = now
		entry.leaseID, entry.leaseUntil = leaseID, leaseUntil
		s.records[key] = entry
		return sharedGuardCircuitProbeAcquired, nil
	}
	if entry.record.State == GuardCircuitClosed {
		return sharedGuardCircuitProbeRecovered, nil
	}
	return sharedGuardCircuitProbeBusy, nil
}

func (s *fakeSharedGuardCircuitStore) FinishProbe(_ context.Context, key, leaseID string, record sharedGuardCircuitRecord, success bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.records[key]
	if !ok || entry.record.State != GuardCircuitHalfOpen || entry.leaseID != leaseID {
		return false, nil
	}
	if success {
		s.records[key] = fakeSharedGuardCircuitRecord{record: sharedGuardCircuitRecord{State: GuardCircuitClosed, LastProbeAt: record.LastProbeAt, Fence: entry.record.Fence}}
		return true, nil
	}
	record.Fence = entry.record.Fence
	s.records[key] = fakeSharedGuardCircuitRecord{record: record}
	return true, nil
}

func circuitTestConfig(endpoint ActiveEndpoint) ActiveConfig {
	return ActiveConfig{
		RiskControlEnabled: true,
		Enabled:            true,
		ConfigVersion:      17,
		Scanners:           AllScannerIDs,
		AllGroups:          true,
		Endpoints:          []ActiveEndpoint{endpoint},
	}
}

func circuitTestEndpoint() ActiveEndpoint {
	return ActiveEndpoint{ID: "office-mini", BaseURL: "http://127.0.0.1:18765", Model: DefaultGuardModel, Enabled: true, TimeoutMS: 1000, InputLimit: 1024}
}

func TestGuardCircuitOpensAndOnlyBackgroundProbeRestoresAdmission(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	circuit := newGuardCircuit(clock)
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	failure := &GuardError{Code: ErrorCodeUnavailable, Retryable: true}

	for range guardCircuitFailureThreshold {
		circuit.RecordFailure(cfg, endpoint, failure)
	}
	require.False(t, circuit.Allows(cfg, endpoint))
	status := circuit.Snapshot(cfg)[endpoint.ID]
	require.Equal(t, GuardCircuitOpen, status.State)
	require.Equal(t, guardCircuitFailureThreshold, status.ConsecutiveFailures)
	circuit.RecordSuccess(cfg, endpoint)
	require.False(t, circuit.Allows(cfg, endpoint), "a late foreground success must not bypass background recovery")

	clock.Advance(guardCircuitOpenDuration)
	require.True(t, circuit.BeginProbe(cfg, endpoint), "the recovery probe owns the only half-open permit")
	require.False(t, circuit.Allows(cfg, endpoint), "foreground calls stay rejected while the probe owns half-open")
	circuit.FinishProbe(cfg, endpoint, nil)
	require.True(t, circuit.Allows(cfg, endpoint))
	require.Equal(t, GuardCircuitClosed, circuit.Snapshot(cfg)[endpoint.ID].State)
}

func TestGuardCircuitKeepsEndpointQuarantinedAcrossUnrelatedConfigVersion(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	circuit := newGuardCircuit(clock)
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	for range guardCircuitFailureThreshold {
		circuit.RecordFailure(cfg, endpoint, &GuardError{Code: ErrorCodeUnavailable, Retryable: true})
	}

	updated := cfg
	updated.ConfigVersion++
	require.False(t, circuit.Allows(updated, endpoint), "queue or group settings must not reset endpoint quarantine")
	require.Equal(t, GuardCircuitOpen, circuit.Snapshot(updated)[endpoint.ID].State)

	clock.Advance(guardCircuitOpenDuration)
	require.True(t, circuit.BeginProbe(updated, endpoint))
	circuit.FinishProbe(updated, endpoint, nil)
	require.True(t, circuit.Allows(updated, endpoint), "only a successful recovery probe may restore admission")
}

func TestGuardCircuitKeepsEndpointQuarantinedAcrossTemporaryDisable(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	circuit := newGuardCircuit(clock)
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	for range guardCircuitFailureThreshold {
		circuit.RecordFailure(cfg, endpoint, &GuardError{Code: ErrorCodeUnavailable, Retryable: true})
	}

	disabled := cfg
	disabled.Endpoints = append([]ActiveEndpoint(nil), cfg.Endpoints...)
	disabled.Endpoints[0].Enabled = false
	circuit.PruneInactive(disabled)

	reenabled := cfg
	require.False(t, circuit.Allows(reenabled, endpoint), "re-enabling the identical target must not bypass the background recovery probe")
	require.Equal(t, GuardCircuitOpen, circuit.Snapshot(reenabled)[endpoint.ID].State)
}

func TestGuardCircuitPrunesRetiredOpenGenerationAfterTargetReplacement(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	circuit := newGuardCircuit(clock)
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	for range guardCircuitFailureThreshold {
		circuit.RecordFailure(cfg, endpoint, &GuardError{Code: ErrorCodeUnavailable, Retryable: true})
	}

	replacement := endpoint
	replacement.Token = "rotated-token"
	replacementCfg := circuitTestConfig(replacement)
	circuit.PruneInactive(replacementCfg)

	// The replacement has a distinct endpoint generation. The retired target
	// cannot be selected or probed by the current configuration, so retaining
	// its open entry would only turn repeated rotations into unbounded state.
	require.Equal(t, GuardCircuitClosed, circuit.Snapshot(cfg)[endpoint.ID].State)
	require.True(t, circuit.Allows(replacementCfg, replacement))
}

func TestGuardCircuitNeverEvictsOpenEntriesAtCapacity(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	circuit := newGuardCircuit(clock)
	failure := &GuardError{Code: ErrorCodeUnavailable, Retryable: true}
	var firstConfig ActiveConfig
	var firstEndpoint ActiveEndpoint

	// This intentionally constructs more targets than configuration validation
	// permits. A malformed/stale in-memory config must still not turn a known
	// open circuit into admission simply because the local cache is full.
	for index := 0; index <= guardCircuitMaxEntries; index++ {
		endpoint := circuitTestEndpoint()
		endpoint.ID = fmt.Sprintf("guard-%d", index)
		cfg := circuitTestConfig(endpoint)
		for range guardCircuitFailureThreshold {
			circuit.RecordFailure(cfg, endpoint, failure)
		}
		if index == 0 {
			firstConfig = cfg
			firstEndpoint = endpoint
		}
	}

	require.False(t, circuit.Allows(firstConfig, firstEndpoint))
	require.Equal(t, GuardCircuitOpen, circuit.Snapshot(firstConfig)[firstEndpoint.ID].State)
}

func TestGuardCircuitCountsOnlyModelEndpointFailures(t *testing.T) {
	require.True(t, guardCircuitFailure(&GuardError{Code: ErrorCodeUnavailable}), "authentication and protocol failures use unavailable")
	require.True(t, guardCircuitFailure(&GuardError{Code: ErrorCodeInvalidResponse}))
	require.False(t, guardCircuitFailure(&GuardError{Code: "payload_missing", Retryable: true}))
	require.False(t, guardCircuitFailure(&GuardError{Code: ErrorCodeUnavailable, Cause: context.Canceled}))
}

func TestGuardEvaluatorCircuitStopsFourthOutboundAttempt(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	circuit := newGuardCircuit(clock)
	calls := 0
	evaluator := newGuardEvaluatorWithCircuit(PromptScannerFunc(func(context.Context, ActiveEndpoint, string, []string) (*NormalizedResult, error) {
		calls++
		return nil, &GuardError{Code: ErrorCodeUnavailable, Retryable: true}
	}), nil, NewAtomicMetrics(), 2, 2, circuit)
	cfg := circuitTestConfig(circuitTestEndpoint())
	snapshot := PromptSnapshot{ScanText: "test", PromptLength: 4}

	for range guardCircuitFailureThreshold {
		_, err := evaluator.Evaluate(context.Background(), cfg, snapshot)
		require.Error(t, err)
	}
	_, err := evaluator.Evaluate(context.Background(), cfg, snapshot)
	require.Error(t, err)
	require.Equal(t, guardCircuitFailureThreshold, calls, "an open circuit must not make another model request")
}

func TestRunnerStopsSchedulingWhenAllEndpointCircuitsAreOpen(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	circuit := newGuardCircuit(clock)
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	cfg.WorkerCount = 1
	repo := &fakeJobRepository{claimQueue: []*Job{
		{ID: 1, ClaimVersion: 1, Attempts: 1, MaxAttempts: 3, Snapshot: PromptSnapshot{PromptLength: 4}},
		{ID: 2, ClaimVersion: 1, Attempts: 1, MaxAttempts: 3, Snapshot: PromptSnapshot{PromptLength: 4}},
		{ID: 3, ClaimVersion: 1, Attempts: 1, MaxAttempts: 3, Snapshot: PromptSnapshot{PromptLength: 4}},
		{ID: 4, ClaimVersion: 1, Attempts: 1, MaxAttempts: 3, Snapshot: PromptSnapshot{PromptLength: 4}},
		{ID: 5, ClaimVersion: 1, Attempts: 1, MaxAttempts: 3, Snapshot: PromptSnapshot{PromptLength: 4}},
	}}
	payload := &fakePayloadStore{values: map[int64]string{1: "test", 2: "test", 3: "test", 4: "test", 5: "test"}}
	calls := 0
	runner := newRunnerWithCircuit(&fakeConfigStore{cfg: cfg, active: true}, repo, payload, PromptScannerFunc(func(context.Context, ActiveEndpoint, string, []string) (*NormalizedResult, error) {
		calls++
		return nil, &GuardError{Code: ErrorCodeUnavailable, Retryable: true}
	}), NewAtomicMetrics(), circuit)

	runner.processAvailable(context.Background(), 0, cfg)
	require.False(t, runner.CanSchedule(context.Background(), cfg))
	require.Equal(t, guardCircuitFailureThreshold, calls)
	require.Len(t, repo.claimQueue, 2, "open circuit must leave unclaimed work in the queue")
}

func TestPromptServiceNilRedisClientLeavesAsyncJobsQueued(t *testing.T) {
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	repo := &fakeJobRepository{claimQueue: []*Job{{
		ID: 71, ClaimVersion: 1, Attempts: 1, MaxAttempts: 3, Snapshot: PromptSnapshot{PromptLength: 4},
	}}}
	calls := 0
	service := NewPromptService(
		&fakeConfigStore{cfg: cfg, active: true},
		NewPostgreSQLRepository(nil),
		NewRedisPayloadStore(nil),
		PromptScannerFunc(func(context.Context, ActiveEndpoint, string, []string) (*NormalizedResult, error) {
			calls++
			return nil, errors.New("scanner must not run while Redis is unavailable")
		}),
		NewAtomicMetrics(),
	)
	service.runner.repo = repo

	require.NotNil(t, service.shared)
	require.False(t, service.runner.CanSchedule(context.Background(), cfg))
	service.runner.processAvailable(context.Background(), 0, cfg)
	require.Len(t, repo.claimQueue, 1)
	require.Zero(t, repo.retried)
	require.Zero(t, repo.failed)
	require.Zero(t, calls)
}

func TestSharedCircuitStopsDrainingConsumerFromClaimingJobs(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	store := &fakeSharedGuardCircuitStore{}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	cfg.WorkerCount = 1

	ownerRepo := &fakeJobRepository{claimQueue: []*Job{
		{ID: 1, ClaimVersion: 1, Attempts: 1, MaxAttempts: 3, Snapshot: PromptSnapshot{PromptLength: 4}},
		{ID: 2, ClaimVersion: 2, Attempts: 1, MaxAttempts: 3, Snapshot: PromptSnapshot{PromptLength: 4}},
		{ID: 3, ClaimVersion: 3, Attempts: 1, MaxAttempts: 3, Snapshot: PromptSnapshot{PromptLength: 4}},
	}}
	ownerCalls := 0
	owner := newRunnerWithCircuits(&fakeConfigStore{cfg: cfg, active: true}, ownerRepo, &fakePayloadStore{values: map[int64]string{1: "test", 2: "test", 3: "test"}}, PromptScannerFunc(func(context.Context, ActiveEndpoint, string, []string) (*NormalizedResult, error) {
		ownerCalls++
		return nil, &GuardError{Code: ErrorCodeUnavailable, Retryable: true}
	}), NewAtomicMetrics(), newGuardCircuit(clock), newSharedGuardCircuit(store, clock))
	owner.clock = clock
	owner.processAvailable(context.Background(), 0, cfg)
	require.Equal(t, guardCircuitFailureThreshold, ownerCalls)

	repo := &fakeJobRepository{claimQueue: []*Job{{ID: 42, ClaimVersion: 9, Snapshot: PromptSnapshot{PromptLength: 4}}}}
	calls := 0
	runner := newRunnerWithCircuits(&fakeConfigStore{cfg: cfg, active: true}, repo, &fakePayloadStore{values: map[int64]string{42: "test"}}, PromptScannerFunc(func(context.Context, ActiveEndpoint, string, []string) (*NormalizedResult, error) {
		calls++
		return nil, nil
	}), NewAtomicMetrics(), newGuardCircuit(clock), newSharedGuardCircuit(store, clock))
	runner.clock = clock

	require.False(t, runner.CanSchedule(context.Background(), cfg))
	runner.processAvailable(context.Background(), 0, cfg)
	require.Zero(t, calls)
	require.Len(t, repo.claimQueue, 1, "a draining color must leave the shared queue untouched")
}

func TestSharedCircuitStoreFailureStopsAsyncScheduling(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	store := &fakeSharedGuardCircuitStore{readErr: errors.New("redis unavailable")}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	cfg.WorkerCount = 1
	runner := newRunnerWithCircuits(&fakeConfigStore{cfg: cfg, active: true}, &fakeJobRepository{}, &fakePayloadStore{}, PromptScannerFunc(func(context.Context, ActiveEndpoint, string, []string) (*NormalizedResult, error) {
		return nil, nil
	}), NewAtomicMetrics(), newGuardCircuit(clock), newSharedGuardCircuit(store, clock))
	runner.clock = clock

	require.False(t, runner.CanSchedule(context.Background(), cfg))
}

func TestSharedCircuitFreshAdmissionBypassesShortClosedCache(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	store := &fakeSharedGuardCircuitStore{}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	observer := newSharedGuardCircuit(store, clock)
	allowed, err := observer.Allows(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.True(t, allowed)
	require.NoError(t, newSharedGuardCircuit(store, clock).Open(context.Background(), cfg, endpoint))

	allowed, err = observer.Allows(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.True(t, allowed, "idle worker polling may use its short closed-state cache")
	allowed, err = observer.AllowsFresh(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.False(t, allowed, "claim and dispatch gates must refresh shared state")
}

func TestRedisSharedCircuitCoordinatesCrossInstanceRecovery(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(server.Close)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	redisNow := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	server.SetTime(redisNow)
	clock := &manualCircuitClock{now: redisNow}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	owner := newSharedGuardCircuit(newRedisSharedGuardCircuitStore(client), clock)
	draining := newSharedGuardCircuit(newRedisSharedGuardCircuitStore(client), clock)

	require.NoError(t, owner.Open(context.Background(), cfg, endpoint))
	allowed, err := draining.AllowsFresh(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.False(t, allowed)
	clock.Advance(guardCircuitOpenDuration)
	server.SetTime(redisNow.Add(guardCircuitOpenDuration))
	state, leaseID, err := owner.TryBeginProbe(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.Equal(t, sharedGuardCircuitProbeAcquired, state)
	state, _, err = draining.TryBeginProbe(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.Equal(t, sharedGuardCircuitProbeBusy, state)
	require.NoError(t, owner.FinishProbe(context.Background(), cfg, endpoint, leaseID, nil))

	allowed, err = draining.AllowsFresh(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.True(t, allowed)
	state, _, err = draining.TryBeginProbe(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.Equal(t, sharedGuardCircuitProbeRecovered, state)
}

func TestRedisSharedCircuitRejectsLateProbeSuccessAfterNewFailure(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(server.Close)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	redisNow := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	server.SetTime(redisNow)
	clock := &manualCircuitClock{now: redisNow}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	circuit := newSharedGuardCircuit(newRedisSharedGuardCircuitStore(client), clock)
	require.NoError(t, circuit.Open(context.Background(), cfg, endpoint))
	clock.Advance(guardCircuitOpenDuration)
	server.SetTime(redisNow.Add(guardCircuitOpenDuration))
	state, leaseID, err := circuit.TryBeginProbe(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.Equal(t, sharedGuardCircuitProbeAcquired, state)

	// A real failure while the probe is in flight wins over the probe's late
	// success and starts a new cooldown.
	require.NoError(t, circuit.Open(context.Background(), cfg, endpoint))
	require.NoError(t, circuit.FinishProbe(context.Background(), cfg, endpoint, leaseID, nil))
	allowed, err := circuit.AllowsFresh(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestRunnerReleasesClaimWhenCircuitOpensBeforeDispatch(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	circuit := newGuardCircuit(clock)
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	cfg.WorkerCount = 1
	repo := &fakeJobRepository{claimQueue: []*Job{{ID: 77, ClaimVersion: 3, Attempts: 0, MaxAttempts: 1, Snapshot: PromptSnapshot{PromptLength: 4}}}}
	repo.claimHook = func() {
		for range guardCircuitFailureThreshold {
			circuit.RecordFailure(cfg, endpoint, &GuardError{Code: ErrorCodeUnavailable, Retryable: true})
		}
	}
	calls := 0
	runner := newRunnerWithCircuit(&fakeConfigStore{cfg: cfg, active: true}, repo, &fakePayloadStore{values: map[int64]string{77: "test"}}, PromptScannerFunc(func(context.Context, ActiveEndpoint, string, []string) (*NormalizedResult, error) {
		calls++
		return nil, &GuardError{Code: ErrorCodeUnavailable, Retryable: true}
	}), NewAtomicMetrics(), circuit)
	runner.clock = clock

	runner.processAvailable(context.Background(), 0, cfg)
	require.Equal(t, 1, repo.released)
	require.Zero(t, calls, "an opened circuit must release before any model call")
	require.Zero(t, repo.retried)
	require.Zero(t, repo.failed)
	require.Len(t, repo.claimQueue, 1, "the final allowed attempt must remain queued")
}

func TestRunnerReleasesClaimWhenSharedCircuitOpensBeforeDispatch(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	store := &fakeSharedGuardCircuitStore{}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	cfg.WorkerCount = 1
	owner := newSharedGuardCircuit(store, clock)
	repo := &fakeJobRepository{claimQueue: []*Job{{ID: 78, ClaimVersion: 4, Attempts: 0, MaxAttempts: 1, Snapshot: PromptSnapshot{PromptLength: 4}}}}
	repo.claimHook = func() { require.NoError(t, owner.Open(context.Background(), cfg, endpoint)) }
	calls := 0
	runner := newRunnerWithCircuits(&fakeConfigStore{cfg: cfg, active: true}, repo, &fakePayloadStore{values: map[int64]string{78: "test"}}, PromptScannerFunc(func(context.Context, ActiveEndpoint, string, []string) (*NormalizedResult, error) {
		calls++
		return nil, &GuardError{Code: ErrorCodeUnavailable, Retryable: true}
	}), NewAtomicMetrics(), newGuardCircuit(clock), newSharedGuardCircuit(store, clock))
	runner.clock = clock

	runner.processAvailable(context.Background(), 0, cfg)
	require.Equal(t, 1, repo.released)
	require.Zero(t, calls)
	require.Zero(t, repo.retried)
	require.Zero(t, repo.failed)
	require.Len(t, repo.claimQueue, 1)
}

func TestPromptServiceBackgroundProbeRestoresOnlyAnOpenCircuit(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	circuit := newGuardCircuit(clock)
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	for range guardCircuitFailureThreshold {
		circuit.RecordFailure(cfg, endpoint, &GuardError{Code: ErrorCodeUnavailable, Retryable: true})
	}
	clock.Advance(guardCircuitOpenDuration)
	calls := 0
	service := &PromptService{
		config: &fakeConfigStore{cfg: cfg, active: true},
		scanner: PromptScannerFunc(func(_ context.Context, actual ActiveEndpoint, text string, _ []string) (*NormalizedResult, error) {
			calls++
			require.Equal(t, endpoint.ID, actual.ID)
			require.Equal(t, guardCircuitProbeText, text)
			return &NormalizedResult{Decision: EventPass, RiskLevel: RiskLow, Action: ActionAllow, ScannerScores: map[string]float64{}, ScannerEvidence: map[string]string{}}, nil
		}),
		circuit: circuit,
		clock:   clock,
		probes:  map[string]ProbeResult{},
	}

	service.probeOpenCircuits(context.Background())
	require.Equal(t, 1, calls)
	require.True(t, circuit.Allows(cfg, endpoint))
	require.True(t, service.probeSnapshot()[endpoint.ID].OK)
}

func TestPromptServiceSharedProbeRecoversForAllConsumersWithOneLease(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	store := &fakeSharedGuardCircuitStore{}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	owner := newSharedGuardCircuit(store, clock)
	require.NoError(t, owner.Open(context.Background(), cfg, endpoint))
	clock.Advance(guardCircuitOpenDuration)

	first := newSharedGuardCircuit(store, clock)
	second := newSharedGuardCircuit(store, clock)
	state, leaseID, err := first.TryBeginProbe(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.Equal(t, sharedGuardCircuitProbeAcquired, state)
	state, _, err = second.TryBeginProbe(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.Equal(t, sharedGuardCircuitProbeBusy, state, "only one process may issue the recovery probe")
	require.NoError(t, first.FinishProbe(context.Background(), cfg, endpoint, leaseID, nil))

	repo := &fakeJobRepository{claimQueue: []*Job{{ID: 91, ClaimVersion: 1, Snapshot: PromptSnapshot{PromptLength: 4}}}}
	runner := newRunnerWithCircuits(&fakeConfigStore{cfg: cfg, active: true}, repo, &fakePayloadStore{values: map[int64]string{91: "test"}}, PromptScannerFunc(func(context.Context, ActiveEndpoint, string, []string) (*NormalizedResult, error) {
		return &NormalizedResult{Decision: EventPass, RiskLevel: RiskLow, Action: ActionAllow, ScannerScores: map[string]float64{}, ScannerEvidence: map[string]string{}}, nil
	}), NewAtomicMetrics(), newGuardCircuit(clock), second)
	runner.clock = clock
	require.True(t, runner.CanSchedule(context.Background(), cfg), "a successful shared probe must restore every consumer")
}

func TestPromptServiceUsesSharedLeaseForBackgroundRecoveryProbe(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	store := &fakeSharedGuardCircuitStore{}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	owner := newSharedGuardCircuit(store, clock)
	require.NoError(t, owner.Open(context.Background(), cfg, endpoint))
	clock.Advance(guardCircuitOpenDuration)
	calls := 0
	service := &PromptService{
		config: &fakeConfigStore{cfg: cfg, active: true},
		scanner: PromptScannerFunc(func(_ context.Context, actual ActiveEndpoint, text string, _ []string) (*NormalizedResult, error) {
			calls++
			require.Equal(t, endpoint.ID, actual.ID)
			require.Equal(t, guardCircuitProbeText, text)
			return &NormalizedResult{Decision: EventPass, RiskLevel: RiskLow, Action: ActionAllow, ScannerScores: map[string]float64{}, ScannerEvidence: map[string]string{}}, nil
		}),
		circuit: newGuardCircuit(clock),
		shared:  newSharedGuardCircuit(store, clock),
		clock:   clock,
		probes:  map[string]ProbeResult{},
	}

	service.probeOpenCircuits(context.Background())
	require.Equal(t, 1, calls)
	allowed, err := service.shared.Allows(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.True(t, allowed)
	require.True(t, service.probeSnapshot()[endpoint.ID].OK)

}

func TestSharedCircuitReconcileRepublishesAfterTransientWriteWithoutExtendingCooldown(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	store := &fakeSharedGuardCircuitStore{openErr: errors.New("redis unavailable")}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	localCircuit := newGuardCircuit(clock)
	for range guardCircuitFailureThreshold {
		localCircuit.RecordFailure(cfg, endpoint, &GuardError{Code: ErrorCodeUnavailable, Retryable: true})
	}
	local := localCircuit.Snapshot(cfg)[endpoint.ID]
	shared := newSharedGuardCircuit(store, clock)
	require.Error(t, shared.PublishLocalOpen(context.Background(), cfg, endpoint, local))

	clock.Advance(29 * time.Second)
	store.mu.Lock()
	store.openErr = nil
	store.mu.Unlock()
	recovered, err := shared.ReconcileLocalOpen(context.Background(), cfg, endpoint, local)
	require.NoError(t, err)
	require.False(t, recovered)
	record, found, err := store.Read(context.Background(), guardCircuitSharedKey(cfg, endpoint))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, GuardCircuitOpen, record.State)
	require.WithinDuration(t, clock.Now().Add(time.Second), record.NextProbeAt, time.Millisecond)

	clock.Advance(time.Second)
	state, _, err := newSharedGuardCircuit(store, clock).TryBeginProbe(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.Equal(t, sharedGuardCircuitProbeAcquired, state, "republication must preserve the original remaining cooldown")
}

func TestSharedCircuitPrunesRetiredStateButKeepsTemporaryDisabledFence(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	store := &fakeSharedGuardCircuitStore{}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	localCircuit := newGuardCircuit(clock)
	for range guardCircuitFailureThreshold {
		localCircuit.RecordFailure(cfg, endpoint, &GuardError{Code: ErrorCodeUnavailable, Retryable: true})
	}
	shared := newSharedGuardCircuit(store, clock)
	require.NoError(t, shared.PublishLocalOpen(context.Background(), cfg, endpoint, localCircuit.Snapshot(cfg)[endpoint.ID]))

	disabled := cfg
	disabled.Endpoints = append([]ActiveEndpoint(nil), cfg.Endpoints...)
	disabled.Endpoints[0].Enabled = false
	shared.PruneInactive(disabled)
	shared.localMu.Lock()
	disabledEvents := len(shared.localOpens)
	shared.localMu.Unlock()
	require.Equal(t, 1, disabledEvents)

	replacement := endpoint
	replacement.Token = "rotated-token"
	shared.PruneInactive(circuitTestConfig(replacement))
	shared.cacheMu.Lock()
	cacheEntries := len(shared.cache)
	shared.cacheMu.Unlock()
	shared.localMu.Lock()
	localEvents := len(shared.localOpens)
	shared.localMu.Unlock()
	require.Zero(t, cacheEntries)
	require.Zero(t, localEvents)
}

func TestSharedCircuitFinishLocalProbeRemovesCompletedPublicationEvent(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	store := &fakeSharedGuardCircuitStore{}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	localCircuit := newGuardCircuit(clock)
	for range guardCircuitFailureThreshold {
		localCircuit.RecordFailure(cfg, endpoint, &GuardError{Code: ErrorCodeUnavailable, Retryable: true})
	}
	local := localCircuit.Snapshot(cfg)[endpoint.ID]
	shared := newSharedGuardCircuit(store, clock)
	require.NoError(t, shared.PublishLocalOpen(context.Background(), cfg, endpoint, local))
	clock.Advance(guardCircuitOpenDuration)
	state, leaseID, err := shared.TryBeginProbe(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.Equal(t, sharedGuardCircuitProbeAcquired, state)
	require.NoError(t, shared.FinishLocalProbe(context.Background(), cfg, endpoint, leaseID, nil, local.Generation))
	shared.localMu.Lock()
	localEvents := len(shared.localOpens)
	shared.localMu.Unlock()
	require.Zero(t, localEvents)
}

func TestSharedCircuitFinishLocalProbeKeepsNewerPublicationEvent(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	store := &fakeSharedGuardCircuitStore{}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	localCircuit := newGuardCircuit(clock)
	for range guardCircuitFailureThreshold {
		localCircuit.RecordFailure(cfg, endpoint, &GuardError{Code: ErrorCodeUnavailable, Retryable: true})
	}
	local := localCircuit.Snapshot(cfg)[endpoint.ID]
	shared := newSharedGuardCircuit(store, clock)
	require.NoError(t, shared.PublishLocalOpen(context.Background(), cfg, endpoint, local))
	clock.Advance(guardCircuitOpenDuration)
	state, leaseID, err := shared.TryBeginProbe(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.Equal(t, sharedGuardCircuitProbeAcquired, state)

	newer := local
	newer.Generation++
	now := clock.Now()
	newer.OpenedAt = &now
	next := now.Add(guardCircuitOpenDuration)
	newer.NextProbeAt = &next
	_, _, ok := shared.noteLocalOpen(cfg, endpoint, newer)
	require.True(t, ok)
	require.NoError(t, shared.FinishLocalProbe(context.Background(), cfg, endpoint, leaseID, nil, local.Generation))

	key := guardCircuitSharedKey(cfg, endpoint)
	shared.localMu.Lock()
	event, found := shared.localOpens[key]
	shared.localMu.Unlock()
	require.True(t, found)
	require.Equal(t, newer.Generation, event.generation)
}

func TestReportLocalOpenDoesNotWaitForAnInFlightSharedWrite(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	store := &fakeSharedGuardCircuitStore{openStarted: started, openRelease: release}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	localCircuit := newGuardCircuit(clock)
	for range guardCircuitFailureThreshold {
		localCircuit.RecordFailure(cfg, endpoint, &GuardError{Code: ErrorCodeUnavailable, Retryable: true})
	}
	local := localCircuit.Snapshot(cfg)[endpoint.ID]
	shared := newSharedGuardCircuit(store, clock)
	shared.ReportLocalOpen(cfg, endpoint, local)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected background shared write to start")
	}

	done := make(chan struct{})
	go func() {
		shared.ReportLocalOpen(cfg, endpoint, local)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("synchronous circuit reporting waited for the in-flight shared write")
	}
	close(release)
}

func TestSharedCircuitDoesNotAcceptClosedMarkerOlderThanLocalFailure(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	store := &fakeSharedGuardCircuitStore{}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	owner := newSharedGuardCircuit(store, clock)
	require.NoError(t, owner.Open(context.Background(), cfg, endpoint))
	clock.Advance(guardCircuitOpenDuration)
	state, leaseID, err := owner.TryBeginProbe(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.Equal(t, sharedGuardCircuitProbeAcquired, state)
	require.NoError(t, owner.FinishProbe(context.Background(), cfg, endpoint, leaseID, nil))

	clock.Advance(time.Second)
	localCircuit := newGuardCircuit(clock)
	for range guardCircuitFailureThreshold {
		localCircuit.RecordFailure(cfg, endpoint, &GuardError{Code: ErrorCodeUnavailable, Retryable: true})
	}
	local := localCircuit.Snapshot(cfg)[endpoint.ID]
	shared := newSharedGuardCircuit(store, clock)
	runtime, err := shared.Overlay(context.Background(), cfg, localCircuit.Snapshot(cfg))
	require.NoError(t, err)
	require.Equal(t, GuardCircuitOpen, runtime[endpoint.ID].State, "runtime must not hide a local open behind an older shared closed marker")
	recovered, err := shared.ReconcileLocalOpen(context.Background(), cfg, endpoint, local)
	require.NoError(t, err)
	require.False(t, recovered, "a closed marker from before this local failure is not a recovery proof")
	require.False(t, localCircuit.Allows(cfg, endpoint))
	record, found, err := store.Read(context.Background(), guardCircuitSharedKey(cfg, endpoint))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, GuardCircuitOpen, record.State)
}

func TestPromptServiceAcceptsOnlyFencedSharedRecoveryForLocalCircuit(t *testing.T) {
	clock := &manualCircuitClock{now: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}
	store := &fakeSharedGuardCircuitStore{}
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	localCircuit := newGuardCircuit(clock)
	for range guardCircuitFailureThreshold {
		localCircuit.RecordFailure(cfg, endpoint, &GuardError{Code: ErrorCodeUnavailable, Retryable: true})
	}
	localShared := newSharedGuardCircuit(store, clock)
	require.NoError(t, localShared.PublishLocalOpen(context.Background(), cfg, endpoint, localCircuit.Snapshot(cfg)[endpoint.ID]))
	clock.Advance(guardCircuitOpenDuration)
	owner := newSharedGuardCircuit(store, clock)
	state, leaseID, err := owner.TryBeginProbe(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.Equal(t, sharedGuardCircuitProbeAcquired, state)
	require.NoError(t, owner.FinishProbe(context.Background(), cfg, endpoint, leaseID, nil))

	calls := 0
	service := &PromptService{
		config: &fakeConfigStore{cfg: cfg, active: true},
		scanner: PromptScannerFunc(func(context.Context, ActiveEndpoint, string, []string) (*NormalizedResult, error) {
			calls++
			return nil, errors.New("the shared recovery must avoid a second model probe")
		}),
		circuit: localCircuit,
		shared:  localShared,
		clock:   clock,
		probes:  map[string]ProbeResult{},
	}
	service.probeOpenCircuits(context.Background())
	require.Zero(t, calls)
	require.True(t, localCircuit.Allows(cfg, endpoint))
}

func TestRedisSharedCircuitLeaseUsesServerTimeDespiteCallerClockSkew(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(server.Close)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	endpoint := circuitTestEndpoint()
	cfg := circuitTestConfig(endpoint)
	redisNow := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	server.SetTime(redisNow)
	ownerClock := &manualCircuitClock{now: redisNow}
	fastClock := &manualCircuitClock{now: ownerClock.Now().Add(time.Hour)}
	owner := newSharedGuardCircuit(newRedisSharedGuardCircuitStore(client), ownerClock)
	fastPeer := newSharedGuardCircuit(newRedisSharedGuardCircuitStore(client), fastClock)
	require.NoError(t, owner.Open(context.Background(), cfg, endpoint))
	server.SetTime(redisNow.Add(guardCircuitOpenDuration))
	state, _, err := owner.TryBeginProbe(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.Equal(t, sharedGuardCircuitProbeAcquired, state)
	state, _, err = fastPeer.TryBeginProbe(context.Background(), cfg, endpoint)
	require.NoError(t, err)
	require.Equal(t, sharedGuardCircuitProbeBusy, state, "a fast peer clock must not steal the server-side recovery lease")
}
