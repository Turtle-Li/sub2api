<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl">
      <div v-if="loading" class="flex items-center justify-center py-24">
        <div class="h-8 w-8 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
      </div>

      <template v-else>
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

        <div v-if="tabs.length" class="mb-6 flex flex-wrap items-center justify-between gap-3" data-test="purchase-toolbar">
          <div v-if="tabs.length > 1" role="tablist" class="payment-segment shrink-0">
            <button v-for="tab in tabs" :key="tab.key"
              role="tab"
              type="button"
              :aria-selected="activeTab === tab.key"
              :class="['payment-segment__item', activeTab === tab.key && 'payment-segment__item--active']"
              @click="activeTab = tab.key">{{ tab.label }}</button>
          </div>
          <div v-if="enabledMethods.length > 0 && subscriptionPeriodOptions.length > 1" :class="['min-w-0', activeTab !== 'subscription' && 'invisible pointer-events-none']" :inert="activeTab !== 'subscription' || undefined" :aria-hidden="activeTab !== 'subscription'">
            <div class="payment-segment max-w-full overflow-x-auto" role="group" :aria-label="t('payment.subscriptionSectionTitle')">
              <button v-for="period in subscriptionPeriodOptions" :key="period.key" type="button"
                :aria-pressed="selectedSubscriptionPeriod === period.key"
                :class="['payment-segment__item shrink-0', selectedSubscriptionPeriod === period.key && 'payment-segment__item--active']"
                @click="selectSubscriptionPeriod(period.key)">
                {{ period.label }}
                <span v-if="period.discountText" class="ml-1 hidden text-[11px] font-semibold text-emerald-600 dark:text-emerald-400 sm:inline">{{ period.discountText }}</span>
              </button>
            </div>
          </div>

        </div>
        <div v-if="tabs.length === 0" class="card py-16 text-center">
          <p class="text-gray-500 dark:text-gray-400">{{ t('payment.billingUnavailable') }}</p>
        </div>

        <template v-else>
          <div v-if="enabledMethods.length === 0" class="card py-16 text-center">
            <p class="text-gray-500 dark:text-gray-400">{{ t('payment.notAvailable') }}</p>
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
                ref="resetCardShop"
                :subscriptions="activeSubscriptions"
                :plans="checkout.plans"
                :disabled="submitting"
                :target-subscription-id="resetCardTargetSubscriptionId"
                :selected-subscription-id="selectedResetCard?.subscription.id ?? null"
                :selected-quote="selectedResetCard?.quote ?? null"
                :quantity="resetCardQuantity"
                :use-on-purchase="resetCardUseOnPurchase"
                @select="selectResetCard"
                @update-options="updateResetCardOptions"
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

            <PaymentDiscountCodeInput
              class="mt-4 lg:hidden"
              input-id="payment-discount-code-mobile"
              :model-value="couponCode"
              :applied="selectedCoupon?.quote"
              :applying="couponQuoting"
              :disabled="isResetCardCheckout || !railBaseCanSubmit"
              :placeholder="isResetCardCheckout ? t('payment.coupon.resetCardUnavailable') : undefined"
              :status="couponStatus"
              :error="!!couponError"
              @update:model-value="couponCode = $event"
              @apply="applyCoupon"
              @remove="removeCoupon"
            />

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
            :total-amount="railDisplayTotalAmount"
            :discount="selectedCoupon?.quote"
            :credit-line="railCreditLine"
            :credit-label="t('payment.creditedBalance')"
            :notice="railNotice"
            :action-label="railActionLabel"
            :button-class="paymentButtonClass"
            :disabled="!railCanSubmit"
            :submitting="submitting"
            :format-pay="formatRailPaymentAmount"
            :show-breakdown="activeTab === 'recharge' || feeRate > 0"
            methods-collapsed-on-mobile
            @select-method="selectedMethod = $event"
            @submit="handleRailSubmit"
          >
            <template #coupon>
              <PaymentDiscountCodeInput
                class="hidden lg:block"
                input-id="payment-discount-code-rail"
                :model-value="couponCode"
                :applied="selectedCoupon?.quote"
                :applying="couponQuoting"
                :disabled="isResetCardCheckout || !railBaseCanSubmit"
                :placeholder="isResetCardCheckout ? t('payment.coupon.resetCardUnavailable') : undefined"
                :status="couponStatus"
                :error="!!couponError"
                @update:model-value="couponCode = $event"
                @apply="applyCoupon"
                @remove="removeCoupon"
              />
            </template>
          </PaymentOrderRail>
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
      :close-on-click-outside="true"
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
        :checkout-frame-url="paymentState.checkoutFrameUrl"
        :order-type="paymentState.orderType"
        :currency="paymentState.currency || selectedCurrency"
        :out-trade-no="paymentState.outTradeNo"
        :mobile-alipay-deep-link="paymentState.alipayMobilePrecreateDeepLink"
        :payment-discount="paymentState.paymentDiscount"
        :wechat-jsapi="recoveredWechatJsapi"
        :initial-cancellation-pending="recoveryPendingState === 'cancellation'"
        :initial-confirmation-pending="recoveryPendingState === 'confirmation'"
        @done="onPaymentDone"
        @success="onPaymentSuccess"
        @settled="onPaymentSettled"
      />
    </BaseDialog>

    <BaseDialog
      :show="existingOrderPromptVisible"
      :title="t('payment.orderOps.existingOrderTitle')"
      width="narrow"
      data-test="existing-order-prompt"
      :close-on-escape="!existingOrderActionBusy"
      :close-on-click-outside="!existingOrderActionBusy"
      :show-close-button="!existingOrderActionBusy"
      @close="closeExistingOrderPrompt"
    >
      <div class="space-y-4">
        <div v-if="existingOrderLoading" class="flex items-center gap-3 py-4 text-sm text-gray-500 dark:text-gray-400" data-test="existing-order-loading">
          <span class="h-5 w-5 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"></span>
          {{ t('common.loading') }}
        </div>
        <div v-else-if="existingOrderLoadError" class="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300" data-test="existing-order-load-error" role="alert">
          {{ existingOrderLoadError }}
        </div>
        <template v-else-if="existingOrder">
          <p v-if="existingOrder.status === 'PENDING'" class="text-sm leading-6 text-gray-600 dark:text-gray-300">
            {{ t('payment.orderOps.existingOrderMessage') }}
          </p>
          <p v-else data-test="existing-order-status" class="text-sm leading-6 text-gray-600 dark:text-gray-300">
            {{ t(`payment.status.${existingOrder.status.toLowerCase()}`) }}
          </p>
          <dl class="space-y-2 rounded-xl bg-gray-50 p-4 text-sm dark:bg-dark-800" data-test="existing-order-details">
            <div class="flex items-start justify-between gap-4">
              <dt class="shrink-0 text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</dt>
              <dd data-test="existing-order-number" class="min-w-0 break-all text-right font-mono text-xs text-gray-900 dark:text-white">{{ existingOrder.out_trade_no }}</dd>
            </div>
            <div class="flex items-start justify-between gap-4">
              <dt class="shrink-0 text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</dt>
              <dd data-test="existing-order-amount" class="text-right font-semibold text-gray-900 dark:text-white">{{ formatExistingOrderAmount(existingOrder) }}</dd>
            </div>
            <div class="flex items-start justify-between gap-4">
              <dt class="shrink-0 text-gray-500 dark:text-gray-400">{{ t('payment.orders.paymentMethod') }}</dt>
              <dd data-test="existing-order-payment-method" class="text-right text-gray-900 dark:text-white">{{ existingOrderPaymentMethodLabel(existingOrder) }}</dd>
            </div>
            <div class="flex items-start justify-between gap-4">
              <dt class="shrink-0 text-gray-500 dark:text-gray-400">{{ t('payment.orders.product') }}</dt>
              <dd data-test="existing-order-product" class="min-w-0 break-words text-right text-gray-900 dark:text-white">{{ existingOrderProductName(existingOrder) }}</dd>
            </div>
            <div class="flex items-start justify-between gap-4">
              <dt class="shrink-0 text-gray-500 dark:text-gray-400">{{ t('payment.admin.orderType') }}</dt>
              <dd data-test="existing-order-type" class="text-right text-gray-900 dark:text-white">{{ existingOrderTypeLabel(existingOrder) }}</dd>
            </div>
            <div class="flex items-start justify-between gap-4">
              <dt class="shrink-0 text-gray-500 dark:text-gray-400">{{ t('payment.orders.createdAt') }}</dt>
              <dd data-test="existing-order-created-at" class="text-right text-gray-900 dark:text-white">{{ formatExistingOrderDate(existingOrder.created_at) }}</dd>
            </div>
            <div class="flex items-start justify-between gap-4">
              <dt class="shrink-0 text-gray-500 dark:text-gray-400">{{ t('payment.admin.expiresAt') }}</dt>
              <dd data-test="existing-order-expires-at" class="text-right text-gray-900 dark:text-white">{{ formatExistingOrderDate(existingOrder.expires_at) }}</dd>
            </div>
          </dl>
          <p v-if="existingOrderActionError" class="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300" data-test="existing-order-action-error" role="alert">
            {{ existingOrderActionError }}
          </p>
        </template>
      </div>
      <template #footer>
        <div class="flex flex-wrap justify-end gap-3">
          <template v-if="existingOrderLoadError">
            <button type="button" class="btn btn-secondary" @click="closeExistingOrderPrompt">
              {{ t('common.close') }}
            </button>
            <button type="button" class="btn btn-primary" data-test="retry-existing-order" @click="retryExistingOrderPrompt">
              {{ t('common.retry') }}
            </button>
          </template>
          <template v-else-if="existingOrder && existingOrder.status === 'PENDING'">
            <button
              ref="existingOrderCancelButton"
              type="button"
              class="btn btn-secondary"
              data-test="cancel-existing-order"
              :disabled="existingOrderActionBusy"
              @click="existingOrderCancellationRetryNeeded ? retryCancelExistingOrder() : askCancelExistingOrder()"
            >
              {{ existingOrderCancelLoading ? t('common.processing') : existingOrderCancellationRetryNeeded ? t('payment.orderOps.retryCancellation') : t('payment.orderOps.cancelExistingOrder') }}
            </button>
            <button v-if="!existingOrderCancellationRetryNeeded" type="button" class="btn btn-primary" data-test="open-existing-order" :disabled="existingOrderActionBusy" @click="openExistingOrder">
              {{ existingOrderContinueLoading ? t('common.processing') : t('payment.orderOps.openExistingOrder') }}
            </button>
          </template>
          <button v-else-if="existingOrder" type="button" class="btn btn-primary" :disabled="existingOrderActionBusy" @click="closeExistingOrderPrompt">
            {{ t('common.close') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="cancelExistingOrderConfirmVisible"
      :title="t('payment.orderOps.cancelOrderConfirmTitle')"
      :message="t('payment.orderOps.cancelOrderConfirmMessage')"
      :confirm-text="t('payment.orders.cancel')"
      :cancel-text="t('payment.orderOps.keepOrder')"
      :danger="true"
      data-test="cancel-existing-order-confirm"
      @confirm="confirmCancelExistingOrder"
      @cancel="closeExistingOrderCancelConfirmation"
    />

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
import { ref, computed, nextTick, onMounted, onBeforeUnmount, watch } from 'vue'
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
import { extractApiErrorCode, extractApiErrorMessage, extractI18nErrorMessage, extractMappedI18nErrorMessage } from '@/utils/apiError'
import { isMobileDevice } from '@/utils/device'
import type {
  CheckoutInfoResponse,
  CreateOrderResult,
  OrderType,
  PaymentCouponQuoteRequest,
  PaymentDiscountQuote,
  PaymentDiscountSnapshot,
  PaymentOrder,
  RechargeOption,
  SubscriptionPlan,
  WechatJSAPIPayload,
} from '@/types/payment'
import type { UserSubscription } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import AmountInput from '@/components/payment/AmountInput.vue'
import PaymentMethodSelector from '@/components/payment/PaymentMethodSelector.vue'
import { METHOD_ORDER, getPaymentPopupFeatures, isBuiltInAlipayMethod, isBuiltInWxpayMethod } from '@/components/payment/providerConfig'
import {
  PAYMENT_RECOVERY_STORAGE_KEY,
  clearQueuedPaymentCancellation,
  buildCreateOrderPayload,
  clearResetCardCheckoutAttempt,
  clearPaymentRecoverySnapshot,
  decidePaymentLaunch,
  discardResetCardCheckoutAttemptForSelectionChange,
  getOrCreateResetCardCheckoutAttempt,
  getVisibleMethods,
  matchResetCardCheckoutAttemptForResume,
  normalizeVisibleMethod,
  queuePaymentCancellation,
  readQueuedPaymentCancellationIds,
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
import PaymentDiscountCodeInput from '@/components/payment/PaymentDiscountCodeInput.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatPaymentAmount, normalizePaymentCurrency } from '@/components/payment/currency'
import { creditedBalanceAmount, subscriptionGatewayAmount } from '@/components/payment/pricing'
import { planValiditySuffix as validitySuffixOf } from '@/components/payment/validity'
import type { PaymentMethodOption } from '@/components/payment/PaymentMethodSelector.vue'
import { buildPaymentErrorToastMessage, describePaymentScenarioError } from './paymentUx'
import { hasWechatResumeQuery, parseWechatResumeRoute, stripWechatResumeQuery } from './paymentWechatResume'
import { createIdempotencyKey } from '@/utils/idempotency'
import { formatDateTimeToMinute } from '@/utils/format'
import { purchaseName } from '@/components/payment/orderPresentation'

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

type ResetCardShopHandle = {
  focusOffer: (subscriptionId?: number | null) => boolean
}

const resetCardShop = ref<ResetCardShopHandle | null>(null)
const resetCardTargetSubscriptionId = ref<number | null>(null)

function getDaysRemaining(expiresAt: string): number {
  const diff = new Date(expiresAt).getTime() - Date.now()
  return Math.max(0, Math.ceil(diff / (1000 * 60 * 60 * 24)))
}

function isResetCardPurchaseQuery(): boolean {
  return route.query.purchase === 'reset_card'
}

function resetCardSubscriptionIdFromQuery(): number | null {
  if (!isResetCardPurchaseQuery()) return null
  const subscriptionId = Number(route.query.subscription_id)
  return Number.isSafeInteger(subscriptionId) && subscriptionId > 0 ? subscriptionId : null
}

async function focusResetCardShop() {
  await nextTick()
  const shop = resetCardShop.value
  if (typeof shop?.focusOffer === 'function') {
    shop.focusOffer(resetCardTargetSubscriptionId.value)
  }
}



const loading = ref(true)
const submitting = ref(false)
const errorMessage = ref('')
const errorHintMessage = ref('')
const activeTab = ref<'recharge' | 'subscription'>('recharge')
const amount = ref<number | null>(null)
const selectedMethod = ref('')
const selectedPlan = ref<SubscriptionPlan | null>(null)
interface ResetCardSelection {
  subscription: UserSubscription
  quote: ResetCardQuote
}
const selectedResetCard = ref<ResetCardSelection | null>(null)
const pendingResetCardSelection = ref<ResetCardSelection | null>(null)
const resetCardQuantity = ref(1)
const resetCardUseOnPurchase = ref(false)
const isResetCardCheckout = computed(() => selectedResetCard.value !== null)
type SubscriptionPeriod = 'month' | 'quarter' | 'year' | 'custom'
const selectedSubscriptionPeriod = ref<SubscriptionPeriod | ''>('')
const previewImage = ref('')

interface CouponQuoteContext {
  couponCode: string
  amount: number
  paymentType: string
  orderType: OrderType
  planId?: number
  subscriptionId?: number
  resetCardTierRevision?: string
  key: string
}

interface AppliedCoupon {
  quote: PaymentDiscountQuote
  context: CouponQuoteContext
  /** Replayed after a response-loss retry only while this exact quote remains selected. */
  idempotencyKey: string
}

const couponCode = ref('')
const couponQuoting = ref(false)
const couponError = ref('')
const appliedCoupon = ref<AppliedCoupon | null>(null)
let couponQuoteRequest = 0

const paymentPhase = ref<'select' | 'paying'>('select')
const paymentModalVisible = ref(false)
const existingOrderPromptVisible = ref(false)
const cancelExistingOrderConfirmVisible = ref(false)
const existingOrder = ref<PaymentOrder | null>(null)
const existingOrderLoading = ref(false)
const existingOrderLoadError = ref('')
const existingOrderActionError = ref('')
const existingOrderContinueLoading = ref(false)
const existingOrderCancelLoading = ref(false)
const existingOrderCancellationRetryNeeded = ref(false)
const existingOrderCancelButton = ref<HTMLButtonElement | null>(null)
const existingOrderRequestedId = ref<number | null>(null)
let existingOrderPromptRequest = 0
let existingOrderActionRequest = 0
const existingOrderActionBusy = computed(() => existingOrderContinueLoading.value || existingOrderCancelLoading.value)

interface CreateOrderOptions {
  openid?: string
  wechatResumeToken?: string
  paymentType?: string
  isResume?: boolean
  mobileQrFallbackAttempted?: boolean
  subscriptionId?: number
  /** Opaque reset-card quote binding; never derive a tier from client state. */
  resetCardTierRevision?: string
  resetCardQuantity?: number
  resetCardUseOnPurchase?: boolean
  /** Reserve a popup synchronously inside an explicit desktop checkout click. */
  preopenHostedPopup?: boolean
  /** Reuse this key for one local reset-card checkout attempt and its QR fallback. */
  idempotencyKey?: string
  /** Persisted before the reset-card create-order POST and bound after its response. */
  resetCardAttempt?: ResetCardCheckoutAttempt
  /** Fresh quoted coupon for this exact order; never copied into an OAuth resume. */
  coupon?: AppliedCoupon
  /** Display-only quote restored from a signed WeChat resume flow. */
  paymentDiscountDisplay?: PaymentDiscountSnapshot
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
    checkoutFrameUrl: '',
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
type RecoveryPendingState = 'cancellation' | 'confirmation' | null
const recoveredWechatJsapi = ref<WechatJSAPIPayload | undefined>()
const recoveryPendingState = ref<RecoveryPendingState>(null)

function setRecoveredPaymentState(
  snapshot: PaymentRecoverySnapshot,
  options: { wechatJsapi?: WechatJSAPIPayload; pendingState?: RecoveryPendingState } = {},
) {
  paymentState.value = snapshot
  recoveredWechatJsapi.value = options.wechatJsapi
  recoveryPendingState.value = options.pendingState ?? null
  paymentPhase.value = 'paying'
  paymentModalVisible.value = true
  const restoredMethod = normalizeVisibleMethod(snapshot.paymentType)
    || (visibleMethods.value[snapshot.paymentType] ? snapshot.paymentType : '')
  if (restoredMethod) selectedMethod.value = restoredMethod
}

function snapshotWithoutLaunchMaterial(snapshot: PaymentRecoverySnapshot): PaymentRecoverySnapshot {
  return {
    ...snapshot,
    qrCode: '',
    payUrl: '',
    checkoutFrameUrl: '',
    clientSecret: '',
    intentId: '',
    paymentMode: '',
    alipayMobilePrecreateDeepLink: false,
    createdAt: Date.now(),
  }
}

const queuedCancellationInFlight = new Set<number>()

function recoverySnapshotForExistingOrder(order: PaymentOrder): PaymentRecoverySnapshot {
  return {
    ...emptyPaymentState(),
    orderId: order.id,
    amount: order.amount,
    payAmount: order.pay_amount,
    currency: order.currency || '',
    paymentType: order.payment_type,
    outTradeNo: order.out_trade_no,
    orderType: order.order_type,
    expiresAt: order.expires_at,
    createdAt: Date.now(),
  }
}

function settlePersistedCancellation(orderId: number, message: 'cancelled' | 'already_paid') {
  if (typeof window === 'undefined') return
  clearQueuedPaymentCancellation(window.localStorage, orderId)
  const current = paymentState.value
  const currentMatches = current.orderId === orderId
  const stored = currentMatches
    ? current
    : readPaymentRecoverySnapshot(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY))
  const snapshot = stored?.orderId === orderId ? stored : null

  if (message === 'cancelled') {
    clearPaymentRecoverySnapshot(window.localStorage, PAYMENT_RECOVERY_STORAGE_KEY, { orderId })
    clearResetCardCheckoutAttempt(window.localStorage, { orderId })
    if (!currentMatches || !current.cancellationRequested) return
    const nextResetCardSelection = pendingResetCardSelection.value
    const wasResetCard = current.orderType === 'reset_card'
    resetPayment()
    pendingResetCardSelection.value = null
    if (wasResetCard) clearResetCardSelection()
    if (nextResetCardSelection) applyResetCardSelection(nextResetCardSelection)
    return
  }

  if (!snapshot) return
  const safeSnapshot = {
    ...snapshotWithoutLaunchMaterial(snapshot),
    cancellationRequested: undefined,
  }
  if (currentMatches) {
    setRecoveredPaymentState(safeSnapshot, { pendingState: 'confirmation' })
  }
  persistRecoverySnapshot(safeSnapshot)
}

async function commitQueuedPaymentCancellation(orderId: number): Promise<void> {
  if (typeof window === 'undefined' || queuedCancellationInFlight.has(orderId)) return
  queuedCancellationInFlight.add(orderId)
  try {
    const response = await paymentAPI.cancelOrder(orderId)
    const message = response.data?.message
    if (message === 'cancelled' || message === 'already_paid') {
      settlePersistedCancellation(orderId, message)
    }
  } catch {
    // Preserve the local intent for a later explicit retry or a future visit.
  } finally {
    queuedCancellationInFlight.delete(orderId)
  }
}

function flushQueuedPaymentCancellations() {
  if (typeof window === 'undefined') return
  for (const orderId of readQueuedPaymentCancellationIds(window.localStorage)) {
    void commitQueuedPaymentCancellation(orderId)
  }
}

async function resumeStoredPayment(snapshot: PaymentRecoverySnapshot): Promise<void> {
  const orderType: OrderType = snapshot.orderType || 'balance'
  const cancellationQueued = typeof window !== 'undefined'
    && readQueuedPaymentCancellationIds(window.localStorage).includes(snapshot.orderId)
  if (snapshot.cancellationRequested || cancellationQueued) {
    if (!cancellationQueued) queuePaymentCancellation(window.localStorage, snapshot.orderId)
    void commitQueuedPaymentCancellation(snapshot.orderId)
    return
  }
  try {
    const response = await paymentAPI.resumeOrder(snapshot.orderId)
    const result = response.data
    const visibleMethod = normalizeVisibleMethod(result.payment_type || snapshot.paymentType)
      || result.payment_type
      || snapshot.paymentType
    const decision = decidePaymentLaunch(result, {
      visibleMethod,
      orderType,
      isMobile: isMobileDevice(),
      isWechatBrowser: /MicroMessenger/i.test(window.navigator.userAgent),
      forceQRCode: !!(checkout.value.alipay_force_qrcode && visibleMethod === 'alipay'),
      mobilePrecreateDeepLink: checkout.value.alipay_mobile_precreate_deep_link === true,
      paymentDiscount: result.payment_discount ?? snapshot.paymentDiscount,
      resetCardQuantity: snapshot.resetCardQuantity,
      resetCardUseOnPurchase: snapshot.resetCardUseOnPurchase,
    })

    if (decision.kind === 'unhandled') {
      removeRecoverySnapshot(snapshot)
      return
    }
    if (decision.kind === 'wechat_oauth' && decision.oauth?.authorize_url) {
      persistRecoverySnapshot(decision.recovery)
      window.location.href = buildWechatOAuthAuthorizeUrl(decision.oauth.authorize_url, {
        paymentType: visibleMethod,
        orderType,
        resetCardQuantity: snapshot.resetCardQuantity,
        resetCardUseOnPurchase: snapshot.resetCardUseOnPurchase,
        orderAmount: result.amount,
      })
      return
    }

    setRecoveredPaymentState(decision.paymentState, {
      wechatJsapi: decision.kind === 'wechat_jsapi' ? decision.jsapi : undefined,
    })
    persistRecoverySnapshot(decision.recovery)
  } catch (err: unknown) {
    const code = extractApiErrorCode(err)
    const pendingState: RecoveryPendingState = code === 'PAYMENT_CANCELLATION_PENDING'
      ? 'cancellation'
      : code === 'PAYMENT_CONFIRMATION_PENDING'
        ? 'confirmation'
        : null
    if (!pendingState) {
      removeRecoverySnapshot(snapshot)
      return
    }

    // Cached provider URLs are never an authority. Keep only this local order
    // reference while the server confirms its close/payment outcome.
    const safeSnapshot = snapshotWithoutLaunchMaterial(snapshot)
    removeRecoverySnapshot(snapshot)
    setRecoveredPaymentState(safeSnapshot, { pendingState })
    persistRecoverySnapshot(safeSnapshot)
  }
}

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
  recoveredWechatJsapi.value = undefined
  recoveryPendingState.value = null
  if (!previous.cancellationRequested) removeRecoverySnapshot(previous)
}

function buildWechatOAuthAuthorizeUrl(
  authorizeUrl: string,
  context: { paymentType: string; orderType: OrderType; planId?: number; subscriptionId?: number; resetCardTierRevision?: string; resetCardQuantity?: number; resetCardUseOnPurchase?: boolean; orderAmount: number },
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
    const resetCardQuantity = Number(context.resetCardQuantity)
    if (context.orderType === 'reset_card' && Number.isSafeInteger(resetCardQuantity) && resetCardQuantity >= 1 && resetCardQuantity <= 99) {
      redirectUrl.searchParams.set('reset_card_quantity', String(resetCardQuantity))
    } else {
      redirectUrl.searchParams.delete('reset_card_quantity')
    }
    if (context.orderType === 'reset_card' && context.resetCardUseOnPurchase === true) {
      redirectUrl.searchParams.set('reset_card_use_on_purchase', '1')
    } else {
      redirectUrl.searchParams.delete('reset_card_use_on_purchase')
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
  if (wasResetCard) clearResetCardSelection()
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
  removeCoupon()
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
  if (outcome === 'cancelled') {
    // PaymentStatusPanel emits this only after the local cancellation commit.
    // Do not queue a second cancellation or retain stale launch material.
    if (typeof window !== 'undefined' && settled.orderId > 0) {
      clearQueuedPaymentCancellation(window.localStorage, settled.orderId)
    }
    removeRecoverySnapshot(settled)
    return
  }
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
  if (leavingSubscription) {
    selectedPlan.value = null
    clearResetCardSelection()
  }
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
    if (plan.validity_days >= 12) return 'year'
    if (plan.validity_days >= 3) return 'quarter'
    return 'month'
  }
  if (unit.includes('week')) {
    if (plan.validity_days >= 43) return 'year'
    if (plan.validity_days >= 12) return 'quarter'
    return 'custom'
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
  const order: SubscriptionPeriod[] = ['month', 'quarter', 'year', 'custom']
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

// Recommendation is an explicit catalog choice, independent of discounts.
const featuredPlanId = computed<number | null>(() =>
  visibleSubscriptionPlans.value.find(plan => plan.entitlements?.recommended)?.id ?? null,
)

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

function formatSelectedPaymentAmount(value: number | string): string {
  return formatPaymentAmount(Number(value), selectedCurrency.value, localeCode.value)
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

function normalizeResetCardQuantity(value: unknown): number {
  const parsed = typeof value === 'number' ? value : Number.parseInt(String(value), 10)
  if (!Number.isSafeInteger(parsed)) return 1
  return Math.min(99, Math.max(1, parsed))
}

function resetCardBaseAmountForCurrency(quote: ResetCardQuote, quantity: number, currency: string): number {
  return roundPaymentAmount(quote.price * normalizeResetCardQuantity(quantity), currency)
}

function resetCardGatewayAmount(quote: ResetCardQuote, paymentType: string, quantity = 1): number {
  const currency = normalizePaymentCurrency(visibleMethods.value[paymentType]?.currency)
  const baseAmount = resetCardBaseAmountForCurrency(quote, quantity, currency)
  if (baseAmount <= 0 || feeRate.value <= 0) return baseAmount
  const fee = ceilPaymentAmount((baseAmount * feeRate.value) / 100, currency)
  return roundPaymentAmount(baseAmount + fee, currency)
}

const RESET_CARD_GATEWAY_METHODS = ['alipay', 'wxpay'] as const

function resetCardPaymentMethodForQuote(quote: ResetCardQuote, quantity = 1): string {
  const eligible = RESET_CARD_GATEWAY_METHODS.filter((paymentType) => {
    const limit = visibleMethods.value[paymentType]
    if (!limit || limit.available === false || normalizePaymentCurrency(limit.currency) !== 'CNY') {
      return false
    }
    const gatewayAmount = resetCardGatewayAmount(quote, paymentType, quantity)
    return gatewayAmount > 0 && amountFitsMethod(gatewayAmount, paymentType)
  })
  const selected = normalizeVisibleMethod(selectedMethod.value)
  return selected && eligible.includes(selected as typeof RESET_CARD_GATEWAY_METHODS[number])
    ? selected
    : (eligible[0] || '')
}

const resetCardMethodOptions = computed<PaymentMethodOption[]>(() => {
  const selection = selectedResetCard.value
  if (!selection) return []
  return RESET_CARD_GATEWAY_METHODS.map((type) => {
    const limit = visibleMethods.value[type]
    const eligible = !!limit
      && limit.available !== false
      && normalizePaymentCurrency(limit.currency) === 'CNY'
      && amountFitsMethod(resetCardGatewayAmount(selection.quote, type, resetCardQuantity.value), type)
    return {
      type,
      display_name: limit?.display_name,
      fee_rate: limit?.fee_rate ?? 0,
      available: eligible,
    }
  }).filter(option => visibleMethods.value[option.type])
})

const resetCardBaseAmount = computed(() => {
  const selection = selectedResetCard.value
  return selection ? resetCardBaseAmountForCurrency(selection.quote, resetCardQuantity.value, 'CNY') : 0
})
const resetCardFeeAmount = computed(() => {
  if (feeRate.value <= 0 || resetCardBaseAmount.value <= 0) return 0
  return ceilPaymentAmount((resetCardBaseAmount.value * feeRate.value) / 100, 'CNY')
})
const resetCardTotalAmount = computed(() => {
  if (feeRate.value <= 0 || resetCardBaseAmount.value <= 0) return resetCardBaseAmount.value
  return roundPaymentAmount(resetCardBaseAmount.value + resetCardFeeAmount.value, 'CNY')
})
const canSubmitResetCard = computed(() => {
  const selection = selectedResetCard.value
  if (!selection) return false
  const paymentType = normalizeVisibleMethod(selectedMethod.value) || selectedMethod.value
  return RESET_CARD_GATEWAY_METHODS.includes(paymentType as typeof RESET_CARD_GATEWAY_METHODS[number])
    && resetCardTotalAmount.value > 0
    && amountFitsMethod(resetCardTotalAmount.value, paymentType)
    && visibleMethods.value[paymentType]?.available !== false
    && normalizePaymentCurrency(visibleMethods.value[paymentType]?.currency) === 'CNY'
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
const railCurrency = computed(() => isResetCardCheckout.value ? 'CNY' : selectedCurrency.value)

const railProductName = computed(() => {
  if (isRecharge.value) {
    const option = selectedRechargeOption.value
    if (!option) return ''
    return option.label || t('payment.rechargeTierName', { amount: validAmount.value })
  }
  if (isResetCardCheckout.value) return t('payment.resetShop.title')
  return selectedPlan.value?.name || ''
})

const railProductMeta = computed(() => {
  if (isRecharge.value) return selectedRechargeOption.value?.description || ''
  const resetCard = selectedResetCard.value
  if (resetCard) {
    return `${resetCard.subscription.group?.name || t('payment.groupFallback', { id: resetCard.subscription.group_id })} · ${t('payment.resetShop.quantitySummary', { quantity: resetCardQuantity.value })} · ${resetCard.quote.validity_days ? t('payment.resetShop.validDays', { days: resetCard.quote.validity_days }) : t('payment.resetShop.validUntil', { date: formatResetCardExpiry(resetCard.quote.expires_at) })}`
  }
  if (!selectedPlan.value) return ''
  return `${platformLabel(selectedPlan.value.group_platform || '')} · ${planValiditySuffix.value}`
})

const railMethods = computed(() => (isRecharge.value ? methodOptions.value : isResetCardCheckout.value ? resetCardMethodOptions.value : subMethodOptions.value))
const railBaseAmount = computed(() => (isRecharge.value ? validAmount.value : isResetCardCheckout.value ? resetCardBaseAmount.value : subPaymentAmount.value))
const railFeeAmount = computed(() => (isRecharge.value ? feeAmount.value : isResetCardCheckout.value ? resetCardFeeAmount.value : subFeeAmount.value))
const railTotalAmount = computed(() => (isRecharge.value ? totalAmount.value : isResetCardCheckout.value ? resetCardTotalAmount.value : subTotalAmount.value))

const railBaseCanSubmit = computed(() => (isRecharge.value ? canSubmit.value : isResetCardCheckout.value ? canSubmitResetCard.value : canSubmitSubscription.value))

function formatRailPaymentAmount(value: number | string): string {
  return formatPaymentAmount(Number(value), railCurrency.value, localeCode.value)
}

function formatResetCardExpiry(value: string): string {
  const expiresAt = Date.parse(value)
  if (!Number.isFinite(expiresAt)) return value
  return new Intl.DateTimeFormat(localeCode.value, { dateStyle: 'medium' }).format(new Date(expiresAt))
}

function normalizeCouponCode(value: string): string {
  return value.trim().toUpperCase()
}

function createCouponQuoteContext(input: Omit<CouponQuoteContext, 'key' | 'couponCode'> & { couponCode: string }): CouponQuoteContext {
  const coupon = normalizeCouponCode(input.couponCode)
  const paymentType = normalizeVisibleMethod(input.paymentType) || input.paymentType.trim()
  const context = {
    amount: input.amount,
    paymentType,
    orderType: input.orderType,
    planId: input.planId,
    subscriptionId: input.subscriptionId,
    resetCardTierRevision: String(input.resetCardTierRevision || '').trim() || undefined,
    couponCode: coupon,
  }
  return {
    ...context,
    key: JSON.stringify(context),
  }
}

function selectedCouponQuoteContext(): CouponQuoteContext | null {
  if (isResetCardCheckout.value) return null
  const coupon = normalizeCouponCode(couponCode.value)
  const orderAmount = isRecharge.value ? validAmount.value : selectedPlan.value?.price ?? 0
  if (!coupon || !railBaseCanSubmit.value || !selectedMethod.value || orderAmount <= 0) return null
  return createCouponQuoteContext({
    couponCode: coupon,
    amount: orderAmount,
    paymentType: selectedMethod.value,
    orderType: isRecharge.value ? 'balance' : 'subscription',
    planId: isRecharge.value ? undefined : selectedPlan.value?.id,
  })
}

function couponForContext(context: CouponQuoteContext | null): AppliedCoupon | null {
  if (!context || !appliedCoupon.value || appliedCoupon.value.context.key !== context.key) return null
  return appliedCoupon.value
}

const selectedCoupon = computed(() => couponForContext(selectedCouponQuoteContext()))
const couponReady = computed(() => {
  const code = normalizeCouponCode(couponCode.value)
  return !code || selectedCoupon.value !== null
})
const couponStatus = computed(() => {
  if (couponError.value) return couponError.value
  if (couponQuoting.value) return t('payment.coupon.quoting')
  if (selectedCoupon.value) return t('payment.coupon.appliedCode', { code: selectedCoupon.value.quote.code })
  return ''
})
const railDisplayTotalAmount = computed<number | string>(() => selectedCoupon.value?.quote.pay_amount ?? railTotalAmount.value)

function invalidateCouponQuote(): void {
  couponQuoteRequest += 1
  couponQuoting.value = false
  couponError.value = ''
  appliedCoupon.value = null
}

async function quoteCouponContext(context: CouponQuoteContext, requireSelectedContext: boolean): Promise<AppliedCoupon | null> {
  const request = ++couponQuoteRequest
  couponQuoting.value = true
  couponError.value = ''
  appliedCoupon.value = null
  const payload: PaymentCouponQuoteRequest = {
    coupon_code: context.couponCode,
    amount: context.amount,
    payment_type: context.paymentType,
    order_type: context.orderType,
    ...(context.planId ? { plan_id: context.planId } : {}),
    ...(context.subscriptionId ? { subscription_id: context.subscriptionId } : {}),
    ...(context.resetCardTierRevision ? { reset_card_tier_revision: context.resetCardTierRevision } : {}),
  }

  try {
    const response = await paymentAPI.getCouponQuote(payload)
    const quote = response.data
    if (
      request !== couponQuoteRequest
      || normalizeCouponCode(couponCode.value) !== context.couponCode
      || (requireSelectedContext && selectedCouponQuoteContext()?.key !== context.key)
    ) {
      return null
    }
    if (!quote || !quote.revision || !quote.code || !quote.pay_amount) throw new Error('invalid coupon quote')
    const applied = {
      context,
      quote,
      idempotencyKey: createIdempotencyKey('payment-coupon'),
    }
    appliedCoupon.value = applied
    return applied
  } catch {
    if (
      request !== couponQuoteRequest
      || normalizeCouponCode(couponCode.value) !== context.couponCode
      || (requireSelectedContext && selectedCouponQuoteContext()?.key !== context.key)
    ) {
      return null
    }
    couponError.value = t('payment.coupon.invalid')
  } finally {
    if (request === couponQuoteRequest) couponQuoting.value = false
  }
  return null
}

async function applyCoupon(): Promise<void> {
  // Input and selection watchers invalidate a prior quote. Let that queued
  // invalidation finish before this new request gets its response.
  await nextTick()
  const context = selectedCouponQuoteContext()
  if (!context) {
    couponError.value = normalizeCouponCode(couponCode.value)
      ? t('payment.coupon.selectOrderFirst')
      : t('payment.coupon.enterCode')
    return
  }
  await quoteCouponContext(context, true)
}

function removeCoupon(): void {
  couponCode.value = ''
  invalidateCouponQuote()
}

watch(couponCode, () => invalidateCouponQuote(), { flush: 'sync' })
watch(
  () => [activeTab.value, validAmount.value, selectedMethod.value, selectedPlan.value?.id, selectedPlan.value?.price, selectedResetCard.value?.subscription.id, resetCardQuantity.value, resetCardUseOnPurchase.value] as const,
  () => invalidateCouponQuote(),
  { flush: 'sync' },
)

const railCreditLine = computed(() => {
  if (!isRecharge.value || validAmount.value <= 0) return ''
  return formatCreditAmount(creditedAmount.value)
})

// The rail is where a blocked purchase has to explain itself; the alternative
// is a disabled button with no reason attached.
const railNotice = computed(() => {
  if (isResetCardCheckout.value) {
    return canSubmitResetCard.value ? '' : t('payment.resetShop.paymentUnavailable')
  }
  const eligibility = isRecharge.value ? selectedRechargeOption.value?.eligibility : selectedPlan.value?.eligibility
  if (eligibility?.can_purchase === false) return t('payment.eligibility.minimum', { required: eligibility.required_total_recharge || 0, current: eligibility.current_total_recharge || 0 })
  if (isRecharge.value) {
    if (validAmount.value <= 0) return t('payment.selectTierFirst')
    return amountError.value
  }
  if (!selectedPlan.value) return t('payment.selectPlanFirst')
  return ''
})

const railCanSubmit = computed(() => railBaseCanSubmit.value && couponReady.value && !couponQuoting.value)

const railActionLabel = computed(() => {
  const label = isResetCardCheckout.value ? t('payment.resetShop.checkout') : t('payment.createOrder')
  if (Number(railDisplayTotalAmount.value) <= 0) return label
  return `${label} ${formatRailPaymentAmount(railDisplayTotalAmount.value)}`
})

function handleRailSubmit() {
  if (isRecharge.value) {
    void handleSubmitRecharge()
    return
  }
  if (isResetCardCheckout.value) {
    void submitResetCardCheckout()
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
  clearResetCardSelection()
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

  // Native Alipay QR responses close this popup after the launch decision. A
  // page-pay response must use the same user-gesture popup at top level; it
  // cannot be rendered safely in an iframe.
  return visibleMethod === 'alipay'
}

async function handleSubmitRecharge() {
  if (!railCanSubmit.value || submitting.value) return
  await createOrder(validAmount.value, 'balance', undefined, {
    coupon: selectedCoupon.value ?? undefined,
    preopenHostedPopup: true,
  })
}

async function confirmSubscribe() {
  if (!selectedPlan.value || !railCanSubmit.value || submitting.value) return
  await createOrder(selectedPlan.value.price, 'subscription', selectedPlan.value.id, {
    coupon: selectedCoupon.value ?? undefined,
    preopenHostedPopup: true,
  })
}

function discardUnboundResetCardAttempt(): void {
  if (typeof window === 'undefined') return
  discardResetCardCheckoutAttemptForSelectionChange(window.localStorage)
}

function clearResetCardSelection(): void {
  const hadSelection = selectedResetCard.value !== null
  if (hadSelection) discardUnboundResetCardAttempt()
  selectedResetCard.value = null
  resetCardQuantity.value = 1
  resetCardUseOnPurchase.value = false
  if (hadSelection) removeCoupon()
}

function applyResetCardSelection(next: ResetCardSelection): void {
  if (!isSameResetCardSelection(next)) discardUnboundResetCardAttempt()
  selectedResetCard.value = next
  selectedPlan.value = null
  resetCardQuantity.value = 1
  resetCardUseOnPurchase.value = false
  removeCoupon()
  errorMessage.value = ''
  errorHintMessage.value = ''
  const paymentType = resetCardPaymentMethodForQuote(next.quote, resetCardQuantity.value)
  if (paymentType) selectedMethod.value = paymentType
}

function isSameResetCardSelection(next: ResetCardSelection): boolean {
  const current = selectedResetCard.value
  return current?.subscription.id === next.subscription.id
    && current.quote.plan_id === next.quote.plan_id
    && current.quote.price === next.quote.price
    && (current.quote.validity_days ? current.quote.validity_days === next.quote.validity_days : current.quote.expires_at === next.quote.expires_at)
    && current.quote.reset_card_tier_revision === next.quote.reset_card_tier_revision
}

function selectResetCard(next: ResetCardSelection): void {
  if (submitting.value) return
  if (paymentPhase.value === 'paying' && paymentState.value.orderId > 0) {
    pendingResetCardSelection.value = next
    void openExistingOrderPrompt(paymentState.value.orderId)
    return
  }
  applyResetCardSelection(next)
}

function formatExistingOrderAmount(order: PaymentOrder): string {
  return formatPaymentAmount(order.pay_amount, order.currency || selectedCurrency.value, localeCode.value)
}

function formatExistingOrderDate(value: string): string {
  return formatDateTimeToMinute(value, localeCode.value)
}

function existingOrderTypeLabel(order: PaymentOrder): string {
  if (order.order_type === 'balance') return t('payment.admin.balanceOrder')
  if (order.order_type === 'subscription') return t('payment.admin.subscriptionOrder')
  return t('payment.orderOps.resetCards')
}

function existingOrderProductName(order: PaymentOrder): string {
  return purchaseName(order) || existingOrderTypeLabel(order)
}

function existingOrderPaymentMethodLabel(order: PaymentOrder): string {
  const visibleMethod = normalizeVisibleMethod(order.payment_type) || order.payment_type
  if (visibleMethod === 'alipay') return t('payment.methods.alipay')
  if (visibleMethod === 'wxpay') return t('payment.methods.wxpay')
  if (visibleMethod === 'stripe') return t('payment.methods.stripe')
  if (visibleMethod === 'airwallex') return t('payment.methods.airwallex')
  return order.payment_type
}

function resetExistingOrderPromptState(): void {
  existingOrder.value = null
  existingOrderLoading.value = false
  existingOrderLoadError.value = ''
  existingOrderActionError.value = ''
  existingOrderCancellationRetryNeeded.value = false
  existingOrderRequestedId.value = null
}

function closeExistingOrderPrompt(): void {
  if (existingOrderActionBusy.value) return
  existingOrderPromptRequest += 1
  existingOrderPromptVisible.value = false
  cancelExistingOrderConfirmVisible.value = false
  pendingResetCardSelection.value = null
  resetExistingOrderPromptState()
}

async function openExistingOrderPrompt(orderId?: number): Promise<void> {
  const request = ++existingOrderPromptRequest
  const requestedId = Number.isSafeInteger(orderId) && Number(orderId) > 0 ? Number(orderId) : null
  existingOrderRequestedId.value = requestedId
  existingOrderPromptVisible.value = true
  existingOrderLoading.value = true
  existingOrderLoadError.value = ''
  existingOrderActionError.value = ''
  existingOrder.value = null

  let order: PaymentOrder | null = null
  if (requestedId) {
    try {
      const response = await paymentAPI.getOrder(requestedId)
      if (response.data?.id === requestedId) order = response.data
    } catch {
      // Legacy servers may omit an ID in TOO_MANY_PENDING. Fall back to the
      // authenticated pending-order list before presenting an error.
    }
  }

  if (!order) {
    try {
      const response = await paymentAPI.getMyOrders({ page: 1, page_size: 50, status: 'PENDING' })
      const candidates = (response.data.items || []).filter(item => item.status === 'PENDING')
      order = requestedId
        ? candidates.find(item => item.id === requestedId) || null
        : [...candidates].sort((left, right) => Date.parse(right.created_at) - Date.parse(left.created_at))[0] || null
    } catch {
      // The dialog owns this failure so TOO_MANY_PENDING never falls through
      // to the generic checkout toast layer.
    }
  }

  if (request !== existingOrderPromptRequest || !existingOrderPromptVisible.value) return
  existingOrderLoading.value = false
  if (!order) {
    existingOrderLoadError.value = t('payment.errors.pendingOrderExists')
    return
  }
  existingOrder.value = order
  existingOrderCancellationRetryNeeded.value = typeof window !== 'undefined'
    && readQueuedPaymentCancellationIds(window.localStorage).includes(order.id)
  if (existingOrderCancellationRetryNeeded.value) {
    existingOrderActionError.value = t('payment.orderOps.cancelFailedRetry')
  }
}

function retryExistingOrderPrompt(): void {
  if (existingOrderActionBusy.value) return
  void openExistingOrderPrompt(existingOrderRequestedId.value ?? undefined)
}

function askCancelExistingOrder(): void {
  if (!existingOrder.value || existingOrder.value.status !== 'PENDING' || existingOrderActionBusy.value) return
  existingOrderActionError.value = ''
  cancelExistingOrderConfirmVisible.value = true
}

function restoreExistingOrderCancelFocus(): void {
  void nextTick(() => existingOrderCancelButton.value?.focus())
}

function closeExistingOrderCancelConfirmation(): void {
  cancelExistingOrderConfirmVisible.value = false
  restoreExistingOrderCancelFocus()
}

function retryCancelExistingOrder(): void {
  if (!existingOrderCancellationRetryNeeded.value || existingOrderActionBusy.value) return
  void confirmCancelExistingOrder()
}

function clearExistingOrderDialogAfterAction(): void {
  existingOrderPromptRequest += 1
  existingOrderPromptVisible.value = false
  cancelExistingOrderConfirmVisible.value = false
  resetExistingOrderPromptState()
}

function existingOrderLaunchRoutes(result: CreateOrderResult, visibleMethod: string) {
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
  return { stripeRouteUrl, airwallexRouteUrl }
}

async function openExistingOrder(): Promise<void> {
  const order = existingOrder.value
  if (!order || order.status !== 'PENDING' || existingOrderActionBusy.value || existingOrderCancellationRetryNeeded.value) return
  const promptRequest = existingOrderPromptRequest
  const actionRequest = ++existingOrderActionRequest
  existingOrderContinueLoading.value = true
  existingOrderActionError.value = ''
  let popup: Window | null = null
  let popupNavigated = false
  if (typeof window !== 'undefined' && !isMobileDevice()) {
    try {
      popup = window.open('', 'paymentPopup', getPaymentPopupFeatures())
    } catch {
      popup = null
    }
  }
  const closePopup = () => {
    if (popup && !popup.closed && !popupNavigated && typeof popup.close === 'function') popup.close()
  }
  const navigatePopup = (url: string) => {
    if (popup && !popup.closed) {
      try {
        popup.location.href = url
        popupNavigated = true
        return
      } catch {
        // Browser popup ownership can change after the authenticated response.
      }
    }
    const opened = window.open(url, 'paymentPopup', getPaymentPopupFeatures())
    if (!opened || opened.closed) window.location.href = url
  }

  try {
    const response = await paymentAPI.resumeOrder(order.id)
    const result = response.data
    if (result.order_id !== order.id) throw new Error('Invalid existing order resume response')
    if (promptRequest !== existingOrderPromptRequest || existingOrder.value?.id !== order.id) return
    const visibleMethod = normalizeVisibleMethod(result.payment_type || order.payment_type) || result.payment_type || order.payment_type
    const { stripeRouteUrl, airwallexRouteUrl } = existingOrderLaunchRoutes(result, visibleMethod)
    const decision = decidePaymentLaunch(result, {
      visibleMethod,
      orderType: order.order_type,
      isMobile: isMobileDevice(),
      isWechatBrowser: /MicroMessenger/i.test(window.navigator.userAgent),
      forceQRCode: !!(checkout.value.alipay_force_qrcode && visibleMethod === 'alipay'),
      mobilePrecreateDeepLink: checkout.value.alipay_mobile_precreate_deep_link === true,
      stripePopupUrl: stripeRouteUrl,
      stripeRouteUrl,
      airwallexRouteUrl,
      paymentDiscount: result.payment_discount,
    })
    if (decision.kind === 'unhandled' || !decision.paymentState.orderId) {
      throw new Error('Invalid existing order launch')
    }
    if (decision.kind === 'wechat_oauth' && decision.oauth?.authorize_url) {
      persistRecoverySnapshot(decision.recovery)
      clearExistingOrderDialogAfterAction()
      window.location.href = buildWechatOAuthAuthorizeUrl(decision.oauth.authorize_url, {
        paymentType: visibleMethod,
        orderType: order.order_type,
        orderAmount: result.amount,
      })
      return
    }

    setRecoveredPaymentState(decision.paymentState, {
      wechatJsapi: decision.kind === 'wechat_jsapi' ? decision.jsapi : undefined,
    })
    persistRecoverySnapshot(decision.recovery)
    pendingResetCardSelection.value = null
    clearExistingOrderDialogAfterAction()

    if (decision.kind === 'redirect_waiting' || decision.kind === 'stripe_popup') {
      if (decision.paymentState.payUrl) navigatePopup(decision.paymentState.payUrl)
      return
    }
    if (decision.kind === 'stripe_route' || decision.kind === 'airwallex_route') {
      if (decision.paymentState.payUrl) window.location.href = decision.paymentState.payUrl
      return
    }
  } catch (err: unknown) {
    if (promptRequest === existingOrderPromptRequest && existingOrder.value?.id === order.id) {
      existingOrderActionError.value = extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))
    }
  } finally {
    closePopup()
    if (actionRequest === existingOrderActionRequest) existingOrderContinueLoading.value = false
  }
}

async function confirmCancelExistingOrder(): Promise<void> {
  const order = existingOrder.value
  if (!order || order.status !== 'PENDING' || existingOrderActionBusy.value) return
  const orderId = order.id
  const promptRequest = existingOrderPromptRequest
  const actionRequest = ++existingOrderActionRequest
  cancelExistingOrderConfirmVisible.value = false
  existingOrderCancelLoading.value = true
  existingOrderActionError.value = ''
  if (typeof window !== 'undefined') queuePaymentCancellation(window.localStorage, orderId)
  try {
    const response = await paymentAPI.cancelOrder(orderId)
    const message = response.data?.message
    if (message !== 'cancelled' && message !== 'already_paid') {
      throw { reason: 'CANCEL_RESPONSE_INVALID' }
    }
    if (typeof window !== 'undefined') clearQueuedPaymentCancellation(window.localStorage, orderId)

    if (message === 'cancelled') {
      clearPaymentRecoverySnapshot(window.localStorage, PAYMENT_RECOVERY_STORAGE_KEY, { orderId })
      clearResetCardCheckoutAttempt(window.localStorage, { orderId })
      if (promptRequest !== existingOrderPromptRequest || existingOrder.value?.id !== orderId) return
      const currentMatches = paymentState.value.orderId === orderId
      const nextResetCardSelection = currentMatches ? pendingResetCardSelection.value : null
      const wasResetCard = currentMatches && paymentState.value.orderType === 'reset_card'
      if (currentMatches) resetPayment()
      pendingResetCardSelection.value = null
      if (wasResetCard) clearResetCardSelection()
      if (nextResetCardSelection) applyResetCardSelection(nextResetCardSelection)
      clearExistingOrderDialogAfterAction()
      return
    }

    if (promptRequest !== existingOrderPromptRequest || existingOrder.value?.id !== orderId) return
    if (paymentState.value.orderId > 0 && paymentState.value.orderId !== orderId) {
      existingOrderActionError.value = t('payment.result.paymentReceivedProcessing')
      return
    }
    const current = paymentState.value.orderId === orderId
      ? paymentState.value
      : recoverySnapshotForExistingOrder(order)
    const safeSnapshot = {
      ...snapshotWithoutLaunchMaterial(current),
      cancellationRequested: undefined,
    }
    pendingResetCardSelection.value = null
    setRecoveredPaymentState(safeSnapshot, { pendingState: 'confirmation' })
    persistRecoverySnapshot(safeSnapshot)
    clearExistingOrderDialogAfterAction()
  } catch (err: unknown) {
    if (promptRequest === existingOrderPromptRequest && existingOrder.value?.id === orderId) {
      existingOrderCancellationRetryNeeded.value = true
      existingOrderActionError.value = extractMappedI18nErrorMessage(
        err,
        t,
        'payment.errors',
        t('payment.orderOps.cancelFailedRetry'),
      )
    }
  } finally {
    if (actionRequest === existingOrderActionRequest) {
      existingOrderCancelLoading.value = false
      if (existingOrderCancellationRetryNeeded.value) restoreExistingOrderCancelFocus()
    }
  }
}

function updateResetCardOptions(next: { quantity: number; useOnPurchase: boolean }): void {
  if (!selectedResetCard.value || submitting.value || paymentPhase.value === 'paying') return
  const quantity = normalizeResetCardQuantity(next.quantity)
  const useOnPurchase = next.useOnPurchase === true
  if (quantity === resetCardQuantity.value && useOnPurchase === resetCardUseOnPurchase.value) return
  discardUnboundResetCardAttempt()
  resetCardQuantity.value = quantity
  resetCardUseOnPurchase.value = useOnPurchase
  const paymentType = resetCardPaymentMethodForQuote(selectedResetCard.value.quote, quantity)
  if (paymentType) selectedMethod.value = paymentType
}

async function submitResetCardCheckout() {
  const selection = selectedResetCard.value
  if (!selection || !railCanSubmit.value || submitting.value) return
  const userId = user.value?.id
  if (!Number.isSafeInteger(userId) || !userId || typeof window === 'undefined') {
    errorMessage.value = t('payment.result.failed')
    return
  }
  const quantity = normalizeResetCardQuantity(resetCardQuantity.value)
  const paymentType = resetCardPaymentMethodForQuote(selection.quote, quantity)
  if (!paymentType) {
    errorMessage.value = t('payment.resetShop.paymentUnavailable')
    errorHintMessage.value = ''
    return
  }
  selectedMethod.value = paymentType
  const attempt = getOrCreateResetCardCheckoutAttempt(window.localStorage, {
    userId,
    subscriptionId: selection.subscription.id,
    groupId: selection.quote.group_id,
    planId: selection.quote.plan_id,
    amount: resetCardBaseAmountForCurrency(selection.quote, quantity, 'CNY'),
    monthlyPrice: selection.quote.monthly_price,
    expiresAt: selection.quote.expires_at,
    validityDays: selection.quote.validity_days,
    paymentType,
    tierRevision: selection.quote.reset_card_tier_revision,
    quantity,
    useOnPurchase: resetCardUseOnPurchase.value,
  }, () => createIdempotencyKey('reset-card-payment'))
  await createOrder(resetCardBaseAmountForCurrency(selection.quote, quantity, 'CNY'), 'reset_card', selection.quote.plan_id, {
    subscriptionId: selection.subscription.id,
    resetCardTierRevision: selection.quote.reset_card_tier_revision,
    resetCardQuantity: quantity,
    resetCardUseOnPurchase: resetCardUseOnPurchase.value,
    paymentType,
    idempotencyKey: attempt.idempotencyKey,
    resetCardAttempt: attempt,
    preopenHostedPopup: true,
  })
}

async function createOrder(orderAmount: number, orderType: OrderType, planId?: number, options: CreateOrderOptions = {}) {
  if (!options.isResume && paymentPhase.value === 'paying' && paymentState.value.orderId > 0) {
    paymentModalVisible.value = true
    return
  }
  if (orderType !== 'reset_card' && !options.isResume && normalizeCouponCode(couponCode.value) && !options.coupon) {
    couponError.value = t('payment.coupon.reapply')
    return
  }
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
      resetCardQuantity: options.resetCardQuantity,
      resetCardUseOnPurchase: options.resetCardUseOnPurchase,
    })
    if (options.openid) {
      payload.openid = options.openid
    }
    if (options.wechatResumeToken) {
      payload.wechat_resume_token = options.wechatResumeToken
    }
    if (options.coupon && !options.isResume && !options.wechatResumeToken) {
      payload.coupon_code = options.coupon.quote.code
      payload.coupon_revision = options.coupon.quote.revision
    }

    const result = await paymentStore.createOrder(
      payload,
      options.idempotencyKey || options.coupon?.idempotencyKey
        ? { headers: { 'Idempotency-Key': options.idempotencyKey || options.coupon?.idempotencyKey } }
        : undefined,
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
      paymentDiscount: result.payment_discount ?? options.paymentDiscountDisplay ?? options.coupon?.quote,
      resetCardQuantity: options.resetCardQuantity,
      resetCardUseOnPurchase: options.resetCardUseOnPurchase,
    })

    if (decision.kind === 'wechat_oauth' && decision.oauth?.authorize_url) {
      // The signed token authenticates the next request. OAuth may precede
      // order creation (orderId=0); after resume the server response supplies
      // the persisted discount snapshot for confirmation display.
      persistRecoverySnapshot(decision.recovery)
      window.location.href = buildWechatOAuthAuthorizeUrl(decision.oauth.authorize_url, {
        paymentType: visibleMethod,
        orderType,
        planId,
        subscriptionId: options.subscriptionId,
        resetCardTierRevision: options.resetCardTierRevision,
        resetCardQuantity: options.resetCardQuantity,
        resetCardUseOnPurchase: options.resetCardUseOnPurchase,
        orderAmount,
      })
      return
    }

    if (decision.kind === 'unhandled') {
      closePreopenedPopup()
      applyScenarioError({ reason: 'UNHANDLED_PAYMENT_SCENARIO' }, visibleMethod)
      return
    }

    recoveredWechatJsapi.value = undefined
    recoveryPendingState.value = null
    paymentState.value = decision.paymentState
    paymentPhase.value = 'paying'
    persistRecoverySnapshot(decision.recovery)
    paymentModalVisible.value = true

    if (decision.kind === 'qr_waiting' || decision.kind === 'status_waiting') {
      closePreopenedPopup()
    }

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
              resetCardQuantity: options.resetCardQuantity,
              resetCardUseOnPurchase: options.resetCardUseOnPurchase,
              wechatResumeToken: options.wechatResumeToken,
              idempotencyKey: options.idempotencyKey,
              resetCardAttempt: options.resetCardAttempt,
              coupon: options.coupon,
              paymentDiscountDisplay: options.paymentDiscountDisplay,
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
          resetCardQuantity: options.resetCardQuantity,
          resetCardUseOnPurchase: options.resetCardUseOnPurchase,
          wechatResumeToken: options.wechatResumeToken,
          idempotencyKey: options.idempotencyKey,
          resetCardAttempt: options.resetCardAttempt,
          coupon: options.coupon,
          paymentDiscountDisplay: options.paymentDiscountDisplay,
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
      if (visibleMethod === 'alipay') {
        openWindow(decision.paymentState.payUrl)
        return
      }
      openWindow(decision.paymentState.payUrl)
    }
  } catch (err: unknown) {
    const apiErr = err as Record<string, unknown>
    if (['COUPON_QUOTE_CHANGED', 'COUPON_QUOTE_REQUIRED', 'COUPON_INVALID'].includes(String(apiErr.reason))) {
      invalidateCouponQuote()
      couponError.value = t(apiErr.reason === 'COUPON_INVALID' ? 'payment.coupon.invalid' : 'payment.coupon.reapply')
      errorMessage.value = couponError.value
      errorHintMessage.value = ''
    } else if (apiErr.reason === 'TOO_MANY_PENDING') {
      const metadata = apiErr.metadata as Record<string, unknown> | undefined
      const existingOrderId = Number(metadata?.order_id)
      errorMessage.value = ''
      errorHintMessage.value = ''
      void openExistingOrderPrompt(existingOrderId > 0 ? existingOrderId : undefined)
      return
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
      resetCardQuantity: options.resetCardQuantity,
      resetCardUseOnPurchase: options.resetCardUseOnPurchase,
      wechatResumeToken: options.wechatResumeToken,
      idempotencyKey: options.idempotencyKey,
      resetCardAttempt: options.resetCardAttempt,
      coupon: options.coupon,
      paymentDiscountDisplay: options.paymentDiscountDisplay,
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
  resetCardQuantity?: number
  resetCardUseOnPurchase?: boolean
  wechatResumeToken?: string
  idempotencyKey?: string
  resetCardAttempt?: ResetCardCheckoutAttempt
  coupon?: AppliedCoupon
  paymentDiscountDisplay?: PaymentDiscountSnapshot
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
    // Do not create a second Alipay order to switch presentation modes. A
    // failed first request must be resumed or reported to the user; only the
    // existing WeChat mobile fallback has an explicit same-order resume path.
    return false
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
      resetCardQuantity: context.resetCardQuantity,
      resetCardUseOnPurchase: context.resetCardUseOnPurchase,
      origin: typeof window !== 'undefined' ? window.location.origin : '',
      isMobile: false,
      isWechatBrowser: false,
    })
    if (context.wechatResumeToken) {
      payload.wechat_resume_token = context.wechatResumeToken
    }
    if (context.coupon && !context.wechatResumeToken) {
      payload.coupon_code = context.coupon.quote.code
      payload.coupon_revision = context.coupon.quote.revision
    }
    const result = await paymentStore.createOrder(
      payload,
      context.idempotencyKey || context.coupon?.idempotencyKey
        ? { headers: { 'Idempotency-Key': context.idempotencyKey || context.coupon?.idempotencyKey } }
        : undefined,
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
      paymentDiscount: result.payment_discount ?? context.paymentDiscountDisplay ?? context.coupon?.quote,
      resetCardQuantity: context.resetCardQuantity,
      resetCardUseOnPurchase: context.resetCardUseOnPurchase,
    })

    if (decision.kind !== 'qr_waiting' || !decision.paymentState.qrCode) {
      return false
    }

    errorMessage.value = ''
    errorHintMessage.value = ''
    recoveredWechatJsapi.value = undefined
    recoveryPendingState.value = null
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
  const resumePaymentDiscount = resume.wechatResumeToken
    && paymentState.value.resumeToken === resume.wechatResumeToken
    ? paymentState.value.paymentDiscount
    : undefined

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
      resetCardQuantity: resume.resetCardQuantity,
      resetCardUseOnPurchase: resume.resetCardUseOnPurchase,
      // The signed token matched this local attempt by hash. Retain the raw
      // key only in request headers so an H5/JSAPI failure and its QR retry
      // replay the same server-side reset-card checkout.
      idempotencyKey: resetCardAttempt?.idempotencyKey,
      resetCardAttempt: resetCardAttempt || undefined,
      paymentDiscountDisplay: resumePaymentDiscount,
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
      resetCardQuantity: resume.resetCardQuantity,
      resetCardUseOnPurchase: resume.resetCardUseOnPurchase,
    })
  }
}

