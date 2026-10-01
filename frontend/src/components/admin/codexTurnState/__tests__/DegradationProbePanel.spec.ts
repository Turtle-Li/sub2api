import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import DegradationProbePanel from '../DegradationProbePanel.vue'
import type { PinnedAccount } from '../types'

const { listProbes, createProbes, getProbe, deleteProbe, deleteAllProbes, showSuccess, showError, openHtmlPreviewWindow } = vi.hoisted(() => ({
  listProbes: vi.fn(),
  createProbes: vi.fn(),
  getProbe: vi.fn(),
  deleteProbe: vi.fn(),
  deleteAllProbes: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  openHtmlPreviewWindow: vi.fn(),
}))

vi.mock('@/api/admin/codexDegradationProbe', () => ({
  NO_PINNED_STATE_CODE: 'CODEX_DEGRADATION_PROBE_NO_PINNED_STATE',
  listProbes,
  createProbes,
  getProbe,
  deleteProbe,
  deleteAllProbes,
}))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('@/utils/htmlPreview', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/utils/htmlPreview')>()),
  openHtmlPreviewWindow,
}))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

const DialogStub = defineComponent({
  props: { show: Boolean, title: { type: String, default: '' } },
  emits: ['close'],
  template: '<div v-if="show" data-test="dialog"><slot /></div>',
})

const PINNED: PinnedAccount[] = [
  { id: 69, name: 'cashtech', models: ['gpt-6-astra', 'gpt-5.6-terra'] },
  { id: 90, name: 'backup', models: ['gpt-5.6-terra'] },
]

function probe(overrides: Record<string, unknown> = {}) {
  return {
    id: 1, batch_id: 'b', account_id: 69, account_name: 'cashtech', path: 'turn_state', model: 'gpt-6-astra',
    effort: 'high', applied_effort: 'high', prompt: 'q', status: 'succeeded', content_preview: 'answer',
    content_length: 6, error_message: '', input_tokens: 1, output_tokens: 2, reasoning_tokens: 3, duration_ms: 1500,
    created_at: new Date().toISOString(),
    ...overrides,
  }
}

const wrappers: VueWrapper[] = []

async function mountPanel(pinnedAccounts: PinnedAccount[] = PINNED) {
  const wrapper = mount(DegradationProbePanel, {
    props: { pinnedAccounts },
    global: { stubs: { BaseDialog: DialogStub } },
  })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}

