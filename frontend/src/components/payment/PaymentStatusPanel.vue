<template>
  <div class="space-y-4">
    <section v-if="paymentDiscount" class="rounded-lg border border-emerald-200 bg-emerald-50/60 p-3 text-sm dark:border-emerald-900/70 dark:bg-emerald-950/20" data-test="payment-discount-confirmation">
      <div class="flex items-center justify-between gap-3">
        <span class="min-w-0 break-all font-medium text-emerald-900 dark:text-emerald-100">{{ t('payment.coupon.appliedCode', { code: paymentDiscount.code }) }}</span>
        <span class="shrink-0 font-semibold text-emerald-900 dark:text-emerald-100"><span class="mr-1 text-xs font-normal">{{ t('payment.actualPay') }}</span>{{ formatGatewayAmount(paymentDiscount.pay_amount, paymentDiscount.currency) }}</span>
      </div>
      <dl class="mt-2 space-y-1 text-xs text-emerald-800 dark:text-emerald-200">
        <div class="flex justify-between gap-4">
          <dt>{{ t('payment.coupon.originalAmount') }}</dt>
          <dd>{{ formatGatewayAmount(paymentDiscount.original_amount, paymentDiscount.currency) }}</dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt>{{ t('payment.coupon.discount') }}</dt>
          <dd>-{{ formatGatewayAmount(paymentDiscount.discount_amount, paymentDiscount.currency) }}</dd>
        </div>
      </dl>
    </section>
    <!-- ═══ Terminal States: show result, user clicks to return ═══ -->

    <!-- Success -->
    <template v-if="outcome === 'success'">
      <div class="card p-6">
        <div class="flex flex-col items-center space-y-4 py-4">
          <div class="flex h-16 w-16 items-center justify-center rounded-full bg-green-100 dark:bg-green-900/30">
            <Icon name="check" size="lg" class="text-green-500" />
          </div>
          <p class="text-lg font-bold text-gray-900 dark:text-white">{{ props.orderType === 'subscription' ? t('payment.result.subscriptionSuccess') : t('payment.result.success') }}</p>
          <div v-if="paidOrder" class="w-full rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
            <div class="space-y-2 text-sm">
              <div class="flex items-start justify-between gap-3">
                <span class="shrink-0 text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
                <span class="min-w-0 font-medium text-right text-gray-900 dark:text-white">#{{ paidOrder.id }}</span>
              </div>
              <div v-if="paidOrder.out_trade_no" class="flex items-start justify-between gap-3">
                <span class="shrink-0 text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</span>
                <div class="flex min-w-0 items-start justify-end gap-1">
                  <code data-test="payment-result-order-number" class="min-w-0 break-all text-right font-mono text-xs text-gray-900 dark:text-white">{{ paidOrder.out_trade_no }}</code>
                  <button
                    data-test="copy-payment-result-order"
                    type="button"
                    class="inline-flex shrink-0 rounded p-1 text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-gray-200"
                    :aria-label="t('payment.orderOps.copyOrder')"
                    :title="t('payment.orderOps.copyOrder')"
                    @click="copyOrderNumber(paidOrder.out_trade_no)"
                  >
                    <Icon name="copy" size="xs" />
                  </button>
                </div>
              </div>
              <div v-if="paidOrder.order_type === 'balance'" class="flex items-start justify-between gap-3">
                <span class="shrink-0 text-gray-500 dark:text-gray-400">{{ t('payment.orders.creditedAmount') }}</span>
                <span class="min-w-0 text-right font-medium text-gray-900 dark:text-white">{{ creditedAmountSymbol }}{{ paidOrder.amount.toFixed(2) }}</span>
              </div>
              <div class="flex items-start justify-between gap-3">
                <span class="shrink-0 text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</span>
                <span class="min-w-0 text-right font-medium text-gray-900 dark:text-white">{{ formatGatewayAmount(paidOrder.pay_amount, paidOrder.currency) }}</span>
              </div>
            </div>
          </div>
          <button class="btn btn-primary" @click="handleDone">{{ t('common.confirm') }}</button>
        </div>
      </div>
    </template>

    <!-- Cancelled -->
    <template v-else-if="outcome === 'cancelled'">
      <div class="card p-6">
        <div class="flex flex-col items-center space-y-4 py-4">
          <div class="flex h-16 w-16 items-center justify-center rounded-full bg-gray-100 dark:bg-dark-700">
            <svg class="h-8 w-8 text-gray-400 dark:text-gray-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </div>
          <p class="text-lg font-bold text-gray-900 dark:text-white">{{ t('payment.qr.cancelled') }}</p>
          <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.qr.cancelledDesc') }}</p>
          <button class="btn btn-primary" @click="handleDone">{{ t('common.confirm') }}</button>
        </div>
      </div>
    </template>

    <!-- Expired / Failed -->
    <template v-else-if="outcome === 'expired'">
      <div class="card p-6">
        <div class="flex flex-col items-center space-y-4 py-4">
          <div class="flex h-16 w-16 items-center justify-center rounded-full bg-orange-100 dark:bg-orange-900/30">
            <svg class="h-8 w-8 text-orange-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </div>
          <p class="text-lg font-bold text-gray-900 dark:text-white">{{ t('payment.qr.expired') }}</p>
          <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.qr.expiredDesc') }}</p>
          <button class="btn btn-primary" @click="handleDone">{{ t('common.confirm') }}</button>
        </div>
      </div>
    </template>

    <!-- The provider accepted cancellation, but the local order is still pending. -->
    <template v-else-if="cancellationPending || props.initialCancellationPending">
      <div data-test="payment-cancellation-pending" class="card p-6">
        <div class="flex flex-col items-center space-y-3 py-3 text-center">
          <div class="flex h-12 w-12 items-center justify-center rounded-full bg-amber-100 dark:bg-amber-900/30">
            <Icon name="sync" size="lg" class="text-amber-600 dark:text-amber-300" />
          </div>
          <p class="text-base font-semibold text-gray-900 dark:text-white">{{ t('payment.orderOps.cancellationPending') }}</p>
          <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.result.processingHint') }}</p>
        </div>
      </div>
      <button v-if="pollExhausted" class="btn btn-secondary w-full" @click="refreshNow">
        {{ t('payment.qr.refreshStatus') }}
      </button>
    </template>

    <!-- Never present a terminal result while the gateway state is unknown. -->
    <template v-else-if="confirmationPending || props.initialConfirmationPending">
      <div data-test="payment-confirmation-pending" class="card p-6">
        <div class="flex flex-col items-center space-y-3 py-3 text-center">
          <div class="flex h-12 w-12 items-center justify-center rounded-full bg-blue-50 dark:bg-blue-950/30">
            <Icon name="sync" size="lg" class="animate-spin text-primary-600 dark:text-primary-300" />
          </div>
          <p class="text-base font-semibold text-gray-900 dark:text-white">{{ t('payment.orderOps.confirmationPending') }}</p>
          <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.result.processingHint') }}</p>
        </div>
      </div>
      <button v-if="pollExhausted" class="btn btn-secondary w-full" @click="refreshNow">
        {{ t('payment.qr.refreshStatus') }}
      </button>
    </template>

    <!-- A browser clock only prompts an authoritative status check. -->
    <template v-else-if="deadlineReached && !paymentReceivedHint">
      <div data-test="payment-expiry-checking" class="card p-6">
        <div class="flex flex-col items-center space-y-3 py-3 text-center">
          <div class="flex h-12 w-12 items-center justify-center rounded-full bg-gray-100 dark:bg-dark-700">
            <Icon name="sync" size="lg" class="animate-spin text-gray-600 dark:text-gray-300" />
          </div>
          <p class="text-base font-semibold text-gray-900 dark:text-white">{{ t('payment.orderOps.paymentExpiryChecking') }}</p>
          <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.result.processingHint') }}</p>
        </div>
      </div>
      <button v-if="pollExhausted" class="btn btn-secondary w-full" @click="refreshNow">
        {{ t('payment.qr.refreshStatus') }}
      </button>
    </template>

    <!-- ═══ Active States: QR or Popup waiting ═══ -->

    <!-- Mobile Alipay app handoff. The QR fallback stays hidden until launch timeout. -->
    <template v-else-if="isMobileAlipayDeepLink">
      <template v-if="!deepLinkFallbackVisible">
        <div class="card p-6">
          <div class="flex flex-col items-center space-y-4 py-4 text-center">
            <div
              v-if="deepLinkState === 'launching'"
              class="h-10 w-10 animate-spin rounded-full border-4 border-[#00AEEF] border-t-transparent"
            ></div>
            <div
              v-else
              class="flex h-12 w-12 items-center justify-center rounded-full bg-blue-50 dark:bg-blue-950/30"
            >
              <Icon name="checkCircle" size="lg" class="text-[#00AEEF]" />
            </div>
            <p class="text-lg font-semibold text-gray-900 dark:text-white">
              {{ deepLinkState === 'backgrounded' ? t('payment.qr.alipayContinueInApp') : t('payment.qr.alipayOpening') }}
            </p>
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.qr.alipayWaitingHint') }}</p>
            <button
              v-if="deepLinkState === 'backgrounded'"
              data-test="reopen-alipay"
              class="btn btn-alipay inline-flex items-center gap-2 text-sm"
              :disabled="resumingLaunch"
              @click="reopenAlipay"
            >
              <Icon name="externalLink" size="sm" />
              {{ t('payment.qr.reopenAlipay') }}
            </button>
          </div>
        </div>
        <div class="card p-4 text-center">
          <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.qr.expiresIn') }}</p>
          <p class="mt-1 text-2xl font-bold tabular-nums text-gray-900 dark:text-white">{{ countdownDisplay }}</p>
          <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">{{ waitingHint }}</p>
        </div>
      </template>
      <template v-else>
        <div data-test="alipay-qr-fallback" class="card p-6">
          <div class="flex flex-col items-center space-y-4">
            <div class="text-center">
              <p class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('payment.qr.alipayFallbackTitle') }}</p>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('payment.qr.alipayFallbackHint') }}</p>
            </div>
            <div class="w-full space-y-2 border-y border-gray-100 py-3 text-sm dark:border-dark-600">
              <div class="flex items-start justify-between gap-4">
                <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</span>
                <span class="font-semibold text-gray-900 dark:text-white">{{ displayPaymentAmount }}</span>
              </div>
              <div class="flex items-start justify-between gap-4">
                <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</span>
                <span class="max-w-[70%] break-all text-right font-mono text-xs text-gray-900 dark:text-white">
                  {{ displayOrderNumber }}
                </span>
              </div>
              <div class="flex items-start justify-between gap-4">
                <span class="text-gray-500 dark:text-gray-400">{{ t('payment.qr.expiresIn') }}</span>
                <span class="font-semibold tabular-nums text-gray-900 dark:text-white">{{ countdownDisplay }}</span>
              </div>
            </div>
            <div :class="['relative rounded-lg border-2 p-4', qrBorderClass]">
              <canvas :key="sessionVersion" ref="qrCanvas" class="mx-auto"></canvas>
              <div class="pointer-events-none absolute inset-0 flex items-center justify-center">
                <span :class="['rounded-full p-2 shadow ring-2 ring-white', qrLogoBgClass]">
                  <img :src="qrLogoIcon" alt="" class="h-5 w-5 brightness-0 invert" />
                </span>
              </div>
            </div>
            <p class="text-center text-sm leading-6 text-gray-600 dark:text-gray-300">
              {{ t('payment.qr.alipaySaveAndScanHint') }}
            </p>
            <div class="grid w-full gap-2 sm:grid-cols-2">
              <button
                data-test="reopen-alipay"
                class="btn btn-alipay inline-flex items-center justify-center gap-2"
                :disabled="resumingLaunch"
                @click="reopenAlipay"
              >
                <Icon name="externalLink" size="sm" />
                {{ t('payment.qr.reopenAlipay') }}
              </button>
              <button
                data-test="save-alipay-qr"
                class="btn btn-secondary inline-flex items-center justify-center gap-2"
                @click="saveQRCode"
              >
                <Icon name="download" size="sm" />
                {{ t('payment.qr.saveQRCode') }}
              </button>
            </div>
            <button class="btn btn-secondary w-full" @click="handleDone">
              {{ t('payment.result.backToRecharge') }}
            </button>
          </div>
        </div>
      </template>
    </template>

    <!-- QR Code Mode -->
    <template v-else-if="showQRCode">
      <div class="card p-6">
        <div class="flex flex-col items-center space-y-4">
          <p class="text-lg font-semibold text-gray-900 dark:text-white">{{ scanTitle }}</p>
          <p v-if="paymentReceivedHint" class="text-center text-sm text-amber-600 dark:text-amber-300">{{ paymentReceivedHint }}</p>
          <div :class="['relative rounded-lg border-2 p-4', qrBorderClass]">
            <canvas :key="sessionVersion" ref="qrCanvas" class="mx-auto"></canvas>
            <!-- Brand logo overlay -->
            <div class="pointer-events-none absolute inset-0 flex items-center justify-center">
              <span :class="['rounded-full p-2 shadow ring-2 ring-white', qrLogoBgClass]">
                <img :src="qrLogoIcon" alt="" class="h-5 w-5 brightness-0 invert" />
              </span>
            </div>
          </div>
          <p v-if="scanHint" class="text-center text-sm text-gray-500 dark:text-gray-400">{{ scanHint }}</p>
          <button v-if="currentHostedPayUrl" class="btn btn-secondary text-sm" :disabled="resumingLaunch" @click="reopenPopup">
            {{ t('payment.qr.openPayWindow') }}
          </button>
        </div>
      </div>
      <div class="card p-4 text-center">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.qr.expiresIn') }}</p>
        <p class="mt-1 text-2xl font-bold tabular-nums text-gray-900 dark:text-white">{{ countdownDisplay }}</p>
        <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">{{ waitingHint }}</p>
      </div>
      <button v-if="pollExhausted" class="btn btn-secondary w-full" @click="refreshNow">
        {{ t('payment.qr.refreshStatus') }}
      </button>
      <button class="btn btn-secondary w-full" :disabled="cancelling" @click="handleCancel">
        {{ cancelling ? t('common.processing') : t('payment.qr.cancelOrder') }}
      </button>
    </template>

    <!-- iframe Checkout Mode (e.g. Alipay page-pay with qr_pay_mode=4) -->
    <template v-else-if="showCheckoutFrame">
      <div class="card p-6">
        <div class="flex flex-col items-center space-y-4">
          <p class="text-lg font-semibold text-gray-900 dark:text-white">{{ scanTitle }}</p>
          <p v-if="paymentReceivedHint" class="text-center text-sm text-amber-600 dark:text-amber-300">{{ paymentReceivedHint }}</p>
          <div :class="['relative overflow-hidden rounded-lg border-2 p-2 bg-white flex items-center justify-center', qrBorderClass]" style="width: 236px; height: 236px;">
            <div v-if="iframeLoading" class="absolute inset-0 flex items-center justify-center bg-white/90 dark:bg-dark-800/90 z-10">
              <div class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"></div>
            </div>
            <iframe
              data-test="alipay-checkout-frame"
              :src="currentCheckoutFrameUrl"
              class="border-0"
              style="width: 220px; height: 220px; overflow: hidden; display: block;"
              scrolling="no"
              frameborder="0"
              @load="onIframeLoad"
              @error="onIframeError"
            />
          </div>
          <p v-if="scanHint" class="text-center text-sm text-gray-500 dark:text-gray-400">{{ scanHint }}</p>
          <button v-if="currentHostedPayUrl" class="btn btn-secondary text-sm" :disabled="resumingLaunch" @click="reopenPopup">
            {{ t('payment.qr.openPayWindow') }}
          </button>
        </div>
      </div>
      <div class="card p-4 text-center">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.qr.expiresIn') }}</p>
        <p class="mt-1 text-2xl font-bold tabular-nums text-gray-900 dark:text-white">{{ countdownDisplay }}</p>
        <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">{{ waitingHint }}</p>
      </div>
      <button v-if="pollExhausted" class="btn btn-secondary w-full" @click="refreshNow">
        {{ t('payment.qr.refreshStatus') }}
      </button>
      <button class="btn btn-secondary w-full" :disabled="cancelling" @click="handleCancel">
        {{ cancelling ? t('common.processing') : t('payment.qr.cancelOrder') }}
      </button>
    </template>

    <!-- Waiting for Popup/Redirect Mode -->
    <template v-else>
      <div class="card p-6">
        <div class="flex flex-col items-center space-y-4 py-4">
          <div class="h-10 w-10 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
          <p class="text-center text-sm text-gray-500 dark:text-gray-400">{{ waitingHint }}</p>
          <button v-if="currentHostedPayUrl" class="btn btn-secondary text-sm" :disabled="resumingLaunch" @click="reopenPopup">
            {{ t('payment.qr.openPayWindow') }}
          </button>
        </div>
      </div>
      <div class="card p-4 text-center">
        <p class="mt-1 text-2xl font-bold tabular-nums text-gray-900 dark:text-white">{{ countdownDisplay }}</p>
        <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">{{ waitingHint }}</p>
      </div>
      <button v-if="pollExhausted" class="btn btn-secondary w-full" @click="refreshNow">
        {{ t('payment.qr.refreshStatus') }}
      </button>
      <button class="btn btn-secondary w-full" :disabled="cancelling" @click="handleCancel">
        {{ cancelling ? t('common.processing') : t('payment.qr.cancelOrder') }}
      </button>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { usePaymentStore } from '@/stores/payment'
