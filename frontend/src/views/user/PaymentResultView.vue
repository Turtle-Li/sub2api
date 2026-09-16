<template>
  <div class="flex min-h-screen items-center justify-center bg-gray-50 px-4 dark:bg-dark-900">
    <div class="w-full max-w-md space-y-6">
      <!-- Loading -->
      <div v-if="loading" class="flex items-center justify-center py-20">
        <div class="h-8 w-8 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
      </div>
      <template v-else>
        <!-- Status Icon -->
        <div class="text-center">
          <div v-if="isSuccess"
            class="mx-auto flex h-20 w-20 items-center justify-center rounded-full bg-green-100 dark:bg-green-900/30">
            <svg class="h-10 w-10 text-green-500" fill="none" viewBox="0 0 24 24" stroke="currentColor"
              stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
            </svg>
          </div>
          <div v-else-if="isPending"
            class="mx-auto flex h-20 w-20 items-center justify-center rounded-full bg-yellow-100 dark:bg-yellow-900/30">
            <div class="h-10 w-10 animate-spin rounded-full border-4 border-yellow-500 border-t-transparent"></div>
          </div>
          <div v-else-if="isUnknown" class="mx-auto flex h-20 w-20 items-center justify-center rounded-full bg-gray-100 dark:bg-dark-700">
            <span class="text-3xl font-semibold text-gray-500 dark:text-gray-300">?</span>
          </div>
          <div v-else
            class="mx-auto flex h-20 w-20 items-center justify-center rounded-full bg-red-100 dark:bg-red-900/30">
            <svg class="h-10 w-10 text-red-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </div>
          <h2 class="mt-4 text-2xl font-bold text-gray-900 dark:text-white">
            {{ statusTitle }}
          </h2>
          <p v-if="statusHint" class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ statusHint }}</p>
        </div>
        <!-- Order Info -->
        <div v-if="order" class="rounded-xl bg-white p-5 shadow-sm dark:bg-dark-800">
          <div class="space-y-3 text-sm">
            <div v-if="hasOrderId(order)" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">#{{ order.id }}</span>
            </div>
            <div v-if="order.out_trade_no" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ order.out_trade_no }}</span>
            </div>
            <div v-if="hasAmountFields(order)" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.baseAmount') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ formatGatewayAmount(baseAmount) }}</span>
            </div>
            <div v-if="hasAmountFields(order) && Number(order.fee_rate) > 0" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.fee') }} ({{ order.fee_rate }}%)</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ formatGatewayAmount(feeAmount) }}</span>
            </div>
            <div v-if="hasAmountFields(order)" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</span>
              <span class="font-bold text-primary-600 dark:text-primary-400">{{ formatGatewayAmount(order.pay_amount) }}</span>
            </div>
            <div v-if="hasAmountFields(order) && order.amount !== order.pay_amount" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.creditedAmount') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ order.order_type === 'balance' ? '$' + order.amount.toFixed(2) : formatGatewayAmount(order.amount) }}</span>
            </div>
            <div v-if="hasPaymentType(order)" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.paymentMethod') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ t(paymentMethodI18nKey(order.payment_type), normalizedOrderPaymentType(order.payment_type)) }}</span>
            </div>
            <div v-if="order.status" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.status') }}</span>
              <OrderStatusBadge :status="displayOrderStatus(order.status)" />
            </div>
          </div>
          <p v-if="isPaidFulfillmentFailure" class="mt-4 text-sm text-amber-700 dark:text-amber-300">{{ t('payment.result.paidButFulfillmentFailed') }}</p>
          <p v-else-if="isManualReview" class="mt-4 text-sm text-amber-700 dark:text-amber-300">{{ t('payment.result.paidManualReview') }}</p>
        </div>
        <!-- EasyPay return info (when no order loaded) -->
        <div v-else-if="returnInfo" class="rounded-xl bg-white p-5 shadow-sm dark:bg-dark-800">
          <div class="space-y-3 text-sm">
            <div v-if="returnInfo.outTradeNo" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ returnInfo.outTradeNo }}</span>
            </div>
            <div v-if="returnInfo.money" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ formatGatewayAmount(Number(returnInfo.money) || 0) }}</span>
            </div>
            <div v-if="returnInfo.type" class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.paymentMethod') }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ t(paymentMethodI18nKey(returnInfo.type), normalizedOrderPaymentType(returnInfo.type)) }}</span>
            </div>
          </div>
        </div>
        <!-- Actions -->
        <div class="flex gap-3">
          <button class="btn btn-secondary flex-1" @click="router.push('/purchase')">{{ t('payment.result.backToRecharge') }}</button>
          <button v-if="canRefresh" class="btn btn-secondary flex-1" @click="refreshNow">{{ t('payment.qr.refreshStatus') }}</button>
          <button class="btn btn-primary flex-1" @click="router.push('/orders')">{{ t('payment.result.viewOrders') }}</button>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onBeforeUnmount, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import OrderStatusBadge from '@/components/payment/OrderStatusBadge.vue'
