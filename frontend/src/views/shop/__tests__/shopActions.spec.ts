import { beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach } from 'vitest'
import type { ShopOrderDetail, ShopProduct } from '@/types/shop'
import ShopProductEditor from '@/components/shop/ShopProductEditor.vue'
import ShopAutomationPanel from '@/components/shop/ShopAutomationPanel.vue'
import ShopDelivery from '@/components/shop/ShopDelivery.vue'
import AdminShopOrderView from '../AdminShopOrderView.vue'
import ShopOrderView from '../ShopOrderView.vue'

const api = vi.hoisted(() => ({ order: vi.fn(), adminOrder: vi.fn(), manageAutomation: vi.fn() }))
const stepUp = vi.hoisted(() => vi.fn((fn: () => Promise<unknown>) => fn()))
vi.mock('@/api/shop', () => ({ shopAPI: api }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { id: '42' } }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run: stepUp, onCancel: vi.fn() }), isStepUpCancelled: () => false }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ contactInfo: '测试客服入口' }) }))

const product: ShopProduct = {
  id: 7, name: '软件授权', category: '软件', summary: '', description: '', price: 3, currency: 'CNY',
  duration_value: 1, duration_unit: 'year', fulfillment_mode: 'account_delivery', input_format: 'text',
  input_url: '', input_instructions: '', guide_key: '', enabled: false, sort_order: 0, revision: 4,
  created_at: '', updated_at: '', automatic_fulfillment: true
}
const integration = { id: 'license', version: 2, adapter: 'http.webhook.v1', enabled: true }
function order(): ShopOrderDetail {
  return { order_id: 42, out_trade_no: 'synthetic-42', product, user_id: 1, pay_amount: 3, currency: 'CNY',
    payment_status: 'PAID', fulfillment_status: 'PROCESSING', revision: 3, input_revision: 0,
    input_account: '', created_at: '2026-11-05T00:00:00Z', paid_at: '2026-11-05T00:01:00Z', status_note: '',
    email_status: 'NOT_SENT', admin_notification_status: 'SENT', can_submit: false, can_start: false,
    can_complete: false, can_reveal_input: false,
    automation: { status: 'REVIEW_REQUIRED', result_kind: 'none', claim_revision: 5, attempts: 2, can_retry: true }
  }
}
const global = { stubs: {
  AppLayout: { template: '<main><slot /></main>' }, RouterLink: { template: '<a><slot /></a>' },
  ShopOrderSummary: true, TotpStepUpDialog: true,
  BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' }
} }
enableAutoUnmount(afterEach)
beforeEach(() => { vi.clearAllMocks() })

