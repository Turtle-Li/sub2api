<template>
  <div class="payment-recharge-options">
    <div v-if="filteredOptions.length > 0" class="payment-recharge-grid">
      <article
        v-for="option in filteredOptions"
        :key="option.amount"
        role="button"
        tabindex="0"
        :aria-pressed="isSelected(option)"
        :aria-disabled="option.eligibility?.can_purchase === false"
        :class="[
          'payment-product-card payment-recharge-card',
          option.eligibility?.can_purchase === false && 'payment-product-card--unavailable',
          isSelected(option) && 'payment-product-card--selected',
          isFeatured(option) && 'payment-product-card--featured',
        ]"
        @click="selectAmount(option.amount)"
        @keydown.enter.prevent="selectAmount(option.amount)"
        @keydown.space.prevent="selectAmount(option.amount)"
      >
        <span v-if="isFeatured(option)" class="payment-product-card__ribbon">
          <Icon name="sparkles" size="xs" :stroke-width="2" />
          {{ t(option.recommended ? 'payment.recommended' : 'payment.bestValue') }}
        </span>

        <span v-if="isSelected(option)" class="payment-product-card__check" aria-hidden="true">
          <Icon name="check" size="xs" :stroke-width="3" />
        </span>
        <div class="payment-product-card__body">
          <!-- Identity -->
          <div class="min-w-0">
            <h3 :title="tierName(option)" class="payment-product-card__title pr-5">{{ tierName(option) }}</h3>
          </div>

          <!-- Price. The list price and discount get their own row rather than
               wrapping out of the headline, so every card breaks in the same
               place regardless of how long its numbers are. -->
          <div class="mt-4 min-w-0">
            <span class="payment-product-card__price">{{ formatAmount(option.amount) }}</span>
            <div v-if="discountPercent(option) > 0" class="mt-1.5 flex flex-wrap items-center gap-2">
              <span class="payment-product-card__strike">{{ formatAmount(option.original_price || 0) }}</span>
              <span class="payment-product-card__discount">-{{ discountPercent(option) }}%</span>
            </div>
            <p v-if="feeRate > 0" class="mt-1.5 text-xs text-gray-400 dark:text-dark-500">
              {{ t('payment.plusFee', { rate: feeRate }) }}
            </p>
          </div>

          <!-- What actually lands. The credited balance and any bonus share one
               panel, so a tier without a bonus has no empty box beside its
               number. -->
          <div class="payment-recharge-card__credit">
            <div class="payment-recharge-card__credit-heading">
              <div class="min-w-0">
                <span class="payment-recharge-card__credit-label">{{ t('payment.creditedBalance') }}</span>
                <strong class="payment-recharge-card__credit-value">{{ formatAmountValue(creditedFor(option)) }} <span class="payment-recharge-card__credit-unit">{{ t('payment.creditUnit') }}</span></strong>
              </div>
              <div v-if="hasBalanceBonus(option)" class="payment-recharge-card__bonus-side">
                <span class="payment-recharge-card__bonus-label">{{ t('payment.bonusIncluded') }}</span>
                <span class="payment-recharge-card__bonus">
                  <Icon name="gift" size="sm" :stroke-width="1.8" />
                  +{{ formatAmountValue(option.balance_bonus || 0) }} <span class="payment-recharge-card__bonus-unit">{{ t('payment.creditUnit') }}</span>
                </span>
              </div>
            </div>
          </div>

          <PurchaseEligibilityHint :eligibility="option.eligibility" class="mt-3" />

          <!-- Benefits, estimates and the admin's own copy. Absent data renders
               nothing at all. -->
          <div v-if="listItems(option).length > 0 || visibleDescription(option)" class="payment-recharge-card__footer">
            <ul v-if="listItems(option).length > 0" class="payment-product-card__list">
              <li
                v-for="item in listItems(option)"
                :key="item.text"
                :class="['payment-product-card__list-item', item.benefit && 'payment-product-card__list-item--benefit']"
              >
                <Icon
                  name="check"
                  size="xs"
                  :stroke-width="2.2"
                  :class="['mt-[4px] shrink-0', item.benefit ? 'text-primary-500 dark:text-primary-400' : 'text-gray-300 dark:text-dark-600']"
                />
                <span class="min-w-0">{{ item.text }}</span>
              </li>
            </ul>
            <p v-if="visibleDescription(option)" class="payment-recharge-card__description">
              {{ visibleDescription(option) }}
            </p>
          </div>
        </div>
      </article>
    </div>

    <div v-else class="rounded-2xl border border-dashed border-gray-300 bg-gray-50/60 px-5 py-12 text-center dark:border-dark-600 dark:bg-dark-800/50">
      <Icon name="creditCard" size="lg" class="mx-auto mb-3 text-gray-300 dark:text-dark-600" />
      <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('payment.noRechargeOptions') }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RechargeOption } from '@/types/payment'
import Icon from '@/components/icons/Icon.vue'
import PurchaseEligibilityHint from './PurchaseEligibilityHint.vue'
import { DEFAULT_PAYMENT_CURRENCY, formatPaymentAmount } from './currency'
import { creditedBalanceAmount } from './pricing'

