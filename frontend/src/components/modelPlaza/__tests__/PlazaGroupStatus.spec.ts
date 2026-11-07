import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import PlazaGroupStatus from '../PlazaGroupStatus.vue'
import type { PlazaGroupAvailability } from '@/api/modelPlaza'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, locale: ref('zh-CN') }) }))
const fixture = (overrides: Partial<PlazaGroupAvailability> = {}): PlazaGroupAvailability => ({
  status: 'operational', source: 'probe', checked_at: '2026-11-07T01:00:00Z', probe_enabled: true,
  probe_model: 'cheap-model', request_success_rate: null, probe_success_rate: 100,
  request_sample_state: 'none', probe_sample_state: 'ready',
  cells: [{ start: '2026-11-07T01:00:00Z', status: 'operational', source: 'probe' }], ...overrides
})

describe('group availability inside the plaza card', () => {
  it('labels a probe percentage separately from real requests', () => {
    const wrapper = mount(PlazaGroupStatus, { props: { status: fixture() } })
    expect(wrapper.text()).toContain('modelPlaza.availability.probeRate')
    expect(wrapper.text()).toContain('100.0%')
    expect(wrapper.text()).toContain('cheap-model')
    expect(wrapper.find('details').exists()).toBe(true)
  })
  it('preserves zero real success instead of replacing it with a green probe rate', () => {
    const wrapper = mount(PlazaGroupStatus, { props: { status: fixture({ request_success_rate: 0, request_sample_state: 'ready' }) } })
    expect(wrapper.find('strong').text()).toBe('0.0%')
    expect(wrapper.text()).toContain('modelPlaza.availability.requestRate')
  })
  it('suppresses any percentage when the server marks its sample insufficient', () => {
    const wrapper = mount(PlazaGroupStatus, { props: { status: fixture({ request_success_rate: 66.7, probe_success_rate: 100, request_sample_state: 'insufficient', probe_sample_state: 'insufficient' }) } })
    expect(wrapper.find('strong').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('66.7%')
    expect(wrapper.text()).not.toContain('100.0%')
  })
  it('shows waiting and disabled states without inventing rates', () => {
    const wrapper = mount(PlazaGroupStatus, { props: { status: fixture({ status: 'unknown', checked_at: null, probe_success_rate: null }) } })
    expect(wrapper.text()).toContain('modelPlaza.availability.waiting')
    expect(wrapper.find('strong').exists()).toBe(false)
    wrapper.unmount()
    const disabled = mount(PlazaGroupStatus, { props: { status: fixture({ status: 'unknown', checked_at: null, probe_enabled: false, probe_success_rate: null }) } })
    expect(disabled.text()).toContain('modelPlaza.availability.probeOff')
    expect(disabled.find('strong').exists()).toBe(false)
  })
})
