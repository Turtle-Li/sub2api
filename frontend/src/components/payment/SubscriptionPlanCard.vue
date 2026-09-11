<template>
  <article
    role="button"
    tabindex="0"
    :aria-pressed="selected"
    :aria-disabled="plan.eligibility?.can_purchase === false"
    :aria-label="`${plan.name} · ${displayPrice}`"
    :class="[
      'payment-product-card',
      plan.eligibility?.can_purchase === false && 'payment-product-card--unavailable',
      selected && 'payment-product-card--selected',
      featured && 'payment-product-card--featured',
    ]"
    @click="plan.eligibility?.can_purchase !== false && emit('select', plan)"
    @keydown.enter.prevent="plan.eligibility?.can_purchase !== false && emit('select', plan)"
    @keydown.space.prevent="plan.eligibility?.can_purchase !== false && emit('select', plan)"
  >
    <span v-if="featured" class="payment-product-card__ribbon">
      <Icon name="sparkles" size="xs" :stroke-width="2" />
      {{ t('payment.recommended') }}
    </span>

    <span v-if="selected" class="payment-product-card__check" aria-hidden="true">
      <Icon name="check" size="xs" :stroke-width="3" />
    </span>
    <div class="payment-product-card__body">
      <!-- Identity -->
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-1.5">
          <span :class="['inline-flex shrink-0 rounded-md px-1.5 py-0.5 text-[11px] font-medium', badgeLightClass]">{{ pLabel }}</span>
          <span v-if="periodDisplay" class="payment-product-card__eyebrow">{{ periodDisplay }}</span>
          <span v-if="isRenewal" class="text-xs font-medium text-primary-600 dark:text-primary-300">{{ t('payment.renewNow') }}</span>
        </div>
        <h3 :title="plan.name" class="payment-product-card__title mt-2">{{ plan.name }}</h3>
        <p v-if="plan.description" class="mt-1 text-[13px] leading-relaxed text-gray-500 dark:text-dark-400">
          {{ plan.description }}
        </p>
      </div>

      <!-- Price -->
      <div class="min-w-0">
        <div class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <span class="payment-product-card__price">{{ displayPrice }}</span>
          <span class="text-sm text-gray-400 dark:text-dark-500">/ {{ validitySuffix }}</span>
        </div>
        <div v-if="displayOriginalPrice" class="mt-1.5 flex flex-wrap items-center gap-2">
          <span class="payment-product-card__strike">{{ displayOriginalPrice }}</span>
          <span v-if="discountText" class="payment-product-card__discount">{{ discountText }}</span>
        </div>
      </div>

      <PurchaseEligibilityHint :eligibility="plan.eligibility" />

      <!-- Everything the plan includes: paid entitlements first, then quota facts. -->
      <ul class="payment-product-card__list">
        <li
          v-for="item in includedItems"
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

      <p v-if="plan.entitlements?.message" class="text-xs leading-relaxed text-primary-700/80 dark:text-primary-300/80">
        {{ plan.entitlements.message }}
      </p>


    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SubscriptionPlan } from '@/types/payment'
import type { UserSubscription } from '@/types'
import { useAppStore } from '@/stores/app'
import { hasPeakRate as groupHasPeakRate, formatPeakRateWindow, serverTimezoneLabel } from '@/utils/peak-rate'
import { planValiditySuffix, resetCardValidityLabel } from './validity'
import { DEFAULT_PAYMENT_CURRENCY, formatPaymentAmount } from '@/components/payment/currency'
import { subscriptionGatewayAmount } from '@/components/payment/pricing'
import Icon from '@/components/icons/Icon.vue'
import PurchaseEligibilityHint from './PurchaseEligibilityHint.vue'
import {
  platformBadgeLightClass,
  platformLabel,
} from '@/utils/platformColors'

const props = withDefaults(defineProps<{
  plan: SubscriptionPlan
  activeSubscriptions?: UserSubscription[]
  /** Gateway currency the plan will actually be charged in. */
  displayCurrency?: string
  locale?: string
  /** Subscription CNY conversion rate; only applied for the default currency, as on the server. */
  usdToCnyRate?: number
  selected?: boolean
  /** Set by the parent for the single recommended plan in the visible group. */
  featured?: boolean
}>(), {
  activeSubscriptions: undefined,
  displayCurrency: DEFAULT_PAYMENT_CURRENCY,
  locale: undefined,
  usdToCnyRate: 0,
  selected: false,
  featured: false,
})
const emit = defineEmits<{ select: [plan: SubscriptionPlan] }>()
const { t } = useI18n()

const platform = computed(() => props.plan.group_platform || '')
const isRenewal = computed(() =>
  props.activeSubscriptions?.some(s => s.group_id === props.plan.group_id && s.status === 'active') ?? false
)

const badgeLightClass = computed(() => platformBadgeLightClass(platform.value))
const pLabel = computed(() => platformLabel(platform.value))

