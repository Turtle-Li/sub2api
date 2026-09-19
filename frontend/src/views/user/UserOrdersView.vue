<template>
  <AppLayout>
    <div class="space-y-4">
      <!-- Filters -->
      <div class="card p-4">
        <div class="flex flex-wrap items-center gap-3">
          <Select :aria-label="t('payment.orders.status')" v-model="currentFilter" :options="statusFilters" class="w-36" @change="handleFilterChange" />
          <Select v-model="fulfillmentFilter" :options="fulfillmentOptions" :aria-label="t('payment.orderOps.fulfillmentFilterLabel')" class="w-44" @change="handleFilterChange" />
          <Select v-model="invoiceFilter" :options="invoiceOptions" :aria-label="t('payment.invoice.currentStatus')" class="w-44" @change="handleFilterChange" />
          <div class="flex flex-1 items-center justify-end gap-2">
            <button @click="fetchOrders" :disabled="loading" class="btn btn-secondary" :title="t('common.refresh')">
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button v-if="canShowPaymentEntry" class="btn btn-primary" @click="router.push('/purchase')">{{ t('payment.result.backToRecharge') }}</button>
          </div>
        </div>
      </div>

      <!-- Table -->
      <OrderTable :orders="orders" :loading="loading">
        <template #payment-status="{ row }">
          <div class="space-y-1">
            <OrderStatusBadge
              :data-test="`order-status-${row.id}`"
              :status="row.status"
              :cancellation-pending="row.cancellation_pending === true"
            />
            <OrderLifecycleBadge
              :data-test="`order-payment-fact-${row.id}`"
              kind="payment"
              :value="paymentFact(row)"
            />
          </div>
        </template>
        <template #actions="{ row }">
          <div class="flex flex-wrap items-center gap-2">
            <button type="button" class="btn btn-secondary btn-sm" @click="openDetails(row)">{{ t('common.view') }}</button>
            <template v-if="row.status === 'PENDING'">
              <span
                v-if="row.cancellation_pending"
                :data-test="`order-cancellation-pending-${row.id}`"
                class="inline-flex items-center gap-1.5 rounded-lg border border-amber-200 bg-amber-50 px-2.5 py-1.5 text-xs font-medium text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200"
                role="status"
              >
                <Icon name="sync" size="xs" class="animate-spin" />
                {{ t('payment.orderOps.cancellationPending') }}
              </span>
              <span
                v-else-if="isConfirmationPending(row)"
                :data-test="`order-confirmation-pending-${row.id}`"
                class="inline-flex items-center gap-1.5 rounded-lg border border-blue-200 bg-blue-50 px-2.5 py-1.5 text-xs font-medium text-blue-800 dark:border-blue-900/60 dark:bg-blue-950/30 dark:text-blue-200"
                role="status"
              >
                <Icon name="sync" size="xs" class="animate-spin" />
                {{ t('payment.orderOps.confirmationPending') }}
              </span>
              <span
                v-else-if="isPaymentDeadlineReached(row)"
                :data-test="`order-expiry-checking-${row.id}`"
                class="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 bg-gray-50 px-2.5 py-1.5 text-xs font-medium text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200"
                role="status"
              >
                <Icon name="sync" size="xs" class="animate-spin" />
                {{ t('payment.orderOps.paymentExpiryChecking') }}
              </span>
              <template v-else>
                <span
                  :data-test="`order-countdown-${row.id}`"
                  class="inline-flex items-center gap-1 rounded-lg bg-primary-50 px-2.5 py-1.5 text-xs font-semibold tabular-nums text-primary-700 dark:bg-primary-950/30 dark:text-primary-200"
                  :title="t('payment.orderOps.paymentExpiresIn', { time: paymentCountdown(row) })"
                >
                  <Icon name="clock" size="xs" />
                  {{ paymentCountdown(row) }}
                </span>
                <button
                  :data-test="`continue-payment-${row.id}`"
                  type="button"
                  class="btn btn-primary btn-sm"
                  :disabled="resumingOrderId === row.id"
                  @click="continuePayment(row)"
                >
                  <Icon name="creditCard" size="xs" />
                  {{ resumingOrderId === row.id ? t('common.processing') : t('payment.continuePayment') }}
                </button>
                <button
                  :data-test="`cancel-order-${row.id}`"
                  type="button"
                  class="btn btn-secondary btn-sm border-red-200 text-red-700 hover:border-red-300 hover:bg-red-50 dark:border-red-900/60 dark:text-red-300 dark:hover:border-red-800 dark:hover:bg-red-950/30"
                  :disabled="cancellingOrderId === row.id"
                  @click="handleCancel(row)"
                >
                  <Icon name="x" size="xs" />
                  {{ t('payment.orders.cancel') }}
                </button>
              </template>
            </template>
            <button v-if="canRequestRefund(row)" @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-purple-600 hover:bg-purple-50 dark:text-purple-400 dark:hover:bg-purple-900/20">
              <Icon name="dollar" size="sm" />
              <span>{{ t('payment.orders.requestRefund') }}</span>
            </button>
            <button v-if="canOpenInvoice(row)" @click="openInvoiceDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/20">
              <Icon :name="row.invoice?.status === 'ISSUED' ? 'mail' : 'document'" size="sm" />
              <span>{{ invoiceActionLabel(row) }}</span>
            </button>
          </div>
        </template>
      </OrderTable>

      <!-- Pagination -->
      <Pagination
        v-if="pagination.total > 0"
        :page="pagination.page"
        :total="pagination.total"
        :page-size="pagination.page_size"
        @update:page="handlePageChange"
        @update:pageSize="handlePageSizeChange"
      />
    </div>

    <BaseDialog :show="!!detailOrder" :title="t('payment.orderOps.detail')" @close="closeDetails">
      <div v-if="detailOrder" class="space-y-5">
        <section data-test="order-detail-purchase" class="space-y-3">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('payment.orderOps.purchaseInfo') }}</h3>
          <dl class="grid grid-cols-1 gap-3 border-b border-gray-100 pb-3 text-sm dark:border-dark-600 sm:grid-cols-2">
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</dt>
              <dd class="font-mono text-gray-900 dark:text-white">#{{ detailOrder.id }}</dd>
            </div>
            <div class="min-w-0 sm:col-span-2">
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</dt>
              <dd class="mt-1 flex min-w-0 items-start justify-between gap-2">
                <code data-test="detail-order-number" class="min-w-0 break-all font-mono text-xs text-gray-900 dark:text-white">{{ detailOrder.out_trade_no }}</code>
                <button
                  type="button"
                  class="inline-flex shrink-0 items-center gap-1 text-xs font-medium text-primary-700 hover:text-primary-800 dark:text-primary-300 dark:hover:text-primary-200"
                  @click="copyOrderNumber(detailOrder.out_trade_no)"
                >
                  <Icon name="copy" size="xs" />
                  {{ t('payment.orderOps.copyOrder') }}
                </button>
              </dd>
            </div>
          </dl>
          <OrderPurchaseSnapshot :order="detailOrder" :show-title="false" :show-financials="false" />
        </section>

        <section data-test="order-detail-financials" class="space-y-3 border-t border-gray-100 pt-4 dark:border-dark-600">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('payment.orderOps.amountAndDiscount') }}</h3>
          <dl class="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</dt>
              <dd class="font-semibold text-gray-900 dark:text-white">{{ formatOrderAmount(detailOrder.pay_amount, detailOrder.currency) }}</dd>
            </div>
            <div v-if="detailOrder.order_type === 'balance'">
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.creditedAmount') }}</dt>
              <dd class="font-semibold text-gray-900 dark:text-white">${{ detailOrder.amount.toFixed(2) }}</dd>
            </div>
            <div v-if="detailOrder.product_snapshot?.price != null">
              <dt class="text-gray-500 dark:text-gray-400">{{ t(detailOrder.order_type === 'reset_card' ? 'payment.orderOps.resetCardTotalPrice' : 'payment.orderOps.listPrice') }}</dt>
              <dd class="text-gray-900 dark:text-white">{{ formatOrderAmount(detailOrder.product_snapshot.price, detailOrder.product_snapshot.currency || (detailOrder.order_type === 'balance' ? detailOrder.currency : 'USD')) }}</dd>
            </div>
            <template v-if="detailPaymentDiscount">
              <div>
                <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.paymentCoupon') }}</dt>
                <dd><code class="font-mono text-gray-900 dark:text-white">{{ detailPaymentDiscount.code }}</code></dd>
              </div>
              <div>
                <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.originalPayment') }}</dt>
                <dd class="text-gray-900 dark:text-white">{{ formatOrderAmount(Number(detailPaymentDiscount.original_amount), detailPaymentDiscount.currency) }}</dd>
              </div>
              <div>
                <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.paymentDiscount') }}</dt>
                <dd class="text-emerald-700 dark:text-emerald-300">-{{ formatOrderAmount(Number(detailPaymentDiscount.discount_amount), detailPaymentDiscount.currency) }}</dd>
              </div>
              <div>
                <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.finalPayment') }}</dt>
                <dd class="font-semibold text-gray-900 dark:text-white">{{ formatOrderAmount(Number(detailPaymentDiscount.pay_amount), detailPaymentDiscount.currency) }}</dd>
              </div>
            </template>
          </dl>
        </section>

        <section data-test="order-detail-status" class="space-y-3 border-t border-gray-100 pt-4 dark:border-dark-600">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('payment.orderOps.statusAndFulfillment') }}</h3>
          <div class="flex flex-wrap gap-2">
            <OrderStatusBadge :status="detailOrder.status" :cancellation-pending="detailOrder.cancellation_pending === true" />
            <OrderLifecycleBadge kind="payment" :value="paymentFact(detailOrder)" />
            <OrderLifecycleBadge kind="fulfillment" :value="fulfillmentFact(detailOrder)" />
          </div>
          <p v-if="detailOrder.invoice" class="text-sm text-gray-600 dark:text-gray-300">
            {{ t('payment.invoice.currentStatus') }}: {{ t(`payment.invoice.status.${detailOrder.invoice.status.toLowerCase()}`) }}
          </p>
          <p v-if="detailOrder.needs_manual_review" class="text-sm text-amber-700 dark:text-amber-300">{{ t('payment.orderOps.reviewRequired') }}</p>
        </section>

        <section data-test="order-detail-timeline" class="space-y-3 border-t border-gray-100 pt-4 dark:border-dark-600">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('payment.orderOps.timeline') }}</h3>
          <dl class="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
            <div><dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.createdAt') }}</dt><dd class="text-gray-900 dark:text-white">{{ formatOrderDateTime(detailOrder.created_at) }}</dd></div>
            <div v-if="detailOrder.paid_at"><dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.paidAt') }}</dt><dd class="text-gray-900 dark:text-white">{{ formatOrderDateTime(detailOrder.paid_at) }}</dd></div>
            <div v-if="detailOrder.completed_at"><dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.fulfillment.fulfilled') }}</dt><dd class="text-gray-900 dark:text-white">{{ formatOrderDateTime(detailOrder.completed_at) }}</dd></div>
          </dl>
          <div v-if="canRequestRefund(detailOrder) || canOpenInvoice(detailOrder)" class="flex flex-wrap gap-2 border-t border-gray-100 pt-3 dark:border-dark-600">
            <button v-if="canRequestRefund(detailOrder)" type="button" class="btn btn-secondary btn-sm" @click="openRefundFromDetails(detailOrder)">{{ t('payment.orders.requestRefund') }}</button>
            <button v-if="canOpenInvoice(detailOrder)" type="button" class="btn btn-secondary btn-sm" @click="openInvoiceFromDetails(detailOrder)">{{ invoiceActionLabel(detailOrder) }}</button>
          </div>
        </section>
      </div>
    </BaseDialog>
    <!-- Cancel Confirm Dialog -->
    <BaseDialog
      :show="!!cancelTarget"
      :title="t('payment.orders.cancel')"
      width="narrow"
      :close-on-escape="cancellingOrderId === null"
      :close-on-click-outside="cancellingOrderId === null"
      :show-close-button="cancellingOrderId === null"
      @close="closeCancelDialog"
    >
      <div v-if="cancelTarget" class="space-y-4">
        <div class="flex items-start gap-3 rounded-lg border border-red-100 bg-red-50/70 p-3 dark:border-red-900/50 dark:bg-red-950/20">
          <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-red-100 text-red-600 dark:bg-red-900/40 dark:text-red-300">
            <Icon name="x" size="sm" />
          </span>
          <div class="min-w-0">
            <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('payment.confirmCancel') }}</p>
            <p class="mt-1 text-xs leading-5 text-gray-600 dark:text-gray-300">{{ t('payment.orderOps.cancelNotice') }}</p>
          </div>
        </div>
        <dl class="space-y-2 rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800">
          <div class="flex items-start justify-between gap-4">
            <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</dt>
            <dd class="max-w-[65%] break-all text-right font-mono text-xs text-gray-900 dark:text-white">{{ cancelTarget.out_trade_no }}</dd>
          </div>
          <div class="flex items-center justify-between gap-4">
            <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</dt>
            <dd class="font-semibold text-gray-900 dark:text-white">{{ currencySymbol(cancelTarget.currency) }}{{ cancelTarget.pay_amount.toFixed(2) }}</dd>
          </div>
        </dl>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button data-test="dismiss-cancel-order" class="btn btn-secondary" :disabled="!!cancellingOrderId" @click="closeCancelDialog">{{ t('payment.orderOps.keepOrder') }}</button>
          <button data-test="confirm-cancel-order" class="btn btn-danger" :disabled="!!cancellingOrderId" @click="confirmCancel">{{ cancellingOrderId ? t('common.processing') : t('payment.orders.cancel') }}</button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog
      :show="!!resumedPayment"
      :title="resumeDialogTitle"
      width="normal"
      mobile-sheet
      @close="closeResumedPayment"
    >
      <PaymentStatusPanel
        v-if="resumedPayment"
        :key="`${resumedPayment.orderId}-${resumedPayment.createdAt}`"
        :order-id="resumedPayment.orderId"
        :amount="resumedPayment.amount"
        :pay-amount="resumedPayment.payAmount"
        :qr-code="resumedPayment.qrCode"
        :expires-at="resumedPayment.expiresAt"
        :payment-type="resumedPayment.paymentType"
        :pay-url="resumedPayment.payUrl"
        :checkout-frame-url="resumedPayment.checkoutFrameUrl"
        :allow-checkout-frame="true"
        :order-type="resumedPayment.orderType"
        :currency="resumedPayment.currency"
        :out-trade-no="resumedPayment.outTradeNo"
        :mobile-alipay-deep-link="resumedPayment.alipayMobilePrecreateDeepLink"
        :payment-discount="resumedPayment.paymentDiscount"
        :wechat-jsapi="resumedWechatJsapi"
        @done="closeResumedPayment"
        @settled="onResumedPaymentSettled"
      />
    </BaseDialog>

    <!-- Refund Dialog -->
    <BaseDialog
      :show="!!refundTarget"
      :title="t('payment.orders.requestRefund')"
      :close-on-escape="!actionLoading"
      :close-on-click-outside="!actionLoading"
      :show-close-button="!actionLoading"
      @close="closeRefundDialog"
    >
      <div v-if="refundTarget" class="space-y-4">
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
          <div class="flex justify-between text-sm">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
            <span class="font-mono text-gray-900 dark:text-white">#{{ refundTarget.id }}</span>
          </div>
          <div class="mt-2 flex justify-between text-sm">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</span>
            <span class="text-gray-900 dark:text-white">{{ formatOrderAmount(refundTarget.pay_amount, refundTarget.currency) }}</span>
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('payment.refundReason') }}</label>
          <textarea v-model="refundReason" rows="3" class="input mt-1 w-full" :placeholder="t('payment.refundReasonPlaceholder')" />
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button class="btn btn-secondary" :disabled="actionLoading" @click="closeRefundDialog">{{ t('common.cancel') }}</button>
          <button class="btn btn-primary" :disabled="actionLoading || !refundReason.trim()" @click="confirmRefund">{{ actionLoading ? t('common.processing') : t('payment.orders.requestRefund') }}</button>
        </div>
      </template>
    </BaseDialog>

    <InvoiceRequestDialog
      :show="!!invoiceTarget"
      :order="invoiceTarget"
      :submitting="invoiceSubmitting"
      @close="closeInvoiceDialog"
      @submit="submitInvoiceRequest"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores'
