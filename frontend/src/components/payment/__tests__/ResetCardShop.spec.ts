import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ResetCardShop from '../ResetCardShop.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import type { UserSubscription } from '@/types'
import type { SubscriptionPlan } from '@/types/payment'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/subscriptions', () => ({ getResetCardQuote: vi.fn() }))

import { getResetCardQuote } from '@/api/subscriptions'

const sub = { id: 7, group_id: 4, status: 'active', expires_at: '2027-01-01', group: { name: 'Plus', platform: 'openai' } } as UserSubscription
const quote = { subscription_id: 7, group_id: 4, plan_id: 10, monthly_price: 120, price: 40, expires_at: '2027-01-01' }
const plans = [{ id: 10, group_id: 4, group_platform: 'openai', currency: 'CNY', price: 120, validity_unit: 'month', validity_days: 1 }] as SubscriptionPlan[]

const render = (subscriptions = [sub], planOverrides: Record<string, unknown> = {}) => mount(ResetCardShop, {
  props: { subscriptions, plans: [{ ...plans[0], ...planOverrides }] },
  global: { stubs: { ConfirmDialog: true } },
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

  it('quotes before confirmation and emits a shared external checkout request', async () => {
    const wrapper = render()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(getResetCardQuote).toHaveBeenCalledWith(7)
    wrapper.getComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    expect(wrapper.emitted('checkout')).toEqual([[{ subscription: sub, quote }]])
  })

  it('does not start another reset-card checkout while its parent is submitting', async () => {
    const wrapper = mount(ResetCardShop, {
      props: { subscriptions: [sub], plans, disabled: true },
      global: { stubs: { ConfirmDialog: true } },
    })

    await wrapper.get('button').trigger('click')

    expect(getResetCardQuote).not.toHaveBeenCalled()
    expect(wrapper.emitted('checkout')).toBeUndefined()
  })

  it('offers no purchase button without an active subscription', () => {
    const wrapper = render([])
    expect(wrapper.text()).toContain('payment.resetShop.requiresSubscription')
    expect(wrapper.find('button').exists()).toBe(false)
    expect(getResetCardQuote).not.toHaveBeenCalled()
  })
})
