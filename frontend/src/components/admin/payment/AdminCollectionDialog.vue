<template>
  <BaseDialog
    :show="show"
    :title="t('payment.admin.collection.title')"
    width="narrow"
    :close-on-escape="!creating"
    :close-on-click-outside="!creating"
    @close="handleClose"
  >
    <form v-if="!checkout" class="space-y-5" @submit.prevent="createCollectionOrder">
      <p class="text-sm leading-6 text-gray-600 dark:text-gray-300">
        {{ t('payment.admin.collection.description') }}
      </p>

      <div>
        <label class="input-label" for="collection-amount">{{ t('payment.admin.collection.amountLabel') }}</label>
        <div class="relative mt-1">
          <span class="pointer-events-none absolute inset-y-0 left-3 flex items-center text-sm text-gray-500 dark:text-gray-400">¥</span>
          <input
            id="collection-amount"
            v-model="amountInput"
            data-test="collection-amount"
            class="input w-full pl-7 tabular-nums"
            type="text"
            inputmode="decimal"
            autocomplete="off"
            placeholder="0.00"
            aria-describedby="collection-amount-hint collection-amount-error"
            :aria-invalid="amountTouched && !amountFen ? 'true' : undefined"
            :disabled="creating"
            @blur="amountTouched = true"
          />
        </div>
        <p id="collection-amount-hint" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('payment.admin.collection.amountHint') }}
        </p>
        <p v-if="amountTouched && !amountFen" id="collection-amount-error" class="mt-1 text-xs text-red-600 dark:text-red-400">
          {{ t('payment.admin.collection.invalidAmount') }}
        </p>
      </div>

      <div>
        <label class="input-label" for="collection-payment-type">{{ t('payment.admin.collection.paymentMethod') }}</label>
        <Select
          id="collection-payment-type"
          v-model="paymentType"
          data-test="collection-payment-type"
          :options="paymentTypeOptions"
          class="mt-1 w-full"
          :disabled="creating"
        />
      </div>

      <p class="rounded-md border border-amber-200 bg-amber-50 p-3 text-sm leading-6 text-amber-900 dark:border-amber-900/70 dark:bg-amber-950/20 dark:text-amber-100">
        {{ t('payment.admin.collection.noBalanceCredit') }}
      </p>

      <p v-if="createError" data-test="collection-create-error" class="rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900/70 dark:bg-red-950/20 dark:text-red-300" role="alert">
        {{ createError }}
      </p>

      <div class="flex justify-end gap-3 border-t border-gray-200 pt-4 dark:border-dark-600">
        <button type="button" class="btn btn-secondary" :disabled="creating" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button type="submit" data-test="create-collection-order" class="btn btn-primary inline-flex items-center gap-2" :disabled="creating || !amountFen">
          <Icon name="refresh" size="sm" :class="creating ? 'animate-spin' : 'hidden'" />
          {{ creating ? t('common.processing') : t('payment.admin.collection.create') }}
        </button>
      </div>
    </form>

    <section v-else class="space-y-4">
      <p class="rounded-md border border-amber-200 bg-amber-50 p-3 text-sm leading-6 text-amber-900 dark:border-amber-900/70 dark:bg-amber-950/20 dark:text-amber-100">
        {{ t('payment.admin.collection.noBalanceCredit') }}
      </p>

      <div v-if="checkoutActionable && nativeQRCode" class="flex flex-col items-center gap-3">
        <div class="rounded-lg bg-white p-4 shadow-sm dark:bg-dark-800">
          <canvas ref="qrCanvas" data-test="collection-qr-canvas" class="mx-auto"></canvas>
        </div>
        <button type="button" data-test="download-collection-qr" class="btn btn-secondary inline-flex items-center gap-2" :disabled="!!qrError" @click="downloadQRCode">
          <Icon name="download" size="sm" />
          {{ t('payment.admin.collection.downloadQRCode') }}
        </button>
      </div>

      <p v-if="checkoutActionable && !nativeQRCode" data-test="collection-native-qr-error" class="rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900/70 dark:bg-red-950/20 dark:text-red-300" role="alert">
        {{ t('payment.admin.collection.nativeQRCodeUnavailable') }}
      </p>

      <p v-if="checkoutActionable && qrError" data-test="collection-qr-error" class="rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900/70 dark:bg-red-950/20 dark:text-red-300" role="alert">
        {{ qrError }}
      </p>

      <a
        v-if="checkoutActionable && checkoutURL"
        :href="checkoutURL"
        data-test="collection-checkout-link"
        class="btn btn-secondary inline-flex w-full items-center justify-center gap-2"
        target="_blank"
        rel="noopener noreferrer"
      >
        <Icon name="externalLink" size="sm" />
        {{ t('payment.admin.collection.openCheckoutPage') }}
      </a>

      <dl class="space-y-3 rounded-lg bg-gray-50 p-4 text-sm dark:bg-dark-800">
        <div class="flex items-start justify-between gap-4">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.collection.orderNumber') }}</dt>
          <dd class="break-all text-right font-mono text-gray-900 dark:text-white">{{ checkout.out_trade_no || `#${checkout.order_id}` }}</dd>
        </div>
        <div class="flex items-start justify-between gap-4">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.collection.actualAmount') }}</dt>
          <dd class="text-right font-semibold tabular-nums text-gray-900 dark:text-white">{{ paymentAmount }}</dd>
        </div>
        <div class="flex items-start justify-between gap-4">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.collection.expiresAt') }}</dt>
          <dd class="text-right text-gray-900 dark:text-white">{{ expiresAt }}</dd>
        </div>
      </dl>

      <p
        v-if="paymentStateMessage"
        data-test="collection-payment-state"
        class="rounded-md p-3 text-sm"
        :class="paymentConfirmed ? 'bg-green-50 text-green-800 dark:bg-green-950/20 dark:text-green-200' : paymentTerminal ? 'bg-amber-50 text-amber-800 dark:bg-amber-950/20 dark:text-amber-200' : 'bg-gray-50 text-gray-700 dark:bg-dark-800 dark:text-gray-200'"
      >
        {{ paymentStateMessage }}
      </p>

      <p v-if="refreshError" data-test="collection-refresh-error" class="rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900/70 dark:bg-red-950/20 dark:text-red-300" role="alert">
        {{ refreshError }}
      </p>

      <div class="flex flex-wrap justify-end gap-3 border-t border-gray-200 pt-4 dark:border-dark-600">
        <button type="button" data-test="refresh-collection-order" class="btn btn-secondary" :disabled="refreshing" @click="refreshOrder">
          <Icon name="refresh" size="sm" :class="refreshing ? 'animate-spin' : ''" />
          {{ refreshing ? t('common.loading') : t('payment.admin.collection.refreshOrder') }}
        </button>
        <button v-if="checkoutActionable && !nativeQRCode" type="button" class="btn btn-primary" :disabled="creating" @click="retryCreate">
          {{ t('payment.admin.collection.retryCreate') }}
        </button>
        <button type="button" class="btn btn-primary" :disabled="creating || refreshing" @click="handleClose">
          {{ t('common.confirm') }}
        </button>
      </div>
    </section>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import QRCode from 'qrcode'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { adminPaymentAPI, type AdminCollectionPaymentType } from '@/api/admin/payment'