import {
  PAYMENT_RECOVERY_STORAGE_KEY,
  clearResetCardCheckoutAttempt,
  clearPaymentRecoverySnapshot,
  readPaymentRecoverySnapshot,
  type PaymentRecoverySnapshot,
} from '@/components/payment/paymentFlow'
import { usePaymentStore } from '@/stores/payment'
import { useAuthStore } from '@/stores/auth'
import { paymentAPI } from '@/api/payment'
import type { PublicOrderVerifyResult } from '@/api/payment'
import type { OrderStatus, PaymentOrder } from '@/types/payment'
import { formatPaymentAmount, normalizePaymentCurrency } from '@/components/payment/currency'
import { normalizePaymentMethodForDisplay, paymentMethodI18nKey } from './paymentUx'
import { paymentFact, fulfillmentFact } from '@/components/payment/orderPresentation'

const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()
const paymentStore = usePaymentStore()
const authStore = useAuthStore()

type ResolvedOrder = PaymentOrder | PublicOrderVerifyResult

const order = ref<ResolvedOrder | null>(null)
const recovery = ref<PaymentRecoverySnapshot | null>(null)
const loading = ref(true)
const knownContext = ref(false)
const retryExhausted = ref(false)
const currency = ref('CNY')

interface ReturnInfo {
  outTradeNo: string
  money: string
  type: string
  tradeStatus: string
}
const returnInfo = ref<ReturnInfo | null>(null)

// A paid order is not necessarily fulfilled yet. Only COMPLETED means the
// balance or subscription/reset-card entitlement was actually delivered.
const PENDING_STATUSES = new Set(['PENDING', 'CREATED', 'WAITING', 'PROCESSING', 'PAID', 'RECHARGING'])
const STATUS_REFRESH_INTERVAL_MS = 2000
const STATUS_REFRESH_MAX_ATTEMPTS = 15

let statusRefreshTimer: ReturnType<typeof setTimeout> | null = null
let activeRefresh: (() => Promise<ResolvedOrder | null>) | null = null
let refreshInFlight = false
let userBalanceRefreshStarted = false
const refreshAttempts = ref(0)
let lifecycleGeneration = 0
let disposed = false

/** 充值金额 = pay_amount / (1 + fee_rate/100)，fee_rate=0 时等于 pay_amount */
const baseAmount = computed(() => {
  if (!hasAmountFields(order.value)) return 0
  const feeRate = Number(order.value.fee_rate) || 0
  if (feeRate <= 0) return order.value.pay_amount ?? 0
  return Math.round((order.value.pay_amount / (1 + feeRate / 100)) * 100) / 100
})

/** 手续费 = pay_amount - baseAmount */
const feeAmount = computed(() => {
  if (!hasAmountFields(order.value)) return 0
  const feeRate = Number(order.value.fee_rate) || 0
  if (feeRate <= 0) return 0
  return Math.round((order.value.pay_amount - baseAmount.value) * 100) / 100
})

const localeCode = computed(() => {
  const raw = i18n.locale as unknown
  if (typeof raw === 'string') return raw
  if (raw && typeof raw === 'object' && 'value' in raw) {
    return String((raw as { value?: string }).value || '')
  }
  return undefined
})

