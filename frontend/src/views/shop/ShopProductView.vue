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
import ShopGuideContent from '@/components/shop/ShopGuideContent.vue'
import ShopSKUSelector from '@/components/shop/ShopSKUSelector.vue'
import ShopDaypassCardSelector from '@/components/shop/ShopDaypassCardSelector.vue'
import { daypassDateTime } from '@/components/shop/daypassCheckout'
import { shopAPI } from '@/api/shop'
import { paymentAPI } from '@/api/payment'
import { clearPaymentRecoverySnapshot, getVisibleMethods, writePaymentRecoverySnapshot } from '@/components/payment/paymentFlow'
import { shopDuration, shopDeliveryLabel, isShopDaypass, type ShopDaypassCards, type ShopDaypassSelection, type ShopPassDurationDays, type ShopProduct } from '@/types/shop'
import type { CreateOrderResult, PaymentOrder } from '@/types/payment'
import { createIdempotencyKey } from '@/utils/idempotency'
const route = useRoute()
const router = useRouter()
const product = ref<ShopProduct | null>(null)
const hasDeliveryTime = computed(() => /5\s*分钟\s*[-–—~～至]\s*2\s*小时/.test(`${product.value?.summary || ''}\n${product.value?.description || ''}`))
const checkout = ref<CreateOrderResult | null>(null)
const recoveryAvailable = ref(false)
const periods = [{ days: 1, label: '日卡', duration: '24 小时' }, { days: 7, label: '周卡', duration: '7 天' }] as const
const paymentLabels = {
  alipay: { name: '支付宝', icon: alipayIcon },
  wxpay: { name: '微信支付', icon: wxpayIcon },
} as const
const durationDays = ref<ShopPassDurationDays>(1)
const concurrency = ref(2)
const selectedId = ref('')
const configuredSKUs = computed(() => product.value?.skus?.filter(sku => sku.enabled) ?? [])
const selectedSKU = computed(() => configuredSKUs.value.find(sku => sku.id === selectedId.value))
const options = computed(() => product.value?.concurrency_options ?? [])
const hasWeekly = computed(() => options.value.length > 0 && options.value.every(option => (option.weekly_price ?? 0) > 0))
const selectedOption = computed(() => options.value.find(option => option.concurrency === concurrency.value))
const selectedPrice = computed(() => product.value?.skus?.length ? selectedSKU.value?.price ?? 0 : durationDays.value === 7
  ? selectedOption.value?.weekly_price ?? 0
  : selectedOption.value?.price ?? product.value?.price ?? 0)
