<template>
  <div class="group-health" data-testid="plaza-group-status">
    <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
      <span class="health-state" :data-state="status.status">
        <span class="health-dot" aria-hidden="true"></span>{{ stateLabel }}
      </span>
      <span class="text-xs text-gray-500 dark:text-dark-400">
        {{ rateLabel }} <strong v-if="rate !== null" class="ml-1 font-mono font-medium text-gray-800 dark:text-dark-100">{{ rate.toFixed(1) }}%</strong>
      </span>
    </div>
    <div class="health-strip" role="img" :aria-label="t('modelPlaza.availability.history')">
      <span v-for="cell in status.cells" :key="cell.start" :data-state="cell.status" :title="cellTitle(cell)"></span>
    </div>
    <div class="flex flex-wrap items-center justify-between gap-1 text-[11px] text-gray-500 dark:text-dark-400">
      <span>{{ t('modelPlaza.availability.sixHours') }}</span>
      <span>{{ checkedLabel }}</span>
    </div>
    <details class="mt-2 text-xs text-gray-500 dark:text-dark-400">
      <summary class="w-fit cursor-pointer rounded focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500">{{ status.probe_enabled ? t('modelPlaza.availability.probeEvery') : t('modelPlaza.availability.passive') }}</summary>
      <p v-if="status.probe_model" class="mt-2 break-words">{{ t('modelPlaza.availability.model') }} <code>{{ status.probe_model }}</code></p>
      <p class="mt-1 leading-relaxed">{{ t('modelPlaza.availability.scope') }}</p>
      <p v-if="status.request_success_rate !== null && status.probe_success_rate !== null" class="mt-1">{{ t('modelPlaza.availability.probeRate') }} {{ status.probe_success_rate.toFixed(1) }}%</p>
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PlazaGroupAvailability } from '@/api/modelPlaza'

const props = defineProps<{ status: PlazaGroupAvailability }>()
const { t, locale } = useI18n()
const rate = computed(() => props.status.request_success_rate ?? props.status.probe_success_rate)
const rateLabel = computed(() => t(`modelPlaza.availability.${props.status.request_success_rate !== null ? 'requestRate' : props.status.probe_success_rate !== null ? 'probeRate' : props.status.probe_enabled ? 'waiting' : 'noRequests'}`))
const stateLabel = computed(() => t(`modelPlaza.availability.${props.status.status === 'unknown' ? 'unknown' : props.status.status}`))
const time = (at: string) => new Date(at).toLocaleTimeString(locale.value, { hour: '2-digit', minute: '2-digit' })
const checkedLabel = computed(() => props.status.checked_at ? t(`modelPlaza.availability.${props.status.source === 'probe' ? 'checked' : 'observed'}`, { time: time(props.status.checked_at) }) : t(props.status.probe_enabled ? 'modelPlaza.availability.waiting' : 'modelPlaza.availability.probeOff'))
const cellTitle = (cell: PlazaGroupAvailability['cells'][number]) => `${time(cell.start)} · ${t(`modelPlaza.availability.${cell.status === 'insufficient' ? 'unknown' : cell.status}`)} · ${t(`modelPlaza.availability.${cell.source === 'probe' ? 'probe' : 'requests'}`)}`
</script>

<style scoped>
.group-health { @apply border-t border-gray-100 px-5 py-3 dark:border-dark-700/60; }
.health-state { @apply inline-flex items-center gap-1.5 text-xs font-medium text-gray-500 dark:text-dark-400; }
.health-dot { @apply h-1.5 w-1.5 rounded-full bg-gray-400; }
.health-state[data-state="operational"] { @apply text-emerald-700 dark:text-emerald-400; }
.health-state[data-state="degraded"] { @apply text-amber-700 dark:text-amber-400; }
.health-state[data-state="down"] { @apply text-red-700 dark:text-red-400; }
.health-state[data-state="operational"] .health-dot { @apply bg-emerald-500; }
.health-state[data-state="degraded"] .health-dot { @apply bg-amber-500; }
.health-state[data-state="down"] .health-dot { @apply bg-red-500; }
.health-strip { display: flex; gap: 3px; height: 7px; margin: 10px 0 5px; }
.health-strip > span { flex: 1; min-width: 0; border-radius: 2px; @apply bg-gray-200 dark:bg-dark-600; }
.health-strip > span[data-state="operational"] { @apply bg-emerald-500 dark:bg-emerald-400; }
.health-strip > span[data-state="degraded"] { @apply bg-amber-500; }
.health-strip > span[data-state="down"] { @apply bg-red-500; }
</style>
