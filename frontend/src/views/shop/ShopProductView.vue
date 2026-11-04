<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
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
    methods.value = (['alipay', 'wxpay'] as const).filter(key => visible[key]?.available)
    method.value = methods.value[0] ?? 'alipay'
  } catch { error.value = '商品加载失败，请刷新重试。' }
  finally { loading.value = false }
}
async function buy() {
  if (!product.value || busy.value) return
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
    <div class="mx-auto max-w-5xl space-y-6">
      <RouterLink to="/shop" class="text-sm text-primary-700 dark:text-primary-300">← 返回商城</RouterLink>
      <p v-if="loading" role="status">正在加载商品…</p>
      <div v-if="error" role="alert" class="card p-4"><p>{{ error }}</p><button v-if="!product" class="btn btn-secondary mt-3" @click="load">重试</button></div>
      <template v-if="product">
        <div class="grid items-start gap-6 lg:grid-cols-[1fr_320px]">
          <section class="card p-6 sm:p-8">
            <p class="text-sm text-primary-700 dark:text-primary-300">{{ product.category }}</p>
            <h1 class="mt-3 break-words text-3xl font-bold text-gray-900 dark:text-white">{{ product.name }}</h1>
            <p class="my-5 whitespace-pre-wrap break-words text-gray-500 dark:text-gray-400">{{ product.summary }}</p>
            <ShopMarkdown v-if="product.description" :content="product.description" />
            <p v-else class="text-sm text-gray-500 dark:text-gray-400">暂无详细说明，可联系客服了解商品。</p>
          </section>
          <aside class="card space-y-5 p-6">
            <div><strong class="text-3xl font-bold text-gray-900 dark:text-white">¥{{ product.price.toFixed(2) }}</strong><span class="ml-2 text-sm text-gray-500 dark:text-gray-400">/ {{ shopDuration(product) }}</span></div>
            <p class="text-sm">{{ shopModeLabels[product.fulfillment_mode] }}</p>
            <RouterLink v-if="product.guide_key" :to="`/shop/help/${product.guide_key}`" class="block rounded-lg bg-primary-50 p-3 text-sm font-medium text-primary-800 underline dark:bg-primary-950/30 dark:text-primary-200">购买前查看使用教程 →</RouterLink>
            <fieldset><legend class="mb-2 text-sm font-medium">支付方式</legend><div class="flex gap-2"><label v-for="item in methods" :key="item" class="flex cursor-pointer items-center gap-2 rounded-lg border border-gray-200 p-3 dark:border-dark-600"><input v-model="method" type="radio" :value="item" name="payment-method">{{ item === 'alipay' ? '支付宝' : '微信' }}</label></div><p v-if="!methods.length" class="text-sm text-gray-500 dark:text-gray-400">暂无可用支付方式</p></fieldset>
            <button class="btn btn-primary w-full" :disabled="busy || !methods.length || !product.enabled || product.price <= 0" @click="buy">{{ busy ? '正在创建订单…' : '购买' }}</button>
            <ShopNotice :show-delivery-time="!hasDeliveryTime" />
            <p class="text-xs leading-6 text-gray-500 dark:text-gray-400">{{ product.fulfillment_mode === 'session_topup' ? '付款 → 提交完整资料 → 等待人工处理 → 邮件通知' : '付款 → 等待人工发货 → 在订单和邮箱查看交付信息' }}</p>
          </aside>
        </div>
      </template>
    </div>
    <BaseDialog :show="!!checkout" title="商城订单付款" mobile-sheet @close="openOrder">
      <PaymentStatusPanel v-if="checkout" :key="checkout.order_id" :order-id="checkout.order_id" :amount="checkout.amount" :pay-amount="checkout.pay_amount" :qr-code="checkout.qr_code || ''" :expires-at="checkout.expires_at" :payment-type="checkout.payment_type || method" :pay-url="checkout.pay_url" :checkout-frame-url="checkout.checkout_frame_url" :allow-checkout-frame="true" order-type="manual_shop" currency="CNY" :out-trade-no="checkout.out_trade_no" @done="openOrder" @settled="openOrder" />
    </BaseDialog>
  </AppLayout>
</template>
