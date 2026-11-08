import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import ScheduledTestsPanel from '../ScheduledTestsPanel.vue'
import Input from '@/components/common/Input.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import type { ScheduledTestPlan } from '@/types'

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => params ? `${key} ${JSON.stringify(params)}` : key }),
}))
const { listByAccount, create, update, showError } = vi.hoisted(() => ({
  listByAccount: vi.fn(), create: vi.fn(), update: vi.fn(), showError: vi.fn(),
}))
vi.mock('@/api/admin', () => ({ adminAPI: { scheduledTests: { listByAccount, create, update } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn() }) }))
enableAutoUnmount(afterEach)

const existing: ScheduledTestPlan = {
  id: 7, account_id: 1, model_id: 'small-model', cron_expression: '0 * * * *',
  enabled: true, max_results: 50, auto_recover: false, idle_hours: 72,
  last_activity_at: '2026-11-06T00:00:00Z', last_success_at: null,
  last_run_at: null, next_run_at: '2026-11-08T01:00:00Z',
  created_at: '2026-11-01T00:00:00Z', updated_at: '2026-11-01T00:00:00Z',
}

async function openPanel(plans: ScheduledTestPlan[] = []) {
  listByAccount.mockResolvedValue(plans)
  const wrapper = shallowMount(ScheduledTestsPanel, {
    props: { show: false, accountId: 1, modelOptions: [{ value: 'small-model', label: 'small-model' }] },
    global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } },
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

type Panel = Awaited<ReturnType<typeof openPanel>>
function idleToggle(wrapper: Panel) {
  return wrapper.findAllComponents(Toggle).find(toggle => toggle.attributes('aria-label') === 'admin.scheduledTests.idleOnly')!
}
async function save(wrapper: Panel) {
  await wrapper.findAll('button').find(button => button.text() === 'common.save')!.trigger('click')
  await flushPromises()
}
async function addIdlePlan(wrapper: Panel) {
  await wrapper.findAll('button').find(button => button.text() === 'admin.scheduledTests.addPlan')!.trigger('click')
  wrapper.findComponent(Select).vm.$emit('update:modelValue', 'small-model')
  idleToggle(wrapper).vm.$emit('update:modelValue', true)
  await flushPromises()
}

describe('account idle keep-alive configuration', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    create.mockResolvedValue(existing)
    update.mockImplementation(async (_id, patch) => ({ ...existing, ...patch }))
  })

  it('opts in per account with 48 hours and an hourly check by default', async () => {
    const wrapper = await openPanel()
    await addIdlePlan(wrapper)
    const hours = wrapper.findAllComponents(Input).find(input => input.props('id') === 'new-keepalive-hours')!
    expect(hours.props('modelValue')).toBe('48')
    await save(wrapper)
    expect(create).toHaveBeenCalledWith(expect.objectContaining({ account_id: 1, model_id: 'small-model', idle_hours: 48, cron_expression: '0 * * * *' }))
  })

  it.each(['', '0', '-1', '1.5', '8761'])('rejects invalid idle hours %s before saving', async value => {
    const wrapper = await openPanel()
    await addIdlePlan(wrapper)
    wrapper.findAllComponents(Input).find(input => input.props('id') === 'new-keepalive-hours')!.vm.$emit('update:modelValue', value)
    await save(wrapper)
    expect(create).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.scheduledTests.invalidIdleHours')
  })

  it('preserves a custom threshold in edit and can turn idle-only mode off', async () => {
    const wrapper = await openPanel([existing])
    await wrapper.find('button[title="admin.scheduledTests.editPlan"]').trigger('click')
    const hours = wrapper.findAllComponents(Input).find(input => input.props('id') === 'edit-keepalive-hours')!
    expect(hours.props('modelValue')).toBe('72')
    hours.vm.$emit('update:modelValue', '36')
    await save(wrapper)
    expect(update).toHaveBeenLastCalledWith(7, expect.objectContaining({ idle_hours: 36 }))
    await wrapper.find('button[title="admin.scheduledTests.editPlan"]').trigger('click')
    idleToggle(wrapper).vm.$emit('update:modelValue', false)
    await save(wrapper)
    expect(update).toHaveBeenLastCalledWith(7, expect.objectContaining({ idle_hours: 0 }))
  })

  it('shows account activity, elapsed idle hours and the next check separately', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(Date.parse('2026-11-08T00:00:00Z'))
    const wrapper = await openPanel([existing])
    expect(wrapper.text()).toContain('admin.scheduledTests.lastActivity')
    expect(wrapper.text()).toContain('admin.scheduledTests.idleDuration {"hours":48}')
    expect(wrapper.text()).toContain('admin.scheduledTests.nextCheck')
    expect(wrapper.text()).toContain('admin.scheduledTests.idleBadge {"hours":72}')
    vi.restoreAllMocks()
  })

  it('leaves an ordinary schedule unconditional when idle mode is off', async () => {
    const wrapper = await openPanel()
    await addIdlePlan(wrapper)
    idleToggle(wrapper).vm.$emit('update:modelValue', false)
    await flushPromises()
    expect(wrapper.text()).toContain('admin.scheduledTests.regularModeHelp')
    await save(wrapper)
    expect(create).toHaveBeenCalledWith(expect.objectContaining({ idle_hours: 0 }))
  })

  it('clears an unsaved keep-alive opt-in when the account dialog closes', async () => {
    const wrapper = await openPanel()
    await addIdlePlan(wrapper)
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true, accountId: 2 })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'admin.scheduledTests.addPlan')!.trigger('click')
    expect(idleToggle(wrapper).props('modelValue')).toBe(false)
    expect(wrapper.findAllComponents(Input).some(input => input.props('id') === 'new-keepalive-hours')).toBe(false)
  })
})