import { useAppStore } from '@/stores'
import { paymentAPI } from '@/api/payment'
import { extractApiErrorCode } from '@/utils/apiError'
import { getPaymentPopupFeatures, isBuiltInAlipayMethod, isBuiltInWxpayMethod } from '@/components/payment/providerConfig'
import { isMobileDevice } from '@/utils/device'
import { currencySymbol, formatPaymentAmount, normalizePaymentCurrency } from '@/components/payment/currency'
import {
  clearQueuedPaymentCancellation,
  queuePaymentCancellation,
  validateAlipayCheckoutFrameUrl,
  validateAlipayHostedCheckoutUrl,
  validateAlipayQRCode,
} from '@/components/payment/paymentFlow'
import type { CreateOrderResult, PaymentDiscountSnapshot, PaymentOrder, WechatJSAPIPayload } from '@/types/payment'
import Icon from '@/components/icons/Icon.vue'
import QRCode from 'qrcode'
import alipayIcon from '@/assets/icons/alipay.svg'
import wxpayIcon from '@/assets/icons/wxpay.svg'
import paymentIcon from '@/assets/icons/payment.svg'
import { fulfillmentFact, paymentFact } from '@/components/payment/orderPresentation'
import {
  createAlipayDeepLinkLauncher,
  type AlipayDeepLinkLauncher,
  type AlipayDeepLinkState,
} from './alipayDeepLink'

