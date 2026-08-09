package service

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	// Count a capacity failure only after the request has exhausted its bounded
	// same-account retries. This keeps one unlucky request from quarantining an
	// account by itself while preserving intermittent failures across turns.
	openAICapacityShedFailureWindow = 15 * time.Minute
	openAICapacityShedThreshold     = 3
	openAICapacityShedMaxEntries    = 4096
)

type openAICapacityShedEntry struct {
	failures       []time.Time
	lastTouched    time.Time
	lastCooldownAt time.Time
}

type openAICapacityShedDecision struct {
	FailureCount int
	TripCooldown bool
}

// openAICapacityShedState is process-local bookkeeping for logical capacity
// failures. The actual cooldown remains persisted in accounts.overload_until.
type openAICapacityShedState struct {
	mu         sync.Mutex
	entries    map[int64]openAICapacityShedEntry
	maxEntries int
}

func newOpenAICapacityShedState(maxEntries int) *openAICapacityShedState {
	if maxEntries <= 0 {
		maxEntries = openAICapacityShedMaxEntries
	}
	return &openAICapacityShedState{
		entries:    make(map[int64]openAICapacityShedEntry),
		maxEntries: maxEntries,
	}
}

func (s *openAICapacityShedState) recordFailure(accountID int64, now time.Time) openAICapacityShedDecision {
	if s == nil || accountID <= 0 {
		return openAICapacityShedDecision{}
	}
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[int64]openAICapacityShedEntry)
	}
	if s.maxEntries <= 0 {
		s.maxEntries = openAICapacityShedMaxEntries
	}

	entry, exists := s.entries[accountID]
	if !exists {
		s.evictOldestLocked()
	}
	if !exists || now.Before(entry.lastTouched) {
		entry.failures = nil
		entry.lastCooldownAt = time.Time{}
	}
	entry.failures = pruneOpenAICapacityShedFailures(entry.failures, now)
	entry.failures = append(entry.failures, now)
	if len(entry.failures) > openAICapacityShedThreshold {
		entry.failures = entry.failures[len(entry.failures)-openAICapacityShedThreshold:]
	}
	entry.lastTouched = now

	failuresSinceLastCooldown := 0
	for _, failureAt := range entry.failures {
		if entry.lastCooldownAt.IsZero() || failureAt.After(entry.lastCooldownAt) {
			failuresSinceLastCooldown++
		}
	}
	tripCooldown := len(entry.failures) >= openAICapacityShedThreshold &&
		failuresSinceLastCooldown >= openAICapacityShedThreshold
	if tripCooldown {
		entry.lastCooldownAt = now
	}
	s.entries[accountID] = entry

	return openAICapacityShedDecision{FailureCount: len(entry.failures), TripCooldown: tripCooldown}
}

func (s *openAICapacityShedState) failureStreak(accountID int64, now time.Time) int {
	if s == nil || accountID <= 0 {
		return 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[accountID]
	if !ok {
		return 0
	}
	if now.Before(entry.lastTouched) {
		delete(s.entries, accountID)
		return 0
	}
	entry.failures = pruneOpenAICapacityShedFailures(entry.failures, now)
	if len(entry.failures) == 0 {
		delete(s.entries, accountID)
		return 0
	}
	s.entries[accountID] = entry
	return len(entry.failures)
}

func pruneOpenAICapacityShedFailures(failures []time.Time, now time.Time) []time.Time {
	if len(failures) == 0 {
		return nil
	}
	cutoff := now.Add(-openAICapacityShedFailureWindow)
	firstLive := 0
	for firstLive < len(failures) && failures[firstLive].Before(cutoff) {
		firstLive++
	}
	if firstLive == 0 {
		return failures
	}
	return append([]time.Time(nil), failures[firstLive:]...)
}

func (s *openAICapacityShedState) evictOldestLocked() {
	if len(s.entries) < s.maxEntries {
		return
	}
	var oldestID int64
	var oldestAt time.Time
	found := false
	for accountID, entry := range s.entries {
		if !found || entry.lastTouched.Before(oldestAt) {
			oldestID = accountID
			oldestAt = entry.lastTouched
			found = true
		}
	}
	if found {
		delete(s.entries, oldestID)
	}
}

func (s *OpenAIGatewayService) getOpenAICapacityShedState() *openAICapacityShedState {
	if s == nil {
		return nil
	}
	s.openaiCapacityShedOnce.Do(func() {
		if s.openaiCapacityShed == nil {
			s.openaiCapacityShed = newOpenAICapacityShedState(openAICapacityShedMaxEntries)
		}
	})
	return s.openaiCapacityShed
}

func (s *OpenAIGatewayService) recordOpenAICapacityShedFailure(ctx context.Context, account *Account, signal, eventName string) {
	if s == nil || account == nil || account.Platform != PlatformOpenAI {
		return
	}
	state := s.getOpenAICapacityShedState()
	if state == nil {
		return
	}
	decision := state.recordFailure(account.ID, time.Now())
	if eventName == "" {
		eventName = "openai_capacity_shed_failure"
	}
	slog.Warn(eventName,
		"account_id", account.ID,
		"signal", signal,
		"failure_count", decision.FailureCount,
		"threshold", openAICapacityShedThreshold,
		"window_seconds", int(openAICapacityShedFailureWindow.Seconds()),
		"cooldown_tripped", decision.TripCooldown,
	)
	if !decision.TripCooldown {
		return
	}

	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	if s.rateLimitService != nil {
		s.rateLimitService.handleRepeatedOpenAICapacityShed(stateCtx, account)
		return
	}

	cooldown := 10 * time.Minute
	if s.cfg != nil && s.cfg.RateLimit.OverloadCooldownMinutes > 0 {
		cooldown = time.Duration(s.cfg.RateLimit.OverloadCooldownMinutes) * time.Minute
	}
	s.BlockAccountScheduling(account, time.Now().Add(cooldown), "openai_capacity_shed")
}

// RecordOpenAICapacityShedRetryExhausted records exactly one logical OpenAI
// request after its same-account retry budget was exhausted by a capacity
// signal. Three such logical failures inside 15 minutes use the existing
// operator-configured overload cooldown; successes deliberately do not erase
// the evidence in between.
func (s *OpenAIGatewayService) RecordOpenAICapacityShedRetryExhausted(ctx context.Context, account *Account, failoverErr *UpstreamFailoverError) {
	if s == nil || account == nil || account.Platform != PlatformOpenAI || failoverErr == nil || !failoverErr.RequestScopedTransient {
		return
	}
	s.recordOpenAICapacityShedFailure(ctx, account, "retry_exhausted", "openai_capacity_shed_retry_exhausted")
}

// RecordOpenAICapacityShedTerminalFailure records a capacity failure after a
// streamed response has already started. Replaying that request is unsafe, so
// this only contributes to the account's later cooldown decision; it never
// triggers a second upstream request for the current client response.
func (s *OpenAIGatewayService) RecordOpenAICapacityShedTerminalFailure(ctx context.Context, account *Account, payload []byte, message string) {
	if s == nil || account == nil || account.Platform != PlatformOpenAI || !isOpenAIUpstreamCapacityShedError(payload, message) {
		return
	}
	s.recordOpenAIProxyCapacityShed(account)
	s.recordOpenAICapacityShedFailure(ctx, account, "terminal_failure", "openai_capacity_shed_terminal_failure")
}
