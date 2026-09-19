<template>
  <section ref="shopRef" aria-labelledby="reset-card-shop-title" class="mt-7 border-t border-gray-200 pt-5 dark:border-dark-700" tabindex="-1">
    <div class="flex items-center gap-2">
      <Icon name="refresh" size="sm" class="text-primary-600 dark:text-primary-400" />
      <h2 id="reset-card-shop-title" class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('payment.resetShop.title') }}</h2>
    </div>
    <p v-if="!offers.length" class="mt-3 text-sm text-gray-500">{{ t('payment.resetShop.requiresSubscription') }}</p>

    <div v-else class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
      <button
        v-for="offer in offers"
        :key="offer.subscription.id"
        type="button"
        :data-reset-card-offer="offer.subscription.id"
        :aria-pressed="isSelectedOffer(offer)"
        :class="[
          'group flex min-w-0 items-center gap-3 rounded-xl border bg-white px-4 py-3.5 text-left transition hover:border-primary-400 hover:bg-primary-50/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 disabled:cursor-not-allowed disabled:opacity-70 dark:bg-dark-800 dark:hover:border-primary-600 dark:hover:bg-primary-950/30',
          isHighlightedOffer(offer)
            ? 'border-primary-500 bg-primary-50/70 ring-1 ring-primary-500/30 dark:border-primary-500 dark:bg-primary-950/35'
            : 'border-gray-200 dark:border-dark-700',
        ]"
        :disabled="disabled || loading || offer.eligibility?.can_purchase === false"
        @click="select(offer.subscription)"
      >
        <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300">
          <Icon :name="isSelectedOffer(offer) ? 'check' : 'refresh'" size="sm" />
        </span>
        <span class="min-w-0 flex-1">
          <span class="block truncate text-sm font-semibold text-gray-900 dark:text-white">{{ offer.title }}</span>
          <span class="mt-0.5 block text-xs text-gray-500 dark:text-dark-400">
            {{ offer.description || t('payment.resetShop.singleCard') }}
            <PurchaseEligibilityHint :eligibility="offer.eligibility" />
          </span>
        </span>
        <span class="shrink-0 text-right">
          <strong class="block text-lg font-semibold tabular-nums text-gray-900 dark:text-white">{{ offer.price }}</strong>
          <span class="text-[10px] text-gray-500 dark:text-dark-400">{{ t('payment.currencyUnit') }}</span>
        </span>
        <Icon name="chevronRight" size="xs" class="shrink-0 text-gray-400 group-hover:text-primary-500" />
      </button>
    </div>

    <div v-if="selectedQuote" class="mt-4 rounded-xl border border-primary-200 bg-primary-50/40 p-4 dark:border-primary-900/60 dark:bg-primary-950/20" data-test="reset-card-options">
      <div class="flex flex-wrap items-end justify-between gap-4">
        <label class="grid gap-1.5 text-sm font-medium text-gray-800 dark:text-gray-100">
          <span>{{ t('payment.resetShop.quantity') }}</span>
          <input
            data-test="reset-card-quantity"
            class="input h-10 w-24 tabular-nums"
            type="number"
            min="1"
            max="99"
            step="1"
            inputmode="numeric"
            :value="normalizedQuantity"
            :disabled="disabled"
            @input="updateQuantity"
          />
        </label>
        <div class="min-w-0 text-right text-sm text-gray-600 dark:text-gray-300">
          <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('payment.resetShop.validity') }}</p>
          <p class="mt-1 font-medium tabular-nums text-gray-900 dark:text-white">{{ formatExpiry(selectedQuote.expires_at) }}</p>
        </div>
      </div>
      <label class="mt-4 flex cursor-pointer items-start gap-3 rounded-lg border border-primary-100 bg-white/80 p-3 text-sm text-gray-800 dark:border-primary-900/40 dark:bg-dark-800/70 dark:text-gray-100">
        <input
          data-test="reset-card-use-on-purchase"
          class="mt-0.5 h-4 w-4 shrink-0 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
          type="checkbox"
          :checked="useOnPurchase"
          :disabled="disabled"
          @change="updateUseOnPurchase"
        />
        <span>{{ t('payment.resetShop.useOnPurchase') }}</span>
      </label>
    </div>

    <p v-if="error" role="alert" class="mt-3 text-sm text-red-600 dark:text-red-400">{{ error }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getResetCardQuote, type ResetCardQuote } from '@/api/subscriptions'
