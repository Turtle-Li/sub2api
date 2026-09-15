<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl">
      <div v-if="loading" class="flex items-center justify-center py-24">
        <div class="h-8 w-8 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
      </div>

      <template v-else>
        <div v-if="paymentPhase === 'paying' && !paymentModalVisible" class="mb-6 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-primary-200 bg-primary-50 px-4 py-3 dark:border-primary-800 dark:bg-primary-950/30">
          <div class="min-w-0">
            <p class="text-sm font-semibold text-primary-900 dark:text-primary-100">{{ t('payment.resume.title') }}</p>
            <p class="mt-0.5 truncate text-xs text-primary-700 dark:text-primary-300">{{ paymentState.outTradeNo || `#${paymentState.orderId}` }}</p>
          </div>
          <button type="button" class="btn btn-primary shrink-0" data-test="resume-payment" @click="paymentModalVisible = true">
            {{ t('payment.resume.open') }}
          </button>
        </div>

        <!-- Masthead: who is buying, and what they hold today. -->
        <header class="flex flex-wrap items-end justify-between gap-4 pb-6">
          <div class="min-w-0">
            <h1 class="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">{{ t('payment.title') }}</h1>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('payment.rechargeSectionHint') }}</p>
          </div>
          <div class="flex items-center gap-3 rounded-xl border border-gray-200 bg-white px-4 py-2.5 dark:border-dark-700 dark:bg-dark-800">
            <Icon name="creditCard" size="sm" class="shrink-0 text-primary-500 dark:text-primary-400" />
            <div class="min-w-0">
              <p class="text-[11px] uppercase tracking-wider text-gray-400 dark:text-dark-500">{{ t('payment.currentBalance') }}</p>
              <p class="text-sm font-semibold tabular-nums text-gray-900 dark:text-white">{{ formatCreditAmount(user?.balance || 0) }}</p>
            </div>
          </div>
        </header>

        <PaymentPromoBanner :banner="checkout.banner" />

        <div v-if="tabs.length > 1" role="tablist" class="payment-segment mb-6">
          <button v-for="tab in tabs" :key="tab.key"
            role="tab"
            type="button"
            :aria-selected="activeTab === tab.key"
            :class="['payment-segment__item', activeTab === tab.key && 'payment-segment__item--active']"
            @click="activeTab = tab.key">{{ tab.label }}</button>
        </div>
        <div v-if="tabs.length === 0" class="card py-16 text-center">
          <p class="text-gray-500 dark:text-gray-400">{{ t('payment.billingUnavailable') }}</p>
        </div>

        <template v-else>
          <div v-if="enabledMethods.length === 0" class="card py-16 text-center">
            <p class="text-gray-500 dark:text-gray-400">{{ t('payment.notAvailable') }}</p>
          </div>

          <div v-if="activeTab === 'subscription' && enabledMethods.length > 0 && subscriptionPeriodOptions.length > 1" class="mb-5 flex justify-start">
            <div class="payment-segment max-w-full overflow-x-auto">
              <button v-for="period in subscriptionPeriodOptions" :key="period.key" type="button"
                :aria-pressed="selectedSubscriptionPeriod === period.key"
                :class="['payment-segment__item shrink-0', selectedSubscriptionPeriod === period.key && 'payment-segment__item--active']"
                @click="selectSubscriptionPeriod(period.key)">
                {{ period.label }}
                <span v-if="period.discountText" class="ml-1 text-[11px] font-semibold text-emerald-600 dark:text-emerald-400">{{ period.discountText }}</span>
              </button>
            </div>
          </div>

          <!-- Products on the left, the running total on the right. Below `lg`
               the rail detaches to the bottom of the viewport, so the extra
               padding keeps the last card clear of it. -->
          <div v-if="enabledMethods.length > 0" class="grid grid-cols-1 items-start gap-6 pb-40 lg:grid-cols-[minmax(0,1fr)_320px] lg:pb-0">
            <div class="min-w-0">
              <!-- Top-up -->
              <template v-if="activeTab === 'recharge'">
                <AmountInput
                  v-model="amount"
                  :amounts="rechargePresetAmounts"
                  :options="rechargePresetOptions"
                  :min="globalMinAmount"
                  :max="globalMaxAmount"
                  :currency="selectedCurrency"
                  :locale="localeCode"
                  :fee-rate="feeRate"
                  :balance-multiplier="balanceRechargeMultiplier"
                />
                <p v-if="amountError" class="mt-2 text-xs text-amber-600 dark:text-amber-300">{{ amountError }}</p>
                <p v-if="balanceRechargeMultiplier !== 1" class="mt-4 text-xs text-gray-500 dark:text-dark-400">
                  {{ t('payment.rechargeRatePreview', { currency: selectedCurrency, usd: balanceRechargeMultiplier.toFixed(2) }) }}
                </p>
              </template>

            <!-- Subscribe -->
            <template v-else-if="activeTab === 'subscription'">
              <div v-if="checkout.plans.length === 0" class="card py-16 text-center">
                <Icon name="gift" size="xl" class="mx-auto mb-3 text-gray-300 dark:text-dark-600" />
                <p class="text-gray-500 dark:text-gray-400">{{ t('payment.noPlans') }}</p>
              </div>
              <template v-else>
                <div :class="planGridClass">
                  <SubscriptionPlanCard v-for="plan in visibleSubscriptionPlans" :key="plan.id"
                    :plan="plan"
                    :active-subscriptions="activeSubscriptions"
                    :display-currency="selectedCurrency"
                    :locale="localeCode"
                    :usd-to-cny-rate="subscriptionUsdToCnyRate"
                    :selected="selectedPlan?.id === plan.id"
                    :featured="featuredPlanId === plan.id"
                    @select="selectPlan" />
                </div>
              </template>

              <ResetCardShop
                :subscriptions="activeSubscriptions"
                :plans="checkout.plans"
                :disabled="submitting || paymentPhase === 'paying'"
                @checkout="startResetCardCheckout"
              />

              <div v-if="activeSubscriptions.length > 0" class="mt-8">
                <p class="payment-product-card__eyebrow mb-2">{{ t('payment.activeSubscription') }}</p>
                <div class="space-y-2">
                  <div v-for="sub in activeSubscriptions" :key="sub.id"
                    class="flex items-center gap-3 rounded-xl border border-gray-200 bg-white px-3 py-2.5 dark:border-dark-700 dark:bg-dark-800">
                    <div :class="['h-6 w-1 shrink-0 rounded-full', platformAccentBarClass(sub.group?.platform || '')]" />
                    <div class="min-w-0 flex-1">
                      <div class="flex items-center gap-1.5">
                        <span class="truncate text-xs font-semibold text-gray-900 dark:text-white">{{ sub.group?.name || t('payment.groupFallback', { id: sub.group_id }) }}</span>
                        <span :class="['shrink-0 rounded-full px-1.5 py-0.5 text-[9px] font-medium', platformBadgeLightClass(sub.group?.platform || '')]">{{ platformLabel(sub.group?.platform || '') }}</span>
                      </div>
                      <div class="flex flex-wrap gap-x-3 text-[11px] text-gray-400 dark:text-gray-500">
                        <span>{{ t('payment.planCard.rate') }}: ×{{ sub.group?.rate_multiplier ?? 1 }}</span>
                        <span v-if="subscriptionHasPeakRate(sub)">{{ t('payment.planCard.peakRate') }}: {{ subscriptionPeakRateLabel(sub) }}</span>
                        <span v-if="sub.expires_at">{{ t('userSubscriptions.daysRemaining', { days: getDaysRemaining(sub.expires_at) }) }}</span>
                        <span v-else>{{ t('userSubscriptions.noExpiration') }}</span>
                      </div>
                    </div>
                    <span class="badge badge-success shrink-0 text-[10px]">{{ t('userSubscriptions.status.active') }}</span>
                  </div>
                </div>
              </div>
            </template>

            <section v-if="railMethods.length > 0" class="mt-6 lg:hidden">
              <PaymentMethodSelector :methods="railMethods" :selected="selectedMethod" @select="selectedMethod = $event" />
            </section>

            <div v-if="checkout.help_text || checkout.help_image_url" class="card mt-6 p-4">
              <div class="flex flex-col items-center gap-3">
                <img v-if="checkout.help_image_url" :src="checkout.help_image_url" alt=""
                  class="h-40 max-w-full cursor-pointer rounded-lg object-contain transition-opacity hover:opacity-80"
                  @click="previewImage = checkout.help_image_url" />
                <div v-if="checkout.help_text" class="markdown-body w-full overflow-x-auto break-words" v-html="renderedHelpText"></div>
              </div>
            </div>
          </div>

          <PaymentOrderRail
            :product-name="railProductName"
            :product-meta="railProductMeta"
            :methods="railMethods"
            :selected-method="selectedMethod"
            :base-amount="railBaseAmount"
            :fee-rate="feeRate"
            :fee-amount="railFeeAmount"
            :total-amount="railTotalAmount"
            :credit-line="railCreditLine"
            :credit-label="t('payment.creditedBalance')"
            :notice="railNotice"
            :footnote="t('payment.serverControlled')"
            :action-label="railActionLabel"
            :button-class="paymentButtonClass"
            :disabled="!railCanSubmit"
            :submitting="submitting"
            :format-pay="formatSelectedPaymentAmount"
            :show-breakdown="activeTab === 'recharge' || feeRate > 0"
            methods-collapsed-on-mobile
            @select-method="selectedMethod = $event"
            @submit="handleRailSubmit"
          />
          </div>
        </template>
      </template>
    </div>

    <BaseDialog
      v-if="paymentPhase === 'paying'"
      :show="paymentModalVisible"
      :title="paymentDialogTitle"
      width="normal"
      mobile-sheet
      keep-mounted
      :close-on-click-outside="false"
      @close="hidePaymentModal"
    >
      <PaymentStatusPanel
        :order-id="paymentState.orderId"
        :amount="paymentState.amount"
        :pay-amount="paymentState.payAmount"
        :qr-code="paymentState.qrCode"
        :expires-at="paymentState.expiresAt"
        :payment-type="paymentState.paymentType"
        :pay-url="paymentState.payUrl"
        :order-type="paymentState.orderType"
        :currency="paymentState.currency || selectedCurrency"
        :out-trade-no="paymentState.outTradeNo"
        :mobile-alipay-deep-link="paymentState.alipayMobilePrecreateDeepLink"
        @done="onPaymentDone"
        @success="onPaymentSuccess"
        @settled="onPaymentSettled"
      />
    </BaseDialog>

    <!-- Renewal Plan Selection Modal -->
    <Teleport to="body">
      <Transition name="modal">
        <div v-if="showRenewalModal" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm p-4" @click.self="closeRenewalModal">
          <div class="relative flex max-h-full w-full max-w-lg flex-col rounded-2xl border border-gray-200 bg-white p-6 shadow-2xl dark:border-dark-700 dark:bg-dark-900">
            <button class="absolute right-4 top-4 rounded-lg p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700 dark:hover:text-gray-200" @click="closeRenewalModal">
              <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>
            </button>
            <h3 class="mb-4 shrink-0 text-lg font-semibold text-gray-900 dark:text-white">{{ t('payment.selectPlan') }}</h3>
            <div class="min-h-0 space-y-4 overflow-y-auto">
              <SubscriptionPlanCard v-for="plan in renewalPlans" :key="plan.id" :plan="plan" :active-subscriptions="activeSubscriptions"
                :display-currency="selectedCurrency" :locale="localeCode" :usd-to-cny-rate="subscriptionUsdToCnyRate" @select="selectPlanFromModal" />
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>

    <!-- Image Preview Overlay -->
    <Teleport to="body">
      <Transition name="modal">
        <div v-if="previewImage" class="fixed inset-0 z-[60] flex items-center justify-center bg-black/70 backdrop-blur-sm" @click="previewImage = ''">
          <img :src="previewImage" alt="" class="max-h-[85vh] max-w-[90vw] rounded-xl object-contain shadow-2xl" />
        </div>
      </Transition>
    </Teleport>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import '@/styles/announcement-markdown.css'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePaymentStore } from '@/stores/payment'
