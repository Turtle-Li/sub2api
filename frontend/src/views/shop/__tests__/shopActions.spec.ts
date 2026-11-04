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

describe('payment action administration', () => {
  it('binds another product to a registered version without changing its price or duration', async () => {
    const wrapper = mount(ShopProductEditor, { props: { product, busy: false, integrations: [integration] } })
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