const selectedDuration = computed(() => {
  if (!product.value) return ''
  if (selectedSKU.value) return shopDuration({ ...product.value, ...selectedSKU.value })
  if (options.value.length) return durationDays.value === 7 ? '7 天' : '24 小时'
  return shopDuration(product.value)
})
const cardInventory = ref<ShopDaypassCards>()
const cardLoading = ref(false)
const cardError = ref(false)
const cardMode = ref<'renew' | 'new'>('new')
const selectedCardId = ref<number | null>(null)
const selectedCard = computed(() => cardInventory.value?.cards.find(card => card.card_id === selectedCardId.value))
const cardSelection = computed<ShopDaypassSelection | undefined>(() => {
  if (!product.value?.daypass_cards || !cardInventory.value || cardLoading.value || cardError.value) return
  if (cardMode.value === 'new') return { action: 'new' }
  if (selectedCard.value) return { action: 'renew', card_id: selectedCard.value.card_id }
})
const estimate = computed(() => {
  if (!cardSelection.value || !cardInventory.value) return
  const reference = cardInventory.value.server_time
  const card = cardMode.value === 'renew' ? selectedCard.value : undefined
  const duration = selectedSKU.value?.duration_value ?? (options.value.length ? durationDays.value : product.value?.duration_value)
  if (duration !== 1 && duration !== 7) return
  const end = card?.expires_at ?? 0
  const startsLater = end > reference
  const start = startsLater ? daypassDateTime(end, reference) : '付款后'
  return {
    start,
    end: startsLater ? daypassDateTime(end + duration * 86400, reference) : duration === 7 ? '付款后 7 天' : '付款后 24 小时',
    target: card ? `卡 ${card.card_id} · 尾号 ${card.key_suffix}` : '另购一张卡 · 新 Key',
    effect: !card ? '付款后开始计时' : startsLater ? '沿用原 Key，到期后接续' : '沿用原 Key，付款后恢复',
  }
})
let loadGeneration = 0
async function loadCards(id: number, generation: number, initial: boolean) {
  cardLoading.value = true
  cardError.value = false
  try {
    const result = (await shopAPI.daypassCards(id)).data
    if (generation !== loadGeneration) return
    cardInventory.value = result
    const linkedCard = route.query.card
    if (initial && typeof linkedCard === 'string' && /^\d+$/.test(linkedCard)) {
      selectedCardId.value = result.cards.find(card => card.card_id === Number(linkedCard))?.card_id ?? null
      cardMode.value = 'renew'
      if (!selectedCardId.value) error.value = '这张卡已无法续费，请核对购买方式。'
    } else if (!result.cards.length) {
      if (initial) cardMode.value = 'new'
      selectedCardId.value = null
    } else if (initial) {
      cardMode.value = 'renew'
      selectedCardId.value = result.cards.length === 1 ? result.cards[0]?.card_id ?? null : null
    } else if (selectedCardId.value !== null && !selectedCard.value) {
      selectedCardId.value = null
    }
  } catch {
    if (generation === loadGeneration) cardError.value = true
  } finally {
    if (generation === loadGeneration) cardLoading.value = false
  }
}
function retryCards() {
  if (product.value?.daypass_cards) void loadCards(product.value.id, loadGeneration, !cardInventory.value)
}
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
const attemptKeys = new Map<string, string>()
let checkoutSelection = ''
async function load() {
  const generation = ++loadGeneration
  loading.value = true
  error.value = ''
  try {
    const [item, config] = await Promise.all([shopAPI.product(Number(route.params.id)), paymentAPI.getCheckoutInfo()])
    if (generation !== loadGeneration) return
    product.value = item.data
    selectedId.value = configuredSKUs.value.find(sku => sku.id === item.data.default_sku_id)?.id ?? configuredSKUs.value[0]?.id ?? ''
    durationDays.value = 1
    concurrency.value = item.data.concurrency ?? 2
    methodLimits.value = getVisibleMethods(config.data.methods)
    method.value = methods.value[0] ?? 'alipay'
    if (item.data.daypass_cards) void loadCards(item.data.id, generation, true)
  } catch {
    if (generation === loadGeneration) error.value = '商品加载失败，请刷新重试。'
  } finally {
    if (generation === loadGeneration) loading.value = false
  }
}
const canBuy = computed(() => !!product.value?.enabled && !busy.value && !checkout.value && selectedPrice.value > 0
  && methods.value.includes(method.value) && (!product.value?.daypass_cards || !!cardSelection.value))

