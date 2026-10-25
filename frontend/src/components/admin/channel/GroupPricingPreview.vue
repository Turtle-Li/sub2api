<template>
  <div class="mt-3 border-t border-gray-200 pt-3 text-xs dark:border-dark-600" data-testid="group-pricing-preview">
    <p class="font-medium text-gray-700 dark:text-gray-200">{{ t('admin.channels.form.groupPricingPreview') }}</p>
    <p v-if="groups.length === 0" class="mt-1 text-gray-500">{{ t('admin.channels.form.groupPricingSelect') }}</p>
    <ul v-else class="mt-2 space-y-1.5">
      <li v-for="group in groups" :key="group.id" class="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span class="font-medium text-gray-700 dark:text-gray-200">{{ group.name }}</span>
        <span class="text-primary-600 dark:text-primary-400">{{ group.rate_multiplier }}x</span>
        <span v-if="hasGroupPricing(group)" class="text-gray-500 dark:text-gray-400">{{ t('admin.channels.form.groupPricingOverride') }}</span>
        <template v-else>
          <span>{{ t('admin.channels.form.inputPrice') }} {{ price(group, entry.input_price) }}</span>
          <span>{{ t('admin.channels.form.outputPrice') }} {{ price(group, entry.output_price) }}</span>
        </template>
      </li>
    </ul>
    <p class="mt-2 text-gray-500 dark:text-gray-400">{{ t('admin.channels.form.groupPricingPreviewHint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import type { AdminGroup } from '@/types'
import { formatScaled } from '@/utils/pricing'
import { pricingCurrencyFromPublicSettings } from '@/utils/settlementCurrency'
import { normalizePricingCurrency, type PricingFormEntry } from './types'

type PreviewGroup = Pick<AdminGroup, 'id' | 'name' | 'platform' | 'subscription_type' | 'rate_multiplier' | 'model_pricing'>

const props = defineProps<{
  entry: PricingFormEntry
  groups: readonly PreviewGroup[]
}>()
const { t } = useI18n()
const appStore = useAppStore()
const policy = computed(() => pricingCurrencyFromPublicSettings(appStore.cachedPublicSettings))

function normalizeModel(model: string): string {
  const name = model.trim().toLowerCase()
  return name.startsWith('claude-') ? name.replace(/\./g, '-') : name
}

function hasGroupPricing(group: PreviewGroup): boolean {
  return (group.model_pricing ?? []).some(card => card.models.some(pattern => {
    const name = normalizeModel(pattern)
    return props.entry.models.some(model => {
      const requested = normalizeModel(model)
      return requested === name || (name.endsWith('*') && requested.startsWith(name.slice(0, -1)))
    })
  }))
}

function price(group: PreviewGroup, value: number | string | null): string {
  if (value == null || value === '') return '-'
  const usd = Number(value) / (normalizePricingCurrency(props.entry.currency) === 'CNY' ? policy.value.usdToCNYRate : 1)
  const direct = group.subscription_type === 'standard' && ['openai', 'anthropic'].includes(group.platform)
  const settlementRate = policy.value.settlementCurrency === 'CNY' && group.subscription_type !== 'subscription' && !direct
    ? policy.value.usdToCNYRate : 1
  return formatScaled(usd * settlementRate * group.rate_multiplier, 1, 2)
}
</script>