const props = defineProps<{
  orderId: number
  amount?: number
  payAmount?: number
  qrCode: string
  expiresAt: string
  paymentType: string
  payUrl?: string
  checkoutFrameUrl?: string
  /** The signed Alipay QR frame may only be embedded by a local checkout dialog. */
  allowCheckoutFrame?: boolean
  orderType?: string
  currency?: string
  outTradeNo?: string
  mobileAlipayDeepLink?: boolean
  paymentDiscount?: PaymentDiscountSnapshot
  /** A recovered WeChat JSAPI payload belongs to this existing order only. */
  wechatJsapi?: WechatJSAPIPayload
  /** Resume was refused because the central close is still being applied. */
  initialCancellationPending?: boolean
  /** Resume was refused while the provider payment state is still uncertain. */
  initialConfirmationPending?: boolean
}>()

type PaymentOutcome = 'success' | 'cancelled' | 'expired'

const emit = defineEmits<{ done: []; success: []; settled: [outcome: PaymentOutcome] }>()

const i18n = useI18n()
const { t } = i18n
const paymentStore = usePaymentStore()
const appStore = useAppStore()

const qrCanvas = ref<HTMLCanvasElement | null>(null)
const qrUrl = ref('')
// `null` means the original first-launch material is still displayed. Once a
// user explicitly reopens checkout, this holds only the authoritative resume
// response and never falls back to the cached prop URL.
const resumedPayUrl = ref<string | null>(null)
const resumedCheckoutFrameUrl = ref<string | null>(null)
const resumedMobileAlipayDeepLink = ref<boolean | null>(null)
const sessionVersion = ref(0)
const remainingSeconds = ref(0)
const cancelling = ref(false)
const resumingLaunch = ref(false)
const cancellationPending = ref(false)
const confirmationPending = ref(false)
const paidOrder = ref<PaymentOrder | null>(null)
const latestOrder = ref<PaymentOrder | null>(null)
const deepLinkState = ref<AlipayDeepLinkState>('idle')
const deepLinkFallbackVisible = ref(false)
const paymentCurrency = computed(() => normalizePaymentCurrency(props.currency))
const creditedAmountSymbol = currencySymbol('USD')
const localeCode = computed(() => {
  const raw = i18n.locale as unknown
  if (typeof raw === 'string') return raw
  if (raw && typeof raw === 'object' && 'value' in raw) {
    return String((raw as { value?: string }).value || '')
  }
  return undefined
})

