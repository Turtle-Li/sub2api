<template>
  <AppLayout>
    <div class="space-y-4">
      <!-- Filters -->
      <div class="card p-4">
        <div class="flex flex-wrap items-center gap-3">
          <Select :aria-label="t('payment.orders.status')" v-model="currentFilter" :options="statusFilters" class="w-36" @change="handleFilterChange" />
          <Select v-model="fulfillmentFilter" :options="fulfillmentOptions" :aria-label="t('payment.orderOps.fulfillmentLabel')" class="w-44" @change="handleFilterChange" />
          <Select v-model="invoiceFilter" :options="invoiceOptions" :aria-label="t('payment.invoice.currentStatus')" class="w-44" @change="handleFilterChange" />
          <div class="flex flex-1 items-center justify-end gap-2">
            <button @click="fetchOrders" :disabled="loading" class="btn btn-secondary" :title="t('common.refresh')">
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button v-if="appStore.cachedPublicSettings?.payment_enabled" class="btn btn-primary" @click="router.push('/purchase')">{{ t('payment.result.backToRecharge') }}</button>
          </div>
        </div>
      </div>

      <!-- Table -->
      <OrderTable :orders="orders" :loading="loading">
        <template #actions="{ row }">
          <div class="flex flex-wrap items-center gap-2">
            <button type="button" class="btn btn-secondary btn-sm" @click="openDetails(row)">{{ t('common.view') }}</button>
            <button v-if="row.status === 'PENDING'" @click="handleCancel(row.id)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-yellow-600 hover:bg-yellow-50 dark:text-yellow-400 dark:hover:bg-yellow-900/20">
              <Icon name="x" size="sm" />
              <span>{{ t('payment.orders.cancel') }}</span>
            </button>
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
      <div v-if="detailOrder" class="space-y-4">
        <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }} · #{{ detailOrder.id }}</p><p class="break-all text-sm">{{ detailOrder.out_trade_no }}</p><button type="button" class="mt-2 text-sm text-primary-700 dark:text-primary-300" @click="copyOrderNumber(detailOrder.out_trade_no)">{{ t('payment.orderOps.copyOrder') }}</button></div>
        <div class="flex flex-wrap gap-2"><OrderLifecycleBadge kind="payment" :value="paymentFact(detailOrder)" /><OrderLifecycleBadge kind="fulfillment" :value="fulfillmentFact(detailOrder)" /></div>
        <dl class="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
          <div><dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</dt><dd class="font-semibold">{{ currencySymbol(detailOrder.currency) }}{{ detailOrder.pay_amount.toFixed(2) }}</dd></div>
          <div><dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.createdAt') }}</dt><dd>{{ formatOrderDateTime(detailOrder.created_at) }}</dd></div>
          <div v-if="detailOrder.paid_at"><dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.paidAt') }}</dt><dd>{{ formatOrderDateTime(detailOrder.paid_at) }}</dd></div>
          <div v-if="detailOrder.completed_at"><dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.fulfillment.fulfilled') }}</dt><dd>{{ formatOrderDateTime(detailOrder.completed_at) }}</dd></div>
        </dl>
        <OrderPurchaseSnapshot :order="detailOrder" />
      </div>
    </BaseDialog>
    <!-- Cancel Confirm Dialog -->
    <BaseDialog :show="!!cancelTargetId" :title="t('payment.orders.cancel')" width="narrow" @close="cancelTargetId = null">
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('payment.confirmCancel') }}</p>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button class="btn btn-secondary" @click="cancelTargetId = null">{{ t('common.cancel') }}</button>
          <button class="btn btn-danger" :disabled="actionLoading" @click="confirmCancel">{{ actionLoading ? t('common.processing') : t('payment.orders.cancel') }}</button>
        </div>
      </template>
    </BaseDialog>

    <!-- Refund Dialog -->
    <BaseDialog :show="!!refundTarget" :title="t('payment.orders.requestRefund')" @close="refundTarget = null">
      <div v-if="refundTarget" class="space-y-4">
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
          <div class="flex justify-between text-sm">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
            <span class="font-mono text-gray-900 dark:text-white">#{{ refundTarget.id }}</span>
          </div>
          <div class="mt-2 flex justify-between text-sm">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.amount') }}</span>
            <span class="text-gray-900 dark:text-white">${{ refundTarget.amount.toFixed(2) }}</span>
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('payment.refundReason') }}</label>
          <textarea v-model="refundReason" rows="3" class="input mt-1 w-full" :placeholder="t('payment.refundReasonPlaceholder')" />
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button class="btn btn-secondary" @click="refundTarget = null">{{ t('common.cancel') }}</button>
          <button class="btn btn-primary" :disabled="actionLoading || !refundReason.trim()" @click="confirmRefund">{{ actionLoading ? t('common.processing') : t('payment.orders.requestRefund') }}</button>
        </div>
      </template>
    </BaseDialog>

    <InvoiceRequestDialog
      :show="!!invoiceTarget"
      :order="invoiceTarget"
      :submitting="invoiceSubmitting"
      @close="invoiceTarget = null"
      @submit="submitInvoiceRequest"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores'
import { paymentAPI } from '@/api/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import type { CreateInvoiceRequest, PaymentOrder } from '@/types/payment'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import OrderTable from '@/components/payment/OrderTable.vue'
import InvoiceRequestDialog from '@/components/payment/InvoiceRequestDialog.vue'
import OrderPurchaseSnapshot from '@/components/payment/OrderPurchaseSnapshot.vue'
import OrderLifecycleBadge from '@/components/payment/OrderLifecycleBadge.vue'
import { fulfillmentFact, paymentFact } from '@/components/payment/orderPresentation'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import { currencySymbol } from '@/components/payment/currency'

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()

const loading = ref(false)
const actionLoading = ref(false)
const orders = ref<PaymentOrder[]>([])
const refundEligibleProviders = ref<Set<string>>(new Set())
const currentFilter = ref('')
const fulfillmentFilter = ref('')
const invoiceFilter = ref('')
const detailOrder = ref<PaymentOrder | null>(null)
const fulfillmentOptions = computed(() => [
  { value: '', label: t('payment.orderOps.allFulfillments') },
  ...['PENDING', 'FAILED', 'MANUAL_REVIEW', 'FULFILLED', 'NOT_STARTED'].map(value => ({ value, label: t(`payment.orderOps.fulfillment.${value.toLowerCase()}`) })),
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
const cancelTargetId = ref<number | null>(null)
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
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    if (request === listRequest) loading.value = false
  }
}

function handlePageChange(page: number) { pagination.page = page; fetchOrders() }
function handlePageSizeChange(size: number) { pagination.page_size = size; pagination.page = 1; fetchOrders() }

function handleCancel(orderId: number) { cancelTargetId.value = orderId }

async function confirmCancel() {
  if (!cancelTargetId.value) return
  actionLoading.value = true
  try {
    await paymentAPI.cancelOrder(cancelTargetId.value)
    appStore.showSuccess(t('common.success'))
    cancelTargetId.value = null
    await fetchOrders()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    actionLoading.value = false
  }
}

function openRefundDialog(order: PaymentOrder) { refundTarget.value = order; refundReason.value = '' }

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
  if (order.status !== 'COMPLETED') return false
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
  return order.invoice_eligible ?? (order.status === 'COMPLETED' && !order.needs_manual_review && order.refund_amount === 0 && order.pay_amount > 0)
}

function openInvoiceDialog(order: PaymentOrder) {
  invoiceTarget.value = order
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

onMounted(() => { fetchOrders(); loadRefundEligibility() })
</script>
