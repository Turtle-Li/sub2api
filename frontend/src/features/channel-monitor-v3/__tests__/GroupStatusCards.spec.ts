import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import type { ModelStatusGroup } from '@/api/modelStatus'

const mocks = vi.hoisted(() => ({
  status: vi.fn(), settings: vi.fn(), update: vi.fn(), groups: vi.fn(),
  auth: { isAdmin: false },
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, locale: ref('zh-CN') }) }))
vi.mock('@/api/modelStatus', () => ({ getModelStatus: mocks.status }))
vi.mock('@/api/channelMonitorV3', () => ({ getSettings: mocks.settings, updateConfig: mocks.update }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getAll: mocks.groups } } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => mocks.auth }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ fetchPublicSettings: vi.fn() }) }))
import GroupStatusCard from '../GroupStatusCard.vue'
import GroupStatusDashboard from '../GroupStatusDashboard.vue'
import GroupStatusControls from '../GroupStatusControls.vue'

const group = (overrides: Record<string, unknown> = {}): ModelStatusGroup => ({
  id: 6, name: 'GPT 按量', platform: 'openai',
  availability: { status: 'operational', source: 'probe', checked_at: '2026-11-07T09:10:00Z', probe_enabled: true,
    probe_model: 'owner-selected', request_success_rate: 99.9, probe_success_rate: 100, cache_read_rate: 97.2, ttft_p50_ms: 3000,
    request_sample_state: 'ready', probe_sample_state: 'ready', min_requests: 20, window_minutes: 90,
    cells: Array.from({ length: 18 }, (_, i) => ({ start: `2026-11-07T09:${String(i).padStart(2, '0')}:00Z`, status: i < 3 ? 'unknown' : 'operational', source: i < 3 ? 'none' : 'requests' })), ...overrides },
})
beforeEach(() => {
  vi.clearAllMocks()
  mocks.auth.isAdmin = false
  mocks.status.mockResolvedValue({ groups: [group()] })
})
describe('status group cards', () => {
  it('shows cache share, request success and actual first-token P50 without prices', () => {
    const wrapper = mount(GroupStatusCard, { props: { group: group() } })
    expect(wrapper.get('[data-metric="cache"]').text()).toBe('97.2%')
    expect(wrapper.get('[data-metric="success"]').text()).toBe('99.9%')
    expect(wrapper.get('[data-metric="ttft"]').text()).toBe('3.0s')
    expect(wrapper.findAll('.status-timeline button')).toHaveLength(18)
    expect(wrapper.find('table').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('$')
    wrapper.unmount()
  })
  it('does not publish small-sample numerics, including probe percentages', () => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({ request_sample_state: 'insufficient', probe_sample_state: 'insufficient', request_success_rate: 66.7 }) } })
    expect(wrapper.findAll('dd').map(x => x.text())).toEqual(['—', '—', '—'])
    expect(wrapper.text()).not.toMatch(/66\.7%|100\.0%/)
    expect(wrapper.text()).toContain('channelMonitorV3.cards.insufficient')
    wrapper.unmount()
  })
  it('never uses a synthetic check as request latency or cache data', () => {
    const wrapper = mount(GroupStatusCard, { props: { group: group({ request_sample_state: 'none', request_success_rate: null, cache_read_rate: null, ttft_p50_ms: null }) } })
    expect(wrapper.findAll('dd').map(x => x.text())).toEqual(['—', '—', '—'])
    expect(wrapper.text()).toContain('100.0%')
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
    const config = { version: 4, public_enabled: true, min_requests: 1, ignored_error_categories: ['client_cancelled'], footer_note: 'keep', group_probe: { enabled: true, groups: [{ group_id: 6, model: 'old-owner-model', enabled: false }] } }
    mocks.settings.mockResolvedValue({ config })
    mocks.groups.mockResolvedValue([{ id: 6, name: 'GPT', platform: 'openai', status: 'active' }])
    mocks.update.mockImplementation(async next => ({ ...next, version: 5 }))
    const wrapper = mount(GroupStatusControls)
    await flushPromises()
    await wrapper.get('#probe-model-0').setValue('new-owner-model')
    await wrapper.get('[data-testid="status-save-controls"]').trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({ version: 4, footer_note: 'keep', ignored_error_categories: ['client_cancelled'], group_probe: { enabled: true, groups: [{ group_id: 6, model: 'new-owner-model', enabled: false }] } }))
    expect(wrapper.emitted('saved')).toHaveLength(1)
    wrapper.unmount()
  })
})
