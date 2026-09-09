<template>
  <AppLayout>
    <div class="space-y-4">
      <!-- Filters -->
      <div class="card p-4">
        <div class="flex flex-wrap items-center gap-3">
          <div class="min-w-56 flex-1 sm:max-w-80">
            <input v-model="orderSearch" type="search" :aria-label="t('payment.admin.searchOrders')" :placeholder="t('payment.admin.searchOrders')" class="input" @input="debounceLoadOrders" />
          </div>
          <Select :aria-label="t('payment.orders.status')" v-model="orderFilters.status" :options="statusFilterOptions" class="w-44" @change="handleFilterChange" />
          <Select :aria-label="t('payment.orders.paymentMethod')" v-model="orderFilters.payment_type" :options="paymentTypeFilterOptions" class="w-40" @change="handleFilterChange" />
          <Select :aria-label="t('payment.orders.orderType')" v-model="orderFilters.order_type" :options="orderTypeFilterOptions" class="w-44" @change="handleFilterChange" />
          <Select v-model="orderFilters.invoice_status" :options="invoiceStatusFilterOptions" :aria-label="t('payment.invoice.admin.filterLabel')" class="w-40" @change="handleFilterChange" />
          <Select v-model="orderFilters.payment_status" :options="paymentFactOptions" :aria-label="t('payment.orderOps.paymentLabel')" class="w-40" @change="handleFilterChange" />
          <Select v-model="orderFilters.fulfillment_status" :options="fulfillmentOptions" :aria-label="t('payment.orderOps.fulfillmentLabel')" class="w-40" @change="handleFilterChange" />
          <div class="flex flex-1 flex-wrap items-center justify-end gap-2">
            <button @click="loadOrders" :disabled="ordersLoading" class="btn btn-secondary" :title="t('common.refresh')">
              <Icon name="refresh" size="md" :class="ordersLoading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>
      </div>

      <div class="flex justify-end"><RouterLink to="/admin/orders/invoices" class="btn btn-secondary">{{ t('payment.orderOps.invoiceQueue') }}</RouterLink></div>
      <details class="text-sm text-gray-600 dark:text-gray-300"><summary class="mb-2 cursor-pointer py-2">{{ t('payment.orderOps.paymentTest') }}</summary><AdminPaymentOwnerTest @created="loadOrders" /></details>

      <!-- Table -->
      <OrderTable :orders="orders" :loading="ordersLoading" show-user>
        <template #actions="{ row }">
          <div class="flex items-center gap-1">
            <button @click="showOrderDetail(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-gray-600 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-dark-600">
              <Icon name="eye" size="sm" />
              {{ t('common.view') }}
            </button>
            <button v-if="row.status === 'PENDING'" :disabled="refundMutationBusy" @click="handleCancelOrder(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-yellow-600 hover:bg-yellow-50 dark:text-yellow-400 dark:hover:bg-yellow-900/20">
              <Icon name="x" size="sm" />
              {{ t('payment.orders.cancel') }}
            </button>
            <button v-if="fulfillmentFact(row) === 'FAILED' && !row.needs_manual_review" :disabled="refundMutationBusy" @click="handleRetryOrder(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/20">
              <Icon name="refresh" size="sm" />
              {{ t('payment.admin.retry') }}
            </button>
            <template v-if="row.status === 'REFUND_REQUESTED'">
              <span v-if="row.refund_amount" class="rounded-full bg-purple-100 px-1.5 py-0.5 text-xs font-medium text-purple-700 dark:bg-purple-900/30 dark:text-purple-300">{{ creditedAmountSymbol }}{{ row.refund_amount.toFixed(2) }}</span>
              <button :disabled="refundMutationBusy" @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-purple-600 hover:bg-purple-50 dark:text-purple-400 dark:hover:bg-purple-900/20">
                <Icon name="check" size="sm" />
                {{ t('payment.admin.approveRefund') }}
              </button>
            </template>
            <button v-else-if="row.status === 'REFUND_FAILED'" :disabled="refundMutationBusy" @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-purple-600 hover:bg-purple-50 dark:text-purple-400 dark:hover:bg-purple-900/20">
              <Icon name="refresh" size="sm" />
              {{ t('payment.admin.retryRefund') }}
            </button>
            <button v-else-if="row.status === 'REFUND_PENDING'" :disabled="refundMutationBusy" @click="handleQueryRefund(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-orange-600 hover:bg-orange-50 disabled:opacity-60 dark:text-orange-400 dark:hover:bg-orange-900/20">
              <Icon name="refresh" size="sm" :class="refundQueryingIds.has(row.id) ? 'animate-spin' : ''" />
              {{ t('payment.admin.queryRefundStatus') }}
            </button>
            <button v-else-if="row.status === 'COMPLETED' || row.status === 'PARTIALLY_REFUNDED'" :disabled="refundMutationBusy" @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20">
              <Icon name="dollar" size="sm" />
              {{ t('payment.admin.refund') }}
            </button>
            <button v-if="row.invoice" @click="openInvoiceDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/20">
              <Icon name="document" size="sm" />
              {{ row.invoice.status === 'ISSUED' || row.invoice.status === 'REJECTED' ? t('payment.invoice.admin.view') : t('payment.invoice.admin.process') }}
            </button>
          </div>
        </template>
      </OrderTable>
      <Pagination v-if="orderPagination.total > 0" :page="orderPagination.page" :total="orderPagination.total" :page-size="orderPagination.page_size" @update:page="handleOrderPageChange" @update:pageSize="handleOrderPageSizeChange" />
    </div>

    <!-- Order Detail Dialog -->
    <BaseDialog :show="showDetailDialog" :title="t('payment.admin.orderDetail')" width="wide" @close="showDetailDialog = false">
      <div v-if="selectedOrder" class="space-y-4">
        <div class="grid grid-cols-2 gap-4">
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</p><p class="font-mono text-sm font-medium text-gray-900 dark:text-white">#{{ selectedOrder.id }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</p><p class="break-all text-sm font-medium text-gray-900 dark:text-white">{{ selectedOrder.out_trade_no }}</p><button type="button" class="mt-1 text-xs text-primary-700 dark:text-primary-300" @click="copyOrderNumber(selectedOrder.out_trade_no)">{{ t('payment.orderOps.copyOrder') }}</button></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.status') }}</p><OrderStatusBadge :status="selectedOrder.status" /></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.amount') }}</p><p class="text-sm font-medium text-gray-900 dark:text-white">{{ creditedAmountSymbol }}{{ selectedOrder.amount.toFixed(2) }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</p><p class="text-sm font-medium text-gray-900 dark:text-white">{{ paymentAmountSymbol(selectedOrder) }}{{ selectedOrder.pay_amount.toFixed(2) }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.paymentMethod') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ t('payment.methods.' + selectedOrder.payment_type, selectedOrder.payment_type) }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.feeRate') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ selectedOrder.fee_rate }}%</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.createdAt') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ formatDateTime(selectedOrder.created_at) }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.expiresAt') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ formatDateTime(selectedOrder.expires_at) }}</p></div>
          <div v-if="selectedOrder.paid_at"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.paidAt') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ formatDateTime(selectedOrder.paid_at) }}</p></div>
          <div v-if="selectedOrder.refund_amount"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundAmount') }}</p><p class="text-sm font-medium text-red-600 dark:text-red-400">{{ creditedAmountSymbol }}{{ selectedOrder.refund_amount.toFixed(2) }}</p></div>
          <div v-if="selectedOrder.refund_reason" class="col-span-2"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundReason') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ selectedOrder.refund_reason }}</p></div>
          <!-- Refund request info -->
          <div v-if="selectedOrder.refund_requested_at" class="col-span-2 border-t border-gray-200 pt-3 dark:border-dark-600">
            <p class="mb-2 text-xs font-medium text-purple-600 dark:text-purple-400">{{ t('payment.admin.refundRequestInfo') }}</p>
            <div class="grid grid-cols-2 gap-4">
              <div>
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundRequestedAt') }}</p>
                <p class="text-sm text-gray-700 dark:text-gray-300">{{ formatDateTime(selectedOrder.refund_requested_at) }}</p>
              </div>
              <div>
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundRequestedBy') }}</p>
                <p class="text-sm text-gray-700 dark:text-gray-300">#{{ selectedOrder.refund_requested_by }}</p>
              </div>
              <div class="col-span-2">
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundRequestReason') }}</p>
                <p class="text-sm text-gray-700 dark:text-gray-300">{{ selectedOrder.refund_request_reason }}</p>
              </div>
            </div>
          </div>
          <div v-if="selectedOrder.invoice" class="col-span-2 border-t border-gray-200 pt-3 dark:border-dark-600">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div>
                <p class="mb-1 text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.invoice.admin.title') }}</p>
                <InvoiceStatusBadge :status="selectedOrder.invoice.status" />
              </div>
              <button class="btn btn-secondary inline-flex items-center gap-2" @click="openInvoiceDialog(selectedOrder)">
                <Icon name="document" size="sm" />{{ t('payment.invoice.admin.openWorkflow') }}
              </button>
            </div>
          </div>
        </div>
        <div class="flex flex-wrap gap-3">
          <OrderLifecycleBadge kind="payment" :value="paymentFact(selectedOrder)" />
          <OrderLifecycleBadge kind="fulfillment" :value="fulfillmentFact(selectedOrder)" />
        </div>
        <OrderPurchaseSnapshot :order="selectedOrder" />
        <!-- Audit Logs -->
        <div v-if="orderAuditLogs.length > 0" class="border-t border-gray-200 pt-4 dark:border-dark-600">
          <p class="mb-2 text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.admin.auditLogs') }}</p>
          <div class="max-h-48 space-y-2 overflow-y-auto">
            <div v-for="log in orderAuditLogs" :key="log.id" class="rounded-lg border border-gray-100 bg-gray-50 p-2.5 dark:border-dark-600 dark:bg-dark-800">
              <div class="flex items-center justify-between">
                <span class="text-xs font-medium text-gray-700 dark:text-gray-300">{{ log.action }}</span>
                <span class="text-xs text-gray-400">{{ formatDateTime(log.created_at) }}</span>
              </div>
              <div v-if="log.detail" class="mt-1 break-all text-xs text-gray-500 dark:text-gray-400">{{ log.detail }}</div>
              <div v-if="log.operator" class="mt-1 text-xs text-gray-400">{{ t('payment.admin.operator') }}: {{ log.operator }}</div>
            </div>
          </div>
        </div>
      </div>
    </BaseDialog>

    <AdminRefundDialog :show="showRefundDialog" :order="selectedOrder" :submitting="refundMutationBusy" :require-force="refundRequireForce" :warning="refundWarning" @confirm="handleRefund" @cancel="closeRefundDialog" />
    <TotpStepUpDialog :controller="stepUp" />
    <AdminInvoiceDialog :show="!!invoiceTarget" :order="invoiceTarget" :submitting="invoiceSubmitting" :retrying="invoiceEmailRetrying" @submit="handleInvoiceUpdate" @retry-email="handleInvoiceEmailRetry" @retry-feishu="handleInvoiceFeishuRetry" @close="invoiceTarget = null" />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import type { AdminUpdateInvoiceRequest, PaymentOrder } from '@/types/payment'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import AdminRefundDialog from '@/components/admin/payment/AdminRefundDialog.vue'
