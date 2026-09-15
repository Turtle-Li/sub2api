import { mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import UserDashboardStats from '../UserDashboardStats.vue'
import type { PlatformDashboardStats, UserDashboardStats as UserStatsType } from '@/api/usage'
import type { PlatformQuotaItem } from '@/types'

const appStore = vi.hoisted(() => ({
  cachedPublicSettings: null as { pricing_currency?: { settlement_currency: string; usd_to_cny_rate: number } } | null,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'dashboard.mixedCurrencyUsage') {
          return 'Historical usage contains multiple currencies and is not totaled.'
        }
        return params ? `${key}:${JSON.stringify(params)}` : key
      },
    }),
  }
})

function createStats(): UserStatsType {
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

beforeEach(() => {
  appStore.cachedPublicSettings = null
})

describe('UserDashboardStats currency display', () => {
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

function makeStats(over: Partial<UserStatsType> = {}): UserStatsType {
  return {
    total_api_keys: 1,
    active_api_keys: 1,
    total_requests: 0,
    total_input_tokens: 0,
    total_output_tokens: 0,
    total_cache_creation_tokens: 0,
    total_cache_read_tokens: 0,
    total_tokens: 0,
    total_cost: 0,
    total_actual_cost: 0,
    today_requests: 0,
    today_input_tokens: 0,
    today_output_tokens: 0,
    today_cache_creation_tokens: 0,
    today_cache_read_tokens: 0,
    today_tokens: 0,
    today_cost: 0,
    today_actual_cost: 0,
    average_duration_ms: 0,
    rpm: 0,
    tpm: 0,
    by_platform: [],
    ...over,
  }
}

function usage(platform: string, cost: number): PlatformDashboardStats {
  return {
    platform,
    total_requests: 1,
    total_tokens: 10,
    total_actual_cost: cost,
    today_requests: 1,
    today_tokens: 10,
    today_actual_cost: cost,
  }
}

function quota(over: Partial<PlatformQuotaItem> & { platform: string }): PlatformQuotaItem {
  return {
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    daily_usage_usd: 0,
    weekly_usage_usd: 0,
    monthly_usage_usd: 0,
    ...over,
  } as PlatformQuotaItem
}

function mountStats(stats: UserStatsType, platformQuotas: PlatformQuotaItem[] | null = null, isSimple = false) {
  return mount(UserDashboardStats, {
    props: { stats, balance: 0, isSimple, platformQuotas },
    global: { stubs: { Icon: true } },
  })
}

/** 渲染出的平台卡片，按 DOM 顺序返回 data-platform */
function cardPlatforms(wrapper: VueWrapper): string[] {
  return wrapper.findAll('[data-testid="platform-card"]').map((card) => card.attributes('data-platform') ?? '')
}

describe('UserDashboardStats 按平台拆分', () => {
  it('只有用量的平台才产生卡片；三档全空的限额记录不产生卡片', () => {
    const wrapper = mountStats(
      makeStats({ total_actual_cost: 0.03, today_actual_cost: 0.03, by_platform: [usage('grok', 0.03)] }),
      [
        quota({ platform: 'anthropic' }),
        quota({ platform: 'openai' }),
        quota({ platform: 'gemini' }),
        quota({ platform: 'grok' }),
      ]
    )
    expect(cardPlatforms(wrapper)).toEqual(['grok'])
    expect(wrapper.text()).toContain('dashboard.platformCount:{"count":1}')
    expect(wrapper.html()).not.toContain('dashboard.platformQuota.title')
  })

  it('配置了限额但没有用量的平台也产生卡片，并渲染配额区', () => {
    const wrapper = mountStats(
      makeStats({ total_actual_cost: 0.03, today_actual_cost: 0.03, by_platform: [usage('grok', 0.03)] }),
      [quota({ platform: 'openai', daily_limit_usd: 10, daily_usage_usd: 2.5 }), quota({ platform: 'anthropic' })]
    )
    expect(cardPlatforms(wrapper)).toEqual(['openai', 'grok'])
    expect(wrapper.text()).toContain('dashboard.platformQuota.title')
    expect(wrapper.text()).toContain('dashboard.platformCount:{"count":2}')
  })

  it('同一平台既有用量又有限额只产生一张卡片', () => {
    const wrapper = mountStats(
      makeStats({ total_actual_cost: 1, today_actual_cost: 1, by_platform: [usage('openai', 1)] }),
      [quota({ platform: 'openai', daily_limit_usd: 10, daily_usage_usd: 1 })]
    )
    expect(cardPlatforms(wrapper)).toEqual(['openai'])
    expect(wrapper.text()).toContain('dashboard.platformQuota.title')
  })

  it('限额为 0 的平台视为已配置，渲染禁用态', () => {
    const wrapper = mountStats(makeStats(), [quota({ platform: 'gemini', weekly_limit_usd: 0 })])
    expect(cardPlatforms(wrapper)).toEqual(['gemini'])
    expect(wrapper.text()).toContain('dashboard.platformQuota.disabled')
    expect(wrapper.text()).toContain('dashboard.platformCount:{"count":1}')
  })

  it('固定顺序之外的平台也产生卡片，并排在固定顺序之后', () => {
    const wrapper = mountStats(
      makeStats({ total_actual_cost: 0.5, today_actual_cost: 0, by_platform: [usage('kimi', 0.3), usage('anthropic', 0.2)] })
    )
    expect(cardPlatforms(wrapper)).toEqual(['anthropic', 'kimi'])
    expect(wrapper.text()).toContain('Kimi')
  })

  it('总值大于各平台之和时追加"其他"卡片，且不计入平台计数', () => {
    const wrapper = mountStats(
      makeStats({ total_actual_cost: 1.0, today_actual_cost: 0, by_platform: [usage('anthropic', 0.4)] })
    )
    expect(cardPlatforms(wrapper)).toEqual(['anthropic', '__other__'])
    expect(wrapper.text()).toContain('dashboard.platformOther')
    expect(wrapper.text()).toContain('dashboard.platformCount:{"count":1}')
  })

  it('没有任何用量也没有配置限额时不渲染整块', () => {
    const wrapper = mountStats(makeStats(), [quota({ platform: 'anthropic' }), quota({ platform: 'openai' })])
    expect(wrapper.html()).not.toContain('dashboard.platformBreakdown')
    expect(cardPlatforms(wrapper)).toEqual([])
  })

  it('简易模式不渲染整块', () => {
    const wrapper = mountStats(makeStats({ by_platform: [usage('openai', 1)] }), null, true)
    expect(wrapper.html()).not.toContain('dashboard.platformBreakdown')
  })
})
