import { createApp, h } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHistory, RouterView } from 'vue-router'
import { i18n, loadLocaleMessages } from '@/i18n'
import { apiClient } from '@/api/client'
import { useAppStore } from '@/stores/app'
import PaymentView from '@/views/user/PaymentView.vue'
import AdminPaymentCouponsView from '@/views/admin/orders/AdminPaymentCouponsView.vue'
import AdminOrdersView from '@/views/admin/orders/AdminOrdersView.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'
import catalog from './catalog.json'
import '@/style.css'

const method = { daily_limit: 0, daily_used: 0, daily_remaining: 0, single_min: 0, single_max: 0, fee_rate: 0, available: true }
const previewPlans = () => catalog.plans.map(plan => ({ ...plan, ...catalog.groups[String(plan.group_id) as keyof typeof catalog.groups] }))
const checkout = { methods: { alipay: method, wxpay: method }, global_min: 0, global_max: 0, plans: previewPlans(), recharge_options: catalog.recharge_options, balance_disabled: false, balance_recharge_multiplier: 1, subscription_usd_to_cny_rate: 0, recharge_fee_rate: 0, help_text: '', help_image_url: '', stripe_publishable_key: '' }
const previewCoupon = {
  id: 2026,
  code: 'SAVE2026',
  discount_type: 'percent',
  discount_value: '20',
  order_types: ['balance', 'subscription'],
  plan_ids: [],
  currency: 'CNY',
  max_uses: 100,
  per_user_max_uses: 1,
  target_user_id: null,
  starts_at: '2026-09-01T00:00:00Z',
  expires_at: '2026-12-31T15:59:59Z',
  enabled: true,
  notes: '本地预览数据',
  version: 1,
  reserved_uses: 1,
  consumed_uses: 4,
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-19T00:00:00Z',
}
const previewCouponUsages = [
  {
    order_id: 202601,
    user_id: 12,
    status: 'consumed',
    original_amount: '99.00',
    discount_amount: '19.00',
    pay_amount: '80.00',
    currency: 'CNY',
    created_at: '2026-09-18T08:00:00Z',
    updated_at: '2026-09-18T08:01:00Z',
  },
  {
    order_id: 202602,
    user_id: 18,
    status: 'paid_review',
    original_amount: '5.00',
    discount_amount: '1.00',
    pay_amount: '4.00',
    currency: 'CNY',
    created_at: '2026-09-19T08:00:00Z',
    updated_at: '2026-09-19T08:01:00Z',
  },
]
const previewCouponAudits = [
  { id: 1, admin_user_id: 1, action: 'created', detail: '本地预览数据', created_at: '2026-09-01T00:00:00Z' },
  { id: 2, admin_user_id: 1, action: 'enabled', detail: '本地预览数据', created_at: '2026-09-01T00:00:00Z' },
]
const previewOrder = {
  id: 202601, user_id: 12, user_email: 'preview@example.test', user_name: '预览用户',
  amount: 104, pay_amount: 80, fee_rate: 0, currency: 'CNY', payment_type: 'alipay',
  order_type: 'balance', status: 'COMPLETED', payment_status: 'PAID', fulfillment_status: 'FULFILLED',
  out_trade_no: 'preview_order_202601', payment_trade_no: 'preview_transaction_202601',
  created_at: '2026-09-18T08:00:00Z', expires_at: '2026-09-18T08:30:00Z',
  paid_at: '2026-09-18T08:01:00Z', completed_at: '2026-09-18T08:01:00Z',
  product_snapshot: {
    schema_version: 2, name: '进阶使用', credited_amount: 104, paid_credit_amount: 80, gift_credit_amount: 24,
    payment_discount: { code_id: 2026, code: 'SAVE2026', original_amount: '99.00', discount_amount: '19.00', pay_amount: '80.00', currency: 'CNY' },
  },
}
const previewReviewOrder = {
  ...previewOrder, id: 202602, user_id: 18, amount: 5, pay_amount: 4, status: 'FAILED',
  fulfillment_status: 'FAILED', needs_manual_review: true, refund_entitlement_status: 'NOT_APPLICABLE',
  out_trade_no: 'preview_order_202602', completed_at: null,
  product_snapshot: {
    schema_version: 2, name: '轻量体验', credited_amount: 5, paid_credit_amount: 4, gift_credit_amount: 1,
    payment_discount: { code_id: 2026, code: 'SAVE2026', original_amount: '5.00', discount_amount: '1.00', pay_amount: '4.00', currency: 'CNY' },
  },
}

function page(items: unknown[]) {
  return { items, total: items.length, page: 1, page_size: 20, pages: 1 }
}

function previewResponse(config: Parameters<NonNullable<typeof apiClient.defaults.adapter>>[0], data: unknown) {
  return { data: { code: 0, data }, status: 200, statusText: 'OK', headers: {}, config }
}