import { useSubscriptionStore } from '@/stores/subscriptions'
import { useAppStore } from '@/stores'
import { FeatureFlags, resolveFeatureFlag } from '@/utils/featureFlags'
import { paymentAPI } from '@/api/payment'
import { extractApiErrorMessage, extractI18nErrorMessage } from '@/utils/apiError'
import { isMobileDevice } from '@/utils/device'
import { hasPeakRate, formatPeakRateWindow, serverTimezoneLabel, type PeakRateFields } from '@/utils/peak-rate'
import type { SubscriptionPlan, CheckoutInfoResponse, CreateOrderResult, OrderType, RechargeOption } from '@/types/payment'
import type { UserSubscription } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import AmountInput from '@/components/payment/AmountInput.vue'
import PaymentMethodSelector from '@/components/payment/PaymentMethodSelector.vue'
import { METHOD_ORDER, getPaymentPopupFeatures, isBuiltInAlipayMethod, isBuiltInWxpayMethod } from '@/components/payment/providerConfig'
import {
  PAYMENT_RECOVERY_STORAGE_KEY,
  buildCreateOrderPayload,
  clearResetCardCheckoutAttempt,
  clearPaymentRecoverySnapshot,
  decidePaymentLaunch,
  getOrCreateResetCardCheckoutAttempt,
  getVisibleMethods,
  matchResetCardCheckoutAttemptForResume,
  normalizeVisibleMethod,
  readPaymentRecoverySnapshot,
  recordResetCardCheckoutOrder,
  type ResetCardCheckoutAttempt,
  type PaymentRecoverySnapshot,
  writePaymentRecoverySnapshot,
} from '@/components/payment/paymentFlow'
import type { ResetCardQuote } from '@/api/subscriptions'
import { platformAccentBarClass, platformBadgeLightClass, platformLabel } from '@/utils/platformColors'
import SubscriptionPlanCard from '@/components/payment/SubscriptionPlanCard.vue'
import ResetCardShop from '@/components/payment/ResetCardShop.vue'
import PaymentPromoBanner from '@/components/payment/PaymentPromoBanner.vue'
import PaymentOrderRail from '@/components/payment/PaymentOrderRail.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatPaymentAmount, normalizePaymentCurrency } from '@/components/payment/currency'
import { creditedBalanceAmount, subscriptionGatewayAmount } from '@/components/payment/pricing'
import { planValiditySuffix as validitySuffixOf } from '@/components/payment/validity'
import type { PaymentMethodOption } from '@/components/payment/PaymentMethodSelector.vue'
import { buildPaymentErrorToastMessage, describePaymentScenarioError } from './paymentUx'
import { parseWechatResumeRoute, stripWechatResumeQuery } from './paymentWechatResume'
import { createIdempotencyKey } from '@/utils/idempotency'

const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const paymentStore = usePaymentStore()
const subscriptionStore = useSubscriptionStore()
const appStore = useAppStore()

const user = computed(() => authStore.user)
const activeSubscriptions = computed(() => subscriptionStore.activeSubscriptions)

function getDaysRemaining(expiresAt: string): number {
  const diff = new Date(expiresAt).getTime() - Date.now()
  return Math.max(0, Math.ceil(diff / (1000 * 60 * 60 * 24)))
}

function subscriptionHasPeakRate(sub: { group?: PeakRateFields | null }): boolean {
  return hasPeakRate(sub.group)
}

function subscriptionPeakRateLabel(sub: { group?: PeakRateFields | null }): string {
  return formatPeakRateWindow(sub.group, serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset))
}

const loading = ref(true)
const submitting = ref(false)
const errorMessage = ref('')
const errorHintMessage = ref('')
const activeTab = ref<'recharge' | 'subscription'>('recharge')
const amount = ref<number | null>(null)
const selectedMethod = ref('')
const selectedPlan = ref<SubscriptionPlan | null>(null)
type SubscriptionPeriod = 'month' | 'quarter' | 'year' | 'custom'
const selectedSubscriptionPeriod = ref<SubscriptionPeriod | ''>('')
const previewImage = ref('')

const paymentPhase = ref<'select' | 'paying'>('select')
const paymentModalVisible = ref(false)