const isSuccess = computed(() => normalizeOrderStatus(order.value?.status) === 'COMPLETED')

const isPaidFulfillmentFailure = computed(() => {
  return !!order.value && paymentFact(order.value) === 'PAID' && fulfillmentFact(order.value) === 'FAILED'
})

const isManualReview = computed(() => {
  return !!order.value && paymentFact(order.value) === 'PAID' && Boolean(order.value.needs_manual_review)
})

const isProviderSuccessReturn = computed(() => {
  const status = readRouteQueryString('status').trim().toLowerCase()
  return status === 'success' && (
    readRouteOrderId() > 0
    || readRouteQueryString('resume_token') !== ''
    || readRouteQueryString('out_trade_no') !== ''
  )
})

const isPending = computed(() => {
  if (isSuccess.value || isPaidFulfillmentFailure.value || isManualReview.value) return false
  if (!knownContext.value) return false
  if (order.value) {
    // Once the server has recorded PAID or RECHARGING, a finite refresh
    // budget must not turn that payment into a false failure. The user can
    // manually resume checks while delivery is still being reconciled.
    return paymentFact(order.value) === 'PAID'
    || isPendingStatus(order.value.status)
    || (isProviderSuccessReturn.value && !isTerminalStatus(order.value.status))
  }
  // A route order id, resume token, or recovered checkout is sufficient
  // context to keep polling after the first lookup races the provider.
  return !retryExhausted.value
})

const isUnknown = computed(() => !order.value && (!knownContext.value || retryExhausted.value))
const canRefresh = computed(() => !!activeRefresh && (isPending.value || isUnknown.value))

const statusTitle = computed(() => {
  if (isSuccess.value) {
    return t('payment.result.success')
  }
  if (isPending.value) {
    return t('payment.result.processing')
  }
  if (isUnknown.value) {
    return t('payment.result.unknown')
  }
  if (isPaidFulfillmentFailure.value) {
    return t('payment.result.paidButFulfillmentFailed')
  }
  if (isManualReview.value) {
    return t('payment.result.paidManualReview')
  }
  return t('payment.result.failed')
})

const statusHint = computed(() => {
  if (isPaidFulfillmentFailure.value || isManualReview.value) return ''
  if (isPending.value) return t('payment.result.processingHint')
  if (isUnknown.value) return t('payment.result.unknown')
  return ''
})

function normalizedOrderPaymentType(paymentType: string): string {
  return normalizePaymentMethodForDisplay(paymentType || '') || paymentType || ''
}

function formatGatewayAmount(value: number): string {
  return formatPaymentAmount(value, currency.value, localeCode.value)
}

function setResolvedOrder(nextOrder: ResolvedOrder | null): void {
  if (!nextOrder) return
  order.value = nextOrder
  knownContext.value = true
  retryExhausted.value = false
  if ('currency' in nextOrder && nextOrder.currency) {
    currency.value = normalizePaymentCurrency(nextOrder.currency)
  }
  refreshUserBalanceForSuccessfulOrder(nextOrder)
}

function refreshUserBalanceForSuccessfulOrder(nextOrder: ResolvedOrder): void {
  if (userBalanceRefreshStarted || normalizeOrderStatus(nextOrder.status) !== 'COMPLETED') {
    return
  }
  if ('order_type' in nextOrder && nextOrder.order_type !== 'balance') {
    return
  }

  userBalanceRefreshStarted = true
  void authStore.refreshUser().catch(() => {
    // The order result remains authoritative even if refreshing profile data fails.
  })
}

function hasOrderId(nextOrder: ResolvedOrder | null): nextOrder is ResolvedOrder & { id: number } {
  return !!nextOrder && typeof nextOrder.id === 'number' && nextOrder.id > 0
}

function hasAmountFields(nextOrder: ResolvedOrder | null): nextOrder is ResolvedOrder & { amount: number; pay_amount: number; fee_rate: number } {
  return !!nextOrder
    && typeof nextOrder.amount === 'number'
    && typeof nextOrder.pay_amount === 'number'
    && typeof nextOrder.fee_rate === 'number'
}

