<template>
  <div class="card">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
        {{ t('admin.settings.openaiModelBilling.title') }}
      </h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.settings.openaiModelBilling.description') }}
      </p>
    </div>
    <div class="space-y-4 p-6">
      <div v-if="loadState === 'loading'" role="status" class="flex items-center gap-2 text-gray-500">
        <div class="h-4 w-4 animate-spin rounded-full border-b-2 border-primary-600"></div>
        {{ t('common.loading') }}
      </div>
      <div v-else-if="loadState === 'failed'" class="flex flex-wrap items-center justify-between gap-3">
        <p role="alert" class="text-sm text-red-600 dark:text-red-400">{{ loadError }}</p>
        <button type="button" class="btn btn-secondary btn-sm" @click="loadSettings">
          {{ t('common.retry') }}
        </button>
      </div>
      <template v-else>
        <div
          v-for="(entry, index) in rows"
          :key="index"
          class="grid grid-cols-1 gap-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600 sm:grid-cols-[minmax(0,1fr)_10rem_auto] sm:items-end"
        >
          <div>
            <label :for="`openai-model-billing-name-${index}`" class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.settings.openaiModelBilling.model') }}
            </label>
            <input
              :id="`openai-model-billing-name-${index}`"
              v-model="entry.model"
              type="text"
              class="input w-full"
              :placeholder="t('admin.settings.openaiModelBilling.modelPlaceholder')"
            />
          </div>
          <div>
            <label :for="`openai-model-billing-factor-${index}`" class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.settings.openaiModelBilling.multiplier') }}
            </label>
            <input
              :id="`openai-model-billing-factor-${index}`"
              v-model.number="entry.multiplier"
              type="number"
              min="0.01"
              max="100"
              step="0.01"
              class="input w-full"
            />
          </div>
          <button
            type="button"
            class="btn btn-secondary btn-sm text-red-600 hover:text-red-700 dark:text-red-400"
            data-testid="openai-model-billing-delete"
            @click="rows.splice(index, 1)"
          >
            {{ t('common.delete') }}
          </button>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <button type="button" class="btn btn-secondary btn-sm" @click="rows.push({ model: '', multiplier: 1 })">
            {{ t('admin.settings.openaiModelBilling.addModel') }}
          </button>
          <button
            type="button"
            class="btn btn-primary btn-sm"
            data-testid="openai-model-billing-save"
            :disabled="saving"
            @click="saveSettings"
          >
            <span v-if="saving" class="mr-1 inline-block h-4 w-4 animate-spin rounded-full border-b-2 border-white"></span>
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const loadState = ref<'loading' | 'ready' | 'failed'>('loading')
const loadError = ref('')
const saving = ref(false)
const rows = ref<Array<{ model: string; multiplier: number }>>([])

function toRows(multipliers: Readonly<Record<string, number>>) {
  return Object.entries(multipliers)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([model, multiplier]) => ({ model, multiplier }))
}

async function loadSettings() {
  loadState.value = 'loading'
  try {
    const settings = await adminAPI.settings.getOpenAIModelBillingSettings()
    rows.value = toRows(settings.multipliers)
    loadState.value = 'ready'
  } catch (error: unknown) {
    loadError.value = extractApiErrorMessage(error, t('admin.settings.failedToLoad'))
    loadState.value = 'failed'
  }
}

async function saveSettings() {
  if (loadState.value !== 'ready' || saving.value) return

  const entries: Array<[string, number]> = []
  for (const entry of rows.value) {
    const model = entry.model.trim()
    const multiplier = Number(entry.multiplier)
    if (!model || !Number.isFinite(multiplier) || multiplier < 0.01 || multiplier > 100) {
      appStore.showError(t('admin.settings.openaiModelBilling.invalidEntry'))
      return
    }
    entries.push([model, multiplier])
  }

  saving.value = true
  try {
    const updated = await adminAPI.settings.updateOpenAIModelBillingSettings({
      multipliers: Object.fromEntries(entries),
    })
    rows.value = toRows(updated.multipliers)
    appStore.showSuccess(t('admin.settings.openaiModelBilling.saved'))
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('admin.settings.openaiModelBilling.saveFailed')))
  } finally {
    saving.value = false
  }
}

onMounted(loadSettings)
</script>