import AdminPaymentOwnerTest from '@/components/admin/payment/AdminPaymentOwnerTest.vue'
import OrderStatusBadge from '@/components/payment/OrderStatusBadge.vue'
import InvoiceStatusBadge from '@/components/payment/InvoiceStatusBadge.vue'
import OrderTable from '@/components/payment/OrderTable.vue'
import OrderPurchaseSnapshot from '@/components/payment/OrderPurchaseSnapshot.vue'
import OrderLifecycleBadge from '@/components/payment/OrderLifecycleBadge.vue'
import { paymentFact, fulfillmentFact } from '@/components/payment/orderPresentation'
import AdminInvoiceDialog from '@/components/admin/payment/AdminInvoiceDialog.vue'
import { currencySymbol } from '@/components/payment/currency'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'

interface AuditLog {
  id: number
  action: string
  detail: string | null
  operator: string | null
  created_at: string
}

interface RefundRequest {
  amount: number
  reason: string
  deduct_balance: boolean
  force: boolean
}

const { t } = useI18n()
const appStore = useAppStore()
const route = useRoute()

const invoiceStatusFromQuery = (() => {
  const value = typeof route.query.invoice_status === 'string' ? route.query.invoice_status.toUpperCase() : ''
  return ['NONE', 'PENDING', 'PROCESSING', 'ISSUED', 'REJECTED'].includes(value) ? value : ''
})()