function hasPaymentType(nextOrder: ResolvedOrder | null): nextOrder is ResolvedOrder & { payment_type: string } {
  return !!nextOrder && typeof nextOrder.payment_type === 'string' && nextOrder.payment_type.trim() !== ''
}

function normalizeOrderStatus(status: string | null | undefined): string {
  return String(status || '').trim().toUpperCase()
}

function displayOrderStatus(status: string): OrderStatus {
  return normalizeOrderStatus(status) as OrderStatus
}

function isPendingStatus(status: string | null | undefined): boolean {
  return PENDING_STATUSES.has(normalizeOrderStatus(status))
}

function isTerminalStatus(status: string | null | undefined): boolean {
  return ['COMPLETED', 'CANCELLED', 'EXPIRED', 'FAILED'].includes(normalizeOrderStatus(status))
}

function readRouteQueryString(key: string): string {
  const value = route.query[key]
  if (Array.isArray(value)) {
    return typeof value[0] === 'string' ? value[0] : ''
  }
  return typeof value === 'string' ? value : ''
}

function readRouteOrderId(): number {
  const value = Number(readRouteQueryString('order_id'))
  return Number.isSafeInteger(value) && value > 0 ? value : 0
}

function isCentralProviderReturn(): boolean {
  const method = readRouteQueryString('method').trim().toLowerCase()
  return readRouteQueryString('channel_out_trade_no') !== ''
    || readRouteQueryString('trade_no') !== ''
    || readRouteQueryString('app_id') !== ''
    || method.startsWith('alipay.')
}

function isCurrentLifecycle(generation: number): boolean {
  return !disposed && generation === lifecycleGeneration
}

function hasTrustedLegacyDirectProviderMarker(): boolean {
  // Legacy direct providers use this exact browser-return marker. Central
  // provider metadata is deliberately not accepted as an order lookup grant.
  return !isCentralProviderReturn() && readRouteQueryString('trade_status').trim() !== ''
}

function restoreRecoverySnapshot(context: {
  resumeToken: string
  routeOrderId: number
  routeOutTradeNo: string
}): PaymentRecoverySnapshot | null {
  if (typeof window === 'undefined') {
    return null
  }

  const rawSnapshot = window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)
  if (!rawSnapshot) {
    return null
  }

  if (context.resumeToken) {
    return readPaymentRecoverySnapshot(rawSnapshot, {
      resumeToken: context.resumeToken,
    })
  }

  if (context.routeOrderId > 0) {
    return readPaymentRecoverySnapshot(rawSnapshot, {
      orderId: context.routeOrderId,
    })
  }

  if (context.routeOutTradeNo && !isCentralProviderReturn()) {
    const matchingLocalOrder = readPaymentRecoverySnapshot(rawSnapshot, {
      outTradeNo: context.routeOutTradeNo,
    })
    if (matchingLocalOrder) {
      return matchingLocalOrder
    }
  }

  // The fixed unified return URL commonly contains provider-side order IDs.
  // They are not Sub2's local out_trade_no; resume the active local checkout
  // instead of allowing provider metadata to redirect us to another order.
  return readPaymentRecoverySnapshot(rawSnapshot)
}

async function resolveOrderFromResumeToken(resumeToken: string): Promise<ResolvedOrder | null> {
  try {
    const result = await paymentAPI.resolveOrderPublicByResumeToken(resumeToken)
    return result.data
  } catch (_err: unknown) {
    return null
  }
}

async function resolveOrderFromOutTradeNo(outTradeNo: string): Promise<ResolvedOrder | null> {
  try {
    const result = await paymentAPI.verifyOrder(outTradeNo)
    return result.data
  } catch (_err: unknown) {
    try {
      const result = await paymentAPI.verifyOrderPublic(outTradeNo)
      return result.data
    } catch (_innerErr: unknown) {
      return null
    }
  }
}

