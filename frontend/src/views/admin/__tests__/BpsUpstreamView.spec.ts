import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import BpsUpstreamView from '../BpsUpstreamView.vue'

const { getOverview, updateConfig, resetBreaker, listAccounts, showSuccess, showError } = vi.hoisted(() => ({
  getOverview: vi.fn(),
  updateConfig: vi.fn(),
  resetBreaker: vi.fn(),
  listAccounts: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin/bpsUpstream', () => ({ getOverview, updateConfig, resetBreaker }))
vi.mock('@/api/admin/accounts', () => ({ list: listAccounts, accountsAPI: { list: listAccounts }, default: { list: listAccounts } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, te: () => false }),
}))

const PassThroughStub = defineComponent({ template: '<div><slot /></div>' })
const ToggleStub = defineComponent({
  props: { modelValue: Boolean },
  emits: ['update:modelValue'],
  template: '<button data-test="toggle" @click="$emit(\'update:modelValue\', !modelValue)">{{ modelValue }}</button>',
})
const SelectStub = defineComponent({
  props: { modelValue: { type: String, default: null }, options: { type: Array, default: () => [] } },
  emits: ['update:modelValue', 'search'],
  template: `
    <select data-test="picker" :value="modelValue" @change="$emit('update:modelValue', $event.target.value)">
      <option value="">select</option>
      <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
    </select>
  `,
})

const future = new Date(Date.now() + 60_000).toISOString()

function overview(overrides: Record<string, unknown> = {}) {
  return {
    config: { enabled: true, account_ids: [69], live_search: false },
    policy: { models: ['gpt-6-astra'], breaker_threshold: 3, breaker_open_seconds: 600, immediate_breaker_status: [401, 403, 429] },
    accounts: [{
      id: 69, name: 'cashtech', platform: 'openai', type: 'oauth', status: 'active', schedulable: true, eligible: true, missing: false,
      stats: { account_id: 69, successes: 5, fallbacks: 2, errors_after_output: 1, skipped: { compact: 3 }, breaker_open_until: future, breaker_failures: 0 },
    }],
    unlisted_stats: [{ account_id: 12, successes: 1, fallbacks: 0, errors_after_output: 0, breaker_failures: 0 }],
    monitor_started_at: new Date().toISOString(),
    now: new Date().toISOString(),
    ...overrides,
  }
}

const wrappers: VueWrapper[] = []

async function mountView() {
  const wrapper = mount(BpsUpstreamView, {
    global: { stubs: { AppLayout: PassThroughStub, Toggle: ToggleStub, Select: SelectStub } },
  })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}

describe('BpsUpstreamView', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    getOverview.mockResolvedValue(overview())
    updateConfig.mockImplementation(async (cfg) => cfg)
    resetBreaker.mockResolvedValue(undefined)
    listAccounts.mockImplementation(async (_page, _size, filters: { type: string }) => ({
      items: filters.type === 'oauth' ? [{ id: 69, name: 'cashtech' }, { id: 90, name: 'backup' }] : [],
    }))
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  afterEach(() => {
    wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
    vi.useRealTimers()
    vi.clearAllMocks()
    vi.restoreAllMocks()
  })

  it('renders totals, accounts, breaker state without an event list', async () => {
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="bps-total-success"]').text()).toBe('6')
    expect(wrapper.get('[data-test="bps-total-fallback"]').text()).toBe('2')
    expect(wrapper.get('[data-test="bps-total-breakers"]').text()).toBe('1')
    expect(wrapper.findAll('[data-test="bps-account-row"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('admin.bpsUpstream.resetBreaker')
    expect(wrapper.find('[data-test="bps-event-row"]').exists()).toBe(false)
  })

  it('filters account picker, adds and removes accounts', async () => {
    const wrapper = await mountView()
    const options = wrapper.findAll('[data-test="bps-account-picker"] option').map((option) => option.attributes('value'))
    expect(options).toEqual(['', '90'])

    await wrapper.get('[data-test="bps-account-picker"]').setValue('90')
    await wrapper.get('[data-test="bps-add-account"]').trigger('click')
    await flushPromises()
    expect(updateConfig).toHaveBeenLastCalledWith({ enabled: true, account_ids: [69, 90], live_search: false })

    // 保存后重新加载，概览仍只返回 #69。
    await wrapper.get('[data-test="bps-remove-account"]').trigger('click')
    await flushPromises()
    expect(updateConfig).toHaveBeenLastCalledWith({ enabled: true, account_ids: [], live_search: false })
  })

  it('toggles the global switch and resets breakers', async () => {
    const wrapper = await mountView()
    await wrapper.get('[data-test="bps-global-toggle"]').trigger('click')
    await flushPromises()
    expect(updateConfig).toHaveBeenCalledWith({ enabled: false, account_ids: [69], live_search: false })

    await wrapper.get('[data-test="bps-live-search-toggle"]').trigger('click')
    await flushPromises()
    expect(updateConfig).toHaveBeenLastCalledWith({ enabled: true, account_ids: [69], live_search: true })

    const resetButton = wrapper.findAll('button').find((button) => button.text() === 'admin.bpsUpstream.resetBreaker')
    await resetButton!.trigger('click')
    await flushPromises()
    expect(resetBreaker).toHaveBeenCalledWith(69)
  })

  it('auto refreshes and surfaces save errors', async () => {
    const wrapper = await mountView()
    expect(getOverview).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(10_000)
    await flushPromises()
    expect(getOverview).toHaveBeenCalledTimes(2)

    updateConfig.mockRejectedValueOnce(new Error('account 81 is not an OpenAI OAuth account'))
    await wrapper.get('[data-test="bps-global-toggle"]').trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('account 81 is not an OpenAI OAuth account')
  })
})
