<template>
  <BaseDialog
    :show="show"
    :title="t('payment.admin.refundOrder')"
    width="normal"
    :close-on-click-outside="!submitting && !backfilling"
    :close-on-escape="!submitting && !backfilling"
    :show-close-button="!submitting && !backfilling"
    @close="requestCancel"
  >
    <form id="refund-form" class="space-y-4" @submit.prevent="handleSubmit">
      <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-700">
        <div class="flex justify-between gap-3 text-sm">
          <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
          <span class="font-mono text-gray-900 dark:text-white">#{{ order?.id }}</span>
        </div>
        <div class="mt-1 flex justify-between gap-3 text-sm">
          <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</span>
          <span class="font-medium text-gray-900 dark:text-white">{{ formatCash(order?.pay_amount, order?.currency) }}</span>
        </div>
        <div v-if="order?.refund_request_reason" class="mt-2 border-t border-gray-200 pt-2 text-sm dark:border-dark-600">
          <span class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundRequestReason') }}:</span>
          <span class="ml-1 text-gray-900 dark:text-white">{{ order.refund_request_reason }}</span>
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
        v-else-if="error && !review"
        role="alert"
        class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300"
      >
        {{ error }}
      </div>

      <template v-if="review">
        <div
          v-if="error"
          role="alert"
          class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300"
        >
          {{ error }}
        </div>
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
          v-else-if="review.subscription && review.can_refund && !review.requires_manual_review && review.quote_revision"
          class="space-y-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600"
          :aria-label="t('payment.admin.subscriptionRefundImpact')"
        >
          <h3 class="text-sm font-medium text-gray-900 dark:text-white">{{ t('payment.admin.subscriptionRefundImpact') }}</h3>
          <div>
            <label for="refund-amount" class="input-label">{{ t('payment.admin.refundAmount') }}</label>
            <input
              id="refund-amount"
              :value="refundAmount"
              name="refund_amount"
              inputmode="decimal"
              autocomplete="off"
              class="input mt-1"
              :aria-invalid="Boolean(refundAmountError)"
              :aria-describedby="refundAmountError ? 'refund-amount-error' : 'refund-amount-range'"
              :disabled="submitting || backfilling"
              @input="setRefundAmount"
            />
            <p id="refund-amount-range" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('payment.admin.refundAmountMaximum', { amount: formatCash(review.max_refund_amount, review.currency) }) }}
              <template v-if="minimumRefundAmount > 0.01"> · {{ t('payment.admin.refundAmountMinimum', { amount: formatCash(minimumRefundAmount, review.currency) }) }}</template>
            </p>
            <p v-if="refundAmountError" id="refund-amount-error" role="alert" class="mt-1 text-xs text-red-700 dark:text-red-300">
              {{ refundAmountError }}
            </p>
            <p v-else-if="previewing" role="status" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('payment.admin.refundAmountPreviewing') }}
            </p>
          </div>
          <dl class="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.usedTime') }}</dt>
              <dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ formatDuration(review.subscription.used_seconds) }}</dd>
            </div>
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.remainingTime') }}</dt>
              <dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ formatDuration(review.subscription.remaining_seconds) }}</dd>
            </div>
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundEntitlementTime') }}</dt>
              <dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ formatDuration(review.subscription.seconds_to_reclaim) }}</dd>
            </div>
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.newExpiry') }}</dt>
              <dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ formatDateTime(review.subscription.new_expires_at) }}</dd>
            </div>
          </dl>
        </section>

        <section v-else-if="!review.requires_manual_review" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <div class="flex justify-between gap-3 text-sm">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundCash') }}</span>
            <span class="font-medium text-red-600 dark:text-red-400">{{ formatCash(review.default_refund_amount, review.currency) }}</span>
          </div>
        </section>

        <section
          v-if="review.benefits && review.can_refund"
          data-testid="refund-benefits"
          class="rounded-lg border border-gray-200 p-3 dark:border-dark-600"
          :aria-label="t('payment.admin.benefitRefundImpact')"
        >
          <h3 class="text-sm font-medium text-gray-900 dark:text-white">{{ t('payment.admin.benefitRefundImpact') }}</h3>
          <dl class="mt-3 space-y-3 text-sm">
            <div v-if="review.benefits.reset_cards_to_reclaim > 0" class="flex justify-between gap-3">
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.giftResetCardsRecovery') }}</dt>
              <dd class="font-medium text-gray-900 dark:text-white">{{ t('payment.admin.giftResetCardsCount', { count: review.benefits.reset_cards_to_reclaim }) }}</dd>
            </div>
            <div>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.concurrencyRefundChange') }}</dt>
              <dd class="mt-1">
                <span class="font-medium tabular-nums text-gray-900 dark:text-white">{{ formatConcurrencyLimit(review.benefits.concurrency_current) }} → {{ formatConcurrencyLimit(review.benefits.concurrency_after_refund) }}</span>
                <p v-if="review.benefits.concurrency_before != null" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.concurrencyBeforePurchase', { count: formatConcurrencyLimit(review.benefits.concurrency_before) }) }}</p>
              </dd>
            </div>
          </dl>
          <details v-if="review.benefits.reset_card_grant_ids?.length" class="mt-3 border-t border-gray-100 pt-2 text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400">
            <summary class="cursor-pointer">{{ t('payment.admin.giftResetCardGrantIds') }}</summary>
            <p class="mt-2 break-words font-mono">{{ review.benefits.reset_card_grant_ids.map(id => `#${id}`).join(' · ') }}</p>
          </details>
        </section>

        <div
          v-if="review.requires_manual_review"
          role="alert"
          class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200"
        >
          <p class="font-medium">{{ canBackfillSubscription ? t('payment.admin.subscriptionGrantBackfillTitle') : t('payment.admin.refundManualReviewRequired') }}</p>
          <p v-if="reviewReason" class="mt-1">{{ reviewReason }}</p>
        </div>
        <section
          v-if="canBackfillSubscription"
          class="space-y-3 rounded-lg border border-blue-200 bg-blue-50/70 p-3 dark:border-blue-800 dark:bg-blue-900/20"
          :aria-label="t('payment.admin.subscriptionGrantBackfillTitle')"
        >
          <div>
            <h3 class="text-sm font-semibold text-blue-900 dark:text-blue-100">{{ t('payment.admin.subscriptionGrantBackfillTitle') }}</h3>
            <p class="mt-1 text-xs leading-5 text-blue-800 dark:text-blue-200">{{ t('payment.admin.subscriptionGrantBackfillHint') }}</p>
            <p class="mt-1 text-xs leading-5 text-blue-800 dark:text-blue-200">
              {{ t('payment.admin.subscriptionGrantBackfillSnapshot', {
                group: review?.subscription_backfill?.subscription_group_id,
                days: review?.subscription_backfill?.purchased_days,
              }) }}
            </p>
          </div>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label class="block">
              <span class="input-label">{{ t('payment.admin.subscriptionGrantBackfillSubscription') }}</span>
              <select
                v-model.number="backfillForm.subscriptionId"
                data-testid="backfill-subscription-id"
                class="input"
                :disabled="backfilling"
                required
                @change="applySelectedBackfillCandidate"
              >
                <option
                  v-for="candidate in review?.subscription_backfill?.candidates || []"
                  :key="candidate.subscription_id"
                  :value="candidate.subscription_id"
                >
                  #{{ candidate.subscription_id }} · {{ formatDateTime(candidate.starts_at) }} – {{ formatDateTime(candidate.expires_at) }}
                </option>
              </select>
            </label>
            <label class="block">
              <span class="input-label">{{ t('payment.admin.subscriptionGrantBackfillEvidenceSource') }}</span>
              <select v-model="backfillForm.evidenceSource" data-testid="backfill-evidence-source" class="input" :disabled="backfilling">
                <option v-for="source in backfillEvidenceSources" :key="source" :value="source">{{ t(`payment.admin.subscriptionGrantBackfillEvidenceSources.${source}`) }}</option>
              </select>
            </label>
            <label class="block">
              <span class="input-label">{{ t('payment.admin.subscriptionGrantBackfillTermStart') }}</span>
              <input v-model="backfillForm.termStartAt" data-testid="backfill-term-start" type="datetime-local" step="0.001" class="input" :disabled="backfilling" required />
            </label>
            <label class="block">
              <span class="input-label">{{ t('payment.admin.subscriptionGrantBackfillTermEnd') }}</span>
              <input v-model="backfillForm.termEndAt" data-testid="backfill-term-end" type="datetime-local" step="0.001" class="input" :disabled="backfilling" required />
            </label>
          </div>
          <label class="block">
            <span class="input-label">{{ t('payment.admin.subscriptionGrantBackfillEvidenceDetail') }}</span>
            <textarea
              v-model="backfillForm.evidenceDetail"
              data-testid="backfill-evidence-detail"
              rows="3"
              maxlength="240"
              class="input"
              :placeholder="t('payment.admin.subscriptionGrantBackfillEvidencePlaceholder')"
              :disabled="backfilling"
              required
            ></textarea>
          </label>
          <p class="text-xs leading-5 text-blue-800 dark:text-blue-200">{{ t('payment.admin.subscriptionGrantBackfillAmountHint') }}</p>
        </section>
        <div
          v-else-if="!review.can_refund && !review.requires_manual_review"
          role="alert"
          class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200"
        >
          <p class="font-medium">{{ t('payment.admin.refundUnavailable') }}</p>
          <p v-if="reviewReason" class="mt-1">{{ reviewReason }}</p>
        </div>
      </template>

      <div v-if="review?.can_refund && !review.requires_manual_review" class="space-y-3">
        <div>
          <label for="refund-reason-code" class="input-label">{{ t('payment.admin.refundReasonCode') }}</label>
          <select
            id="refund-reason-code"
            v-model="form.reasonCode"
            class="input"
            :disabled="loading || submitting || backfilling"
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
            :disabled="loading || submitting || backfilling"
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
        <button type="button" class="btn btn-secondary" :disabled="submitting || backfilling" @click="requestCancel">
          {{ t('common.cancel') }}
        </button>
        <button
          v-if="canBackfillSubscription"
          type="button"
          data-testid="subscription-grant-backfill"
          :disabled="!canSubmitBackfill"
          class="btn btn-primary"
          @click="handleBackfill"
        >
          {{ backfilling ? t('common.processing') : t('payment.admin.subscriptionGrantBackfillAction') }}
        </button>
        <button
          v-if="review?.can_refund && !review?.requires_manual_review"
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
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type {
  RefundReasonCode,
  RefundReview,
  SubscriptionGrantBackfillEvidenceSource,
  SubscriptionGrantBackfillRequest,
} from '@/api/admin/payment'
import type { PaymentOrder } from '@/types/payment'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import { currencySymbol } from '@/components/payment/currency'

