import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PaidFallbackSettingsCard from '../PaidFallbackSettingsCard.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), disable: vi.fn(), groups: vi.fn(), keys: vi.fn(), showError: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api', () => ({ adminAPI: {
  settings: { getPaidFallbackSettings: mocks.get, updatePaidFallbackSettings: mocks.put, disablePaidFallback: mocks.disable },
  groups: { getAllIncludingInactive: mocks.groups }, usage: { searchApiKeys: mocks.keys },
} }))
vi.mock('@/stores', () => ({ useAppStore: () => mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('PaidFallbackSettingsCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.get.mockResolvedValue({ enabled: false, group_ids: [7] })
    mocks.groups.mockResolvedValue([{ id: 7, name: 'DS test' }])
    mocks.keys.mockResolvedValue([{ id: 11, name: 'DS smoke' }])
  })
  it('enables a group for every user and preserves unresolved group IDs', async () => {
    mocks.get.mockResolvedValue({ enabled: false, group_ids: [7, 999], api_key_ids: [11, 888] })
    mocks.put.mockImplementation(async value => value)
    const wrapper = mount(PaidFallbackSettingsCard)
    await flushPromises()
    await wrapper.get('[data-testid="paid-fallback-toggle"]').trigger('click')
    await wrapper.get('[data-testid="paid-fallback-save"]').trigger('click')
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledWith({ enabled: true, group_ids: [7, 999] })
    expect(wrapper.text()).toContain('#999')
    expect(wrapper.find('#paid-fallback-key').exists()).toBe(false)
    expect(mocks.keys).not.toHaveBeenCalled()
    expect(wrapper.find('form').exists()).toBe(false)
  })
  it('does not allow enabling without a group', async () => {
    mocks.get.mockResolvedValue({ enabled: false, group_ids: null })
    const wrapper = mount(PaidFallbackSettingsCard)
    await flushPromises()
    await wrapper.get('[data-testid="paid-fallback-toggle"]').trigger('click')
    expect((wrapper.get('[data-testid="paid-fallback-save"]').element as HTMLButtonElement).disabled).toBe(true)
    expect(mocks.put).not.toHaveBeenCalled()
  })
  it('can disable after a load error without overwriting with assumed defaults', async () => {
    mocks.get.mockRejectedValue(new Error('offline'))
    mocks.disable.mockResolvedValue({ enabled: false, group_ids: [7] })
    const wrapper = mount(PaidFallbackSettingsCard)
    await flushPromises()
    expect(wrapper.find('[data-testid="paid-fallback-save"]').exists()).toBe(false)
    await wrapper.get('[data-testid="paid-fallback-disable"]').trigger('click')
    await flushPromises()
    expect(mocks.disable).toHaveBeenCalledOnce()
    expect(mocks.put).not.toHaveBeenCalled()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })
  it('retains unsaved edits on a failed save', async () => {
    mocks.put.mockRejectedValue(new Error('offline'))
    const wrapper = mount(PaidFallbackSettingsCard)
    await flushPromises()
    await wrapper.get('[data-testid="paid-fallback-toggle"]').trigger('click')
    await wrapper.get('[data-testid="paid-fallback-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.paidFallback.unsaved')
    expect(mocks.showError).toHaveBeenCalledOnce()
  })
})
