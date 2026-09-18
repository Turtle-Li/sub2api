//go:build unit

package service

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"strconv"
	"testing"
	"time"
)

func TestSubscriptionPartialCashQuote(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 123456000, time.UTC)
	in := subscriptionRefundInputs{OrderAmount: decimal.NewFromInt(100), PayAmount: decimal.NewFromInt(103), TermStart: start, OriginalEnd: start.Add(30 * 24 * time.Hour), CurrentEnd: start.Add(30 * 24 * time.Hour), ValuationAt: start}
	full := calculateSubscriptionRefundQuote(in)
	_, min, err := selectSubscriptionRefundQuote(in, full, 1)
	require.Error(t, err)
	require.Equal(t, int64(2), min)
	first, _, err := selectSubscriptionRefundQuote(in, full, 3)
	require.NoError(t, err)
	require.Equal(t, int64(3), first.CashMinor)
	require.Equal(t, 123456000, first.NewExpiry.Nanosecond())
	in.RefundedSeconds = first.Seconds
	in.RefundedCashMinor = 3
	in.SettledProductAmount = first.ProductAmount
	in.CurrentEnd = first.NewExpiry
	second, _, err := selectSubscriptionRefundQuote(in, calculateSubscriptionRefundQuote(in), 3)
	require.NoError(t, err)
	original := in
	original.RefundedSeconds = 0
	original.RefundedCashMinor = 0
	original.SettledProductAmount = decimal.Zero
	original.CurrentEnd = original.OriginalEnd
	combined, _, err := selectSubscriptionRefundQuote(original, full, 6)
	require.NoError(t, err)
	require.Equal(t, combined.Seconds, first.Seconds+second.Seconds)
	require.True(t, combined.ProductAmount.Equal(first.ProductAmount.Add(second.ProductAmount)))
	max := calculateSubscriptionRefundQuote(in)
	selected, _, err := selectSubscriptionRefundQuote(in, max, max.CashMinor)
	require.NoError(t, err)
	require.Equal(t, max, selected)
}
func TestReviewedSubscriptionSelectedAmountPersists(t *testing.T) {
	svc, order, _, _, _ := newReviewedSubscriptionRefundFixture(t)
	amount := "0.03"
	review, err := svc.ReviewRefundWithAmount(context.Background(), order.ID, &amount)
	require.NoError(t, err)
	require.Equal(t, 0.03, review.DefaultRefundAmount)
	require.Greater(t, review.MaxRefundAmount, review.DefaultRefundAmount)
	plan, err := svc.PrepareReviewedRefundRequestWithAmount(context.Background(), order.ID, review.QuoteRevision, RefundReasonInput{LegacyReason: "partial refund"}, &amount)
	require.NoError(t, err)
	attempt, err := svc.reserveUnifiedRefundAttempt(context.Background(), plan)
	require.NoError(t, err)
	require.Equal(t, int64(3), attempt.AmountFen)
	require.Equal(t, review.Subscription.SecondsToReclaim, attempt.SubscriptionSeconds)
	require.True(t, review.Subscription.NewExpiresAt.Equal(*attempt.ValuationAt))
}

func TestSelectedRefundAmountValidation(t *testing.T) {
	for _, raw := range []string{"", "0", "-1", "0.001", "NaN", "Inf", "99999999999999999999", "1e99999999"} {
		t.Run(raw, func(t *testing.T) { _, err := parseSelectedRefundAmount(raw); require.Error(t, err) })
	}
	for _, raw := range []string{"0.01", "1.20", "100"} {
		_, err := parseSelectedRefundAmount(raw)
		require.NoError(t, err)
	}
}

func TestSelectedSubscriptionQuoteRevisionAndRecheck(t *testing.T) {
	ctx := context.Background()
	svc, order, _, _, _ := newReviewedSubscriptionRefundFixture(t)
	amount := "0.03"
	other := "0.04"
	review, err := svc.ReviewRefundWithAmount(ctx, order.ID, &amount)
	require.NoError(t, err)
	_, err = svc.PrepareReviewedRefundRequestWithAmount(ctx, order.ID, review.QuoteRevision, RefundReasonInput{LegacyReason: "test"}, &other)
	require.Error(t, err)
	plan, err := svc.PrepareReviewedRefundRequestWithAmount(ctx, order.ID, review.QuoteRevision, RefundReasonInput{LegacyReason: "test"}, &amount)
	require.NoError(t, err)
	*plan.RequestedCashMinor = 4
	_, err = svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.Error(t, err)
	grant, _, err := loadPaymentSubscriptionRefundState(ctx, svc.entClient, order.ID, false)
	require.NoError(t, err)
	require.Zero(t, grant.ReservedSeconds)
	*plan.RequestedCashMinor = 3
	before := svc.refundReviewNow()
	svc.refundReviewNow = func() time.Time { return before.Add(time.Minute) }
	_, err = svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.Error(t, err)
}

