import { describe, expect, it } from 'vitest'
import {
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
})
