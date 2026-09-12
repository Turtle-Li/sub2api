<template>
  <div class="flex justify-between gap-2">
    <span class="text-gray-500 dark:text-gray-400">{{ label }}</span>
    <span class="font-mono">{{ display }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { formatScaled } from '@/utils/pricing'
import {
  convertPriceCurrency,
  pricingCurrencyFromPublicSettings,
  settlementCurrencySymbol
} from '@/utils/settlementCurrency'
import { useAppStore } from '@/stores/app'

const props = withDefaults(
  defineProps<{
    label: string
    value: number | null
    unit: string
    scale: number
    sourceCurrency?: 'USD' | 'CNY'
  }>(),
  { value: null, sourceCurrency: 'USD' }
)

const appStore = useAppStore()
const pricingCurrency = computed(() => pricingCurrencyFromPublicSettings(appStore.cachedPublicSettings))

const display = computed(() => {
  const converted = convertPriceCurrency(props.value, props.sourceCurrency, pricingCurrency.value)
  if (converted == null) return '-'
  const formatted = formatScaled(converted, props.scale).replace(
    /^\$/,
    settlementCurrencySymbol(pricingCurrency.value.settlementCurrency)
  )
  const symbol = settlementCurrencySymbol(pricingCurrency.value.settlementCurrency)
  const unit = symbol === '$' ? props.unit : props.unit.replace(/\$/g, symbol)
  return `${formatted} ${unit}`
})
</script>
