<template>
  <div>
    <div v-if="filteredOptions.length > 0" class="grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3">
      <article
        v-for="option in filteredOptions"
        :key="option.amount"
        :class="[
          'payment-product-card',
          isSelected(option)
            ? 'border-primary-500 shadow-md dark:border-primary-400'
            : 'border-gray-200 dark:border-dark-600',
        ]"
      >
        <div :class="['h-1.5', isSelected(option) ? 'bg-primary-500 dark:bg-primary-400' : 'bg-gray-200 dark:bg-dark-600']" />

        <div class="payment-product-card__body">
          <div class="mb-3 flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="mb-2 flex items-center gap-1.5">
                <span class="text-[11px] font-medium text-gray-400 dark:text-dark-500">{{ t('payment.meteredTier') }}</span>
                <span v-if="isSelected(option)" class="rounded-full bg-primary-50 px-2 py-0.5 text-[10px] font-bold text-primary-600 dark:bg-primary-900/30 dark:text-primary-300">
                  {{ t('payment.selectedRechargeTier') }}
                </span>
              </div>
              <h3
                :title="option.label || t('payment.rechargeTierName', { amount: formatAmountValue(option.amount) })"
                class="h-12 min-w-0 break-words [overflow-wrap:anywhere] text-base font-bold leading-6 text-gray-900 dark:text-white line-clamp-2"
              >
                {{ option.label || t('payment.rechargeTierName', { amount: formatAmountValue(option.amount) }) }}
              </h3>
              <p class="mt-1 min-h-10 text-xs leading-relaxed text-gray-500 dark:text-dark-400 line-clamp-2">
                {{ option.description || t('payment.fixedAmount') }}
              </p>
            </div>

            <div class="shrink-0 text-right">
              <div class="flex items-baseline justify-end gap-1">
                <span class="text-xs text-gray-400 dark:text-dark-500">$</span>
                <span class="text-2xl font-extrabold tracking-tight text-primary-600 dark:text-primary-300">{{ formatAmountValue(option.amount) }}</span>
              </div>
              <div v-if="discountPercent(option) > 0" class="mt-1 flex items-center justify-end gap-1.5">
                <span class="text-xs text-gray-400 line-through dark:text-dark-500">${{ formatAmountValue(option.original_price || 0) }}</span>
                <span class="rounded-full bg-emerald-50 px-1.5 py-0.5 text-[10px] font-bold text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300">
                  -{{ discountPercent(option) }}%
                </span>
              </div>
            </div>
          </div>

          <div class="payment-product-card__meta">
            <div class="flex min-w-0 flex-col gap-1">
              <span class="text-gray-400 dark:text-dark-500">{{ t('payment.rateEstimate') }}</span>
              <span class="truncate font-semibold text-gray-700 dark:text-gray-300">{{ formatRate(option.estimated_rate_multiplier) }}</span>
            </div>
            <div class="flex min-w-0 flex-col gap-1 border-l border-gray-200 pl-3 dark:border-dark-600">
              <span class="text-gray-400 dark:text-dark-500">{{ t('payment.tokenEstimate') }}</span>
              <span class="truncate font-semibold text-gray-700 dark:text-gray-300">{{ formatTokens(option.estimated_tokens) }}</span>
            </div>
          </div>

          <div v-if="hasBenefits(option)" class="payment-product-card__benefits space-y-1 border border-emerald-100 bg-emerald-50/60 dark:border-emerald-900/40 dark:bg-emerald-950/20">
            <p v-if="option.balance_bonus && option.balance_bonus > 0" class="font-medium text-emerald-700 dark:text-emerald-300">
              + ${{ option.balance_bonus.toFixed(2) }} {{ t('payment.entitlements.balanceBonus') }}
            </p>
            <p v-if="option.concurrency && option.concurrency > 0" class="font-medium text-emerald-700 dark:text-emerald-300">
              {{ t('payment.entitlements.concurrency', { count: option.concurrency }) }}
            </p>
          </div>
          <div v-else class="payment-product-card__benefits border border-gray-100 bg-gray-50/70 text-gray-500 dark:border-dark-700 dark:bg-dark-700/30 dark:text-dark-400">
            {{ t('payment.standardRechargeBenefits') }}
          </div>

          <div class="flex-1" />

          <button
            type="button"
            :aria-pressed="isSelected(option)"
            :aria-label="`${formatAmount(option.amount)} · ${option.label || t('payment.rechargePreset')}`"
            :class="[
              'payment-product-card__action',
              isSelected(option)
                ? 'bg-primary-600 text-white shadow-sm hover:bg-primary-700 dark:bg-primary-500 dark:hover:bg-primary-400'
                : 'bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-dark-700 dark:text-gray-200 dark:hover:bg-dark-600',
            ]"
            @click="selectAmount(option.amount)"
          >
            {{ isSelected(option) ? t('payment.selectedRechargeTier') : t('payment.selectRechargeTier') }}
          </button>
        </div>
      </article>
    </div>

    <div v-else class="rounded-xl border border-dashed border-gray-300 bg-gray-50 px-5 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:bg-dark-800/50 dark:text-dark-400">
      {{ t('payment.noRechargeOptions') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RechargeOption } from '@/types/payment'

const props = withDefaults(defineProps<{
  amounts?: number[]
  options?: RechargeOption[]
  modelValue: number | null
  min?: number
  max?: number
}>(), {
  amounts: () => [20, 50, 100, 200, 500],
  options: () => [],
  min: 0,
  max: 0,
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

function isSelected(option: RechargeOption): boolean {
  return props.modelValue === option.amount
}

function selectAmount(amount: number) {
  emit('update:modelValue', amount)
}

function formatAmount(value: number): string {
  return `$${value.toFixed(2)}`
}

function formatAmountValue(value: number): string {
  return Number.isInteger(value) ? String(value) : value.toFixed(2)
}

function discountPercent(option: RechargeOption): number {
  if (!option.original_price || option.original_price <= option.amount) return 0
  return Math.max(0, Math.round((1 - option.amount / option.original_price) * 100))
}

function formatRate(value?: number): string {
  if (!value || value <= 0) return t('payment.notConfigured')
  return `×${Number(value.toPrecision(10))}`
}

function formatTokens(value?: number): string {
  if (!value || value <= 0) return t('payment.notConfigured')
  if (value >= 1_000_000_000) return `≈ ${(value / 1_000_000_000).toFixed(value % 1_000_000_000 === 0 ? 0 : 1)}B`
  if (value >= 1_000_000) return `≈ ${(value / 1_000_000).toFixed(value % 1_000_000 === 0 ? 0 : 1)}M`
  if (value >= 1_000) return `≈ ${(value / 1_000).toFixed(value % 1_000 === 0 ? 0 : 1)}K`
  return `≈ ${value}`
}

function hasBenefits(option: RechargeOption): boolean {
  return Boolean((option.balance_bonus && option.balance_bonus > 0) || (option.concurrency && option.concurrency > 0))
}
</script>