let checkoutRefreshPending = false
let checkoutDisposed = false
const checkoutIsPaying = () => paymentPhase.value === 'paying'

// Checkout data is an ephemeral view of the linked groups, not an entitlement
// snapshot. Refetch when returning from group editing without losing selection.
async function refreshCheckoutCatalog() {
  if (loading.value || submitting.value || paymentPhase.value === 'paying' || checkoutRefreshPending || checkoutDisposed) return
  checkoutRefreshPending = true
  try {
    const response = await paymentAPI.getCheckoutInfo()
    if (checkoutDisposed || submitting.value || checkoutIsPaying()) return
    const selectedID = selectedPlan.value?.id
    checkout.value = response.data
    selectedPlan.value = selectedID ? response.data.plans.find(plan => plan.id === selectedID) ?? null : null
    if (selectedPlan.value) selectedSubscriptionPeriod.value = subscriptionPeriodOf(selectedPlan.value)
    invalidateCouponQuote()
  } catch (err: unknown) {
    if (!checkoutDisposed) appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    checkoutRefreshPending = false
  }
}

function refreshVisibleCheckout() {
  if (document.visibilityState !== 'hidden') void refreshCheckoutCatalog()
}

watch(activeTab, (tab) => {
  if (tab === 'subscription') {
    void refreshCheckoutCatalog()
    return
  }
  clearResetCardSelection()
})