describe('DegradationProbePanel', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    listProbes.mockResolvedValue([])
    createProbes.mockResolvedValue([])
    deleteProbe.mockResolvedValue(undefined)
    deleteAllProbes.mockResolvedValue(2)
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  afterEach(() => {
    wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
    vi.useRealTimers()
    vi.restoreAllMocks()
    vi.clearAllMocks()
  })

  it('defaults to the preferred model, medium effort and the pelican prompt', async () => {
    const wrapper = await mountPanel()

    const model = wrapper.get<HTMLSelectElement>('[data-test="degradation-probe-model"]')
    expect(model.element.value).toBe('gpt-6-astra')
    expect(model.findAll('option').map((option) => option.text())).toEqual(['gpt-5.6-terra', 'gpt-6-astra'])
    expect(wrapper.get<HTMLSelectElement>('[data-test="degradation-probe-effort"]').element.value).toBe('medium')
    expect(wrapper.get<HTMLTextAreaElement>('[data-test="degradation-probe-prompt"]').element.value)
      .toBe('admin.codexTurnState.probe.presets.pelican.text')
    expect(wrapper.get('[data-test="degradation-probe-run"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).not.toMatch(/bps/i)
  })

  it('lists only accounts holding a valid ticket for the selected model', async () => {
    const wrapper = await mountPanel()

    expect(wrapper.find('[data-test="degradation-probe-account-69"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="degradation-probe-account-90"]').exists()).toBe(false)

    await wrapper.get('[data-test="degradation-probe-model"]').setValue('gpt-5.6-terra')
    expect(wrapper.find('[data-test="degradation-probe-account-69"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="degradation-probe-account-90"]').exists()).toBe(true)
  })

  it('drops selected accounts that lose their ticket', async () => {
    const wrapper = await mountPanel()
    await wrapper.get('[data-test="degradation-probe-account-69"]').setValue(true)
    expect(wrapper.get('[data-test="degradation-probe-run"]').attributes('disabled')).toBeUndefined()

    await wrapper.setProps({ pinnedAccounts: [] })
    expect(wrapper.find('[data-test="degradation-probe-no-accounts"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="degradation-probe-run"]').attributes('disabled')).toBeDefined()
  })

  it('submits selected accounts, model, effort and trimmed prompt', async () => {
    const wrapper = await mountPanel()
    await wrapper.get('[data-test="degradation-probe-model"]').setValue('gpt-5.6-terra')
    await wrapper.get('[data-test="degradation-probe-account-69"]').setValue(true)
    await wrapper.get('[data-test="degradation-probe-account-90"]').setValue(true)
    await wrapper.get('[data-test="degradation-probe-effort"]').setValue('xhigh')
    await wrapper.get('[data-test="degradation-probe-preset-code"]').trigger('click')
    await wrapper.get('[data-test="degradation-probe-prompt"]').setValue('  solve it  ')

    await wrapper.get('[data-test="degradation-probe-run"]').trigger('click')
    await flushPromises()

    expect(createProbes).toHaveBeenCalledWith({ account_ids: [69, 90], model: 'gpt-5.6-terra', effort: 'xhigh', prompt: 'solve it' })
    expect(showSuccess).toHaveBeenCalledWith('admin.codexTurnState.probe.started')
    expect(listProbes).toHaveBeenCalledTimes(2)
  })

  it('shows a friendly message when the account no longer holds a pinned ticket', async () => {
    createProbes.mockRejectedValueOnce({ status: 400, code: 'CODEX_DEGRADATION_PROBE_NO_PINNED_STATE', message: 'no pinned state' })
    const wrapper = await mountPanel()
    await wrapper.get('[data-test="degradation-probe-account-69"]').setValue(true)

    await wrapper.get('[data-test="degradation-probe-run"]').trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.codexTurnState.probe.noPinnedState')
  })

  it('passes through other submit errors', async () => {
    createProbes.mockRejectedValueOnce({ status: 400, message: 'prompt too long' })
    const wrapper = await mountPanel()
    await wrapper.get('[data-test="degradation-probe-account-69"]').setValue(true)

    await wrapper.get('[data-test="degradation-probe-run"]').trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('prompt too long')
  })

  it('polls while results are pending and stops when they finish', async () => {
    listProbes
      .mockResolvedValueOnce([probe({ status: 'running', content_length: 0, content_preview: '' })])
      .mockResolvedValueOnce([probe()])
    const wrapper = await mountPanel()
    expect(wrapper.get('[data-test="degradation-probe-status-1"]').text()).toBe('admin.codexTurnState.probe.status.running')

    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()
    expect(listProbes).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="degradation-probe-status-1"]').text()).toBe('admin.codexTurnState.probe.status.succeeded')

    await vi.advanceTimersByTimeAsync(6000)
    expect(listProbes).toHaveBeenCalledTimes(2)
  })

  it('opens the detail with HTML preview and deletes results', async () => {
    const html = '<!doctype html><html><body>pelican</body></html>'
    listProbes.mockResolvedValue([probe()])
    getProbe.mockResolvedValue(probe({ content: `\`\`\`html\n${html}\n\`\`\`` }))
    openHtmlPreviewWindow.mockReturnValueOnce(false)
    const wrapper = await mountPanel()

    await wrapper.get('[data-test="degradation-probe-view-1"]').trigger('click')
    await flushPromises()
    expect(getProbe).toHaveBeenCalledWith(1)
    expect(wrapper.find('[data-test="degradation-probe-detail"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="degradation-probe-inline-html"]').attributes('sandbox')).toBe('allow-scripts allow-forms allow-modals')

    await wrapper.get('[data-test="degradation-probe-open-html"]').trigger('click')
    expect(openHtmlPreviewWindow).toHaveBeenCalledWith(expect.stringContaining('pelican'), 'probe #1')
    expect(showError).toHaveBeenCalledWith('admin.codexTurnState.probe.popupBlocked')

    await wrapper.get('[data-test="degradation-probe-delete-1"]').trigger('click')
    await flushPromises()
    expect(deleteProbe).toHaveBeenCalledWith(1)
    expect(wrapper.find('[data-test="degradation-probe-detail"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test="degradation-probe-row"]')).toHaveLength(0)
  })

  it('clears all results after confirmation', async () => {
    listProbes.mockResolvedValueOnce([probe(), probe({ id: 2 })]).mockResolvedValueOnce([])
    const wrapper = await mountPanel()

    await wrapper.get('[data-test="degradation-probe-clear"]').trigger('click')
    await flushPromises()

    expect(window.confirm).toHaveBeenCalled()
    expect(deleteAllProbes).toHaveBeenCalled()
    expect(showSuccess).toHaveBeenCalledWith('admin.codexTurnState.probe.cleared')
    expect(wrapper.findAll('[data-test="degradation-probe-row"]')).toHaveLength(0)
  })
})
