package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// openAICapacityShedRequestTracker admits at most one retry-exhausted capacity
// sample per logical client request. A single request may walk several accounts
// after each one exhausts its bounded same-account retries; recording every one
// would turn three pool-wide shedding requests into a simultaneous cooldown of
// the entire pool. The first exhausted OpenAI account remains attributable.
type openAICapacityShedRequestTracker struct {
	recorded bool
}

// resetAfterSuccessfulTurn opens a new logical request for a long-lived
// Responses WebSocket. It deliberately runs only after a successful turn: a
// capacity failure followed by an account switch is still the same turn and
// must retain its first-account sample.
func (t *openAICapacityShedRequestTracker) resetAfterSuccessfulTurn() {
	if t != nil {
		t.recorded = false
	}
}

func (t *openAICapacityShedRequestTracker) recordRetryExhausted(
	ctx context.Context,
	gatewayService *service.OpenAIGatewayService,
	account *service.Account,
	failoverErr *service.UpstreamFailoverError,
) bool {
	if t == nil || t.recorded || gatewayService == nil || account == nil ||
		account.ID <= 0 || account.Platform != service.PlatformOpenAI ||
		failoverErr == nil || !failoverErr.RequestScopedTransient {
		return false
	}

	gatewayService.RecordOpenAICapacityShedRetryExhausted(ctx, account, failoverErr)
	t.recorded = true
	return true
}