const periodDisplay = computed(() => {
  const label = String(props.plan.period_label || '').trim().toLowerCase()
  if (label === 'quarter') return t('payment.periods.quarter')
  if (label === 'year') return t('payment.periods.year')
  if (label === 'month') return t('payment.periods.month')
  return ''
})

const discountText = computed(() => {
  if (props.plan.discount_percent && props.plan.discount_percent > 0) return `-${Math.round(props.plan.discount_percent)}%`
  if (!props.plan.original_price || props.plan.original_price <= 0) return ''
  const pct = Math.round((1 - props.plan.price / props.plan.original_price) * 100)
  return pct > 0 ? `-${pct}%` : ''
})

const appStore = useAppStore()

// The list card used to print plan.price behind a hardcoded USD symbol while
// the confirm step converted the same plan into the gateway currency, so one
// plan showed two different prices. Both now go through the server's rule.
function formatGatewayPrice(value: number): string {
  return formatPaymentAmount(
    subscriptionGatewayAmount(value, props.usdToCnyRate, props.displayCurrency),
    props.displayCurrency,
    props.locale,
  )
}

const displayPrice = computed(() => formatGatewayPrice(props.plan.price))
const displayOriginalPrice = computed(() =>
  props.plan.original_price ? formatGatewayPrice(props.plan.original_price) : ''
)

// Bonus balance is platform credit, not a gateway charge.
function formatCredit(value: number): string {
  return `${Number.isInteger(value) ? value : value.toFixed(2)} ${t('payment.creditUnit')}`
}

const hasPeakRate = computed(() => groupHasPeakRate(props.plan))
const validitySuffix = computed(() => planValiditySuffix(props.plan, t))

const MODEL_SCOPE_LABELS: Record<string, string> = {
  claude: 'Claude',
  gemini_text: 'Gemini',
  gemini_image: 'Imagen',
}

const modelScopeLabels = computed(() => {
  if (platform.value !== 'antigravity') return []
  const scopes = props.plan.supported_model_scopes
  if (!scopes || scopes.length === 0) return []
  return scopes.map(s => MODEL_SCOPE_LABELS[s] || s)
})

interface IncludedItem {
  text: string
  /** Paid entitlements carry the accent; quota facts are plain checks. */
  benefit: boolean
}

/**
 * One list for everything the plan includes, ordered by what a buyer decides
 * on: the entitlements they are paying extra for, then the quota facts, then
 * the admin's own feature copy.
 *
 * Reset cards spell out both the count and the validity period. That period is
 * a count plus a unit (day/week/month) on the server, so it goes through
 * resetCardValidityLabel instead of being assumed to be days.
 */
const includedItems = computed<IncludedItem[]>(() => {
  const items: IncludedItem[] = []
  const entitlements = props.plan.entitlements

  if (entitlements?.balance_bonus && entitlements.balance_bonus > 0) {
    items.push({ text: `${t('payment.entitlements.balanceBonus')} +${formatCredit(entitlements.balance_bonus)}`, benefit: true })
  }
  if (entitlements?.reset_card_count && entitlements.reset_card_count > 0) {
    const validity = resetCardValidityLabel(entitlements, t)
    items.push({
      text: `${entitlements.reset_card_count} ${t('payment.entitlements.resetCards', { validity })}`,
      benefit: true,
    })
  }
  if (entitlements?.concurrency && entitlements.concurrency > 0) {
    items.push({ text: t('payment.entitlements.concurrency', { count: entitlements.concurrency }), benefit: true })
  }

  items.push({ text: `${t('payment.planCard.rate')} ×${Number((props.plan.rate_multiplier ?? 1).toPrecision(10))}`, benefit: false })
  if (hasPeakRate.value) {
    items.push({
      text: `${t('payment.planCard.peakRate')} ${formatPeakRateWindow(props.plan, serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset))}`,
      benefit: false,
    })
  }
  if ((props.plan.daily_limit_usd ?? 0) > 0) {
    items.push({ text: `${t('payment.planCard.dailyLimit')} ${formatCredit(props.plan.daily_limit_usd!)}`, benefit: false })
  }
  if ((props.plan.weekly_limit_usd ?? 0) > 0) {
    items.push({ text: `${t('payment.planCard.weeklyLimit')} ${formatCredit(props.plan.weekly_limit_usd!)}`, benefit: false })
  }
  if ((props.plan.monthly_limit_usd ?? 0) > 0) {
    items.push({ text: `${t('payment.planCard.monthlyLimit')} ${formatCredit(props.plan.monthly_limit_usd!)}`, benefit: false })
  }
  if ([props.plan.daily_limit_usd, props.plan.weekly_limit_usd, props.plan.monthly_limit_usd].every(limit => (limit ?? 0) <= 0)) {
    items.push({ text: `${t('payment.planCard.quota')} ${t('payment.planCard.unlimited')}`, benefit: false })
  }
  if (modelScopeLabels.value.length > 0) {
    items.push({ text: `${t('payment.planCard.models')} ${modelScopeLabels.value.join(' / ')}`, benefit: false })
  }
  for (const feature of props.plan.features || []) {
    items.push({ text: feature, benefit: false })
  }
  return items
})
</script>
