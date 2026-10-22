import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PaidFallbackOpsCard from '../PaidFallbackOpsCard.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), report: vi.fn(), showError: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api', () => ({ adminAPI: { settings: {
  getPaidFallbackNotifications: mocks.get, updatePaidFallbackNotifications: mocks.put, getPaidFallbackReport: mocks.report,
} } }))
vi.mock('@/stores', () => ({ useAppStore: () => mocks }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('PaidFallbackOpsCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.get.mockResolvedValue({ enabled: false, reminder_minutes: 30, recovery_quiet_seconds: 60, daily_cost_usd: null })
    mocks.report.mockResolvedValue({ attempts: [], incidents: [] })
    mocks.put.mockImplementation(async value => value)
  })
  it('leaves the threshold unset and performs no write on load', async () => {
    const wrapper = mount(PaidFallbackOpsCard)
    await flushPromises()
    expect((wrapper.get('#paid-daily-cost').element as HTMLInputElement).value).toBe('')
    expect(mocks.put).not.toHaveBeenCalled()
    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.paidFallback.noActivity')
  })
  it('saves a numeric threshold and explicitly clears it to null', async () => {
    const wrapper = mount(PaidFallbackOpsCard)
    await flushPromises()
    await wrapper.get('#paid-daily-cost').setValue('12.5')
    await wrapper.get('[data-testid="paid-notifications-save"]').trigger('click')
    await flushPromises()
    expect(mocks.put).toHaveBeenLastCalledWith({ enabled: false, reminder_minutes: 30, recovery_quiet_seconds: 60, daily_cost_usd: 12.5 })
    await wrapper.get('#paid-daily-cost').setValue('')
    await wrapper.get('[data-testid="paid-notifications-save"]').trigger('click')
    await flushPromises()
    expect(mocks.put).toHaveBeenLastCalledWith({ enabled: false, reminder_minutes: 30, recovery_quiet_seconds: 60, daily_cost_usd: null })
  })
  it('blocks invalid values and retains edits after a failed save', async () => {
    mocks.put.mockRejectedValue(new Error('offline'))
    const wrapper = mount(PaidFallbackOpsCard)
    await flushPromises()
    await wrapper.get('#paid-reminder-minutes').setValue('1')
    expect((wrapper.get('[data-testid="paid-notifications-save"]').element as HTMLButtonElement).disabled).toBe(true)
    await wrapper.get('#paid-reminder-minutes').setValue('15')
    await wrapper.get('[data-testid="paid-notifications-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.paidFallback.unsaved')
    expect((wrapper.get('#paid-reminder-minutes').element as HTMLInputElement).value).toBe('15')
    expect(mocks.showError).toHaveBeenCalledOnce()
  })
  it('requires a successful load before settings can be saved and permits retry', async () => {
    mocks.get.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mount(PaidFallbackOpsCard)
    await flushPromises()
    expect(wrapper.find('[data-testid="paid-notifications-save"]').exists()).toBe(false)
    expect(mocks.put).not.toHaveBeenCalled()
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="paid-notifications-save"]').exists()).toBe(true)
    expect(mocks.put).not.toHaveBeenCalled()
  })
})
