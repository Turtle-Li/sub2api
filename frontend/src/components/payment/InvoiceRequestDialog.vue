<template>
  <BaseDialog :show="show" :title="dialogTitle" width="normal" @close="emit('close')">
    <div v-if="order" class="space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
        <div class="min-w-0">
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</p>
          <p class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ order.out_trade_no }}</p>
        </div>
        <p class="text-sm font-semibold text-gray-900 dark:text-white">{{ formatMoney(order.pay_amount, order.currency) }}</p>
      </div>

      <div v-if="invoice" class="space-y-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.currentStatus') }}</p>
            <InvoiceStatusBadge :status="invoice.status" class="mt-1" />
          </div>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.revision', { revision: invoice.revision }) }}</p>
        </div>

        <div v-if="invoice.status === 'REJECTED'" role="alert" class="rounded-xl bg-red-50 p-3 text-sm text-red-800 dark:bg-red-950/30 dark:text-red-200">
          <p class="font-medium">{{ t('payment.invoice.rejectedTitle') }}</p>
          <p class="mt-1 break-words">{{ invoice.rejection_reason }}</p>
          <p class="mt-2 text-xs">{{ t('payment.invoice.correctAndResubmit') }}</p>
        </div>

        <dl v-if="!editable" class="grid gap-4 sm:grid-cols-2">
          <div class="min-w-0"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.title') }}</dt><dd class="mt-1 break-words text-sm text-gray-900 dark:text-white">{{ invoice.title }}</dd></div>
          <div class="min-w-0"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.recipientEmail') }}</dt><dd class="mt-1 break-all text-sm text-gray-900 dark:text-white">{{ invoice.recipient_email }}</dd></div>
          <div v-if="invoice.tax_identifier" class="min-w-0"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.taxIdentifier') }}</dt><dd class="mt-1 break-all text-sm text-gray-900 dark:text-white">{{ invoice.tax_identifier }}</dd></div>
          <div v-if="invoice.invoice_item_name" class="min-w-0"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.itemName') }}</dt><dd class="mt-1 break-words text-sm text-gray-900 dark:text-white">{{ invoice.invoice_item_name }}</dd></div>
          <div v-if="invoice.invoice_number" class="min-w-0"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.invoiceNumber') }}</dt><dd class="mt-1 break-all text-sm text-gray-900 dark:text-white">{{ invoice.invoice_number }}</dd></div>
        </dl>

        <div v-if="invoice.status === 'ISSUED'" role="status" class="rounded-xl border border-green-200 bg-green-50 p-3 text-sm text-green-800 dark:border-green-900/60 dark:bg-green-950/30 dark:text-green-200">
          <p class="font-medium">{{ t('payment.invoice.pdfEmailTitle') }}</p>
          <p class="mt-1">{{ t('payment.invoice.pdfEmailHint', { email: invoice.recipient_email }) }}</p>
          <p v-if="invoice.document_filename" class="mt-1 break-all text-xs">{{ invoice.document_filename }}</p>
          <div class="mt-2"><InvoiceEmailDeliveryBadge :status="invoice.email_delivery_status" /></div>
        </div>
      </div>

      <form v-if="editable" id="invoice-request-form" class="space-y-4" @submit.prevent="submit">
        <div>
          <label for="invoice-title-type" class="input-label">{{ t('payment.invoice.titleType') }}</label>
          <select id="invoice-title-type" v-model="form.title_type" class="input mt-1 w-full">
            <option value="personal">{{ t('payment.invoice.personal') }}</option>
            <option value="enterprise">{{ t('payment.invoice.enterprise') }}</option>
          </select>
        </div>
        <div>
          <label for="invoice-title" class="input-label">{{ t('payment.invoice.title') }}</label>
          <input id="invoice-title" v-model.trim="form.title" class="input mt-1 w-full" maxlength="200" required autocomplete="organization" />
        </div>
        <div v-if="form.title_type === 'enterprise'">
          <label for="invoice-tax-id" class="input-label">{{ t('payment.invoice.taxIdentifier') }}</label>
          <input id="invoice-tax-id" v-model.trim="form.tax_identifier" class="input mt-1 w-full uppercase" maxlength="64" required autocomplete="off" />
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.taxIdentifierHint') }}</p>
        </div>
        <div>
          <label for="invoice-email" class="input-label">{{ t('payment.invoice.recipientEmail') }}</label>
          <input id="invoice-email" v-model.trim="form.recipient_email" type="email" class="input mt-1 w-full" maxlength="255" required autocomplete="email" />
        </div>
        <div>
          <label for="invoice-phone" class="input-label">{{ t('payment.invoice.recipientPhone') }}</label>
          <input id="invoice-phone" v-model.trim="form.recipient_phone" type="tel" class="input mt-1 w-full" maxlength="32" autocomplete="tel" />
        </div>
        <div>
          <label for="invoice-remark" class="input-label">{{ t('payment.invoice.remark') }}</label>
          <textarea id="invoice-remark" v-model.trim="form.remark" rows="3" class="input mt-1 w-full" maxlength="1000" :placeholder="t('payment.invoice.remarkPlaceholder')" />
        </div>
        <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('payment.invoice.legalNotice') }}</p>
      </form>
    </div>

    <template #footer>
      <div class="flex w-full flex-wrap justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.close') }}</button>
        <button v-if="editable" type="submit" form="invoice-request-form" class="btn btn-primary" :disabled="submitting || !formValid">
          {{ submitting ? t('common.processing') : submitLabel }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import InvoiceEmailDeliveryBadge from '@/components/payment/InvoiceEmailDeliveryBadge.vue'
import InvoiceStatusBadge from '@/components/payment/InvoiceStatusBadge.vue'
import type { CreateInvoiceRequest, PaymentOrder } from '@/types/payment'

const props = defineProps<{ show: boolean; order: PaymentOrder | null; submitting?: boolean }>()
const emit = defineEmits<{ (event: 'close'): void; (event: 'submit', payload: CreateInvoiceRequest): void }>()
const { t, locale } = useI18n()

const form = reactive<CreateInvoiceRequest>({
  title_type: 'personal',
  title: '',
  tax_identifier: '',
  recipient_email: '',
  recipient_phone: '',
  remark: '',
})

const invoice = computed(() => props.order?.invoice)
const orderEligible = computed(() => Boolean(
  props.order?.invoice_eligible ?? (
    props.order?.status === 'COMPLETED' &&
    !props.order?.needs_manual_review &&
    props.order.refund_amount === 0 &&
    (props.order.refund_requested_amount ?? 0) === 0 &&
    props.order.pay_amount > 0
  )
))
const editable = computed(() => orderEligible.value && (!invoice.value || invoice.value.status === 'REJECTED'))
const dialogTitle = computed(() => editable.value ? t('payment.invoice.requestTitle') : t('payment.invoice.detailTitle'))
const submitLabel = computed(() => invoice.value?.status === 'REJECTED' ? t('payment.invoice.resubmit') : t('payment.invoice.submit'))
const formValid = computed(() => Boolean(
  form.title.trim() &&
  form.recipient_email.trim() &&
  (form.title_type === 'personal' || form.tax_identifier?.trim())
))

function resetForm() {
  const current = props.order?.invoice
  form.title_type = current?.title_type || 'personal'
  form.title = current?.title || ''
  form.tax_identifier = current?.tax_identifier || ''
  form.recipient_email = current?.recipient_email || ''
  form.recipient_phone = current?.recipient_phone || ''
  form.remark = current?.remark || ''
}

function submit() {
  if (!formValid.value || props.submitting) return
  emit('submit', {
    ...form,
    tax_identifier: form.title_type === 'enterprise' ? form.tax_identifier?.trim() : undefined,
  })
}

function formatMoney(amount: number, currency?: string): string {
  const normalizedCurrency = (currency || 'CNY').toUpperCase()
  try {
    return new Intl.NumberFormat(locale.value, { style: 'currency', currency: normalizedCurrency }).format(amount)
  } catch {
    return `${normalizedCurrency} ${amount.toFixed(2)}`
  }
}

watch(() => [props.show, props.order?.id, props.order?.invoice?.revision], resetForm, { immediate: true })
</script>
