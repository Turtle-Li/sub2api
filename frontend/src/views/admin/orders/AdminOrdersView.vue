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
          <Select v-model="orderFilters.fulfillment_status" :options="fulfillmentOptions" :aria-label="t('payment.orderOps.fulfillmentFilterLabel')" class="w-40" @change="handleFilterChange" />
          <div class="flex flex-1 flex-wrap items-center justify-end gap-2">
            <button @click="loadOrders" :disabled="ordersLoading" class="btn btn-secondary" :title="t('common.refresh')">
              <Icon name="refresh" size="md" :class="ordersLoading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>
      </div>


      <!-- Table -->
      <OrderTable :orders="orders" :loading="ordersLoading" show-user>
        <template #actions="{ row }">
          <div class="flex flex-wrap items-center gap-1">
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
            <template v-if="row.status === 'REFUND_REQUESTED' && canOpenRefundReview(row)">
              <button :disabled="refundMutationBusy" @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-purple-600 hover:bg-purple-50 dark:text-purple-400 dark:hover:bg-purple-900/20">
                <Icon name="check" size="sm" />
                {{ t('payment.admin.approveRefund') }}
              </button>
            </template>
            <button v-else-if="row.status === 'REFUND_FAILED' && canOpenRefundReview(row)" :disabled="refundMutationBusy" @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-purple-600 hover:bg-purple-50 dark:text-purple-400 dark:hover:bg-purple-900/20">
              <Icon name="refresh" size="sm" />
              {{ t('payment.admin.retryRefund') }}
            </button>
            <template v-else-if="row.status === 'REFUND_PENDING'">
              <template v-if="row.refund_recovery?.state === 'WAITING_PROVIDER_BALANCE'">
                <button :disabled="refundMutationBusy" @click="handleRetryPausedRefund(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-blue-700 hover:bg-blue-50 disabled:opacity-60 dark:text-blue-300 dark:hover:bg-blue-900/20">
                  <Icon name="refresh" size="sm" :class="refundRetryingIds.has(row.id) ? 'animate-spin' : ''" />
                  {{ t('payment.admin.retryPausedRefund') }}
                </button>
                <button :disabled="refundMutationBusy" @click="openExternalRefundDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-amber-700 hover:bg-amber-50 disabled:opacity-60 dark:text-amber-300 dark:hover:bg-amber-900/20">
                  <Icon name="check" size="sm" />
                  {{ t('payment.admin.externalRefundAction') }}
                </button>
              </template>
              <button v-else :disabled="refundMutationBusy" @click="handleQueryRefund(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-orange-600 hover:bg-orange-50 disabled:opacity-60 dark:text-orange-400 dark:hover:bg-orange-900/20">
                <Icon name="refresh" size="sm" :class="refundQueryingIds.has(row.id) ? 'animate-spin' : ''" />
                {{ t('payment.admin.queryRefundStatus') }}
              </button>
            </template>
            <button v-else-if="canOpenRefundReview(row)" :disabled="refundMutationBusy" @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20">
              <Icon name="dollar" size="sm" />
              {{ row.needs_manual_review ? t('payment.admin.reviewRefund') : t('payment.admin.refund') }}
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
          <div v-if="selectedOrder.refund_requested_amount"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.pendingRefundAmount') }}</p><p class="text-sm font-medium text-amber-600 dark:text-amber-400">{{ creditedAmountSymbol }}{{ selectedOrder.refund_requested_amount.toFixed(2) }}</p></div>
          <div v-if="selectedOrder.refund_reason" class="col-span-2"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundReason') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ selectedOrder.refund_reason }}</p></div>
          <div v-if="selectedOrder.refund_recovery?.state === 'WAITING_PROVIDER_BALANCE'" class="col-span-2 rounded-lg border border-amber-300 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-950/30">
            <p class="text-xs font-semibold text-amber-900 dark:text-amber-100">{{ t('payment.admin.refundRecoveryStatus') }}</p>
            <p class="mt-1 text-sm leading-6 text-amber-800 dark:text-amber-200">{{ t('payment.admin.refundMerchantBalanceInsufficientDetail') }}</p>
            <p v-if="selectedOrder.refund_recovery.updated_at" class="mt-1 text-xs text-amber-700 dark:text-amber-300">{{ formatDateTime(selectedOrder.refund_recovery.updated_at) }}</p>
          </div>
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
            <div>
              <p class="mb-1 text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.invoice.admin.title') }}</p>
              <InvoiceStatusBadge :status="selectedOrder.invoice.status" />
            </div>
          </div>
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
            <p class="mb-1 text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.issuanceRecord') }}</p>
            <OrderLifecycleBadge kind="fulfillment" :value="fulfillmentFact(selectedOrder)" />
          </div>
          <div v-if="selectedOrder.needs_manual_review && !hasRefundHandling(selectedOrder)" class="rounded-lg border border-amber-200 bg-amber-50 p-3 sm:col-span-2 dark:border-amber-900/70 dark:bg-amber-950/20">
            <p class="text-xs font-medium text-amber-900 dark:text-amber-100">{{ t('payment.orderOps.reviewRequired') }}</p>
            <p v-if="paymentFact(selectedOrder) === 'PAID'" class="mt-1 text-sm text-amber-800 dark:text-amber-200">{{ t('payment.result.paidManualReview') }}</p>
          </div>
          <div v-if="hasRefundEntitlementStatus(selectedOrder)" class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
            <p class="mb-1 text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.currentEntitlement') }}</p>
            <OrderLifecycleBadge kind="refundEntitlement" :value="selectedOrder.refund_entitlement_status || 'NOT_APPLICABLE'" />
          </div>
        </div>
        <p v-if="refundBlockerKey(selectedOrder)" class="text-sm" :class="refundBlockerClass(selectedOrder)">
          {{ t(refundBlockerKey(selectedOrder)) }}
        </p>
        <OrderPurchaseSnapshot :order="selectedOrder" />
        <div class="flex flex-wrap justify-end gap-2 border-t border-gray-200 pt-4 dark:border-dark-600">
          <button v-if="selectedOrder.status === 'PENDING'" :disabled="refundMutationBusy" class="btn btn-secondary" @click="handleCancelOrder(selectedOrder)">
            {{ t('payment.orders.cancel') }}
          </button>
          <button v-if="fulfillmentFact(selectedOrder) === 'FAILED' && !selectedOrder.needs_manual_review" :disabled="refundMutationBusy" class="btn btn-secondary" @click="handleRetryOrder(selectedOrder)">
            {{ t('payment.admin.retry') }}
          </button>
          <button v-if="canOpenRefundReview(selectedOrder)" :disabled="refundMutationBusy" class="btn btn-danger" @click="openRefundDialog(selectedOrder)">
            {{ selectedOrder.needs_manual_review ? t('payment.admin.reviewRefund') : refundActionLabel(selectedOrder) }}
          </button>
          <template v-else-if="selectedOrder.status === 'REFUND_PENDING'">
            <template v-if="selectedOrder.refund_recovery?.state === 'WAITING_PROVIDER_BALANCE'">
              <button :disabled="refundMutationBusy" class="btn btn-secondary" @click="handleRetryPausedRefund(selectedOrder)">
                {{ t('payment.admin.retryPausedRefund') }}
              </button>
              <button :disabled="refundMutationBusy" class="btn btn-secondary" @click="openExternalRefundDialog(selectedOrder)">
                {{ t('payment.admin.externalRefundAction') }}
              </button>
            </template>
            <button v-else :disabled="refundMutationBusy" class="btn btn-secondary" @click="handleQueryRefund(selectedOrder)">
              {{ t('payment.admin.queryRefundStatus') }}
            </button>
          </template>
        </div>
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

    <AdminRefundDialog
      :show="showRefundDialog"
      :order="refundTarget"
      :review="refundReview"
      :loading="refundReviewLoading"
      :previewing="refundPreviewing"
      :reviewed-refund-amount="reviewedRefundAmount"
      :error="refundReviewError"
      :submitting="refundSubmitting"
      :backfilling="refundBackfilling"
      :warning="refundWarning"
      @confirm="handleRefund"
      @preview="scheduleRefundPreview"
      @backfill="handleSubscriptionGrantBackfill"
      @cancel="closeRefundDialog"
    />
    <BaseDialog :show="!!externalRefundTarget" :title="t('payment.admin.externalRefundTitle')" @close="closeExternalRefundDialog">
      <form v-if="externalRefundTarget" class="space-y-4" @submit.prevent="handleConfirmExternalRefund">
        <div class="rounded-lg border border-amber-300 bg-amber-50 p-4 text-sm leading-6 text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-100">
          <p class="font-semibold">{{ t('payment.admin.externalRefundWarningTitle') }}</p>
          <p class="mt-1">{{ t('payment.admin.externalRefundWarning') }}</p>
        </div>
        <div class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-700">
          <div class="flex items-center justify-between gap-3">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
            <span class="font-mono text-gray-900 dark:text-white">#{{ externalRefundTarget.id }}</span>
          </div>
          <div class="mt-2 flex items-center justify-between gap-3">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.externalRefundExactAmount') }}</span>
            <span class="font-semibold text-gray-900 dark:text-white">{{ recoveryAmount(externalRefundTarget) }}</span>
          </div>
        </div>
        <div>
          <label class="input-label" for="external-refund-method">{{ t('payment.admin.externalRefundMethod') }}</label>
          <Select id="external-refund-method" v-model="externalRefundForm.method_code" :options="externalRefundMethodOptions" class="mt-1 w-full" />
        </div>
        <div>
          <label class="input-label" for="external-refund-reference">{{ t('payment.admin.externalRefundReference') }}</label>
          <input id="external-refund-reference" v-model="externalRefundForm.external_reference" class="input mt-1 w-full" maxlength="160" :placeholder="t('payment.admin.externalRefundReferencePlaceholder')" />
        </div>
        <div>
          <label class="input-label" for="external-refund-time">{{ t('payment.admin.externalRefundTime') }}</label>
          <input id="external-refund-time" v-model="externalRefundForm.refunded_at" type="datetime-local" class="input mt-1 w-full" />
        </div>
        <div>
          <label class="input-label" for="external-refund-evidence">{{ t('payment.admin.externalRefundEvidence') }}</label>
          <textarea id="external-refund-evidence" v-model="externalRefundForm.evidence_detail" rows="3" maxlength="240" class="input mt-1 w-full" :placeholder="t('payment.admin.externalRefundEvidencePlaceholder')" />
        </div>
        <div class="flex justify-end gap-3 border-t border-gray-200 pt-4 dark:border-dark-600">
          <button type="button" class="btn btn-secondary" :disabled="externalRefundSubmitting" @click="closeExternalRefundDialog">{{ t('common.cancel') }}</button>
          <button type="submit" class="btn btn-danger" :disabled="!externalRefundFormValid || externalRefundSubmitting">
            {{ externalRefundSubmitting ? t('common.processing') : t('payment.admin.externalRefundConfirm') }}
          </button>
        </div>
      </form>
    </BaseDialog>
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import type {
  ExternalRefundConfirmationRequest,
  ExternalRefundMethodCode,
  RefundOrderRequest,
  RefundReasonCode,
  RefundReview,
  SubscriptionGrantBackfillRequest,
} from '@/api/admin/payment'
import { extractApiErrorCode, extractI18nErrorMessage } from '@/utils/apiError'
import { canRefund as canOpenRefundReview, formatOrderDateTime } from '@/components/payment/orderUtils'
import type { PaymentOrder } from '@/types/payment'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import AdminRefundDialog from '@/components/admin/payment/AdminRefundDialog.vue'
import OrderStatusBadge from '@/components/payment/OrderStatusBadge.vue'
import InvoiceStatusBadge from '@/components/payment/InvoiceStatusBadge.vue'
import OrderTable from '@/components/payment/OrderTable.vue'
import OrderPurchaseSnapshot from '@/components/payment/OrderPurchaseSnapshot.vue'
import OrderLifecycleBadge from '@/components/payment/OrderLifecycleBadge.vue'
import { fulfillmentFact, paymentFact } from '@/components/payment/orderPresentation'
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
  reason_code: RefundReasonCode
  reason_detail?: string
  refund_amount?: number
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
const refundTarget = ref<PaymentOrder | null>(null)
const refundReview = ref<RefundReview | null>(null)
const refundReviewLoading = ref(false)
const refundPreviewing = ref(false)
const reviewedRefundAmount = ref<number | undefined>(undefined)
const refundReviewError = ref('')
const refundSubmitting = ref(false)
const refundBackfilling = ref(false)
const refundWarning = ref('')
const refundQueryingIds = ref(new Set<number>())
const refundRetryingIds = ref(new Set<number>())
const externalRefundTarget = ref<PaymentOrder | null>(null)
const externalRefundSubmitting = ref(false)
const externalRefundForm = reactive({
  method_code: 'wechat_transfer' as ExternalRefundMethodCode,
  external_reference: '',
  refunded_at: '',
  evidence_detail: '',
})
const orderAuditLogs = ref<AuditLog[]>([])
const creditedAmountSymbol = currencySymbol('USD')
const stepUp = useStepUp()
const refundMutationBusy = computed(() => refundSubmitting.value || refundBackfilling.value || externalRefundSubmitting.value || refundQueryingIds.value.size > 0 || refundRetryingIds.value.size > 0)
const externalRefundMethodOptions = computed(() => [
  { value: 'wechat_transfer', label: t('payment.admin.externalRefundMethods.wechat_transfer') },
  { value: 'original_channel_manual', label: t('payment.admin.externalRefundMethods.original_channel_manual') },
  { value: 'bank_transfer', label: t('payment.admin.externalRefundMethods.bank_transfer') },
  { value: 'other', label: t('payment.admin.externalRefundMethods.other') },
])
const externalRefundFormValid = computed(() => {
  const timestamp = new Date(externalRefundForm.refunded_at).getTime()
  return !!externalRefundTarget.value && !!externalRefundForm.external_reference.trim() &&
    !!externalRefundForm.evidence_detail.trim() && Number.isFinite(timestamp) && timestamp <= Date.now() + 5 * 60 * 1000
})

