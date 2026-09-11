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
    expect(wrapper.find('.payment-recharge-card__credit').exists()).toBe(true)
    expect(wrapper.find('button').classes()).toContain('payment-product-card__action')
    expect(wrapper.text()).toContain('Growth')
    expect(wrapper.text()).toContain('-17%')
    expect(wrapper.text()).toContain('×0.9')
    expect(wrapper.text()).toContain('≈ 12M')
    // Bonus balance is platform credit, so it carries no currency symbol.
    expect(wrapper.find('.payment-recharge-card__bonus').text()).toContain('payment.rechargeBonus')
    expect(wrapper.find('.payment-recharge-card__bonus').text()).toContain('+8 payment.creditUnit')
    expect(wrapper.findAll('.payment-product-card__list-item').map(item => item.text())).not.toContain('payment.entitlements.balanceBonus +8 payment.creditUnit')
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
    expect(starterCard.attributes('aria-pressed')).toBe('false')
  })

  // The whole card is the control, so a click anywhere on it selects the tier.
  it('selects a tier from the card body as well as its action button', async () => {
    const wrapper = mount(AmountInput, { props: { modelValue: 100, options } })

    await wrapper.findAll('article')[1].trigger('click')

    expect(wrapper.emitted('update:modelValue')).toEqual([[20]])
  })

  // Unconfigured estimates are omitted entirely. A row that says "not
  // configured" tells the reader information is missing and nothing else.
  it('omits estimate rows a tier has no data for', () => {
    const wrapper = mount(AmountInput, { props: { modelValue: 20, options } })
    const starter = wrapper.findAll('article')[1]

    expect(starter.text()).not.toContain('payment.notConfigured')
    expect(starter.text()).not.toContain('payment.rateEstimate')
    expect(starter.findAll('.payment-product-card__list-item')).toHaveLength(0)
  })

  // Highlight one tier at most, and only when a discount justifies the claim.
  it('recommends only the deepest-discounted tier', () => {
    const wrapper = mount(AmountInput, { props: { modelValue: 100, options } })
    const ribbons = wrapper.findAll('.payment-product-card__ribbon')

    expect(ribbons).toHaveLength(1)
    expect(wrapper.findAll('article')[0].classes()).toContain('payment-product-card--featured')

    const noDiscount = mount(AmountInput, {
      props: { modelValue: 20, options: [{ amount: 20, sort_order: 1, enabled: true }] },
    })
    expect(noDiscount.findAll('.payment-product-card__ribbon')).toHaveLength(0)
  })

  it('honors the explicit admin recommendation before discount fallback', () => {
    const wrapper = mount(AmountInput, {
      props: {
        modelValue: 20,
        options: options.map((option) => ({
          ...option,
          recommended: option.amount === 20,
        })),
      },
    })

    expect(wrapper.findAll('article')[0].find('.payment-product-card__ribbon').exists()).toBe(false)
    expect(wrapper.findAll('article')[1].find('.payment-product-card__ribbon').exists()).toBe(true)
  })

  // The card states what actually lands in the balance, which is the tier
  // amount through the global multiplier plus any configured bonus.
  it('shows the credited balance for each tier', () => {
    const wrapper = mount(AmountInput, {
      props: { modelValue: 100, options, balanceMultiplier: 2 },
    })

    expect(wrapper.findAll('.payment-recharge-card__credit-value')[0].text()).toBe('208 payment.creditUnit')
    expect(wrapper.findAll('.payment-recharge-card__credit-value')[1].text()).toBe('40 payment.creditUnit')
  })

  it('emphasizes the actual credited total once and omits an empty bonus', () => {
    const wrapper = mount(AmountInput, {
      props: {
        modelValue: 599,
        options: [
          { amount: 599, balance_bonus: 145, sort_order: 1, enabled: true },
          { amount: 49, balance_bonus: 0, sort_order: 2, enabled: true },
        ],
      },
    })

    const premium = wrapper.findAll('article')[0]
    expect(premium.find('.payment-recharge-card__credit-value').text()).toBe('744 payment.creditUnit')
    expect(premium.find('.payment-recharge-card__bonus').text()).toContain('+145 payment.creditUnit')
    expect(premium.findAll('.payment-product-card__list-item')).toHaveLength(0)

    const standard = wrapper.findAll('article')[1]
    expect(standard.find('.payment-recharge-card__credit-value').text()).toBe('49 payment.creditUnit')
    expect(standard.find('.payment-recharge-card__bonus').exists()).toBe(false)
  })
})