const ordersLoading = ref(false)
const orders = ref<PaymentOrder[]>([])
const orderSearch = ref('')
const orderFilters = reactive({ status: '', payment_type: '', order_type: '', invoice_status: invoiceStatusFromQuery, payment_status: '', fulfillment_status: '' })
const orderPagination = reactive({ page: 1, page_size: 20, total: 0 })
const selectedOrder = ref<PaymentOrder | null>(null)
const showDetailDialog = ref(false)
const showRefundDialog = ref(false)
const refundSubmitting = ref(false)
const refundRequireForce = ref(false)
const refundWarning = ref('')
const refundQueryingIds = ref(new Set<number>())
const orderAuditLogs = ref<AuditLog[]>([])
const invoiceTarget = ref<PaymentOrder | null>(null)
const invoiceSubmitting = ref(false)
const invoiceEmailRetrying = ref(false)
const creditedAmountSymbol = currencySymbol('USD')
const stepUp = useStepUp()
const refundMutationBusy = computed(() => refundSubmitting.value || refundQueryingIds.value.size > 0)

function paymentAmountSymbol(order: PaymentOrder | null | undefined): string {
  return currencySymbol(order?.currency)
}

let debounceTimer: ReturnType<typeof setTimeout> | null = null
function debounceLoadOrders() {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(handleFilterChange, 300)
}