// Terminal outcome: null = still active, 'success' | 'cancelled' | 'expired'
const outcome = ref<PaymentOutcome | null>(null)

let pollTimer: ReturnType<typeof setInterval> | null = null
let countdownTimer: ReturnType<typeof setInterval> | null = null
let verifyAttempts = 0
let lastVerifyAt = 0
let lastCancellationVerifyAt = 0
let alipayLauncher: AlipayDeepLinkLauncher | null = null
let pollAttempts = 0
const pollExhausted = ref(false)
let lifecycleGeneration = 0
let disposed = false
let mounted = false
const deadlineReached = ref(false)
let deadlineCheckRequestedGeneration: number | null = null
let wechatJsapiLaunchGeneration: number | null = null
const pollInFlightGenerations = new Set<number>()

const VERIFY_RETRY_INTERVAL_MS = 15000
const VERIFY_RETRY_MAX_ATTEMPTS = 6
const POLL_INTERVAL_MS = 3000
const POLL_MAX_ATTEMPTS = 120

const isAlipay = computed(() => isBuiltInAlipayMethod(props.paymentType))
const isWxpay = computed(() => isBuiltInWxpayMethod(props.paymentType))
const currentPayUrl = computed(() => {
  const raw = resumedPayUrl.value ?? props.payUrl ?? ''
  return isAlipay.value ? validateAlipayHostedCheckoutUrl(raw) || validateAlipayCheckoutFrameUrl(raw) : raw
})
const currentHostedPayUrl = computed(() => (
  currentPayUrl.value
  || resumedCheckoutFrameUrl.value
  || validateAlipayCheckoutFrameUrl(props.checkoutFrameUrl)
))
const currentCheckoutFrameUrl = computed(() => {
  const raw = resumedCheckoutFrameUrl.value ?? props.checkoutFrameUrl ?? ''
  return validateAlipayCheckoutFrameUrl(raw)
})
const frameLoadError = ref(false)
const iframeLoading = ref(true)

function onIframeLoad() {
  iframeLoading.value = false
}

function onIframeError() {
  iframeLoading.value = false
  frameLoadError.value = true
}

const isMobileAlipayDeepLink = computed(() => (resumedMobileAlipayDeepLink.value ?? props.mobileAlipayDeepLink) === true && isAlipay.value && !!qrUrl.value)
const showQRCode = computed(() => (
  !!qrUrl.value
  && (!isMobileAlipayDeepLink.value || deepLinkFallbackVisible.value)
))
const showCheckoutFrame = computed(() => (
  !showQRCode.value
  && !isMobileAlipayDeepLink.value
  && !isMobileDevice()
  && props.allowCheckoutFrame === true
  && !!currentCheckoutFrameUrl.value
  && !frameLoadError.value
))

const qrBorderClass = computed(() => {
  if (isAlipay.value) return 'border-[#00AEEF] bg-blue-50 dark:border-[#00AEEF]/70 dark:bg-blue-950/20'
  if (isWxpay.value) return 'border-[#2BB741] bg-green-50 dark:border-[#2BB741]/70 dark:bg-green-950/20'
  return 'border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800'
})

