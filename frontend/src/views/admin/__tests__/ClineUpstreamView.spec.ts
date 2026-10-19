import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ClineUpstreamView from '../ClineUpstreamView.vue'

const mocks = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  getClineUpstreams: vi.fn(),
  updateClineUpstreams: vi.fn(),
  probeClineUpstreams: vi.fn(),
  validateClineUpstreams: vi.fn(),
  testClineUpstreams: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: { accounts: { list: mocks.listAccounts } }
}))
vi.mock('@/api/admin/clineUpstream', () => ({
  getClineUpstreams: mocks.getClineUpstreams,
  updateClineUpstreams: mocks.updateClineUpstreams,
  probeClineUpstreams: mocks.probeClineUpstreams,
  validateClineUpstreams: mocks.validateClineUpstreams,
  testClineUpstreams: mocks.testClineUpstreams,
  default: {}
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key) })
}))
vi.mock('@/utils/format', () => ({ formatDateTime: (value: unknown) => String(value) }))

const Dialog = defineComponent({
  props: ['show', 'title'],
  template: '<section v-if="show"><h2>{{ title }}</h2><slot /><slot name="footer" /></section>'
})

function mountView() {
  return mount(ClineUpstreamView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        BaseDialog: Dialog,
        Icon: true
      }
    }
  })
}

function stateFixture(overrides: Record<string, unknown> = {}) {
  return {
    account_id: 7,
    account_name: 'cline-main',
    platform: 'cline',
    supported: true,
    base_url: 'https://api.cline.bot/api/v1',
    fetched_at: 1_700_000_000,
    models: [
      {
        model: 'cline-pass/deepseek-v4-flash',
        source: 'subscription',
        pin: { mode: 'off', upstreams: [], exclude: [], sort: '' },
        meta: {
          pipeline: 'planner',
          pinnable: true,
          upstreams: ['alibaba', 'baseten'],
          statuses: { alibaba: { status: 'ok', ms: 120, note: '', checked_at: 1 } },
          last_provider: 'alibaba',
          canonical_slug: 'deepseek/deepseek-v4-flash',
          last_error: '',
          probed_at: 1,
          validated_at: 0
        },
        candidates: [],
        configured: false
      }
    ],
    ...overrides
  }
}