import { paymentAPI } from '@/api/payment'
import { extractApiErrorCode, extractI18nErrorMessage } from '@/utils/apiError'
import type { CreateInvoiceRequest, PaymentOrder, WechatJSAPIPayload } from '@/types/payment'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import OrderTable from '@/components/payment/OrderTable.vue'
import InvoiceRequestDialog from '@/components/payment/InvoiceRequestDialog.vue'
import OrderPurchaseSnapshot from '@/components/payment/OrderPurchaseSnapshot.vue'
import OrderLifecycleBadge from '@/components/payment/OrderLifecycleBadge.vue'
import OrderStatusBadge from '@/components/payment/OrderStatusBadge.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'
import { fulfillmentFact, paymentFact } from '@/components/payment/orderPresentation'
import { decidePaymentLaunch, type PaymentRecoverySnapshot } from '@/components/payment/paymentFlow'
import { isPaymentEntryVisible } from '@/utils/featureFlags'
import { isMobileDevice } from '@/utils/device'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import { currencySymbol, formatPaymentAmount } from '@/components/payment/currency'

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()

// Recharge CTA stays gated on the loaded payment switch; the presentation-only
// entry switch additionally hides it while keeping this history page reachable.
const canShowPaymentEntry = computed(
  () => Boolean(appStore.cachedPublicSettings?.payment_enabled) && isPaymentEntryVisible(),
)

