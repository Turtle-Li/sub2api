<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import alipayIcon from '@/assets/icons/alipay.svg'
import wxpayIcon from '@/assets/icons/wxpay.svg'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'
import ShopNotice from '@/components/shop/ShopNotice.vue'
import ShopMarkdown from '@/components/shop/ShopMarkdown.vue'
import { shopAPI } from '@/api/shop'
import { paymentAPI } from '@/api/payment'
import { clearPaymentRecoverySnapshot, getVisibleMethods, writePaymentRecoverySnapshot } from '@/components/payment/paymentFlow'
import { shopDuration, shopDeliveryLabel, isShopDaypass, type ShopProduct } from '@/types/shop'
import type { CreateOrderResult, PaymentOrder } from '@/types/payment'
import { createIdempotencyKey } from '@/utils/idempotency'
const route = useRoute()
const router = useRouter()
const product = ref<ShopProduct | null>(null)
const hasDeliveryTime = computed(() => /5\s*分钟\s*[-–—~～至]\s*2\s*小时/.test(`${product.value?.summary || ''}\n${product.value?.description || ''}`))
const checkout = ref<CreateOrderResult | null>(null)
const recoveryAvailable = ref(false)
const concurrency = ref(2)
const options = computed(() => product.value?.concurrency_options ?? [])
const selectedPrice = computed(() => options.value.find(option => option.concurrency === concurrency.value)?.price ?? product.value?.price ?? 0)
const methodLimits = ref<ReturnType<typeof getVisibleMethods>>({})
const methods = computed(() => (['alipay', 'wxpay'] as const).filter(key => {
  const limit = methodLimits.value[key]
  return limit && limit.available !== false && (!limit.currency || limit.currency === 'CNY')
    && (!(limit.single_min > 0) || selectedPrice.value >= limit.single_min)
    && (!(limit.single_max > 0) || selectedPrice.value <= limit.single_max)
}))
const method = ref<'alipay' | 'wxpay'>('alipay')
const busy = ref(false)
const loading = ref(true)
const error = ref('')
let attemptKey = ''
let attemptMethod = ''
async function load() {
  loading.value = true
  error.value = ''
  try {
    const [item, config] = await Promise.all([shopAPI.product(Number(route.params.id)), paymentAPI.getCheckoutInfo()])
    product.value = item.data
    concurrency.value = item.data.concurrency ?? 2
    methodLimits.value = getVisibleMethods(config.data.methods)
    method.value = methods.value[0] ?? 'alipay'
  } catch { error.value = '商品加载失败，请刷新重试。' }
  finally { loading.value = false }
}
async function buy() {
  if (!product.value || busy.value || !methods.value.includes(method.value)) return
  busy.value = true
  error.value = ''
  try {
    const selection = `${method.value}/${options.value.length ? concurrency.value : 0}`
    if (!attemptKey || attemptMethod !== selection) { attemptKey = createIdempotencyKey('shop-order'); attemptMethod = selection }
    const result = (await shopAPI.createOrder({ product_id: product.value.id, product_revision: product.value.revision, payment_type: method.value, ...(options.value.length ? { concurrency: concurrency.value } : {}) }, attemptKey)).data
    try {
      writePaymentRecoverySnapshot(window.localStorage, {
        orderId: result.order_id, amount: result.amount, payAmount: result.pay_amount,
        qrCode: result.qr_code || '', expiresAt: result.expires_at,
        paymentType: result.payment_type || method.value, payUrl: result.pay_url || '',
        checkoutFrameUrl: result.checkout_frame_url, outTradeNo: result.out_trade_no || '',
        clientSecret: '', intentId: '', currency: 'CNY', countryCode: '', paymentEnv: '',
        orderType: 'manual_shop', paymentMode: result.payment_mode || '',
        resumeToken: result.resume_token || '', createdAt: Date.now(),
      })
      recoveryAvailable.value = true
    } catch {
      // The order exists. Keep its active checkout; disable navigation that
      // would need the optional browser recovery cache on the return trip.
      recoveryAvailable.value = false
    }
    checkout.value = result
  } catch { error.value = '未能创建订单，请重试。若订单已创建，可到“我的订单”继续付款。' }
  finally { busy.value = false }
}
function onPaymentSettled(outcome: 'success' | 'cancelled' | 'expired') {
  if (outcome === 'cancelled' || outcome === 'expired') {
    attemptKey = ''
    attemptMethod = ''
    if (checkout.value && recoveryAvailable.value) {
      try { clearPaymentRecoverySnapshot(window.localStorage, undefined, { orderId: checkout.value.order_id }) }
      catch { recoveryAvailable.value = false }
    }
  }
}
function closeCheckout() {
  checkout.value = null
}
function onPaymentSuccess(paid?: Pick<PaymentOrder, 'id' | 'order_type'>) {
  if (paid?.order_type !== 'manual_shop' || paid.id !== checkout.value?.order_id) return
  closeCheckout()
  void router.push(`/shop/orders/${paid.id}`)
}
watch(() => route.params.id, () => { product.value = null; checkout.value = null; attemptKey = ''; void load() }, { immediate: true })
watch(methods, available => { if (!available.includes(method.value)) method.value = available[0] ?? 'alipay' })
</script>
<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-[1180px] px-4 py-6 sm:px-6 lg:py-8">
      <RouterLink to="/shop" class="mb-5 inline-flex min-h-11 items-center gap-2 rounded-md text-sm font-medium text-gray-600 hover:text-gray-900 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:text-gray-400 dark:hover:text-gray-100 dark:focus:ring-offset-gray-950">
        <Icon name="chevronLeft" class="h-4 w-4" />
        返回商店
      </RouterLink>

      <div v-if="loading" class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_340px]" aria-busy="true" aria-label="正在加载商品">
        <div class="h-72 animate-pulse rounded-xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900"></div>
        <div class="h-80 animate-pulse rounded-xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900"></div>
      </div>

      <section v-else-if="!product" class="rounded-xl border border-red-200 bg-white p-6 dark:border-red-900 dark:bg-gray-900" role="alert">
        <h1 class="text-lg font-semibold text-gray-900 dark:text-gray-100">无法显示商品</h1>
        <p class="mt-2 text-sm text-red-700 dark:text-red-300">{{ error }}</p>
        <button type="button" class="mt-5 inline-flex min-h-11 items-center justify-center rounded-md bg-primary-700 px-5 text-sm font-medium text-white hover:bg-primary-800 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:bg-primary-400 dark:text-gray-950 dark:hover:bg-primary-300 dark:focus:ring-offset-gray-900" @click="load">
          重新加载
        </button>
      </section>

      <template v-else>
        <header class="mb-6 max-w-3xl">
          <div class="flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span class="badge bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-300">{{ product.category }}</span>
            <span>{{ shopDeliveryLabel(product) }}</span>
          </div>
          <h1 class="mt-3 break-words text-2xl font-semibold leading-tight text-gray-900 dark:text-gray-100 sm:text-[28px]">{{ product.name }}</h1>
          <p v-if="product.summary" class="mt-3 whitespace-pre-wrap break-words text-sm leading-7 text-gray-600 dark:text-gray-400">{{ product.summary }}</p>
        </header>

        <div class="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_340px]">
          <article class="min-w-0 rounded-xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-gray-900 sm:p-6">
            <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">商品详情</h2>
            <div v-if="product.description" class="mt-5 min-w-0 text-sm leading-7 text-gray-700 dark:text-gray-300">
              <ShopMarkdown :content="product.description" />
            </div>
            <p v-else class="mt-5 text-sm text-gray-500 dark:text-gray-400">暂无更多商品说明。</p>
          </article>

          <aside class="min-w-0 rounded-xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-gray-900 lg:sticky lg:top-6" aria-label="购买商品">
            <RouterLink v-if="product.guide_key" :to="`/shop/help/${product.guide_key}?product=${product.id}`" class="mb-5 flex min-h-11 items-center justify-between gap-3 rounded-lg border border-primary-200 bg-primary-50 px-4 py-3 text-sm font-medium text-primary-800 hover:bg-primary-100 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:border-primary-800 dark:bg-primary-950 dark:text-primary-200 dark:focus:ring-offset-gray-900">
              <span>购买前请阅读使用指南</span>
              <Icon name="arrowRight" class="h-4 w-4 shrink-0" />
            </RouterLink>

            <dl class="flex items-end justify-between gap-4 border-b border-gray-200 pb-5 dark:border-gray-800">
              <div>
                <dt class="text-xs text-gray-500 dark:text-gray-400">价格</dt>
                <dd aria-live="polite" class="mt-1 text-2xl font-semibold text-gray-900 dark:text-gray-100">¥{{ selectedPrice.toFixed(2) }}</dd>
              </div>
              <div class="text-right">
                <dt class="text-xs text-gray-500 dark:text-gray-400">时长</dt>
                <dd class="mt-1 text-sm font-medium text-gray-800 dark:text-gray-200">{{ shopDuration(product) }}</dd>
              </div>
            </dl>

            <fieldset v-if="options.length" class="my-5" :disabled="busy || !!checkout">
              <legend class="text-sm font-medium text-gray-900 dark:text-gray-100">并发数量</legend>
              <div class="mt-3 grid grid-cols-2 gap-2">
                <label v-for="option in options" :key="option.concurrency" class="flex min-h-16 cursor-pointer items-center gap-3 rounded-lg border px-3 py-3 text-sm" :class="concurrency === option.concurrency ? 'border-primary-500 bg-primary-50 dark:bg-primary-950' : 'border-gray-200 dark:border-gray-700'">
                  <input v-model="concurrency" type="radio" name="concurrency" :value="option.concurrency" class="h-5 w-5 shrink-0 accent-primary-700 focus:ring-primary-500" />
                  <span class="text-gray-800 dark:text-gray-200"><strong class="block font-medium">{{ option.concurrency }} 并发</strong><span class="mt-1 block text-xs">¥{{ option.price.toFixed(2) }} / 天</span></span>
                </label>
              </div>
              <p class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">同一 Key 可同时进行的请求数，最高 3 并发。</p>
            </fieldset>

            <ShopNotice :show-delivery-time="!hasDeliveryTime" :automatic="product.automatic_fulfillment" :daypass="isShopDaypass(product)" :concurrency="options.length ? concurrency : undefined" />

            <fieldset class="mt-5" :disabled="busy">
              <legend class="text-sm font-medium text-gray-900 dark:text-gray-100">支付方式</legend>
              <div class="mt-3 space-y-2">
                <label v-if="methods.includes('alipay')" class="flex min-h-11 cursor-pointer items-center gap-3 rounded-lg border px-3 py-2 text-sm transition-colors" :class="method === 'alipay' ? 'border-primary-500 bg-primary-50 dark:bg-primary-950' : 'border-gray-200 dark:border-gray-700'">
                  <input v-model="method" type="radio" name="payment-method" value="alipay" class="h-5 w-5 shrink-0 accent-primary-700 focus:ring-primary-500" :disabled="busy || !methods.includes('alipay')" />
                  <img :src="alipayIcon" alt="" class="h-5 w-5 shrink-0" />
                  <span class="font-medium text-gray-800 dark:text-gray-200">支付宝</span>
                </label>
                <label v-if="methods.includes('wxpay')" class="flex min-h-11 cursor-pointer items-center gap-3 rounded-lg border px-3 py-2 text-sm transition-colors" :class="method === 'wxpay' ? 'border-primary-500 bg-primary-50 dark:bg-primary-950' : 'border-gray-200 dark:border-gray-700'">
                  <input v-model="method" type="radio" name="payment-method" value="wxpay" class="h-5 w-5 shrink-0 accent-primary-700 focus:ring-primary-500" :disabled="busy || !methods.includes('wxpay')" />
                  <img :src="wxpayIcon" alt="" class="h-5 w-5 shrink-0" />
                  <span class="font-medium text-gray-800 dark:text-gray-200">微信支付</span>
                </label>
              </div>
            </fieldset>

            <p v-if="!methods.length" class="mt-3 text-sm text-gray-600 dark:text-gray-400">暂无可用支付方式，请稍后再试。</p>

            <button type="button" class="mt-5 inline-flex min-h-11 w-full items-center justify-center rounded-md bg-primary-700 px-5 text-sm font-semibold text-white hover:bg-primary-800 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-primary-400 dark:text-gray-950 dark:hover:bg-primary-300 dark:focus:ring-offset-gray-900" :disabled="busy || !methods.includes(method) || !product.enabled || product.price <= 0" :aria-busy="busy" @click="buy">
              购买
            </button>
            <p v-if="error" class="mt-3 break-words text-sm leading-6 text-red-700 dark:text-red-300" role="alert">{{ error }}</p>
          </aside>
        </div>
      </template>
    </div>

    <BaseDialog v-if="checkout" :show="true" :title="`订单支付 · ¥${checkout.pay_amount.toFixed(2)}`" mobile-sheet @close="closeCheckout">
      <div class="shop-checkout">
      <PaymentStatusPanel
        :key="checkout.order_id"
        :order-id="checkout.order_id"
        :amount="checkout.amount"
        :pay-amount="checkout.pay_amount"
        :qr-code="checkout.qr_code || ''"
        :expires-at="checkout.expires_at"
        :payment-type="checkout.payment_type || method"
        :pay-url="checkout.pay_url"
        :checkout-frame-url="checkout.checkout_frame_url"
        :allow-checkout-frame="true"
        :allow-top-level-redirect="recoveryAvailable"
        order-type="manual_shop"
        currency="CNY"
        :out-trade-no="checkout.out_trade_no"
        @done="closeCheckout"
        @settled="onPaymentSettled"
        @success="onPaymentSuccess"
      />
      </div>
    </BaseDialog>
  </AppLayout>
</template>
<style scoped>
.shop-checkout :deep(.card) { padding: 1rem; box-shadow: none; }
.shop-checkout :deep(.card > .space-y-4 > :not([hidden]) ~ :not([hidden])) { margin-top: .75rem; }
.shop-checkout :deep(button) { min-height: 44px; }
</style>