function handleFilterChange() { orderPagination.page = 1; loadOrders() }
onUnmounted(() => { if (debounceTimer) clearTimeout(debounceTimer) })
const paymentFactOptions = computed(() => [
  { value: '', label: t('payment.orderOps.allPayments') },
  ...['PAID', 'UNPAID'].map(value => ({ value, label: t(`payment.orderOps.payment.${value.toLowerCase()}`) })),
])
const fulfillmentOptions = computed(() => [
  { value: '', label: t('payment.orderOps.allFulfillments') },
  ...['PENDING', 'FAILED', 'MANUAL_REVIEW', 'FULFILLED', 'NOT_STARTED'].map(value => ({ value, label: t(`payment.orderOps.fulfillment.${value.toLowerCase()}`) })),
])
let listRequest = 0
async function loadOrders() {
  const request = ++listRequest
  ordersLoading.value = true
  try {
    const res = await adminPaymentAPI.getOrders({
      page: orderPagination.page, page_size: orderPagination.page_size,
      keyword: orderSearch.value || undefined, status: orderFilters.status || undefined,
      payment_type: orderFilters.payment_type || undefined, order_type: orderFilters.order_type || undefined,
      invoice_status: orderFilters.invoice_status || undefined,
      payment_status: orderFilters.payment_status || undefined,
      fulfillment_status: orderFilters.fulfillment_status || undefined,
    })
    if (request !== listRequest) return
    orders.value = res.data.items || []
    orderPagination.total = res.data.total || 0
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally { if (request === listRequest) ordersLoading.value = false }
}

function handleOrderPageChange(page: number) { orderPagination.page = page; loadOrders() }
function handleOrderPageSizeChange(size: number) { orderPagination.page_size = size; orderPagination.page = 1; loadOrders() }

const statusFilterOptions = computed(() => [
  { value: '', label: t('payment.admin.allStatuses') },
  { value: 'PENDING', label: t('payment.status.pending') },
  { value: 'PAID', label: t('payment.status.paid') },
  { value: 'COMPLETED', label: t('payment.status.completed') },
  { value: 'EXPIRED', label: t('payment.status.expired') },
  { value: 'CANCELLED', label: t('payment.status.cancelled') },
  { value: 'FAILED', label: t('payment.status.failed') },
  { value: 'REFUNDED', label: t('payment.status.refunded') },
  { value: 'REFUND_REQUESTED', label: t('payment.status.refund_requested') },
  { value: 'REFUND_PENDING', label: t('payment.status.refund_pending') },
  { value: 'REFUND_FAILED', label: t('payment.status.refund_failed') },
])

const paymentTypeFilterOptions = computed(() => [
  { value: '', label: t('payment.admin.allPaymentTypes') },
  { value: 'alipay', label: t('payment.methods.alipay') },
  { value: 'wxpay', label: t('payment.methods.wxpay') },
  { value: 'stripe', label: t('payment.methods.stripe') },
  { value: 'airwallex', label: t('payment.methods.airwallex') },
])

const orderTypeFilterOptions = computed(() => [
  { value: '', label: t('payment.admin.allOrderTypes') },
  { value: 'balance', label: t('payment.admin.balanceOrder') },
  { value: 'subscription', label: t('payment.admin.subscriptionOrder') },
])

const invoiceStatusFilterOptions = computed(() => [
  { value: '', label: t('payment.invoice.admin.allStatuses') },
  { value: 'NONE', label: t('payment.invoice.admin.notRequested') },
  { value: 'PENDING', label: t('payment.invoice.status.pending') },
  { value: 'PROCESSING', label: t('payment.invoice.status.processing') },
  { value: 'ISSUED', label: t('payment.invoice.status.issued') },
  { value: 'REJECTED', label: t('payment.invoice.status.rejected') },
])

async function copyOrderNumber(value: string) {
  try { await navigator.clipboard.writeText(value); appStore.showSuccess(t('common.success')) }
  catch { appStore.showError(t('payment.orderOps.copyFailed')) }
}
let detailRequestSequence = 0
async function showOrderDetail(order: PaymentOrder) {
  const requestSequence = ++detailRequestSequence
  selectedOrder.value = order
  orderAuditLogs.value = []
  showDetailDialog.value = true
  try {
    const res = await adminPaymentAPI.getOrder(order.id)
    if (requestSequence !== detailRequestSequence || !showDetailDialog.value || selectedOrder.value?.id !== order.id) return
    const data = res.data as unknown as Record<string, unknown>
    if (data.order) selectedOrder.value = data.order as PaymentOrder
    orderAuditLogs.value = ((data.auditLogs || data.audit_logs || []) as unknown) as AuditLog[]
  } catch (_err: unknown) { /* keep cached order data */ }
}

async function handleCancelOrder(order: PaymentOrder) {
  if (refundMutationBusy.value) return
  try { await adminPaymentAPI.cancelOrder(order.id); appStore.showSuccess(t('payment.admin.orderCancelled')); loadOrders() }
  catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
}

async function handleRetryOrder(order: PaymentOrder) {
  if (refundMutationBusy.value) return
  try { await adminPaymentAPI.retryRecharge(order.id); appStore.showSuccess(t('payment.admin.retrySuccess')); loadOrders() }
  catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
}

function openRefundDialog(order: PaymentOrder) {
  if (refundMutationBusy.value) return
  selectedOrder.value = order
  refundRequireForce.value = false
  refundWarning.value = order.invoice?.status === 'ISSUED' ? t('payment.invoice.admin.refundCorrectionWarning') : ''
  showRefundDialog.value = true
}

function openInvoiceDialog(order: PaymentOrder) {
  invoiceTarget.value = order
  showDetailDialog.value = false
}

async function handleInvoiceUpdate(payload: AdminUpdateInvoiceRequest) {
  const target = invoiceTarget.value
  if (!target || invoiceSubmitting.value || invoiceEmailRetrying.value) return
  invoiceSubmitting.value = true
  try {
    const res = await adminPaymentAPI.updateInvoiceRequest(target.id, payload)
    const updated = { ...target, invoice: res.data }
    if (invoiceTarget.value === target) invoiceTarget.value = updated
    orders.value = orders.value.map((order) => order.id === updated.id ? updated : order)
    appStore.showSuccess(t('payment.invoice.admin.updated'))
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    invoiceSubmitting.value = false
  }
}

async function handleInvoiceFeishuRetry() {
  const target = invoiceTarget.value
  if (!target || invoiceEmailRetrying.value || invoiceSubmitting.value) return
  invoiceEmailRetrying.value = true
  try {
    const res = await adminPaymentAPI.retryInvoiceFeishu(target.id)
    if (invoiceTarget.value === target) invoiceTarget.value = { ...target, invoice: res.data }
    appStore.showSuccess(t('payment.orderOps.notificationQueued'))
    await loadOrders()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally { invoiceEmailRetrying.value = false }
}

async function handleInvoiceEmailRetry() {
  const target = invoiceTarget.value
  if (!target || invoiceEmailRetrying.value || invoiceSubmitting.value) return
  invoiceEmailRetrying.value = true
  try {
    const res = await adminPaymentAPI.retryInvoiceEmail(target.id)
    const updated = { ...target, invoice: res.data }
    if (invoiceTarget.value === target) invoiceTarget.value = updated
    orders.value = orders.value.map((order) => order.id === updated.id ? updated : order)
    appStore.showSuccess(t('payment.invoice.admin.emailRetryQueued'))
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    invoiceEmailRetrying.value = false
  }
}

function closeRefundDialog() {
  showRefundDialog.value = false
  refundRequireForce.value = false
  refundWarning.value = ''
}

function isRefundPendingWarning(warning: string | undefined): boolean {
  return /pending|处理中|待/.test(String(warning || '').toLowerCase())
}

function closeRefundDialogFor(orderID: number) {
  if (selectedOrder.value?.id === orderID) closeRefundDialog()
}

async function handleRefund(data: RefundRequest) {
  if (refundMutationBusy.value || !selectedOrder.value) return
  // Keep the exact operation immutable while a step-up prompt is open. The
  // current selection can change through the surrounding admin view, but it
  // must never redirect the retry to a different order or altered amount.
  const orderID = selectedOrder.value.id
  const request: RefundRequest = {
    amount: data.amount,
    reason: data.reason,
    deduct_balance: data.deduct_balance,
    force: data.force,
  }
  refundSubmitting.value = true
  try {
    const res = await stepUp.run(() => adminPaymentAPI.refundOrder(orderID, request))
    if (res.data.success) {
      if (res.data.warning) appStore.showWarning(res.data.warning)
      else appStore.showSuccess(t('payment.admin.refundSuccess'))
      closeRefundDialogFor(orderID)
      loadOrders()
      return
    }
    if (isRefundPendingWarning(res.data.warning)) {
      appStore.showSuccess(t('payment.admin.refundPending'))
      closeRefundDialogFor(orderID)
      loadOrders()
      return
    }
    if (res.data.require_force) {
      // Backend needs an explicit force confirmation (e.g. the user spent their
      // balance after requesting the refund). Keep the dialog open and surface
      // the force checkbox instead of dropping the admin back to the list.
      if (selectedOrder.value?.id === orderID) {
        refundRequireForce.value = true
        refundWarning.value = res.data.warning || ''
      }
      return
    }
    appStore.showError(res.data.warning || t('common.error'))
  } catch (err: unknown) {
    if (!isStepUpCancelled(err)) appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  }
  finally { refundSubmitting.value = false }
}

async function handleQueryRefund(order: PaymentOrder) {
  if (refundMutationBusy.value) return
  const orderID = order.id
  refundQueryingIds.value = new Set(refundQueryingIds.value).add(orderID)
  try {
    const res = await stepUp.run(() => adminPaymentAPI.queryRefund(orderID))
    if (res.data.success) {
      if (res.data.warning) appStore.showWarning(res.data.warning)
      else appStore.showSuccess(t('payment.admin.refundSuccess'))
    } else if (isRefundPendingWarning(res.data.warning)) {
      appStore.showSuccess(t('payment.admin.refundPending'))
    } else {
      appStore.showError(res.data.warning || t('common.error'))
    }
    loadOrders()
  } catch (err: unknown) {
    if (!isStepUpCancelled(err)) appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    const next = new Set(refundQueryingIds.value)
    next.delete(orderID)
    refundQueryingIds.value = next
  }
}

function formatDateTime(dateStr: string): string { return formatOrderDateTime(dateStr) }

onMounted(() => loadOrders())
</script>