const props = withDefaults(defineProps<{
  amounts?: number[]
  options?: RechargeOption[]
  modelValue: number | null
  min?: number
  max?: number
  /** Gateway currency the tier amounts are charged in. */
  currency?: string
  locale?: string
  /** Gateway fee percentage, surfaced so the tier price is not read as the total. */
  feeRate?: number
  /** Global balance multiplier, so each card can show what actually lands. */
  balanceMultiplier?: number
}>(), {
  amounts: () => [20, 50, 100, 200, 500],
  options: () => [],
  min: 0,
  max: 0,
  currency: DEFAULT_PAYMENT_CURRENCY,
  locale: undefined,
  feeRate: 0,
  balanceMultiplier: 1,
})

const emit = defineEmits<{
  'update:modelValue': [value: number]
}>()

const { t } = useI18n()

const normalizedOptions = computed<RechargeOption[]>(() =>
  props.options.length > 0
    ? props.options
    : props.amounts.map((amount, sort_order) => ({ amount, sort_order, enabled: true }))
)

const filteredOptions = computed(() =>
  normalizedOptions.value.filter((option) =>
    option.enabled
    && (props.min <= 0 || option.amount >= props.min)
    && (props.max <= 0 || option.amount <= props.max)
  )
)

// Prefer the admin-selected recommendation. The discount fallback keeps older
// configurations useful until an admin explicitly selects a tier.
const featuredAmount = computed<number | null>(() => {
  const recommended = filteredOptions.value.find(option => option.recommended)
  if (recommended) return recommended.amount

  let best: RechargeOption | null = null
  for (const option of filteredOptions.value) {
    const percent = discountPercent(option)
    if (percent <= 0) continue
    if (!best || percent > discountPercent(best)) best = option
  }
  return best ? best.amount : null
})

// A locked tier keeps its admin-chosen recommendation priority but shows no
// marketing emphasis: a card that cannot be bought carries no ribbon.
function isFeatured(option: RechargeOption): boolean {
  return featuredAmount.value === option.amount && option.eligibility?.can_purchase !== false
}

// A previously selected tier that is now gated drops the selected appearance
// (ring, check, aria-pressed). The model value itself is left untouched.
function isSelected(option: RechargeOption): boolean {
  return props.modelValue === option.amount && option.eligibility?.can_purchase !== false
}

function selectAmount(amount: number) {
  if (filteredOptions.value.find(option => option.amount === amount)?.eligibility?.can_purchase === false) return
  emit('update:modelValue', amount)
}

function tierName(option: RechargeOption): string {
  return option.label || t('payment.rechargeTierName', { amount: formatAmountValue(option.amount) })
}

// Tier amounts are charged in the gateway currency, so they must be formatted
// with it. Hardcoding "$" put a dollar sign on the card and a yuan sign in the
// order summary directly below it, for the same money.
function formatAmount(value: number): string {
  return formatPaymentAmount(value, props.currency, props.locale)
}

// Balance is platform credit, not a gateway charge. Labelling it with a
// currency symbol conflates the two units.
function formatAmountValue(value: number): string {
  return Number.isInteger(value) ? String(value) : value.toFixed(2)
}

function creditedFor(option: RechargeOption): number {
  return creditedBalanceAmount(option.amount, props.balanceMultiplier, option.balance_bonus || 0)
}

function hasBalanceBonus(option: RechargeOption): boolean {
  return Boolean(option.balance_bonus && option.balance_bonus > 0)
}

// Some configurations carry an auto-generated description that only restates
// the bonus the credited panel already shows. Exactly that generated sentence
// ("额外赠送 N 额度", N being this tier's formatted bonus) is dropped; any
// other wording, including custom copy that mentions bonuses, stays whole.
function visibleDescription(option: RechargeOption): string {
  const description = (option.description || '').trim().replace(/\s+/g, ' ')
  if (!description) return ''
  if (hasBalanceBonus(option) && description === `额外赠送 ${formatAmountValue(option.balance_bonus || 0)} 额度`) return ''
  return description
}

function discountPercent(option: RechargeOption): number {
  if (!option.original_price || option.original_price <= option.amount) return 0
  return Math.max(0, Math.round((1 - option.amount / option.original_price) * 100))
}

function formatRate(value?: number): string {
  return `×${Number(value!.toPrecision(10))}`
}

function formatTokens(value: number): string {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(value % 1_000_000_000 === 0 ? 0 : 1)}B`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value % 1_000_000 === 0 ? 0 : 1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(value % 1_000 === 0 ? 0 : 1)}K`
  return String(value)
}

interface TierListItem {
  text: string
  /** Paid entitlements carry the accent; estimates are plain facts. */
  benefit: boolean
}

