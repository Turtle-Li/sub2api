import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SubscriptionsView from '../SubscriptionsView.vue'

const { settings, getMySubscriptions, push } = vi.hoisted(() => ({
  settings: { cachedPublicSettings: {} as Record<string, unknown> },
  getMySubscriptions: vi.fn(),
  push: vi.fn(),
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => settings }))
vi.mock('@/api/subscriptions', () => ({ default: { getMySubscriptions } }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push }) }))
vi.mock('vue-i18n', async (original) => ({
  ...(await original<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('subscription renewal entry', () => {
  beforeEach(() => {
    push.mockReset()
    getMySubscriptions.mockResolvedValue([{
      id: 1, group_id: 4, status: 'active', expires_at: null,
      group: { name: 'Plus', platform: 'openai', rate_multiplier: 1 },
    }])
  })

  it.each([
    [{ payment_enabled: false, payment_entry_enabled: true }, false],
    [{ payment_enabled: false }, false],
    [{ payment_enabled: true, payment_entry_enabled: false }, false],
    [{ payment_enabled: true }, true],
  ])('respects both switches while retaining subscription details (%j)', async (flags, visible) => {
    settings.cachedPublicSettings = flags
    const wrapper = mount(SubscriptionsView, {
      global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Icon: true, ConfirmDialog: true } },
    })
    await flushPromises()
    expect(wrapper.text()).toContain('Plus')
    const renew = wrapper.findAll('button').find((button) => button.text() === 'payment.renewNow')
    const resetCard = wrapper.findAll('button').find((button) => button.text() === 'payment.resetShop.quickEntry')
    expect(Boolean(renew)).toBe(visible)
    expect(Boolean(resetCard)).toBe(visible)
    expect(wrapper.text()).toContain('userSubscriptions.upgradeContactAdmin')
    if (renew) {
      await renew.trigger('click')
      expect(push).toHaveBeenCalledWith({ path: '/purchase', query: { tab: 'subscription', group: '4' } })
    }
    wrapper.unmount()
  })

  it('links only current OpenAI subscriptions to their exact reset-card offer', async () => {
    settings.cachedPublicSettings = { payment_enabled: true, payment_entry_enabled: true }
    getMySubscriptions.mockResolvedValue([
      {
        id: 1, group_id: 4, status: 'active', expires_at: '2099-01-01T00:00:00Z',
        group: { name: 'Plus', platform: 'openai', rate_multiplier: 1 },
      },
      {
        id: 2, group_id: 5, status: 'active', expires_at: '2000-01-01T00:00:00Z',
        group: { name: 'Expired Plus', platform: 'openai', rate_multiplier: 1 },
      },
      {
        id: 3, group_id: 6, status: 'active', expires_at: '2099-01-01T00:00:00Z',
        group: { name: 'Claude', platform: 'anthropic', rate_multiplier: 1 },
      },
    ])

    const wrapper = mount(SubscriptionsView, {
      global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Icon: true, ConfirmDialog: true } },
    })
    await flushPromises()

    const resetEntries = wrapper.findAll('button').filter((button) => button.text() === 'payment.resetShop.quickEntry')
    expect(resetEntries).toHaveLength(1)
    await resetEntries[0].trigger('click')
    expect(push).toHaveBeenCalledWith({
      path: '/purchase',
      query: { tab: 'subscription', purchase: 'reset_card', subscription_id: '1' },
    })
    wrapper.unmount()
  })
})
