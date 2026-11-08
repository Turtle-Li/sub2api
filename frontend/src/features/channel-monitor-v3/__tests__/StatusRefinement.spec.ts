import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import type { PlazaGroupAvailability } from '@/api/modelPlaza'
import GroupStatusCard from '../GroupStatusCard.vue'
import { cacheCapability, quotaNotice } from '../statusPresentation'
import zh from '@/i18n/locales/zh/channelMonitorV3'

const now = Date.parse('2026-11-09T10:00:00Z')
const status = (overrides: Partial<PlazaGroupAvailability> = {}): PlazaGroupAvailability => ({
  status: 'down', source: 'probe', checked_at: new Date(now).toISOString(), probe_enabled: true,
  request_success_rate: null, probe_success_rate: null, request_sample_state: 'none',
  probe_cache_verification_version: 1, probe_cache_verification_window_hours: 24,
  cells: Array.from({ length: 72 }, (_, i) => ({ start: new Date(now - (71 - i) * 600000).toISOString(), status: 'operational', source: 'probe' })),
  ...overrides,
})
afterEach(() => vi.useRealTimers())

describe('status refinement contract', () => {
  it('retains one keyboard stop when a rollback shrinks history to 18 cells', async () => {
    const group = { id: 10, name: 'Claude', platform: 'anthropic', availability: status() }
    const wrapper = mount(GroupStatusCard, { props: { group }, global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })] } })
    await wrapper.setProps({ group: { ...group, availability: status({ cells: group.availability.cells.slice(-18) }) } })
    expect(wrapper.findAll('.status-timeline button')).toHaveLength(18)
    expect(wrapper.findAll('.status-timeline button[tabindex="0"]')).toHaveLength(1)
    wrapper.unmount()
  })

  it('verifies capability using a dated receipt regardless of cold aggregate ratio', () => {
    for (const rate of [0, 16, 50, 100]) {
      expect(cacheCapability(status({ probe_cache_verified_at: new Date(now - 3600000).toISOString(), probe_cache_read_rate: rate }), now)).toBe('verified')
    }
    for (const date of ['', 'invalid', new Date(now + 1).toISOString(), new Date(now - 86400001).toISOString()]) {
      expect(cacheCapability(status({ probe_cache_verified_at: date }), now)).toBe('pending')
    }
    expect(cacheCapability(status({ probe_cache_verification_version: undefined }), now)).toBeNull()
    expect(cacheCapability(status({ probe_enabled: false }), now)).toBeNull()
  })

  it('requires a fresh, typed quota snapshot and never guesses a reset', () => {
    const base = status({ pool_quota_state: 'limited', pool_quota_observed_at: new Date(now).toISOString() })
    expect(quotaNotice(base, now)?.remaining).toBeNull()
    expect(quotaNotice({ ...base, pool_quota_resets_at: new Date(now + 65000).toISOString() }, now)?.clock).toBe('00:01:05')
    expect(quotaNotice(base, now + 90001)).toBeNull()
    expect(quotaNotice({ ...base, pool_quota_observed_at: new Date(now + 60001).toISOString() }, now)).toBeNull()
    expect(quotaNotice({ ...base, pool_quota_resets_at: 'invalid' }, now)?.clock).toBeNull()
    expect(quotaNotice({ ...base, pool_quota_resets_at: new Date(now - 1).toISOString() }, now)?.clock).toBeNull()
  })

  it('renders fine history with one keyboard stop and live partial recovery countdown', async () => {
    vi.useFakeTimers(); vi.setSystemTime(now)
    const wrapper = mount(GroupStatusCard, { props: { group: { id: 10, name: 'Claude Code', platform: 'anthropic', availability: status({
      pool_quota_state: 'limited', pool_quota_observed_at: new Date(now).toISOString(), pool_quota_resets_at: new Date(now + 65000).toISOString(),
      probe_cache_verified_at: new Date(now - 3600000).toISOString(),
    }) } }, global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })] } })
    expect(wrapper.get('[data-testid="pool-quota-notice"]').text()).toContain('部分账号额度不足')
    expect(wrapper.get('[data-testid="quota-countdown"]').text()).toBe('00:01:05')
    expect(wrapper.get('[data-metric="cache"]').text()).toBe('缓存已验证')
    expect(wrapper.findAll('.status-timeline button')).toHaveLength(72)
    expect(wrapper.findAll('.status-timeline button[tabindex="0"]')).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.get('[data-testid="quota-countdown"]').text()).toBe('00:01:04')
    await vi.advanceTimersByTimeAsync(64000)
    expect(wrapper.get('[data-testid="pool-quota-notice"]').text()).toContain('等待检测确认')
    await vi.advanceTimersByTimeAsync(26000)
    expect(wrapper.find('[data-testid="pool-quota-notice"]').exists()).toBe(false)
    wrapper.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})