function paymentAmountSymbol(order: PaymentOrder | null | undefined): string {
  return currencySymbol(order?.currency)
}

function hasRefundEntitlementStatus(order: PaymentOrder): boolean {
  return Boolean(order.refund_entitlement_status && order.refund_entitlement_status !== 'NOT_APPLICABLE')
}

function hasRefundHandling(order: PaymentOrder): boolean {
  return hasRefundEntitlementStatus(order) || Boolean(order.refund_recovery?.state)
}

function refundBlockerKey(order: PaymentOrder): string {
  if (order.refund_recovery?.state === 'WAITING_PROVIDER_BALANCE') return 'payment.admin.refundMerchantBalanceInsufficientShort'
  if (order.refund_recovery?.state === 'RETRY_QUEUED') return 'payment.admin.refundRetryQueuedShort'
  return ''
}

function refundBlockerClass(order: PaymentOrder): string {
  if (order.refund_recovery?.state === 'WAITING_PROVIDER_BALANCE') return 'text-amber-800 dark:text-amber-200'
  if (order.refund_recovery?.state === 'RETRY_QUEUED') return 'text-blue-700 dark:text-blue-300'
  return 'text-red-700 dark:text-red-300'
}

function refundActionLabel(order: PaymentOrder): string {
  if (order.status === 'REFUND_REQUESTED') return t('payment.admin.approveRefund')
  if (order.status === 'REFUND_FAILED') return t('payment.admin.retryRefund')
  return t('payment.admin.refund')
}