import { extractI18nErrorMessage } from '@/utils/apiError'
import type { UserSubscription } from '@/types'
import type { SubscriptionPlan } from '@/types/payment'
import Icon from '@/components/icons/Icon.vue'
import PurchaseEligibilityHint from './PurchaseEligibilityHint.vue'

const props = withDefaults(defineProps<{
  subscriptions: UserSubscription[]
  plans?: SubscriptionPlan[]
  disabled?: boolean
  targetSubscriptionId?: number | null
  selectedSubscriptionId?: number | null
  selectedQuote?: ResetCardQuote | null
  quantity?: number
  useOnPurchase?: boolean
}>(), {
  plans: () => [],
  disabled: false,
  targetSubscriptionId: null,
  selectedSubscriptionId: null,
  selectedQuote: null,
  quantity: 1,
  useOnPurchase: false,
})

const emit = defineEmits<{
  select: [payload: { subscription: UserSubscription; quote: ResetCardQuote }]
  updateOptions: [payload: { quantity: number; useOnPurchase: boolean }]
}>()

const { t } = useI18n()
const loading = ref(false)
const error = ref('')
const shopRef = ref<HTMLElement | null>(null)

const offers = computed(() => props.subscriptions.flatMap(subscription => {
  if (subscription.group?.platform !== 'openai' || subscription.status !== 'active' || (subscription.expires_at && Date.parse(subscription.expires_at) <= Date.now())) return []
  const monthlyPlans = props.plans.filter(plan => plan.group_id === subscription.group_id && plan.group_platform === 'openai'
    && plan.currency?.toUpperCase() === 'CNY' && ((['month', 'months'].includes(plan.validity_unit || '') && plan.validity_days === 1) ||
      (['day', 'days', ''].includes(plan.validity_unit || '') && plan.validity_days === 30)))
  if (monthlyPlans.length !== 1) return []
  const plan = monthlyPlans[0]
  if (plan.reset_card_eligibility?.visible === false) return []
  const price = plan.entitlements?.reset_card_purchase_price ?? Math.round(plan.price / 3 * 100) / 100
  return Number.isFinite(price) && price > 0 ? [{
    subscription,
    price,
    eligibility: plan.reset_card_eligibility,
    title: plan.entitlements?.reset_card_title || subscription.group?.name,
    description: plan.entitlements?.reset_card_description,
  }] : []
}))

const normalizedQuantity = computed(() => clampQuantity(props.quantity))

function clampQuantity(value: unknown): number {
  const parsed = typeof value === 'number' ? value : Number.parseInt(String(value), 10)
  if (!Number.isSafeInteger(parsed)) return 1
  return Math.min(99, Math.max(1, parsed))
}

function isSelectedOffer(offer: { subscription: UserSubscription }): boolean {
  return props.selectedSubscriptionId !== null && offer.subscription.id === props.selectedSubscriptionId
}

function isHighlightedOffer(offer: { subscription: UserSubscription }): boolean {
  return isSelectedOffer(offer) || (props.selectedSubscriptionId === null && props.targetSubscriptionId === offer.subscription.id)
}

function focusOffer(subscriptionId = props.selectedSubscriptionId ?? props.targetSubscriptionId): boolean {
  const offer = subscriptionId === null
    ? offers.value[0]
    : offers.value.find(item => item.subscription.id === subscriptionId)
  if (!offer) return false

  const button = shopRef.value?.querySelector<HTMLButtonElement>(
    `[data-reset-card-offer="${offer.subscription.id}"]`,
  )
  if (!button) return false

  button.scrollIntoView?.({ behavior: 'smooth', block: 'center' })
  button.focus({ preventScroll: true })
  return true
}

defineExpose({ focusOffer })

function errorMessage(value: unknown): string {
  return extractI18nErrorMessage(value, t, 'payment.errors', t('payment.resetShop.failed'))
}

async function select(subscription: UserSubscription) {
  if (props.disabled || loading.value) return
  error.value = ''
  loading.value = true
  try {
    const quote = await getResetCardQuote(subscription.id)
    emit('select', { subscription, quote })
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    loading.value = false
  }
}

function updateQuantity(event: Event) {
  const input = event.target as HTMLInputElement
  emit('updateOptions', { quantity: clampQuantity(input.value), useOnPurchase: props.useOnPurchase })
}

function updateUseOnPurchase(event: Event) {
  const input = event.target as HTMLInputElement
  emit('updateOptions', { quantity: normalizedQuantity.value, useOnPurchase: input.checked })
}

function formatExpiry(value: string): string {
  const expiresAt = Date.parse(value)
  if (!Number.isFinite(expiresAt)) return value
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(new Date(expiresAt))
}
</script>
