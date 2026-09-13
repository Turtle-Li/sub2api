<template>
  <div class="space-y-4">
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
              <div class="flex justify-between">
                <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
                <span class="font-medium text-gray-900 dark:text-white">#{{ paidOrder.id }}</span>
              </div>
              <div v-if="paidOrder.out_trade_no" class="flex justify-between">
                <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</span>
                <span class="font-medium text-gray-900 dark:text-white">{{ paidOrder.out_trade_no }}</span>
              </div>
              <div class="flex justify-between">
                <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.amount') }}</span>
                <span class="font-medium text-gray-900 dark:text-white">{{ creditedAmountSymbol }}{{ paidOrder.amount.toFixed(2) }}</span>
              </div>
              <div class="flex justify-between">
                <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</span>
                <span class="font-medium text-gray-900 dark:text-white">{{ formatGatewayAmount(paidOrder.pay_amount, paidOrder.currency) }}</span>
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
          <button v-if="payUrl" class="btn btn-secondary text-sm" @click="reopenPopup">
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
          <button v-if="payUrl" class="btn btn-secondary text-sm" @click="reopenPopup">
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
import { extractI18nErrorMessage } from '@/utils/apiError'
import { getPaymentPopupFeatures, isBuiltInAlipayMethod, isBuiltInWxpayMethod } from '@/components/payment/providerConfig'
import { currencySymbol, formatPaymentAmount, normalizePaymentCurrency } from '@/components/payment/currency'
import type { PaymentOrder } from '@/types/payment'
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
  orderType?: string
  currency?: string
  outTradeNo?: string
  mobileAlipayDeepLink?: boolean
}>()

type PaymentOutcome = 'success' | 'cancelled' | 'expired'

const emit = defineEmits<{ done: []; success: []; settled: [outcome: PaymentOutcome] }>()

const i18n = useI18n()
const { t } = i18n
const paymentStore = usePaymentStore()
const appStore = useAppStore()

const qrCanvas = ref<HTMLCanvasElement | null>(null)
const qrUrl = ref('')
const sessionVersion = ref(0)
const remainingSeconds = ref(0)
const cancelling = ref(false)
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
let alipayLauncher: AlipayDeepLinkLauncher | null = null
let pollAttempts = 0
const pollExhausted = ref(false)
let lifecycleGeneration = 0
let disposed = false
let mounted = false
let deadlineReached = false
let deadlineCheckRequestedGeneration: number | null = null
const pollInFlightGenerations = new Set<number>()

const VERIFY_RETRY_INTERVAL_MS = 15000
const VERIFY_RETRY_MAX_ATTEMPTS = 6
const POLL_INTERVAL_MS = 3000
const POLL_MAX_ATTEMPTS = 120

const isAlipay = computed(() => isBuiltInAlipayMethod(props.paymentType))
const isWxpay = computed(() => isBuiltInWxpayMethod(props.paymentType))
const isMobileAlipayDeepLink = computed(() => props.mobileAlipayDeepLink === true && isAlipay.value && !!qrUrl.value)
const showQRCode = computed(() => !!qrUrl.value && (!isMobileAlipayDeepLink.value || deepLinkFallbackVisible.value))

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
  if (fulfillmentFact(current) === 'MANUAL_REVIEW' || fulfillmentFact(current) === 'FAILED') {
    return t('payment.result.paidButFulfillmentFailed')
  }
  return t('payment.result.paymentReceivedProcessing')
})

const waitingHint = computed(() => paymentReceivedHint.value || t('payment.qr.waitingPayment'))

function formatGatewayAmount(value: number, currency?: string | null): string {
  return formatPaymentAmount(value, currency || paymentCurrency.value, localeCode.value)
}

function isSuccessStatus(status: string | null | undefined): boolean {
  return normalizeStatus(status) === 'COMPLETED'
}

function normalizeStatus(status: string | null | undefined): string {
  return String(status || '').trim().toUpperCase()
}

function reopenPopup() {
  if (props.payUrl) {
    const win = window.open(props.payUrl, 'paymentPopup', getPaymentPopupFeatures())
    if (!win || win.closed) {
      window.location.href = props.payUrl
    }
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
    props.orderType,
    props.currency,
    props.outTradeNo,
    props.mobileAlipayDeepLink,
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

function reopenAlipay() {
  alipayLauncher?.launch()
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

async function tryRecoverPendingOrder(
  order: PaymentOrder,
  context: { generation: number; fingerprint: string; paymentType: string },
): Promise<PaymentOrder> {
  if (!isBuiltInWxpayMethod(context.paymentType) && !isBuiltInAlipayMethod(context.paymentType)) return order
  const outTradeNo = String(order.out_trade_no || '').trim()
  if (!outTradeNo) return order
  const normalizedStatus = String(order.status || '').trim().toUpperCase()
  if (normalizedStatus !== 'PENDING') return order
  const now = Date.now()
  if (verifyAttempts >= VERIFY_RETRY_MAX_ATTEMPTS || now - lastVerifyAt < VERIFY_RETRY_INTERVAL_MS) {
    return order
  }

  lastVerifyAt = now
  verifyAttempts += 1
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
  if (!isCurrentLifecycle(generation, fingerprint) || deadlineReached || outcome.value) return
  deadlineReached = true
  remainingSeconds.value = 0
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
  deadlineCheckRequestedGeneration = generation
  void pollStatus({ force: true }, generation, fingerprint)
}

async function handleCancel() {
  const generation = lifecycleGeneration
  const fingerprint = currentSessionFingerprint()
  const orderId = props.orderId
  if (!isCurrentLifecycle(generation, fingerprint) || !orderId || cancelling.value) return
  cancelling.value = true
  try {
    await paymentAPI.cancelOrder(orderId)
    if (!isCurrentLifecycle(generation, fingerprint)) return
    // The cancellation endpoint can race a payment callback. Query the local
    // order afterwards and only clear recovery once the server records its
    // terminal state.
    await pollStatus({ force: true }, generation, fingerprint)
  } catch (err: unknown) {
    if (isCurrentLifecycle(generation, fingerprint)) {
      appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
    }
  } finally {
    if (isCurrentLifecycle(generation, fingerprint)) {
      cancelling.value = false
    }
  }
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
  qrUrl.value = props.qrCode
  sessionVersion.value += 1
  remainingSeconds.value = 0
  cancelling.value = false
  paidOrder.value = null
  latestOrder.value = null
  outcome.value = null
  deepLinkState.value = 'idle'
  deepLinkFallbackVisible.value = false
  verifyAttempts = 0
  lastVerifyAt = 0
  pollAttempts = 0
  pollExhausted.value = false
  deadlineReached = false
  deadlineCheckRequestedGeneration = null
  let seconds = 30 * 60
  if (props.expiresAt) {
    seconds = Math.floor((new Date(props.expiresAt).getTime() - Date.now()) / 1000)
  }
  startCountdown(seconds, generation, fingerprint)
  pollTimer = setInterval(() => { void pollStatus({}, generation, fingerprint) }, POLL_INTERVAL_MS)
  void pollStatus({}, generation, fingerprint)
  void renderQR(generation, fingerprint)

  if (!isMobileAlipayDeepLink.value) return
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

watch(
  () => [
    props.orderId,
    props.amount,
    props.payAmount,
    props.qrCode,
    props.expiresAt,
    props.paymentType,
    props.payUrl,
    props.orderType,
    props.currency,
    props.outTradeNo,
    props.mobileAlipayDeepLink,
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