function clearStatusRefreshTimer(): void {
  if (statusRefreshTimer !== null) {
    clearTimeout(statusRefreshTimer)
    statusRefreshTimer = null
  }
}

async function pollOrderById(orderId: number): Promise<PaymentOrder | null> {
  if (orderId <= 0) return null
  try {
    return await paymentStore.pollOrderStatus(orderId)
  } catch (_err: unknown) {
    return null
  }
}

function recoveryMatch(nextOrder?: ResolvedOrder | null): { orderId?: number; resumeToken?: string } | undefined {
  const orderId = nextOrder && hasOrderId(nextOrder) ? nextOrder.id : recovery.value?.orderId
  const resumeToken = recovery.value?.resumeToken || readRouteQueryString('resume_token')
  return orderId || resumeToken
    ? { orderId: orderId || undefined, resumeToken: resumeToken || undefined }
    : undefined
}

function clearRecoveryForTerminal(nextOrder?: ResolvedOrder | null): void {
  if (!nextOrder || typeof window === 'undefined') return
  const status = normalizeOrderStatus(nextOrder.status)
  const wasExplicitlyUnpaidTerminal = paymentFact(nextOrder) === 'UNPAID'
    && ['CANCELLED', 'EXPIRED', 'FAILED'].includes(status)
  // PAID/RECHARGING and paid FAILED orders remain recoverable. A completed
  // order is the sole success terminal state; unpaid terminal states are safe
  // to remove only after the server has actually reported them.
  if (status === 'COMPLETED' || wasExplicitlyUnpaidTerminal) {
    const match = recoveryMatch(nextOrder)
    if (match) {
      clearPaymentRecoverySnapshot(window.localStorage, PAYMENT_RECOVERY_STORAGE_KEY, match)
    }
    if (nextOrder.order_type === 'reset_card' && hasOrderId(nextOrder)) {
      clearResetCardCheckoutAttempt(window.localStorage, { orderId: nextOrder.id })
    }
  }
}

function shouldContinuePolling(): boolean {
  return !!activeRefresh && isPending.value && refreshAttempts.value < STATUS_REFRESH_MAX_ATTEMPTS
}

function scheduleStatusRefresh(generation = lifecycleGeneration): void {
  if (!isCurrentLifecycle(generation)) return
  clearStatusRefreshTimer()
  if (!shouldContinuePolling()) {
    if (activeRefresh && isPending.value && refreshAttempts.value >= STATUS_REFRESH_MAX_ATTEMPTS) {
      retryExhausted.value = true
    }
    return
  }

  statusRefreshTimer = setTimeout(async () => {
    statusRefreshTimer = null
    if (!isCurrentLifecycle(generation) || refreshInFlight || !activeRefresh) return
    const refresh = activeRefresh
    refreshInFlight = true
    refreshAttempts.value += 1
    try {
      const refreshedOrder = await refresh()
      if (!isCurrentLifecycle(generation)) return
      if (refreshedOrder) {
        setResolvedOrder(refreshedOrder)
        clearRecoveryForTerminal(refreshedOrder)
      }
    } finally {
      if (isCurrentLifecycle(generation)) {
        refreshInFlight = false
      }
    }
    if (!isCurrentLifecycle(generation)) return
    if (shouldContinuePolling()) {
      scheduleStatusRefresh(generation)
    } else if (isPending.value && refreshAttempts.value >= STATUS_REFRESH_MAX_ATTEMPTS) {
      retryExhausted.value = true
    }
  }, STATUS_REFRESH_INTERVAL_MS)
}

async function refreshNow(): Promise<void> {
  const generation = lifecycleGeneration
  if (!isCurrentLifecycle(generation) || !activeRefresh || refreshInFlight) return
  const refresh = activeRefresh
  clearStatusRefreshTimer()
  retryExhausted.value = false
  refreshAttempts.value = 0
  refreshInFlight = true
  try {
    const refreshedOrder = await refresh()
    if (!isCurrentLifecycle(generation)) return
    if (refreshedOrder) {
      setResolvedOrder(refreshedOrder)
      clearRecoveryForTerminal(refreshedOrder)
    }
  } finally {
    if (isCurrentLifecycle(generation)) {
      refreshInFlight = false
    }
  }
  if (isCurrentLifecycle(generation)) {
    scheduleStatusRefresh(generation)
  }
}

