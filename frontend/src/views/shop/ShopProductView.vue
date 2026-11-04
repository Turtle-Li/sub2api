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
import { getVisibleMethods } from '@/components/payment/paymentFlow'
import { shopDuration, shopModeLabels, type ShopProduct } from '@/types/shop'
import type { CreateOrderResult } from '@/types/payment'
const route = useRoute()
const router = useRouter()
const product = ref<ShopProduct | null>(null)
const hasDeliveryTime = computed(() => /5\s*分钟\s*[-–—~～至]\s*2\s*小时/.test(`${product.value?.summary || ''}\n${product.value?.description || ''}`))
const checkout = ref<CreateOrderResult | null>(null)
const methods = ref<('alipay' | 'wxpay')[]>([])
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
    const visible = getVisibleMethods(config.data.methods)
    methods.value = (['alipay', 'wxpay'] as const).filter(key => {
      const limit = visible[key]
      return limit && limit.available !== false
        && (!limit.currency || limit.currency === 'CNY')
        && (!(limit.single_min > 0) || item.data.price >= limit.single_min)
        && (!(limit.single_max > 0) || item.data.price <= limit.single_max)
    })
    method.value = methods.value[0] ?? 'alipay'
  } catch { error.value = '商品加载失败，请刷新重试。' }
  finally { loading.value = false }
}
async function buy() {
  if (!product.value || busy.value || !methods.value.includes(method.value)) return
  busy.value = true
  error.value = ''
  if (!attemptKey || attemptMethod !== method.value) { attemptKey = crypto.randomUUID(); attemptMethod = method.value }
  try {
    checkout.value = (await shopAPI.createOrder({ product_id: product.value.id, product_revision: product.value.revision, payment_type: method.value }, attemptKey)).data
  } catch { error.value = '未能创建订单，请重试。若订单已创建，可到“我的订单”继续付款。' }
  finally { busy.value = false }
}
function openOrder() { if (checkout.value) void router.push(`/shop/orders/${checkout.value.order_id}`) }
watch(() => route.params.id, () => { product.value = null; checkout.value = null; attemptKey = ''; void load() }, { immediate: true })
</script>
<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-5">
      <div>
        <RouterLink
          to="/shop"
          class="inline-flex min-h-[44px] items-center gap-1.5 rounded-xl px-3 py-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-gray-300 dark:hover:bg-dark-800 dark:hover:text-white"
        >
          <Icon name="chevronLeft" size="sm" />
          <span>返回商城</span>
        </RouterLink>
      </div>

      <div v-if="loading" role="status" class="card flex items-center justify-center gap-2.5 py-16 text-sm text-gray-500 dark:text-gray-400">
        <Icon name="clock" size="md" class="animate-spin text-primary-500" />
        <span>正在加载商品…</span>
      </div>

      <div v-if="error && !product" role="alert" class="card flex flex-col items-start justify-between gap-4 border-red-200/80 p-5 sm:flex-row sm:items-center dark:border-red-900/40">
        <p class="text-sm text-gray-700 dark:text-gray-200">{{ error }}</p>
        <button v-if="!product" class="btn btn-secondary min-h-[44px] shrink-0 px-5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500" @click="load">重试</button>
      </div>

      <template v-if="product">
        <div class="grid grid-cols-1 items-start gap-6 lg:grid-cols-[minmax(0,1fr)_360px]">
          <section class="card min-w-0 overflow-hidden border border-gray-200/80 p-5 sm:p-8 dark:border-dark-700">
            <h1 class="break-words text-2xl font-bold tracking-tight text-gray-900 sm:text-3xl dark:text-white">{{ product.name }}</h1>

            <div class="mt-3 flex flex-wrap items-center gap-2">
              <span
                v-if="product.category"
                class="inline-flex items-center rounded-md bg-primary-50 px-2.5 py-1 text-xs font-medium text-primary-700 dark:bg-primary-950/60 dark:text-primary-300"
              >
                {{ product.category }}
              </span>
              <span class="inline-flex items-center gap-1.5 rounded-md bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-300">
                <Icon :name="product.fulfillment_mode === 'session_topup' ? 'key' : 'user'" size="xs" class="text-primary-600 dark:text-primary-400" />
                <span>{{ shopModeLabels[product.fulfillment_mode] }}</span>
              </span>
            </div>

            <p
              v-if="product.summary"
              class="mt-5 whitespace-pre-wrap break-words text-sm leading-6 text-gray-600 dark:text-gray-300"
            >{{ product.summary }}</p>

            <div class="mt-6 border-t border-gray-100 pt-6 dark:border-dark-700/80">
              <div class="min-w-0 break-words">
                <ShopMarkdown v-if="product.description" :content="product.description" />
                <p v-else class="text-sm text-gray-500 dark:text-gray-400">暂无详细说明，可联系客服了解商品。</p>
              </div>
            </div>
          </section>

          <aside class="card min-w-0 space-y-5 border border-gray-200/80 p-5 sm:p-6 lg:sticky lg:top-6 dark:border-dark-700">
            <div class="border-b border-gray-100 pb-4 dark:border-dark-700/80">
              <div class="flex items-center justify-between gap-2">
                <span class="text-xs font-medium text-gray-500 dark:text-gray-400">售价</span>
              </div>
              <div class="mt-2 flex items-baseline gap-1.5">
                <strong class="text-2xl font-bold tabular-nums text-gray-900 sm:text-3xl dark:text-white">¥{{ product.price.toFixed(2) }}</strong>
                <span class="text-sm text-gray-500 dark:text-gray-400">/ {{ shopDuration(product) }}</span>
              </div>
            </div>

            <RouterLink
              v-if="product.guide_key"
              :to="`/shop/help/${product.guide_key}`"
              class="flex min-h-[44px] items-center justify-between gap-2 rounded-xl border border-primary-200 bg-primary-50/70 px-3.5 py-2.5 text-sm font-medium text-primary-900 transition-colors hover:bg-primary-100/80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:border-primary-800/60 dark:bg-primary-950/40 dark:text-primary-200 dark:hover:bg-primary-900/50"
            >
              <span class="inline-flex items-center gap-2">
                <Icon name="book" size="sm" class="shrink-0 text-primary-600 dark:text-primary-400" />
                <span>购买前查看使用教程</span>
              </span>
              <span class="inline-flex shrink-0 items-center gap-1 rounded-md bg-primary-600 px-2 py-0.5 text-xs font-semibold text-white dark:bg-primary-400 dark:text-dark-950">
                <span>必看</span>
                <Icon name="chevronRight" size="xs" />
              </span>
            </RouterLink>

            <fieldset class="space-y-2.5">
              <legend class="flex items-center gap-1.5 text-sm font-medium text-gray-900 dark:text-white">
                <Icon name="creditCard" size="sm" class="text-gray-400 dark:text-gray-500" />
                <span>支付方式</span>
              </legend>
              <div v-if="methods.length" class="grid grid-cols-2 gap-2.5">
                <label
                  v-for="item in methods"
                  :key="item"
                  class="relative flex min-h-[44px] cursor-pointer items-center justify-between gap-2 rounded-xl border px-3.5 py-2.5 text-sm font-medium transition-colors focus-within:ring-2 focus-within:ring-primary-500 focus-within:ring-offset-1 dark:focus-within:ring-offset-dark-800"
                  :class="method === item ? 'border-primary-500 bg-primary-50/60 text-primary-900 dark:border-primary-400 dark:bg-primary-950/40 dark:text-white' : 'border-gray-200 bg-white text-gray-700 hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200 dark:hover:bg-dark-700'"
                >
                  <input v-model="method" type="radio" :value="item" name="payment-method" class="sr-only">
                  <span class="inline-flex items-center gap-2"><img :src="item === 'alipay' ? alipayIcon : wxpayIcon" alt="" class="h-5 w-5 shrink-0"><span>{{ item === 'alipay' ? '支付宝' : '微信' }}</span></span>
                  <Icon v-if="method === item" name="check" size="sm" class="shrink-0 text-primary-600 dark:text-primary-400" />
                </label>
              </div>
              <p v-else class="rounded-xl border border-gray-200/80 bg-gray-50 px-3.5 py-3 text-sm text-gray-500 dark:border-dark-700 dark:bg-dark-800/60 dark:text-gray-400">暂无可用支付方式</p>
            </fieldset>

            <p v-if="error" role="alert" class="text-sm leading-6 text-red-700 dark:text-red-300">{{ error }}</p>
            <button
              class="btn btn-primary min-h-[44px] w-full text-base font-semibold focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500"
              :disabled="busy || !methods.length || !product.enabled || product.price <= 0"
              @click="buy"
            >
              {{ busy ? '正在创建订单…' : '购买' }}
            </button>

            <div class="space-y-3 border-t border-gray-100 pt-4 dark:border-dark-700/80">
              <div class="flex items-start gap-2 text-xs leading-5 text-gray-600 dark:text-gray-300">
                <Icon name="mail" size="sm" class="mt-0.5 shrink-0 text-primary-600 dark:text-primary-400" />
                <p>{{ product.fulfillment_mode === 'session_topup' ? '付款 → 提交完整资料 → 等待人工处理 → 邮件通知' : '付款 → 等待人工发货 → 在订单和邮箱查看交付信息' }}</p>
              </div>
              <ShopNotice :show-delivery-time="!hasDeliveryTime" />
            </div>
          </aside>
        </div>
      </template>
    </div>

    <BaseDialog :show="!!checkout" title="商城订单付款" mobile-sheet @close="openOrder">
      <PaymentStatusPanel v-if="checkout" :key="checkout.order_id" :order-id="checkout.order_id" :amount="checkout.amount" :pay-amount="checkout.pay_amount" :qr-code="checkout.qr_code || ''" :expires-at="checkout.expires_at" :payment-type="checkout.payment_type || method" :pay-url="checkout.pay_url" :checkout-frame-url="checkout.checkout_frame_url" :allow-checkout-frame="true" order-type="manual_shop" currency="CNY" :out-trade-no="checkout.out_trade_no" @done="openOrder" @settled="openOrder" />
    </BaseDialog>
  </AppLayout>
</template>
