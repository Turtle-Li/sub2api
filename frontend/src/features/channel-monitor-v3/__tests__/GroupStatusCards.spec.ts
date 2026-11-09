import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import type { ModelStatusGroup } from '@/api/modelStatus'

const mocks = vi.hoisted(() => ({
  status: vi.fn(), settings: vi.fn(), update: vi.fn(), groups: vi.fn(), models: vi.fn(),
  auth: { isAdmin: false },
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, values?: Record<string, string>) => key.endsWith('.cacheHour') ? `${key} ${values?.start} – ${values?.end}` : key, locale: ref('zh-CN') }) }))
vi.mock('@/api/modelStatus', () => ({ getModelStatus: mocks.status }))
vi.mock('@/api/channelMonitorV3', () => ({ getSettings: mocks.settings, updateConfig: mocks.update }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getAll: mocks.groups, getModelAllowlistCandidates: mocks.models } } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => mocks.auth }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ fetchPublicSettings: vi.fn() }) }))
import GroupStatusCard from '../GroupStatusCard.vue'
import GroupStatusDashboard from '../GroupStatusDashboard.vue'
import GroupStatusControls from '../GroupStatusControls.vue'

const SelectStub = {
  props: ['modelValue', 'options', 'disabled'], emits: ['update:modelValue'],
  template: `<select :value="modelValue" :disabled="disabled" @change="$emit('update:modelValue', $event.target.value === '' ? null : (options.find(o => String(o.value) === $event.target.value)?.value))"><option value=""></option><option v-for="o in options" :key="o.value" :value="o.value" :disabled="o.disabled">{{ o.label }}</option></select>`,
}
const mountControls = () => mount(GroupStatusControls, { global: { stubs: { Select: SelectStub } } })

