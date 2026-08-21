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

const account = (id: number, updatedAt?: string): Account => ({
  id,
  name: 'deepseek-official',
  platform: 'deepseek',
  type: 'apikey',
  credentials: { account_mode: 'payg' },
  extra: updatedAt ? { deepseek_balance_updated_at: updatedAt } : {}
} as Account)

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
    const wrapper = mount(CNProviderBalanceCell, { props: { account: account(101) } })
    await flushPromises()

    expect(queryBalance).toHaveBeenCalledTimes(1)
    expect(queryBalance).toHaveBeenCalledWith(101)
    expect(wrapper.text()).toContain('12.34')
  })

  it('uses a fresh persisted balance without probing on mount', async () => {
    mount(CNProviderBalanceCell, {
      props: { account: account(102, new Date().toISOString()) }
    })
    await flushPromises()

    expect(queryBalance).not.toHaveBeenCalled()
  })
})