import {
  collectionIntentFingerprint,
  parseCollectionAmountFen,
  resolveCollectionCheckoutURL,
  resolveCollectionNativeQRCode,
} from '@/components/admin/payment/adminCollection'
import { formatPaymentAmount } from '@/components/payment/currency'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import { paymentFact } from '@/components/payment/orderPresentation'
import type { CreateOrderResult, PaymentOrder } from '@/types/payment'
import type { StepUpController } from '@/composables/useStepUp'
import { isStepUpCancelled } from '@/composables/useStepUp'
import { createIdempotencyKey } from '@/utils/idempotency'
import { extractI18nErrorMessage } from '@/utils/apiError'

const props = defineProps<{
  show: boolean
  stepUp: StepUpController
}>()

const emit = defineEmits<{
  close: []
}>()

const { t } = useI18n()

const amountInput = ref('')
const amountTouched = ref(false)
const paymentType = ref<AdminCollectionPaymentType>('alipay')
const creating = ref(false)
const refreshing = ref(false)
const createError = ref('')
const refreshError = ref('')
const checkout = ref<CreateOrderResult | null>(null)
const refreshedOrder = ref<PaymentOrder | null>(null)
const qrError = ref('')
const qrCanvas = ref<HTMLCanvasElement | null>(null)
const localNow = ref(Date.now())