let debounceTimer: ReturnType<typeof setTimeout> | null = null
function debounceLoadOrders() {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(handleFilterChange, 300)
}

function handleFilterChange() { orderPagination.page = 1; loadOrders() }
onUnmounted(() => {
  if (debounceTimer) clearTimeout(debounceTimer)
  clearRefundPreviewTimer()
  listRequest += 1
  stopRefundRefreshes()
  document.removeEventListener('visibilitychange', resumeRefundRefreshes)
})
const paymentFactOptions = computed(() => [
  { value: '', label: t('payment.orderOps.allPayments') },
  ...['PAID', 'UNPAID'].map(value => ({ value, label: t(`payment.orderOps.payment.${value.toLowerCase()}`) })),
])
const fulfillmentOptions = computed(() => [
  { value: '', label: t('payment.orderOps.allFulfillments') },
  ...['PENDING', 'FAILED', 'FULFILLED', 'NOT_STARTED'].map(value => ({ value, label: t(`payment.orderOps.fulfillment.${value.toLowerCase()}`) })),
])
let listRequest = 0
async function loadOrders() {
  stopRefundRefreshes()
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

// Only explicit refund operations enroll a row. Reads are local; provider
// reconciliation remains owned by the existing backend worker.
type RefundRefresh = {
  controller: AbortController
  timer?: ReturnType<typeof setTimeout>
  expiresAt: number
  attempts: number
  busy: boolean
}
const refundRefreshes = new Map<number, RefundRefresh>()
function stopRefundRefresh(orderID: number) {
  const refresh = refundRefreshes.get(orderID)
  if (!refresh) return
  clearTimeout(refresh.timer)
  refresh.controller.abort()
  refundRefreshes.delete(orderID)
}
function stopRefundRefreshes() {
  for (const id of refundRefreshes.keys()) stopRefundRefresh(id)
}
function resumeRefundRefreshes() {
  if (document.hidden) return
  for (const [id, refresh] of refundRefreshes) {
    clearTimeout(refresh.timer)
    void refreshRefundRow(id, refresh)
  }
}
function startRefundRefresh(orderID: number, listVersion: number) {
  // A mutation finishing after navigation must not enroll a different page.
  if (listVersion !== listRequest || !orders.value.some(row => row.id === orderID)) return
  stopRefundRefresh(orderID)
  const refresh: RefundRefresh = { controller: new AbortController(), expiresAt: Date.now() + 300_000, attempts: 0, busy: false }
  refundRefreshes.set(orderID, refresh)
  void refreshRefundRow(orderID, refresh)
}
async function refreshRefundRow(orderID: number, refresh: RefundRefresh) {
  if (refundRefreshes.get(orderID) !== refresh || refresh.busy) return
  if (Date.now() >= refresh.expiresAt || !orders.value.some(row => row.id === orderID)) {
    stopRefundRefresh(orderID)
    return
  }
  if (document.hidden) return
  refresh.busy = true
  try {
    const res = await adminPaymentAPI.getOrder(orderID, refresh.controller.signal)
    if (refundRefreshes.get(orderID) !== refresh) return
    const data = res.data as unknown as { order: PaymentOrder; auditLogs?: AuditLog[]; audit_logs?: AuditLog[] }
    const updated = data.order
    if (!updated || updated.id !== orderID) {
      stopRefundRefresh(orderID)
      return
    }
    const index = orders.value.findIndex(row => row.id === orderID)
    if (index !== -1) orders.value[index] = updated
    if (showDetailDialog.value && selectedOrder.value?.id === orderID) {
      // Prevent an older detail request from restoring the pre-refund state.
      detailRequestSequence += 1
      selectedOrder.value = updated
      orderAuditLogs.value = data.auditLogs || data.audit_logs || []
    }
    if (updated.status !== 'REFUND_PENDING' || updated.needs_manual_review ||
        ['WAITING_PROVIDER_BALANCE', 'MANUAL_REVIEW', 'SUCCEEDED', 'FAILED'].includes(updated.refund_recovery?.state || '')) {
      stopRefundRefresh(orderID)
    }
  } catch (err: unknown) {
    const status = (err as { status?: number })?.status
    if (status === 401 || status === 403 || status === 404) stopRefundRefresh(orderID)
    // Transient reads retry silently within the same bounded budget.
  } finally {
    refresh.busy = false
    if (refundRefreshes.get(orderID) === refresh) {
      const delay = Math.min(2000 * 2 ** Math.min(refresh.attempts++, 3), 15000)
      refresh.timer = setTimeout(() => void refreshRefundRow(orderID, refresh), Math.min(delay, Math.max(0, refresh.expiresAt - Date.now())))
    }
  }
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
  { value: 'PARTIALLY_REFUNDED', label: t('payment.status.partially_refunded') },
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

function parseOrderIDQuery(value: unknown): number | null {
  if (typeof value !== 'string' || !/^[1-9]\d*$/.test(value)) return null
  const orderID = Number(value)
  return Number.isSafeInteger(orderID) ? orderID : null
}

function getRouteOrderID(): number | null {
  return parseOrderIDQuery(route.query.order_id)
}

function extractOrderDetail(data: unknown, orderID: number): { order: PaymentOrder; auditLogs: AuditLog[] } | null {
  if (!data || typeof data !== 'object') return null
  const response = data as Record<string, unknown>
  const candidate = response.order && typeof response.order === 'object' ? response.order as Record<string, unknown> : response
  if (typeof candidate.id !== 'number' || candidate.id !== orderID) return null

  const auditLogValue = response.auditLogs || response.audit_logs
  return {
    order: candidate as unknown as PaymentOrder,
    auditLogs: Array.isArray(auditLogValue) ? auditLogValue as AuditLog[] : [],
  }
}

interface OrderDetailLoadOptions {
  fallbackOrder?: PaymentOrder
  isCurrent?: () => boolean
}

let detailRequestSequence = 0
async function loadOrderDetail(orderID: number, options: OrderDetailLoadOptions = {}) {
  const requestSequence = ++detailRequestSequence
  const isCurrent = options.isCurrent || (() => true)

  if (options.fallbackOrder) {
    selectedOrder.value = options.fallbackOrder
    orderAuditLogs.value = []
    showDetailDialog.value = true
  } else {
    selectedOrder.value = null
    orderAuditLogs.value = []
    showDetailDialog.value = false
  }

  try {
    const res = await adminPaymentAPI.getOrder(orderID)
    if (requestSequence !== detailRequestSequence || !isCurrent()) return
    const detail = extractOrderDetail(res.data, orderID)
    if (detail) {
      selectedOrder.value = detail.order
      orderAuditLogs.value = detail.auditLogs
      showDetailDialog.value = true
      return
    }

    if (!options.fallbackOrder) {
      selectedOrder.value = null
      orderAuditLogs.value = []
      showDetailDialog.value = false
      appStore.showError(t('common.error'))
    }
  } catch (err: unknown) {
    if (requestSequence !== detailRequestSequence || !isCurrent()) return
    if (options.fallbackOrder) return

    selectedOrder.value = null
    orderAuditLogs.value = []
    showDetailDialog.value = false
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  }
}

async function showOrderDetail(order: PaymentOrder) {
  await loadOrderDetail(order.id, {
    fallbackOrder: order,
    isCurrent: () => showDetailDialog.value && selectedOrder.value?.id === order.id,
  })
}

let routeDetailRequest = 0
function loadOrderDetailFromRoute() {
  const request = ++routeDetailRequest
  const orderID = getRouteOrderID()
  if (orderID === null) return

  void loadOrderDetail(orderID, {
    isCurrent: () => request === routeDetailRequest && getRouteOrderID() === orderID,
  })
}

watch(() => route.query.order_id, loadOrderDetailFromRoute, { immediate: true })

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

let refundDialogSession = 0
let refundReviewRequest = 0
let refundPreviewTimer: ReturnType<typeof setTimeout> | null = null

function isCurrentRefundDialog(orderID: number, session: number): boolean {
  return refundDialogSession === session && showRefundDialog.value && refundTarget.value?.id === orderID
}

function clearRefundPreviewTimer() {
  if (!refundPreviewTimer) return
  clearTimeout(refundPreviewTimer)
  refundPreviewTimer = null
}

async function loadRefundReview(order: PaymentOrder, session: number, refundAmount?: number, request = ++refundReviewRequest): Promise<void> {
  const orderID = order.id
  const isExplicitAmount = refundAmount !== undefined
  if (isCurrentRefundDialog(orderID, session)) {
    if (isExplicitAmount) {
      refundPreviewing.value = true
    } else {
      refundReviewLoading.value = true
      refundReview.value = null
    }
    reviewedRefundAmount.value = undefined
    refundReviewError.value = ''
  }

  try {
    const res = await adminPaymentAPI.getRefundReview(orderID, refundAmount)
    if (!isCurrentRefundDialog(orderID, session) || request !== refundReviewRequest) return
    refundReview.value = res.data
    reviewedRefundAmount.value = refundAmount
  } catch (err: unknown) {
    if (!isCurrentRefundDialog(orderID, session) || request !== refundReviewRequest) return
    refundReviewError.value = extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))
  } finally {
    if (isCurrentRefundDialog(orderID, session) && request === refundReviewRequest) {
      if (isExplicitAmount) refundPreviewing.value = false
      else refundReviewLoading.value = false
    }
  }
}