const qrLogoBgClass = computed(() => {
  if (isAlipay.value) return 'bg-[#00AEEF]'
  if (isWxpay.value) return 'bg-[#2BB741]'
  return 'bg-gray-400'
})

const qrLogoIcon = computed(() => {
  if (isAlipay.value) return alipayIcon
  if (isWxpay.value) return wxpayIcon
  return paymentIcon
})

const scanTitle = computed(() => {
  if (isAlipay.value) return t('payment.qr.scanAlipay')
  if (isWxpay.value) return t('payment.qr.scanWxpay')
  return t('payment.qr.scanToPay')
})

const scanHint = computed(() => {
  if (isAlipay.value) return t('payment.qr.scanAlipayHint')
  if (isWxpay.value) return t('payment.qr.scanWxpayHint')
  return ''
})

const countdownDisplay = computed(() => {
  const m = Math.floor(remainingSeconds.value / 60)
  const s = remainingSeconds.value % 60
  return m.toString().padStart(2, '0') + ':' + s.toString().padStart(2, '0')
})

const displayPaymentAmount = computed(() => formatGatewayAmount(props.payAmount || props.amount || 0))
const displayOrderNumber = computed(() => props.outTradeNo || `#${props.orderId}`)

const paymentReceivedHint = computed(() => {
  const current = latestOrder.value
  if (!current || paymentFact(current) !== 'PAID' || normalizeStatus(current.status) === 'COMPLETED') return ''
  if (current.needs_manual_review || fulfillmentFact(current) === 'FAILED') {
    return t('payment.result.paidButFulfillmentFailed')
  }
  return t('payment.result.paymentReceivedProcessing')
})

const waitingHint = computed(() => paymentReceivedHint.value || t('payment.qr.waitingPayment'))

function formatGatewayAmount(value: number | string, currency?: string | null): string {
  return formatPaymentAmount(Number(value), currency || paymentCurrency.value, localeCode.value)
}

async function copyOrderNumber(value: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(value)
    appStore.showSuccess(t('common.success'))
  } catch {
    appStore.showError(t('payment.orderOps.copyFailed'))
  }
}

function isSuccessStatus(status: string | null | undefined): boolean {
  return normalizeStatus(status) === 'COMPLETED'
}

function normalizeStatus(status: string | null | undefined): string {
  return String(status || '').trim().toUpperCase()
}

interface ResumedPaymentLaunch {
  qrCode: string
  payUrl: string
  checkoutFrameUrl: string
  expiresAt: string
  mobileAlipayDeepLink: boolean
}

function clearLaunchMaterial() {
  qrUrl.value = ''
  resumedPayUrl.value = ''
  resumedCheckoutFrameUrl.value = ''
  resumedMobileAlipayDeepLink.value = false
  frameLoadError.value = false
  iframeLoading.value = true
  alipayLauncher?.dispose()
  alipayLauncher = null
}

function waitForAuthoritativeConfirmation() {
  clearLaunchMaterial()
  cancellationPending.value = false
  confirmationPending.value = true
  void pollStatus({ force: true })
}

function waitForAuthoritativeExpiry() {
  clearLaunchMaterial()
  deadlineReached.value = true
  remainingSeconds.value = 0
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
  void pollStatus({ force: true })
}

function setTerminalResumeStatus(status: string): boolean {
  if (status === 'CANCELLED') {
    clearLaunchMaterial()
    cleanupSession()
    setOutcome('cancelled')
    return true
  }
  if (status === 'EXPIRED' || status === 'FAILED') {
    clearLaunchMaterial()
    cleanupSession()
    setOutcome('expired')
    return true
  }
  return false
}

function isSamePaymentFamily(paymentType: string): boolean {
  const expected = String(props.paymentType || '').trim()
  const received = paymentType.trim()
  if (!expected || !received) return false
  if (isBuiltInAlipayMethod(expected) || isBuiltInAlipayMethod(received)) {
    return isBuiltInAlipayMethod(expected) && isBuiltInAlipayMethod(received)
  }
  if (isBuiltInWxpayMethod(expected) || isBuiltInWxpayMethod(received)) {
    return isBuiltInWxpayMethod(expected) && isBuiltInWxpayMethod(received)
  }
  return expected === received
}

function applyResumedPaymentLaunch(result: CreateOrderResult): ResumedPaymentLaunch | null {
  const status = normalizeStatus(result.status)
  if (status !== 'PENDING') {
    if (!setTerminalResumeStatus(status)) waitForAuthoritativeConfirmation()
    return null
  }
  if (!isSamePaymentFamily(String(result.payment_type || ''))) {
    waitForAuthoritativeConfirmation()
    return null
  }
  const deadline = Date.parse(result.expires_at || '')
  if (!Number.isFinite(deadline) || deadline <= Date.now()) {
    waitForAuthoritativeExpiry()
    return null
  }
  const qrCode = isBuiltInAlipayMethod(props.paymentType)
    ? validateAlipayQRCode(result.qr_code)
    : String(result.qr_code || '').trim()
  const payUrl = isBuiltInAlipayMethod(props.paymentType)
    ? validateAlipayHostedCheckoutUrl(result.pay_url) || validateAlipayCheckoutFrameUrl(result.pay_url)
    : String(result.pay_url || '').trim()
  const checkoutFrameUrl = validateAlipayCheckoutFrameUrl(result.checkout_frame_url)
  if (!qrCode && !payUrl && !checkoutFrameUrl) {
    waitForAuthoritativeConfirmation()
    return null
  }

  qrUrl.value = qrCode
  resumedPayUrl.value = payUrl
  resumedCheckoutFrameUrl.value = checkoutFrameUrl
  resumedMobileAlipayDeepLink.value = result.alipay_mobile_precreate_deep_link === true
  frameLoadError.value = false
  iframeLoading.value = true
  deadlineReached.value = false
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
  sessionVersion.value += 1
  startCountdown(Math.floor((deadline - Date.now()) / 1000), lifecycleGeneration, currentSessionFingerprint())
  void renderQR()
  return {
    qrCode,
    payUrl,
    checkoutFrameUrl,
    expiresAt: result.expires_at,
    mobileAlipayDeepLink: result.alipay_mobile_precreate_deep_link === true,
  }
}