let dialogGeneration = 0
let qrRenderGeneration = 0
let refreshGeneration = 0
let attempt: { fingerprint: string; idempotencyKey: string } | null = null
let expiryTimer: ReturnType<typeof setInterval> | null = null

const amountFen = computed(() => parseCollectionAmountFen(amountInput.value))
const paymentTypeOptions = computed(() => [
  { value: 'alipay', label: t('payment.methods.alipay') },
  { value: 'wxpay', label: t('payment.methods.wxpay') },
])
const nativeQRCode = computed(() => {
  if (!checkout.value) return ''
  return resolveCollectionNativeQRCode(checkout.value, paymentType.value)
})
const checkoutURL = computed(() => checkout.value ? resolveCollectionCheckoutURL(checkout.value) : '')
const paymentAmount = computed(() => {
  if (!checkout.value) return ''
  return formatPaymentAmount(checkout.value.pay_amount, checkout.value.currency || 'CNY')
})
const expiresAt = computed(() => formatOrderDateTime(checkout.value?.expires_at || ''))
const paymentConfirmed = computed(() => paymentFact(refreshedOrder.value || checkout.value || {}) === 'PAID')
const refreshedStatus = computed(() => String(refreshedOrder.value?.status || checkout.value?.status || '').trim().toUpperCase())
const locallyExpired = computed(() => {
  const deadline = Date.parse(checkout.value?.expires_at || '')
  return Number.isFinite(deadline) && deadline <= localNow.value
})
const paymentTerminal = computed(() => {
  if (paymentConfirmed.value || locallyExpired.value) return true
  return new Set([
    'EXPIRED',
    'CANCELLED',
    'FAILED',
    'REFUND_REQUESTED',
    'REFUNDING',
    'REFUND_PENDING',
    'REFUNDED',
    'PARTIALLY_REFUNDED',
    'REFUND_FAILED',
  ]).has(refreshedStatus.value)
})
const checkoutActionable = computed(() => Boolean(checkout.value) && !paymentTerminal.value)
const paymentStateMessage = computed(() => {
  if (locallyExpired.value && (!refreshedStatus.value || refreshedStatus.value === 'PENDING')) {
    return t('payment.admin.collection.expiredLocally')
  }
  if (!refreshedStatus.value) return ''
  if (paymentConfirmed.value) return t('payment.admin.collection.paymentConfirmed')
  if (refreshedStatus.value === 'PENDING') return t('payment.admin.collection.paymentPending')
  return t(`payment.status.${refreshedStatus.value.toLowerCase()}`)
})

function stopExpiryTimer(): void {
  if (expiryTimer !== null) clearInterval(expiryTimer)
  expiryTimer = null
}

function startExpiryTimer(): void {
  stopExpiryTimer()
  if (!checkout.value) return
  localNow.value = Date.now()
  expiryTimer = setInterval(() => { localNow.value = Date.now() }, 1_000)
}

function resetDialog(): void {
  dialogGeneration += 1
  qrRenderGeneration += 1
  refreshGeneration += 1
  amountInput.value = ''
  amountTouched.value = false
  paymentType.value = 'alipay'
  creating.value = false
  refreshing.value = false
  createError.value = ''
  refreshError.value = ''
  checkout.value = null
  refreshedOrder.value = null
  qrError.value = ''
  attempt = null
  localNow.value = Date.now()
  stopExpiryTimer()
}

function handleClose(): void {
  if (creating.value || refreshing.value) return
  emit('close')
}

function idempotencyKeyFor(amount: number, type: AdminCollectionPaymentType): string {
  const fingerprint = collectionIntentFingerprint(amount, type)
  if (attempt?.fingerprint === fingerprint) return attempt.idempotencyKey
  attempt = { fingerprint, idempotencyKey: createIdempotencyKey('admin-collection') }
  return attempt.idempotencyKey
}

