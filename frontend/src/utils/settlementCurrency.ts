import type { PublicSettings } from '@/types'

export type SettlementCurrency = 'USD' | 'CNY'

export interface PricingCurrencyDisplaySettings {
  settlementCurrency: SettlementCurrency
  usdToCNYRate: number
}

export const DEFAULT_USD_TO_CNY_RATE = 6.75

export function normalizeSettlementCurrency(value: unknown): SettlementCurrency {
  return typeof value === 'string' && value.trim().toUpperCase() === 'CNY'
    ? 'CNY'
    : 'USD'
}

export function normalizeUSDToCNYRate(value: unknown): number {
  const rate = Number(value)
  return Number.isFinite(rate) && rate > 0 ? rate : DEFAULT_USD_TO_CNY_RATE
}

/**
 * Reads the public settlement configuration while keeping old injected public
 * settings compatible. Wallet and quota values are already stored in this
 * unit; callers must only format them, never convert them in the browser.
 */
export function pricingCurrencyFromPublicSettings(
  settings?: Pick<PublicSettings, 'pricing_currency'> | null,
): PricingCurrencyDisplaySettings {
  return {
    settlementCurrency: normalizeSettlementCurrency(settings?.pricing_currency?.settlement_currency),
    usdToCNYRate: normalizeUSDToCNYRate(settings?.pricing_currency?.usd_to_cny_rate),
  }
}

export function settlementCurrencySymbol(currency: SettlementCurrency): '$' | '¥' {
  return currency === 'CNY' ? '¥' : '$'
}

/** Formats an amount that is already denominated in the active wallet unit. */
export function formatSettlementAmount(
  value: number | null | undefined,
  currency: SettlementCurrency,
  fractionDigits = 2,
): string {
  const numeric = Number(value)
  const safeValue = Number.isFinite(numeric) ? numeric : 0
  return `${settlementCurrencySymbol(currency)}${safeValue.toFixed(fractionDigits)}`
}

/**
 * Converts a display-only model price from its card currency to the public
 * settlement currency. Missing or invalid legacy card currencies are USD.
 * It must not be used for balances, quotas, payments, or historical usage
 * values.
 */
export function convertPriceCurrency(
  value: number | null | undefined,
  sourceCurrency: unknown,
  settings: PricingCurrencyDisplaySettings,
): number | null | undefined {
  if (value == null || !Number.isFinite(value)) return value
  const source = normalizeSettlementCurrency(sourceCurrency)
  if (source === settings.settlementCurrency) return value
  return source === 'USD' ? value * settings.usdToCNYRate : value / settings.usdToCNYRate
}

/** Resolver/catalog prices without a card currency are legacy USD values. */
export function convertUSDPriceForSettlement(
  value: number | null | undefined,
  settings: PricingCurrencyDisplaySettings,
): number | null | undefined {
  return convertPriceCurrency(value, 'USD', settings)
}
