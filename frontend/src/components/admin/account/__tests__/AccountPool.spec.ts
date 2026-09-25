import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AccountPoolStrip from '../AccountPoolStrip.vue'
import AccountPoolSelect from '../AccountPoolSelect.vue'
import { poolStatusBuckets, poolStatusSegments } from '../accountPoolStats'
import type { AccountPool, AccountPoolStats } from '@/api/admin/accountPools'

const { listPools } = vi.hoisted(() => ({ listPools: vi.fn() }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accountPools: {
      list: listPools
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key
    })
  }
})

const stats = (overrides: Partial<AccountPoolStats> = {}): AccountPoolStats => ({
  total: 10,
  normal: 6,
  rate_limited: 2,
  temp_unschedulable: 0,
  unschedulable: 0,
  error: 2,
  inactive: 0,
  expired: 0,
  ...overrides
})

const pool = (overrides: Partial<AccountPool> = {}): AccountPool => ({
  id: 7,
  name: 'grok free batch',
  platform: 'grok',
  stats: stats(),
  usage: null,
  created_at: '2026-09-25T00:00:00Z',
  updated_at: '2026-09-25T00:00:00Z',
  ...overrides
})

describe('accountPoolStats', () => {
  it('keeps normal and non-empty buckets only for the status bar', () => {
    expect(poolStatusSegments(stats({ normal: 0 })).map(s => s.key)).toEqual(['normal', 'rate_limited', 'error'])
  })

  it('maps every bucket to the matching list status filter', () => {
    expect(poolStatusBuckets(stats()).map(s => [s.key, s.filter])).toEqual([
      ['normal', 'active'],
      ['rate_limited', 'rate_limited'],
      ['temp_unschedulable', 'temp_unschedulable'],
      ['unschedulable', 'unschedulable'],
      ['error', 'error'],
      ['inactive', 'inactive']
    ])
  })
})

describe('AccountPoolStrip', () => {
  const stubs = { Icon: true, PlatformIcon: true }

  it('renders pool cards with stats and emits open/create', async () => {
    const wrapper = mount(AccountPoolStrip, {
      props: {
        pools: [
          pool({
            usage: {
              window_hours: 24,
              requests: 1200,
              tokens: 0,
              cost: 1.5,
              grok_free: { accounts: 10, used_tokens: 50, limit_tokens: 100, near_limit: 1 },
              updated_at: '2026-09-25T00:00:00Z'
            }
          })
        ]
      },
      global: { stubs }
    })

    const card = wrapper.get('[data-testid="account-pool-card-7"]')
    expect(card.text()).toContain('grok free batch')
    expect(card.text()).toContain('admin.accounts.pools.stats.normal 6')
    expect(card.text()).toContain('admin.accounts.pools.stats.rateLimited 2')
    expect(card.text()).toContain('admin.accounts.pools.stats.error 2')
    expect(card.text()).toContain('(50%)')
    expect(card.text()).not.toContain('admin.accounts.pools.stats.inactive')

    await card.trigger('click')
    expect(wrapper.emitted('open')?.[0]?.[0]).toMatchObject({ id: 7 })

    await wrapper.get('[data-testid="account-pool-create"]').trigger('click')
    expect(wrapper.emitted('create')).toHaveLength(1)
  })

  it('does not divide by zero for empty pools', () => {
    const wrapper = mount(AccountPoolStrip, {
      props: { pools: [pool({ stats: stats({ total: 0, normal: 0, rate_limited: 0, error: 0 }) })] },
      global: { stubs }
    })
    expect(wrapper.html()).not.toContain('NaN')
  })
})

describe('AccountPoolSelect', () => {
  const SelectStub = {
    props: ['modelValue', 'options'],
    emits: ['update:modelValue'],
    template: `<select data-testid="pool-select" :value="modelValue" @change="$emit('update:modelValue', Number($event.target.value))">
      <option v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</option>
    </select>`
  }

  beforeEach(() => {
    listPools.mockReset()
  })

  it('stays hidden when the platform has no pools', async () => {
    listPools.mockResolvedValue([])
    const wrapper = mount(AccountPoolSelect, {
      props: { platform: 'grok', modelValue: null },
      global: { stubs: { Select: SelectStub } }
    })
    await flushPromises()
    expect(listPools).toHaveBeenCalledWith('grok')
    expect(wrapper.find('[data-testid="account-pool-select"]').exists()).toBe(false)
  })

  it('selects a pool and clears it when the platform changes', async () => {
    listPools.mockResolvedValueOnce([pool()]).mockResolvedValueOnce([])
    const wrapper = mount(AccountPoolSelect, {
      props: {
        platform: 'grok',
        modelValue: null,
        'onUpdate:modelValue': (value: number | null) => wrapper.setProps({ modelValue: value })
      },
      global: { stubs: { Select: SelectStub } }
    })
    await flushPromises()

    await wrapper.get('[data-testid="pool-select"]').setValue('7')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([7])

    await wrapper.setProps({ platform: 'openai' })
    await flushPromises()
    expect(listPools).toHaveBeenLastCalledWith('openai')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([null])
  })
})