describe('ClineUpstreamView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.listAccounts.mockResolvedValue({
      items: [{ id: 7, name: 'cline-main', platform: 'cline', type: 'apikey' }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    mocks.getClineUpstreams.mockResolvedValue(stateFixture())
    mocks.probeClineUpstreams.mockResolvedValue({ account_id: 7, results: [{ model: 'cline-pass/deepseek-v4-flash', ok: true }] })
    mocks.validateClineUpstreams.mockResolvedValue({ model: 'cline-pass/deepseek-v4-flash', pipeline: 'planner', upstreams: ['alibaba'], statuses: { alibaba: { status: 'ok', ms: 10, note: '', checked_at: 1 } }, summary: { ok: 1 } })
    mocks.testClineUpstreams.mockResolvedValue({ model: 'cline-pass/deepseek-v4-flash', ok: true, ms: 42, pipeline: 'planner', actual_provider: 'alibaba', actual_provider_name: 'alibaba', canonical_slug: '', content: 'OK', targets: ['alibaba'], exclude: [], candidates: ['alibaba'] })
    mocks.updateClineUpstreams.mockResolvedValue(stateFixture())
  })

  it('loads the cline account list and its upstream state', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(mocks.listAccounts).toHaveBeenCalledWith(1, 200, { platform: 'cline' })
    expect(mocks.getClineUpstreams).toHaveBeenCalledWith(7)
    expect(wrapper.find('[data-testid="cline-upstream-table"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('cline-pass/deepseek-v4-flash')
  })

  it('plans one-click vendor pinning and skips what it cannot pin', async () => {
    mocks.getClineUpstreams.mockResolvedValue(
      stateFixture({
        models: [
          {
            model: 'cline-pass/deepseek-v4-pro',
            source: 'subscription',
            pin: { mode: 'off', upstreams: [], exclude: [], sort: '' },
            meta: { pipeline: 'planner', pinnable: true, upstreams: ['alibaba', 'deepseek'], statuses: {}, last_provider: 'deepseek', canonical_slug: 'deepseek/deepseek-v4-pro', last_error: '', harvest_note: '', response_model: '', routing_raw: '', probed_at: 1, validated_at: 0 },
            candidates: [],
            configured: false
          },
          {
            model: 'cline-pass/deepseek-v4.1-flash',
            source: 'subscription',
            pin: { mode: 'off', upstreams: [], exclude: [], sort: '' },
            meta: { pipeline: 'planner', pinnable: false, upstreams: ['openai-compatible-private'], statuses: {}, last_provider: 'openai-compatible-private', canonical_slug: 'private/deepseek-v4p1-flash-contributor', last_error: '', harvest_note: 'single upstream', response_model: '', routing_raw: '', probed_at: 1, validated_at: 0 },
            candidates: [],
            configured: false
          },
          {
            model: 'cline-pass/glm-5.3',
            source: 'subscription',
            pin: { mode: 'off', upstreams: [], exclude: [], sort: '' },
            meta: { pipeline: 'planner', pinnable: true, upstreams: ['baseten'], statuses: {}, last_provider: 'baseten', canonical_slug: 'zai/glm-5.3', last_error: '', harvest_note: '', response_model: '', routing_raw: '', probed_at: 1, validated_at: 0 },
            candidates: [],
            configured: false
          }
        ]
      })
    )
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="cline-upstream-one-click"]').trigger('click')
    await flushPromises()

    const dialog = wrapper.text()
    // deepseek-v4-pro 池里有 deepseek -> 钉住；4.1-flash 单一上游 -> 跳过；glm 池里没有 zai/alibaba -> 跳过
    expect(dialog).toContain('oneClick.summary')
    await wrapper.get('[data-testid="cline-upstream-one-click-apply"]').trigger('click')
    await flushPromises()

    expect(mocks.updateClineUpstreams).toHaveBeenCalledTimes(1)
    const [, pins] = mocks.updateClineUpstreams.mock.calls[0]
    expect(pins['cline-pass/deepseek-v4-pro']).toMatchObject({ mode: 'strict', upstreams: ['deepseek'] })
    expect(pins['cline-pass/deepseek-v4.1-flash']).toBeUndefined()
    expect(pins['cline-pass/glm-5.3']).toBeUndefined()
  })

  it('reports that the pin was not adopted when another channel served it', async () => {
    mocks.testClineUpstreams.mockResolvedValue({
      model: 'cline-pass/deepseek-v4.1-flash',
      ok: false,
      adherence: 'not-adopted',
      error: 'pin not adopted: served by openai-compatible-private',
      ms: 42,
      pipeline: 'planner',
      actual_provider: 'openai-compatible-private',
      actual_provider_name: 'openai-compatible-private',
      canonical_slug: '',
      content: 'OK',
      targets: ['deepseek'],
      exclude: [],
      candidates: ['deepseek']
    })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="cline-upstream-row-cline-pass/deepseek-v4-flash"] button.btn-primary').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="cline-upstream-test"]').trigger('click')
    await flushPromises()

    const adherence = wrapper.get('[data-testid="cline-upstream-adherence"]')
    expect(adherence.text()).toContain('adherence.notAdopted')
    expect(wrapper.text()).toContain('pin not adopted')
  })

  it('lets the operator force a channel name the probe never listed', async () => {
    // 探测清单可能不全：网关对未知渠道名可能静默忽略而不是报错，所以要能强制指定并测试。
    mocks.getClineUpstreams.mockResolvedValue(
      stateFixture({
        models: [
          {
            model: 'cline-pass/deepseek-v4.1-flash',
            source: 'subscription',
            pin: { mode: 'off', upstreams: [], exclude: [], sort: '' },
            meta: { pipeline: 'planner', pinnable: false, upstreams: ['openai-compatible-private'], statuses: {}, last_provider: 'openai-compatible-private', canonical_slug: 'private/deepseek-v4p1-flash-contributor', last_error: '', harvest_note: 'gateway ignored', response_model: '', routing_raw: '', probed_at: 1, validated_at: 0 },
            candidates: [],
            configured: false
          }
        ]
      })
    )
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="cline-upstream-row-cline-pass/deepseek-v4.1-flash"] button.btn-primary').trigger('click')
    await flushPromises()

    // 强制指定 deepseek，并切到严格钉住
    await wrapper.get('[data-testid="cline-upstream-mode-strict"]').trigger('click')
    await wrapper.get('[data-testid="cline-upstream-custom-input"]').setValue('deepseek')
    await wrapper.get('[data-testid="cline-upstream-custom-add"]').trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="cline-upstream-test"]').trigger('click')
    await flushPromises()

    expect(mocks.testClineUpstreams).toHaveBeenCalledWith(
      7,
      expect.objectContaining({ model: 'cline-pass/deepseek-v4.1-flash', mode: 'strict', upstreams: ['deepseek'] })
    )
  })

  it('renders null list fields from the API without crashing', async () => {
    // Go 把 nil slice 序列化成 null；页面必须容忍（曾经抛 Cannot read properties of null）
    mocks.getClineUpstreams.mockResolvedValue(
      stateFixture({
        models: [
          {
            model: 'cline-pass/deepseek-v4-flash',
            source: 'configured',
            pin: { mode: 'off', upstreams: null, exclude: null, sort: '' },
            meta: { pipeline: '', pinnable: false, upstreams: null, statuses: null, last_provider: '', canonical_slug: '', last_error: '', probed_at: 0, validated_at: 0 },
            candidates: null,
            configured: false
          }
        ]
      })
    )
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="cline-upstream-table"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('cline-pass/deepseek-v4-flash')
  })

  it('shows the empty state when no cline account exists', async () => {
    mocks.listAccounts.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 1 })
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="cline-upstream-no-accounts"]').exists()).toBe(true)
    expect(mocks.getClineUpstreams).not.toHaveBeenCalled()
  })

  it('warns when the account cannot be probed', async () => {
    mocks.getClineUpstreams.mockResolvedValue(stateFixture({ supported: false }))
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="cline-upstream-unsupported"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="cline-upstream-probe-all"]').attributes('disabled')).toBeDefined()
  })

  it('probes all models through the account', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="cline-upstream-probe-all"]').trigger('click')
    await flushPromises()

    expect(mocks.probeClineUpstreams).toHaveBeenCalledWith(7)
    expect(mocks.showSuccess).toHaveBeenCalled()
  })

  it('saves a strict pin built in the dialog', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="cline-upstream-row-cline-pass/deepseek-v4-flash"] button.btn-primary').trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="cline-upstream-mode-strict"]').trigger('click')
    const option = wrapper.get('[data-testid="cline-upstream-option-alibaba"] input[type="checkbox"]')
    await option.setValue(true)
    await wrapper.get('[data-testid="cline-upstream-save"]').trigger('click')
    await flushPromises()

    expect(mocks.updateClineUpstreams).toHaveBeenCalledTimes(1)
    const [accountId, pins] = mocks.updateClineUpstreams.mock.calls[0]
    expect(accountId).toBe(7)
    expect(pins['cline-pass/deepseek-v4-flash']).toMatchObject({ mode: 'strict', upstreams: ['alibaba'] })
  })

  it('tests the draft configuration without saving it', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="cline-upstream-row-cline-pass/deepseek-v4-flash"] button.btn-primary').trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="cline-upstream-test"]').trigger('click')
    await flushPromises()

    expect(mocks.testClineUpstreams).toHaveBeenCalledWith(
      7,
      expect.objectContaining({ model: 'cline-pass/deepseek-v4-flash' })
    )
    expect(mocks.updateClineUpstreams).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="cline-upstream-test-result"]').exists()).toBe(true)
  })
})