async function resumePaymentLaunch(): Promise<ResumedPaymentLaunch | null> {
  const generation = lifecycleGeneration
  const fingerprint = currentSessionFingerprint()
  if (!isCurrentLifecycle(generation, fingerprint) || !props.orderId || resumingLaunch.value) return null

  resumingLaunch.value = true
  try {
    const response = await paymentAPI.resumeOrder(props.orderId)
    if (!isCurrentLifecycle(generation, fingerprint)) return null
    const result = response.data
    if (result.order_id !== props.orderId) {
      waitForAuthoritativeConfirmation()
      return null
    }
    return applyResumedPaymentLaunch(result)
  } catch (err: unknown) {
    if (!isCurrentLifecycle(generation, fingerprint)) return null
    const code = extractApiErrorCode(err)
    clearLaunchMaterial()
    if (code === 'PAYMENT_CANCELLATION_PENDING') {
      cancellationPending.value = true
      confirmationPending.value = false
      void pollStatus({ force: true }, generation, fingerprint)
    } else if (code === 'PAYMENT_CONFIRMATION_PENDING') {
      cancellationPending.value = false
      confirmationPending.value = true
      void pollStatus({ force: true }, generation, fingerprint)
    } else {
      waitForAuthoritativeConfirmation()
    }
    return null
  } finally {
    if (isCurrentLifecycle(generation, fingerprint)) resumingLaunch.value = false
  }
}

async function reopenPopup() {
  // Reserve a browsing context synchronously from the click. It is closed if
  // the authenticated resume call says this order is no longer payable.
  const popup = window.open('', 'paymentPopup', getPaymentPopupFeatures())
  let navigated = false
  try {
    const launch = await resumePaymentLaunch()
    const launchUrl = launch?.payUrl || launch?.checkoutFrameUrl
    if (!launchUrl) return
    if (popup && !popup.closed) {
      popup.location.href = launchUrl
      navigated = true
      return
    }
    const opened = window.open(launchUrl, 'paymentPopup', getPaymentPopupFeatures())
    if (!opened || opened.closed) window.location.assign(launchUrl)
  } finally {
    if (popup && !popup.closed && !navigated && typeof popup.close === 'function') popup.close()
  }
}

function setOutcome(next: PaymentOutcome) {
  if (outcome.value === next) return
  outcome.value = next
  emit('settled', next)
}

function currentSessionFingerprint(): string {
  return JSON.stringify([
    props.orderId,
    props.amount,
    props.payAmount,
    props.qrCode,
    props.expiresAt,
    props.paymentType,
    props.payUrl,
    props.checkoutFrameUrl,
    props.allowCheckoutFrame,
    props.orderType,
    props.currency,
    props.outTradeNo,
    props.mobileAlipayDeepLink,
    props.wechatJsapi,
    props.initialCancellationPending,
    props.initialConfirmationPending,
  ])
}

function isCurrentLifecycle(generation: number, fingerprint?: string): boolean {
  return !disposed
    && generation === lifecycleGeneration
    && (fingerprint === undefined || fingerprint === currentSessionFingerprint())
}

async function renderQR(generation = lifecycleGeneration, fingerprint = currentSessionFingerprint()) {
  await nextTick()
  if (!isCurrentLifecycle(generation, fingerprint) || !showQRCode.value || !qrCanvas.value || !qrUrl.value) return
  const canvas = qrCanvas.value
  const value = qrUrl.value
  await QRCode.toCanvas(canvas, value, {
    width: 220, margin: 2,
    errorCorrectionLevel: 'M',
  })
  // Each checkout receives a keyed canvas. This also prevents a deferred QR
  // renderer from drawing an old checkout into a newly selected session.
  if (!isCurrentLifecycle(generation, fingerprint) || qrCanvas.value !== canvas || qrUrl.value !== value) return
}

function updateDeepLinkState(
  state: AlipayDeepLinkState,
  generation = lifecycleGeneration,
  fingerprint = currentSessionFingerprint(),
) {
  if (!isCurrentLifecycle(generation, fingerprint)) return
  deepLinkState.value = state
  if (state === 'fallback') {
    deepLinkFallbackVisible.value = true
    void renderQR(generation, fingerprint)
  } else if (state === 'backgrounded') {
    deepLinkFallbackVisible.value = false
  }
}

function launchAlipayDeepLink(
  generation = lifecycleGeneration,
  fingerprint = currentSessionFingerprint(),
) {
  if (!isCurrentLifecycle(generation, fingerprint) || !isMobileAlipayDeepLink.value) return
  alipayLauncher?.dispose()
  alipayLauncher = createAlipayDeepLinkLauncher({
    qrCode: qrUrl.value,
    document,
    lifecycleTarget: window,
    userAgent: window.navigator.userAgent,
    assignLocation: (url) => window.location.assign(url),
    onStateChange: (state) => updateDeepLinkState(state, generation, fingerprint),
  })
  alipayLauncher.launch()
}

async function reopenAlipay() {
  const launch = await resumePaymentLaunch()
  if (!launch?.qrCode || !launch.mobileAlipayDeepLink) return
  launchAlipayDeepLink()
}

function saveQRCode() {
  const canvas = qrCanvas.value
  if (!canvas) return
  const link = document.createElement('a')
  link.href = canvas.toDataURL('image/png')
  link.download = `alipay-${props.outTradeNo || props.orderId}.png`
  document.body.appendChild(link)
  link.click()
  link.remove()
}

interface WeixinJSBridgeLike {
  invoke(
    action: string,
    payload: Record<string, unknown>,
    callback: (result: Record<string, unknown>) => void,
  ): void
}

function getWeixinJSBridge(): WeixinJSBridgeLike | undefined {
  return (window as Window & { WeixinJSBridge?: WeixinJSBridgeLike }).WeixinJSBridge
}

