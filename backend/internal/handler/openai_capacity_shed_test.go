package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAICapacityShedRequestTrackerRecordsOnlyFirstCapacityExhaustion(t *testing.T) {
	tracker := &openAICapacityShedRequestTracker{}
	gatewayService := &service.OpenAIGatewayService{}
	capacityErr := &service.UpstreamFailoverError{RequestScopedTransient: true}
	firstAccount := &service.Account{ID: 1, Platform: service.PlatformOpenAI}
	secondAccount := &service.Account{ID: 2, Platform: service.PlatformOpenAI}

	require.True(t, tracker.recordRetryExhausted(context.Background(), gatewayService, firstAccount, capacityErr))
	require.True(t, tracker.recorded)
	require.False(t, tracker.recordRetryExhausted(context.Background(), gatewayService, secondAccount, capacityErr))
}

func TestOpenAICapacityShedRequestTrackerIgnoresNonCapacityFailures(t *testing.T) {
	tracker := &openAICapacityShedRequestTracker{}
	gatewayService := &service.OpenAIGatewayService{}
	account := &service.Account{ID: 1, Platform: service.PlatformOpenAI}

	require.False(t, tracker.recordRetryExhausted(context.Background(), gatewayService, account, &service.UpstreamFailoverError{}))
	require.False(t, tracker.recorded)
}

func TestOpenAICapacityShedRequestTrackerResetsOnlyAtTurnBoundary(t *testing.T) {
	tracker := &openAICapacityShedRequestTracker{}
	gatewayService := &service.OpenAIGatewayService{}
	capacityErr := &service.UpstreamFailoverError{RequestScopedTransient: true}
	firstAccount := &service.Account{ID: 1, Platform: service.PlatformOpenAI}
	secondAccount := &service.Account{ID: 2, Platform: service.PlatformOpenAI}

	require.True(t, tracker.recordRetryExhausted(context.Background(), gatewayService, firstAccount, capacityErr))
	require.False(t, tracker.recordRetryExhausted(context.Background(), gatewayService, secondAccount, capacityErr), "the same failed WS turn must not sample a second account")

	tracker.resetAfterSuccessfulTurn()
	require.True(t, tracker.recordRetryExhausted(context.Background(), gatewayService, secondAccount, capacityErr), "a later successful turn opens a new sampling window")
}
