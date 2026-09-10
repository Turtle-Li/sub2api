package service

import (
	"math"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/shopspring/decimal"
)

const defaultBalanceRechargeMultiplier = 1.0

func normalizeBalanceRechargeMultiplier(multiplier float64) float64 {
	if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier <= 0 {
		return defaultBalanceRechargeMultiplier
	}
	return multiplier
}

// normalizeSubscriptionUSDToCNYRate 将非法值归一为 0（换算关闭）。
// 与余额倍率不同，0 是合法状态：表示订阅保持 price 直付的存量行为。
func normalizeSubscriptionUSDToCNYRate(rate float64) float64 {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
		return 0
	}
	return rate
}

func calculateCreditedBalance(paymentAmount, multiplier float64) float64 {
	return decimal.NewFromFloat(paymentAmount).
		Mul(decimal.NewFromFloat(normalizeBalanceRechargeMultiplier(multiplier))).
		Round(2).
		InexactFloat64()
}

func calculateGatewayRefundAmount(orderAmount, payAmount, refundAmount float64, currency string) float64 {
	if !isFinitePositiveRefundAmount(orderAmount) ||
		!isFinitePositiveRefundAmount(payAmount) ||
		!isFinitePositiveRefundAmount(refundAmount) {
		return 0
	}
	fractionDigits := int32(payment.CurrencyMaxFractionDigits(currency))
	// Payment amounts are stored as floats for historical reasons, but the
	// gateway operates on the currency's smallest accepted unit.  The broader
	// provider-notification tolerance (one fen for CNY) must not turn a valid
	// one-fen partial refund on a two-fen order into a full refund.
	if math.Abs(refundAmount-orderAmount) < paymentAmountZeroTolerance(currency) {
		return decimal.NewFromFloat(payAmount).Round(fractionDigits).InexactFloat64()
	}
	return decimal.NewFromFloat(payAmount).
		Mul(decimal.NewFromFloat(refundAmount)).
		Div(decimal.NewFromFloat(orderAmount)).
		Round(fractionDigits).
		InexactFloat64()
}

// calculateGatewayRefundDelta returns the channel amount for one partial
// attempt.  Computing the difference between the rounded cumulative targets
// prevents independent per-attempt rounding from ever exceeding the original
// paid amount.
func calculateGatewayRefundDelta(orderAmount, payAmount, settledAmount, attemptAmount float64, currency string) float64 {
	if !isFinitePositiveRefundAmount(orderAmount) ||
		!isFinitePositiveRefundAmount(payAmount) ||
		!isFiniteNonNegativeRefundAmount(settledAmount) ||
		!isFinitePositiveRefundAmount(attemptAmount) {
		return 0
	}
	if settledAmount >= orderAmount || attemptAmount <= paymentAmountZeroTolerance(currency) {
		return 0
	}
	previous := calculateGatewayRefundAmount(orderAmount, payAmount, settledAmount, currency)
	total := calculateGatewayRefundAmount(orderAmount, payAmount, settledAmount+attemptAmount, currency)
	delta := total - previous
	if !isFiniteNonNegativeRefundAmount(delta) || delta > payAmount {
		return 0
	}
	return delta
}

// Refund amounts historically use float columns. Keep all arithmetic helpers
// fail-closed when a corrupted row or an in-memory caller supplies NaN/Inf;
// allowing such a value to flow into a comparison can otherwise bypass the
// cumulative refund cap.
func isFinitePositiveRefundAmount(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func isFiniteNonNegativeRefundAmount(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