func TestSelectedSubscriptionTerminalLifecycle(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(strconv.FormatBool(success), func(t *testing.T) {
			ctx := context.Background()
			svc, order, sub, _, end := newReviewedSubscriptionRefundFixture(t)
			amount := "0.03"
			for i := 0; i < 2; i++ {
				review, err := svc.ReviewRefundWithAmount(ctx, order.ID, &amount)
				require.NoError(t, err)
				plan, err := svc.PrepareReviewedRefundRequestWithAmount(ctx, order.ID, review.QuoteRevision, RefundReasonInput{LegacyReason: "partial lifecycle"}, &amount)
				require.NoError(t, err)
				attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
				require.NoError(t, err)
				status := unifiedpay.RefundStatusFailed
				if success {
					status = unifiedpay.RefundStatusSucceeded
				}
				resource := unifiedRefundFixtureResource(attempt, status)
				resource.RefundRequestID = "cccccccc-cccc-4ccc-8ccc-" + fmt.Sprintf("%012d", i+1)
				resource.ChannelOutRefundNo = attempt.ProductRefundNo
				result, err := svc.applyUnifiedRefundResource(ctx, order.ID, resource, "test")
				require.NoError(t, err)
				require.Equal(t, success, result.Success)
				_, err = svc.applyUnifiedRefundResource(ctx, order.ID, resource, "duplicate")
				require.NoError(t, err)
				grant, _, err := loadPaymentSubscriptionRefundState(ctx, svc.entClient, order.ID, false)
				require.NoError(t, err)
				require.Zero(t, grant.ReservedSeconds)
				require.Zero(t, grant.ReservedCashMinor)
				current, err := svc.entClient.UserSubscription.Get(ctx, sub.ID)
				require.NoError(t, err)
				if success {
					require.Equal(t, int64((i+1)*3), grant.RefundedCashMinor)
					require.True(t, current.ExpiresAt.Equal(review.Subscription.NewExpiresAt))
				} else {
					require.True(t, current.ExpiresAt.Equal(end))
					require.Zero(t, grant.RefundedCashMinor)
				}
			}
		})
	}
}

func TestSubscriptionPartialMaximumAndBoundaries(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 41622000, time.UTC)
	for _, price := range []int64{1, 100, 103} {
		in := subscriptionRefundInputs{OrderAmount: decimal.NewFromInt(price).Shift(-2), PayAmount: decimal.NewFromInt(price).Shift(-2), TermStart: start, OriginalEnd: start.Add(30 * 24 * time.Hour), CurrentEnd: start.Add(30 * 24 * time.Hour), ValuationAt: start.Add(-time.Hour)}
		full := calculateSubscriptionRefundQuote(in)
		chosen, minimum, err := selectSubscriptionRefundQuote(in, full, full.CashMinor)
		require.NoError(t, err)
		require.Equal(t, full, chosen)
		require.Equal(t, int64(1), minimum)
		require.True(t, chosen.NewExpiry.Equal(start))
		_, _, err = selectSubscriptionRefundQuote(in, full, full.CashMinor+1)
		require.Error(t, err)
	}
	ctx := context.Background()
	svc, order, _, _, _ := newReviewedSubscriptionRefundFixture(t)
	defaultReview, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	explicit := strconv.FormatFloat(defaultReview.MaxRefundAmount, 'f', 2, 64)
	selected, err := svc.ReviewRefundWithAmount(ctx, order.ID, &explicit)
	require.NoError(t, err)
	require.NotEqual(t, defaultReview.QuoteRevision, selected.QuoteRevision)
	require.Equal(t, defaultReview.Subscription, selected.Subscription)
	require.Equal(t, defaultReview.EntitlementAmount, selected.EntitlementAmount)
	legacyPlan, err := svc.PrepareReviewedRefund(ctx, order.ID, defaultReview.QuoteRevision, "legacy")
	require.NoError(t, err)
	require.Nil(t, legacyPlan.RequestedCashMinor)
}

func TestBalanceSelectedPartialIsExplicitlyRejected(t *testing.T) {
	svc, order := newReviewedBalanceRefundFixture(t, 120, 100)
	amount := "0.03"
	_, err := svc.ReviewRefundWithAmount(context.Background(), order.ID, &amount)
	require.Error(t, err)
}