function scheduleRefundPreview(refundAmount: number | null) {
  const target = refundTarget.value
  if (!target || !showRefundDialog.value || refundSubmitting.value || refundBackfilling.value) return

  const session = refundDialogSession
  clearRefundPreviewTimer()
  const request = ++refundReviewRequest
  reviewedRefundAmount.value = undefined
  refundReviewLoading.value = false
  refundReviewError.value = ''

  if (refundAmount === null) {
    refundPreviewing.value = false
    return
  }

  refundPreviewing.value = true
  refundPreviewTimer = setTimeout(() => {
    refundPreviewTimer = null
    void loadRefundReview(target, session, refundAmount, request)
  }, 250)
}

function openRefundDialog(order: PaymentOrder) {
  if (refundMutationBusy.value || !canOpenRefundReview(order)) return
  showDetailDialog.value = false
  const session = ++refundDialogSession
  refundTarget.value = order
  refundReview.value = null
  reviewedRefundAmount.value = undefined
  refundReviewError.value = ''
  refundPreviewing.value = false
  refundWarning.value = order.invoice?.status === 'ISSUED' ? t('payment.invoice.refundBlockedAfterIssue') : ''
  showRefundDialog.value = true
  void loadRefundReview(order, session)
}

function closeRefundDialog() {
  clearRefundPreviewTimer()
  refundDialogSession += 1
  refundReviewRequest += 1
  showRefundDialog.value = false
  refundTarget.value = null
  refundReview.value = null
  refundReviewLoading.value = false
  refundPreviewing.value = false
  reviewedRefundAmount.value = undefined
  refundReviewError.value = ''
  refundWarning.value = ''
}