function waitForWeixinJSBridge(timeoutMs = 4000): Promise<WeixinJSBridgeLike | null> {
  const existing = getWeixinJSBridge()
  if (existing) return Promise.resolve(existing)

  return new Promise((resolve) => {
    let settled = false
    const finish = (bridge: WeixinJSBridgeLike | null) => {
      if (settled) return
      settled = true
      document.removeEventListener('WeixinJSBridgeReady', handleReady)
      document.removeEventListener('onWeixinJSBridgeReady', handleReady)
      window.clearTimeout(timer)
      resolve(bridge)
    }
    const handleReady = () => finish(getWeixinJSBridge() ?? null)
    const timer = window.setTimeout(() => finish(getWeixinJSBridge() ?? null), timeoutMs)
    document.addEventListener('WeixinJSBridgeReady', handleReady, false)
    document.addEventListener('onWeixinJSBridgeReady', handleReady, false)
  })
}

async function invokeRecoveredWechatJsapi(
  payload: WechatJSAPIPayload,
): Promise<Record<string, unknown>> {
  const bridge = await waitForWeixinJSBridge()
  if (!bridge) throw new Error('WECHAT_JSAPI_UNAVAILABLE')
  return new Promise((resolve) => {
    bridge.invoke('getBrandWCPayRequest', payload as Record<string, unknown>, (result) => resolve(result || {}))
  })
}

async function launchRecoveredWechatJsapi(
  generation = lifecycleGeneration,
  fingerprint = currentSessionFingerprint(),
) {
  const payload = props.wechatJsapi
  if (!payload || !isCurrentLifecycle(generation, fingerprint) || wechatJsapiLaunchGeneration === generation) return
  wechatJsapiLaunchGeneration = generation

  try {
    const result = await invokeRecoveredWechatJsapi(payload)
    if (!isCurrentLifecycle(generation, fingerprint)) return
    const resultMessage = String(result.err_msg || '').toLowerCase()
    if (resultMessage.includes('cancel')) {
      // Closing the provider sheet does not prove the local order is unpaid.
      // Keep this exact order in the authoritative polling loop.
      appStore.showInfo(t('payment.qr.paymentSheetDismissed'))
    } else if (resultMessage && !resultMessage.includes('ok')) {
      appStore.showError(t('payment.errors.wechatJsapiFailed'))
    }
  } catch {
    if (isCurrentLifecycle(generation, fingerprint)) {
      appStore.showError(t('payment.errors.wechatJsapiUnavailable'))
    }
  }
}

async function tryRecoverPendingOrder(
  order: PaymentOrder,
  context: { generation: number; fingerprint: string; paymentType: string },
): Promise<PaymentOrder> {
  if (!isBuiltInWxpayMethod(context.paymentType) && !isBuiltInAlipayMethod(context.paymentType)) return order
  const outTradeNo = String(order.out_trade_no || '').trim()
  if (!outTradeNo) return order
  const normalizedStatus = String(order.status || '').trim().toUpperCase()
  if (normalizedStatus !== 'PENDING') return order
  // The close endpoint can acknowledge the request before the order projection
  // exposes cancellation_pending. Keep retrying from the local safety state in
  // that short window so cancellation recovery is not capped at normal checks.
  const isCancellationPending = order.cancellation_pending === true || cancellationPending.value
  const now = Date.now()
  if (isCancellationPending) {
    if (now - lastCancellationVerifyAt < VERIFY_RETRY_INTERVAL_MS) return order
    lastCancellationVerifyAt = now
  } else {
    if (verifyAttempts >= VERIFY_RETRY_MAX_ATTEMPTS || now - lastVerifyAt < VERIFY_RETRY_INTERVAL_MS) {
      return order
    }
    lastVerifyAt = now
    verifyAttempts += 1
  }

  try {
    const result = await paymentAPI.verifyOrder(outTradeNo)
    if (!isCurrentLifecycle(context.generation, context.fingerprint)) return order
    return result.data ?? order
  } catch {
    return order
  }
}

async function pollStatus(
  options: { force?: boolean } = {},
  generation = lifecycleGeneration,
  fingerprint = currentSessionFingerprint(),
) {
  if (!isCurrentLifecycle(generation, fingerprint) || !props.orderId || outcome.value || (pollExhausted.value && !options.force)) return
  // A slow lookup for a replaced checkout must not prevent the new checkout
  // from polling. Only de-duplicate requests from the same lifecycle.
  if (pollInFlightGenerations.has(generation)) {
    if (options.force) deadlineCheckRequestedGeneration = generation
    return
  }

  const orderId = props.orderId
  const paymentType = props.paymentType
  pollInFlightGenerations.add(generation)
  try {
    if (deadlineCheckRequestedGeneration === generation) {
      deadlineCheckRequestedGeneration = null
    }
    pollAttempts += 1
    let order: PaymentOrder | null = null
    try {
      order = await paymentStore.pollOrderStatus(orderId)
    } catch {
      // A temporary transport failure is indistinguishable from a lookup race
      // to the checkout shell. Keep a bounded recovery loop instead of
      // converting it into an expiry or an immediate terminal state.
      order = null
    }
    if (!isCurrentLifecycle(generation, fingerprint) || outcome.value) return
    if (pollAttempts >= POLL_MAX_ATTEMPTS && !order) {
      pollExhausted.value = true
      cleanupPollTimer()
    }
    if (!order) return
    latestOrder.value = order
    order = await tryRecoverPendingOrder(order, { generation, fingerprint, paymentType })
    if (!isCurrentLifecycle(generation, fingerprint) || outcome.value) return
    latestOrder.value = order
    const orderStatus = normalizeStatus(order.status)
    const serverCancellationPending = orderStatus === 'PENDING' && order.cancellation_pending === true
    // The 409 is an accepted close request. A list/status projection can lag
    // that audit write briefly, so retain this safety state until a terminal
    // server result rather than re-exposing a payable checkout in the gap.
    if (serverCancellationPending) cancellationPending.value = true
    if (orderStatus !== 'PENDING') cancellationPending.value = false
    if (cancellationPending.value || orderStatus !== 'PENDING') {
      confirmationPending.value = false
    }
    if (isSuccessStatus(order.status)) {
      cleanupSession()
      paidOrder.value = order
      setOutcome('success')
      emit('success')
    } else if (normalizeStatus(order.status) === 'CANCELLED') {
      cleanupSession()
      setOutcome('cancelled')
    } else if (normalizeStatus(order.status) === 'EXPIRED' || (normalizeStatus(order.status) === 'FAILED' && paymentFact(order) === 'UNPAID')) {
      // An expired browser countdown is only a prompt to ask the server. A
      // terminal expiry is authoritative only after the server records it.
      cleanupSession()
      setOutcome('expired')
    }
  } finally {
    pollInFlightGenerations.delete(generation)
    if (
      isCurrentLifecycle(generation, fingerprint)
      && deadlineCheckRequestedGeneration === generation
      && !outcome.value
    ) {
      deadlineCheckRequestedGeneration = null
      void pollStatus({ force: true }, generation, fingerprint)
    }
  }
}