async function initialize(): Promise<void> {
  disposed = false
  const generation = ++lifecycleGeneration
  const resumeToken = readRouteQueryString('resume_token')
  const routeOrderId = readRouteOrderId()
  const routeOutTradeNo = readRouteQueryString('out_trade_no')
  let orderId = routeOrderId
  let resumeLookupFailed = false
  const recoveryStoragePresent = typeof window !== 'undefined'
    && !!window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)

  // A syntactically valid route ID is itself enough context to make a finite
  // set of status checks, even if the first request races a webhook or fails.
  if (routeOrderId > 0) {
    knownContext.value = true
  }

  recovery.value = restoreRecoverySnapshot({
    resumeToken,
    routeOrderId,
    routeOutTradeNo,
  })
  if (recovery.value) {
    knownContext.value = true
    orderId ||= recovery.value.orderId
    if (recovery.value.currency) {
      currency.value = normalizePaymentCurrency(recovery.value.currency)
    }
  }

  if (resumeToken) {
    knownContext.value = true
    const resolvedOrder = await resolveOrderFromResumeToken(resumeToken)
    if (!isCurrentLifecycle(generation)) return
    if (resolvedOrder) {
      setResolvedOrder(resolvedOrder)
      if (!orderId && hasOrderId(resolvedOrder)) {
        orderId = resolvedOrder.id
      }
    } else {
      resumeLookupFailed = true
    }
  }

  // Only the documented legacy direct-provider marker permits a raw browser
  // out_trade_no lookup. A central-provider return can carry a provider-side
  // number with the same name, so it must use a signed token, local recovery,
  // or an explicit local order id instead.
  const safeLocalOutTradeNo = !!routeOutTradeNo
    && hasTrustedLegacyDirectProviderMarker()
    && (!recoveryStoragePresent || recovery.value?.outTradeNo === routeOutTradeNo)
  const shouldUsePublicOutTradeNo = safeLocalOutTradeNo

  activeRefresh = async (): Promise<ResolvedOrder | null> => {
    if (resumeToken) {
      const resolvedOrder = await resolveOrderFromResumeToken(resumeToken)
      if (resolvedOrder) {
        return resolvedOrder
      }
    }
    if (orderId > 0) {
      return pollOrderById(orderId)
    }
    if (shouldUsePublicOutTradeNo) {
      return resolveOrderFromOutTradeNo(routeOutTradeNo)
    }
    return null
  }

  if (!order.value && orderId > 0) {
    const resolvedOrder = await pollOrderById(orderId)
    if (!isCurrentLifecycle(generation)) return
    setResolvedOrder(resolvedOrder)
  }

  if (!order.value && shouldUsePublicOutTradeNo && (resumeLookupFailed || !resumeToken)) {
    const resolvedOrder = await resolveOrderFromOutTradeNo(routeOutTradeNo)
    if (!isCurrentLifecycle(generation)) return
    setResolvedOrder(resolvedOrder)
  }

  if (!isCurrentLifecycle(generation)) return

  if (!order.value && !knownContext.value && safeLocalOutTradeNo) {
    returnInfo.value = {
      outTradeNo: routeOutTradeNo,
      money: readRouteQueryString('money'),
      type: readRouteQueryString('type'),
      tradeStatus: readRouteQueryString('trade_status'),
    }
  }

  if (order.value) {
    clearRecoveryForTerminal(order.value)
    if (isPending.value) {
      scheduleStatusRefresh(generation)
    }
  } else if (knownContext.value) {
    scheduleStatusRefresh(generation)
  }
  loading.value = false
}

onMounted(() => {
  void initialize()
})

onBeforeUnmount(() => {
  disposed = true
  lifecycleGeneration += 1
  clearStatusRefreshTimer()
  activeRefresh = null
  refreshInFlight = false
})
</script>