async function buy() {
  if (!product.value || !canBuy.value) return
  const generation = loadGeneration
  const card = cardSelection.value
  busy.value = true
  error.value = ''
  try {
    const selection = `${method.value}/${selectedId.value}/${options.value.length ? concurrency.value : 0}/${durationDays.value}/${card?.action ?? ''}/${card?.action === 'renew' ? card.card_id : ''}`
    const attemptKey = attemptKeys.get(selection) ?? createIdempotencyKey('shop-order')
    attemptKeys.set(selection, attemptKey)
    const result = (await shopAPI.createOrder({
      product_id: product.value.id,
      product_revision: product.value.revision,
      payment_type: method.value,
      ...(card ? { daypass: card } : {}),
      ...(product.value.skus?.length ? { sku_id: selectedId.value } : {
        ...(options.value.length ? { concurrency: concurrency.value } : {}),
        ...(durationDays.value === 7 ? { duration_days: durationDays.value } : {})
      })
    }, attemptKey)).data
    if (generation !== loadGeneration) return
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
    checkoutSelection = selection
    checkout.value = result
  } catch (failure) {
    if (generation !== loadGeneration) return
    if (typeof failure === 'object' && failure !== null && 'reason' in failure && failure.reason === 'SHOP_DAYPASS_CARD_UNAVAILABLE') {
      error.value = '这张卡已无法续费，请重新选卡。'
      selectedCardId.value = null
      retryCards()
    } else {
      error.value = '未能创建订单，请重试。若订单已创建，可到“我的订单”继续付款。'
    }
  } finally {
    if (generation === loadGeneration) busy.value = false
  }
}
function onPaymentSettled(outcome: 'success' | 'cancelled' | 'expired') {
  if (outcome === 'cancelled' || outcome === 'expired') {
    attemptKeys.delete(checkoutSelection)
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
watch([() => route.params.id, () => route.query.card], () => {
  product.value = null
  checkout.value = null
  attemptKeys.clear()
  checkoutSelection = ''
  busy.value = false
  cardInventory.value = undefined
  cardLoading.value = false
  cardError.value = false
  selectedCardId.value = null
  void load()
}, { immediate: true })
watch(methods, available => { if (!available.includes(method.value)) method.value = available[0] ?? 'alipay' })
</script>
<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-[1180px]">
      <RouterLink to="/shop" class="mb-2 inline-flex min-h-11 items-center gap-2 rounded-md text-sm font-medium text-gray-600 hover:text-gray-900 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:text-gray-400 dark:hover:text-gray-100 dark:focus:ring-offset-gray-950">
        <Icon name="chevronLeft" class="h-4 w-4" />
        返回商店
      </RouterLink>

      <div v-if="loading" class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_360px] lg:gap-6" aria-busy="true" aria-label="正在加载商品">
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

      <div v-else class="grid items-start gap-3 lg:grid-cols-[minmax(0,1fr)_360px] lg:gap-x-6 lg:gap-y-4">
        <header class="min-w-0 lg:col-span-2 lg:row-start-1">
          <div class="flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span class="badge bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-300">{{ product.category }}</span>
            <span>{{ shopDeliveryLabel(product) }}</span>
          </div>
          <h1 class="mt-2 break-words text-[22px] font-semibold leading-tight tracking-tight text-gray-900 dark:text-gray-100 sm:text-[28px]">{{ product.name }}</h1>
        </header>

        <aside
          class="grid min-w-0 gap-2 rounded-xl border border-gray-200 bg-white px-4 py-3 dark:border-gray-800 dark:bg-gray-900 sm:gap-3 sm:p-5 lg:row-start-2"
          :class="product.daypass_cards ? 'lg:col-span-2 lg:grid-cols-[minmax(0,1fr)_minmax(17rem,.65fr)] lg:items-start lg:gap-6' : 'lg:sticky lg:top-24 lg:col-start-2'"
          aria-label="购买商品"
        >
          <div :class="product.daypass_cards ? 'grid min-w-0 gap-4' : 'contents'">
            <template v-if="product.daypass_cards">
              <p v-if="cardLoading" class="flex min-h-11 items-center text-sm text-gray-600 dark:text-gray-400" role="status">正在读取你的卡…</p>
              <div v-else-if="cardError" class="flex min-w-0 flex-wrap items-center justify-between gap-x-3 rounded-lg bg-gray-50 px-3 text-sm text-gray-700 dark:bg-gray-800 dark:text-gray-300">
                <p role="alert">暂时无法读取你的卡。</p>
                <button type="button" class="min-h-11 rounded-sm font-medium text-primary-700 focus-visible:outline-primary-500 dark:text-primary-300" @click="retryCards">重新读取</button>
              </div>
              <ShopDaypassCardSelector v-else-if="cardInventory" v-model:mode="cardMode" v-model:card-id="selectedCardId" :inventory="cardInventory" :disabled="busy || !!checkout" />
            </template>

          <ShopSKUSelector v-if="configuredSKUs.length" v-model="selectedId" :skus="configuredSKUs" :disabled="busy || !!checkout" />
          <fieldset v-if="!configuredSKUs.length && hasWeekly" class="min-w-0" :disabled="busy || !!checkout">
            <legend class="text-sm font-medium text-gray-900 dark:text-gray-100">有效期</legend>
            <div class="mt-1 grid grid-cols-2 gap-2">
              <label v-for="period in periods" :key="period.days" class="sku-choice" :class="{ 'sku-choice--selected': durationDays === period.days }">
                <input v-model="durationDays" type="radio" name="duration-days" :value="period.days" />
                <span class="min-w-0 leading-4">
                  <strong class="block font-medium">{{ period.label }}</strong>
                  <span class="block text-xs text-gray-600 dark:text-gray-400">{{ period.duration }}</span>
                </span>
              </label>
            </div>
          </fieldset>

          <fieldset v-if="!configuredSKUs.length && options.length" class="min-w-0" :disabled="busy || !!checkout">
            <legend class="text-sm font-medium text-gray-900 dark:text-gray-100">
              并发数量 <span class="ml-1 text-xs font-normal text-gray-500 dark:text-gray-400">同一 Key 的同时请求数</span>
            </legend>
            <div class="mt-1 grid grid-cols-2 gap-2">
              <label v-for="option in options" :key="option.concurrency" class="sku-choice" :class="{ 'sku-choice--selected': concurrency === option.concurrency }">
                <input v-model="concurrency" type="radio" name="concurrency" :value="option.concurrency" />
                <span class="min-w-0 leading-4">
                  <strong class="block font-medium">{{ option.concurrency }} 并发</strong>
                  <span class="block text-xs tabular-nums text-gray-600 dark:text-gray-400">¥{{ (durationDays === 7 ? option.weekly_price : option.price)?.toFixed(2) }} / {{ durationDays === 7 ? '周' : '天' }}</span>
                </span>
              </label>
            </div>
          </fieldset>

          <fieldset v-if="methods.length" class="min-w-0" :disabled="busy || !!checkout">
            <legend class="text-sm font-medium text-gray-900 dark:text-gray-100">支付方式</legend>
            <div class="mt-1 grid gap-2" :class="methods.length > 1 ? 'grid-cols-2' : 'grid-cols-1'">
              <label v-for="available in methods" :key="available" class="sku-choice" :class="{ 'sku-choice--selected': method === available }">
                <input v-model="method" type="radio" name="payment-method" :value="available" />
                <img :src="paymentLabels[available].icon" alt="" class="h-4 w-4 shrink-0" />
                <span class="min-w-0 font-medium">{{ paymentLabels[available].name }}</span>
              </label>
            </div>
          </fieldset>
          <p v-else class="text-sm text-gray-600 dark:text-gray-400">暂无可用支付方式，请稍后再试。</p>
          </div>

          <div :class="product.daypass_cards ? 'grid min-w-0 gap-3 rounded-xl border border-gray-200 bg-gray-50 p-3 dark:border-gray-800 dark:bg-gray-800/50 sm:p-4' : 'contents'">
            <div v-if="product.daypass_cards" class="flex items-baseline justify-between gap-2">
              <h2 class="text-sm font-semibold text-gray-900 dark:text-gray-100">本次订单</h2>
              <span class="text-xs text-gray-600 dark:text-gray-400">北京时间</span>
            </div>
            <dl class="flex items-end justify-between gap-3 border-b border-gray-200 pb-3 dark:border-gray-700" :class="{ 'order-first': !product.daypass_cards }">
              <div>
                <dt class="sr-only text-xs text-gray-500 dark:text-gray-400 sm:not-sr-only">价格</dt>
                <dd aria-live="polite" aria-atomic="true" class="text-2xl font-semibold leading-none tabular-nums text-gray-900 dark:text-gray-100 sm:mt-1">¥{{ selectedPrice.toFixed(2) }}</dd>
              </div>
              <div class="text-right">
                <dt class="sr-only text-xs text-gray-500 dark:text-gray-400 sm:not-sr-only">时长</dt>
                <dd class="text-sm font-medium text-gray-800 dark:text-gray-200 sm:mt-1">{{ selectedDuration }}<template v-if="product.daypass_cards"> · {{ selectedSKU?.concurrency || concurrency }} 并发</template></dd>
              </div>
            </dl>
            <div v-if="product.daypass_cards" aria-live="polite">
              <template v-if="estimate">
                <p class="text-sm font-semibold text-gray-900 dark:text-gray-100">{{ estimate.target }}</p>
                <p class="mt-0.5 text-xs text-gray-600 dark:text-gray-400">{{ estimate.effect }}</p>
                <dl class="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs tabular-nums text-gray-700 dark:text-gray-300">
                  <dt class="text-gray-600 dark:text-gray-400">预计生效</dt><dd>{{ estimate.start }}</dd>
                  <dt class="text-gray-600 dark:text-gray-400">预计到期</dt><dd>{{ estimate.end }}</dd>
                </dl>
              </template>
              <p v-else-if="cardInventory && !cardError && !cardLoading" class="text-sm text-gray-600 dark:text-gray-400">{{ cardInventory.cards.length ? '请选择一张要续费的卡。' : '可选择另购一张新卡。' }}</p>
            </div>
            <button type="button" class="inline-flex min-h-11 w-full items-center justify-center rounded-md bg-primary-700 px-5 text-sm font-semibold text-white hover:bg-primary-800 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-primary-400 dark:text-gray-950 dark:hover:bg-primary-300 dark:focus:ring-offset-gray-900" :disabled="!canBuy" :aria-busy="busy" @click="buy">
              {{ product.daypass_cards ? '确认并付款' : '购买' }}
            </button>
            <p v-if="error" class="break-words text-sm leading-6 text-red-700 dark:text-red-300" role="alert">{{ error }}</p>
          </div>
        </aside>

        <article class="min-w-0 rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-gray-900 sm:p-5 lg:col-start-1" :class="product.daypass_cards ? 'lg:col-span-2 lg:row-start-3' : 'lg:row-start-2'">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">商品详情</h2>
          <p v-if="product.summary" class="mt-3 whitespace-pre-wrap break-words text-sm leading-6 text-gray-700 dark:text-gray-300">{{ product.summary }}</p>
          <div v-if="product.description" class="mt-3 min-w-0 text-sm leading-7 text-gray-700 dark:text-gray-300">
            <ShopMarkdown :content="product.description" />
          </div>
          <p v-else-if="!product.summary" class="mt-3 text-sm text-gray-500 dark:text-gray-400">暂无更多商品说明。</p>
          <ShopNotice class="mt-4 border-t border-gray-200 pt-3 dark:border-gray-800" :show-delivery-time="!hasDeliveryTime" :automatic="product.automatic_fulfillment" :daypass="isShopDaypass(product)" :concurrency="selectedSKU?.concurrency || (options.length ? concurrency : undefined)" :duration-days="selectedSKU?.duration_unit === 'day' && selectedSKU.duration_value === 7 ? 7 : durationDays" />
          <section v-if="product.guide_key" class="mt-4 border-t border-gray-200 pt-4 dark:border-gray-800" aria-labelledby="product-guide-title">
            <h3 id="product-guide-title" class="text-base font-semibold text-gray-900 dark:text-gray-100">使用指南</h3>
            <ShopGuideContent v-if="isShopDaypass(product)" :guide-key="product.guide_key" embedded class="mt-3" />
            <details v-else class="group mt-2">
              <summary class="flex min-h-11 cursor-pointer list-none items-center justify-between gap-2 rounded-md bg-gray-50 px-3 py-2 text-sm font-medium text-gray-800 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500 dark:bg-gray-800/70 dark:text-gray-200 [&::-webkit-details-marker]:hidden">
                登录、账号安全与售后（图文）
                <Icon name="chevronDown" class="h-4 w-4 shrink-0 group-open:rotate-180" aria-hidden="true" />
              </summary>
              <ShopGuideContent :guide-key="product.guide_key" embedded class="mt-3" />
            </details>
          </section>
        </article>
      </div>
    </div>

    <BaseDialog v-if="checkout" :show="true" :title="`订单支付 · ¥${checkout.pay_amount.toFixed(2)}`" mobile-sheet @close="closeCheckout">
      <div class="shop-checkout">
      <p v-if="estimate" class="mb-3 rounded-lg bg-gray-50 px-3 py-2 text-sm text-gray-700 dark:bg-gray-800 dark:text-gray-300">{{ estimate.target }} · {{ selectedDuration }}</p>
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
.sku-choice {
  @apply flex min-h-11 min-w-0 cursor-pointer items-center gap-1.5 rounded-lg border border-gray-200 px-2.5 py-1 text-sm text-gray-800 transition-colors hover:border-gray-400 focus-within:ring-2 focus-within:ring-primary-500 dark:border-gray-700 dark:text-gray-200 dark:hover:border-gray-500 sm:gap-2;
}
.sku-choice input {
  @apply h-4 w-4 shrink-0 accent-primary-700 focus:outline-none focus:ring-0 focus:ring-offset-0;
}
.sku-choice--selected {
  @apply border-primary-500 bg-primary-50 hover:border-primary-500 dark:border-primary-600 dark:bg-primary-950 dark:hover:border-primary-600;
}
fieldset:disabled .sku-choice {
  @apply cursor-not-allowed opacity-70;
}
.shop-checkout :deep(.card) { padding: 1rem; box-shadow: none; }
.shop-checkout :deep(.card > .space-y-4 > :not([hidden]) ~ :not([hidden])) { margin-top: .75rem; }
.shop-checkout :deep(button) { min-height: 44px; }
</style>
