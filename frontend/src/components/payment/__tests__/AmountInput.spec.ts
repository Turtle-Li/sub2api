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
  it('keeps a gated tier visible with its condition but rejects mouse and keyboard selection', async () => {
    const wrapper = mount(AmountInput, { props: { modelValue: null, options: [{ amount: 599, enabled: true, sort_order: 0, eligibility: { can_purchase: false, reason: 'minimum_recharge', required_total_recharge: 1000, current_total_recharge: 49 } }] } })
    const card = wrapper.get('article')
    expect(card.attributes('aria-disabled')).toBe('true')
    expect(wrapper.text()).toContain('payment.eligibility.minimum')
    await card.trigger('click')
    await card.trigger('keydown.enter')
    await card.trigger('keydown.space')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

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
    expect(wrapper.find('button').exists()).toBe(false)
    expect(wrapper.text()).toContain('Growth')
    expect(wrapper.text()).toContain('For regular usage')
    expect(wrapper.text()).toContain('-17%')
    expect(wrapper.text()).toContain('×0.9')
    expect(wrapper.text()).toContain('≈ 12M')
    // Bonus balance is platform credit, so it carries no currency symbol; the
    // "Includes bonus" label above it names it, the line itself stays icon +N.
    expect(wrapper.find('.payment-recharge-card__bonus').text()).toContain('+8')
    expect(wrapper.find('.payment-recharge-card__bonus').text()).not.toContain('payment.rechargeBonusShort')
    expect(wrapper.findAll('.payment-product-card__list-item').map(item => item.text())).not.toContain('payment.entitlements.balanceBonus +8 payment.creditUnit')
    expect(wrapper.text()).toContain('Concurrency raised to 5')
    expect(wrapper.find('input').exists()).toBe(false)
  })

  it('exposes the full tier content to assistive technology instead of overriding it with only price and title', () => {
    const wrapper = mount(AmountInput, { props: { modelValue: 100, options } })
    const card = wrapper.get('[role="button"]')
    // A button gets its accessible name from its contents. An aria-label here
    // would replace the description, credited balance, bonus and benefits.
    expect(card.attributes('aria-label')).toBeUndefined()
    expect(card.text()).toContain('Growth')
    expect(card.text()).toContain('For regular usage')
    expect(card.text()).toContain('¥100.00')
    expect(card.text()).toContain('108 payment.creditUnit')
    expect(card.text()).toContain('+8 payment.creditUnit')
    expect(card.text()).toContain('Concurrency raised to 5')
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
    await starterCard.trigger('keydown', { key: 'Enter' })

    expect(wrapper.emitted('update:modelValue')).toEqual([[20]])
    expect(starterCard.attributes('aria-pressed')).toBe('false')
  })

  // The whole card is the control, so a click anywhere on it selects the tier.
  it('selects a tier from the card body', async () => {
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
    expect(premium.find('.payment-recharge-card__bonus').text()).toContain('+145')
    expect(premium.findAll('.payment-product-card__list-item')).toHaveLength(0)

    const standard = wrapper.findAll('article')[1]
    expect(standard.find('.payment-recharge-card__credit-value').text()).toBe('49 payment.creditUnit')
    expect(standard.find('.payment-recharge-card__bonus').exists()).toBe(false)
    expect(premium.get('.payment-recharge-card__credit-heading').find('.payment-recharge-card__bonus').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('payment.selectedRechargeTier')
    expect(wrapper.text()).not.toContain('payment.selectRechargeTier')
  })

  // Configurations can carry an auto-generated description that merely restates
  // the bonus already shown in the credited panel. Only that exact generated
  // sentence is dropped; custom copy — even copy that mentions a bonus — stays.
  it('drops only the exact auto-generated bonus sentence and keeps every other description', () => {
    const wrapper = mount(AmountInput, {
      props: {
        modelValue: 99,
        options: [
          { amount: 99, balance_bonus: 5, description: '额外赠送 5 额度', sort_order: 1, enabled: true },
          { amount: 199, balance_bonus: 20, description: '额外赠送 20 额度，限本月活动', sort_order: 2, enabled: true },
          { amount: 49, description: '按充值金额到账', sort_order: 3, enabled: true },
        ],
      },
    })

    const cards = wrapper.findAll('article')
    expect(cards[0].text()).toContain('+5')
    expect(cards[0].text()).not.toContain('额外赠送')
    expect(cards[1].text()).toContain('额外赠送 20 额度，限本月活动')
    expect(cards[2].text()).toContain('按充值金额到账')
  })

  // A locked tier cannot be bought, so it never carries the recommendation
  // ribbon even when an admin marked it. Its priority is kept, though: the
  // discount fallback must not crown another tier in its place.
  it('never features a tier whose purchase is gated', () => {
    const wrapper = mount(AmountInput, {
      props: {
        modelValue: null,
        options: [
          { amount: 599, recommended: true, sort_order: 1, enabled: true, eligibility: { can_purchase: false, reason: 'minimum_recharge', required_total_recharge: 1000, current_total_recharge: 49 } },
          { amount: 99, sort_order: 2, enabled: true },
          { amount: 199, original_price: 299, sort_order: 3, enabled: true },
        ],
      },
    })

    expect(wrapper.findAll('.payment-product-card__ribbon')).toHaveLength(0)
    const cards = wrapper.findAll('article')
    expect(cards[0].classes()).not.toContain('payment-product-card--featured')
    expect(cards[2].classes()).not.toContain('payment-product-card--featured')
  })

  // A tier selected before it became gated drops the selected appearance —
  // ring, check and aria-pressed — without touching the model value, and still
  // refuses mouse and keyboard selection.
  it('drops the selected state when the chosen tier becomes gated', async () => {
    const wrapper = mount(AmountInput, {
      props: {
        modelValue: 599,
        options: [
          { amount: 599, sort_order: 1, enabled: true, eligibility: { can_purchase: false, reason: 'minimum_recharge', required_total_recharge: 1000, current_total_recharge: 49 } },
        ],
      },
    })

    const card = wrapper.get('article')
    expect(card.attributes('aria-pressed')).toBe('false')
    expect(card.classes()).not.toContain('payment-product-card--selected')
    expect(card.find('.payment-product-card__check').exists()).toBe(false)
    await card.trigger('click')
    await card.trigger('keydown.enter')
    await card.trigger('keydown.space')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })
})