async function handleSubscriptionGrantBackfill(request: SubscriptionGrantBackfillRequest) {
  const target = refundTarget.value
  if (refundMutationBusy.value || !target) return

  const orderID = target.id
  const session = refundDialogSession
  clearRefundPreviewTimer()
  refundReviewRequest += 1
  refundPreviewing.value = false
  reviewedRefundAmount.value = undefined
  refundBackfilling.value = true
  try {
    const res = await stepUp.run(() => adminPaymentAPI.backfillSubscriptionGrant(orderID, request))
    if (!isCurrentRefundDialog(orderID, session)) return
    refundReview.value = res.data
    reviewedRefundAmount.value = undefined
    refundReviewError.value = ''
    appStore.showSuccess(t('payment.admin.subscriptionGrantBackfillSuccess'))
    void loadOrders()
  } catch (err: unknown) {
    if (isStepUpCancelled(err)) return
    const code = extractApiErrorCode(err)
    if (code === 'SUBSCRIPTION_BACKFILL_AUDIT_STALE' || code === 'REFUND_QUOTE_STALE') {
      if (isCurrentRefundDialog(orderID, session)) {
        appStore.showWarning(t('payment.admin.subscriptionGrantBackfillStale'))
        await loadRefundReview(target, session)
      }
      return
    }
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    refundBackfilling.value = false
  }
}