const group = (overrides: Record<string, unknown> = {}): ModelStatusGroup => ({
  id: 6, name: 'GPT 按量', platform: 'openai',
  availability: { status: 'operational', source: 'probe', checked_at: '2026-11-07T09:10:00Z', probe_enabled: true,
    probe_model: 'owner-selected', probe_metrics_version: 1, request_success_rate: 99.9, probe_success_rate: 100, cache_read_rate: null, cache_peak_hour_rate: 97.2, ttft_p50_ms: 3000,
    cache_statistic: 'best_complete_hour', cache_lookback_hours: 24, cache_hour_start: '2026-11-07T08:00:00Z', cache_hour_end: '2026-11-07T09:00:00Z',
    request_sample_state: 'ready', cache_sample_state: 'ready', cache_scope: 'shared_account_pool', probe_sample_state: 'ready', min_requests: 20, window_minutes: 180,
    cells: Array.from({ length: 18 }, (_, i) => ({ start: `2026-11-07T09:${String(i).padStart(2, '0')}:00Z`, status: i < 3 ? 'unknown' : 'operational', source: i < 3 ? 'none' : 'requests' })), ...overrides },
})
beforeEach(() => {
  vi.clearAllMocks()
  mocks.auth.isAdmin = false
  mocks.status.mockResolvedValue({ groups: [group()] })
  mocks.models.mockReset().mockResolvedValue(['new-owner-model', 'deepseek-chat'])
})
describe('status group cards', () => {
  it('keeps sharing internals out of public cards without borrowing cache or timings', () => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({ shared_probe: true, shared_probe_source_name: 'DeepSeek shared',
      source: 'shared_probe', request_sample_state: 'none', request_success_rate: null, ttft_p50_ms: null,
      cache_sample_state: 'none', cache_peak_hour_rate: null, probe_ttft_p50_ms: 200, probe_latency_p50_ms: 1000,
      probe_cache_verification_version: 1, probe_cache_verification_window_hours: 24, probe_cache_verified_at: new Date().toISOString(),
      cells: [{ start: new Date().toISOString(), status: 'operational', source: 'shared_probe' }] }) } })
    expect(wrapper.find('[data-testid="shared-probe-notice"]').exists()).toBe(false)
    expect(wrapper.find('footer').exists()).toBe(false)
    expect(wrapper.text()).toContain('channelMonitorV3.cards.probeSuccess')
    expect(wrapper.text()).not.toContain('DeepSeek shared')
    expect(wrapper.get('[data-metric="ttft"]').text()).toBe('—')
    expect(wrapper.find('[data-metric="cache"]').exists()).toBe(false)
    expect(wrapper.get('.status-timeline button').attributes('aria-label')).not.toContain('shared')
    wrapper.unmount()
  })
  it('retains the shared group’s independent real cache measurement', () => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({ shared_probe: true, cache_peak_hour_rate: 80 }) } })
    expect(wrapper.get('[data-metric="cache"]').text()).toBe('80.0%')
    expect(wrapper.get('[data-metric="success"]').text()).toBe('99.9%')
    wrapper.unmount()
  })
  it('configures a follower without losing its model or starting an independent check', async () => {
    mocks.settings.mockResolvedValue({ config: { version: 5, public_enabled: true, group_probe: { enabled: true, groups: [
      { group_id: 17, model: 'deepseek-chat', enabled: true, listed: true },
      { group_id: 18, model: 'kimi-original', enabled: true, listed: true },
    ] } } })
    mocks.groups.mockResolvedValue([
      { id: 17, name: 'DeepSeek', platform: 'deepseek', status: 'active' },
      { id: 18, name: 'Kimi', platform: 'kimi', status: 'active' },
    ])
    mocks.update.mockImplementation(async next => ({ ...next, version: 6 }))
    const wrapper = mountControls()
    await flushPromises()
    await wrapper.get('#probe-source-18').setValue('17')
    expect(wrapper.find('#probe-model-18').exists()).toBe(false)
    await wrapper.get('[data-testid="status-save-controls"]').trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({ group_probe: { enabled: true, groups: [
      { group_id: 17, model: 'deepseek-chat', enabled: true, listed: true },
      { group_id: 18, model: 'kimi-original', enabled: false, listed: true, source_group_id: 17 },
    ] } }))
    await wrapper.get('#probe-source-18').setValue('0')
    expect(wrapper.get('[data-status-group="18"] input[type="checkbox"]').element).toHaveProperty('checked', false)
    wrapper.unmount()
  })
  const probeCache = { cache_sample_state: 'none', cache_peak_hour_rate: null, probe_cache_read_rate: 96.5,
    probe_cache_sample_state: 'ready', probe_cache_scope: 'group_probe', probe_cache_statistic: 'weighted_tokens' }
  it.each([
    { probe_cache_sample_state: 'none' }, { probe_cache_sample_state: 'insufficient' },
    { probe_cache_sample_state: 'stale' }, { probe_cache_sample_state: 'disabled' },
    { probe_cache_scope: undefined }, { probe_cache_scope: 'shared_account_pool' },
    { probe_cache_statistic: undefined }, { probe_cache_statistic: 'best_complete_hour' },
    { window_minutes: 60 }, { probe_enabled: false }, { probe_metrics_version: undefined },
    { probe_sample_state: 'stale' }, { probe_cache_read_rate: null },
    { probe_cache_read_rate: NaN }, { probe_cache_read_rate: Infinity },
    { probe_cache_read_rate: -1 }, { probe_cache_read_rate: 101 },
  ])('keeps unavailable or incompatible check cache unknown: %j', overrides => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({ ...probeCache, ...overrides }) } })
    expect(wrapper.find('[data-metric="cache"]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('does not advertise synthetic ratios as conversational cache rates', () => {
    for (const rate of [0, 52, 89.99, 100]) {
      const wrapper = mount(GroupStatusCard, { props: { group: group({ ...probeCache, probe_cache_read_rate: rate }) } })
      expect(wrapper.find('[data-metric="cache"]').exists()).toBe(false)
      wrapper.unmount()
    }
  })
  it('prefers eligible real pool cache to check cache without relabelling either', () => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({ ...probeCache, cache_sample_state: 'ready', cache_peak_hour_rate: 87.2 }) } })
    expect(wrapper.get('[data-metric="cache"]').text()).toBe('87.2%')
    expect(wrapper.text()).toContain('channelMonitorV3.cards.cacheScope')
    expect(wrapper.text()).not.toContain('channelMonitorV3.cards.probeCache')
    wrapper.unmount()
  })
  it('keeps legacy synthetic cache receipts out of the public usage ratio', () => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({ cache_sample_state: 'none', cache_peak_hour_rate: null,
      probe_cache_sample_state: 'ready', probe_cache_scope: 'group_probe', probe_cache_statistic: 'weighted_tokens', probe_cache_read_rate: 96.5 }) } })
    expect(wrapper.find('[data-metric="cache"]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('shows labelled scheduled metrics when real request samples are missing', () => {
    // Given an idle group with qualified scheduled-check statistics.
    const idle = group({ request_sample_state: 'none', request_success_rate: null, ttft_p50_ms: null,
      probe_latency_p50_ms: 4500, probe_ttft_p50_ms: 1200 })
    // When its card is rendered.
    const wrapper = mount(GroupStatusCard, { props: { group: idle } })
    // Then scheduled statistics fill the card with an explicit source.
    expect(wrapper.get('[data-metric="success"]').text()).toBe('100.0%')
    expect(wrapper.get('[data-metric="ttft"]').text()).toBe('1.2s')
    expect(wrapper.text()).toContain('channelMonitorV3.cards.probeSuccess')
    expect(wrapper.text()).toContain('channelMonitorV3.cards.probeTTFT')
    expect(wrapper.text()).toContain('channelMonitorV3.cards.probeScope')
    expect(wrapper.text()).not.toContain('channelMonitorV3.cards.waiting')
    wrapper.unmount()
  })
  it('labels legacy whole-check duration without presenting it as first-token latency', () => {
    // Given qualified legacy checks without native first-token receipts.
    const idle = group({ request_sample_state: 'none', request_success_rate: null, ttft_p50_ms: null,
      probe_latency_p50_ms: 4500, probe_ttft_p50_ms: null })
    // When its card is rendered.
    const wrapper = mount(GroupStatusCard, { props: { group: idle } })
    // Then the actual whole-request duration is explicitly labelled.
    expect(wrapper.get('[data-metric="ttft"]').text()).toBe('4.5s')
    expect(wrapper.text()).toContain('channelMonitorV3.cards.probeLatency')
    expect(wrapper.text()).not.toContain('channelMonitorV3.cards.probeTTFT')
    wrapper.unmount()
  })
  it('publishes the labelled best complete hour independently of a legacy current rate', () => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({
      cache_read_rate: null, cache_peak_hour_rate: 87.2, cache_statistic: 'best_complete_hour', cache_lookback_hours: 24,
      cache_hour_start: '2026-11-07T08:00:00Z', cache_hour_end: '2026-11-07T09:00:00Z',
    }) } })
    expect(wrapper.get('[data-metric="cache"]').text()).toBe('87.2%')
    expect(wrapper.text()).toContain('channelMonitorV3.cards.cache')
    expect(wrapper.get('[data-metric="cache"]').attributes('title')).toContain('2026')
    wrapper.unmount()
  })
  it('shows cache share, request success and actual first-token P50 without prices', () => {
    const wrapper = mount(GroupStatusCard, { props: { group: group() } })
    expect(wrapper.get('[data-metric="cache"]').text()).toBe('97.2%')
    expect(wrapper.get('[data-metric="success"]').text()).toBe('99.9%')
    expect(wrapper.get('[data-metric="ttft"]').text()).toBe('3.0s')
    expect(wrapper.findAll('.status-timeline button')).toHaveLength(18)
    expect(wrapper.find('table').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('$')
    expect(wrapper.text()).not.toContain('owner-selected')
    wrapper.unmount()
  })
  it('does not publish small-sample numerics, including probe percentages', () => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({ request_sample_state: 'insufficient', cache_sample_state: 'insufficient', probe_sample_state: 'insufficient', request_success_rate: 66.7 }) } })
    expect(wrapper.findAll('dd').map(x => x.text())).toEqual(['—', '—'])
    expect(wrapper.text()).not.toMatch(/66\.7%|100\.0%/)
    expect(wrapper.text()).toContain('channelMonitorV3.cards.insufficient')
    wrapper.unmount()
  })
  it('labels qualified probe success without inventing latency or cache data', () => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({ request_sample_state: 'none', cache_sample_state: 'none', request_success_rate: null, cache_read_rate: null, ttft_p50_ms: null }) } })
    expect(wrapper.findAll('dd').map(x => x.text())).toEqual(['100.0%', '—'])
    expect(wrapper.text()).toContain('channelMonitorV3.cards.probeSuccess')
    expect(wrapper.text()).not.toContain('channelMonitorV3.cards.cacheScope')
    wrapper.unmount()
  })
  it('shows real pool cache independently of idle check-group metrics and labels its scope', () => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({ request_sample_state: 'none', request_success_rate: null, ttft_p50_ms: null }) } })
    expect(wrapper.get('[data-metric="cache"]').text()).toBe('97.2%')
    expect(wrapper.get('[data-metric="success"]').text()).toBe('100.0%')
    expect(wrapper.text()).toContain('channelMonitorV3.cards.cacheScope')
    wrapper.unmount()
  })
  it('prefers independently qualified real metrics and fills only missing request timing', () => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({ ttft_p50_ms: null, probe_ttft_p50_ms: 0, probe_latency_p50_ms: 4500 }) } })
    expect(wrapper.get('[data-metric="success"]').text()).toBe('99.9%')
    expect(wrapper.get('[data-metric="ttft"]').text()).toBe('0.0s')
    expect(wrapper.text()).toContain('channelMonitorV3.cards.probeTTFT')
    expect(wrapper.text()).not.toContain('channelMonitorV3.cards.probeSuccess')
    wrapper.unmount()
  })
  it.each([
    { probe_metrics_version: undefined }, { probe_metrics_version: 2 },
    { probe_enabled: false }, { probe_sample_state: 'stale' },
    { probe_sample_state: 'insufficient' }, { probe_sample_state: 'disabled' },
    { probe_sample_state: 'none' },
  ])('keeps unavailable or incompatible probe metrics unknown: %j', overrides => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({ request_sample_state: 'none', request_success_rate: null, ttft_p50_ms: null,
      probe_ttft_p50_ms: 1200, probe_latency_p50_ms: 4500, ...overrides }) } })
    expect(wrapper.get('[data-metric="success"]').text()).toBe('—')
    expect(wrapper.get('[data-metric="ttft"]').text()).toBe('—')
    wrapper.unmount()
  })
  it.each([-1, Infinity, NaN, 101])('rejects invalid probe percentages without altering real cache: %s', value => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({ request_sample_state: 'none', probe_success_rate: value, probe_ttft_p50_ms: -1, probe_latency_p50_ms: Infinity }) } })
    expect(wrapper.get('[data-metric="success"]').text()).toBe('—')
    expect(wrapper.get('[data-metric="ttft"]').text()).toBe('—')
    expect(wrapper.get('[data-metric="cache"]').text()).toBe('97.2%')
    wrapper.unmount()
  })
  it.each([
    { cache_sample_state: 'below_threshold', cache_read_rate: 52 },
    { cache_peak_hour_rate: 101 },
    { cache_peak_hour_rate: -1 },
    { cache_peak_hour_rate: Infinity },
    { cache_peak_hour_rate: null, cache_read_rate: 99 },
    { cache_statistic: undefined },
    { cache_lookback_hours: 3 },
    { cache_hour_start: null },
    { cache_hour_end: '2026-11-07T09:10:00Z' },
    { cache_sample_state: undefined },
    { cache_scope: undefined },
  ])('collapses unpublished cache while preserving group metrics: %j', overrides => {
    const wrapper = mount(GroupStatusCard, { props: { group: group(overrides) } })
    expect(wrapper.find('[data-metric="cache"]').exists()).toBe(false)
    expect(wrapper.get('[data-metric="success"]').text()).toBe('99.9%')
    expect(wrapper.get('[data-metric="ttft"]').text()).toBe('3.0s')
    wrapper.unmount()
  })
  it.each([0, 50, 89.99])('does not invent a ratio floor for a valid best-hour result: %s', value => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({ cache_peak_hour_rate: value }) } })
    expect(wrapper.get('[data-metric="cache"]').text()).toBe(`${value.toFixed(1)}%`)
    wrapper.unmount()
  })
  it('renders ordinary-user cards with no management controls or admin reads', async () => {
    const wrapper = mount(GroupStatusDashboard)
    await flushPromises()
    expect(wrapper.findAll('[data-testid="model-status-card"]')).toHaveLength(1)
    expect(wrapper.find('[data-testid="status-manage"]').exists()).toBe(false)
    expect(mocks.settings).not.toHaveBeenCalled()
    expect(mocks.groups).not.toHaveBeenCalled()
    expect(wrapper.find('table').exists()).toBe(false)
    wrapper.unmount()
  })
  it('clears old health and metrics on a failed refresh, keeping the group name', async () => {
    const wrapper = mount(GroupStatusDashboard)
    await flushPromises()
    mocks.status.mockRejectedValueOnce(new Error('offline'))
    await wrapper.findAll('button').find(x => x.text().includes('common.refresh'))!.trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('GPT 按量')
    expect(wrapper.text()).not.toContain('99.9%')
    wrapper.unmount()
  })
  it('edits and saves the exact owner-selected model in this feature, preserving unrelated settings', async () => {
    const config = { version: 4, public_enabled: true, min_requests: 1, ignored_error_categories: ['client_cancelled'], footer_note: 'keep', group_probe: { enabled: true, groups: [{ group_id: 6, model: 'old-owner-model', enabled: false, listed: true }] } }
    mocks.settings.mockResolvedValue({ config })
    mocks.groups.mockResolvedValue([{ id: 6, name: 'GPT', platform: 'openai', status: 'active' }])
    mocks.update.mockImplementation(async next => ({ ...next, version: 5 }))
    const wrapper = mountControls()
    await flushPromises()
    await wrapper.get('#probe-model-6').setValue('new-owner-model')
    await wrapper.get('#status-name-6').setValue('GPT Standard')
    await wrapper.get('[data-testid="status-save-controls"]').trigger('click')
    await flushPromises()
    expect(mocks.models).toHaveBeenCalledWith(6, undefined, true)
    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({ version: 4, footer_note: 'keep', ignored_error_categories: ['client_cancelled'], group_probe: { enabled: true, groups: [{ group_id: 6, model: 'new-owner-model', enabled: false, listed: true, display_name: 'GPT Standard' }] } }))
    expect(wrapper.emitted('saved')).toHaveLength(1)
    wrapper.unmount()
  })
  it('adds only chosen public groups and retains hidden settings without listing the whole catalog', async () => {
    const hidden = [{ group_id: 4, model: 'private-choice', display_name: 'Private alias', enabled: true }, { group_id: 5, model: 'inactive-choice', display_name: 'Inactive alias', enabled: false }]
    mocks.settings.mockResolvedValue({ config: { version: 4, public_enabled: true, group_probe: { enabled: true, groups: hidden } } })
    mocks.groups.mockResolvedValue([
      { id: 2, name: 'DeepSeek', platform: 'deepseek', status: 'active', is_exclusive: false },
      { id: 3, name: 'Kimi', platform: 'kimi', status: 'active', is_exclusive: false },
      { id: 4, name: 'Private', platform: 'openai', status: 'active', is_exclusive: true },
      { id: 5, name: 'Inactive', platform: 'minimax', status: 'inactive', is_exclusive: false },
    ])
    const wrapper = mountControls()
    await flushPromises()
    expect(wrapper.findAll('[data-status-group]')).toHaveLength(0)
    expect(wrapper.get('[data-testid="status-groups-empty"]').exists()).toBe(true)
    expect(wrapper.text()).not.toMatch(/Private|Inactive|Representative|代表/)
    expect(wrapper.find('[data-testid="status-save-controls"]').attributes('disabled')).toBeDefined()
    await wrapper.get('#status-add-group').setValue('2')
    await wrapper.get('[data-testid="status-add-group"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-status-group]')).toHaveLength(1)
    expect(wrapper.find('#status-name-3').exists()).toBe(false)
    expect(wrapper.get('#status-add-group').find('option[value="2"]').exists()).toBe(false)
    await wrapper.get('#status-name-2').setValue('DeepSeek Public')
    await wrapper.get('[data-testid="status-save-controls"]').trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({ group_probe: expect.objectContaining({ groups: expect.arrayContaining([...hidden.map(target => ({ ...target, enabled: false, listed: false })), { group_id: 2, model: '', enabled: false, listed: true, display_name: 'DeepSeek Public' }]) }) }))
    wrapper.unmount()
  })
  it('hides unchecked legacy rows, and removes/readds a group without losing its name or enabling checks', async () => {
    mocks.settings.mockResolvedValue({ config: { version: 4, public_enabled: true, group_probe: { enabled: true, groups: [
      { group_id: 6, model: 'new-owner-model', enabled: true, display_name: 'My GPT' },
      { group_id: 29, model: 'paused-choice', enabled: false },
    ] } } })
    mocks.groups.mockResolvedValue([{ id: 6, name: 'GPT', platform: 'openai', status: 'active' }, { id: 29, name: 'Paused', platform: 'anthropic', status: 'active' }])
    const wrapper = mountControls()
    await flushPromises()
    expect(wrapper.findAll('[data-status-group]')).toHaveLength(1)
    await wrapper.get('[data-status-group="6"] button').trigger('click')
    expect(wrapper.findAll('[data-status-group]')).toHaveLength(0)
    await wrapper.get('[data-testid="status-save-controls"]').trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenLastCalledWith(expect.objectContaining({ group_probe: expect.objectContaining({ groups: expect.arrayContaining([{ group_id: 6, model: 'new-owner-model', enabled: false, listed: false, display_name: 'My GPT' }]) }) }))
    await wrapper.get('#status-add-group').setValue('6')
    await wrapper.get('[data-testid="status-add-group"]').trigger('click')
    await flushPromises()
    expect((wrapper.get('#status-name-6').element as HTMLInputElement).value).toBe('My GPT')
    expect((wrapper.get('#probe-model-6').element as HTMLSelectElement).value).toBe('new-owner-model')
    expect((wrapper.get('[data-status-group="6"] input[type="checkbox"]').element as HTMLInputElement).checked).toBe(false)
    wrapper.unmount()
  })
  it('preserves an unavailable model and retries failed group-scoped discovery without falling back', async () => {
    mocks.settings.mockResolvedValue({ config: { version: 4, group_probe: { enabled: true, groups: [{ group_id: 6, model: 'retained-model', enabled: false, listed: true }] } } })
    mocks.groups.mockResolvedValue([{ id: 6, name: 'GPT', platform: 'openai', status: 'active' }])
    mocks.models.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce([])
    const wrapper = mountControls()
    await flushPromises()
    expect(wrapper.get('[data-status-group="6"] [role="alert"]').exists()).toBe(true)
    expect((wrapper.get('#probe-model-6').element as HTMLSelectElement).value).toBe('retained-model')
    const retry = wrapper.findAll('button').find(button => button.text() === 'common.retry')!
    await retry.trigger('click')
    await flushPromises()
    expect(mocks.models).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('channelMonitorV3.probes.modelUnavailable')
    expect(wrapper.get('#probe-model-6').findAll('option')).toHaveLength(2)
    expect(wrapper.get('[data-status-group="6"] input[type="checkbox"]').attributes('disabled')).toBeDefined()
    expect(mocks.update).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
