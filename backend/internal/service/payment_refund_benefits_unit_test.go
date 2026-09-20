//go:build unit

package service

import (
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestPaymentRefundBenefitAutomaticExtrasAcceptsProvenBalanceGiftAndConcurrency(t *testing.T) {
	order := &dbent.PaymentOrder{
		OrderType: payment.OrderTypeBalance,
		Amount:    100,
		PayAmount: 10,
		ProductSnapshot: map[string]any{
			"schema_version":     2,
			"kind":               paymentSnapshotKindBalance,
			"credited_amount":    100.0,
			"paid_credit_amount": 10.0,
			"gift_credit_amount": 90.0,
			"entitlements": map[string]any{
				"balance_bonus": 90.0,
				"concurrency":   5,
			},
		},
	}
	require.True(t, paymentRefundBenefitHasOnlyAutomaticExtras(order))

	malformed := *order
	malformed.ProductSnapshot = map[string]any{
		"schema_version":     2,
		"kind":               paymentSnapshotKindBalance,
		"credited_amount":    100.0,
		"paid_credit_amount": 20.0,
		"gift_credit_amount": 80.0,
		"entitlements": map[string]any{
			"balance_bonus": 90.0,
			"concurrency":   5,
		},
	}
	require.False(t, paymentRefundBenefitHasOnlyAutomaticExtras(&malformed))

	subscription := *order
	subscription.OrderType = payment.OrderTypeSubscription
	require.False(t, paymentRefundBenefitHasOnlyAutomaticExtras(&subscription))
}

func TestPaymentRefundBenefitAuditKeepsPresentZeroConcurrency(t *testing.T) {
	source := &paymentRefundBenefitSource{
		ID:                    41,
		PaymentOrderID:        52,
		SourceOrigin:          "fulfillment",
		State:                 refundBenefitStateReserved,
		ConcurrencyBefore:     intPtrForRefundBenefitTest(2),
		ConcurrencyTarget:     5,
		ConcurrencyAfterGrant: intPtrForRefundBenefitTest(5),
	}
	before, after := 0, 0
	detail := paymentRefundBenefitSourceAuditDetailWithPresence(source, nil, &before, &after)
	require.Equal(t, 0, detail["concurrency_current"])
	require.Equal(t, 0, detail["concurrency_after"])
}

func TestReplayPaymentRefundConcurrencyTreatsUnattributedAsBarrier(t *testing.T) {
	sourceBeforeBarrier := int64(11)
	sourceAfterBarrier := int64(12)
	otherSourceAfterBarrier := int64(13)
	events := []paymentRefundConcurrencyEvent{
		{Sequence: 1, Kind: refundBenefitConcurrencyEventPaymentMax, Target: intPtrForRefundBenefitTest(6), SourceID: sourceBeforeBarrier, SourceState: refundBenefitStateActive, Before: 5, After: 6},
		{Sequence: 2, Kind: refundBenefitConcurrencyEventUnattributed, Target: intPtrForRefundBenefitTest(7), Before: 6, After: 7},
		{Sequence: 3, Kind: refundBenefitConcurrencyEventPaymentMax, Target: intPtrForRefundBenefitTest(9), SourceID: sourceAfterBarrier, SourceState: refundBenefitStateActive, Before: 7, After: 9},
		{Sequence: 4, Kind: refundBenefitConcurrencyEventPaymentMax, Target: intPtrForRefundBenefitTest(11), SourceID: otherSourceAfterBarrier, SourceState: refundBenefitStateActive, Before: 9, After: 11},
		{Sequence: 5, Kind: refundBenefitConcurrencyEventDelta, RequestedDelta: intPtrForRefundBenefitTest(1), Before: 11, After: 12},
	}

	_, err := replayPaymentRefundConcurrencyAfterSourceRemoval(5, events, sourceBeforeBarrier, 6)
	require.ErrorIs(t, err, errRefundBenefitProvenanceMissing)

	remaining, err := replayPaymentRefundConcurrencyAfterSourceRemoval(5, events, sourceAfterBarrier, 9)
	require.NoError(t, err)
	require.Equal(t, 12, remaining, "other payment provenance after the barrier and the post-source delta remain after removal")
}

func intPtrForRefundBenefitTest(value int) *int {
	return &value
}