// Unconfigured fields are omitted rather than rendered as "not configured".
// A placeholder telling the reader that information is missing is worse than
// the shorter card.
function listItems(option: RechargeOption): TierListItem[] {
  const items: TierListItem[] = []
  if (option.concurrency && option.concurrency > 0) {
    items.push({ text: t('payment.entitlements.concurrency', { count: option.concurrency }), benefit: true })
  }
  if (option.estimated_rate_multiplier && option.estimated_rate_multiplier > 0) {
    items.push({ text: `${t('payment.rateEstimate')} ${formatRate(option.estimated_rate_multiplier)}`, benefit: false })
  }
  if (option.estimated_tokens && option.estimated_tokens > 0) {
    items.push({ text: `${t('payment.tokenEstimate')} ≈ ${formatTokens(option.estimated_tokens)}`, benefit: false })
  }
  return items
}
</script>

<style scoped>
.payment-recharge-options {
  container-type: inline-size;
}
.payment-recharge-grid {
  @apply grid grid-cols-1 gap-5;
}
/* The sidebar and order summary share the viewport; use the space actually
   available to the cards before introducing a second column. */
@container (min-width: 34rem) {
  .payment-recharge-grid {
    @apply grid-cols-2;
  }
}

/* Each card measures itself, so tight two-column tracks relax the padding and
   the price size before anything wraps awkwardly. */
.payment-recharge-card {
  container-type: inline-size;
}

/* The shared card body uses a uniform 16px rhythm; recharge cards widen the
   padding and give each block its own cadence (16px into the price, 24px into
   the credited panel, 18px into the footer) via the margins below. */
.payment-recharge-card .payment-product-card__body {
  @apply gap-0 p-6;
}
.payment-recharge-card .payment-product-card__title {
  @apply text-lg leading-[26px];
}
.payment-recharge-card .payment-product-card__price {
  @apply text-[40px] leading-[1.1] tracking-[-0.025em] tabular-nums text-primary-700 [overflow-wrap:anywhere] dark:text-primary-300;
}
@container (max-width: 360px) {
  .payment-recharge-card .payment-product-card__body {
    @apply p-5;
  }
  .payment-recharge-card .payment-product-card__price {
    @apply text-[36px];
  }
}

/* Credited platform balance sits directly under the gateway price, one step
   down in weight, so "what I pay" and "what I get" never read as one number. */
.payment-recharge-card__credit {
  @apply mt-6 rounded-xl border border-primary-100/70 bg-primary-50/60 p-4;
  @apply dark:border-primary-800/40 dark:bg-primary-900/15;
}
/* Selection deepens only this panel; the ring and check stay the signal. */
.payment-recharge-card.payment-product-card--selected .payment-recharge-card__credit {
  @apply border-primary-200 bg-primary-50 dark:border-primary-700/50 dark:bg-primary-900/25;
}
.payment-recharge-card__credit-heading {
  @apply flex flex-wrap items-end justify-between gap-x-4 gap-y-3;
}
.payment-recharge-card__credit-label {
  @apply block text-[13px] leading-5 text-gray-600 dark:text-dark-300;
}
.payment-recharge-card__credit-value {
  @apply mt-1 block text-[32px] font-semibold leading-none tracking-tight tabular-nums text-primary-700 dark:text-primary-200;
}
.payment-recharge-card__credit-unit {
  @apply text-sm font-medium tracking-normal;
}
.payment-recharge-card__bonus-side {
  @apply ms-auto flex min-w-0 flex-col items-end gap-1 text-right;
}
.payment-recharge-card__bonus-label {
  @apply text-[13px] leading-5 text-gray-600 dark:text-dark-300;
}
.payment-recharge-card__bonus {
  @apply inline-flex items-baseline gap-1.5 text-xl font-semibold tabular-nums text-primary-700 dark:text-primary-200;
}
.payment-recharge-card__bonus-unit {
  @apply text-[13px] font-medium;
}
/* A narrow card lets the bonus drop to its own left-aligned row instead of
   overflowing the credited number. */
@container (max-width: 280px) {
  .payment-recharge-card__bonus-side {
    @apply ms-0 w-full items-start text-left;
  }
}

.payment-recharge-card__footer {
  @apply mt-[18px] flex flex-col gap-3;
}
.payment-recharge-card .payment-product-card__list {
  @apply gap-2 text-sm leading-[22px];
}
.payment-recharge-card .payment-product-card__list-item,
.payment-recharge-card .payment-product-card__list-item--benefit {
  @apply font-normal text-gray-700 dark:text-dark-200;
}
.payment-recharge-card__description {
  @apply text-[13px] leading-relaxed text-gray-500 [overflow-wrap:anywhere] dark:text-dark-400;
}

/* Locked tiers drop the marketing accents but stay fully readable, including
   the eligibility condition rendered by the hint. */
.payment-recharge-card.payment-product-card--unavailable .payment-product-card__price,
.payment-recharge-card.payment-product-card--unavailable .payment-recharge-card__credit-value,
.payment-recharge-card.payment-product-card--unavailable .payment-recharge-card__bonus {
  @apply text-gray-500 dark:text-dark-400;
}
.payment-recharge-card.payment-product-card--unavailable .payment-recharge-card__credit {
  @apply border-gray-200/80 bg-gray-50 dark:border-dark-700 dark:bg-dark-800/60;
}
</style>