function isRefundPendingWarning(warning: string | undefined): boolean {
  return /pending|处理中|待/.test(String(warning || '').toLowerCase())
}

function closeRefundDialogFor(orderID: number, session: number) {
  if (isCurrentRefundDialog(orderID, session)) closeRefundDialog()
}

async function handleRefund(data: RefundRequest) {
  const target = refundTarget.value
  const review = refundReview.value
  const hasCurrentSelection = data.refund_amount === undefined || (
    reviewedRefundAmount.value !== undefined &&
    Math.round(reviewedRefundAmount.value * 100) === Math.round(data.refund_amount * 100)
  )
  if (refundMutationBusy.value || refundReviewLoading.value || refundPreviewing.value || !!refundReviewError.value || !hasCurrentSelection || !target || !review || !review.can_refund || review.requires_manual_review || !review.quote_revision) return

  // Capture the exact server quote before the MFA prompt can suspend this
  // operation. A later selection, quote refresh, close/reopen, or response
  // must never redirect the retry to a different order or stale quote.
  const listVersion = listRequest
  const orderID = target.id
  stopRefundRefresh(orderID)
  const session = refundDialogSession
  const request: RefundOrderRequest = {
    quote_revision: review.quote_revision,
    reason_code: data.reason_code,
    ...(data.reason_detail ? { reason_detail: data.reason_detail } : {}),
    ...(data.refund_amount !== undefined ? { refund_amount: data.refund_amount } : {}),
  }
  refundSubmitting.value = true
  try {
    const res = await stepUp.run(() => adminPaymentAPI.refundOrder(orderID, request))
    if (res.data.success) {
      if (res.data.warning) appStore.showWarning(res.data.warning)
      else appStore.showSuccess(t('payment.admin.refundSuccess'))
      closeRefundDialogFor(orderID, session)
      startRefundRefresh(orderID, listVersion)
      return
    }
    if (isRefundPendingWarning(res.data.warning)) {
      appStore.showSuccess(t('payment.admin.refundPending'))
      closeRefundDialogFor(orderID, session)
      startRefundRefresh(orderID, listVersion)
      return
    }
    startRefundRefresh(orderID, listVersion)
    appStore.showError(res.data.warning || t('common.error'))
  } catch (err: unknown) {
    if (isStepUpCancelled(err)) return
    if (extractApiErrorCode(err) === 'REFUND_ALREADY_SETTLED') {
      closeRefundDialogFor(orderID, session)
      startRefundRefresh(orderID, listVersion)
    }
    if (extractApiErrorCode(err) === 'REFUND_QUOTE_STALE') {
      if (isCurrentRefundDialog(orderID, session)) {
        appStore.showWarning(t('payment.admin.refundQuoteStale'))
        await loadRefundReview(target, session, data.refund_amount)
      }
      return
    }
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  }
  finally { refundSubmitting.value = false }
}

