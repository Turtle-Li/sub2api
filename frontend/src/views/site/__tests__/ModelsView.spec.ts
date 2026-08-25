import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'

import ModelsView from '../ModelsView.vue'

const { appStore, authStore, getModelPlaza } = vi.hoisted(() => ({
  appStore: {
    cachedPublicSettings: {} as Record<string, unknown>,
    siteName: 'TurtleRoute',
    siteLogo: '',
    docUrl: '',
    publicSettingsLoaded: true,
    fetchPublicSettings: vi.fn(),
  },
  authStore: {
    isAuthenticated: false,
    isAdmin: false,
    checkAuth: vi.fn(),
  },
  getModelPlaza: vi.fn(),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => appStore,
  useAuthStore: () => authStore,
}))

// isFeatureFlagEnabled 直接从 @/stores/app 取 store，不走桶文件
vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('@/api/modelPlaza', () => ({ getModelPlaza }))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh' } }),
  }
})

async function mountModels() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }],
  })

  const wrapper = mount(ModelsView, {
    global: {
      plugins: [router],
      stubs: {
        RouterLink: RouterLinkStub,
        LocaleSwitcher: { template: '<div />' },
        Icon: { template: '<span />' },
      },
    },
  })

  await flushPromises()
  return wrapper
}

describe('ModelsView', () => {
  beforeEach(() => {
    getModelPlaza.mockReset()
    appStore.cachedPublicSettings = {}
  })

  // 广场关闭时后端返回 404，require_auth 开启时匿名也拿不到。
  // 公开页在这两种情况下都不能白屏，也不能假装这就是全量目录。
  it('falls back to a labelled representative list when the plaza is unavailable', async () => {
    getModelPlaza.mockRejectedValue(new Error('404'))

    const wrapper = await mountModels()
    const text = wrapper.text()

    expect(text).toContain('site.models.fallbackTitle')
    expect(text).toContain('claude-sonnet-4-5')
    expect(text).not.toContain('site.models.loading')
  })

  it('renders live models with their effective rate when the plaza responds', async () => {
    getModelPlaza.mockResolvedValue({
      description: '',
      groups: [
        {
          id: 1,
          name: 'Standard',
          description: '',
          platform: 'anthropic',
          subscription_type: 'standard',
          rate_multiplier: 0.3,
          peak_rate_enabled: false,
          peak_start: '',
          peak_end: '',
          peak_rate_multiplier: 1,
          is_exclusive: false,
          image_rate_independent: false,
          image_rate_multiplier: 1,
          long_context_pricing_enabled: true,
          models: [
            {
              name: 'claude-sonnet-4-5',
              platform: 'anthropic',
              pricing: {
                billing_mode: 'token',
                input_price: 0.000003,
                output_price: 0.000015,
                cache_write_price: null,
                cache_read_price: null,
                image_input_price: null,
                image_output_price: null,
                per_request_price: null,
                intervals: [],
              },
              official_pricing: null,
            },
          ],
        },
      ],
    })

    const wrapper = await mountModels()
    const text = wrapper.text()

    expect(text).not.toContain('site.models.fallbackTitle')
    expect(text).toContain('Standard')
    // USD/token 要换算成每百万 tokens 才有可读性
    expect(text).toContain('$3.00')
    expect(text).toContain('$15.00')
  })

  // 专属倍率优先于分组倍率，这是计费口径，展示错了就是误导报价
  it('prefers the per-account rate over the group rate', async () => {
    getModelPlaza.mockResolvedValue({
      description: '',
      groups: [
        {
          id: 2,
          name: 'VIP',
          description: '',
          platform: 'anthropic',
          subscription_type: 'standard',
          rate_multiplier: 0.5,
          user_rate_multiplier: 0.2,
          peak_rate_enabled: false,
          peak_start: '',
          peak_end: '',
          peak_rate_multiplier: 1,
          is_exclusive: true,
          image_rate_independent: false,
          image_rate_multiplier: 1,
          long_context_pricing_enabled: true,
          models: [
            { name: 'gpt-4o', platform: 'openai', pricing: null, official_pricing: null },
          ],
        },
      ],
    })

    const wrapper = await mountModels()

    expect(wrapper.text()).toContain('0.2')
    expect(wrapper.text()).not.toContain('0.5')
  })
})
