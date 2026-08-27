<template>
  <div>
    <div v-if="filteredOptions.length > 0" class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
      <article
        v-for="option in filteredOptions"
        :key="option.amount"
        role="button"
        tabindex="0"
        :aria-pressed="isSelected(option)"
        :aria-label="`${formatAmount(option.amount)} · ${tierName(option)}`"
        :class="[
          'payment-product-card',
          isSelected(option) && 'payment-product-card--selected',
          isFeatured(option) && 'payment-product-card--featured',
        ]"
        @click="selectAmount(option.amount)"
        @keydown.enter.prevent="selectAmount(option.amount)"
        @keydown.space.prevent="selectAmount(option.amount)"
      >
        <span v-if="isFeatured(option)" class="payment-product-card__ribbon">
          <Icon name="sparkles" size="xs" :stroke-width="2" />
          {{ t('payment.bestValue') }}
        </span>

        <div class="payment-product-card__body">
          <!-- Identity -->
          <div class="min-w-0">
            <p class="payment-product-card__eyebrow">{{ t('payment.meteredTier') }}</p>
            <h3 :title="tierName(option)" class="payment-product-card__title mt-1.5">{{ tierName(option) }}</h3>
            <p v-if="option.description" class="mt-1 text-[13px] leading-relaxed text-gray-500 dark:text-dark-400">
              {{ option.description }}
            </p>
          </div>

          <!-- Price -->
          <div class="min-w-0">
            <div class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
              <span class="payment-product-card__price">{{ formatAmount(option.amount) }}</span>
              <template v-if="discountPercent(option) > 0">
                <span class="payment-product-card__strike">{{ formatAmount(option.original_price || 0) }}</span>
                <span class="payment-product-card__discount">-{{ discountPercent(option) }}%</span>
              </template>
            </div>
            <p v-if="feeRate > 0" class="mt-1.5 text-xs text-gray-400 dark:text-dark-500">
              {{ t('payment.plusFee', { rate: feeRate }) }}
            </p>
          </div>

          <!-- What lands in the account -->
          <div class="payment-product-card__credit">
            <span>{{ t('payment.creditedBalance') }}</span>
            <span class="font-semibold text-gray-900 dark:text-white">{{ formatCredit(creditedFor(option)) }}</span>
          </div>

          <!-- Benefits and estimates. Absent data renders nothing at all. -->
          <ul v-if="listItems(option).length > 0" class="payment-product-card__list">
            <li
              v-for="item in listItems(option)"
              :key="item.text"
              :class="['payment-product-card__list-item', item.benefit && 'payment-product-card__list-item--benefit']"
            >
              <Icon
                :name="item.benefit ? 'sparkles' : 'check'"
                size="xs"
                :stroke-width="2.2"
                :class="['mt-[3px] shrink-0', item.benefit ? 'text-primary-500 dark:text-primary-400' : 'text-gray-300 dark:text-dark-600']"
              />
              <span class="min-w-0">{{ item.text }}</span>
            </li>
          </ul>

          <button
            type="button"
            tabindex="-1"
            :class="[
              'payment-product-card__action',
              isSelected(option)
                ? 'bg-primary-600 text-white hover:bg-primary-700 dark:bg-primary-500 dark:hover:bg-primary-400'
                : 'bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-dark-700 dark:text-gray-200 dark:hover:bg-dark-600',
            ]"
            @click.stop="selectAmount(option.amount)"
          >
            {{ isSelected(option) ? t('payment.selectedRechargeTier') : t('payment.selectRechargeTier') }}
          </button>
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

// Highlight at most one tier, and only when the discount actually distinguishes
// it. Marking several "best value" says nothing; marking one with no discount
// to back it up is a claim we cannot support from the configured data.
const featuredAmount = computed<number | null>(() => {
  let best: RechargeOption | null = null
  for (const option of filteredOptions.value) {
    const percent = discountPercent(option)
    if (percent <= 0) continue
    if (!best || percent > discountPercent(best)) best = option
  }
  return best ? best.amount : null
})

function isFeatured(option: RechargeOption): boolean {
  return featuredAmount.value === option.amount
}

function isSelected(option: RechargeOption): boolean {
  return props.modelValue === option.amount
}

function selectAmount(amount: number) {
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
function formatCredit(value: number): string {
  return `${Number.isInteger(value) ? value : value.toFixed(2)} ${t('payment.creditUnit')}`
}

function formatAmountValue(value: number): string {
  return Number.isInteger(value) ? String(value) : value.toFixed(2)
}

function creditedFor(option: RechargeOption): number {
  return creditedBalanceAmount(option.amount, props.balanceMultiplier, option.balance_bonus || 0)
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
  if (option.balance_bonus && option.balance_bonus > 0) {
    items.push({ text: `${t('payment.entitlements.balanceBonus')} +${formatCredit(option.balance_bonus)}`, benefit: true })
  }
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