onBeforeUnmount(() => {
  checkoutDisposed = true
  window.removeEventListener('focus', refreshVisibleCheckout)
  document.removeEventListener('visibilitychange', refreshVisibleCheckout)
})

onMounted(async () => {
  window.addEventListener('focus', refreshVisibleCheckout)
  document.addEventListener('visibilitychange', refreshVisibleCheckout)
  flushQueuedPaymentCancellations()
  try {
    const res = await paymentAPI.getCheckoutInfo()
    checkout.value = res.data
    // Seed the initial choice; explicit deep links and payment recovery below win.
    const monthlyPlus = checkout.value.plans.find(plan =>
      subscriptionPeriodOf(plan) === 'month'
      && /\bplus\b/i.test(plan.name)
      && plan.eligibility?.can_purchase !== false,
    )
    if (monthlyPlus) {
      selectedSubscriptionPeriod.value = 'month'
      selectedPlan.value = monthlyPlus
    }
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
        if (hasWechatResumeQuery(route.query)) {
          // The signed OAuth callback owns this recovery path. Its next
          // create/resume request remains the authority for launch material.
          paymentState.value = restored
        } else {
          await resumeStoredPayment(restored)
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
      if (isResetCardPurchaseQuery()) {
        resetCardTargetSubscriptionId.value = resetCardSubscriptionIdFromQuery()
      } else {
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
    }
  } catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
  finally { loading.value = false }
  // Fetch active subscriptions (uses cache, non-blocking); skipped when the subscription feature is off
  if (subscriptionEnabled.value) {
    subscriptionStore.fetchActiveSubscriptions().then(() => {
      if (resetCardTargetSubscriptionId.value !== null) {
        void focusResetCardShop()
      }
    }).catch(() => {})
  }
})
</script>
