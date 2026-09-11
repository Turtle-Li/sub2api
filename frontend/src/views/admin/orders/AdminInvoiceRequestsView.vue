<template>
  <AppLayout>
    <div class="space-y-4">
      <section class="card p-4">
        <div class="flex flex-wrap items-center gap-3">
          <div class="min-w-56 flex-1 sm:max-w-80">
            <input
              v-model="search"
              type="search"
              class="input"
              :placeholder="t('payment.invoice.admin.searchApplications')"
              :aria-label="t('payment.invoice.admin.searchApplications')"
              @input="debounceLoad"
            />
          </div>
          <Select
            v-model="status"
            :options="statusOptions"
            class="w-44"
            :aria-label="t('payment.invoice.admin.filterLabel')"
            @change="handleFilterChange"
          />
          <Select v-model="emailStatus" :options="emailStatusOptions" :aria-label="t('payment.orderOps.emailStatus')" class="w-44" @change="handleFilterChange" />
          <div class="ml-auto">
            <button type="button" class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="loadApplications">
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
              <span class="sr-only">{{ t('common.refresh') }}</span>
            </button>
          </div>
        </div>
      </section>

      <DataTable :columns="columns" :data="applications" :loading="loading" row-key="id">
        <template #cell-invoice_id="{ row }">
          <span class="font-mono text-xs">#{{ row.invoice.id }}</span>
        </template>
        <template #cell-order_no="{ row }">
          <div class="max-w-52">
            <p class="truncate font-medium">{{ compactOrderNumber(row.out_trade_no) }}</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">#{{ row.id }}</p>
          </div>
        </template>
        <template #cell-title="{ row }">
          <div class="max-w-56">
            <p class="truncate font-medium">{{ row.invoice.title }}</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t(`payment.invoice.${row.invoice.title_type}`) }}</p>
          </div>
        </template>
        <template #cell-amount="{ row }">
          <span class="font-medium">{{ formatMoney(row.invoice.amount, row.invoice.currency) }}</span>
        </template>
        <template #cell-invoice_status="{ row }">
          <InvoiceStatusBadge :status="row.invoice.status" />
        </template>
        <template #cell-email_status="{ row }">
          <InvoiceEmailDeliveryBadge
            v-if="row.invoice.status === 'ISSUED' || row.invoice.status === 'REJECTED'"
            :status="row.invoice.email_delivery_status"
          />
          <span v-else class="text-gray-400">—</span>
        </template>
        <template #cell-feishu="{ row }">
          <InvoiceEmailDeliveryBadge v-if="row.invoice.feishu_notification_status" :status="row.invoice.feishu_notification_status" />
          <span v-else>—</span>
        </template>
        <template #cell-requested_at="{ row }">
          <span class="text-xs text-gray-600 dark:text-gray-300">{{ formatDateTime(row.invoice.requested_at) }}</span>
        </template>
        <template #cell-actions="{ row }">
          <button
            type="button"
            class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/20"
            @click="invoiceTarget = row"
          >
            <Icon name="document" size="sm" />
            {{ row.invoice.status === 'ISSUED' || row.invoice.status === 'REJECTED' ? t('payment.invoice.admin.view') : t('payment.invoice.admin.process') }}
          </button>
        </template>
        <template #empty>
          <div class="flex flex-col items-center py-6">
            <Icon name="document" size="xl" class="mb-3 text-gray-400" />
            <p class="font-medium text-gray-800 dark:text-gray-200">{{ t('payment.invoice.admin.emptyApplications') }}</p>
          </div>
        </template>
      </DataTable>

      <Pagination
        v-if="pagination.total > 0"
        :page="pagination.page"
        :total="pagination.total"
        :page-size="pagination.page_size"
        @update:page="handlePageChange"
        @update:pageSize="handlePageSizeChange"
      />
    </div>

    <AdminInvoiceDialog
      :show="!!invoiceTarget"
      :order="invoiceTarget"
      :submitting="submitting"
      :retrying="retrying"
      @submit="handleUpdate"
      @retry-email="handleRetryEmail" @retry-feishu="handleRetryFeishu"
      @close="invoiceTarget = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { adminPaymentAPI } from '@/api/admin/payment'
import AdminInvoiceDialog from '@/components/admin/payment/AdminInvoiceDialog.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import InvoiceEmailDeliveryBadge from '@/components/payment/InvoiceEmailDeliveryBadge.vue'
import InvoiceStatusBadge from '@/components/payment/InvoiceStatusBadge.vue'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import { compactOrderNumber } from '@/components/payment/orderPresentation'
import { useAppStore } from '@/stores/app'
import type { Column } from '@/components/common/types'
import type { AdminUpdateInvoiceRequest, PaymentOrder } from '@/types/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'

const { t, locale } = useI18n()
const route = useRoute()
const appStore = useAppStore()