function cleanupPollTimer() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function refreshNow() {
  const generation = lifecycleGeneration
  const fingerprint = currentSessionFingerprint()
  if (!isCurrentLifecycle(generation, fingerprint) || outcome.value) return
  pollExhausted.value = false
  pollAttempts = 0
  cleanupPollTimer()
  await pollStatus({ force: true }, generation, fingerprint)
  if (isCurrentLifecycle(generation, fingerprint) && !outcome.value && !pollExhausted.value) {
    pollTimer = setInterval(() => { void pollStatus({}, generation, fingerprint) }, POLL_INTERVAL_MS)
  }
}

function startCountdown(seconds: number, generation: number, fingerprint: string) {
  if (!isCurrentLifecycle(generation, fingerprint)) return
  remainingSeconds.value = Math.max(0, seconds)
  if (remainingSeconds.value <= 0) {
    requestDeadlineCheck(generation, fingerprint)
    return
  }
  countdownTimer = setInterval(() => {
    if (!isCurrentLifecycle(generation, fingerprint)) return
    remainingSeconds.value--
    if (remainingSeconds.value <= 0) requestDeadlineCheck(generation, fingerprint)
  }, 1000)
}

function requestDeadlineCheck(generation = lifecycleGeneration, fingerprint = currentSessionFingerprint()) {
  if (!isCurrentLifecycle(generation, fingerprint) || deadlineReached.value || outcome.value) return
  deadlineReached.value = true
  remainingSeconds.value = 0
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
  deadlineCheckRequestedGeneration = generation
  void pollStatus({ force: true }, generation, fingerprint)
}

function handleCancel() {
  const generation = lifecycleGeneration
  const fingerprint = currentSessionFingerprint()
  const orderId = props.orderId
  if (!isCurrentLifecycle(generation, fingerprint) || !orderId || cancelling.value) return
  cancelling.value = true
  // The local checkout is finished immediately. The server records the
  // cancellation intent before returning and retries provider confirmation in
  // its background reconciliation loop, so provider latency never remains in
  // the foreground dialog.
  if (typeof window !== 'undefined') queuePaymentCancellation(window.localStorage, orderId)
  void Promise.resolve()
    .then(() => paymentAPI.cancelOrder(orderId))
    .then(() => {
      if (typeof window !== 'undefined') clearQueuedPaymentCancellation(window.localStorage, orderId)
    })
    .catch(() => {})
  cleanupSession()
  setOutcome('cancelled')
  emit('done')
}

function handleDone() { cleanupSession(); emit('done') }

function cleanupSession() {
  lifecycleGeneration += 1
  cleanupPollTimer()
  if (countdownTimer) { clearInterval(countdownTimer); countdownTimer = null }
  alipayLauncher?.dispose()
  alipayLauncher = null
}

function startSession() {
  cleanupSession()
  const generation = lifecycleGeneration
  const fingerprint = currentSessionFingerprint()
  if (isAlipay.value) {
    qrUrl.value = validateAlipayQRCode(props.qrCode)
  } else {
    qrUrl.value = props.qrCode
  }
  resumedPayUrl.value = null
  resumedCheckoutFrameUrl.value = null
  resumedMobileAlipayDeepLink.value = null
  frameLoadError.value = false
  iframeLoading.value = true
  sessionVersion.value += 1
  remainingSeconds.value = 0
  cancelling.value = false
  resumingLaunch.value = false
  cancellationPending.value = props.initialCancellationPending === true
  confirmationPending.value = props.initialConfirmationPending === true && !cancellationPending.value
  paidOrder.value = null
  latestOrder.value = null
  outcome.value = null
  deepLinkState.value = 'idle'
  deepLinkFallbackVisible.value = false
  verifyAttempts = 0
  lastVerifyAt = 0
  lastCancellationVerifyAt = 0
  pollAttempts = 0
  pollExhausted.value = false
  deadlineReached.value = false
  deadlineCheckRequestedGeneration = null
  wechatJsapiLaunchGeneration = null
  const deadline = Date.parse(props.expiresAt)
  const seconds = Number.isFinite(deadline)
    ? Math.floor((deadline - Date.now()) / 1000)
    : 0
  startCountdown(seconds, generation, fingerprint)
  pollTimer = setInterval(() => { void pollStatus({}, generation, fingerprint) }, POLL_INTERVAL_MS)
  void pollStatus({}, generation, fingerprint)
  void renderQR(generation, fingerprint)
  void launchRecoveredWechatJsapi(generation, fingerprint)

  launchAlipayDeepLink(generation, fingerprint)
}

watch(
  () => [
    props.orderId,
    props.amount,
    props.payAmount,
    props.qrCode,
    props.expiresAt,
    props.paymentType,
    props.payUrl,
    props.checkoutFrameUrl,
    props.allowCheckoutFrame,
    props.orderType,
    props.currency,
    props.outTradeNo,
    props.mobileAlipayDeepLink,
    props.wechatJsapi,
    props.initialCancellationPending,
    props.initialConfirmationPending,
  ],
  () => {
    if (mounted && !disposed) startSession()
  },
  { flush: 'post' },
)
watch([() => qrUrl.value, showQRCode, sessionVersion], () => { void renderQR() })
onMounted(() => {
  mounted = true
  disposed = false
  startSession()
})
onUnmounted(() => {
  disposed = true
  mounted = false
  cleanupSession()
})
</script>
