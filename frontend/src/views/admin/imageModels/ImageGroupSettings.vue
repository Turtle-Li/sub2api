<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import type { ImageGroupDraft } from './imageGroupDraft'
const draft = defineModel<ImageGroupDraft>({ required: true })
defineProps<{ platform: string; rate: number }>()
const { t } = useI18n()
const tiers = ['1k', '2k', '4k'] as const
function setPrice(tier: typeof tiers[number], event: Event) {
  if (!(event.target instanceof HTMLInputElement)) return
  draft.value[`image_price_${tier}`] = event.target.value === '' ? null : event.target.valueAsNumber
}
</script>

<template>
  <section class="space-y-5" aria-labelledby="image-access-title">
    <div>
      <h2 id="image-access-title" class="text-base font-semibold">{{ t('admin.imageModels.access') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageModels.groupScope') }}</p>
    </div>
    <div class="flex flex-wrap gap-x-8 gap-y-4">
      <label class="flex items-center gap-3 text-sm">
        <Toggle v-model="draft.allow_image_generation" :aria-label="t('admin.imageModels.allowImage')"
          @update:model-value="value => { if (!value) draft.allow_batch_image_generation = false }" />
        {{ t('admin.imageModels.allowImage') }}
      </label>
      <label class="flex items-center gap-3 text-sm">
        <Toggle v-model="draft.allow_batch_image_generation" :disabled="!draft.allow_image_generation"
          :aria-label="t('admin.imageModels.allowBatch')" />
        {{ t('admin.imageModels.allowBatch') }}
      </label>
    </div>
    <div class="border-t border-gray-200 pt-5 dark:border-dark-600">
      <h3 class="font-medium">{{ t('admin.imageModels.pricing') }}</h3>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageModels.pricingHint') }}</p>
      <div class="mt-4 grid gap-4 sm:grid-cols-3">
        <label v-for="tier in tiers" :key="tier" class="space-y-1.5 text-sm">
          <span>{{ tier.toUpperCase() }} (USD source)</span>
          <input type="number" min="0" step="any" :value="draft[`image_price_${tier}`]"
            :placeholder="t('admin.imageModels.defaultPrice')" class="input" @input="setPrice(tier, $event)" />
        </label>
      </div>
      <div class="mt-4 flex flex-wrap items-center gap-4">
        <label class="flex items-center gap-3 text-sm">
          <Toggle v-model="draft.image_rate_independent" :aria-label="t('admin.imageModels.independentRate')" />
          {{ t('admin.imageModels.independentRate') }}
        </label>
        <label v-if="draft.image_rate_independent" class="flex items-center gap-3 text-sm">
          {{ t('admin.imageModels.rate') }}
          <input v-model.number="draft.image_rate_multiplier" class="input w-28" type="number" min="0" step="any" required />
        </label>
        <span v-else class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageModels.groupRate', { rate }) }}</span>
      </div>
      <div v-if="platform === 'gemini'" class="mt-4 grid gap-4 sm:grid-cols-2">
        <label class="space-y-1.5 text-sm">{{ t('admin.imageModels.discount') }}
          <input v-model.number="draft.batch_image_discount_multiplier" class="input" type="number" min="0" step="any" required />
        </label>
        <label class="space-y-1.5 text-sm">{{ t('admin.imageModels.hold') }}
          <input v-model.number="draft.batch_image_hold_multiplier" class="input" type="number" :min="draft.batch_image_discount_multiplier" step="any" required />
        </label>
      </div>
    </div>
  </section>
</template>
