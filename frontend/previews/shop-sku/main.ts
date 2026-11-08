import { createApp, h } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHashHistory, RouterView } from 'vue-router'
import { AxiosError } from 'axios'
import { apiClient } from '@/api/client'
import i18n, { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import AdminPaymentPlansView from '@/views/admin/orders/AdminPaymentPlansView.vue'
import AdminShopProductsView from '@/views/shop/AdminShopProductsView.vue'
import ShopProductView from '@/views/shop/ShopProductView.vue'
import ShopView from '@/views/shop/ShopView.vue'
import ShopOrderView from '@/views/shop/ShopOrderView.vue'
import { resolveShopGuideRedirect } from '@/router/shopGuideRedirect'
import type { ShopOrderDetail, ShopProduct } from '@/types/shop'
import '@/style.css'

const stamp = '2026-11-08T08:00:00Z'
const products: ShopProduct[] = [
  { id: 3, name: 'DeepSeek API 不限量订阅卡', category: 'API 订阅', summary: '有效期内不限 Token 总量，按需选择周期与并发。', description: '付款确认后自动发放独立 API Key。有效期从付款确认时开始。', price: 6, currency: 'CNY', duration_value: 1, duration_unit: 'day', fulfillment_mode: 'account_delivery', input_format: 'text', input_url: '', input_instructions: '', guide_key: 'deepseek-daypass', enabled: true, sort_order: 0, revision: 1, created_at: stamp, updated_at: stamp, automatic_fulfillment: true, payment_actions: [{ binding_key: 'primary-delivery', adapter: 'newapi.daypass.v1', integration_ref: 'newapi-domestic', integration_version: 1, params: { duration_seconds: 86400, default_concurrency: 2 } }], concurrency: 2, concurrency_options: [{ concurrency: 2, price: 6, weekly_price: 30 }, { concurrency: 3, price: 8, weekly_price: 40 }] },
  { id: 1, name: 'ChatGPT Plus 代充', category: '代充', summary: '提交账号资料，由人工完成充值。', description: '', price: 108, currency: 'CNY', duration_value: 1, duration_unit: 'month', fulfillment_mode: 'session_topup', input_format: 'session_json', input_url: 'https://chatgpt.com/api/auth/session', input_instructions: '', guide_key: '', enabled: true, sort_order: 1, revision: 1, created_at: stamp, updated_at: stamp },
  { id: 2, name: 'ChatGPT Plus 成品号', category: '成品号', summary: '人工交付账号资料。', description: '', price: 128, currency: 'CNY', duration_value: 1, duration_unit: 'month', fulfillment_mode: 'account_delivery', input_format: 'text', input_url: '', input_instructions: '', guide_key: 'chatgpt-plus-account', enabled: false, sort_order: 2, revision: 1, created_at: stamp, updated_at: stamp }
]
const orderRequests = new Map<number, number>()
apiClient.defaults.adapter = async config => {
  const path = config.url ?? ''
  let data: unknown = {}
  if (/\/shop\/products(?:\/\d+)?$/.test(path)) {
    const id = Number(path.split('/').at(-1))
    if (config.method === 'put' || config.method === 'post') {
      const input: ShopProduct = JSON.parse(config.data)
      const previous = products.find(p => p.id === id)
      if (previous && previous.revision !== input.revision) throw new AxiosError('商品已被其他管理员更新，请刷新后重试', 'SHOP_CONFLICT', config)
      const saved = { ...input, id: id || 4, currency: 'CNY' as const, revision: (previous?.revision ?? 0) + 1, created_at: stamp, updated_at: stamp, automatic_fulfillment: !!input.payment_actions?.length }
      products.splice(previous ? products.indexOf(previous) : products.length, previous ? 1 : 0, saved)
      data = saved
    } else data = id ? products.find(p => p.id === id) : products
  } else if (/\/shop\/orders\/\d+$/.test(path)) {
    const id = Number(path.split('/').at(-1))
    const requests = (orderRequests.get(id) ?? 0) + 1
    orderRequests.set(id, requests)
    const manual = id === 8802 || id === 8803
    const review = id === 8805
    const completed = !review && (manual || requests >= 3)
    const product = manual ? { ...products[2], automatic_fulfillment: false, delivery_config: id === 8802 ? { individual_copy: true } : undefined } : products[0]
    data = {
      order_id: id, out_trade_no: `synthetic-${id}`, product, user_id: 1, pay_amount: product.price, currency: 'CNY',
      payment_status: completed ? 'COMPLETED' : 'PAID', fulfillment_status: completed ? 'COMPLETED' : 'PROCESSING',
      revision: completed ? 3 : 2, input_revision: 0, input_account: '', created_at: stamp, paid_at: stamp, status_note: '',
      email_status: 'NOT_SENT', admin_notification_status: 'SENT', can_submit: false, can_start: false,
      can_complete: false, can_reveal_input: false, delivery_kind: 'text',
      delivery_content: completed ? manual ? 'synthetic@example.invalid----synthetic-password' : 'DeepSeek API 不限量订阅卡\nBase URL：https://ds.turtleligpt.com/v1\nAPI Key：sk-synthetic-preview-only\n有效期：1天（自付款起算）' : '',
      automation: manual ? undefined : { status: review ? 'REVIEW_REQUIRED' : completed ? 'SUCCEEDED' : 'RUNNING', result_kind: 'text' }
    } satisfies ShopOrderDetail
  } else if (path.endsWith('action-integrations')) data = [{ id: 'newapi-domestic', version: 1, adapter: 'newapi.daypass.v1', enabled: true }, { id: 'license-service', version: 1, adapter: 'http.webhook.v1', enabled: true }]
  else if (path.includes('checkout-info')) data = { enabled: true, methods: { alipay: { available: true, currency: 'CNY', single_min: 0.01, single_max: 9999 }, wxpay: { available: true, currency: 'CNY', single_min: 0.01, single_max: 9999 } } }
  else if (path.endsWith('/plans') || path.endsWith('/groups/all')) data = []
  else if (path.includes('payment/config')) data = { enabled: true }
  else if (path.includes('notices')) data = { items: [], total: 0 }
  return { data: { code: 0, data }, status: 200, statusText: 'OK', headers: {}, config }
}
const router = createRouter({ history: createWebHashHistory(), routes: [
  { path: '/admin/products', component: AdminPaymentPlansView, meta: { title: '商品管理' } },
  { path: '/admin/orders/plans', component: AdminPaymentPlansView, meta: { title: '商品管理' } },
  { path: '/admin/shop/products', component: AdminShopProductsView, meta: { title: '商品管理' } },
  { path: '/shop', component: ShopView, meta: { title: '商城' } },
  { path: '/shop/products/:id', component: ShopProductView, meta: { title: '商品详情' } },
  { path: '/shop/orders/:id', component: ShopOrderView, meta: { title: '交付详情' } },
  { path: '/shop/help/:guide', redirect: to => resolveShopGuideRedirect(to.query) },
  { path: '/:pathMatch(.*)*', redirect: '/admin/products' }
] })
const app = createApp({ render: () => h(RouterView) })
app.use(createPinia())
const auth = useAuthStore()
auth.token = 'synthetic-preview'
auth.user = { id: 1, username: '预览管理员', email: 'preview@example.test', role: 'admin', balance: 0, concurrency: 2, status: 'active', allowed_groups: null, balance_notify_enabled: false, balance_notify_threshold: null, balance_notify_extra_emails: [], created_at: stamp, updated_at: stamp }
useAppStore().publicSettingsLoaded = true
useAdminSettingsStore().paymentEnabled = true
localStorage.setItem('admin_guide_1_admin_v4_interactive', 'true')
await setLocale('zh')
app.use(i18n).use(router)
await router.isReady()
app.mount('#app')