const loading = ref(false)
const actionLoading = ref(false)
const orders = ref<PaymentOrder[]>([])
const refundEligibleProviders = ref<Set<string>>(new Set())
const currentFilter = ref('')
const fulfillmentFilter = ref('')
const invoiceFilter = ref('')
const detailOrder = ref<PaymentOrder | null>(null)
const detailPaymentDiscount = computed(() => detailOrder.value?.product_snapshot?.payment_discount)
const fulfillmentOptions = computed(() => [
  { value: '', label: t('payment.orderOps.allFulfillments') },
  ...['PENDING', 'FAILED', 'FULFILLED', 'NOT_STARTED'].map(value => ({ value, label: t(`payment.orderOps.fulfillment.${value.toLowerCase()}`) })),
])
const invoiceOptions = computed(() => [
  { value: '', label: t('payment.invoice.admin.allStatuses') },
  { value: 'NONE', label: t('payment.invoice.admin.notRequested') },
  ...['PENDING', 'PROCESSING', 'ISSUED', 'REJECTED'].map(value => ({ value, label: t(`payment.invoice.status.${value.toLowerCase()}`) })),
])
function handleFilterChange() { pagination.page = 1; fetchOrders() }
let detailRequestSequence = 0
function closeDetails() { detailRequestSequence++; detailOrder.value = null }
async function openDetails(order: PaymentOrder) {
  const sequence = ++detailRequestSequence
  detailOrder.value = order
  try {
    const response = await paymentAPI.getOrder(order.id)
    if (sequence === detailRequestSequence && detailOrder.value?.id === order.id) detailOrder.value = response.data
  } catch {
    if (sequence === detailRequestSequence && detailOrder.value?.id === order.id) appStore.showError(t('common.error'))
  }
}
async function copyOrderNumber(value: string) {
  try { await navigator.clipboard.writeText(value); appStore.showSuccess(t('common.success')) }
  catch { appStore.showError(t('payment.orderOps.copyFailed')) }
}

