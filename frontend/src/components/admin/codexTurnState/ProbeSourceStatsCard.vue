<template>
  <section class="card p-5" data-test="cts-stats">
    <div class="mb-3 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.codexTurnState.stats.title') }}</h2>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.codexTurnState.stats.hint') }}</p>
      </div>
      <form class="flex flex-wrap items-end gap-2" @submit.prevent="load">
        <label class="text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.codexTurnState.stats.startDay') }}
          <input v-model="startDay" type="date" class="input mt-1" data-test="cts-stats-start" />
        </label>
        <label class="text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.codexTurnState.stats.endDay') }}
          <input v-model="endDay" type="date" class="input mt-1" data-test="cts-stats-end" />
        </label>
        <button type="submit" class="btn btn-secondary btn-sm" :disabled="loading" data-test="cts-stats-filter">{{ t('admin.codexTurnState.stats.filter') }}</button>
      </form>
    </div>

    <div v-if="error" role="alert" class="mb-3 rounded-lg border border-red-200 bg-red-50 p-3 text-xs text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200">{{ error }}</div>
    <p v-else-if="stats" class="mb-3 text-xs text-gray-500 dark:text-gray-400" data-test="cts-stats-summary">
      {{ t('admin.codexTurnState.stats.summary', { attempts: stats.totals.attempts, hits: stats.totals.target_hits, persisted: stats.totals.persisted }) }}
    </p>

    <div class="overflow-x-auto">
      <table class="w-full text-left text-sm">
        <thead class="text-xs uppercase text-gray-500 dark:text-gray-400">
          <tr>
            <th class="px-3 py-2">{{ t('admin.codexTurnState.stats.columns.source') }}</th>
            <th class="px-3 py-2 text-right">{{ t('admin.codexTurnState.stats.columns.attempts') }}</th>
            <th class="px-3 py-2 text-right">HTTP 200</th>
            <th class="px-3 py-2 text-right">{{ t('admin.codexTurnState.stats.columns.targetHits') }}</th>
            <th class="px-3 py-2 text-right">{{ t('admin.codexTurnState.stats.columns.persisted') }}</th>
            <th class="px-3 py-2 text-right">{{ t('admin.codexTurnState.stats.columns.hitRate') }}</th>
            <th class="px-3 py-2 text-right">{{ t('admin.codexTurnState.stats.columns.errors') }}</th>
            <th class="px-3 py-2 text-right">{{ t('admin.codexTurnState.stats.columns.headerMs') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-if="!rows.length">
            <td colspan="8" class="px-3 py-6 text-center text-gray-500 dark:text-gray-400">{{ t('admin.codexTurnState.stats.empty') }}</td>
          </tr>
          <tr v-for="row in rows" :key="row.source" data-test="cts-stats-row">
            <td class="px-3 py-2 font-medium text-gray-900 dark:text-white">{{ row.source }}</td>
            <td class="px-3 py-2 text-right">{{ row.attempts }}</td>
            <td class="px-3 py-2 text-right">{{ row.http_200 }}</td>
            <td class="px-3 py-2 text-right" :class="row.attempts && !row.target_hits ? 'text-amber-600 dark:text-amber-400' : ''">{{ row.target_hits }}</td>
            <td class="px-3 py-2 text-right">{{ row.persisted }}</td>
            <td class="px-3 py-2 text-right">{{ row.attempts ? `${((100 * row.target_hits) / row.attempts).toFixed(1)}%` : '-' }}</td>
            <td class="px-3 py-2 text-right" :title="errorBreakdown(row.status_counts)">{{ row.errors }}</td>
            <td class="px-3 py-2 text-right">{{ row.attempts ? `${Math.round(row.header_ms_average || 0)} ms` : '-' }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getStats, type CodexProbeStats } from '@/api/admin/codexTurnState'
import { describeManagerError } from './managerError'

const { t } = useI18n()

const startDay = ref('')
const endDay = ref('')
const stats = ref<CodexProbeStats | null>(null)
const loading = ref(false)
const error = ref('')

const rows = computed(() => Object.entries(stats.value?.by_source ?? {})
  .map(([source, counts]) => ({ source, ...counts }))
  .sort((a, b) => b.attempts - a.attempts))

function errorBreakdown(counts: Record<string, number> | undefined) {
  return Object.entries(counts ?? {})
    .filter(([code]) => code !== '200')
    .map(([code, n]) => `${code === '0' ? t('admin.codexTurnState.stats.networkError') : `HTTP ${code}`}: ${n}`)
    .join(' · ')
}

async function load() {
  if (startDay.value && endDay.value && startDay.value > endDay.value) {
    error.value = t('admin.codexTurnState.stats.invalidRange')
    return
  }
  loading.value = true
  try {
    stats.value = await getStats({ start_day: startDay.value || undefined, end_day: endDay.value || undefined })
    error.value = ''
  } catch (err) {
    stats.value = null
    error.value = describeManagerError(err, t, 'admin.codexTurnState.stats.loadFailed')
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
})

defineExpose({ load })
</script>
