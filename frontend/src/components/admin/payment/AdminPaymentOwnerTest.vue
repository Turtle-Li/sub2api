<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import {
  createOwnerTestOrder,
  isOwnerTestOrderRequest,
  type OwnerTestOrderRequest,
  type OwnerTestOrderResponse,
  type OwnerTestPaymentType,
} from '@/api/admin/payment'
import { createIdempotencyKey } from '@/utils/idempotency'
import QRCode from 'qrcode'

const ownerTestMessages = {
  zh: {
    title: '管理员小额实测',
    description: '仅限管理员的 live 统一支付通道实测；不改变公开购买设置。扫码付款后按真实订单入账，可在订单列表退款。',
    amount: '测试金额',
    oneFen: '¥0.01（1 分）',
    twoFen: '¥0.02（2 分）',
    alipay: '支付宝',
    wechat: '微信支付',
    creating: '正在创建测试订单…',
    created: '已创建普通余额订单。付款状态以订单记录为准。',
    order: '订单',
    status: '状态',
    openCheckout: '打开收银台',
    scanWechat: '请使用微信扫码付款。',
    noCheckout: '服务未返回结账链接；请按订单号和状态继续核查。',
    unknown: '结果未知。以相同选项重试会重用同一请求键。',
    invalid: '仅支持 1 分或 2 分的支付宝、微信支付测试。',
  },
  en: {
    title: 'Administrator small-amount test',
    description: 'For an administrator live unified-payment check only; it does not change public-purchase settings. A completed checkout credits the administrator through a normal balance order, which can be refunded from the orders list.',
    amount: 'Test amount',
    oneFen: '¥0.01 (1 fen)',
    twoFen: '¥0.02 (2 fen)',
    alipay: 'Alipay',
    wechat: 'WeChat Pay',
    creating: 'Creating test order…',
    created: 'A normal balance order was created. The order record is the source of payment status.',
    order: 'Order',
    status: 'Status',
    openCheckout: 'Open checkout',
    scanWechat: 'Scan this QR code with WeChat Pay.',
    noCheckout: 'The service did not return a checkout link. Continue with the order ID and status.',
    unknown: 'The result is unknown. Retrying the same choice reuses the same request key.',
    invalid: 'Only 1-fen or 2-fen Alipay and WeChat Pay tests are supported.',
  },
}

const { locale } = useI18n()
type OwnerTestMessageKey = keyof typeof ownerTestMessages.en

function ownerTestText(key: OwnerTestMessageKey): string {
  const messages = locale.value.toLowerCase().startsWith('zh') ? ownerTestMessages.zh : ownerTestMessages.en
  return messages[key]
}

interface PendingOwnerTestMutation {
  intent: OwnerTestOrderRequest
  idempotencyKey: string
}

const amountOptions = [1, 2] as const
const selectedAmountFen = ref<1 | 2>(1)
const submitting = ref(false)
const response = ref<OwnerTestOrderResponse | null>(null)
const responsePaymentType = ref<OwnerTestPaymentType | null>(null)
const error = ref('')
const pendingMutations = ref(new Map<string, PendingOwnerTestMutation>())
const qrCanvas = ref<HTMLCanvasElement | null>(null)
const stepUp = useStepUp()
const emit = defineEmits<{
  created: [order: OwnerTestOrderResponse]
}>()

const selectedAmountLabel = computed(() => amountLabel(selectedAmountFen.value))
const qrCode = computed(() => typeof response.value?.qr_code === 'string' ? response.value.qr_code.trim() : '')
const hasWechatQRCode = computed(() => responsePaymentType.value === 'wxpay' && Boolean(qrCode.value))
const showCheckoutLink = computed(() => Boolean(response.value?.pay_url) && !hasWechatQRCode.value)

function amountLabel(amountFen: 1 | 2): string {
  return amountFen === 1 ? ownerTestText('oneFen') : ownerTestText('twoFen')
}

function selectAmount(amountFen: 1 | 2) {
  if (submitting.value) return
  selectedAmountFen.value = amountFen
  response.value = null
  responsePaymentType.value = null
  error.value = ''
}

function intentFor(paymentType: string): OwnerTestOrderRequest | null {
  const candidate = { amount_fen: selectedAmountFen.value, payment_type: paymentType }
  return isOwnerTestOrderRequest(candidate) ? { ...candidate } : null
}

function sameIntent(left: OwnerTestOrderRequest, right: OwnerTestOrderRequest): boolean {
  return left.amount_fen === right.amount_fen && left.payment_type === right.payment_type
}

function intentKey(intent: OwnerTestOrderRequest): string {
  return `${intent.amount_fen}:${intent.payment_type}`
}

function mutationFor(intent: OwnerTestOrderRequest): PendingOwnerTestMutation {
  const key = intentKey(intent)
  const current = pendingMutations.value.get(key)
  if (current && sameIntent(current.intent, intent)) return current
  const mutation = {
    intent: Object.freeze({ ...intent }) as OwnerTestOrderRequest,
    idempotencyKey: createIdempotencyKey('owner-payment-test'),
  }
  const next = new Map(pendingMutations.value)
  next.set(key, mutation)
  pendingMutations.value = next
  return mutation
}

function clearMutation(intent: OwnerTestOrderRequest) {
  const next = new Map(pendingMutations.value)
  next.delete(intentKey(intent))
  pendingMutations.value = next
}

