import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import GatewayHunterPanel from '../GatewayHunterPanel.vue'
import type { GatewayHunterState } from '@/api/admin/codexGatewayHunter'

const { getState, updateConfig, start, stop, showSuccess, showError } = vi.hoisted(() => ({
  getState: vi.fn(),
  updateConfig: vi.fn(),
  start: vi.fn(),
  stop: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin/codexGatewayHunter', () => ({
  codexGatewayHunterAPI: { getState, updateConfig, start, stop },
}))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, te: () => true }),
}))

const nowSeconds = Math.floor(Date.now() / 1000)

function snapshot(overrides: Partial<GatewayHunterState> = {}): GatewayHunterState {
  return {
    generated_at: nowSeconds,
    running: true,
    leader: true,
    started_at: nowSeconds - 600,
    last_error: '',
    config: {
      enabled: true,
      model: 'gpt-6-astra',
      hunter_account_ids: [9],
      tracker_account_ids: [9, 15],
      scout_group_id: 25,
      scout_proxy_id: 147,
      probe_proxy_id: 0,
      workers: 6,
      min_probe_interval_seconds: 45,
      scout_interval_seconds: 3,
      tracker_daily_budget: 150,
    },
    accounts: [
      { id: 9, name: 'pro', role: 'hunter', primary_used_percent: 16, secondary_used_percent: 40, probes_today: 120, retired: false, retired_reason: '', last_error: '' },
      { id: 15, name: 'plus', role: 'tracker', primary_used_percent: 5, secondary_used_percent: 97, probes_today: 4, retired: true, retired_reason: 'daily_budget', last_error: '' },
      { id: 54, name: 'free', role: 'scout', primary_used_percent: null, secondary_used_percent: null, probes_today: 0, retired: false, retired_reason: '', last_error: '' },
    ],
    gateways: [
      { gateway: 'unified-12', first_seen: nowSeconds - 500, scouted_at: nowSeconds - 20, scout_account_id: 54, probes: 8, hits: 2, last_tier: 'full', last_probe_at: nowSeconds - 5, hot_since: nowSeconds - 60, hot_accounts: { '9': 'full', '15': 'full' } },
      { gateway: 'unified-3', first_seen: nowSeconds - 500, scouted_at: nowSeconds - 30, scout_account_id: 54, probes: 9, hits: 0, last_tier: 'pruned', last_probe_at: nowSeconds - 40, hot_since: null, hot_accounts: {} },
    ],
    windows: [{ gateway: 'unified-12', start: nowSeconds - 60, end: null, duration_seconds: 60, found_by: 9, confirmed_by: [9, 15] }],
    probes: [{ at: nowSeconds - 5, phase: 'track', account_id: 15, gateway: 'unified-12', http_status: 200, tier: 'full', answer: '高市早苗', ttfb_ms: 900, total_ms: 9000, reasoning_tokens: 0 }],
    counters: { scouts: 100, scout_errors: 2, hunts: 300, hunt_hits: 2, bare_probes: 20, bare_hits: 0, tracks: 12, track_hits: 10, windows: 1, covered_seconds: 60, running_seconds: 600 },
    ...overrides,
  }
}

const wrappers: VueWrapper[] = []
async function mountPanel() {
  const wrapper = mount(GatewayHunterPanel)
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}

describe('GatewayHunterPanel', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    getState.mockResolvedValue(snapshot())
  })

  afterEach(() => {
    wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  it('summarizes the pool and highlights full-power gateways', async () => {
    const wrapper = await mountPanel()
    expect(wrapper.get('[data-test="ghp-pool"]').text()).toBe('2')
    expect(wrapper.get('[data-test="ghp-hot"]').text()).toBe('1')
    expect(wrapper.find('[data-test="ghp-hot-unified-12"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('admin.codexTurnState.hunter.reasons.daily_budget')
    expect(wrapper.get('[data-test="ghp-log"]').text()).toContain('高市早苗')
  })

  it('polls every five seconds until paused', async () => {
    const wrapper = await mountPanel()
    expect(getState).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(5000)
    expect(getState).toHaveBeenCalledTimes(2)
    await wrapper.get('[data-test="ghp-pause"]').trigger('click')
    await vi.advanceTimersByTimeAsync(10000)
    expect(getState).toHaveBeenCalledTimes(2)
  })

  it('saves edited settings and keeps the draft across polls', async () => {
    updateConfig.mockResolvedValue(snapshot().config)
    const wrapper = await mountPanel()
    await wrapper.get('[data-test="ghp-tab-settings"]').trigger('click')
    await wrapper.get('[data-test="ghp-field-hunter_account_ids"]').setValue('9, 15')
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect((wrapper.get('[data-test="ghp-field-hunter_account_ids"]').element as HTMLInputElement).value).toBe('9, 15')

    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateConfig).toHaveBeenCalledWith(expect.objectContaining({ enabled: true, hunter_account_ids: [9, 15], tracker_account_ids: [9, 15] }))
    expect(showSuccess).toHaveBeenCalled()
  })

  it('starts a stopped monitor', async () => {
    getState.mockResolvedValue(snapshot({ running: false, started_at: null, config: { ...snapshot().config, enabled: false } }))
    start.mockResolvedValue(snapshot())
    const wrapper = await mountPanel()
    await wrapper.get('[data-test="ghp-tab-settings"]').trigger('click')
    await wrapper.get('[data-test="ghp-start"]').trigger('click')
    await flushPromises()
    expect(start).toHaveBeenCalled()
    expect(wrapper.get('[data-test="ghp-running"]').text()).toBe('admin.codexTurnState.hunter.running')
  })
})