function formatOrderAmount(amount: number, currency?: string): string {
  return formatPaymentAmount(amount, currency)
}
const cancelTarget = ref<PaymentOrder | null>(null)
const cancellingOrderId = ref<number | null>(null)
const resumingOrderId = ref<number | null>(null)
const resumedPayment = ref<PaymentRecoverySnapshot | null>(null)
const resumedWechatJsapi = ref<WechatJSAPIPayload | undefined>()
const now = ref(Date.now())
const confirmationPendingOrderIds = ref<Set<number>>(new Set())
const refundTarget = ref<PaymentOrder | null>(null)
const refundReason = ref('')
const invoiceTarget = ref<PaymentOrder | null>(null)
const invoiceSubmitting = ref(false)
const pagination = reactive({ page: 1, page_size: 20, total: 0 })

const statusFilters = computed(() => [
  { value: '', label: t('common.all') },
  { value: 'PENDING', label: t('payment.status.pending') },
  { value: 'COMPLETED', label: t('payment.status.completed') },
  { value: 'FAILED', label: t('payment.status.failed') },
  { value: 'REFUNDED', label: t('payment.status.refunded') },
])

let listRequest = 0
async function fetchOrders() {
  const request = ++listRequest
  loading.value = true
  try {
    const res = await paymentAPI.getMyOrders({
      page: pagination.page,
      page_size: pagination.page_size,
      status: currentFilter.value || undefined,
      fulfillment_status: fulfillmentFilter.value || undefined,
      invoice_status: invoiceFilter.value || undefined,
    })
    if (request !== listRequest) return
    orders.value = res.data.items || []
    pagination.total = res.data.total || 0
    refreshPaymentLifecycle()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    if (request === listRequest) loading.value = false
  }
}

