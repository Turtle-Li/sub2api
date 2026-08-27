import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AmountInput from '../AmountInput.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      if (key === 'payment.rechargeTierName') return `${params?.amount} credit`
      if (key === 'payment.entitlements.concurrency') return `Concurrency raised to ${params?.count}`
      if (key === 'payment.plusFee') return `plus ${params?.rate}% fee`
      return key
    },
  }),
}))

describe('AmountInput', () => {
  const options = [
    {
      amount: 100,
      original_price: 120,
      label: 'Growth',
      description: 'For regular usage',
      balance_bonus: 8,
      concurrency: 5,
      estimated_rate_multiplier: 0.9,
      estimated_tokens: 12_000_000,
      sort_order: 1,
      enabled: true,
    },
    {
      amount: 20,
      label: 'Starter',
      sort_order: 2,
      enabled: true,
    },
  ]

  it('renders a subscription-shaped tier card with structured benefits', () => {
    const wrapper = mount(AmountInput, {
      props: { modelValue: 100, options },
    })

    expect(wrapper.findAll('article')).toHaveLength(2)
    expect(wrapper.find('article').classes()).toContain('payment-product-card')
    expect(wrapper.find('.payment-product-card__body').exists()).toBe(true)
    expect(wrapper.find('.payment-product-card__meta').exists()).toBe(true)
    expect(wrapper.find('button').classes()).toContain('payment-product-card__action')
    expect(wrapper.text()).toContain('Growth')
    expect(wrapper.text()).toContain('-17%')
    expect(wrapper.text()).toContain('×0.9')
    expect(wrapper.text()).toContain('≈ 12M')
    // Bonus balance is platform credit, so it carries no currency symbol.
    expect(wrapper.text()).toContain('+ 8 payment.entitlements.balanceBonus')
    expect(wrapper.text()).toContain('Concurrency raised to 5')
    expect(wrapper.find('input').exists()).toBe(false)
  })

  // Tier amounts are charged in the gateway currency. The card hardcoded "$"
  // while the order summary right below it used the gateway currency, so the
  // same money appeared twice with two different symbols.
  it('prices tiers in the gateway currency and flags the fee separately', () => {
    const cny = mount(AmountInput, { props: { modelValue: 100, options } })
    expect(cny.text()).toContain('¥100.00')
    expect(cny.text()).toContain('¥120.00')
    expect(cny.text()).not.toContain('$100')
    expect(cny.text()).not.toContain('fee')

    const usd = mount(AmountInput, {
      props: { modelValue: 100, options, currency: 'USD', feeRate: 8 },
    })
    expect(usd.text()).toContain('$100.00')
    // The tier price is the tier price; the fee is called out, not folded in.
    expect(usd.text()).toContain('plus 8% fee')
  })

  it('emits the selected fixed tier without accepting custom input', async () => {
    const wrapper = mount(AmountInput, {
      props: { modelValue: 100, options },
    })

    const starterCard = wrapper.findAll('article')[1]
    await starterCard.find('button').trigger('click')

    expect(wrapper.emitted('update:modelValue')).toEqual([[20]])
    expect(starterCard.find('button').attributes('aria-pressed')).toBe('false')
  })
})