async function handleQueryRefund(order: PaymentOrder) {
  if (refundMutationBusy.value) return
  const listVersion = listRequest
  const orderID = order.id
  stopRefundRefresh(orderID)
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
    startRefundRefresh(orderID, listVersion)
  } catch (err: unknown) {
    if (!isStepUpCancelled(err)) appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    const next = new Set(refundQueryingIds.value)
    next.delete(orderID)
    refundQueryingIds.value = next
  }
}

async function handleRetryPausedRefund(order: PaymentOrder) {
  if (refundMutationBusy.value || !order.refund_recovery?.can_retry) return
  const listVersion = listRequest
  const orderID = order.id
  stopRefundRefresh(orderID)
  refundRetryingIds.value = new Set(refundRetryingIds.value).add(orderID)
  try {
    const res = await stepUp.run(() => adminPaymentAPI.retryRefund(orderID))
    if (res.data.success) appStore.showSuccess(t('payment.admin.refundSuccess'))
    else if (/unconfirmed|未确认/.test(String(res.data.warning || '').toLowerCase())) {
      appStore.showWarning(res.data.warning || t('payment.admin.externalRefundUnconfirmed'))
    } else appStore.showSuccess(t('payment.admin.refundRetryQueued'))
    startRefundRefresh(orderID, listVersion)
  } catch (err: unknown) {
    if (!isStepUpCancelled(err)) appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    const next = new Set(refundRetryingIds.value)
    next.delete(orderID)
    refundRetryingIds.value = next
  }
}