function handlePageChange(page: number) { pagination.page = page; fetchOrders() }
function handlePageSizeChange(size: number) { pagination.page_size = size; pagination.page = 1; fetchOrders() }

function isConfirmationPending(order: PaymentOrder): boolean {
  return confirmationPendingOrderIds.value.has(order.id)
}

function setConfirmationPending(orderId: number, pending: boolean) {
  const next = new Set(confirmationPendingOrderIds.value)
  if (pending) next.add(orderId)
  else next.delete(orderId)
  confirmationPendingOrderIds.value = next
}

function paymentRemainingSeconds(order: PaymentOrder): number {
  const expiresAt = Date.parse(order.expires_at)
  if (!Number.isFinite(expiresAt)) return 0
  return Math.max(0, Math.floor((expiresAt - now.value) / 1000))
}

function paymentCountdown(order: PaymentOrder): string {
  const seconds = paymentRemainingSeconds(order)
  const minutes = Math.floor(seconds / 60)
  return `${String(minutes).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`
}

function isPaymentDeadlineReached(order: PaymentOrder): boolean {
  return paymentRemainingSeconds(order) <= 0
}

function canContinuePayment(order: PaymentOrder): boolean {
  return order.status === 'PENDING'
    && !order.cancellation_pending
    && !isConfirmationPending(order)
    && !isPaymentDeadlineReached(order)
}

