import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CodexTurnStateView from '../CodexTurnStateView.vue'

const {
  getState, getHistory, getJob, startProbe, updateSettings, removeMonitoredModel, addMonitoredModel, getDegraded,
  showSuccess, showError, statsLoad,
} = vi.hoisted(() => ({
  getState: vi.fn(),
  getHistory: vi.fn(),
  getJob: vi.fn(),
  startProbe: vi.fn(),
  updateSettings: vi.fn(),
  removeMonitoredModel: vi.fn(),
  addMonitoredModel: vi.fn(),
  getDegraded: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  statsLoad: vi.fn(),
}))

vi.mock('@/api/admin/codexTurnState', () => ({
  ACTIVE_JOB_STATUSES: ['queued', 'running', 'waiting'],
  getState,
  getHistory,
  getJob,
  startProbe,
  updateSettings,
  removeMonitoredModel,
  addMonitoredModel,
  getDegraded,
}))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, te: () => false }),
}))

const LayoutStub = defineComponent({ template: '<div><slot /></div>' })
const ToggleStub = defineComponent({
  props: { modelValue: Boolean },
  emits: ['update:modelValue'],
  template: '<button type="button" @click="$emit(\'update:modelValue\', !modelValue)" />',
})
const PanelStub = defineComponent({
  name: 'DegradationProbePanel',
  props: { pinnedAccounts: { type: Array, default: () => [] } },
  template: '<div data-test="panel-stub" />',
})
const StatsStub = defineComponent({
  name: 'ProbeSourceStatsCard',
  methods: { load: statsLoad },
  template: '<div data-test="stats-stub" />',
})

function slot(overrides: Record<string, unknown> = {}) {
  return {
    model: 'gpt-6-astra', target_len: 292, state_len: 292, valid: true, expired: false, expires_at: '2026-10-02 10:00:00 UTC',
    remaining_minutes: 30, remaining_seconds: 1800, cookie_present: false, cookie_valid: false, cookie_remaining_seconds: 0,
    cookie_expires_at: null, pinned_updated_at: '2026-10-02T09:00:00Z', length_degraded: false, backoff_seconds: 0,
    next_probe_seconds: 600, degradation: 'no_record', recent_degradation: null,
    ...overrides,
  }
}

function snapshot(overrides: Record<string, unknown> = {}) {
  return {
    generated_at: 1790000000,
    probing_enabled: true,
    settings: { refresh_advance_minutes: 5, probing_enabled: true, error: null },
    renewal_timing: { samples: 3, average_seconds: 42, last_seconds: 40, since: null, window: 200 },
    read_only: false,
    degraded_enabled: true,
    accounts: [
      {
        id: 69, name: 'cashtech', refresh_advance_minutes: 5, source: 'config', error: null,
        models: [
          slot(),
          slot({ model: 'gpt-5.6-terra', valid: false, state_len: 0, remaining_seconds: 0, remaining_minutes: 0, expires_at: null }),
        ],
      },
      { id: 90, name: 'broken', refresh_advance_minutes: 5, source: 'panel', error: 'token missing', models: [] },
    ],
    degraded: [
      { account_id: 77, account_name: 'other', requested_model: 'gpt-6-astra', sent_model: 'gpt-6-astra', response_model: 'gpt-5', count: 3, first_seen: '', last_seen: '', ttft_avg_ms: null },
    ],
    degraded_error: '',
    degraded_window: '24h',
    proxy_count: 4,
    static_proxy_count: 3,
    dynamic_provider_count: 1,
    poll_interval_seconds: 30,
    jobs: [],
    ...overrides,
  }
}

const wrappers: VueWrapper[] = []

async function mountView() {
  const wrapper = mount(CodexTurnStateView, {
    global: {
      stubs: {
        AppLayout: LayoutStub,
        Toggle: ToggleStub,
        DegradationProbePanel: PanelStub,
        ProbeSourceStatsCard: StatsStub,
        AddMonitoredModelDialog: true,
        ProxySourcesDialog: true,
      },
    },
  })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}

