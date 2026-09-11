import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PricingCurrencySettingsCard from '../PricingCurrencySettingsCard.vue'

const { getPricingCurrencySettings, updatePricingCurrencySettings, fetchPublicSettings, showError, showSuccess } = vi.hoisted(() => ({
  getPricingCurrencySettings: vi.fn(),
  updatePricingCurrencySettings: vi.fn(),
  fetchPublicSettings: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api', () => ({
  adminAPI: {
    settings: {
      getPricingCurrencySettings,
      updatePricingCurrencySettings,
    },
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ fetchPublicSettings, showError, showSuccess }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('PricingCurrencySettingsCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getPricingCurrencySettings.mockResolvedValue({ settlement_currency: 'CNY', usd_to_cny_rate: 6.75 })
    updatePricingCurrencySettings.mockResolvedValue({ settlement_currency: 'CNY', usd_to_cny_rate: 7.18 })
  })

  it('shows the settlement currency as read-only and saves a valid same-currency rate', async () => {
    const wrapper = mount(PricingCurrencySettingsCard)
    await flushPromises()

    expect(wrapper.get('[data-testid="pricing-currency-settlement-currency"]').text()).toContain('CNY')
    expect(wrapper.find('select').exists()).toBe(false)

    const rate = wrapper.get('[data-testid="pricing-currency-rate"]')
    await rate.setValue('7.18')
    await wrapper.get('[data-testid="pricing-currency-save"]').trigger('click')
    await flushPromises()

    expect(updatePricingCurrencySettings).toHaveBeenCalledWith({
      settlement_currency: 'CNY',
      usd_to_cny_rate: 7.18,
    })
    expect(fetchPublicSettings).toHaveBeenCalledWith(true)
    expect(showSuccess).toHaveBeenCalledWith('admin.settings.pricingCurrency.saved')
  })

  it('keeps the save action disabled for an invalid migration rate', async () => {
    const wrapper = mount(PricingCurrencySettingsCard)
    await flushPromises()

    const rate = wrapper.get('[data-testid="pricing-currency-rate"]')
    await rate.setValue('0')
    expect((wrapper.get('[data-testid="pricing-currency-save"]').element as HTMLButtonElement).disabled).toBe(true)
    expect(updatePricingCurrencySettings).not.toHaveBeenCalled()
  })
})
