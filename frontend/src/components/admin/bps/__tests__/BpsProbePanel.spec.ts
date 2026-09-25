import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import BpsProbePanel from '../BpsProbePanel.vue'

const { listProbes, createProbes, getProbe, deleteProbe, deleteAllProbes, listAccounts, showSuccess, showError, openHtmlPreviewWindow } = vi.hoisted(() => ({
  listProbes: vi.fn(),
  createProbes: vi.fn(),
  getProbe: vi.fn(),
  deleteProbe: vi.fn(),
  deleteAllProbes: vi.fn(),
  listAccounts: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  openHtmlPreviewWindow: vi.fn(),
}))

vi.mock('@/api/admin/bpsUpstream', () => ({ listProbes, createProbes, getProbe, deleteProbe, deleteAllProbes }))
vi.mock('@/api/admin/accounts', () => ({ list: listAccounts }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('@/utils/htmlPreview', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/utils/htmlPreview')>()),
  openHtmlPreviewWindow,
}))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

const SelectStub = defineComponent({
  props: { modelValue: { type: String, default: null }, options: { type: Array, default: () => [] } },
  emits: ['update:modelValue', 'search'],
  template: `
    <select :value="modelValue" @change="$emit('update:modelValue', $event.target.value)">
      <option value="">select</option>
      <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
    </select>
  `,
})
const DialogStub = defineComponent({
  props: { show: Boolean, title: { type: String, default: '' } },
  emits: ['close'],
  template: '<div v-if="show" data-test="dialog"><slot /></div>',
})

function probe(overrides: Record<string, unknown> = {}) {
  return {
    id: 1, batch_id: 'b', account_id: 69, account_name: 'cashtech', path: 'bps', model: 'gpt-5.6-terra',
    effort: 'high', applied_effort: 'high', prompt: 'q', status: 'succeeded', content_preview: 'answer',
    content_length: 6, error_message: '', input_tokens: 1, output_tokens: 2, reasoning_tokens: 3, duration_ms: 1500,
    created_at: new Date().toISOString(),
    ...overrides,
  }
}

const wrappers: VueWrapper[] = []

async function mountPanel() {
  const wrapper = mount(BpsProbePanel, {
    props: { listedAccounts: [{ id: 69, name: 'cashtech' }], models: ['gpt-6-astra', 'gpt-5.6-terra'] },
    global: { stubs: { Select: SelectStub, BaseDialog: DialogStub } },
  })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}

describe('BpsProbePanel', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    listProbes.mockResolvedValue([])
    createProbes.mockResolvedValue([])
    deleteProbe.mockResolvedValue(undefined)
    deleteAllProbes.mockResolvedValue(2)
    listAccounts.mockResolvedValue({ items: [{ id: 69, name: 'cashtech' }, { id: 90, name: 'backup' }] })
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  afterEach(() => {
    wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
    vi.useRealTimers()
    vi.clearAllMocks()
    vi.restoreAllMocks()
  })

  it('submits selected accounts, both paths and a custom prompt', async () => {
    const wrapper = await mountPanel()
    expect(wrapper.get('[data-test="bps-probe-run"]').attributes('disabled')).toBeDefined()

    await wrapper.get('[data-test="bps-probe-account-69"]').setValue(true)
    await wrapper.get('[data-test="bps-probe-extra-picker"]').setValue('90')
    await wrapper.get('[data-test="bps-probe-extra-add"]').trigger('click')
    await wrapper.get('[data-test="bps-probe-effort"]').setValue('xhigh')
    await wrapper.get('[data-test="bps-probe-prompt"]').setValue('  1+1?  ')
    await wrapper.get('[data-test="bps-probe-run"]').trigger('click')
    await flushPromises()

    expect(createProbes).toHaveBeenCalledWith({
      account_ids: [69, 90], paths: ['bps', 'native'], model: 'gpt-5.6-terra', effort: 'xhigh', prompt: '1+1?',
    })
    expect(listProbes).toHaveBeenCalledTimes(2)
  })

  it('polls while tests are pending and stops when done', async () => {
    listProbes.mockResolvedValueOnce([probe({ status: 'running' })]).mockResolvedValueOnce([probe()])
    const wrapper = await mountPanel()
    expect(wrapper.get('[data-test="bps-probe-status-1"]').text()).toBe('admin.bpsUpstream.probe.status.running')

    vi.advanceTimersByTime(3000)
    await flushPromises()
    expect(wrapper.get('[data-test="bps-probe-status-1"]').text()).toBe('admin.bpsUpstream.probe.status.succeeded')
    vi.advanceTimersByTime(10_000)
    await flushPromises()
    expect(listProbes).toHaveBeenCalledTimes(2)
  })

  it('shows content, previews HTML and deletes results', async () => {
    listProbes.mockResolvedValue([probe(), probe({ id: 2, path: 'native' })])
    getProbe.mockResolvedValue(probe({ content: 'Here:\n```html\n<html><body>hi</body></html>\n```' }))
    openHtmlPreviewWindow.mockReturnValue(false)
    const wrapper = await mountPanel()

    await wrapper.get('[data-test="bps-probe-view-1"]').trigger('click')
    await flushPromises()
    expect(getProbe).toHaveBeenCalledWith(1)
    expect(wrapper.get('[data-test="bps-probe-inline-html"]').attributes('srcdoc')).toBe('<html><body>hi</body></html>')
    await wrapper.get('[data-test="bps-probe-open-html"]').trigger('click')
    expect(openHtmlPreviewWindow).toHaveBeenCalledWith('<html><body>hi</body></html>', 'probe #1')
    expect(showError).toHaveBeenCalledWith('admin.bpsUpstream.probe.popupBlocked')

    await wrapper.get('[data-test="bps-probe-delete-1"]').trigger('click')
    await flushPromises()
    expect(deleteProbe).toHaveBeenCalledWith(1)
    expect(wrapper.findAll('[data-test="bps-probe-row"]')).toHaveLength(1)
    expect(wrapper.find('[data-test="dialog"]').exists()).toBe(false)

    await wrapper.get('[data-test="bps-probe-clear"]').trigger('click')
    await flushPromises()
    expect(deleteAllProbes).toHaveBeenCalled()
    expect(showSuccess).toHaveBeenCalledWith('admin.bpsUpstream.probe.cleared')
  })
})