interface CreateOrderOptions {
  openid?: string
  wechatResumeToken?: string
  paymentType?: string
  isResume?: boolean
  mobileQrFallbackAttempted?: boolean
  subscriptionId?: number
  /** Opaque reset-card quote binding; never derive a tier from client state. */
  resetCardTierRevision?: string
  /** Reserve a popup synchronously inside an explicit desktop checkout click. */
  preopenHostedPopup?: boolean
  /** Reuse this key for one local reset-card checkout attempt and its QR fallback. */
  idempotencyKey?: string
  /** Persisted before the reset-card create-order POST and bound after its response. */
  resetCardAttempt?: ResetCardCheckoutAttempt
}

interface WeixinJSBridgeLike {
  invoke(
    action: string,
    payload: Record<string, unknown>,
    callback: (result: Record<string, unknown>) => void,
  ): void
}

function emptyPaymentState(): PaymentRecoverySnapshot {
  return {
    orderId: 0,
    amount: 0,
    qrCode: '',
    expiresAt: '',
    paymentType: '',
    payUrl: '',
    outTradeNo: '',
    clientSecret: '',
    intentId: '',
    currency: '',
    countryCode: '',
    paymentEnv: '',
    payAmount: 0,
    orderType: '',
    paymentMode: '',
    resumeToken: '',
    alipayMobilePrecreateDeepLink: false,
    createdAt: 0,
  }
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

async function invokeWechatJsapiPayment(payload: Record<string, unknown>): Promise<Record<string, unknown>> {
  const bridge = await waitForWeixinJSBridge()
  if (!bridge) {
    throw new Error('WECHAT_JSAPI_UNAVAILABLE')
  }
  return new Promise((resolve) => {
    bridge.invoke('getBrandWCPayRequest', payload, (result) => resolve(result || {}))
  })
}

const paymentState = ref<PaymentRecoverySnapshot>(emptyPaymentState())

function persistRecoverySnapshot(snapshot: PaymentRecoverySnapshot) {
  if (typeof window === 'undefined' || !snapshot.orderId) return
  writePaymentRecoverySnapshot(window.localStorage, snapshot, PAYMENT_RECOVERY_STORAGE_KEY)
}

function removeRecoverySnapshot(snapshot: PaymentRecoverySnapshot = paymentState.value) {
  if (typeof window === 'undefined') return
  if (snapshot.orderId > 0) {
    clearPaymentRecoverySnapshot(window.localStorage, PAYMENT_RECOVERY_STORAGE_KEY, {
      orderId: snapshot.orderId,
      resumeToken: snapshot.resumeToken || undefined,
    })
    return
  }
  clearPaymentRecoverySnapshot(window.localStorage, PAYMENT_RECOVERY_STORAGE_KEY)
}

function resetPayment() {
  const previous = paymentState.value
  paymentPhase.value = 'select'
  paymentModalVisible.value = false
  paymentState.value = emptyPaymentState()
  removeRecoverySnapshot(previous)
}

function buildWechatOAuthAuthorizeUrl(
  authorizeUrl: string,
  context: { paymentType: string; orderType: OrderType; planId?: number; subscriptionId?: number; resetCardTierRevision?: string; orderAmount: number },
): string {
  const normalizedUrl = authorizeUrl.trim()
  if (!normalizedUrl || typeof window === 'undefined') {
    return normalizedUrl
  }

  try {
    const targetUrl = new URL(normalizedUrl, window.location.origin)
    const redirectPath = targetUrl.searchParams.get('redirect') || '/purchase'
    const redirectUrl = new URL(redirectPath, window.location.origin)
    const paymentType = normalizeVisibleMethod(context.paymentType) || context.paymentType.trim() || 'wxpay'

    redirectUrl.searchParams.set('payment_type', paymentType)
    redirectUrl.searchParams.set('order_type', context.orderType)

    if (context.planId) {
      redirectUrl.searchParams.set('plan_id', String(context.planId))
    } else {
      redirectUrl.searchParams.delete('plan_id')
    }
    if (context.subscriptionId) {
      redirectUrl.searchParams.set('subscription_id', String(context.subscriptionId))
    } else {
      redirectUrl.searchParams.delete('subscription_id')
    }
    const tierRevision = String(context.resetCardTierRevision || '').trim()
    if (tierRevision) {
      redirectUrl.searchParams.set('reset_card_tier_revision', tierRevision)
    } else {
      redirectUrl.searchParams.delete('reset_card_tier_revision')
    }

    if (context.orderAmount > 0) {
      redirectUrl.searchParams.set('amount', String(context.orderAmount))
    } else {
      redirectUrl.searchParams.delete('amount')
    }
    // A reset-card OAuth resume token carries a signed idempotency-key hash.
    // Never put the raw browser key into a redirect URL, browser history, or
    // OAuth cookie; remove an old query copy if a legacy URL supplied one.
    redirectUrl.searchParams.delete('payment_idempotency_key')
    targetUrl.searchParams.delete('payment_idempotency_key')

    targetUrl.searchParams.set('redirect', `${redirectUrl.pathname}${redirectUrl.search}`)
    return targetUrl.toString()
  } catch {
    return normalizedUrl
  }
}

function onPaymentDone() {
  const wasSubscription = paymentState.value.orderType === 'subscription'
  const wasResetCard = paymentState.value.orderType === 'reset_card'
  resetPayment()
  selectedPlan.value = null
  if (wasSubscription || wasResetCard) {
    subscriptionStore.fetchActiveSubscriptions(true).catch(() => {})
  }
}

function hidePaymentModal() {
  // Closing the shell does not cancel the order. The compact resume card keeps
  // the same order/session available until the user explicitly cancels it.
  paymentModalVisible.value = false
}

async function onPaymentSuccess() {
  // The panel emits this only for a server-recorded COMPLETED order. Keep the
  // terminal panel mounted until the user dismisses it; a browser callback is
  // never treated as an entitlement result.
  authStore.refreshUser().catch(() => {})
  if (paymentState.value.orderType === 'subscription' || paymentState.value.orderType === 'reset_card') {
    subscriptionStore.fetchActiveSubscriptions(true).catch(() => {})
  }
}

function onPaymentSettled(outcome: 'success' | 'cancelled' | 'expired') {
  const settled = paymentState.value
  if (settled.orderType === 'reset_card' && settled.orderId > 0 && typeof window !== 'undefined') {
    clearResetCardCheckoutAttempt(window.localStorage, { orderId: settled.orderId })
  }
  if (outcome === 'success') return
  removeRecoverySnapshot()
}

const paymentDialogTitle = computed(() => {
  if (paymentState.value.paymentType === 'alipay') return t('payment.methods.alipay')
  if (paymentState.value.paymentType === 'wxpay') return t('payment.methods.wxpay')
  return t('payment.checkoutTitle')
})

// All checkout data from single API call
const checkout = ref<CheckoutInfoResponse>({
  methods: {}, global_min: 0, global_max: 0,
  plans: [], balance_disabled: false, balance_recharge_multiplier: 1, subscription_usd_to_cny_rate: 0, recharge_fee_rate: 0, recharge_options: [], recharge_mode: undefined, help_text: '', help_image_url: '', banner: undefined, stripe_publishable_key: '',
})

const renderedHelpText = computed(() => DOMPurify.sanitize(
  marked.parse(checkout.value.help_text || '', { async: false, gfm: true, breaks: false }),
))

// 订阅功能开关（public settings 的 subscription_enabled，opt-out）。关闭后购买页只保留充值：
// 不再渲染「订阅」tab，只剩单个 tab 时顶部切换器也随之隐藏。
const subscriptionEnabled = computed(() => resolveFeatureFlag(appStore.cachedPublicSettings, FeatureFlags.subscription))

const tabs = computed(() => {
  const result: { key: 'recharge' | 'subscription'; label: string }[] = []
  if (!checkout.value.balance_disabled) result.push({ key: 'recharge', label: t('payment.tabTopUp') })
  if (subscriptionEnabled.value) result.push({ key: 'subscription', label: t('payment.tabSubscribe') })
  return result
})

// tab 列表随 checkout（balance_disabled）与订阅开关变化。当前 tab 不在列表里时收敛到第一个可用 tab，
// 两个方向都覆盖：关闭订阅 → 回到充值；仅订阅站点重新打开订阅 → 进入订阅。列表为空时模板展示不可用提示。
watch(tabs, (available) => {
  if (available.some((tab) => tab.key === activeTab.value)) return
  const leavingSubscription = activeTab.value === 'subscription'
  activeTab.value = available[0]?.key ?? 'recharge'
  if (leavingSubscription) selectedPlan.value = null
})

const visibleMethods = computed(() => getVisibleMethods(checkout.value.methods))
const enabledMethods = computed(() => Object.keys(visibleMethods.value))
const validAmount = computed(() => amount.value ?? 0)
const balanceRechargeMultiplier = computed(() => {
  const multiplier = checkout.value.balance_recharge_multiplier
  return Number.isFinite(multiplier) && multiplier > 0 ? multiplier : 1
})
// 订阅 CNY 换算汇率（1 USD = X CNY）。0 = 未配置，订阅保持 price 直付（与后端 opt-in 条件严格镜像）。
const subscriptionUsdToCnyRate = computed(() => {
  const rate = checkout.value.subscription_usd_to_cny_rate
  return Number.isFinite(rate) && rate > 0 ? rate : 0
})
function subscriptionPeriodOf(plan: SubscriptionPlan): SubscriptionPeriod {
  const label = String(plan.period_label || '').trim().toLowerCase()
  if (label === 'quarter' || label === 'year' || label === 'month') return label

  const unit = String(plan.validity_unit || '').trim().toLowerCase()
  if (unit.includes('quarter')) return 'quarter'
  if (unit.includes('year')) return 'year'
  if (unit.includes('month')) {
    if (plan.validity_days >= 80) return 'quarter'
    return 'month'
  }
  if (plan.validity_days >= 300) return 'year'
  if (plan.validity_days >= 80) return 'quarter'
  return 'custom'
}

function planDiscountPercent(plan: SubscriptionPlan): number {
  if (plan.discount_percent && plan.discount_percent > 0) return Math.round(plan.discount_percent)
  if (!plan.original_price || plan.original_price <= plan.price) return 0
  return Math.round((1 - plan.price / plan.original_price) * 100)
}

const subscriptionPeriodOptions = computed(() => {
  const order: SubscriptionPeriod[] = ['quarter', 'year', 'month', 'custom']
  const grouped = new Map<SubscriptionPeriod, number>()
  checkout.value.plans.forEach((plan) => {
    const key = subscriptionPeriodOf(plan)
    grouped.set(key, Math.max(grouped.get(key) || 0, planDiscountPercent(plan)))
  })
  return [...grouped.entries()]
    .sort(([left], [right]) => order.indexOf(left) - order.indexOf(right))
    .map(([key, discount]) => ({
      key,
      label: t(`payment.periods.${key}`),
      discountText: discount > 0 ? t('payment.savePercent', { percent: discount }) : '',
    }))
})

const visibleSubscriptionPlans = computed(() => {
  if (!selectedSubscriptionPeriod.value) return checkout.value.plans
  return checkout.value.plans.filter(plan => subscriptionPeriodOf(plan) === selectedSubscriptionPeriod.value)
})

function selectSubscriptionPeriod(period: SubscriptionPeriod) {
  selectedSubscriptionPeriod.value = period
  if (!selectedPlan.value || visibleSubscriptionPlans.value.some(plan => plan.id === selectedPlan.value?.id)) return
  const matching = visibleSubscriptionPlans.value.filter(plan => plan.group_id === selectedPlan.value?.group_id)
  selectedPlan.value = matching.length === 1 && matching[0].eligibility?.can_purchase !== false ? matching[0] : null
  errorMessage.value = ''
}

watch(subscriptionPeriodOptions, (options) => {
  if (!options.some(option => option.key === selectedSubscriptionPeriod.value)) {
    selectedSubscriptionPeriod.value = options[0]?.key || ''
  }
}, { immediate: true })

// Adaptive grid: single card stays full width, 2-col for two, 3-col beyond.
const planGridClass = computed(() => {
  const n = visibleSubscriptionPlans.value.length
  if (n <= 2) return 'grid grid-cols-1 gap-5 sm:grid-cols-2'
  return 'grid grid-cols-1 gap-5 sm:grid-cols-2 xl:grid-cols-3'
})

// Prefer the admin-selected recommendation. The discount fallback keeps older
// configurations useful until an admin explicitly selects a plan.
const featuredPlanId = computed<number | null>(() => {
  const recommended = visibleSubscriptionPlans.value.find(plan => plan.entitlements?.recommended)
  if (recommended) return recommended.id

  let best: SubscriptionPlan | null = null
  for (const plan of visibleSubscriptionPlans.value) {
    const percent = planDiscountPercent(plan)
    if (percent <= 0) continue
    if (!best || percent > planDiscountPercent(best)) best = plan
  }
  return best ? best.id : null
})

// Check if an amount fits a method's [min, max]. 0 = no limit.
function amountFitsMethod(amt: number, methodType: string): boolean {
  if (amt <= 0) return true
  const ml = visibleMethods.value[methodType]
  if (!ml) return false
  if (ml.single_min > 0 && amt < ml.single_min) return false
  if (ml.single_max > 0 && amt > ml.single_max) return false
  return true
}

// Visible methods decide the amount range shown to users.
const globalMinAmount = computed(() => {
  const limits = Object.values(visibleMethods.value)
  if (limits.length === 0) return 0
  if (limits.some(limit => limit.single_min <= 0)) return 0
  return Math.min(...limits.map(limit => limit.single_min))
})
const globalMaxAmount = computed(() => {
  const limits = Object.values(visibleMethods.value)
  if (limits.length === 0) return 0
  if (limits.some(limit => limit.single_max <= 0)) return 0
  return Math.max(...limits.map(limit => limit.single_max))
})

const fallbackRechargeOptions: RechargeOption[] = [20, 50, 100, 200, 500].map((amount, sort_order) => ({
  amount,
  sort_order,
  enabled: true,
}))
// The server reports whether it accepts arbitrary amounts. An empty tier list
// is not the same signal: the list is also empty when every configured tier
// fell outside the visible method limits, and in that case the server still
// rejects anything that is not a configured tier. Offering the hardcoded
// fallback there would show the user five amounts that all fail at checkout.
const acceptsCustomAmount = computed(() => {
  const mode = checkout.value.recharge_mode
  if (mode) return mode === 'custom'
  // Older servers do not send the mode; fall back to the previous inference.
  return checkout.value.recharge_options.filter(option => option.enabled).length === 0
})
const rechargePresetOptions = computed(() => {
  const isAllowed = (value: number) =>
    Number.isFinite(value)
    && value > 0
    && (globalMinAmount.value <= 0 || value >= globalMinAmount.value)
    && (globalMaxAmount.value <= 0 || value <= globalMaxAmount.value)
  const configured = checkout.value.recharge_options
    .filter(option => option.enabled && isAllowed(option.amount))
    .sort((left, right) => left.sort_order - right.sort_order || left.amount - right.amount)
  if (configured.length > 0) return configured
  return acceptsCustomAmount.value ? fallbackRechargeOptions.filter(option => isAllowed(option.amount)) : []
})
const rechargePresetAmounts = computed(() => rechargePresetOptions.value.map(option => option.amount))
const selectedRechargeOption = computed(() =>
  rechargePresetOptions.value.find(option => option.amount === validAmount.value) || null
)
const rechargeBalanceBonus = computed(() => selectedRechargeOption.value?.balance_bonus || 0)
// Mirrors the server's two-step rounding; a single round drifts by a cent.
const creditedAmount = computed(() =>
  creditedBalanceAmount(validAmount.value, balanceRechargeMultiplier.value, rechargeBalanceBonus.value)
)

// Platform credit is not a gateway charge, so it never takes a currency symbol.
function formatCreditAmount(value: number): string {
  const amount = Number.isFinite(value) ? value : 0
  return `${Number.isInteger(amount) ? amount : amount.toFixed(2)} ${t('payment.creditUnit')}`
}


// Selected method's limits (for validation and error messages)
const selectedLimit = computed(() => visibleMethods.value[selectedMethod.value])
const selectedCurrency = computed(() => normalizePaymentCurrency(selectedLimit.value?.currency))
const localeCode = computed(() => {
  const raw = i18n.locale as unknown
  if (typeof raw === 'string') return raw
  if (raw && typeof raw === 'object' && 'value' in raw) {
    return String((raw as { value?: string }).value || '')
  }
  return undefined
})

function currencyFractionDigits(currency: string): number {
  try {
    return new Intl.NumberFormat(undefined, {
      style: 'currency',
      currency,
    }).resolvedOptions().maximumFractionDigits ?? 2
  } catch {
    return 2
  }
}

function roundPaymentAmount(value: number, currency: string): number {
  if (!Number.isFinite(value)) return 0
  const factor = 10 ** currencyFractionDigits(currency)
  return Math.round(value * factor) / factor
}

function ceilPaymentAmount(value: number, currency: string): number {
  if (!Number.isFinite(value)) return 0
  const factor = 10 ** currencyFractionDigits(currency)
  return Math.ceil(value * factor) / factor
}

function subscriptionPaymentAmountForCurrency(value: number, currency: string): number {
  return roundPaymentAmount(subscriptionGatewayAmount(value, subscriptionUsdToCnyRate.value, currency), currency)
}

function formatSelectedPaymentAmount(value: number): string {
  return formatPaymentAmount(value, selectedCurrency.value, localeCode.value)
}


const methodOptions = computed<PaymentMethodOption[]>(() =>
  enabledMethods.value.map((type) => {
    const ml = visibleMethods.value[type]
    return {
      type,
      display_name: ml?.display_name,
      fee_rate: ml?.fee_rate ?? 0,
      available: ml?.available !== false && amountFitsMethod(validAmount.value, type),
    }
  })
)

const feeRate = computed(() => checkout.value?.recharge_fee_rate ?? 0)
const feeAmount = computed(() =>
  feeRate.value > 0 && validAmount.value > 0
    ? Math.ceil(((validAmount.value * feeRate.value) / 100) * 100) / 100
    : 0
)
const totalAmount = computed(() =>
  feeRate.value > 0 && validAmount.value > 0
    ? Math.round((validAmount.value + feeAmount.value) * 100) / 100
    : validAmount.value
)

const amountError = computed(() => {
  if (validAmount.value <= 0) return ''
  // No method can handle this amount
  if (!enabledMethods.value.some((m) => amountFitsMethod(validAmount.value, m))) {
    return t('payment.amountNoMethod')
  }
  // Selected method can't handle this amount (but others can)
  const ml = selectedLimit.value
  if (ml) {
    if (ml.single_min > 0 && validAmount.value < ml.single_min) return t('payment.amountTooLow', { min: formatSelectedPaymentAmount(ml.single_min) })
    if (ml.single_max > 0 && validAmount.value > ml.single_max) return t('payment.amountTooHigh', { max: formatSelectedPaymentAmount(ml.single_max) })
  }
  return ''
})

const canSubmit = computed(() =>
  validAmount.value > 0
    && selectedRechargeOption.value !== null
    && selectedRechargeOption.value.eligibility?.can_purchase !== false
    && amountFitsMethod(validAmount.value, selectedMethod.value)
    && selectedLimit.value?.available !== false
)

const subPaymentAmount = computed(() => {
  const price = selectedPlan.value?.price ?? 0
  return subscriptionPaymentAmountForCurrency(price, selectedCurrency.value)
})

const subFeeAmount = computed(() => {
  if (feeRate.value <= 0 || subPaymentAmount.value <= 0) return 0
  return ceilPaymentAmount((subPaymentAmount.value * feeRate.value) / 100, selectedCurrency.value)
})

const subTotalAmount = computed(() => {
  if (feeRate.value <= 0 || subPaymentAmount.value <= 0) return subPaymentAmount.value
  return roundPaymentAmount(subPaymentAmount.value + subFeeAmount.value, selectedCurrency.value)
})

function subscriptionTotalAmountForCurrency(value: number, currency: string): number {
  const paymentAmount = subscriptionPaymentAmountForCurrency(value, currency)
  if (feeRate.value <= 0 || paymentAmount <= 0) return paymentAmount
  const fee = ceilPaymentAmount((paymentAmount * feeRate.value) / 100, currency)
  return roundPaymentAmount(paymentAmount + fee, currency)
}

// Subscription-specific: method options based on gateway pay amount
const subMethodOptions = computed<PaymentMethodOption[]>(() => {
  const price = selectedPlan.value?.price ?? 0
  return enabledMethods.value.map((type) => {
    const ml = visibleMethods.value[type]
    const currency = normalizePaymentCurrency(ml?.currency)
    return {
      type,
      display_name: ml?.display_name,
      fee_rate: ml?.fee_rate ?? 0,
      available: ml?.available !== false && amountFitsMethod(subscriptionTotalAmountForCurrency(price, currency), type),
    }
  })
})

const canSubmitSubscription = computed(() =>
  selectedPlan.value !== null
    && selectedPlan.value.eligibility?.can_purchase !== false
    && amountFitsMethod(subTotalAmount.value, selectedMethod.value)
    && selectedLimit.value?.available !== false
)

// Auto-switch to first available method when current selection can't handle the amount
watch(() => [validAmount.value, selectedMethod.value] as const, ([amt, method]) => {
  if (amt <= 0 || amountFitsMethod(amt, method)) return
  const available = enabledMethods.value.find((m) => amountFitsMethod(amt, m))
  if (available) selectedMethod.value = available
})

/* ---------------------------------------------------------------------------
 * Order rail
 *
 * One summary serves both tabs. Recharge quotes the tier plus the gateway fee
 * and shows what lands in the balance; subscription quotes the converted plan
 * price plus the fee. Keeping them in one component is what stops the two flows
 * from drifting into two different ideas of "the amount".
 * ------------------------------------------------------------------------- */

const isRecharge = computed(() => activeTab.value === 'recharge')

const railProductName = computed(() => {
  if (isRecharge.value) {
    const option = selectedRechargeOption.value
    if (!option) return ''
    return option.label || t('payment.rechargeTierName', { amount: validAmount.value })
  }
  return selectedPlan.value?.name || ''
})

const railProductMeta = computed(() => {
  if (isRecharge.value) return selectedRechargeOption.value?.description || ''
  if (!selectedPlan.value) return ''
  return `${platformLabel(selectedPlan.value.group_platform || '')} · ${planValiditySuffix.value}`
})

const railMethods = computed(() => (isRecharge.value ? methodOptions.value : subMethodOptions.value))
const railBaseAmount = computed(() => (isRecharge.value ? validAmount.value : subPaymentAmount.value))
const railFeeAmount = computed(() => (isRecharge.value ? feeAmount.value : subFeeAmount.value))
const railTotalAmount = computed(() => (isRecharge.value ? totalAmount.value : subTotalAmount.value))

const railCreditLine = computed(() => {
  if (!isRecharge.value || validAmount.value <= 0) return ''
  return formatCreditAmount(creditedAmount.value)
})

// The rail is where a blocked purchase has to explain itself; the alternative
// is a disabled button with no reason attached.
const railNotice = computed(() => {
  const eligibility = isRecharge.value ? selectedRechargeOption.value?.eligibility : selectedPlan.value?.eligibility
  if (eligibility?.can_purchase === false) return t('payment.eligibility.minimum', { required: eligibility.required_total_recharge || 0, current: eligibility.current_total_recharge || 0 })
  if (isRecharge.value) {
    if (validAmount.value <= 0) return t('payment.selectTierFirst')
    return amountError.value
  }
  if (!selectedPlan.value) return t('payment.selectPlanFirst')
  return ''
})

const railCanSubmit = computed(() => (isRecharge.value ? canSubmit.value : canSubmitSubscription.value))

const railActionLabel = computed(() => {
  if (railTotalAmount.value <= 0) return t('payment.createOrder')
  return `${t('payment.createOrder')} ${formatSelectedPaymentAmount(railTotalAmount.value)}`
})

function handleRailSubmit() {
  if (isRecharge.value) {
    void handleSubmitRecharge()
    return
  }
  void confirmSubscribe()
}

// Payment button class: follows selected payment method color
const paymentButtonClass = computed(() => {
  const m = selectedMethod.value
  if (!m) return 'btn-primary'
  if (isBuiltInAlipayMethod(m)) return 'btn-alipay'
  if (isBuiltInWxpayMethod(m)) return 'btn-wxpay'
  if (m === 'stripe') return 'btn-stripe'
  if (m === 'airwallex') return 'btn-airwallex'
  return 'btn-primary'
})


// Renewal modal state
const showRenewalModal = ref(false)
const renewGroupId = ref<number | null>(null)
const renewalPlans = computed(() => {
  if (renewGroupId.value == null) return []
  return checkout.value.plans.filter(p => p.group_id === renewGroupId.value)
})

const planValiditySuffix = computed(() => {
  if (!selectedPlan.value) return ''
  return validitySuffixOf(selectedPlan.value, t)
})



function selectPlan(plan: SubscriptionPlan) {
  if (plan.eligibility?.can_purchase === false) return
  selectedSubscriptionPeriod.value = subscriptionPeriodOf(plan)
  selectedPlan.value = plan
  errorMessage.value = ''
}

function selectPlanFromModal(plan: SubscriptionPlan) {
  showRenewalModal.value = false
  renewGroupId.value = null
  selectPlan(plan)
}

function closeRenewalModal() {
  showRenewalModal.value = false
  renewGroupId.value = null
}

const RESET_CARD_GATEWAY_METHODS = ['alipay', 'wxpay'] as const

function resetCardGatewayAmount(quote: ResetCardQuote, paymentType: string): number {
  const currency = normalizePaymentCurrency(visibleMethods.value[paymentType]?.currency)
  const baseAmount = roundPaymentAmount(quote.price, currency)
  if (baseAmount <= 0 || feeRate.value <= 0) return baseAmount
  const fee = ceilPaymentAmount((baseAmount * feeRate.value) / 100, currency)
  return roundPaymentAmount(baseAmount + fee, currency)
}

function resetCardPaymentMethodForQuote(quote: ResetCardQuote): string {
  const eligible = RESET_CARD_GATEWAY_METHODS.filter((paymentType) => {
    const limit = visibleMethods.value[paymentType]
    if (!limit || limit.available === false || normalizePaymentCurrency(limit.currency) !== 'CNY') {
      return false
    }
    const gatewayAmount = resetCardGatewayAmount(quote, paymentType)
    return gatewayAmount > 0 && amountFitsMethod(gatewayAmount, paymentType)
  })
  const selected = normalizeVisibleMethod(selectedMethod.value)
  return selected && eligible.includes(selected as typeof RESET_CARD_GATEWAY_METHODS[number])
    ? selected
    : (eligible[0] || '')
}

function shouldPreopenHostedPopup(requestType: string, options: CreateOrderOptions): boolean {
  if (
    !options.preopenHostedPopup
    || options.isResume
    || typeof window === 'undefined'
    || isMobileDevice()
  ) {
    return false
  }

  const visibleMethod = normalizeVisibleMethod(requestType) || requestType
  if (visibleMethod === 'stripe') {
    return true
  }

  // Alipay is the only direct gateway whose desktop hosted checkout can use
  // the reserved popup. The mobile-precreate flag has no effect on a desktop
  // request, so only the explicit force-QR setting rules out this window.
  return visibleMethod === 'alipay'
    && !checkout.value.alipay_force_qrcode
}

async function handleSubmitRecharge() {
  if (!canSubmit.value || submitting.value) return
  await createOrder(validAmount.value, 'balance', undefined, { preopenHostedPopup: true })
}

async function confirmSubscribe() {
  if (!selectedPlan.value || submitting.value) return
  await createOrder(selectedPlan.value.price, 'subscription', selectedPlan.value.id, { preopenHostedPopup: true })
}

async function startResetCardCheckout(payload: { subscription: UserSubscription; quote: ResetCardQuote }) {
  if (submitting.value) return
  const userId = user.value?.id
  if (!Number.isSafeInteger(userId) || !userId || typeof window === 'undefined') {
    errorMessage.value = t('payment.result.failed')
    return
  }
  const paymentType = resetCardPaymentMethodForQuote(payload.quote)
  if (!paymentType) {
    errorMessage.value = t('payment.resetShop.paymentUnavailable')
    errorHintMessage.value = ''
    appStore.showError(errorMessage.value)
    return
  }
  selectedMethod.value = paymentType
  const attempt = getOrCreateResetCardCheckoutAttempt(window.localStorage, {
    userId,
    subscriptionId: payload.subscription.id,
    groupId: payload.quote.group_id,
    planId: payload.quote.plan_id,
    amount: payload.quote.price,
    monthlyPrice: payload.quote.monthly_price,
    expiresAt: payload.quote.expires_at,
    paymentType,
    tierRevision: payload.quote.reset_card_tier_revision,
  }, () => createIdempotencyKey('reset-card-payment'))
  await createOrder(payload.quote.price, 'reset_card', payload.quote.plan_id, {
    subscriptionId: payload.subscription.id,
    resetCardTierRevision: payload.quote.reset_card_tier_revision,
    paymentType,
    idempotencyKey: attempt.idempotencyKey,
    resetCardAttempt: attempt,
    preopenHostedPopup: true,
  })
}

async function createOrder(orderAmount: number, orderType: OrderType, planId?: number, options: CreateOrderOptions = {}) {
  submitting.value = true
  errorMessage.value = ''
  errorHintMessage.value = ''
  const requestType = normalizeVisibleMethod(options.paymentType || selectedMethod.value) || options.paymentType || selectedMethod.value
  const preopenedPopup = shouldPreopenHostedPopup(requestType, options)
    ? window.open('', 'paymentPopup', getPaymentPopupFeatures())
    : null
  let preopenedPopupNavigated = false
  const closePreopenedPopup = () => {
    if (preopenedPopup && !preopenedPopup.closed && !preopenedPopupNavigated) {
      preopenedPopup.close()
    }
  }
  try {
    const payload = buildCreateOrderPayload({
      amount: orderAmount,
      paymentType: requestType,
      orderType,
      planId,
      origin: typeof window !== 'undefined' ? window.location.origin : '',
      isMobile: isMobileDevice(),
      isWechatBrowser: typeof window !== 'undefined' && /MicroMessenger/i.test(window.navigator.userAgent),
      forceQRCode: !!(checkout.value.alipay_force_qrcode && normalizeVisibleMethod(requestType) === 'alipay'),
      mobilePrecreateDeepLink: checkout.value.alipay_mobile_precreate_deep_link === true,
      subscriptionId: options.subscriptionId,
      resetCardTierRevision: options.resetCardTierRevision,
    })
    if (options.openid) {
      payload.openid = options.openid
    }
    if (options.wechatResumeToken) {
      payload.wechat_resume_token = options.wechatResumeToken
    }

    const result = await paymentStore.createOrder(
      payload,
      options.idempotencyKey ? { headers: { 'Idempotency-Key': options.idempotencyKey } } : undefined,
    ) as CreateOrderResult & { resume_token?: string }
    if (orderType === 'reset_card' && options.resetCardAttempt && typeof window !== 'undefined') {
      recordResetCardCheckoutOrder(window.localStorage, options.resetCardAttempt, result.order_id)
    }
    const openWindow = (url: string) => {
      if (preopenedPopup && !preopenedPopup.closed) {
        try {
          preopenedPopup.location.href = url
          preopenedPopupNavigated = true
          return
        } catch {
          // Continue with the ordinary popup attempt below. A browser can
          // discard or deny access to a preopened browsing context.
        }
      }
      const win = window.open(url, 'paymentPopup', getPaymentPopupFeatures())
      if (!win || win.closed) {
        window.location.href = url
      }
    }
    const visibleMethod = normalizeVisibleMethod(requestType) || requestType
    // When user clicks the dedicated Stripe button, leave method blank so the
    // landing page renders Stripe's full Payment Element (card/link/alipay/wxpay).
    const stripeMethod = visibleMethod === 'stripe'
      ? ''
      : visibleMethod === 'wxpay' ? 'wechat_pay' : 'alipay'
    const stripeRouteUrl = result.client_secret && visibleMethod === 'stripe'
      ? router.resolve({
        path: '/payment/stripe',
        query: {
          order_id: String(result.order_id),
          client_secret: result.client_secret,
          method: stripeMethod || undefined,
          resume_token: result.resume_token || undefined,
        },
      }).href
      : ''
    const airwallexRouteUrl = result.client_secret && result.intent_id
      ? router.resolve({
        path: '/payment/airwallex',
        query: {
          order_id: String(result.order_id),
          out_trade_no: result.out_trade_no || undefined,
          resume_token: result.resume_token || undefined,
        },
      }).href
      : ''
    const decision = decidePaymentLaunch(result, {
      visibleMethod,
      orderType,
      isMobile: isMobileDevice(),
      isWechatBrowser: typeof window !== 'undefined' && /MicroMessenger/i.test(window.navigator.userAgent),
      forceQRCode: !!(checkout.value.alipay_force_qrcode && visibleMethod === 'alipay'),
      mobilePrecreateDeepLink: checkout.value.alipay_mobile_precreate_deep_link === true,
      stripePopupUrl: stripeRouteUrl,
      stripeRouteUrl,
      airwallexRouteUrl,
    })

    if (decision.kind === 'wechat_oauth' && decision.oauth?.authorize_url) {
      window.location.href = buildWechatOAuthAuthorizeUrl(decision.oauth.authorize_url, {
        paymentType: visibleMethod,
        orderType,
        planId,
        subscriptionId: options.subscriptionId,
        resetCardTierRevision: options.resetCardTierRevision,
        orderAmount,
      })
      return
    }

    if (decision.kind === 'unhandled') {
      applyScenarioError({ reason: 'UNHANDLED_PAYMENT_SCENARIO' }, visibleMethod)
      return
    }

    paymentState.value = decision.paymentState
    paymentPhase.value = 'paying'
    persistRecoverySnapshot(decision.recovery)
    paymentModalVisible.value = true

    if (decision.kind === 'stripe_popup') {
      openWindow(decision.paymentState.payUrl)
      return
    }
    if (decision.kind === 'stripe_route') {
      window.location.href = decision.paymentState.payUrl
      return
    }
    if (decision.kind === 'airwallex_route') {
      window.location.href = decision.paymentState.payUrl
      return
    }
    if (decision.kind === 'wechat_jsapi' && decision.jsapi) {
      try {
        const jsapiResult = await invokeWechatJsapiPayment(decision.jsapi as Record<string, unknown>)
        const errMsg = String(jsapiResult.err_msg || '').toLowerCase()
        if (errMsg.includes('cancel')) {
          appStore.showInfo(t('payment.qr.cancelled'))
          // JSAPI only reports that the app sheet was dismissed. The local
          // order can still be paid or awaiting callback, so retain its
          // order-scoped recovery context and continue polling in the shell.
          hidePaymentModal()
        } else if (errMsg && !errMsg.includes('ok')) {
          const fallbackApplied = await attemptMobileQrFallback(
            { reason: 'WECHAT_JSAPI_FAILED', message: errMsg },
            {
              orderAmount,
              orderType,
              planId,
              paymentType: visibleMethod,
              attempted: options.mobileQrFallbackAttempted === true,
              subscriptionId: options.subscriptionId,
              resetCardTierRevision: options.resetCardTierRevision,
              wechatResumeToken: options.wechatResumeToken,
              idempotencyKey: options.idempotencyKey,
              resetCardAttempt: options.resetCardAttempt,
            },
          )
          if (!fallbackApplied) {
            applyScenarioError({ reason: 'WECHAT_JSAPI_FAILED', message: errMsg }, visibleMethod)
          }
        } else {
          // The bridge callback is not a fulfillment proof. Keep polling the
          // same order until the server records COMPLETED.
          // PaymentStatusPanel will refresh user state only after COMPLETED.
        }
      } catch (err: unknown) {
        const fallbackApplied = await attemptMobileQrFallback(err, {
          orderAmount,
          orderType,
          planId,
          paymentType: visibleMethod,
          attempted: options.mobileQrFallbackAttempted === true,
          subscriptionId: options.subscriptionId,
          resetCardTierRevision: options.resetCardTierRevision,
          wechatResumeToken: options.wechatResumeToken,
          idempotencyKey: options.idempotencyKey,
          resetCardAttempt: options.resetCardAttempt,
        })
        if (!fallbackApplied) {
          throw err
        }
      }
      return
    }
    if (decision.kind === 'redirect_waiting' && decision.paymentState.payUrl) {
      if (isMobileDevice()) {
        window.location.href = decision.paymentState.payUrl
        return
      }
      openWindow(decision.paymentState.payUrl)
    }
  } catch (err: unknown) {
    const apiErr = err as Record<string, unknown>
    if (apiErr.reason === 'TOO_MANY_PENDING') {
      const metadata = apiErr.metadata as Record<string, unknown> | undefined
      errorMessage.value = t('payment.errors.tooManyPending', { max: metadata?.max || '' })
      errorHintMessage.value = ''
    } else if (apiErr.reason === 'CANCEL_RATE_LIMITED') {
      errorMessage.value = t('payment.errors.cancelRateLimited')
      errorHintMessage.value = ''
    } else if (await attemptMobileQrFallback(err, {
      orderAmount,
      orderType,
      planId,
      paymentType: requestType,
      attempted: options.mobileQrFallbackAttempted === true,
      subscriptionId: options.subscriptionId,
      resetCardTierRevision: options.resetCardTierRevision,
      wechatResumeToken: options.wechatResumeToken,
      idempotencyKey: options.idempotencyKey,
      resetCardAttempt: options.resetCardAttempt,
    })) {
      return
    } else {
      const handled = applyScenarioError(
        err,
        normalizeVisibleMethod(options.paymentType || selectedMethod.value) || selectedMethod.value,
      )
      if (!handled) {
        errorMessage.value = extractI18nErrorMessage(err, t, 'payment.errors', extractApiErrorMessage(err, t('payment.result.failed')))
        errorHintMessage.value = ''
      }
      if (handled) {
        return
      }
    }
    appStore.showError(buildPaymentErrorToastMessage(errorMessage.value, errorHintMessage.value))
  } finally {
    closePreopenedPopup()
    submitting.value = false
  }
}

interface MobileQrFallbackContext {
  orderAmount: number
  orderType: OrderType
  planId?: number
  paymentType: string
  attempted: boolean
  subscriptionId?: number
  resetCardTierRevision?: string
  wechatResumeToken?: string
  idempotencyKey?: string
  resetCardAttempt?: ResetCardCheckoutAttempt
}

function shouldFallbackToDesktopQr(err: unknown, paymentMethod: string, attempted: boolean): boolean {
  if (attempted || !isMobileDevice()) {
    return false
  }

  const normalizedMethod = normalizeVisibleMethod(paymentMethod) || paymentMethod
  const reason = typeof err === 'object' && err && 'reason' in err && typeof err.reason === 'string'
    ? err.reason
    : ''
  const message = err instanceof Error
    ? err.message
    : (typeof err === 'object' && err && 'message' in err && typeof err.message === 'string'
      ? err.message
      : '')
  const normalizedMessage = message.toLowerCase()

  if (normalizedMethod === 'wxpay') {
    return reason === 'WECHAT_H5_NOT_AUTHORIZED'
      || reason === 'WECHAT_PAYMENT_MP_NOT_CONFIGURED'
      || reason === 'WECHAT_JSAPI_FAILED'
      || reason === 'PAYMENT_GATEWAY_ERROR'
      || reason === 'UNHANDLED_PAYMENT_SCENARIO'
      || normalizedMessage.includes('weixinjsbridge is unavailable')
      || normalizedMessage.includes('wechat_jsapi_unavailable')
  }

  if (normalizedMethod === 'alipay') {
    return reason === 'PAYMENT_GATEWAY_ERROR' || reason === 'UNHANDLED_PAYMENT_SCENARIO'
  }

  return false
}

async function attemptMobileQrFallback(err: unknown, context: MobileQrFallbackContext): Promise<boolean> {
  if (!shouldFallbackToDesktopQr(err, context.paymentType, context.attempted)) {
    return false
  }

  try {
    const visibleMethod = normalizeVisibleMethod(context.paymentType) || context.paymentType
    const payload = buildCreateOrderPayload({
      amount: context.orderAmount,
      paymentType: visibleMethod,
      orderType: context.orderType,
      planId: context.planId,
      subscriptionId: context.subscriptionId,
      resetCardTierRevision: context.resetCardTierRevision,
      origin: typeof window !== 'undefined' ? window.location.origin : '',
      isMobile: false,
      isWechatBrowser: false,
    })
    if (context.wechatResumeToken) {
      payload.wechat_resume_token = context.wechatResumeToken
    }
    const result = await paymentStore.createOrder(
      payload,
      context.idempotencyKey ? { headers: { 'Idempotency-Key': context.idempotencyKey } } : undefined,
    ) as CreateOrderResult & { resume_token?: string }
    if (context.orderType === 'reset_card' && context.resetCardAttempt && typeof window !== 'undefined') {
      recordResetCardCheckoutOrder(window.localStorage, context.resetCardAttempt, result.order_id)
    }
    const stripeMethod = visibleMethod === 'wxpay' ? 'wechat_pay' : 'alipay'
    const stripeRouteUrl = visibleMethod === 'stripe' && result.client_secret
      ? router.resolve({
        path: '/payment/stripe',
        query: {
          order_id: String(result.order_id),
          client_secret: result.client_secret,
          method: stripeMethod,
          resume_token: result.resume_token || undefined,
        },
      }).href
      : ''
    const decision = decidePaymentLaunch(result, {
      visibleMethod,
      orderType: context.orderType,
      isMobile: false,
      isWechatBrowser: false,
      stripePopupUrl: stripeRouteUrl,
      stripeRouteUrl,
    })

    if (decision.kind !== 'qr_waiting' || !decision.paymentState.qrCode) {
      return false
    }

    errorMessage.value = ''
    errorHintMessage.value = ''
    paymentState.value = decision.paymentState
    paymentPhase.value = 'paying'
    paymentModalVisible.value = true
    persistRecoverySnapshot(decision.recovery)
    appStore.showWarning(t('payment.errors.mobilePaymentFallbackToQr'))
    return true
  } catch {
    return false
  }
}

function applyScenarioError(err: unknown, paymentMethod: string): boolean {
  const descriptor = describePaymentScenarioError(err, {
    paymentMethod,
    isMobile: isMobileDevice(),
    isWechatBrowser: typeof window !== 'undefined' && /MicroMessenger/i.test(window.navigator.userAgent),
  })
  if (!descriptor) {
    errorMessage.value = ''
    errorHintMessage.value = ''
    return false
  }
  errorMessage.value = t(descriptor.messageKey)
  errorHintMessage.value = descriptor.hintKey ? t(descriptor.hintKey) : ''
  appStore.showError(buildPaymentErrorToastMessage(errorMessage.value, errorHintMessage.value))
  return true
}

async function resumeWechatPaymentFromQuery() {
  const resume = parseWechatResumeRoute(route.query, checkout.value.plans, validAmount.value)
  if (!resume) {
    return
  }

  // OAuth callbacks can arrive after the site mode changes. Do not turn a
  // token-bearing subscription callback into a new order after subscriptions
  // have been disabled; discard the one-time resume context just as an
  // invalid token callback is cleaned from the route.
  if (resume.orderType === 'subscription' && !subscriptionEnabled.value) {
    await router.replace({ path: route.path, query: stripWechatResumeQuery(route.query) })
    errorMessage.value = t('payment.errors.PLAN_NOT_AVAILABLE')
    errorHintMessage.value = ''
    appStore.showError(errorMessage.value)
    return
  }

  const resetCardAttempt = resume.orderType === 'reset_card'
    && resume.wechatResumeToken
    && typeof window !== 'undefined'
    ? await matchResetCardCheckoutAttemptForResume(window.localStorage, resume.wechatResumeToken)
    : null

  selectedMethod.value = resume.paymentType
  if (resume.orderType === 'balance' && resume.orderAmount > 0) {
    amount.value = resume.orderAmount
  }
  if ((resume.orderType === 'subscription' || resume.orderType === 'reset_card') && resume.planId) {
    activeTab.value = 'subscription'
    selectedPlan.value = checkout.value.plans.find(plan => plan.id === resume.planId) ?? null
  }

  await router.replace({ path: route.path, query: stripWechatResumeQuery(route.query) })

  if (resume.wechatResumeToken) {
    await createOrder(0, resume.orderType, resume.planId, {
      wechatResumeToken: resume.wechatResumeToken,
      paymentType: resume.paymentType,
      isResume: true,
      subscriptionId: resume.subscriptionId,
      resetCardTierRevision: resume.resetCardTierRevision,
      // The signed token matched this local attempt by hash. Retain the raw
      // key only in request headers so an H5/JSAPI failure and its QR retry
      // replay the same server-side reset-card checkout.
      idempotencyKey: resetCardAttempt?.idempotencyKey,
      resetCardAttempt: resetCardAttempt || undefined,
    })
    return
  }

  if (resume.orderAmount > 0 && resume.openid) {
    await createOrder(resume.orderAmount, resume.orderType, resume.planId, {
      openid: resume.openid,
      paymentType: resume.paymentType,
      isResume: true,
      subscriptionId: resume.subscriptionId,
      resetCardTierRevision: resume.resetCardTierRevision,
    })
  }
}

onMounted(async () => {
  try {
    const res = await paymentAPI.getCheckoutInfo()
    checkout.value = res.data
    if (amount.value == null && rechargePresetAmounts.value.length > 0) {
      amount.value = rechargePresetOptions.value.find(option => option.eligibility?.can_purchase !== false)?.amount ?? null
    }
    if (enabledMethods.value.length) {
      const order: readonly string[] = METHOD_ORDER
      const sorted = [...enabledMethods.value].sort((a, b) => {
        const ai = order.indexOf(a)
        const bi = order.indexOf(b)
        return (ai === -1 ? 999 : ai) - (bi === -1 ? 999 : bi)
      })
      selectedMethod.value = sorted[0]
    }
    if (typeof window !== 'undefined') {
      const routeResumeToken = typeof route.query.resume_token === 'string'
        ? route.query.resume_token
        : typeof route.query.wechat_resume_token === 'string'
          ? route.query.wechat_resume_token
          : undefined
      const restored = readPaymentRecoverySnapshot(
        window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY),
        { resumeToken: routeResumeToken },
      )
      if (restored) {
        paymentState.value = restored
        paymentPhase.value = 'paying'
        paymentModalVisible.value = true
        const restoredMethod = normalizeVisibleMethod(restored.paymentType)
          || (visibleMethods.value[restored.paymentType] ? restored.paymentType : '')
        if (restoredMethod) {
          selectedMethod.value = restoredMethod
        }
      }
    }
    await resumeWechatPaymentFromQuery()
    // Handle desktop and renewal deep links. The hosted payment page remains
    // responsible for order creation, but preserves the user's desktop choice.
    if (route.query.tab === 'recharge' && tabs.value.some(tab => tab.key === 'recharge')) {
      activeTab.value = 'recharge'
      const requestedAmount = Number(route.query.amount)
      if (Number.isFinite(requestedAmount) && rechargePresetOptions.value.some(option => option.amount === requestedAmount)) {
        amount.value = requestedAmount
      }
    }
    // The tabs watcher selects the only valid tab after configuration changes.
    // Renewal deep links are ignored while subscriptions are disabled.
    if (route.query.tab === 'subscription' && subscriptionEnabled.value) {
      activeTab.value = 'subscription'
      const requestedPlanID = Number(route.query.plan_id)
      const requestedPlan = Number.isFinite(requestedPlanID)
        ? checkout.value.plans.find(plan => plan.id === requestedPlanID)
        : undefined
      if (requestedPlan) {
        selectPlan(requestedPlan)
      } else if (route.query.group) {
        const groupId = Number(route.query.group)
        const groupPlans = checkout.value.plans.filter(p => p.group_id === groupId)
        if (groupPlans.length === 1) {
          selectPlan(groupPlans[0])
        } else if (groupPlans.length > 1) {
          renewGroupId.value = groupId
          showRenewalModal.value = true
        }
      }
    }
  } catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
  finally { loading.value = false }
  // Fetch active subscriptions (uses cache, non-blocking); skipped when the subscription feature is off
  if (subscriptionEnabled.value) {
    subscriptionStore.fetchActiveSubscriptions().catch(() => {})
  }
})
</script>