describe('buyer automatic delivery updates', () => {
  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.clearAllTimers(); vi.useRealTimers() })

  function pendingOrder(): ShopOrderDetail {
    return { ...order(), automation: { status: 'RUNNING', result_kind: 'none' } }
  }

  it('shows completion without a manual refresh and stops polling', async () => {
    const pending = pendingOrder()
    api.order.mockResolvedValueOnce({ data: pending }).mockResolvedValue({ data: {
      ...pending, fulfillment_status: 'COMPLETED', payment_status: 'COMPLETED', delivery_kind: 'none',
      automation: { status: 'SUCCEEDED', result_kind: 'none' }
    } })
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(wrapper.text()).toContain('订单已完成')
    await vi.advanceTimersByTimeAsync(10000)
    expect(api.order).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('keeps the current order visible after a transient refresh failure and retries', async () => {
    api.order.mockResolvedValueOnce({ data: pendingOrder() }).mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue({ data: { ...pendingOrder(), automation: { status: 'REVIEW_REQUIRED', result_kind: 'none' } } })
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(wrapper.findComponent({ name: 'ShopOrderSummary' }).exists()).toBe(true)
    expect(wrapper.text()).not.toContain('无法加载订单')
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(wrapper.text()).toContain('交付待复核')
    await vi.advanceTimersByTimeAsync(6000)
    expect(api.order).toHaveBeenCalledTimes(3)
    wrapper.unmount()
  })

  it('stops requesting the order when leaving the page', async () => {
    api.order.mockResolvedValue({ data: pendingOrder() })
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(6000)
    expect(api.order).toHaveBeenCalledTimes(1)
  })

  it('coalesces a manual refresh with an in-flight automatic refresh', async () => {
    let finish: (value: { data: ShopOrderDetail }) => void = () => {}
    const response = new Promise<{ data: ShopOrderDetail }>(resolve => { finish = resolve })
    api.order.mockResolvedValueOnce({ data: pendingOrder() }).mockReturnValueOnce(response)
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(2000)
    await wrapper.findAll('button').find(button => button.text() === '刷新状态')!.trigger('click')
    expect(api.order).toHaveBeenCalledTimes(2)
    finish({ data: { ...pendingOrder(), automation: { status: 'REVIEW_REQUIRED', result_kind: 'none' } } })
    await flushPromises()
    expect(wrapper.text()).toContain('交付待复核')
    wrapper.unmount()
  })

  it('does not poll manual fulfillment orders', async () => {
    api.order.mockResolvedValue({ data: { ...pendingOrder(), product: { ...product, automatic_fulfillment: false }, automation: undefined } })
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(6000)
    expect(api.order).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})

describe('payment action administration', () => {
  it('retains an explicitly enabled manual account-copy option', async () => {
    const wrapper = mount(ShopProductEditor, { props: { product: { ...product, automatic_fulfillment: false, delivery_config: { individual_copy: true } }, busy: false } })
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('save')?.[0]?.[0]).toMatchObject({ delivery_config: { individual_copy: true }, payment_actions: [] })
  })
  it.each(['topup', 'automatic'])('clears manual account-copy options when switching to %s', async mode => {
    const wrapper = mount(ShopProductEditor, { props: { product: { ...product, automatic_fulfillment: false, delivery_config: { individual_copy: true } }, busy: false, integrations: [integration] } })
    await wrapper.get('#product-editor-delivery').trigger('click')
    if (mode === 'topup') await wrapper.get('#fulfillment-mode').setValue('session_topup')
    else {
      await wrapper.get('input[value="automatic"]').setValue()
      await wrapper.get('#payment-action').setValue('license@2')
    }
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('save')?.[0]?.[0]).toHaveProperty('delivery_config', undefined)
    expect(wrapper.find('#individual-copy').exists()).toBe(false)
  })
  it.each([false, true])('shows individual-copy controls only when a manual purchase opted in: %s', async individualCopy => {
    api.order.mockResolvedValue({ data: { ...order(), product: { ...product, automatic_fulfillment: false, delivery_config: { individual_copy: individualCopy } }, fulfillment_status: 'COMPLETED', payment_status: 'COMPLETED', delivery_kind: 'text', delivery_content: 'synthetic@example.invalid----synthetic-password', automation: undefined } })
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    expect(wrapper.text().includes('复制账号')).toBe(individualCopy)
    expect(wrapper.text().includes('复制密码')).toBe(individualCopy)
    expect(wrapper.text().includes('复制完整原文')).toBe(!individualCopy)
  })
  it('binds another product to a registered version without changing its price or duration', async () => {
    const wrapper = mount(ShopProductEditor, { props: { product, busy: false, integrations: [integration] } })
    await wrapper.get('#product-editor-delivery').trigger('click')
    await wrapper.get('input[value="automatic"]').setValue()
    await wrapper.get('#payment-action').setValue('license@2')
    await wrapper.get('#payment-action-params').setValue('{"sku":"pro-year"}')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('save')?.[0]?.[0]).toMatchObject({ price: 3, duration_value: 1, duration_unit: 'year',
      payment_actions: [{ binding_key: 'primary-delivery', adapter: 'http.webhook.v1', integration_ref: 'license', integration_version: 2, params: { sku: 'pro-year' } }]
    })
    expect(wrapper.html()).not.toContain('credential_alias')
    expect(wrapper.html()).not.toContain('hooks.example.com')
  })
  it.each(['[]', 'null', '{bad json', '"' + 'x'.repeat(4096) + '"'])('blocks invalid parameters: %s', async value => {
    const wrapper = mount(ShopProductEditor, { props: { product, busy: false, integrations: [integration] } })
    await wrapper.get('#product-editor-delivery').trigger('click')
    await wrapper.get('input[value="automatic"]').setValue()
    await wrapper.get('#payment-action').setValue('license@2')
    await wrapper.get('#payment-action-params').setValue(value)
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('save')).toBeUndefined()
    expect(wrapper.get('[role="alert"]').text()).toContain('JSON 对象')
  })
  it('retries the original task through step-up with its current revision', async () => {
    const detail = order()
    api.adminOrder.mockResolvedValue({ data: detail })
    api.manageAutomation.mockResolvedValue({ data: { ...detail, automation: { ...detail.automation, status: 'PENDING', can_retry: true } } })
    const wrapper = mount(AdminShopOrderView, { global })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '重试原任务')!.trigger('click')
    await flushPromises()
    expect(stepUp).toHaveBeenCalledOnce()
    expect(api.manageAutomation).toHaveBeenCalledWith(42, 'retry', 5)
    expect(wrapper.text()).toContain('原任务已重新排队')
    expect(wrapper.find('#delivery-content').exists()).toBe(false)
  })
  it('requires an explicit revoke confirmation and step-up without offering manual delivery', async () => {
    const detail = order()
    detail.automation = { status: 'SUCCEEDED', result_kind: 'text', claim_revision: 6, can_revoke: true, revoke_required: true }
    api.adminOrder.mockResolvedValue({ data: detail })
    api.manageAutomation.mockResolvedValue({ data: { ...detail, automation: { ...detail.automation, status: 'COMPENSATING', can_revoke: false } } })
    const wrapper = mount(AdminShopOrderView, { global })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '撤销此订单授权')!.trigger('click')
    expect(api.manageAutomation).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('撤销不会自动退款')
    await wrapper.findAll('button').find(button => button.text() === '确认撤销')!.trigger('click')
    await flushPromises()
    expect(stepUp).toHaveBeenCalledOnce()
    expect(api.manageAutomation).toHaveBeenCalledWith(42, 'revoke', 6)
    expect(wrapper.text()).toContain('正在撤销授权')
  })
  it('disables action buttons while busy', () => {
    const wrapper = mount(ShopAutomationPanel, { props: { busy: true, automation: { status: 'REVIEW_REQUIRED', result_kind: 'none', can_retry: true, can_revoke: true } } })
    expect(wrapper.findAll('button').every(button => button.attributes('disabled') !== undefined)).toBe(true)
  })
})

describe('generic automatic delivery for buyers', () => {
  it('shows successful service activation without an empty credential panel or DS limits', async () => {
    api.order.mockResolvedValue({ data: { ...order(), fulfillment_status: 'COMPLETED', payment_status: 'COMPLETED',
      delivery_kind: 'none', automation: { status: 'SUCCEEDED', result_kind: 'none' } } })
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    expect(wrapper.text()).toContain('服务已处理完成')
    expect(wrapper.findComponent(ShopDelivery).exists()).toBe(false)
    expect(wrapper.text()).not.toContain('暂无交付内容')
    expect(wrapper.text()).not.toContain('DeepSeek')
    expect(wrapper.text()).not.toContain('2并发')
    expect(wrapper.find('textarea').exists()).toBe(false)
  })
  it('shows review guidance without internal errors or a manual submission path', async () => {
    api.order.mockResolvedValue({ data: order() })
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    expect(wrapper.text()).toContain('交付待复核')
    expect(wrapper.text()).toContain('请勿重复购买')
    expect(wrapper.text()).not.toContain('credential_alias')
    expect(wrapper.find('textarea').exists()).toBe(false)
  })
})
