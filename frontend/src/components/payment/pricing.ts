import { DEFAULT_PAYMENT_CURRENCY, normalizePaymentCurrency } from './currency'

/**
 * Pricing helpers that mirror the server exactly.
 *
 * Everything here has a Go counterpart, and the two must agree to the cent —
 * these values are shown next to a pay button, so any drift is a number the
 * user was quoted and then not charged. Each function names its counterpart.
 */

function round2(value: number): number {
  if (!Number.isFinite(value)) return 0
  return Math.round(value * 100) / 100
}

/**
 * Balance credited for a top-up, mirroring calculateRechargeCreditedAmount.
 *
 * The server rounds twice: once after applying the global multiplier
 * (calculateCreditedBalance) and again after adding the tier bonus. Collapsing
 * that into a single round drifts by a cent on some inputs.
 */
export function creditedBalanceAmount(amount: number, multiplier: number, bonus = 0): number {
  if (!Number.isFinite(amount) || amount <= 0) return 0
  const rate = Number.isFinite(multiplier) && multiplier > 0 ? multiplier : 1
  const credited = round2(amount * rate)
  if (!Number.isFinite(bonus) || bonus <= 0) return credited
  return round2(credited + bonus)
}

/**
 * Gateway charge base for a subscription, mirroring
 * calculateSubscriptionGatewayBaseAmount.
 *
 * The conversion is opt-in on both sides: the server only applies the rate when
 * the gateway currency is the default (CNY). Converting unconditionally quotes
 * a USD gateway in yuan and is off by the whole exchange rate.
 */
export function subscriptionGatewayAmount(price: number, usdToCnyRate: number, currency?: string | null): number {
  const value = Number.isFinite(price) ? price : 0
  const rate = Number.isFinite(usdToCnyRate) && usdToCnyRate > 0 ? usdToCnyRate : 0
  if (rate <= 0 || normalizePaymentCurrency(currency) !== DEFAULT_PAYMENT_CURRENCY) return value
  return round2(value * rate)
}

/**
 * Fee added on top of the gateway base, mirroring calculateCreateOrderPayAmount.
 * The server rounds the fee up, so a display that rounds to nearest would quote
 * less than the user is charged.
 */
export function paymentFeeAmount(baseAmount: number, feeRatePercent: number): number {
  if (!Number.isFinite(baseAmount) || baseAmount <= 0) return 0
  if (!Number.isFinite(feeRatePercent) || feeRatePercent <= 0) return 0
  return Math.ceil(((baseAmount * feeRatePercent) / 100) * 100) / 100
}

/** Total the gateway will actually charge: base plus fee. */
export function paymentTotalAmount(baseAmount: number, feeRatePercent: number): number {
  const base = Number.isFinite(baseAmount) && baseAmount > 0 ? baseAmount : 0
  const fee = paymentFeeAmount(base, feeRatePercent)
  return fee > 0 ? round2(base + fee) : base
}
