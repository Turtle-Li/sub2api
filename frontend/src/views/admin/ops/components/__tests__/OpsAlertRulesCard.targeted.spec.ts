import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import { ref } from 'vue'
import type { AlertRule } from '@/api/admin/ops'
import OpsAlertRulesCard from '../OpsAlertRulesCard.vue'
import OpsAlertAccountPicker from '../OpsAlertAccountPicker.vue'

const { listAlertRules, updateAlertRule, createAlertRule, accountsList } = vi.hoisted(() => ({
  listAlertRules: vi.fn(), updateAlertRule: vi.fn(), createAlertRule: vi.fn(), accountsList: vi.fn()
}))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@vueuse/core', () => ({ useMediaQuery: () => ref(true) }))
vi.mock('@/api', () => ({ adminAPI: { groups: { getAll: vi.fn().mockResolvedValue([]) }, accounts: { list: accountsList } } }))
vi.mock('@/api/admin/ops', () => ({ opsAPI: {
  listAlertRules, updateAlertRule, createAlertRule, getNetworkBandwidthSettings: vi.fn().mockResolvedValue(null)
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))

enableAutoUnmount(afterEach)
const savedRule: AlertRule = {
  id: 7, name: 'Targeted', description: 'Description', enabled: true,
  metric_type: 'account_upstream_error', operator: '==', threshold: 8,
  window_minutes: 15, sustained_minutes: 2, cooldown_minutes: 30, severity: 'P1',
  notify_email: true, notify_feishu: true, notify_emails: ['ops@example.invalid'], custom_message: 'Review the quota',
  filters: { account_ids: [248, 258], group_id: 12, platform: 'openai', region: 'east', upstream_status_codes: [400, 402],
    provider_codes: ['insufficient_quota'], error_keywords: ['literal%_keyword'] }
}

function mountCard() {
  return shallowMount(OpsAlertRulesCard, { global: { stubs: {
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' }
  } } })
}

async function openEdit() {
  const wrapper = mountCard()
  await flushPromises()
  expect(wrapper.findComponent(OpsAlertAccountPicker).exists()).toBe(false)
  expect(accountsList).not.toHaveBeenCalled()
  await wrapper.findAll('button').find((button) => button.text() === 'common.edit')!.trigger('click')
  await flushPromises()
  return wrapper
}

async function save(wrapper: ReturnType<typeof mountCard>) {
  await wrapper.findAll('button').find((button) => button.text() === 'common.save')!.trigger('click')
  await flushPromises()
}

describe('targeted ops rule request', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    listAlertRules.mockResolvedValue([savedRule])
    createAlertRule.mockResolvedValue(savedRule)
    updateAlertRule.mockResolvedValue(savedRule)
  })

  it('round-trips the selected IDs, notification fields and non-default metric choices', async () => {
    const wrapper = await openEdit()
    const picker = wrapper.getComponent(OpsAlertAccountPicker)
    expect(picker.props('modelValue')).toEqual([248, 258])
    expect(picker.props()).toMatchObject({ platform: 'openai', groupId: 12 })
    expect((wrapper.get('textarea[id$="-custom-message"]').element as HTMLTextAreaElement).value).toBe('Review the quota')
    expect((wrapper.get('input[id$="-status"]').element as HTMLInputElement).value).toBe('400, 402')
    const window = wrapper.findAllComponents({ name: 'Select' }).find((select) => select.props('modelValue') === 15)
    expect(window?.props('options').map((item: { value: number }) => item.value)).toEqual([1, 5, 15, 60])
    await save(wrapper)
    const { id, ...fields } = savedRule
    expect(updateAlertRule).toHaveBeenCalledWith(id, fields)
  })

  it('sends explicit empty overrides and removes predicates when the metric changes', async () => {
    const wrapper = await openEdit()
    wrapper.getComponent(OpsAlertAccountPicker).vm.$emit('update:modelValue', [])
    const metric = wrapper.findAllComponents({ name: 'Select' }).find((select) => select.props('modelValue') === 'account_upstream_error')!
    metric.vm.$emit('update:modelValue', 'account_error_ratio')
    await wrapper.get('textarea[id$="-emails"]').setValue('')
    await wrapper.get('textarea[id$="-custom-message"]').setValue('')
    const feishu = wrapper.findAll('label').find((label) => label.text().includes('admin.ops.alertRules.form.notifyFeishu'))!
    await feishu.get('input').setValue(false)
    await save(wrapper)
    expect(updateAlertRule).toHaveBeenCalledWith(7, expect.objectContaining({
      metric_type: 'account_error_ratio', window_minutes: 5,
      filters: { group_id: 12, platform: 'openai', region: 'east' }, notify_emails: [], notify_feishu: false, custom_message: ''
    }))
  })

  it('blocks a wide targeted query, then creates one from a selected status', async () => {
    const wrapper = mountCard()
    await flushPromises()
    await wrapper.findAll('button').find((button) => button.text() === 'admin.ops.alertRules.create')!.trigger('click')
    await wrapper.get('input[type="text"]').setValue('Watch upstream payments')
    const metric = wrapper.findAllComponents({ name: 'Select' }).find((select) => select.props('modelValue') === 'error_rate')!
    metric.vm.$emit('update:modelValue', 'account_upstream_error')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.ops.alertRules.validation.upstreamScopeRequired')
    await save(wrapper)
    expect(createAlertRule).not.toHaveBeenCalled()
    await wrapper.get('input[id$="-status"]').setValue('402, 402')
    await save(wrapper)
    expect(createAlertRule).toHaveBeenCalledWith(expect.objectContaining({
      metric_type: 'account_upstream_error', window_minutes: 5,
      filters: { upstream_status_codes: [402] }, notify_feishu: false, notify_emails: []
    }))
  })
})
