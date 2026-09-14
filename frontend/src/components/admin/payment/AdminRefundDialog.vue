<template>
  <BaseDialog
    :show="show"
    :title="t('payment.admin.refundOrder')"
    width="normal"
    @close="emit('cancel')"
  >
    <form id="refund-form" class="space-y-4" @submit.prevent="handleSubmit">
      <div
        v-if="order?.refund_requested_at || order?.refund_request_reason"
        class="rounded-lg border border-violet-200 bg-violet-50 p-3 dark:border-violet-800 dark:bg-violet-900/20"
      >
        <div class="flex items-center gap-2 text-sm font-medium text-violet-700 dark:text-violet-300">
          <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" aria-hidden="true">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          {{ t('payment.admin.refundRequestInfo') }}
        </div>
        <div v-if="order?.refund_requested_at" class="mt-2 flex justify-between gap-3 text-sm">
          <span class="text-violet-600 dark:text-violet-400">{{ t('payment.admin.refundRequestedAt') }}</span>
          <span class="text-right text-violet-800 dark:text-violet-200">{{ formatDateTime(order.refund_requested_at) }}</span>
        </div>
        <div v-if="order?.refund_request_reason" class="mt-1 text-sm">
          <span class="text-violet-600 dark:text-violet-400">{{ t('payment.admin.refundRequestReason') }}:</span>
          <span class="ml-1 text-violet-800 dark:text-violet-200">{{ order.refund_request_reason }}</span>
        </div>
      </div>

      <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-700">
        <div class="flex justify-between gap-3 text-sm">
          <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
          <span class="font-mono text-gray-900 dark:text-white">#{{ order?.id }}</span>
        </div>
        <div class="mt-1 flex justify-between gap-3 text-sm">
          <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</span>
          <span class="font-medium text-gray-900 dark:text-white">{{ formatCash(order?.pay_amount, order?.currency) }}</span>
        </div>
      </div>

      <div
        v-if="loading"
        role="status"
        class="rounded-lg bg-gray-50 p-4 text-sm text-gray-600 dark:bg-dark-700 dark:text-gray-300"
      >
        {{ t('payment.admin.refundReviewLoading') }}
      </div>

      <div
        v-else-if="error"
        role="alert"
        class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300"
      >
        {{ error }}
      </div>

      <template v-else-if="review">
        <section
          v-if="review.balance"
          class="rounded-lg border border-gray-200 p-3 dark:border-dark-600"
          :aria-label="t('payment.admin.balanceRefundImpact')"
        >
          <h3 class="text-sm font-medium text-gray-900 dark:text-white">{{ t('payment.admin.balanceRefundImpact') }}</h3>
          <dl class="mt-3 grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.paidPrincipalRefundable') }}</dt>
              <dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ formatCredit(review.balance.paid_credit_to_reclaim) }}</dd>
            </div>
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.giftedCreditRecovery') }}</dt>
              <dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ formatCredit(review.balance.gift_credit_to_reclaim) }}</dd>
            </div>
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.currentAvailableCredit') }}</dt>
              <dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ formatCredit(review.balance.available_balance) }}</dd>
            </div>
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundCash') }}</dt>
              <dd class="mt-1 font-medium text-red-600 dark:text-red-400">{{ formatCash(review.default_refund_amount, review.currency) }}</dd>
            </div>
          </dl>
        </section>

        <section
          v-else-if="review.subscription"
          class="rounded-lg border border-gray-200 p-3 dark:border-dark-600"
          :aria-label="t('payment.admin.subscriptionRefundImpact')"
        >
          <h3 class="text-sm font-medium text-gray-900 dark:text-white">{{ t('payment.admin.subscriptionRefundImpact') }}</h3>
          <dl class="mt-3 grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.usedTime') }}</dt>
              <dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ formatDuration(review.subscription.used_seconds) }}</dd>
            </div>
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.remainingTime') }}</dt>
              <dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ formatDuration(review.subscription.remaining_seconds) }}</dd>
            </div>
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.proratedRefund') }}</dt>
              <dd class="mt-1 font-medium text-red-600 dark:text-red-400">{{ formatCash(review.default_refund_amount, review.currency) }}</dd>
            </div>
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.newExpiry') }}</dt>
              <dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ formatDateTime(review.subscription.new_expires_at) }}</dd>
            </div>
          </dl>
        </section>

        <section
          v-else-if="review.requires_manual_review"
          class="rounded-lg border border-amber-200 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-900/20"
        >
          <div class="flex justify-between gap-3 text-sm">
            <span class="text-amber-800 dark:text-amber-200">{{ t('payment.admin.refundCash') }}</span>
            <span class="font-medium text-amber-900 dark:text-amber-100">{{ t('payment.admin.refundAmountPendingManualReview') }}</span>
          </div>
        </section>

        <section v-else class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <div class="flex justify-between gap-3 text-sm">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundCash') }}</span>
            <span class="font-medium text-red-600 dark:text-red-400">{{ formatCash(review.default_refund_amount, review.currency) }}</span>
          </div>
        </section>

        <div
          v-if="review.requires_manual_review"
          role="alert"
          class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200"
        >
          <p class="font-medium">{{ t('payment.admin.refundManualReviewRequired') }}</p>
          <p v-if="reviewReason" class="mt-1">{{ reviewReason }}</p>
        </div>
        <div
          v-else-if="!review.can_refund"
          role="alert"
          class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200"
        >
          <p class="font-medium">{{ t('payment.admin.refundUnavailable') }}</p>
          <p v-if="reviewReason" class="mt-1">{{ reviewReason }}</p>
        </div>
      </template>

      <div class="space-y-3">
        <div>
          <label for="refund-reason-code" class="input-label">{{ t('payment.admin.refundReasonCode') }}</label>
          <select
            id="refund-reason-code"
            v-model="form.reasonCode"
            class="input"
            :disabled="loading || submitting"
          >
            <option v-for="code in refundReasonCodes" :key="code" :value="code">
              {{ t(`payment.admin.refundReasonCodes.${code}`) }}
            </option>
          </select>
        </div>
        <div>
          <label for="refund-reason-detail" class="input-label">
            {{ form.reasonCode === 'other' ? t('payment.admin.refundReasonDetailRequired') : t('payment.admin.refundReasonDetail') }}
          </label>
          <textarea
            id="refund-reason-detail"
            v-model="form.reasonDetail"
            rows="3"
            class="input"
            :placeholder="t('payment.admin.refundReasonDetailPlaceholder')"
            :disabled="loading || submitting"
            :required="form.reasonCode === 'other'"
            maxlength="240"
          ></textarea>
          <p v-if="form.reasonCode === 'other'" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('payment.admin.refundReasonDetailOtherHint') }}
          </p>
        </div>
      </div>

      <div
        v-if="warning"
        class="rounded-lg bg-yellow-50 p-3 text-sm text-yellow-700 dark:bg-yellow-900/20 dark:text-yellow-300"
      >
        {{ warning }}
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="emit('cancel')">
          {{ t('common.cancel') }}
        </button>
        <button
          type="submit"
          form="refund-form"
          :disabled="!canConfirm"
          class="rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 focus:outline-none focus:ring-2 focus:ring-red-500 focus:ring-offset-2 disabled:opacity-50 dark:focus:ring-offset-dark-800"
        >
          {{ submitting ? t('common.processing') : t('payment.admin.confirmRefund') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { RefundReasonCode, RefundReview } from '@/api/admin/payment'
import type { PaymentOrder } from '@/types/payment'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import { currencySymbol } from '@/components/payment/currency'

const { t } = useI18n()

const props = defineProps<{
  show: boolean
  order: PaymentOrder | null
  review: RefundReview | null
  loading?: boolean
  error?: string
  submitting?: boolean
  warning?: string
}>()

const emit = defineEmits<{
  (e: 'confirm', data: { reason_code: RefundReasonCode; reason_detail?: string }): void
  (e: 'cancel'): void
}>()

const creditedAmountSymbol = currencySymbol('USD')
const refundReasonCodes: RefundReasonCode[] = [
  'customer_request',
  'duplicate_charge',
  'service_not_delivered',
  'service_error',
  'other',
]
const form = reactive<{ reasonCode: RefundReasonCode; reasonDetail: string }>({
  reasonCode: 'customer_request',
  reasonDetail: '',
})

const reviewReason = computed(() => {
  const review = props.review
  if (!review) return ''
  if (review.reason_code) {
    const key = `payment.admin.refundReviewReasons.${review.reason_code}`
    const translated = t(key)
    if (translated !== key) return translated
  }
  return review.reason || ''
})

const canConfirm = computed(() => {
  const review = props.review
  return Boolean(
    props.show &&
    !props.loading &&
    !props.error &&
    !props.submitting &&
    review?.can_refund &&
    !review.requires_manual_review &&
    review.quote_revision &&
    (form.reasonCode !== 'other' || form.reasonDetail.trim()),
  )
})

function resetReason() {
  form.reasonCode = 'customer_request'
  form.reasonDetail = props.order?.refund_request_reason || ''
}

watch(() => props.show, (show) => {
  if (show) resetReason()
})

watch(() => props.order?.id, () => {
  if (props.show) resetReason()
})

function formatCredit(value: number | undefined): string {
  const amount = Number.isFinite(value) ? Number(value) : 0
  return `${creditedAmountSymbol}${amount.toFixed(2)}`
}

function formatCash(value: number | undefined, currency: string | undefined): string {
  const amount = Number.isFinite(value) ? Number(value) : 0
  return `${currencySymbol(currency)}${amount.toFixed(2)}`
}

function formatDuration(value: number | undefined): string {
  let seconds = Math.max(0, Math.floor(Number.isFinite(value) ? Number(value) : 0))
  if (seconds === 0) return t('payment.admin.refundTimeZero')

  const units: Array<[number, string]> = [
    [86_400, 'payment.admin.refundTimeDays'],
    [3_600, 'payment.admin.refundTimeHours'],
    [60, 'payment.admin.refundTimeMinutes'],
    [1, 'payment.admin.refundTimeSeconds'],
  ]
  const parts: string[] = []
  for (const [size, key] of units) {
    const count = Math.floor(seconds / size)
    if (count > 0) {
      parts.push(t(key, { count }))
      seconds -= count * size
    }
  }
  return parts.join(' ')
}

function formatDateTime(dateStr: string): string {
  return formatOrderDateTime(dateStr)
}

function handleSubmit() {
  if (!canConfirm.value) return
  const detail = form.reasonDetail.trim()
  emit('confirm', {
    reason_code: form.reasonCode,
    ...(detail ? { reason_detail: detail } : {}),
  })
}
</script>