function updateOrder(updated: PaymentOrder) {
  orders.value = orders.value.map(order => order.id === updated.id ? updated : order)
  if (detailOrder.value?.id === updated.id) detailOrder.value = updated
  if (updated.status !== 'PENDING') {
    paymentSyncStates.delete(updated.id)
    setConfirmationPending(updated.id, false)
  }
}

function markCancellationPending(orderId: number) {
  const current = orders.value.find(order => order.id === orderId)
  if (current) updateOrder({ ...current, cancellation_pending: true })
  setConfirmationPending(orderId, false)
}

function handleCancel(order: PaymentOrder) {
  if (cancellingOrderId.value || !canContinuePayment(order)) return
  cancelTarget.value = order
}

function closeCancelDialog() {
  if (!cancellingOrderId.value) cancelTarget.value = null
}

async function confirmCancel() {
  const target = cancelTarget.value
  if (!target || cancellingOrderId.value) return
  cancellingOrderId.value = target.id
  try {
    await paymentAPI.cancelOrder(target.id)
    appStore.showSuccess(t('payment.orderOps.cancelled'))
    cancelTarget.value = null
    await fetchOrders()
  } catch (err: unknown) {
    const code = extractApiErrorCode(err)
    if (code === 'PAYMENT_CANCELLATION_PENDING') {
      markCancellationPending(target.id)
      cancelTarget.value = null
      refreshPaymentLifecycle()
      void fetchOrders()
    } else if (code === 'PAYMENT_CONFIRMATION_PENDING') {
      setConfirmationPending(target.id, true)
      cancelTarget.value = null
      refreshPaymentLifecycle()
      void fetchOrders()
    } else {
      appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
    }
  } finally {
    if (cancellingOrderId.value === target.id) cancellingOrderId.value = null
  }
}

