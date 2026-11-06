<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import type { ImageModelCatalogue } from '@/api/admin/imageModels'
import { imageRuleCovers, parseImageRules, toggleExactImageRule, type ImageGroupDraft } from './imageGroupDraft'
const draft = defineModel<ImageGroupDraft>({ required: true })
const props = defineProps<{ catalogue: ImageModelCatalogue; dirty: boolean }>()
const { t, te } = useI18n()
const search = ref('')
function providerName(provider: string) {
  const key = `admin.imageModels.providers.${provider}`
  return te(key) ? t(key) : provider
}
const rules = computed(() => parseImageRules(draft.value.rules))
const candidates = computed(() => props.catalogue.candidates.filter(model =>
  `${model.id} ${providerName(model.provider)}`.toLowerCase().includes(search.value.toLowerCase())))
const available = computed(() => new Set(props.catalogue.models.map(model => `${model.provider}:${model.id}`)))
const reason = computed(() => {
  const key = `admin.imageModels.reasons.${props.catalogue.unavailable_reason}`
  return te(key) ? t(key) : t('admin.imageModels.reasons.unknown')
})
const coveredByRule = (model: string) => rules.value.some(rule => rule.toLowerCase() !== model.toLowerCase() && imageRuleCovers(rule, model))
const selected = (model: string) => !draft.value.allowlistEnabled || rules.value.some(rule => imageRuleCovers(rule, model))
</script>

<template>
  <section class="space-y-4 border-t border-gray-200 pt-6 dark:border-dark-600" aria-labelledby="image-models-title">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 id="image-models-title" class="text-base font-semibold">{{ t('admin.imageModels.models') }}</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageModels.readinessHint') }}</p>
      </div>
      <label class="flex items-center gap-3 text-sm">
        <Toggle v-model="draft.allowlistEnabled" :aria-label="t('admin.imageModels.enableAllowlist')" />
        {{ t('admin.imageModels.enableAllowlist') }}
      </label>
    </div>
    <p class="rounded-lg bg-amber-50 p-3 text-sm text-amber-900 dark:bg-amber-900/20 dark:text-amber-200">
      {{ t('admin.imageModels.sharedWarning') }}
    </p>
    <label v-if="draft.allowlistEnabled" class="block space-y-2 text-sm">
      <span class="font-medium">{{ t('admin.imageModels.allRules') }}</span>
      <textarea v-model="draft.rules" required rows="4" class="input font-mono text-xs" :placeholder="t('admin.imageModels.rulesPlaceholder')" />
      <span class="block text-gray-500 dark:text-gray-400">{{ t('admin.imageModels.rulesHint') }}</span>
    </label>
    <p v-else class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageModels.unrestricted') }}</p>
    <div class="flex flex-wrap items-center justify-between gap-3">
      <label class="min-w-0 flex-1 sm:max-w-xs">
        <span class="sr-only">{{ t('admin.imageModels.searchModels') }}</span>
        <input v-model="search" class="input" :placeholder="t('admin.imageModels.searchModels')" type="search" />
      </label>
      <span class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageModels.readyCount', { count: catalogue.models.length }) }}</span>
    </div>
    <p v-if="dirty" class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.imageModels.savedStatus') }}</p>
    <p v-if="catalogue.unavailable_reason" class="text-sm text-gray-600 dark:text-gray-300">{{ reason }}</p>
    <ul v-if="candidates.length" class="max-h-[30rem] divide-y divide-gray-200 overflow-y-auto dark:divide-dark-600">
      <li v-for="model in candidates" :key="`${model.provider}:${model.id}`" class="flex items-start gap-3 py-3">
        <input :id="`model-${model.provider}-${model.id}`" type="checkbox" class="mt-1 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
          :checked="selected(model.id)" :disabled="!draft.allowlistEnabled || coveredByRule(model.id)"
          @change="draft.rules = toggleExactImageRule(draft.rules, model.id)" />
        <div class="min-w-0 flex-1">
          <label :for="`model-${model.provider}-${model.id}`" class="break-all text-sm font-medium">{{ model.id }}</label>
          <div class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
            <span>{{ providerName(model.provider) }}</span>
            <span v-if="model.capabilities">{{ model.capabilities.image_sizes.join(' / ') }} · {{ t('admin.imageModels.references', { count: model.capabilities.max_reference_images }) }}</span>
            <span v-if="coveredByRule(model.id)">{{ t('admin.imageModels.coveredRule') }}</span>
          </div>
        </div>
        <span class="shrink-0 rounded px-2 py-1 text-xs"
          :class="available.has(`${model.provider}:${model.id}`) ? 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'">
          {{ t(available.has(`${model.provider}:${model.id}`) ? 'admin.imageModels.ready' : 'admin.imageModels.candidate') }}
        </span>
      </li>
    </ul>
    <p v-else class="py-6 text-center text-sm text-gray-500">{{ t('admin.imageModels.noModels') }}</p>
  </section>
</template>