const routeStatus = typeof route.query.invoice_status === 'string' ? route.query.invoice_status.toUpperCase() : ''
const status = ref(['PENDING', 'PROCESSING', 'ISSUED', 'REJECTED'].includes(routeStatus) ? routeStatus : 'HAS_INVOICE')
const search = ref('')
const emailStatus = ref('')
const emailStatusOptions = computed(() => [
  { value: '', label: t('payment.orderOps.allEmails') },
  ...['FAILED', 'PENDING', 'SENDING', 'SENT', 'NOT_SENT'].map(value => ({ value, label: t(`payment.invoice.emailStatus.${value.toLowerCase()}`) })),
])
const loading = ref(false)
const submitting = ref(false)
const retrying = ref(false)
const applications = ref<PaymentOrder[]>([])
const invoiceTarget = ref<PaymentOrder | null>(null)
const pagination = reactive({ page: 1, page_size: 20, total: 0 })
let debounceTimer: ReturnType<typeof setTimeout> | null = null

const columns = computed<Column[]>(() => [
  { key: 'invoice_id', label: t('payment.invoice.admin.applicationId') },
  { key: 'order_no', label: t('payment.orders.orderNo') },
  { key: 'title', label: t('payment.invoice.title') },
  { key: 'amount', label: t('payment.orders.payAmount') },
  { key: 'invoice_status', label: t('payment.invoice.currentStatus') },
  { key: 'email_status', label: t('payment.invoice.emailDelivery') },
  { key: 'feishu', label: t('payment.orderOps.feishuNotification') },
  { key: 'requested_at', label: t('payment.invoice.admin.requestedAt') },
  { key: 'actions', label: t('payment.orders.actions') },
])

const statusOptions = computed(() => [
  { value: 'HAS_INVOICE', label: t('payment.invoice.admin.allApplications') },
  { value: 'PENDING', label: t('payment.invoice.status.pending') },
  { value: 'PROCESSING', label: t('payment.invoice.status.processing') },
  { value: 'ISSUED', label: t('payment.invoice.status.issued') },
  { value: 'REJECTED', label: t('payment.invoice.status.rejected') },
])

onUnmounted(() => { if (debounceTimer) clearTimeout(debounceTimer) })
let listRequest = 0
async function loadApplications() {
  const request = ++listRequest
  loading.value = true
  try {
    const res = await adminPaymentAPI.getOrders({
      page: pagination.page,
      page_size: pagination.page_size,
      keyword: search.value.trim() || undefined,
      invoice_status: status.value,
      invoice_email_status: emailStatus.value || undefined,
    })
    if (request !== listRequest) return
    applications.value = (res.data.items || []).filter((order) => Boolean(order.invoice))
    pagination.total = res.data.total || 0
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    if (request === listRequest) loading.value = false
  }
}

function debounceLoad() {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    pagination.page = 1
    loadApplications()
  }, 300)
}

function handleFilterChange() {
  pagination.page = 1
  loadApplications()
}

function handlePageChange(page: number) {
  pagination.page = page
  loadApplications()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page_size = pageSize
  pagination.page = 1
  loadApplications()
}

async function handleUpdate(payload: AdminUpdateInvoiceRequest) {
  const target = invoiceTarget.value
  if (!target || submitting.value || retrying.value) return
  submitting.value = true
  try {
    await adminPaymentAPI.updateInvoiceRequest(target.id, payload)
    if (invoiceTarget.value === target) invoiceTarget.value = null
    appStore.showSuccess(t('payment.invoice.admin.updated'))
    await loadApplications()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    submitting.value = false
  }
}

async function handleRetryFeishu() {
  const target = invoiceTarget.value
  if (!target || retrying.value || submitting.value) return
  retrying.value = true
  try {
    const res = await adminPaymentAPI.retryInvoiceFeishu(target.id)
    if (invoiceTarget.value === target) invoiceTarget.value = { ...target, invoice: res.data }
    appStore.showSuccess(t('payment.orderOps.notificationQueued'))
    await loadApplications()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally { retrying.value = false }
}

async function handleRetryEmail() {
  const target = invoiceTarget.value
  if (!target || retrying.value || submitting.value) return
  retrying.value = true
  try {
    const res = await adminPaymentAPI.retryInvoiceEmail(target.id)
    if (invoiceTarget.value === target) invoiceTarget.value = { ...target, invoice: res.data }
    appStore.showSuccess(t('payment.invoice.admin.emailRetryQueued'))
    await loadApplications()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    retrying.value = false
  }
}

function formatMoney(amount: number, currency?: string): string {
  const normalized = (currency || 'CNY').toUpperCase()
  try {
    return new Intl.NumberFormat(locale.value, { style: 'currency', currency: normalized }).format(amount)
  } catch {
    return `${normalized} ${amount.toFixed(2)}`
  }
}

const formatDateTime = formatOrderDateTime

onMounted(loadApplications)
</script>