function localDateTimeInputValue(date = new Date()): string {
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16)
}

function openExternalRefundDialog(order: PaymentOrder) {
  if (refundMutationBusy.value || !order.refund_recovery?.can_confirm_external) return
  showDetailDialog.value = false
  externalRefundTarget.value = order
  externalRefundForm.method_code = 'wechat_transfer'
  externalRefundForm.external_reference = ''
  externalRefundForm.refunded_at = localDateTimeInputValue()
  externalRefundForm.evidence_detail = ''
}

function closeExternalRefundDialog() {
  if (externalRefundSubmitting.value) return
  externalRefundTarget.value = null
}

function recoveryAmount(order: PaymentOrder): string {
  const recovery = order.refund_recovery
  if (!recovery) return ''
  return `${currencySymbol(recovery.currency)}${(recovery.amount_fen / 100).toFixed(2)}`
}

async function handleConfirmExternalRefund() {
  const target = externalRefundTarget.value
  if (!target || !externalRefundFormValid.value || externalRefundSubmitting.value || !target.refund_recovery?.can_confirm_external) return
  const listVersion = listRequest
  const orderID = target.id
  stopRefundRefresh(orderID)
  const request: ExternalRefundConfirmationRequest = {
    method_code: externalRefundForm.method_code,
    external_reference: externalRefundForm.external_reference.trim(),
    refunded_at: new Date(externalRefundForm.refunded_at).toISOString(),
    evidence_detail: externalRefundForm.evidence_detail.trim(),
  }
  externalRefundSubmitting.value = true
  try {
    const res = await stepUp.run(() => adminPaymentAPI.confirmExternalRefund(orderID, request))
    if (res.data.success) {
      appStore.showSuccess(t('payment.admin.externalRefundSuccess'))
      externalRefundTarget.value = null
    } else {
      appStore.showWarning(res.data.warning || t('payment.admin.externalRefundUnconfirmed'))
    }
    startRefundRefresh(orderID, listVersion)
  } catch (err: unknown) {
    if (!isStepUpCancelled(err)) appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    externalRefundSubmitting.value = false
  }
}

function formatDateTime(dateStr: string): string { return formatOrderDateTime(dateStr) }

onMounted(() => {
  document.addEventListener('visibilitychange', resumeRefundRefreshes)
  void loadOrders()
})
</script>
