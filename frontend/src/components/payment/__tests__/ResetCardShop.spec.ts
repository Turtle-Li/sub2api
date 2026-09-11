import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ResetCardShop from '../ResetCardShop.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { getResetCardQuote, purchaseResetCard } from '@/api/subscriptions'
import type { UserSubscription } from '@/types'
import type { SubscriptionPlan } from '@/types/payment'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/subscriptions', () => ({ getResetCardQuote: vi.fn(), purchaseResetCard: vi.fn() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1 }, refreshUser: vi.fn().mockResolvedValue({}) }) }))
const sub = { id: 7, group_id: 4, status: 'active', expires_at: '2027-01-01', group: { name: 'Plus', platform: 'openai' } } as UserSubscription
const quote = { subscription_id: 7, group_id: 4, plan_id: 10, monthly_price: 120, price: 40, expires_at: '2027-01-01' }
const plans = [{ id: 10, group_id: 4, group_platform: 'openai', currency: 'CNY', price: 120, validity_unit: 'month', validity_days: 1 }] as SubscriptionPlan[]
const render = (subscriptions = [sub]) => mount(ResetCardShop, {
  props: { subscriptions, plans }, global: { stubs: { ConfirmDialog: true } },
})

beforeEach(() => { vi.clearAllMocks(); sessionStorage.clear(); vi.mocked(getResetCardQuote).mockResolvedValue(quote) })

describe('ResetCardShop', () => {
  it('does not offer Claude or groups without a matching sale plan', () => {
    const claude = { ...sub, group: { ...sub.group, platform: 'anthropic' } } as UserSubscription
    expect(render([claude]).find('button').exists()).toBe(false)
    expect(render([{ ...sub, group_id: 99 }]).find('button').exists()).toBe(false)
  })
  it('shows the configured price while obtaining an authoritative quote before purchase', async () => {
    const wrapper = mount(ResetCardShop, { props: { subscriptions: [sub], plans: [{ ...plans[0], price: 550, entitlements: { reset_card_purchase_price: 180 } } as SubscriptionPlan] }, global: { stubs: { ConfirmDialog: true } } })
    expect(wrapper.get('strong').text()).toBe('180')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(getResetCardQuote).toHaveBeenCalledWith(7)
    expect(purchaseResetCard).not.toHaveBeenCalled()
  })
  it('offers no purchase button without a subscription', () => {
    const wrapper = render([])
    expect(wrapper.text()).toContain('payment.resetShop.requiresSubscription')
    expect(wrapper.find('button').exists()).toBe(false)
    expect(getResetCardQuote).not.toHaveBeenCalled()
  })

  it('quotes before confirmation and reuses the key after an uncertain failure', async () => {
    vi.mocked(purchaseResetCard).mockRejectedValueOnce(new Error('network')).mockResolvedValueOnce({ purchase_id: 1, subscription_id: 7, price: 40, expires_at: quote.expires_at })
    const wrapper = render()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(purchaseResetCard).not.toHaveBeenCalled()
    wrapper.getComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    const firstKey = vi.mocked(purchaseResetCard).mock.calls[0][1]
    wrapper.getComponent(ConfirmDialog).vm.$emit('cancel')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    wrapper.getComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    expect(vi.mocked(purchaseResetCard).mock.calls[1]).toEqual([quote, firstKey])
    expect(getResetCardQuote).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('purchased')).toHaveLength(1)
  })

  it('preserves an uncertain debit across component remounts', async () => {
    vi.mocked(purchaseResetCard).mockRejectedValueOnce(new Error('network')).mockResolvedValueOnce({ purchase_id: 1, subscription_id: 7, price: 40, expires_at: quote.expires_at })
    const first = render()
    await first.get('button').trigger('click')
    await flushPromises()
    first.getComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    const firstKey = vi.mocked(purchaseResetCard).mock.calls[0][1]
    first.unmount()
    const retry = render()
    await retry.get('button').trigger('click')
    await flushPromises()
    retry.getComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    expect(vi.mocked(purchaseResetCard).mock.calls[1]).toEqual([quote, firstKey])
    expect(getResetCardQuote).toHaveBeenCalledTimes(1)
    expect(sessionStorage.getItem('reset-card-purchase:1:7')).toBeNull()
  })

  it('requests a fresh quote after the server reports a changed price', async () => {
    vi.mocked(purchaseResetCard).mockRejectedValueOnce({ code: 409, reason: 'RESET_CARD_QUOTE_CHANGED', message: 'Price changed' })
    const wrapper = render()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    wrapper.getComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    expect(wrapper.getComponent(ConfirmDialog).props('show')).toBe(false)
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(getResetCardQuote).toHaveBeenCalledTimes(2)
  })

  it('suppresses double confirmation while a debit is pending', async () => {
    let finish!: (value: { purchase_id: number; subscription_id: number; price: number; expires_at: string }) => void
    vi.mocked(purchaseResetCard).mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const wrapper = render()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    wrapper.getComponent(ConfirmDialog).vm.$emit('confirm')
    wrapper.getComponent(ConfirmDialog).vm.$emit('confirm')
    expect(purchaseResetCard).toHaveBeenCalledTimes(1)
    finish({ purchase_id: 1, subscription_id: 7, price: 40, expires_at: quote.expires_at })
    await flushPromises()
  })
})