async function continuePayment(order: PaymentOrder) {
  if (!canContinuePayment(order) || resumingOrderId.value) return
  resumingOrderId.value = order.id
  try {
    const response = await paymentAPI.resumeOrder(order.id)
    const decision = decidePaymentLaunch(response.data, {
      visibleMethod: response.data.payment_type || order.payment_type,
      orderType: order.order_type,
      isMobile: isMobileDevice(),
      now: Date.now(),
    })
    if (decision.kind === 'unhandled' || !decision.paymentState.orderId) {
      throw new Error('invalid resumed payment order')
    }
    resumedWechatJsapi.value = decision.kind === 'wechat_jsapi' ? decision.jsapi : undefined
    resumedPayment.value = decision.paymentState
  } catch (err: unknown) {
    const code = extractApiErrorCode(err)
    if (code === 'PAYMENT_CANCELLATION_PENDING') {
      markCancellationPending(order.id)
      refreshPaymentLifecycle()
      void fetchOrders()
    } else if (code === 'PAYMENT_CONFIRMATION_PENDING') {
      setConfirmationPending(order.id, true)
      refreshPaymentLifecycle()
      void fetchOrders()
    } else {
      appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
    }
  } finally {
    if (resumingOrderId.value === order.id) resumingOrderId.value = null
  }
}

const resumeDialogTitle = computed(() => {
  if (resumedPayment.value?.paymentType === 'alipay') return t('payment.methods.alipay')
  if (resumedPayment.value?.paymentType === 'wxpay') return t('payment.methods.wxpay')
  return t('payment.checkoutTitle')
})

function closeResumedPayment() {
  resumedPayment.value = null
  resumedWechatJsapi.value = undefined
  void fetchOrders()
}

function onResumedPaymentSettled() {
  void fetchOrders()
}

const ORDER_STATUS_POLL_INTERVAL_MS = 3000
type PaymentSyncState = { inFlight: boolean; nextAttemptAt: number }
const paymentSyncStates = new Map<number, PaymentSyncState>()
let paymentLifecycleTimer: ReturnType<typeof setInterval> | null = null

function shouldSynchronizePaymentOrder(order: PaymentOrder): boolean {
  return order.status === 'PENDING'
    && (order.cancellation_pending === true || isConfirmationPending(order) || isPaymentDeadlineReached(order))
}

async function synchronizePaymentOrder(order: PaymentOrder, state: PaymentSyncState) {
  const deadlineReached = isPaymentDeadlineReached(order)
  try {
    const response = deadlineReached
      ? await paymentAPI.verifyOrder(order.out_trade_no)
      : await paymentAPI.getOrder(order.id)
    updateOrder(response.data)
    if (deadlineReached) void fetchOrders()
  } catch (err: unknown) {
    const code = extractApiErrorCode(err)
    if (code === 'PAYMENT_CANCELLATION_PENDING') {
      markCancellationPending(order.id)
    } else if (code === 'PAYMENT_CONFIRMATION_PENDING') {
      setConfirmationPending(order.id, true)
    }
  } finally {
    state.inFlight = false
  }
}

