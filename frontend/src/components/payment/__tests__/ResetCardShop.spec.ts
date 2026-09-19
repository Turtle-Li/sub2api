import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ResetCardShop from '../ResetCardShop.vue'
import type { UserSubscription } from '@/types'
import type { SubscriptionPlan } from '@/types/payment'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key === 'payment.errors.RESET_CARD_TIER_INSUFFICIENT'
      ? 'localized tier mismatch'
      : key,
  }),
}))
vi.mock('@/api/subscriptions', () => ({ getResetCardQuote: vi.fn() }))

import { getResetCardQuote } from '@/api/subscriptions'

const sub = { id: 7, group_id: 4, status: 'active', expires_at: '2027-01-01', group: { name: 'Plus', platform: 'openai' } } as UserSubscription
const quote = { subscription_id: 7, group_id: 4, plan_id: 10, monthly_price: 120, price: 40, expires_at: '2027-01-01' }
const plans = [{ id: 10, group_id: 4, group_platform: 'openai', currency: 'CNY', price: 120, validity_unit: 'month', validity_days: 1 }] as SubscriptionPlan[]

const render = (
  subscriptions = [sub],
  planOverrides: Record<string, unknown> = {},
  targetSubscriptionId: number | null = null,
) => mount(ResetCardShop, {
  props: { subscriptions, plans: [{ ...plans[0], ...planOverrides }], targetSubscriptionId },
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getResetCardQuote).mockResolvedValue(quote)
})

describe('ResetCardShop', () => {
  it('shows configured reset content and disables cards that fail eligibility', () => {
    const wrapper = render([sub], { entitlements: { reset_card_title: 'GPT refill', reset_card_description: 'One reset', reset_card_purchase_price: 40 }, reset_card_eligibility: { visible: true, can_purchase: false, reason: 'minimum_recharge', required_total_recharge: 1000, current_total_recharge: 100 } })
    expect(wrapper.text()).toContain('GPT refill')
    expect(wrapper.text()).toContain('One reset')
    expect(wrapper.get('button').attributes('disabled')).toBeDefined()
  })

  it('does not offer Claude or groups without a matching sale plan', () => {
    const claude = { ...sub, group: { ...sub.group, platform: 'anthropic' } } as UserSubscription
    expect(render([claude]).find('button').exists()).toBe(false)
    expect(render([{ ...sub, group_id: 99 }]).find('button').exists()).toBe(false)
  })

  it('localizes a tier mismatch returned after the storefront state changes', async () => {
    vi.mocked(getResetCardQuote).mockRejectedValueOnce({
      reason: 'RESET_CARD_TIER_INSUFFICIENT',
      message: 'available reset cards in this family are from a lower subscription tier',
    })
    const wrapper = render()

    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('localized tier mismatch')
    expect(wrapper.text()).not.toContain('available reset cards in this family')
  })

  it('quotes a selected card and emits its checkout selection', async () => {
    const wrapper = render()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(getResetCardQuote).toHaveBeenCalledWith(7)
    expect(wrapper.emitted('select')).toEqual([[{ subscription: sub, quote }]])
  })

  it('shows quantity, expiry, and one-card use controls for the selected quote', async () => {
    const wrapper = mount(ResetCardShop, {
      props: {
        subscriptions: [sub],
        plans,
        selectedSubscriptionId: sub.id,
        selectedQuote: quote,
        quantity: 2,
      },
    })

    expect(wrapper.get('[data-test="reset-card-options"]').text()).toContain('payment.resetShop.validity')
    expect((wrapper.get('[data-test="reset-card-quantity"]').element as HTMLInputElement).value).toBe('2')

    await wrapper.get('[data-test="reset-card-quantity"]').setValue('100')
    expect(wrapper.emitted('updateOptions')).toEqual([[{ quantity: 99, useOnPurchase: false }]])

    await wrapper.get('[data-test="reset-card-use-on-purchase"]').setValue(true)
    expect(wrapper.emitted('updateOptions')?.[1]).toEqual([{ quantity: 2, useOnPurchase: true }])
    expect(wrapper.text()).not.toContain('tierBinding')
  })

  it('keeps a newly entered quantity when the use-on-purchase checkbox changes immediately after it', async () => {
    const Harness = defineComponent({
      components: { ResetCardShop },
      setup() {
        const quantity = ref(1)
        const useOnPurchase = ref(false)
        const updateOptions = (next: { quantity: number; useOnPurchase: boolean }) => {
          quantity.value = next.quantity
          useOnPurchase.value = next.useOnPurchase
        }
        return { quantity, useOnPurchase, updateOptions }
      },
      template: `
        <ResetCardShop
          :subscriptions="subscriptions"
          :plans="plans"
          :selected-subscription-id="subscription.id"
          :selected-quote="quote"
          :quantity="quantity"
          :use-on-purchase="useOnPurchase"
          @update-options="updateOptions"
        />
      `,
      data: () => ({ subscriptions: [sub], plans, subscription: sub, quote }),
    })
    const wrapper = mount(Harness)
    const quantityInput = wrapper.get('[data-test="reset-card-quantity"]')

    ;(quantityInput.element as HTMLInputElement).value = '3'
    await quantityInput.trigger('input')
    await wrapper.get('[data-test="reset-card-use-on-purchase"]').setValue(true)

    expect((quantityInput.element as HTMLInputElement).value).toBe('3')
    expect((wrapper.vm as unknown as { quantity: number }).quantity).toBe(3)
    expect((wrapper.vm as unknown as { useOnPurchase: boolean }).useOnPurchase).toBe(true)
  })

  it('does not start another reset-card checkout while its parent is submitting', async () => {
    const wrapper = mount(ResetCardShop, {
      props: { subscriptions: [sub], plans, disabled: true },
    })

    await wrapper.get('button').trigger('click')

    expect(getResetCardQuote).not.toHaveBeenCalled()
    expect(wrapper.emitted('select')).toBeUndefined()
  })

  it('offers no purchase button without an active subscription', () => {
    const wrapper = render([])
    expect(wrapper.text()).toContain('payment.resetShop.requiresSubscription')
    expect(wrapper.find('button').exists()).toBe(false)
    expect(getResetCardQuote).not.toHaveBeenCalled()
  })

  it('highlights and focuses the deep-linked offer without quoting or opening checkout', () => {
    const wrapper = render([sub], {}, 7)
    const offer = wrapper.get('[data-reset-card-offer="7"]')

    expect(offer.attributes('aria-pressed')).toBe('false')
    expect(offer.classes()).toContain('border-primary-500')
    expect(getResetCardQuote).not.toHaveBeenCalled()

    const shop = wrapper.vm as unknown as { focusOffer: () => boolean }
    expect(shop.focusOffer()).toBe(true)
    expect(getResetCardQuote).not.toHaveBeenCalled()
    expect(wrapper.emitted('select')).toBeUndefined()
  })
})
