import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh/channelMonitorV3'
import en from '@/i18n/locales/en/channelMonitorV3'
import type { ModelStatusGroup } from '@/api/modelStatus'
import GroupStatusCard from '../GroupStatusCard.vue'

describe('check-cache translated window', () => {
  it.each([
    { locale: 'zh', window: '近 180 分钟', cache: '近 3h · Token 加权' },
    { locale: 'en', window: 'Last 180 minutes', cache: 'Last 3h · token-weighted' },
  ])('renders both window parameters with the real $locale messages', ({ locale, window, cache }) => {
    const group: ModelStatusGroup = { id: 18, name: 'Kimi', platform: 'kimi', availability: {
      status: 'operational', source: 'probe', checked_at: null, probe_enabled: true,
      probe_metrics_version: 1, probe_success_rate: 100, probe_sample_state: 'ready',
      request_success_rate: null, cache_read_rate: null, ttft_p50_ms: null, request_sample_state: 'none',
      probe_cache_read_rate: 96.5, probe_cache_sample_state: 'ready', probe_cache_scope: 'group_probe',
      probe_cache_statistic: 'weighted_tokens', window_minutes: 180, min_requests: 20, cells: [],
    } }
    const i18n = createI18n({ legacy: false, locale, messages: { zh, en } })
    const wrapper = mount(GroupStatusCard, { props: { group }, global: { plugins: [i18n] } })
    expect(wrapper.text()).toContain(window)
    expect(wrapper.text()).toContain(cache)
    expect(wrapper.get('[data-metric="cache"]').text()).toBe('96.5%')
    wrapper.unmount()
  })
})