async function renderQRCode() {
  await nextTick()
  if (!qrCanvas.value || !qrCode.value) return
  try {
    await QRCode.toCanvas(qrCanvas.value, qrCode.value, {
      width: 192,
      margin: 2,
      errorCorrectionLevel: 'M',
    })
  } catch {
    // The signed payment response remains available through the order record.
  }
}

watch(qrCode, () => { void renderQRCode() }, { flush: 'post' })

async function createTestOrder(paymentType: OwnerTestPaymentType | string) {
  if (submitting.value) return
  const intent = intentFor(paymentType)
  if (!intent) {
    error.value = ownerTestText('invalid')
    return
  }
  const mutation = mutationFor(intent)
  submitting.value = true
  response.value = null
  responsePaymentType.value = null
  error.value = ''
  try {
    const createdOrder = await stepUp.run(() => createOwnerTestOrder(mutation.intent, mutation.idempotencyKey))
    responsePaymentType.value = mutation.intent.payment_type
    response.value = createdOrder
    clearMutation(mutation.intent)
    emit('created', createdOrder)
  } catch (err) {
    if (!isStepUpCancelled(err)) error.value = ownerTestText('unknown')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <section class="card p-4" aria-labelledby="owner-payment-test-title">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h3 id="owner-payment-test-title" class="text-sm font-semibold text-gray-900 dark:text-white">{{ ownerTestText('title') }}</h3>
        <p class="mt-1 text-xs text-gray-600 dark:text-gray-400">{{ ownerTestText('description') }}</p>
      </div>
      <span class="rounded-full bg-amber-50 px-2 py-1 text-xs font-medium text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">{{ selectedAmountLabel }}</span>
    </div>

    <div class="mt-3 flex flex-wrap items-center gap-2" role="group" :aria-label="ownerTestText('amount')">
      <span class="text-xs font-medium text-gray-700 dark:text-gray-300">{{ ownerTestText('amount') }}</span>
      <button
        v-for="amountFen in amountOptions"
        :key="amountFen"
        type="button"
        :data-testid="`owner-payment-test-amount-${amountFen}`"
        :aria-pressed="selectedAmountFen === amountFen"
        :disabled="submitting"
        :class="[
          'rounded-md border px-2.5 py-1 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-60',
          selectedAmountFen === amountFen
            ? 'border-primary-500 bg-primary-50 text-primary-700 dark:border-primary-400 dark:bg-primary-900/30 dark:text-primary-200'
            : 'border-gray-300 text-gray-700 hover:bg-gray-50 dark:border-dark-600 dark:text-gray-300 dark:hover:bg-dark-700',
        ]"
        @click="selectAmount(amountFen)"
      >
        {{ amountLabel(amountFen) }}
      </button>
    </div>

    <div class="mt-3 flex flex-wrap gap-2">
      <button type="button" data-testid="owner-payment-test-alipay" class="btn btn-secondary" :disabled="submitting" @click="createTestOrder('alipay')">
        {{ ownerTestText('alipay') }} · {{ selectedAmountLabel }}
      </button>
      <button type="button" data-testid="owner-payment-test-wxpay" class="btn btn-secondary" :disabled="submitting" @click="createTestOrder('wxpay')">
        {{ ownerTestText('wechat') }} · {{ selectedAmountLabel }}
      </button>
    </div>

    <p v-if="submitting" role="status" class="mt-3 text-sm text-gray-600 dark:text-gray-300">{{ ownerTestText('creating') }}</p>
    <p v-if="error" role="alert" class="mt-3 text-sm text-red-700 dark:text-red-300">{{ error }}</p>

    <div v-if="response" data-testid="owner-payment-test-result" class="mt-3 rounded-md border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-900 dark:border-emerald-900/70 dark:bg-emerald-950/30 dark:text-emerald-100">
      <p>{{ ownerTestText('created') }}</p>
      <dl class="mt-2 grid gap-x-4 gap-y-1 sm:grid-cols-2">
        <div><dt class="inline font-medium">{{ ownerTestText('order') }}: </dt><dd class="inline">#{{ response.order_id }}</dd></div>
        <div><dt class="inline font-medium">{{ ownerTestText('status') }}: </dt><dd class="inline">{{ response.status }}</dd></div>
      </dl>
      <div v-if="hasWechatQRCode" data-testid="owner-payment-test-qr" class="mt-3 flex flex-col items-center gap-2">
        <div class="rounded-lg bg-white p-3 shadow-sm dark:bg-dark-800"><canvas ref="qrCanvas" /></div>
        <p class="text-xs text-emerald-800 dark:text-emerald-200">{{ ownerTestText('scanWechat') }}</p>
      </div>
      <a v-if="showCheckoutLink && response.pay_url" data-testid="owner-payment-test-checkout" :href="response.pay_url" target="_blank" rel="noopener noreferrer" class="mt-2 inline-flex text-sm font-medium text-primary-700 underline underline-offset-2 hover:text-primary-800 dark:text-primary-300 dark:hover:text-primary-200">
        {{ ownerTestText('openCheckout') }}
      </a>
      <p v-else-if="!hasWechatQRCode" class="mt-2 text-xs text-emerald-800 dark:text-emerald-200">{{ ownerTestText('noCheckout') }}</p>
    </div>
    <TotpStepUpDialog :controller="stepUp" />
  </section>
</template>