function refreshPaymentLifecycle() {
  now.value = Date.now()
  const activeOrderIds = new Set<number>()
  orders.value.forEach(order => {
    if (!shouldSynchronizePaymentOrder(order)) return
    activeOrderIds.add(order.id)
    const state = paymentSyncStates.get(order.id) || { inFlight: false, nextAttemptAt: 0 }
    paymentSyncStates.set(order.id, state)
    if (state.inFlight || state.nextAttemptAt > now.value) return
    state.inFlight = true
    state.nextAttemptAt = now.value + ORDER_STATUS_POLL_INTERVAL_MS
    void synchronizePaymentOrder(order, state)
  })
  for (const orderId of paymentSyncStates.keys()) {
    if (!activeOrderIds.has(orderId)) paymentSyncStates.delete(orderId)
  }
}

function openRefundDialog(order: PaymentOrder) { refundTarget.value = order; refundReason.value = '' }

function openRefundFromDetails(order: PaymentOrder) {
  closeDetails()
  openRefundDialog(order)
}

function closeRefundDialog() {
  if (actionLoading.value) return
  refundTarget.value = null
  refundReason.value = ''
}

async function confirmRefund() {
  if (!refundTarget.value || !refundReason.value.trim()) return
  actionLoading.value = true
  try {
    await paymentAPI.requestRefund(refundTarget.value.id, { reason: refundReason.value.trim() })
    appStore.showSuccess(t('common.success'))
    refundTarget.value = null
    refundReason.value = ''
    await fetchOrders()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    actionLoading.value = false
  }
}

function canRequestRefund(order: PaymentOrder): boolean {
  if (order.status !== 'COMPLETED' || order.refund_amount > 0) return false
  if (order.invoice?.status === 'ISSUED') return false
  if (!order.provider_instance_id) return false
  return refundEligibleProviders.value.has(order.provider_instance_id)
}

function canOpenInvoice(order: PaymentOrder): boolean {
  return Boolean(order.invoice || invoiceOrderEligible(order))
}

function invoiceActionLabel(order: PaymentOrder): string {
  if (!order.invoice) return t('payment.invoice.request')
  if (order.invoice.status === 'REJECTED' && invoiceOrderEligible(order)) return t('payment.invoice.correct')
  if (order.invoice.status === 'ISSUED') return t('payment.invoice.viewDelivery')
  return t('payment.invoice.viewProgress')
}

function invoiceOrderEligible(order: PaymentOrder): boolean {
  return order.invoice_eligible ?? (
    order.status === 'COMPLETED' &&
    !order.needs_manual_review &&
    order.refund_amount === 0 &&
    (order.refund_requested_amount ?? 0) === 0 &&
    order.pay_amount > 0
  )
}

function openInvoiceDialog(order: PaymentOrder) {
  invoiceTarget.value = order
}

function openInvoiceFromDetails(order: PaymentOrder) {
  closeDetails()
  openInvoiceDialog(order)
}

function closeInvoiceDialog() {
  if (!invoiceSubmitting.value) invoiceTarget.value = null
}

async function submitInvoiceRequest(payload: CreateInvoiceRequest) {
  const target = invoiceTarget.value
  if (!target || invoiceSubmitting.value) return
  invoiceSubmitting.value = true
  try {
    const res = await paymentAPI.createInvoiceRequest(target.id, payload)
    if (invoiceTarget.value === target) invoiceTarget.value = { ...target, invoice: res.data }
    appStore.showSuccess(t('payment.invoice.submitted'))
    await fetchOrders()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    invoiceSubmitting.value = false
  }
}

async function loadRefundEligibility() {
  try {
    const res = await paymentAPI.getRefundEligibleProviders()
    refundEligibleProviders.value = new Set(res.data.provider_instance_ids || [])
  } catch { /* ignore — default to hiding refund button */ }
}

onMounted(() => {
  void fetchOrders()
  void loadRefundEligibility()
  paymentLifecycleTimer = setInterval(refreshPaymentLifecycle, 1000)
})

onUnmounted(() => {
  if (paymentLifecycleTimer) clearInterval(paymentLifecycleTimer)
  paymentLifecycleTimer = null
  paymentSyncStates.clear()
})
</script>