async function createCollectionOrder(): Promise<void> {
  const selectedAmountFen = amountFen.value
  if (!selectedAmountFen) {
    amountTouched.value = true
    return
  }

  const selectedPaymentType = paymentType.value
  const requestGeneration = dialogGeneration
  const idempotencyKey = idempotencyKeyFor(selectedAmountFen, selectedPaymentType)
  creating.value = true
  createError.value = ''
  refreshError.value = ''
  try {
    const response = await props.stepUp.run(() => adminPaymentAPI.createCollectionOrder({
      amount_fen: selectedAmountFen,
      payment_type: selectedPaymentType,
    }, idempotencyKey))
    if (requestGeneration !== dialogGeneration || !props.show) return

    checkout.value = response.data
    refreshedOrder.value = null
    qrError.value = ''
  } catch (err: unknown) {
    if (requestGeneration !== dialogGeneration || isStepUpCancelled(err)) return
    createError.value = extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))
  } finally {
    if (requestGeneration === dialogGeneration) creating.value = false
  }
}

async function retryCreate(): Promise<void> {
  if (creating.value) return
  await createCollectionOrder()
}

async function renderQRCode(): Promise<void> {
  const renderGeneration = ++qrRenderGeneration
  const value = nativeQRCode.value
  if (!value) return

  await nextTick()
  const canvas = qrCanvas.value
  if (!canvas || renderGeneration !== qrRenderGeneration) return
  try {
    await QRCode.toCanvas(canvas, value, {
      width: 256,
      margin: 2,
      errorCorrectionLevel: 'M',
    })
  } catch {
    if (renderGeneration !== qrRenderGeneration) return
    qrError.value = t('payment.admin.collection.qrRenderFailed')
  }
}

function downloadQRCode(): void {
  const canvas = qrCanvas.value
  if (!canvas || !checkout.value) return
  try {
    const link = document.createElement('a')
    link.href = canvas.toDataURL('image/png')
    link.download = `collection-${checkout.value.out_trade_no || checkout.value.order_id}.png`
    document.body.appendChild(link)
    link.click()
    link.remove()
  } catch {
    qrError.value = t('payment.admin.collection.downloadFailed')
  }
}

function extractOrder(value: unknown): PaymentOrder | null {
  if (!value || typeof value !== 'object') return null
  const response = value as { order?: unknown }
  const candidate = response.order && typeof response.order === 'object' ? response.order : value
  return typeof candidate === 'object' && 'id' in candidate ? candidate as PaymentOrder : null
}

async function refreshOrder(): Promise<void> {
  if (!checkout.value || refreshing.value) return
  const requestGeneration = ++refreshGeneration
  refreshing.value = true
  refreshError.value = ''
  try {
    const response = await adminPaymentAPI.getOrder(checkout.value.order_id)
    if (requestGeneration !== refreshGeneration || !props.show) return
    const order = extractOrder(response.data)
    if (!order || order.id !== checkout.value.order_id) {
      refreshError.value = t('payment.admin.collection.refreshUnavailable')
      return
    }
    refreshedOrder.value = order
  } catch (err: unknown) {
    if (requestGeneration !== refreshGeneration) return
    refreshError.value = extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))
  } finally {
    if (requestGeneration === refreshGeneration) refreshing.value = false
  }
}

watch(() => props.show, (open, wasOpen) => {
  if (open && !wasOpen) resetDialog()
  if (!open) {
    dialogGeneration += 1
    qrRenderGeneration += 1
    refreshGeneration += 1
    stopExpiryTimer()
  }
}, { immediate: true })

watch(nativeQRCode, () => {
  qrRenderGeneration += 1
  if (nativeQRCode.value) void renderQRCode()
})

watch(checkout, () => {
  if (props.show) startExpiryTimer()
})

onUnmounted(() => {
  dialogGeneration += 1
  qrRenderGeneration += 1
  refreshGeneration += 1
  stopExpiryTimer()
})
</script>