const { t } = useI18n()

const props = defineProps<{
  show: boolean
  order: PaymentOrder | null
  review: RefundReview | null
  loading?: boolean
  previewing?: boolean
  reviewedRefundAmount?: number
  error?: string
  submitting?: boolean
  backfilling?: boolean
  warning?: string
}>()

const emit = defineEmits<{
  (e: 'confirm', data: { reason_code: RefundReasonCode; reason_detail?: string; refund_amount?: number }): void
  (e: 'preview', refundAmount: number | null): void
  (e: 'backfill', data: SubscriptionGrantBackfillRequest): void
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
const refundAmount = ref('')
function formatConcurrencyLimit(value: number): string {
  return value === 0 ? t('payment.admin.unlimited') : String(value)
}

function requestCancel() {
  if (!props.submitting && !props.backfilling) emit('cancel')
}

const refundAmountTouched = ref(false)
const backfillEvidenceSources: SubscriptionGrantBackfillEvidenceSource[] = [
  'payment_audit_and_subscription',
  'provider_receipt',
  'database_backup',
  'other',
]
const backfillForm = reactive<{
  subscriptionId: number | null
  termStartAt: string
  termEndAt: string
  evidenceSource: SubscriptionGrantBackfillEvidenceSource
  evidenceDetail: string
}>({
  subscriptionId: null,
  termStartAt: '',
  termEndAt: '',
  evidenceSource: 'payment_audit_and_subscription',
  evidenceDetail: '',
})
const backfillOriginal = reactive({
  termStartAt: '',
  termEndAt: '',
  termStartLocal: '',
  termEndLocal: '',
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

const isSubscriptionReview = computed(() => Boolean(
  props.review?.subscription &&
  props.review.can_refund &&
  !props.review.requires_manual_review &&
  props.review.quote_revision,
))

const minimumRefundAmount = computed(() => {
  const value = props.review?.min_refund_amount
  return Number.isFinite(value) ? Number(value) : 0.01
})

const selectedRefundAmount = computed<number | null>(() => {
  if (!/^(?:0|[1-9]\d*)(?:\.\d{1,2})?$/.test(refundAmount.value)) return null
  const value = Number(refundAmount.value)
  return Number.isFinite(value) ? value : null
})

function sameCashAmount(left: number | undefined, right: number | null): boolean {
  return left !== undefined && right !== null && Math.round(left * 100) === Math.round(right * 100)
}

const refundAmountError = computed(() => {
  if (!isSubscriptionReview.value || !refundAmountTouched.value) return ''
  const amount = selectedRefundAmount.value
  if (amount === null) return t('payment.admin.refundAmountInvalid')
  if (amount < minimumRefundAmount.value) return t('payment.admin.refundAmountTooSmall')
  if (amount > Number(props.review?.max_refund_amount || 0)) return t('payment.admin.refundAmountExceeded')
  return ''
})

const hasFreshRefundAmountReview = computed(() => {
  if (!isSubscriptionReview.value || !refundAmountTouched.value) return true
  return !refundAmountError.value && !props.previewing && sameCashAmount(props.reviewedRefundAmount, selectedRefundAmount.value)
})

const canConfirm = computed(() => {
  const review = props.review
  return Boolean(
    props.show &&
    !props.loading &&
    !props.previewing &&
    !props.error &&
    !props.submitting &&
    !props.backfilling &&
    review?.can_refund &&
    !review.requires_manual_review &&
    review.quote_revision &&
    hasFreshRefundAmountReview.value &&
    (form.reasonCode !== 'other' || form.reasonDetail.trim()),
  )
})

const canBackfillSubscription = computed(() => Boolean(
  props.review?.requires_manual_review &&
  props.review.reason_code === 'LEGACY_SUBSCRIPTION_UNATTRIBUTED' &&
  props.review.subscription_backfill,
))

const canSubmitBackfill = computed(() => Boolean(
  canBackfillSubscription.value &&
  !props.loading &&
  !props.submitting &&
  !props.backfilling &&
  backfillForm.subscriptionId &&
  backfillForm.termStartAt &&
  backfillForm.termEndAt &&
  backfillForm.evidenceDetail.trim(),
))

function resetReason() {
  form.reasonCode = 'customer_request'
  form.reasonDetail = props.order?.refund_request_reason || ''
}

function resetRefundAmount() {
  refundAmount.value = ''
  refundAmountTouched.value = false
}

function formatEditableCash(value: number | undefined): string {
  return Number.isFinite(value) ? Number(value).toFixed(2) : ''
}

function setRefundAmount(event: Event) {
  refundAmountTouched.value = true
  refundAmount.value = (event.target as HTMLInputElement).value
  emit('preview', refundAmountError.value ? null : selectedRefundAmount.value)
}

function toLocalDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function setSuggestedBackfillTerms(termStartAt: string, termEndAt: string) {
  backfillOriginal.termStartAt = termStartAt
  backfillOriginal.termEndAt = termEndAt
  backfillOriginal.termStartLocal = toLocalDateTime(termStartAt)
  backfillOriginal.termEndLocal = toLocalDateTime(termEndAt)
  backfillForm.termStartAt = backfillOriginal.termStartLocal
  backfillForm.termEndAt = backfillOriginal.termEndLocal
}

function clearBackfillTerms() {
  backfillOriginal.termStartAt = ''
  backfillOriginal.termEndAt = ''
  backfillOriginal.termStartLocal = ''
  backfillOriginal.termEndLocal = ''
  backfillForm.termStartAt = ''
  backfillForm.termEndAt = ''
}

function resetBackfill() {
  const suggestion = props.review?.subscription_backfill
  backfillForm.subscriptionId = suggestion?.suggested_subscription_id || null
  if (suggestion?.suggested_term_start_at && suggestion.suggested_term_end_at) {
    setSuggestedBackfillTerms(suggestion.suggested_term_start_at, suggestion.suggested_term_end_at)
  } else {
    clearBackfillTerms()
  }
  backfillForm.evidenceSource = suggestion?.evidence_source || 'payment_audit_and_subscription'
  backfillForm.evidenceDetail = ''
}

function applySelectedBackfillCandidate() {
  const suggestion = props.review?.subscription_backfill
  const candidate = suggestion?.candidates.find(item => item.subscription_id === backfillForm.subscriptionId)
  if (!candidate || !suggestion) return
  if (candidate.subscription_id === suggestion.suggested_subscription_id) {
    setSuggestedBackfillTerms(suggestion.suggested_term_start_at, suggestion.suggested_term_end_at)
    return
  }
  // A candidate lifecycle can already contain grants. Only the server can prove
  // a safe term interval, so require the administrator to enter reviewed evidence.
  clearBackfillTerms()
}

watch(() => props.show, (show) => {
  if (show) {
    resetReason()
    resetRefundAmount()
  }
})

watch(() => props.order?.id, () => {
  if (props.show) {
    resetReason()
    resetRefundAmount()
  }
})

watch(() => props.review?.subscription_backfill?.audit_revision, () => {
  if (props.show) resetBackfill()
}, { immediate: true })

watch(() => props.review?.quote_revision, () => {
  if (props.show && isSubscriptionReview.value && !refundAmountTouched.value) {
    refundAmount.value = formatEditableCash(props.review?.default_refund_amount)
  }
}, { immediate: true })

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
  const selectedAmount = refundAmountTouched.value ? selectedRefundAmount.value : null
  emit('confirm', {
    reason_code: form.reasonCode,
    ...(detail ? { reason_detail: detail } : {}),
    ...(selectedAmount !== null ? { refund_amount: selectedAmount } : {}),
  })
}

function serializeBackfillTimestamp(value: string, originalValue: string, originalLocalValue: string): string {
  if (originalValue && value === originalLocalValue) return originalValue
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : date.toISOString()
}

function handleBackfill() {
  const suggestion = props.review?.subscription_backfill
  if (!canSubmitBackfill.value || !suggestion || !backfillForm.subscriptionId) return
  const termStartAt = serializeBackfillTimestamp(backfillForm.termStartAt, backfillOriginal.termStartAt, backfillOriginal.termStartLocal)
  const termEndAt = serializeBackfillTimestamp(backfillForm.termEndAt, backfillOriginal.termEndAt, backfillOriginal.termEndLocal)
  if (!termStartAt || !termEndAt) return
  emit('backfill', {
    audit_revision: suggestion.audit_revision,
    subscription_id: backfillForm.subscriptionId,
    term_start_at: termStartAt,
    term_end_at: termEndAt,
    evidence_source: backfillForm.evidenceSource,
    evidence_detail: backfillForm.evidenceDetail.trim(),
  })
}
</script>
