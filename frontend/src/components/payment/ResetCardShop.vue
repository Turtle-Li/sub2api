<template>
  <section ref="shopRef" aria-labelledby="reset-card-shop-title" class="mt-7 border-t border-gray-200 pt-5 dark:border-dark-700" tabindex="-1">
    <div class="flex items-center gap-2">
      <Icon name="refresh" size="sm" class="text-primary-600 dark:text-primary-400" />
      <h2 id="reset-card-shop-title" class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('payment.resetShop.title') }}</h2>
    </div>
    <p v-if="!offers.length" class="mt-3 text-sm text-gray-500">{{ t('payment.resetShop.requiresSubscription') }}</p>

    <div v-else class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
      <article
        v-for="offer in offers"
        :key="offer.subscription.id"
        :class="['payment-product-card', isSelectedOffer(offer) && 'payment-product-card--selected', offer.eligibility?.can_purchase === false && 'payment-product-card--unavailable']"
        @click="select(offer.subscription)"
      >
        <span v-if="isSelectedOffer(offer)" class="payment-product-card__check" aria-hidden="true">
          <Icon name="check" size="xs" :stroke-width="3" />
        </span>
        <div class="payment-product-card__body">
          <h3 class="payment-product-card__title pr-5">{{ offer.title }}</h3>
          <div class="flex flex-wrap items-baseline gap-2">
            <span class="payment-product-card__price">{{ formatPaymentAmount(isSelectedOffer(offer) && selectedQuote ? selectedQuote.price : offer.price, 'CNY') }}</span>
            <span class="text-sm text-gray-500 dark:text-dark-400">/ {{ t('payment.resetShop.perCard') }}</span>
          </div>
          <p v-if="offer.description" class="text-[13px] leading-relaxed text-gray-600 dark:text-dark-300">{{ offer.description }}</p>
          <PurchaseEligibilityHint :eligibility="offer.eligibility" />
          <div class="flex items-baseline justify-between gap-3 text-xs text-gray-500 dark:text-dark-400">
            <span class="shrink-0">{{ t('payment.resetShop.validity') }}</span>
            <span class="text-right tabular-nums text-gray-700 dark:text-dark-200">{{ t('payment.resetShop.validDays', { days: isSelectedOffer(offer) && selectedQuote?.validity_days ? selectedQuote.validity_days : 15 }) }}</span>
          </div>
          <div class="mt-auto border-t border-gray-100 pt-3 dark:border-dark-700" data-test="reset-card-options" @click.stop>
            <div class="flex items-center justify-between gap-3 text-sm text-gray-700 dark:text-dark-200">
              <span>{{ t('payment.resetShop.quantity') }}</span>
              <div role="group" :aria-label="t('payment.resetShop.quantity')" class="inline-flex items-center rounded-lg border border-gray-200 dark:border-dark-600">
                <button
                  type="button"
                  data-test="reset-card-decrease"
                  class="flex h-10 w-10 items-center justify-center rounded-l-lg hover:bg-gray-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-dark-700"
                  :aria-label="t('payment.resetShop.decrease')"
                  :disabled="disabled || !isSelectedOffer(offer) || normalizedQuantity <= 1"
                  @click="changeQuantity(-1)"
                ><Icon name="minus" size="sm" /></button>
                <output data-test="reset-card-quantity" class="min-w-8 text-center font-medium tabular-nums" aria-live="polite">{{ isSelectedOffer(offer) ? normalizedQuantity : 1 }}</output>
                <button
                  type="button"
                  data-test="reset-card-increase"
                  class="flex h-10 w-10 items-center justify-center rounded-r-lg hover:bg-gray-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-dark-700"
                  :aria-label="t('payment.resetShop.increase')"
                  :disabled="disabled || !isSelectedOffer(offer) || normalizedQuantity >= 99"
                  @click="changeQuantity(1)"
                ><Icon name="plus" size="sm" /></button>
              </div>
            </div>
            <label class="mt-2 flex min-h-10 items-center gap-2 text-sm text-gray-700 dark:text-dark-200" :class="isSelectedOffer(offer) && !disabled ? 'cursor-pointer' : 'cursor-not-allowed opacity-60'">
              <input
                data-test="reset-card-use-on-purchase"
                class="peer sr-only"
                type="checkbox"
                :checked="isSelectedOffer(offer) && useOnPurchase"
                :disabled="disabled || !isSelectedOffer(offer)"
                @change="updateUseOnPurchase"
              />
              <span aria-hidden="true" class="flex h-5 w-5 shrink-0 items-center justify-center rounded-md border border-gray-300 bg-white text-transparent transition-colors peer-checked:border-primary-600 peer-checked:bg-primary-600 peer-checked:text-white peer-focus-visible:ring-2 peer-focus-visible:ring-primary-500 peer-focus-visible:ring-offset-2 dark:border-dark-500 dark:bg-dark-900 dark:peer-checked:border-primary-500 dark:peer-checked:bg-primary-500 dark:peer-focus-visible:ring-offset-dark-800">
                <Icon name="check" size="xs" :stroke-width="3" />
              </span>
              <span>{{ t('payment.resetShop.useOnPurchase') }}</span>
            </label>
          </div>
          <button
            type="button"
            :data-reset-card-offer="offer.subscription.id"
            :aria-pressed="isSelectedOffer(offer)"
            :aria-label="`${offer.title} · ${t('payment.resetShop.select')}`"
            :class="['payment-product-card__action', isSelectedOffer(offer) ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300' : 'bg-gray-100 text-gray-800 hover:bg-gray-200 dark:bg-dark-700 dark:text-dark-100 dark:hover:bg-dark-600']"
            :disabled="disabled || loading || offer.eligibility?.can_purchase === false"
            @click.stop="select(offer.subscription)"
          >
            {{ isSelectedOffer(offer) ? t('payment.selectedRechargeTier') : t('payment.resetShop.select') }}
          </button>
        </div>
      </article>
    </div>

    <p v-if="error" role="alert" class="mt-3 text-sm text-red-600 dark:text-red-400">{{ error }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getResetCardQuote, type ResetCardQuote } from '@/api/subscriptions'