describe('CodexTurnStateView', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'setTimeout', 'clearTimeout'] })
    getState.mockResolvedValue(snapshot())
    getHistory.mockResolvedValue({ '69:gpt-6-astra': [{ at: 1, state_len: 292, http_status: 200, header_ms: 10, ok: true, outcome: 'saved', source: 'static' }] })
    updateSettings.mockResolvedValue({})
    removeMonitoredModel.mockResolvedValue(undefined)
    addMonitoredModel.mockResolvedValue(undefined)
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  afterEach(() => {
    wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
    vi.useRealTimers()
    vi.restoreAllMocks()
    vi.clearAllMocks()
  })

  it('renders overview counts, renewal labels and pinned accounts for the probe panel', async () => {
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="cts-overview-accounts"]').text()).toBe('2')
    expect(wrapper.get('[data-test="cts-overview-models"]').text()).toBe('2')
    expect(wrapper.get('[data-test="cts-overview-healthy"]').text()).toBe('1')
    expect(wrapper.get('[data-test="cts-overview-problem"]').text()).toBe('2')
    expect(wrapper.get('[data-test="cts-overview-proxies"]').text()).toBe('3 / 1')
    expect(wrapper.get('[data-test="cts-renewal-69-gpt-6-astra"]').text()).toBe('admin.codexTurnState.renewal.valid')
    expect(wrapper.get('[data-test="cts-renewal-69-gpt-5.6-terra"]').text()).toBe('admin.codexTurnState.renewal.waitingFirst')
    expect(wrapper.get('[data-test="cts-history-69-gpt-6-astra"]').text()).toBe('✓')
    expect(wrapper.get('[data-test="cts-account-error"]').text()).toContain('admin.codexTurnState.slots.accountError')
    expect(wrapper.findAll('[data-test="cts-degraded-row"]')).toHaveLength(1)
    expect(wrapper.find('[data-test="cts-paused"]').exists()).toBe(false)

    expect(wrapper.getComponent(PanelStub).props('pinnedAccounts')).toEqual([
      { id: 69, name: 'cashtech', models: ['gpt-6-astra'] },
    ])
  })

  it('shows a load error banner when the manager is unreachable', async () => {
    getState.mockRejectedValueOnce({ status: 503, message: 'unavailable' })
    const wrapper = await mountView()

    const banner = wrapper.get('[data-test="cts-load-error"]')
    expect(banner.text()).toContain('admin.codexTurnState.errors.unavailable')
    expect(banner.text()).toContain('admin.codexTurnState.unknownHint')
    expect(wrapper.find('[data-test="cts-settings"]').exists()).toBe(false)

    await wrapper.get('[data-test="cts-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="cts-load-error"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="cts-settings"]').exists()).toBe(true)
  })

  it('auto refresh reads only the state endpoint', async () => {
    await mountView()
    expect(getState).toHaveBeenCalledTimes(1)
    expect(getHistory).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(20000)
    expect(getState).toHaveBeenCalledTimes(2)
    expect(getHistory).toHaveBeenCalledTimes(1)
    expect(statsLoad).not.toHaveBeenCalled()
  })

  it('stops auto refresh when toggled off', async () => {
    const wrapper = await mountView()
    await wrapper.get('[data-test="cts-auto-refresh"]').trigger('click')

    await vi.advanceTimersByTimeAsync(60000)
    expect(getState).toHaveBeenCalledTimes(1)
  })

  it('manual refresh reloads state, history and stats', async () => {
    const wrapper = await mountView()
    await wrapper.get('[data-test="cts-refresh"]').trigger('click')
    await flushPromises()

    expect(getState).toHaveBeenCalledTimes(2)
    expect(getHistory).toHaveBeenCalledTimes(2)
    expect(statsLoad).toHaveBeenCalledTimes(1)
  })

  it('queues a probe job, polls it and refreshes when done', async () => {
    startProbe.mockResolvedValue('job-1')
    getJob
      .mockResolvedValueOnce({ id: 'job-1', status: 'running', updated: 0, error: null })
      .mockResolvedValueOnce({ id: 'job-1', status: 'done', updated: 1, error: null })
    const wrapper = await mountView()

    await wrapper.get('[data-test="cts-probe-69-gpt-6-astra"]').trigger('click')
    await flushPromises()
    expect(startProbe).toHaveBeenCalledWith({ account_id: 69, model: 'gpt-6-astra', force: true })
    expect(showSuccess).toHaveBeenCalledWith('admin.codexTurnState.probeResult.queued')
    expect(wrapper.get('[data-test="cts-probe-69-gpt-6-astra"]').attributes('disabled')).toBeDefined()

    await vi.advanceTimersByTimeAsync(2000)
    expect(getJob).toHaveBeenCalledTimes(1)
    expect(getState).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(getJob).toHaveBeenCalledTimes(2)
    expect(getJob).toHaveBeenCalledWith('job-1')
    expect(showSuccess).toHaveBeenCalledWith('admin.codexTurnState.probeResult.done')
    expect(getState).toHaveBeenCalledTimes(2)
    expect(getHistory).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="cts-probe-69-gpt-6-astra"]').attributes('disabled')).toBeUndefined()
  })

  it('reports a failed probe job', async () => {
    startProbe.mockResolvedValue('job-2')
    getJob.mockResolvedValueOnce({ id: 'job-2', status: 'error', updated: 0, error: 'no proxy' })
    const wrapper = await mountView()

    await wrapper.get('[data-test="cts-probe-69-gpt-6-astra"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.codexTurnState.probeResult.error')
  })

  it('removes a monitored model after confirmation', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-test="cts-remove-69-gpt-5.6-terra"]').trigger('click')
    await flushPromises()

    expect(window.confirm).toHaveBeenCalled()
    expect(removeMonitoredModel).toHaveBeenCalledWith(69, 'gpt-5.6-terra')
    expect(showSuccess).toHaveBeenCalledWith('admin.codexTurnState.removed')
    expect(getState).toHaveBeenCalledTimes(2)
  })

  it('pauses automatic probing through the settings endpoint', async () => {
    getState.mockResolvedValueOnce(snapshot()).mockResolvedValueOnce(snapshot({ probing_enabled: false }))
    const wrapper = await mountView()

    await wrapper.get('[data-test="cts-probing-toggle"]').trigger('click')
    await flushPromises()

    expect(updateSettings).toHaveBeenCalledWith({ probing_enabled: false })
    expect(showSuccess).toHaveBeenCalledWith('admin.codexTurnState.settings.pausedToast')
    expect(wrapper.find('[data-test="cts-paused"]').exists()).toBe(true)
  })

  it('validates and saves the renewal lead time', async () => {
    const wrapper = await mountView()
    const input = wrapper.get('[data-test="cts-advance-input"]')

    await input.setValue('45')
    await wrapper.get('[data-test="cts-advance-save"]').trigger('submit')
    expect(showError).toHaveBeenCalledWith('admin.codexTurnState.settings.advanceInvalid')
    expect(updateSettings).not.toHaveBeenCalled()

    await input.setValue('10')
    await wrapper.get('[data-test="cts-advance-save"]').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith({ refresh_advance_minutes: 10 })
  })

  it('adds a degraded account to monitoring with the default target length', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-test="cts-degraded-add-77"]').trigger('click')
    await flushPromises()

    expect(addMonitoredModel).toHaveBeenCalledWith({ account_id: 77, name: 'other', model: 'gpt-6-astra', target_state_len: 292 })
  })

  it('filters slots by status', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-test="cts-status-filter"]').setValue('healthy')
    expect(wrapper.findAll('[data-test="cts-slot-row"]')).toHaveLength(1)
    expect(wrapper.find('[data-test="cts-account-error"]').exists()).toBe(false)

    await wrapper.get('[data-test="cts-search"]').setValue('nothing')
    expect(wrapper.find('[data-test="cts-empty"]').exists()).toBe(true)
  })
})
