<template>
  <BaseDialog :show="show" :title="t('payment.invoice.admin.title')" width="wide" @close="emit('close')">
    <div v-if="order?.invoice" class="space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="min-w-0">
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</p>
          <p class="break-all text-sm font-medium text-gray-900 dark:text-white">{{ order.out_trade_no }}</p>
        </div>
        <InvoiceStatusBadge :status="order.invoice.status" />
      </div>

      <dl class="grid gap-4 rounded-xl bg-gray-50 p-4 sm:grid-cols-2 dark:bg-dark-800">
        <div class="min-w-0"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.title') }}</dt><dd class="mt-1 break-words text-sm text-gray-900 dark:text-white">{{ order.invoice.title }}</dd></div>
        <div class="min-w-0"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.titleType') }}</dt><dd class="mt-1 text-sm text-gray-900 dark:text-white">{{ t(`payment.invoice.${order.invoice.title_type}`) }}</dd></div>
        <div v-if="order.invoice.tax_identifier" class="min-w-0"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.taxIdentifier') }}</dt><dd class="mt-1 break-all text-sm text-gray-900 dark:text-white">{{ order.invoice.tax_identifier }}</dd></div>
        <div class="min-w-0"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.recipientEmail') }}</dt><dd class="mt-1 break-all text-sm text-gray-900 dark:text-white">{{ order.invoice.recipient_email }}</dd></div>
        <div v-if="order.invoice.recipient_phone" class="min-w-0"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.recipientPhone') }}</dt><dd class="mt-1 break-all text-sm text-gray-900 dark:text-white">{{ order.invoice.recipient_phone }}</dd></div>
        <div v-if="order.invoice.remark" class="min-w-0 sm:col-span-2"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.remark') }}</dt><dd class="mt-1 whitespace-pre-wrap break-words text-sm text-gray-900 dark:text-white">{{ order.invoice.remark }}</dd></div>
        <div><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</dt><dd class="mt-1 text-sm font-semibold">{{ currencySymbol(order.invoice.currency) }}{{ order.invoice.amount.toFixed(2) }}</dd></div>
        <div v-if="order.invoice.invoice_number"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.invoiceNumber') }}</dt><dd class="mt-1 break-all text-sm">{{ order.invoice.invoice_number }}</dd></div>
        <div v-if="order.invoice.invoice_item_name"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.itemName') }}</dt><dd class="mt-1 break-words text-sm">{{ order.invoice.invoice_item_name }}</dd></div>
      </dl>

      <div role="note" class="rounded-xl bg-amber-50 p-3 text-sm leading-6 text-amber-900 dark:bg-amber-950/30 dark:text-amber-100">
        {{ t('payment.invoice.admin.complianceNotice') }}
      </div>

      <form v-if="editable" id="admin-invoice-form" class="space-y-4" @submit.prevent="submit">
        <div>
          <label for="admin-invoice-status" class="input-label">{{ t('payment.invoice.admin.targetStatus') }}</label>
          <select id="admin-invoice-status" v-model="form.status" class="input mt-1 w-full">
            <option v-for="status in targetStatuses" :key="status" :value="status">{{ t(`payment.invoice.status.${status.toLowerCase()}`) }}</option>
          </select>
        </div>

        <template v-if="form.status === 'ISSUED'">
          <div><label for="admin-invoice-item" class="input-label">{{ t('payment.invoice.itemName') }}</label><input id="admin-invoice-item" v-model.trim="form.invoice_item_name" class="input mt-1 w-full" maxlength="200" required /></div>
          <div class="grid gap-4 sm:grid-cols-2">
            <div><label for="admin-invoice-code" class="input-label">{{ t('payment.invoice.invoiceCode') }}</label><input id="admin-invoice-code" v-model.trim="form.invoice_code" class="input mt-1 w-full" maxlength="64" /></div>
            <div><label for="admin-invoice-number" class="input-label">{{ t('payment.invoice.invoiceNumber') }}</label><input id="admin-invoice-number" v-model.trim="form.invoice_number" class="input mt-1 w-full" maxlength="64" required /></div>
          </div>
          <div>
            <label for="admin-invoice-pdf" class="input-label">{{ t('payment.invoice.admin.pdfFile') }}</label>
            <input
              id="admin-invoice-pdf"
              type="file"
              accept=".pdf,application/pdf"
              class="mt-1 block w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-700 file:mr-3 file:rounded-md file:border-0 file:bg-primary-50 file:px-3 file:py-1.5 file:text-sm file:font-medium file:text-primary-700 hover:file:bg-primary-100 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200 dark:file:bg-primary-900/30 dark:file:text-primary-300"
              required
              @change="handlePDFSelected"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.admin.pdfFileHint') }}</p>
            <p v-if="pdfError" role="alert" class="mt-1 text-xs text-red-600 dark:text-red-400">{{ pdfError }}</p>
          </div>
        </template>

        <div v-if="form.status === 'REJECTED'">
          <label for="admin-invoice-rejection" class="input-label">{{ t('payment.invoice.admin.rejectionReason') }}</label>
          <textarea id="admin-invoice-rejection" v-model.trim="form.rejection_reason" rows="3" class="input mt-1 w-full" maxlength="500" required />
        </div>

        <p v-if="form.status === 'ISSUED' || form.status === 'REJECTED'" role="note" class="text-xs leading-5 text-gray-500 dark:text-gray-400">
          {{ t('payment.invoice.admin.customerEmailNotice') }}
        </p>
      </form>

      <div v-else class="space-y-3">
        <p v-if="order.invoice.rejection_reason" class="break-words text-sm text-red-700 dark:text-red-300">{{ order.invoice.rejection_reason }}</p>
        <div v-if="order.invoice.status === 'ISSUED' && order.invoice.document_filename" class="rounded-xl bg-gray-50 p-3 dark:bg-dark-800">
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.admin.pdfFile') }}</p>
          <p class="mt-1 break-all text-sm font-medium text-gray-900 dark:text-white">{{ order.invoice.document_filename }}</p>
          <p v-if="order.invoice.document_size_bytes" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ formatBytes(order.invoice.document_size_bytes) }}</p>
        </div>
        <div v-if="order.invoice.status === 'ISSUED' || order.invoice.status === 'REJECTED'" class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-gray-200 p-3 dark:border-dark-600">
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.emailDelivery') }}</p>
            <InvoiceEmailDeliveryBadge :status="order.invoice.email_delivery_status" class="mt-1" />
          </div>
          <button
            v-if="order.invoice.email_retryable ?? (order.invoice.email_delivery_status === 'FAILED')"
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="retrying"
            @click="emit('retry-email')"
          >
            <Icon name="refresh" size="sm" />{{ retrying ? t('common.processing') : t('payment.invoice.admin.retryEmail') }}
          </button>
        </div>
        <p v-if="order.invoice.status === 'ISSUED'" class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('payment.invoice.admin.refundCorrectionWarning') }}</p>
      </div>
    </div>

    <div v-if="order?.invoice?.feishu_notification_status" class="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-gray-200 pt-3 dark:border-dark-600">
      <div><p class="mb-1 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.feishuNotification') }}</p><InvoiceEmailDeliveryBadge :status="order.invoice.feishu_notification_status" /></div>
      <button v-if="order.invoice.feishu_retryable ?? (order.invoice.feishu_notification_status === 'FAILED')" type="button" class="btn btn-secondary btn-sm" :disabled="retrying" @click="emit('retry-feishu')">{{ t('payment.orderOps.retryFeishu') }}</button>
    </div>
    <template #footer>
      <div class="flex w-full flex-wrap justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.close') }}</button>
        <button v-if="editable" type="submit" form="admin-invoice-form" class="btn btn-primary" :disabled="submitting || !formValid">
          {{ submitting ? t('common.processing') : t('payment.invoice.admin.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import InvoiceEmailDeliveryBadge from '@/components/payment/InvoiceEmailDeliveryBadge.vue'
import InvoiceStatusBadge from '@/components/payment/InvoiceStatusBadge.vue'
import { currencySymbol } from '@/components/payment/currency'
import { formatBytes } from '@/utils/format'
import type { AdminUpdateInvoiceRequest, InvoiceStatus, PaymentOrder } from '@/types/payment'

const props = defineProps<{ show: boolean; order: PaymentOrder | null; submitting?: boolean; retrying?: boolean }>()
const emit = defineEmits<{
  (event: 'close'): void
  (event: 'submit', payload: AdminUpdateInvoiceRequest): void
  (event: 'retry-email'): void
  (event: 'retry-feishu'): void
}>()
const { t } = useI18n()

const form = reactive<AdminUpdateInvoiceRequest>({
  status: 'PROCESSING', provider: 'manual', provider_invoice_id: '', invoice_item_name: '',
  invoice_code: '', invoice_number: '', invoice_pdf: undefined, rejection_reason: '',
})
const pdfError = ref('')

const editable = computed(() => ['PENDING', 'PROCESSING'].includes(props.order?.invoice?.status || ''))
const targetStatuses = computed<Exclude<InvoiceStatus, 'PENDING'>[]>(() =>
  props.order?.invoice?.status === 'PROCESSING' ? ['ISSUED', 'REJECTED'] : ['PROCESSING', 'REJECTED']
)
const formValid = computed(() => {
  if (form.status === 'PROCESSING') return true
  if (form.status === 'REJECTED') return Boolean(form.rejection_reason?.trim())
  return Boolean(form.invoice_item_name?.trim() && form.invoice_number?.trim() && form.invoice_pdf && !pdfError.value)
})

function resetForm() {
  const invoice = props.order?.invoice
  form.status = invoice?.status === 'PROCESSING' ? 'ISSUED' : 'PROCESSING'
  form.provider = invoice?.provider || 'manual'
  form.provider_invoice_id = invoice?.provider_invoice_id || ''
  form.invoice_item_name = invoice?.invoice_item_name || ''
  form.invoice_code = invoice?.invoice_code || ''
  form.invoice_number = invoice?.invoice_number || ''
  form.invoice_pdf = undefined
  pdfError.value = ''
  form.rejection_reason = invoice?.rejection_reason || ''
}

function handlePDFSelected(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  pdfError.value = ''
  form.invoice_pdf = undefined
  if (!file) return
  if (!file.name.toLowerCase().endsWith('.pdf') || (file.type && file.type !== 'application/pdf')) {
    pdfError.value = t('payment.invoice.admin.pdfInvalid')
    input.value = ''
    return
  }
  if (file.size > 10 * 1024 * 1024) {
    pdfError.value = t('payment.invoice.admin.pdfTooLarge')
    input.value = ''
    return
  }
  form.invoice_pdf = file
}

function submit() {
  if (!formValid.value || props.submitting) return
  emit('submit', { ...form })
}

watch(() => [props.show, props.order?.id, props.order?.invoice?.status], resetForm, { immediate: true })
</script>
