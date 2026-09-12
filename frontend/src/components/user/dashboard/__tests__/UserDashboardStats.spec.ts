import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import UserDashboardStats from '../UserDashboardStats.vue'
import type { UserDashboardStats as UserDashboardStatsData } from '@/api/usage'

const appStore = vi.hoisted(() => ({
  cachedPublicSettings: null as { pricing_currency?: { settlement_currency: string; usd_to_cny_rate: number } } | null,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key === 'dashboard.mixedCurrencyUsage'
        ? 'Historical usage contains multiple currencies and is not totaled.'
        : key,
    }),
  }
})

function createStats(): UserDashboardStatsData {
  return {
    total_api_keys: 2,
    active_api_keys: 2,
    total_requests: 42,
    total_input_tokens: 10,
    total_output_tokens: 20,
    total_cache_creation_tokens: 0,
    total_cache_read_tokens: 0,
    total_tokens: 30,
    total_cost: 123.4567,
    total_actual_cost: 98.7654,
    today_requests: 3,
    today_input_tokens: 1,
    today_output_tokens: 2,
    today_cache_creation_tokens: 0,
    today_cache_read_tokens: 0,
    today_tokens: 3,
    today_cost: 12.3456,
    today_actual_cost: 9.8765,
    average_duration_ms: 50,
    rpm: 1,
    tpm: 2,
    by_platform: [{
      platform: 'openai',
      total_requests: 42,
      total_tokens: 30,
      total_actual_cost: 98.7654,
      today_requests: 3,
      today_tokens: 3,
      today_actual_cost: 9.8765,
    }],
  }
}

describe('UserDashboardStats', () => {
  beforeEach(() => {
    appStore.cachedPublicSettings = null
  })

  it('restores reference usage numbers while keeping the actual wallet in CNY', () => {
    appStore.cachedPublicSettings = {
      pricing_currency: { settlement_currency: 'CNY', usd_to_cny_rate: 6.75 },
    }
    const wrapper = mount(UserDashboardStats, {
      props: {
        stats: createStats(),
        balance: 67.5,
        isSimple: false,
      },
      global: { stubs: { Icon: true } },
    })

    expect(wrapper.text()).not.toContain('Historical usage contains multiple currencies and is not totaled.')
    expect(wrapper.text()).toContain('¥67.50')
    expect(wrapper.text()).toContain('42')
    expect(wrapper.text()).toContain('30')
    expect(wrapper.text()).toContain('$123.4567')
    expect(wrapper.text()).toContain('$98.7654')
    expect(wrapper.text()).toContain('$9.8765')
  })
})
