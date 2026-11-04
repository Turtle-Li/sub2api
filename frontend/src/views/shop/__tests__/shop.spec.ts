import { reactive } from 'vue'
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
const route = reactive({ params: { id: '42' }, path: '/shop/help/chatgpt-plus-account' })
vi.mock('@/api/shop', () => ({ shopAPI: api }))
vi.mock('@/api/payment', () => ({ paymentAPI: checkoutAPI }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ contactInfo: '测试客服入口' }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAdmin: true }) }))
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ push: vi.fn() }) }))
const stepUpRun = vi.hoisted(() => vi.fn((fn: () => Promise<unknown>) => fn()))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run: stepUpRun, onCancel: vi.fn() }), isStepUpCancelled: () => false }))
const product: ShopProduct = { id: 1, name: '测试商品', category: 'Gemini', summary: '', description: '', price: 12, currency: 'CNY', duration_value: 1, duration_unit: 'month', fulfillment_mode: 'session_topup', input_format: 'session_json', input_url: '', input_instructions: '', guide_key: '', enabled: true, sort_order: 0, revision: 1, created_at: '', updated_at: '' }
const order = (): ShopOrderDetail => ({ order_id: 42, out_trade_no: 'shop-test-42', product, user_id: 8, pay_amount: 12, currency: 'CNY', payment_status: 'PAID', fulfillment_status: 'WAITING_INPUT', revision: 3, input_revision: 0, input_account: '', created_at: '2026-11-04T12:00:00Z', status_note: '', email_status: 'NOT_SENT', admin_notification_status: 'SENT', can_submit: true, can_start: false, can_complete: false, can_reveal_input: false })
const global = { stubs: { AppLayout: { template: '<main><slot /></main>' }, RouterLink: { template: '<a><slot /></a>' }, ShopOrderSummary: true, TotpStepUpDialog: true, BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } } }
enableAutoUnmount(afterEach)
beforeEach(() => { route.params.id = '42'; vi.clearAllMocks(); localStorage.clear(); route.path = '/shop/help/chatgpt-plus-account'; checkoutAPI.getCheckoutInfo.mockResolvedValue({ data: { methods: {} } }); api.order.mockResolvedValue({ data: order() }); api.adminOrder.mockResolvedValue({ data: order() }); api.checkInput.mockResolvedValue({ data: { account: 'target@example.com', plan_type: 'free', expires_at: '2099-01-01T00:00:00Z', warnings: [] } }) })
describe('automatic day-pass surfaces', () => {
  const daypass = { ...product, name: 'DeepSeek 24 小时日卡', price: 6, duration_unit: 'day' as const, fulfillment_mode: 'account_delivery' as const, guide_key: 'deepseek-daypass' as const, automatic_fulfillment: true }
  it('shows the paid clock and key restrictions without the manual delivery estimate', async () => {
    api.product.mockResolvedValue({ data: daypass })
    const wrapper = mount(ShopProductView, { global })
    await flushPromises()
    expect(wrapper.text()).toContain('付款时开始计时')
    expect(wrapper.text()).toContain('默认 2 并发')
    expect(wrapper.text()).toContain('同一 IP')
    expect(wrapper.text()).not.toContain('5分钟')
    expect(wrapper.text()).not.toContain('2小时操作')
  })
  it('refreshes automatic delivery into a buyer-only masked key with the API guide', async () => {
    api.order.mockResolvedValueOnce({ data: { ...order(), product: daypass, fulfillment_status: 'QUEUED', can_submit: false } })
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    expect(wrapper.text()).toContain('正在自动发放 API Key')
    expect(wrapper.text()).toContain('有效期从付款时间开始计算')
    expect(wrapper.find('textarea').exists()).toBe(false)
    api.order.mockResolvedValueOnce({ data: { ...order(), product: daypass, fulfillment_status: 'COMPLETED', payment_status: 'COMPLETED', can_submit: false, delivery_content: 'API Key: sk-fictional-daypass' } })
    await wrapper.findAll('button').find(button => button.text() === '刷新状态')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('a[aria-label="API Key 使用指南"]').attributes('to')).toBe('/shop/help/deepseek-daypass?order=42')
    expect(wrapper.text()).not.toContain('sk-fictional-daypass')
    expect(wrapper.text()).toContain('API Key 已交付')
    expect(wrapper.text()).not.toContain('账号已交付')
    expect(wrapper.text()).not.toContain('成品号使用指南')
    expect(api.submitInput).not.toHaveBeenCalled()
  })
  it('prefills the fixed product contract when the admin selects automatic day-pass delivery', async () => {
    const wrapper = mount(ShopProductEditor, { props: { product: null, busy: false } })
    await wrapper.get('#guide-key').setValue('deepseek-daypass')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('save')?.[0]?.[0]).toMatchObject({ price: 6, duration_value: 1, duration_unit: 'day', fulfillment_mode: 'account_delivery', guide_key: 'deepseek-daypass', enabled: false })
  })
  it('renders working API guide anchors and the formal endpoint without account login steps', () => {
    route.path = '/shop/help/deepseek-daypass'
    const wrapper = mount(ShopGuideView, { global })
    const article = wrapper.get('article')
    expect(article.text()).toContain('https://ds.turtleligpt.com/v1')
    expect(article.text()).toContain('deepseek-v4.1-flash')
    expect(article.text()).toContain('deepseek-v4-pro')
    expect(article.text()).not.toContain('outlook')
    for (const link of wrapper.findAll('a[href^="#"]')) {
      expect(article.element.contains(wrapper.get(link.attributes('href')).element)).toBe(true)
    }
    expect(article.findAll('img')).toHaveLength(0)
  })
})
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
    expect(wrapper.findAll('input[type="radio"]')).toHaveLength(0)
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
    expect(link.attributes('to')).toBe('/shop/help/chatgpt-plus-account?order=42')
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
      const target = wrapper.get(link.attributes('href'))
      expect(target.element === article.element || article.element.contains(target.element)).toBe(true)
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
  it('automatically previews pasted Session locally while retaining the server check', async () => {
    const payload = btoa(JSON.stringify({ exp: 4070908800, 'https://api.openai.com/auth': { chatgpt_plan_type: 'plus' } })).replace(/=+$/, '')
    const content = ` ${JSON.stringify({ user: { name: '本地测试用户', email: 'local@example.invalid' }, account: { planType: 'plus' }, accessToken: `eyJhbGciOiJSUzI1NiJ9.${payload}.c3ludGhldGlj`, expires: '2099-01-01T00:00:00Z' })}\n`
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    await wrapper.get('textarea').setValue(content)
    expect(wrapper.text()).not.toContain('本地初步识别')
    expect(wrapper.text()).toContain('本地测试用户')
    expect(wrapper.text()).toContain('local@example.invalid')
    expect(wrapper.text()).toContain('ChatGPT Plus')
    expect(api.checkInput).not.toHaveBeenCalled()
    expect(api.submitInput).not.toHaveBeenCalled()
    expect(JSON.stringify(localStorage)).not.toContain('local@example.invalid')
    await wrapper.get('textarea').setValue('')
    expect(wrapper.text()).not.toContain('target@example.com')
    expect(wrapper.text()).not.toContain('local@example.invalid')
    expect(wrapper.text()).not.toContain('本地测试用户')
  })
  it('refreshes the buyer order to show manual fulfillment progress without a page reload', async () => {
    api.order.mockResolvedValueOnce({ data: { ...order(), fulfillment_status: 'PROCESSING', can_submit: false } })
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    expect(wrapper.text()).toContain('订单正在人工处理')
    api.order.mockResolvedValueOnce({ data: { ...order(), fulfillment_status: 'COMPLETED', payment_status: 'COMPLETED', can_submit: false } })
    await wrapper.findAll('button').find(button => button.text() === '刷新状态')!.trigger('click')
    await flushPromises()
    expect(api.order).toHaveBeenLastCalledWith(42)
    expect(wrapper.text()).toContain('订单已完成')
    expect(wrapper.text()).not.toContain('订单正在人工处理')
    expect(api.submitInput).not.toHaveBeenCalled()
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
    expect(api.submitInput).toHaveBeenCalledWith(42, { content, revision: 3 })
    expect(wrapper.get('textarea').element.value).toBe('')
    expect((wrapper.vm as unknown as { content: string }).content).toBe('')
    expect(JSON.stringify(localStorage)).not.toContain('fictional-session-canary')
  })
  it('replaces stale local hints on edits and never parses a plain-text product', async () => {
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    await wrapper.get('textarea').setValue(JSON.stringify({ user: { name: '上一个账号', email: 'old@example.invalid' }, account: { planType: 'free' } }))
    expect(wrapper.text()).toContain('old@example.invalid')
    await wrapper.get('textarea').setValue(JSON.stringify({ user: { name: '另一个账号', email: 'new@example.invalid' }, account: { planType: 'plus' } }))
    expect(wrapper.text()).toContain('new@example.invalid')
    expect(wrapper.text()).not.toContain('old@example.invalid')
    expect(wrapper.text()).not.toContain('上一个账号')
    await wrapper.get('textarea').setValue('{')
    expect(wrapper.text()).toContain('不是完整的 JSON')
    expect(wrapper.text()).not.toContain('new@example.invalid')
    expect(api.checkInput).not.toHaveBeenCalled()
    api.order.mockResolvedValueOnce({ data: { ...order(), product: { ...product, input_format: 'text' } } })
    const plain = mount(ShopOrderView, { global })
    await flushPromises()
    await plain.get('textarea').setValue('{plain-text-product')
    expect(plain.text()).not.toContain('本地初步识别')
    expect(plain.text()).not.toContain('不是完整的 JSON')
    api.submitInput.mockResolvedValueOnce({ data: order() })
    await plain.get('form').trigger('submit')
    await flushPromises()
    expect(api.submitInput).toHaveBeenCalledWith(42, { content: '{plain-text-product', revision: 3 })
    expect(api.checkInput).not.toHaveBeenCalled()
  })
  it.each([true, false])('keeps queued input updates behind the server permission: %s', async canSubmit => {
    api.order.mockResolvedValue({ data: { ...order(), fulfillment_status: 'QUEUED', can_submit: canSubmit, input_account: 'submitted@example.invalid', input_submitted_at: '2026-11-05T12:00:00Z' } })
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    expect(wrapper.text()).toContain('等待人工处理')
    expect(wrapper.get('dl').text()).toContain('submitted@example.invalid')
    expect(wrapper.get('dl').text()).toContain('2026-11-05T12:00:00Z')
    expect(wrapper.get('dl').element.closest('details')).toBeNull()
    expect(wrapper.find('textarea').exists()).toBe(canSubmit)
    if (canSubmit) {
      expect(wrapper.get('details summary').text()).toBe('更新充值资料')
      expect(wrapper.get('details').attributes('open')).toBeUndefined()
    }
  })
  it('invalidates the check when the pasted content changes', async () => {
    let release!: (result: unknown) => void
    api.checkInput.mockImplementationOnce(() => new Promise(resolve => { release = resolve }))
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    await wrapper.get('textarea').setValue('first-synthetic-content')
    await wrapper.get('form').trigger('submit')
    await wrapper.get('form').trigger('submit')
    expect(api.checkInput).toHaveBeenCalledTimes(1)
    const state = wrapper.vm as unknown as { content: string }
    state.content = 'changed-synthetic-content'
    state.content = 'first-synthetic-content'
    release({ data: { warnings: [] } })
    await flushPromises()
    expect(api.submitInput).not.toHaveBeenCalled()
  })
  it.each(['resolve', 'reject'])('ignores a stale server check after navigation (%s)', async outcome => {
    let release!: () => void
    api.checkInput.mockImplementationOnce(() => new Promise((resolve, reject) => {
      release = () => outcome === 'resolve' ? resolve({ data: { warnings: [] } }) : reject(new Error('obsolete'))
    }))
    const wrapper = mount(ShopOrderView, { global })
    await flushPromises()
    await wrapper.get('textarea').setValue('synthetic')
    await wrapper.get('form').trigger('submit')
    api.order.mockImplementationOnce(() => new Promise(() => {}))
    route.params.id = '43'
    await flushPromises()
    release()
    await flushPromises()
    expect(api.submitInput).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('obsolete')
    expect(wrapper.get('[aria-label="正在加载订单"]').exists()).toBe(true)
  })
  it('copies the revealed input verbatim and clears feedback for a newer revision or failed copy', async () => {
    const detail = { ...order(), can_reveal_input: true, input_revision: 2 }
    api.adminOrder.mockResolvedValue({ data: detail })
    const raw = '  synthetic raw content\n'
    api.revealInput.mockResolvedValue({ data: { content: raw, input_revision: 2 } })
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const wrapper = mount(AdminShopOrderView, { global })
    await flushPromises()
    const button = wrapper.findAll('button').find(item => item.text() === '复制 Session')!
    await button.trigger('click')
    await flushPromises()
    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(api.revealInput).toHaveBeenCalledWith(42, 3)
    expect(writeText).toHaveBeenCalledWith(raw)
    expect(button.text()).toBe('已复制')
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    api.adminOrder.mockResolvedValue({ data: { ...detail, revision: 4 } })
    await wrapper.findAll('button').find(item => item.text() === '刷新订单')!.trigger('click')
    await flushPromises()
    expect(button.text()).toBe('复制 Session')
    writeText.mockRejectedValueOnce(new Error('denied'))
    await button.trigger('click')
    await flushPromises()
    expect(button.text()).toBe('复制 Session')
    expect(wrapper.get('[role="alert"]').text()).toContain('剪贴板权限')
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
  it('previews delimited account fields while submitting the complete original delivery', async () => {
    const detail = { ...order(), product: { ...product, fulfillment_mode: 'account_delivery' }, fulfillment_status: 'QUEUED', can_submit: false, can_complete: true }
    const raw = 'test@example.invalid---- pass----word \n'
    api.adminOrder.mockResolvedValue({ data: detail })
    api.action.mockResolvedValue({ data: { ...detail, fulfillment_status: 'COMPLETED', can_complete: false } })
    const wrapper = mount(AdminShopOrderView, { global })
    await flushPromises()
    await wrapper.get('textarea').setValue(raw)
    expect(wrapper.get('[aria-label="账号密码预览"]').text()).toContain('test@example.invalid')
    await wrapper.findAll('button').find(button => button.text() === '预览并确认发货')!.trigger('click')
    expect(wrapper.get('[aria-label="确认账号密码"]').text()).toContain('pass----word')
    expect(api.action).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(button => button.text() === '确认完成并通知用户')!.trigger('click')
    await flushPromises()
    expect(api.action).toHaveBeenCalledWith(42, { action: 'complete', revision: 3, input_revision: 0, delivery_content: raw })
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
