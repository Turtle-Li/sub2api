import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import CNProviderBalanceCell from '../CNProviderBalanceCell.vue'
import type { Account } from '@/types'

const { queryBalance } = vi.hoisted(() => ({
  queryBalance: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    cnProviders: { queryBalance }
  }
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

const deepseekAccount = (id: number, updatedAt?: string): Account => ({
  id,
  name: 'deepseek-official',
  platform: 'deepseek',
  type: 'apikey',
  credentials: { account_mode: 'payg' },
  extra: updatedAt ? { deepseek_balance_updated_at: updatedAt } : {}
} as Account)
const account = {
  id: 7,
  platform: 'kimi',
  type: 'apikey',
  credentials: { account_mode: 'payg' },
  extra: {
    kimi_balance: 12.5,
    kimi_balance_currency: 'CNY',
    kimi_balance_updated_at: new Date().toISOString()
  }
} as Account

describe('CNProviderBalanceCell', () => {
  beforeEach(() => {
    queryBalance.mockReset()
    queryBalance.mockResolvedValue({
      provider: 'deepseek',
      success: true,
      balance: 12.34,
      currency: 'CNY',
      balances: [{ currency: 'CNY', balance: 12.34 }],
      available: true,
      fetched_at: Math.floor(Date.now() / 1000),
      persisted: true
    })
  })

  it('refreshes a stale balance when the account enters the page', async () => {
    const wrapper = mount(CNProviderBalanceCell, { props: { account: deepseekAccount(101) } })
    await flushPromises()

    expect(queryBalance).toHaveBeenCalledTimes(1)
    expect(queryBalance).toHaveBeenCalledWith(101)
    expect(wrapper.text()).toContain('12.34')
  })

  it('uses a fresh persisted balance without probing on mount', async () => {
    mount(CNProviderBalanceCell, {
      props: { account: deepseekAccount(102, new Date().toISOString()) }
    })
    await flushPromises()

    expect(queryBalance).not.toHaveBeenCalled()
  })

  it('renders the persisted balance as static text with an explicit query action', async () => {
    const wrapper = mount(CNProviderBalanceCell, { props: { account } })
    await flushPromises()

    // Snapshot value renders without any probe.
    expect(queryBalance).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="cn-provider-balance-value"]').text()).toContain('CNY 12.50')

    // The control reads as an action; the i18n mock returns the key itself.
    const probeButton = wrapper.get('[data-test="cn-provider-balance-probe"]')
    expect(probeButton.text()).toBe('admin.accounts.cnProviders.probe')

    await probeButton.trigger('click')
    await flushPromises()
    expect(queryBalance).toHaveBeenCalledWith(account.id)
  })

  it('shows the low-balance badge from the snapshot marker', () => {
    const lowAccount = {
      ...account,
      extra: {
        kimi_balance: 0.4,
        kimi_balance_low: true,
        kimi_balance_updated_at: new Date().toISOString()
      }
    } as Account

    const wrapper = mount(CNProviderBalanceCell, { props: { account: lowAccount } })

    expect(wrapper.text()).toContain('admin.accounts.cnProviders.balanceLow')
  })

  it('keeps the snapshot balance visible when a query fails', async () => {
    queryBalance.mockResolvedValue({ success: false, error: 'HTTP 401' })
    const wrapper = mount(CNProviderBalanceCell, { props: { account } })

    await wrapper.get('[data-test="cn-provider-balance-probe"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('CNY 12.50')
    expect(wrapper.text()).toContain('HTTP 401')
  })
})