const previewCouponView = new URLSearchParams(window.location.search).get('view') === 'coupons'
const previewConfirmationView = new URLSearchParams(window.location.search).get('view') === 'confirmation'
const previewDeadline = new Date(Date.now() + 30 * 60_000).toISOString()
const confirmationPreview = { render: () => h('main', { class: 'mx-auto max-w-lg p-6' }, [h(PaymentStatusPanel, {
  orderId: 202601, amount: 104, payAmount: 80, currency: 'CNY', paymentType: 'alipay', orderType: 'balance',
  qrCode: 'LOCAL PREVIEW ONLY - NOT A PAYMENT', expiresAt: previewDeadline,
  paymentDiscount: previewOrder.product_snapshot.payment_discount,
})]) }

// Block every write except the local quote fixture, and never forward requests
// to a real API.
apiClient.defaults.adapter = async config => {
  const requestMethod = config.method?.toLowerCase()
  const requestURL = String(config.url || '')
  if (requestMethod === 'post' && requestURL.includes('/payment/coupon-quote')) {
    const body = typeof config.data === 'string' ? JSON.parse(config.data) : (config.data || {})
    if (String(body.coupon_code || '').trim().toUpperCase() !== 'SAVE2026') {
      throw new Error('预览优惠码仅支持 SAVE2026')
    }
    const original = Number(body.amount)
    if (!Number.isFinite(original) || original <= 0) throw new Error('预览金额无效')
    if (!['balance', 'subscription'].includes(body.order_type || 'balance')) throw new Error('重置卡不支持优惠码')
    const pay = Math.max(1, Math.ceil(Math.round(original * 100) * 80 / 10000))
    if (pay >= original) throw new Error('取整后无有效优惠')
    const discount = original - pay
    return previewResponse(config, {
      code_id: 2026,
      code: 'SAVE2026',
      version: 1,
      original_amount: original.toFixed(2),
      discount_amount: discount.toFixed(2),
      pay_amount: pay.toFixed(2),
      currency: 'CNY',
      revision: `preview-${body.order_type || 'balance'}-${body.payment_type || 'alipay'}`,
    })
  }
  if (requestMethod !== 'get') throw new Error('预览不支持创建订单或支付')
  if (requestURL === '/payment/orders/202601') return previewResponse(config, { ...previewOrder, status: 'PENDING', payment_status: 'UNPAID', fulfillment_status: 'NOT_STARTED', paid_at: null, completed_at: null, expires_at: previewDeadline })
  if (requestURL.endsWith('/admin/payment/orders/202601')) return previewResponse(config, { order: previewOrder, audit_logs: [] })
  if (requestURL.endsWith('/admin/payment/orders/202602')) return previewResponse(config, { order: previewReviewOrder, audit_logs: [] })
  if (requestURL.endsWith('/admin/payment/orders')) return previewResponse(config, page([previewOrder, previewReviewOrder]))
  if (requestURL.includes('/admin/payment/coupons/2026/usages')) return previewResponse(config, page(previewCouponUsages))
  if (requestURL.includes('/admin/payment/coupons/2026/audits')) return previewResponse(config, page(previewCouponAudits))
  if (requestURL.includes('/admin/payment/coupons')) return previewResponse(config, page([previewCoupon]))
  if (requestURL.endsWith('/admin/payment/plans')) return previewResponse(config, previewPlans())
  if (requestURL.includes('/admin/promo-codes')) return previewResponse(config, page([]))
  return previewResponse(config, requestURL.includes('checkout') ? { ...checkout, plans: previewPlans() } : [])
}
const pinia = createPinia()
const app = createApp({
  setup() {
    return () => h('div', [
      h('div', { class: 'mx-auto flex max-w-7xl items-center justify-between px-8 pt-4 text-xs text-gray-500' }, [
        h('span', '样式预览 · 示例配置 · 不会创建订单'),
        h('button', { class: 'btn btn-secondary', onClick: () => document.documentElement.classList.toggle('dark') }, '切换明暗'),
      ]),
      h(RouterView),
    ])
  },
})
const router = createRouter({ history: createWebHistory(), routes: [
  { path: '/admin/orders', component: AdminOrdersView },
  { path: '/admin/orders/coupons', component: AdminPaymentCouponsView },
  { path: '/:pathMatch(.*)*', component: previewCouponView ? AdminPaymentCouponsView : previewConfirmationView ? confirmationPreview : PaymentView },
] })
app.use(pinia).use(i18n).use(router)
const store = useAppStore()
store.cachedPublicSettings = { subscription_enabled: true, billing_mode: 'mixed', server_utc_offset: 8 } as never
store.publicSettingsLoaded = true
await loadLocaleMessages('zh')
i18n.global.locale.value = 'zh'
await router.isReady()
app.mount('#preview')
