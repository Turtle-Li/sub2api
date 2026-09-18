package service

import (
	"context"
	"math/big"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

// Parse before arithmetic; no binary floating point or truncating oversized
// decimal.IntPart conversion is allowed at the administrator money boundary.
func parseSelectedRefundAmount(raw string) (int64, error) {
	if len(raw) > 64 || strings.TrimSpace(raw) == "" {
		return 0, infraerrors.BadRequest("INVALID_AMOUNT", "refund amount must be a positive amount with at most two decimal places")
	}
	d, err := decimal.NewFromString(raw)
	if err != nil || d.Exponent() < -2 || d.Exponent() > 18 {
		return 0, infraerrors.BadRequest("INVALID_AMOUNT", "refund amount must have at most two decimal places")
	}
	minor := d.Shift(2)
	if !minor.IsPositive() || minor.GreaterThan(decimal.NewFromInt(1<<63-1)) {
		return 0, infraerrors.BadRequest("INVALID_AMOUNT", "refund amount is outside the supported range")
	}
	return minor.IntPart(), nil
}

func refundRatio(a, b, d int64, ceil bool) int64 {
	n := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	if ceil {
		n.Add(n, big.NewInt(d-1))
	}
	n.Quo(n, big.NewInt(d))
	return n.Int64()
}

// Invert cumulative cash proration, then derive cumulative product recovery.
// The full quote keeps the exact tail boundary and absorbs rounding remnants.
func selectSubscriptionRefundQuote(in subscriptionRefundInputs, full subscriptionRefundQuote, cash int64) (subscriptionRefundQuote, int64, error) {
	p := in.PayAmount.Shift(2).Round(0).IntPart()
	a := in.OrderAmount.Shift(2).Round(0).IntPart()
	total := int64(in.OriginalEnd.Sub(in.TermStart) / time.Second)
	settled := in.SettledProductAmount.Shift(2).Round(0).IntPart()
	if p <= 0 || a <= 0 || total <= 0 || full.CashMinor <= 0 || in.RefundedSeconds < 0 || in.RefundedSeconds >= total || settled < 0 || settled >= a || in.RefundedCashMinor < 0 || in.RefundedCashMinor >= p {
		return subscriptionRefundQuote{}, 0, infraerrors.BadRequest("INVALID_REFUND_STATE", "subscription refund accounting is invalid")
	}
	nmin := refundRatio(settled+1, total, a, true)
	if nmin < in.RefundedSeconds+1 {
		nmin = in.RefundedSeconds + 1
	}
	minimum := refundRatio(nmin-1, p, total, false) + 1 - in.RefundedCashMinor
	if minimum < 1 {
		minimum = 1
	}
	if minimum > full.CashMinor {
		minimum = full.CashMinor
	}
	if cash < minimum {
		return subscriptionRefundQuote{}, minimum, infraerrors.BadRequest("REFUND_AMOUNT_TOO_SMALL", "refund amount must be at least "+decimal.NewFromInt(minimum).Shift(-2).StringFixed(2))
	}
	if cash > full.CashMinor || cash > p-in.RefundedCashMinor {
		return subscriptionRefundQuote{}, minimum, infraerrors.BadRequest("REFUND_AMOUNT_EXCEEDED", "refund amount exceeds the server-calculated maximum")
	}
	if cash == full.CashMinor {
		return full, minimum, nil
	}
	cumulative := refundRatio(in.RefundedCashMinor+cash, total, p, true)
	// Whole-second entitlements cannot represent every fen on short, expensive
	// terms. Never reclaim a more valuable interval than the selected cash.
	if refundRatio(p, cumulative, total, false)-in.RefundedCashMinor != cash {
		return subscriptionRefundQuote{}, minimum, infraerrors.BadRequest("REFUND_AMOUNT_UNREPRESENTABLE", "refund amount cannot be represented by whole subscription seconds; select another amount or the full maximum")
	}
	seconds := cumulative - in.RefundedSeconds
	product := refundRatio(a, cumulative, total, false) - settled
	if seconds <= 0 || seconds > full.Seconds || product <= 0 {
		return subscriptionRefundQuote{}, minimum, infraerrors.BadRequest("INVALID_AMOUNT", "refund amount cannot reclaim a positive subscription entitlement")
	}
	return subscriptionRefundQuote{Seconds: seconds, ProductAmount: decimal.NewFromInt(product).Shift(-2), CashMinor: cash, NewExpiry: in.CurrentEnd.Add(-time.Duration(seconds) * time.Second)}, minimum, nil
}

func (s *PaymentService) ReviewRefundWithAmount(ctx context.Context, orderID int64, amount *string) (*RefundReview, error) {
	var selected *int64
	if amount != nil {
		v, err := parseSelectedRefundAmount(*amount)
		if err != nil {
			return nil, err
		}
		selected = &v
	}
	review, err := s.reviewRefundWithClient(ctx, s.entClient, orderID, s.refundValuationTime(), false)
	if err != nil {
		return nil, err
	}
	if err := selectRefundReviewAmount(review, selected); err != nil {
		return nil, err
	}
	return review, nil
}

func selectRefundReviewAmount(review *RefundReview, selected *int64) error {
	if !review.CanRefund || review.RequiresManualReview {
		return nil
	}
	if review.Subscription == nil {
		if selected != nil && *selected != decimal.NewFromFloat(review.DefaultRefundAmount).Shift(2).Round(0).IntPart() {
			return infraerrors.BadRequest("REFUND_AMOUNT_UNSUPPORTED", "balance refunds must use the complete server quote")
		}
	} else {
		if review.subscriptionInputs == nil {
			return infraerrors.BadRequest("INVALID_REFUND_STATE", "subscription refund inputs are missing")
		}
		full := calculateSubscriptionRefundQuote(*review.subscriptionInputs)
		cash := full.CashMinor
		if selected != nil {
			cash = *selected
		}
		quote, minimum, err := selectSubscriptionRefundQuote(*review.subscriptionInputs, full, cash)
		if err != nil {
			return err
		}
		review.MinRefundAmount = float64(minimum) / 100
		review.DefaultRefundAmount = float64(quote.CashMinor) / 100
		review.EntitlementAmount = quote.ProductAmount.InexactFloat64()
		review.Subscription.SecondsToReclaim = quote.Seconds
		review.Subscription.NewExpiresAt = quote.NewExpiry
	}
	review.QuoteRevision = selectedRefundRevision(review.QuoteRevision, selected)
	return nil
}

func selectedRefundRevision(base string, selected *int64) string {
	if selected == nil {
		return base
	}
	return refundReviewRevision(base, "selected_cash_minor", strconv.FormatInt(*selected, 10))
}
