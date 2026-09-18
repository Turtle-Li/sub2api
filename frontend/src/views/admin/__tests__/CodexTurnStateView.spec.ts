import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CodexTurnStateView from '../CodexTurnStateView.vue'

const { requestCodexPanel, listAccounts } = vi.hoisted(() => ({
  requestCodexPanel: vi.fn(),
  listAccounts: vi.fn(),
}))

vi.mock('@/api/admin/codexTurnState', () => ({ requestCodexPanel }))
vi.mock('@/api/admin/accounts', () => ({
  list: listAccounts,
  accountsAPI: { list: listAccounts },
  default: { list: listAccounts },
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

const PassThroughStub = defineComponent({ template: '<div><slot /></div>' })
const BaseDialogStub = defineComponent({
  props: { show: Boolean, title: String },
  template: '<section v-if="show" data-test="account-picker-dialog"><h2>{{ title }}</h2><slot /><slot name="footer" /></section>',
})
const SelectStub = defineComponent({
  props: {
    modelValue: { type: String, default: '' },
    options: { type: Array, default: () => [] },
    loading: Boolean,
  },
  emits: ['update:modelValue', 'search'],
  template: `
    <select
      data-test="account-picker-select"
      :value="modelValue"
      @change="$emit('update:modelValue', $event.target.value)"
    >
      <option value="">select</option>
      <option v-for="option in options" :key="option.value" :value="option.value" :disabled="option.disabled">{{ option.label }}</option>
    </select>
  `,
})
const wrappers: VueWrapper[] = []

function mountView() {
  const wrapper = mount(CodexTurnStateView, {
    global: {
      stubs: {
        AppLayout: PassThroughStub,
        RouterLink: PassThroughStub,
        BaseDialog: BaseDialogStub,
        Select: SelectStub,
      },
    },
  })
  wrappers.push(wrapper)
  return wrapper
}

function dispatchFromPanel(
  iframe: HTMLIFrameElement,
  data: Record<string, unknown>,
  origin = 'null',
  source: MessageEventSource | null = iframe.contentWindow as MessageEventSource | null,
) {
  window.dispatchEvent(new MessageEvent('message', { data, origin, source }))
}

function installPanelWindow(iframe: HTMLIFrameElement) {
  // Real postMessage rejects Vue reactive proxies; exercise the clone boundary.
  const postMessage = vi.fn((message: unknown) => structuredClone(message))
  const contentWindow = { postMessage } as unknown as Window
  Object.defineProperty(iframe, 'contentWindow', { configurable: true, value: contentWindow })
  return { contentWindow, postMessage }
}

describe('CodexTurnStateView panel bridge', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    requestCodexPanel.mockResolvedValue({ accounts: [] })
    listAccounts.mockResolvedValue({ items: [] })
  })

  afterEach(() => {
    for (const wrapper of wrappers.splice(0)) wrapper.unmount()
    vi.restoreAllMocks()
  })

  it('loads the existing panel into an opaque sandbox without putting the admin token into the iframe', () => {
    localStorage.setItem('auth_token', 'test-session-marker')

    const wrapper = mountView()
    const iframe = wrapper.get('iframe')

    expect(iframe.attributes('sandbox')).toBe('allow-scripts')
    expect(iframe.attributes('src')).toBeUndefined()
    expect(iframe.attributes('srcdoc')).toContain('Codex 续期监控')
    expect(iframe.attributes('srcdoc')).not.toContain('test-session-marker')
    expect(iframe.attributes('srcdoc')).not.toContain('id="addId"')
    expect(iframe.attributes('srcdoc')).not.toContain('confirm(')
    expect(iframe.attributes('srcdoc')).toContain('ctsm-account-picker')
    expect(iframe.attributes('srcdoc')).not.toContain('选择账号超时')
    expect(iframe.attributes('srcdoc')).toContain('pendingAccountPickers.set(id, {resolve});')
    expect(iframe.attributes('srcdoc')).toContain('class="model-status-scroll"')
    expect(iframe.attributes('srcdoc')).toContain('.model-status-table { min-width: 1180px;')
    expect(iframe.attributes('srcdoc')).toContain('${esc(label)}</span>')
    expect(iframe.attributes('srcdoc')).not.toMatch(/(?:getJSON|mutate)\([^\n]*['\"]\/api\//)
    expect(iframe.element.contentWindow).not.toBe(window)
  })

  it('accepts requests only from its own opaque iframe origin', async () => {
    const wrapper = mountView()
    const iframe = wrapper.get('iframe').element as HTMLIFrameElement
    const { postMessage } = installPanelWindow(iframe)
    const request = { type: 'ctsm-request', id: 'request-1', method: 'GET', path: 'api/state' }

    dispatchFromPanel(iframe, request, 'null', window)
    dispatchFromPanel(iframe, request, 'https://sub2api.example')
    await flushPromises()
    expect(requestCodexPanel).not.toHaveBeenCalled()

    dispatchFromPanel(iframe, request)
    await flushPromises()

    expect(requestCodexPanel).toHaveBeenCalledWith('GET', 'api/state', undefined)
    expect(postMessage).toHaveBeenCalledWith({
      type: 'ctsm-result',
      id: 'request-1',
      ok: true,
      data: { accounts: [] },
    }, '*')
  })

  it('uses the parent selector for active OpenAI OAuth-compatible accounts and returns only the chosen account data', async () => {
    listAccounts
      .mockResolvedValueOnce({
        items: [
          { id: 17, name: '研发 账号 "A"', platform: 'openai', type: 'oauth', status: 'active' },
          { id: 19, name: 'Shared OAuth account', platform: 'openai', type: 'oauth', status: 'active' },
          { id: 21, name: 'invalid\u0007name', platform: 'openai', type: 'oauth', status: 'active' },
        ],
      })
      .mockResolvedValueOnce({
        items: [
          { id: 18, name: 'Setup token account', platform: 'openai', type: 'setup-token', status: 'active' },
          { id: 19, name: 'Duplicate setup token account', platform: 'openai', type: 'setup-token', status: 'active' },
        ],
      })
    const wrapper = mountView()
    const iframe = wrapper.get('iframe').element as HTMLIFrameElement
    const { postMessage } = installPanelWindow(iframe)

    dispatchFromPanel(iframe, { type: 'ctsm-account-picker', id: 'picker-1' })
    await flushPromises()

    expect(listAccounts).toHaveBeenNthCalledWith(
      1,
      1,
      50,
      { platform: 'openai', type: 'oauth', status: 'active', lite: 'true' },
      { signal: expect.any(AbortSignal) },
    )
    expect(listAccounts).toHaveBeenNthCalledWith(
      2,
      1,
      50,
      { platform: 'openai', type: 'setup-token', status: 'active', lite: 'true' },
      { signal: expect.any(AbortSignal) },
    )
    expect(listAccounts.mock.calls[0][3]?.signal).toBe(listAccounts.mock.calls[1][3]?.signal)
    const dialog = wrapper.get('[data-test="account-picker-dialog"]')
    expect(dialog.text()).toContain('研发 账号 "A"')
    expect(dialog.text()).toContain('Setup token account')
    expect(dialog.text()).not.toContain('#17')
    expect(dialog.text()).toContain('admin.codexTurnState.accountPicker.incompatibleHint')

    const options = dialog.findAll('option')
    expect(options.find((option) => option.attributes('value') === '17')?.attributes('disabled')).toBeUndefined()
    expect(options.find((option) => option.attributes('value') === '18')?.attributes('disabled')).toBeUndefined()
    expect(options.find((option) => option.attributes('value') === '19')?.text()).toBe('Shared OAuth account')
    expect(options.filter((option) => option.attributes('value') === '19')).toHaveLength(1)
    expect(options.find((option) => option.attributes('value') === '21')?.attributes('disabled')).toBeDefined()

    await dialog.get('[data-test="account-picker-select"]').setValue('18')
    const confirm = dialog.findAll('button').find((button) => button.text().includes('accountPicker.confirm'))
    expect(confirm).toBeDefined()
    await confirm!.trigger('click')

    expect(postMessage).toHaveBeenCalledWith({
      type: 'ctsm-account-picked',
      id: 'picker-1',
      account: { id: 18, name: 'Setup token account' },
    }, '*')
  })

  it('keeps the selected account available when a remote account search fails', async () => {
    listAccounts
      .mockResolvedValueOnce({ items: [{ id: 17, name: '研发账号', platform: 'openai', type: 'oauth', status: 'active' }] })
      .mockResolvedValueOnce({ items: [] })
      .mockRejectedValueOnce(new Error('upstream response must stay out of the dialog'))
      .mockResolvedValueOnce({ items: [] })
    const wrapper = mountView()
    const iframe = wrapper.get('iframe').element as HTMLIFrameElement
    const { postMessage } = installPanelWindow(iframe)

    dispatchFromPanel(iframe, { type: 'ctsm-account-picker', id: 'picker-2' })
    await flushPromises()
    const dialog = wrapper.get('[data-test="account-picker-dialog"]')
    await dialog.get('[data-test="account-picker-select"]').setValue('17')

    wrapper.findComponent(SelectStub).vm.$emit('search', 'missing')
    await flushPromises()

    expect(listAccounts).toHaveBeenNthCalledWith(
      3,
      1,
      50,
      { platform: 'openai', type: 'oauth', status: 'active', lite: 'true', search: 'missing' },
      { signal: expect.any(AbortSignal) },
    )
    expect(listAccounts).toHaveBeenNthCalledWith(
      4,
      1,
      50,
      { platform: 'openai', type: 'setup-token', status: 'active', lite: 'true', search: 'missing' },
      { signal: expect.any(AbortSignal) },
    )
    expect(dialog.text()).toContain('admin.codexTurnState.accountPicker.loadError')
    expect(dialog.find('[data-test="account-picker-select"]').element.value).toBe('17')

    const confirm = dialog.findAll('button').find((button) => button.text().includes('accountPicker.confirm'))
    await confirm!.trigger('click')
    expect(postMessage).toHaveBeenCalledWith({
      type: 'ctsm-account-picked',
      id: 'picker-2',
      account: { id: 17, name: '研发账号' },
    }, '*')
  })

  it('returns a generic unavailable result when the authenticated API bridge fails', async () => {
    requestCodexPanel.mockRejectedValue(new Error('Bearer production-token must not reach the panel'))
    const wrapper = mountView()
    const iframe = wrapper.get('iframe').element as HTMLIFrameElement
    const { postMessage } = installPanelWindow(iframe)

    dispatchFromPanel(iframe, {
      type: 'ctsm-request',
      id: 'request-2',
      method: 'POST',
      path: 'api/probe',
      body: { account_id: 7, model: 'gpt-6-astra' },
    })
    await flushPromises()

    expect(postMessage).toHaveBeenCalledWith({
      type: 'ctsm-result',
      id: 'request-2',
      ok: false,
      error: 'admin.codexTurnState.unavailable',
    }, '*')
    expect(JSON.stringify(postMessage.mock.calls)).not.toContain('production-token')
  })

  it('registers its bridge listener on mount and removes that exact listener on unmount', async () => {
    const addListener = vi.spyOn(window, 'addEventListener')
    const removeListener = vi.spyOn(window, 'removeEventListener')

    const wrapper = mountView()
    const iframe = wrapper.get('iframe').element as HTMLIFrameElement
    installPanelWindow(iframe)
    const added = addListener.mock.calls.find(([type]) => type === 'message')
    expect(added).toBeDefined()
    const handler = added![1]

    wrapper.unmount()

    expect(removeListener).toHaveBeenCalledWith('message', handler)
    dispatchFromPanel(iframe, { type: 'ctsm-request', id: 'request-3', method: 'GET', path: 'api/state' })
    await flushPromises()
    expect(requestCodexPanel).not.toHaveBeenCalled()
  })
})
