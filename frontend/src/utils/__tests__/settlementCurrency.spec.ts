import { describe, expect, it } from 'vitest'
import {
  convertPriceCurrency,
  convertUSDPriceForSettlement,
  formatSettlementAmount,
  pricingCurrencyFromPublicSettings,
} from '../settlementCurrency'

describe('settlementCurrency', () => {
  it('keeps legacy public settings on USD with the approved default rate', () => {
    expect(pricingCurrencyFromPublicSettings()).toEqual({
      settlementCurrency: 'USD',
      usdToCNYRate: 6.75,
    })
  })

  it('formats an already-migrated wallet amount without converting it again', () => {
    expect(formatSettlementAmount(67.5, 'CNY')).toBe('¥67.50')
  })

  it('converts resolver/catalog USD prices only for a CNY price display', () => {
    const settings = pricingCurrencyFromPublicSettings({
      pricing_currency: { settlement_currency: 'CNY', usd_to_cny_rate: 6.75 },
    })
    expect(convertUSDPriceForSettlement(10, settings)).toBe(67.5)
  })

  it('converts model cards from their authored currency and treats a missing currency as USD', () => {
    const cny = pricingCurrencyFromPublicSettings({
      pricing_currency: { settlement_currency: 'CNY', usd_to_cny_rate: 6.75 },
    })
    const usd = pricingCurrencyFromPublicSettings({
      pricing_currency: { settlement_currency: 'USD', usd_to_cny_rate: 6.75 },
    })

    expect(convertPriceCurrency(10, 'USD', cny)).toBe(67.5)
    expect(convertPriceCurrency(67.5, 'CNY', cny)).toBe(67.5)
    expect(convertPriceCurrency(67.5, 'CNY', usd)).toBe(10)
    expect(convertPriceCurrency(10, undefined, cny)).toBe(67.5)
  })
})
