package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type capacityShedRepoStub struct {
	AccountRepository
	overloadCalls int
	overloadUntil time.Time
}

func (r *capacityShedRepoStub) SetOverloaded(_ context.Context, _ int64, until time.Time) error {
	r.overloadCalls++
	r.overloadUntil = until
	return nil
}

func TestOpenAICapacityShedWindowCountsIntermittentLogicalFailures(t *testing.T) {
	state := newOpenAICapacityShedState(8)
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	first := state.recordFailure(42, now)
	second := state.recordFailure(42, now.Add(5*time.Minute))
	third := state.recordFailure(42, now.Add(10*time.Minute))
	fourth := state.recordFailure(42, now.Add(11*time.Minute))

	require.Equal(t, 1, first.FailureCount)
	require.False(t, first.TripCooldown)
	require.Equal(t, 2, second.FailureCount)
	require.False(t, second.TripCooldown)
	require.Equal(t, 3, third.FailureCount)
	require.True(t, third.TripCooldown)
	require.Equal(t, 3, fourth.FailureCount)
	require.False(t, fourth.TripCooldown)

	// A later incident must be able to cool the account again after three new
	// logical failures; the first cooldown must not become a permanent latch.
	laterFirst := state.recordFailure(42, now.Add(openAICapacityShedFailureWindow+12*time.Minute))
	laterSecond := state.recordFailure(42, now.Add(openAICapacityShedFailureWindow+17*time.Minute))
	laterThird := state.recordFailure(42, now.Add(openAICapacityShedFailureWindow+22*time.Minute))
	require.False(t, laterFirst.TripCooldown)
	require.False(t, laterSecond.TripCooldown)
	require.True(t, laterThird.TripCooldown)
}

func TestOpenAI529WaitsForRepeatedRetryExhaustionBeforeCooldown(t *testing.T) {
	repo := &capacityShedRepoStub{}
	cfg := &config.Config{}
	cfg.RateLimit.OverloadCooldownMinutes = 6
	rateLimits := NewRateLimitService(repo, nil, cfg, nil, nil)
	svc := &OpenAIGatewayService{
		cfg:                cfg,
		rateLimitService:   rateLimits,
		openaiCapacityShed: newOpenAICapacityShedState(8),
	}
	rateLimits.SetAccountRuntimeBlocker(svc)
	account := &Account{ID: 101, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	require.True(t, isOpenAIAccountCapacityShedError(account, 529, "", nil))
	require.False(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, 529, http.Header{}, nil))
	require.Zero(t, repo.overloadCalls, "a single 529 must leave the same-account retry budget intact")

	failoverErr := newOpenAIUpstreamFailoverError(account, 529, http.Header{}, nil, "", false)
	require.True(t, failoverErr.RequestScopedTransient)
	require.True(t, failoverErr.RetryableOnSameAccount)
	for range openAICapacityShedThreshold {
		svc.RecordOpenAICapacityShedRetryExhausted(context.Background(), account, failoverErr)
	}
	require.Equal(t, 1, repo.overloadCalls)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestNonOpenAICapacityDoesNotUseOpenAIRetryPolicy(t *testing.T) {
	account := &Account{ID: 102, Platform: PlatformGrok, Type: AccountTypeOAuth}
	failoverErr := newOpenAIUpstreamFailoverError(account, 529, http.Header{}, nil, "", false)
	require.False(t, isOpenAIAccountCapacityShedError(account, 529, "", nil))
	require.False(t, failoverErr.RequestScopedTransient)
	require.False(t, failoverErr.RetryableOnSameAccount)
}

func TestOpenAIStreamCapacityStatusPrefersRateLimitAndMessageOnlyWebSocket(t *testing.T) {
	payload := []byte(`{"response":{"error":{"type":"invalid_request_error","code":"server_is_overloaded"}}}`)
	require.Equal(t, http.StatusTooManyRequests, openAIStreamFailedEventSemanticStatus(payload, "rate_limit exceeded"))

	wsPayload := []byte(`{"type":"response.failed","response":{"error":{"message":"Selected model is at capacity. Please try a different model."}}}`)
	require.Equal(t, http.StatusServiceUnavailable, openAIWSPayloadTransientStatus(wsPayload))
}