import { extractI18nErrorMessage } from '@/utils/apiError'
import type { UserSubscription } from '@/types'
import type { SubscriptionPlan } from '@/types/payment'
import Icon from '@/components/icons/Icon.vue'
import { formatPaymentAmount } from './currency'
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

// Warm eligible quotes while the customer reads the plans. The server still
// revalidates price, tier and eligibility when creating the actual order.
const quoteCache = new Map<number, { quote: ResetCardQuote; fetchedAt: number }>()
const quoteRequests = new Map<number, Promise<ResetCardQuote>>()
let quoteGeneration = 0

function loadQuote(subscriptionId: number, force = false): Promise<ResetCardQuote> {
  const cached = quoteCache.get(subscriptionId)
  if (!force && cached && Date.now() - cached.fetchedAt < 60_000) return Promise.resolve(cached.quote)
  const pending = quoteRequests.get(subscriptionId)
  if (pending) return pending
  const generation = quoteGeneration
  const request = getResetCardQuote(subscriptionId).then(quote => {
    if (generation === quoteGeneration) quoteCache.set(subscriptionId, { quote, fetchedAt: Date.now() })
    return quote
  }).finally(() => {
    if (quoteRequests.get(subscriptionId) === request) quoteRequests.delete(subscriptionId)
  })
  quoteRequests.set(subscriptionId, request)
  return request
}

watch(() => [props.subscriptions, props.plans], () => {
  quoteGeneration++
  quoteCache.clear()
  quoteRequests.clear()
  for (const offer of offers.value) {
    if (offer.eligibility?.can_purchase !== false) void loadQuote(offer.subscription.id).catch(() => {})
  }
}, { immediate: true, deep: true })

async function select(subscription: UserSubscription) {
  if (props.disabled || loading.value ||
    !offers.value.some(offer => offer.subscription.id === subscription.id && offer.eligibility?.can_purchase !== false)) return
  error.value = ''
  loading.value = true
  const generation = quoteGeneration
  try {
    // Clicking an already selected card also gives a stale-quote error a
    // direct retry path without requiring a page reload.
    const quote = await loadQuote(subscription.id, props.selectedSubscriptionId === subscription.id)
    if (generation === quoteGeneration && !props.disabled) emit('select', { subscription, quote })
  } catch (err) {
    if (generation === quoteGeneration) error.value = errorMessage(err)
  } finally {
    loading.value = false
  }
}

function changeQuantity(delta: number) {
  if (props.disabled) return
  emit('updateOptions', { quantity: clampQuantity(normalizedQuantity.value + delta), useOnPurchase: props.useOnPurchase })
}

function updateUseOnPurchase(event: Event) {
  const input = event.target as HTMLInputElement
  emit('updateOptions', { quantity: normalizedQuantity.value, useOnPurchase: input.checked })
}

</script>
