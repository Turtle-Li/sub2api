import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { ShopOrderDetail, ShopProduct } from '@/types/shop'
import ShopView from '../ShopView.vue'
import ShopOrderView from '../ShopOrderView.vue'
import AdminShopOrderView from '../AdminShopOrderView.vue'
import ShopDelivery from '@/components/shop/ShopDelivery.vue'
import ShopMarkdown from '@/components/shop/ShopMarkdown.vue'
import ShopProductEditor from '@/components/shop/ShopProductEditor.vue'
import ShopProductView from '../ShopProductView.vue'
import ShopGuideView from '../ShopGuideView.vue'
import ShopOrderSummary from '@/components/shop/ShopOrderSummary.vue'
import { isManualShopPaymentConfirmed } from '@/components/payment/manualShopPayment'
const api = vi.hoisted(() => ({ products: vi.fn(), product: vi.fn(), createOrder: vi.fn(), order: vi.fn(), checkInput: vi.fn(), submitInput: vi.fn(), adminOrder: vi.fn(), action: vi.fn(), revealInput: vi.fn(), retryNotification: vi.fn() }))
const checkoutAPI = vi.hoisted(() => ({ getCheckoutInfo: vi.fn() }))
vi.mock('@/api/shop', () => ({ shopAPI: api }))
vi.mock('@/api/payment', () => ({ paymentAPI: checkoutAPI }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ contactInfo: '测试客服入口' }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAdmin: true }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { id: '42' } }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run: (fn: () => Promise<unknown>) => fn(), onCancel: vi.fn() }), isStepUpCancelled: () => false }))
const product: ShopProduct = { id: 1, name: '测试商品', category: 'Gemini', summary: '', description: '', price: 12, currency: 'CNY', duration_value: 1, duration_unit: 'month', fulfillment_mode: 'session_topup', input_format: 'session_json', input_url: '', input_instructions: '', guide_key: '', enabled: true, sort_order: 0, revision: 1, created_at: '', updated_at: '' }
const order = (): ShopOrderDetail => ({ order_id: 42, out_trade_no: 'shop-test-42', product, user_id: 8, pay_amount: 12, currency: 'CNY', payment_status: 'PAID', fulfillment_status: 'WAITING_INPUT', revision: 3, input_revision: 0, input_account: '', created_at: '2026-11-04T12:00:00Z', status_note: '', email_status: 'NOT_SENT', admin_notification_status: 'SENT', can_submit: true, can_start: false, can_complete: false, can_reveal_input: false })
const global = { stubs: { AppLayout: { template: '<main><slot /></main>' }, RouterLink: { template: '<a><slot /></a>' }, ShopOrderSummary: true, TotpStepUpDialog: true, BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } } }
enableAutoUnmount(afterEach)
beforeEach(() => { vi.clearAllMocks(); localStorage.clear(); checkoutAPI.getCheckoutInfo.mockResolvedValue({ data: { methods: {} } }); api.order.mockResolvedValue({ data: order() }); api.adminOrder.mockResolvedValue({ data: order() }); api.checkInput.mockResolvedValue({ data: { account: 'target@example.com', plan_type: 'free', expires_at: '2099-01-01T00:00:00Z', warnings: [] } }) })
describe('manual shop surfaces', () => {
  it('offers configured payment methods without an available field and submits the selected method', async () => {
    const limit = { currency: 'CNY', fee_rate: 0, daily_limit: 0, single_min: 0.1, single_max: 0.1 }
    checkoutAPI.getCheckoutInfo.mockResolvedValue({ data: { methods: { alipay: limit, wxpay: limit } } })
    api.product.mockResolvedValue({ data: { ...product, price: 0.1 } })
    api.createOrder.mockResolvedValue({ data: { order_id: 42, amount: 0.1, pay_amount: 0.1, payment_type: 'wxpay', expires_at: '2099-01-01T00:00:00Z' } })
    const wrapper = mount(ShopProductView, { global: { stubs: { ...global.stubs, PaymentStatusPanel: true } } })
    await flushPromises()
    expect(wrapper.text()).not.toContain('暂无可用支付方式')
    expect(wrapper.text()).toContain('支付宝')
    expect(wrapper.text()).toContain('微信')
    const wxpay = wrapper.findAll('input[type="radio"]').find(input => input.attributes('value') === 'wxpay')
    expect(wxpay).toBeDefined()
    await wxpay!.setValue(true)
    const buy = wrapper.findAll('button').find(button => button.text() === '购买')!
    expect(buy.attributes('disabled')).toBeUndefined()
    await buy.trigger('click')
    await flushPromises()
    expect(api.createOrder).toHaveBeenCalledWith({ product_id: product.id, product_revision: product.revision, payment_type: 'wxpay' }, expect.any(String))
    expect(wrapper.findComponent({ name: 'PaymentStatusPanel' }).exists()).toBe(true)
  })
  it('shows an order creation failure beside the purchase action', async () => {
    checkoutAPI.getCheckoutInfo.mockResolvedValue({ data: { methods: { alipay: { currency: 'CNY' } } } })
    api.product.mockResolvedValue({ data: product })
    api.createOrder.mockRejectedValueOnce(new Error('synthetic provider outage'))
    const wrapper = mount(ShopProductView, { global: { stubs: { ...global.stubs, PaymentStatusPanel: true } } })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '购买')!.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[role="alert"]')).toHaveLength(1)
    expect(wrapper.get('aside [role="alert"]').text()).toContain('未能创建订单')
    expect(wrapper.findAll('button').find(button => button.text() === '购买')!.attributes('disabled')).toBeUndefined()
  })
  it.each([
    { methods: {}, label: 'unconfigured' },
    { methods: { alipay: { available: false } }, label: 'explicitly disabled' },
    { methods: { alipay: { single_min: 12.01 } }, label: 'below the method minimum' },
    { methods: { alipay: { single_max: 11.99 } }, label: 'above the method maximum' },
    { methods: { alipay: { currency: 'USD' } }, label: 'a non-CNY method' },
    { methods: { stripe: { available: true } }, label: 'an unsupported method' },
  ])('keeps checkout closed for $label methods', async ({ methods }) => {
    checkoutAPI.getCheckoutInfo.mockResolvedValue({ data: { methods } })
    api.product.mockResolvedValue({ data: product })
    const wrapper = mount(ShopProductView, { global: { stubs: { ...global.stubs, PaymentStatusPanel: true } } })
    await flushPromises()
    expect(wrapper.text()).toContain('暂无可用支付方式')
    expect(wrapper.findAll('button').find(button => button.text() === '购买')!.attributes('disabled')).toBeDefined()
    expect(api.createOrder).not.toHaveBeenCalled()
  })
  it('shows an actionable empty catalog without inventing products or prices', async () => {
    api.products.mockResolvedValue({ data: [] })
    const wrapper = mount(ShopView, { global })
    await flushPromises()
    expect(wrapper.text()).toContain('暂无上架商品')
    expect(wrapper.text()).toContain('配置商品')
    expect(wrapper.text()).not.toContain('¥')
    expect(wrapper.text()).not.toContain('5分钟')
  })
  it.each(['', '服务说明：5分钟-2小时操作完成，着急可联系客服。'])('shows the delivery time once in product details, including configured copy: %s', async description => {
    api.product.mockResolvedValue({ data: { ...product, description } })
    const wrapper = mount(ShopProductView, { global: { stubs: { ...global.stubs, PaymentStatusPanel: true } } })
    await flushPromises()
    expect(wrapper.text().match(/5分钟/g)).toHaveLength(1)
    expect(wrapper.text().match(/测试客服入口/g)).toHaveLength(1)
  })
  it('puts the required guide before delivered credentials without repeating the generic notice or guide link', async () => {
    api.order.mockResolvedValue({ data: { ...order(), product: { ...product, fulfillment_mode: 'account_delivery', guide_key: 'chatgpt-plus-account' }, fulfillment_status: 'COMPLETED', payment_status: 'COMPLETED', can_submit: false, delivery_content: '账号：fictional@example.invalid' } })
    const wrapper = mount(ShopOrderView, { global: { stubs: { ...global.stubs, ShopOrderSummary: false, OrderStatusBadge: true } } })
    await flushPromises()
    const link = wrapper.get('a[aria-label="必看：成品号使用指南"]')
    expect(link.attributes('to')).toBe('/shop/help/chatgpt-plus-account')
    expect(wrapper.text()).not.toContain('查看使用教程')
    expect(wrapper.text()).not.toContain('5分钟')
    expect(wrapper.text().indexOf('必看')).toBeLessThan(wrapper.text().indexOf('账号已交付'))
    expect(wrapper.findComponent(ShopOrderSummary).exists()).toBe(true)
  })
  it('renders the HTML guide with all local illustrations, working section targets and only operational external sites', () => {
    const wrapper = mount(ShopGuideView, { global })
    const article = wrapper.get('article')
    expect(article.findAll('img')).toHaveLength(13)
    expect(article.findAll('img').every(img => /^\/shop\/help\/image-\d{2}\.jpg$/.test(img.attributes('src')))).toBe(true)
    for (const link of wrapper.findAll('a[href^="#"]')) {
      expect(article.find(link.attributes('href')).exists()).toBe(true)
    }
    const allowedHosts = new Set(['www.microsoft.com', 'outlook.live.com', 'chatgpt.com', '2fa.cn'])
    for (const link of article.findAll('a[href^="https://"]')) {
      expect(allowedHosts.has(new URL(link.attributes('href')).hostname)).toBe(true)
      expect(link.attributes('rel')).toContain('noopener')
    }
    expect(article.findAll('.guide-faq details')).toHaveLength(7)
    expect(article.findAll('.guide-methods details')).toHaveLength(2)
    expect(wrapper.html()).not.toMatch(/gptibo|ovooo|\.md\?raw/i)
  })
  it('submits exact session bytes with the current revision then clears the editor', async () => {
    const content = ' {"accessToken":"fictional-session-canary"}\n'
    api.submitInput.mockResolvedValue({ data: { ...order(), fulfillment_status: 'QUEUED', revision: 4 } })
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    await wrapper.get('textarea').setValue(content)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.checkInput).toHaveBeenCalledWith(42, { content, revision: 3 })
    expect(api.submitInput).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('target@example.com')
    expect(wrapper.text()).toContain('Free（免费版）')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.submitInput).toHaveBeenCalledWith(42, { content, revision: 3 })
    expect(wrapper.get('textarea').element.value).toBe('')
    expect(JSON.stringify(localStorage)).not.toContain('fictional-session-canary')
  })
  it('invalidates the check when the pasted content changes', async () => {
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    await wrapper.get('textarea').setValue('first-synthetic-content')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('确认账号无误')
    await wrapper.get('textarea').setValue('changed-synthetic-content')
    expect(wrapper.text()).not.toContain('确认账号无误')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.checkInput).toHaveBeenLastCalledWith(42, { content: 'changed-synthetic-content', revision: 3 })
    expect(api.submitInput).not.toHaveBeenCalled()
  })
  it('shows the actionable precheck error without submitting invalid input', async () => {
    api.checkInput.mockRejectedValueOnce({ message: 'Session 已过期，请重新登录后复制' })
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    await wrapper.get('textarea').setValue('expired-synthetic-content')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('已过期')
    expect(api.submitInput).not.toHaveBeenCalled()
  })
  it('does not offer credential submission on an unpaid order', async () => {
    api.order.mockResolvedValue({ data: { ...order(), payment_status: 'PENDING', fulfillment_status: 'WAITING_PAYMENT', can_submit: false } })
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    expect(wrapper.find('textarea').exists()).toBe(false)
    expect(wrapper.text()).toContain('继续付款')
  })
  it('previews and completes account delivery with a revision fence', async () => {
    const detail = { ...order(), product: { ...product, fulfillment_mode: 'account_delivery' }, fulfillment_status: 'QUEUED', can_submit: false, can_complete: true }
    api.adminOrder.mockResolvedValue({ data: detail })
    api.action.mockResolvedValue({ data: { ...detail, fulfillment_status: 'COMPLETED', can_complete: false, email_status: 'PENDING' } })
    const wrapper = mount(AdminShopOrderView, { global })
    await flushPromises()
    await wrapper.get('textarea').setValue('账号：test@example.invalid\n密码：fictional-password')
    const preview = wrapper.findAll('button').find(button => button.text() === '预览并确认发货')
    expect(preview).toBeDefined()
    await preview?.trigger('click')
    expect(api.action).not.toHaveBeenCalled()
    expect(wrapper.get('pre').text()).toContain('fictional-password')
    const confirm = wrapper.findAll('button').find(button => button.text() === '确认完成并通知用户')
    await confirm?.trigger('click')
    await flushPromises()
    expect(api.action).toHaveBeenCalledWith(42, { action: 'complete', revision: 3, input_revision: 0, delivery_content: '账号：test@example.invalid\n密码：fictional-password' })
    expect(wrapper.find('textarea').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('fictional-password')
  })
  it('masks delivered information until explicitly revealed', async () => {
    const wrapper = mount(ShopDelivery, { props: { content: '密码：fictional-password' } })
    expect(wrapper.text()).not.toContain('fictional-password')
    await wrapper.findAll('button')[0]?.trigger('click')
    expect(wrapper.text()).toContain('fictional-password')
  })
  it('removes executable markup from product details', () => {
    const wrapper = mount(ShopMarkdown, { props: { content: '<script>alert(1)</script><img src="/safe.png" onerror="alert(1)"><a href="javascript:alert(1)">bad</a>' } })
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.html()).not.toContain('onerror')
    expect(wrapper.html()).not.toContain('javascript:')
  })
  it('defaults new drafts to one month without an invented price', async () => {
    const wrapper = mount(ShopProductEditor, { props: { product: null, busy: false } })
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('save')?.[0]?.[0]).toMatchObject({ duration_value: 1, duration_unit: 'month', price: 0, enabled: false })
  })
  it('never treats late cancelled money or ordinary balance PAID as a completed checkout', () => {
    expect(isManualShopPaymentConfirmed({ order_type: 'manual_shop', status: 'CANCELLED' })).toBe(false)
    expect(isManualShopPaymentConfirmed({ order_type: 'balance', status: 'PAID' })).toBe(false)
    expect(isManualShopPaymentConfirmed({ order_type: 'manual_shop', status: 'PAID' })).toBe(true)
  })
})
