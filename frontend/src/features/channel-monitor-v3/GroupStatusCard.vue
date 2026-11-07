<template>
  <article class="status-card" data-testid="model-status-card" :data-group-id="group.id">
    <header class="flex items-start justify-between gap-3">
      <div class="flex min-w-0 items-center gap-3">
        <span class="status-platform" aria-hidden="true"><PlatformIcon :platform="group.platform" size="lg" /></span>
        <h3 class="min-w-0 break-words text-base font-semibold leading-snug text-gray-900 dark:text-dark-50" :title="group.name">{{ group.name }}</h3>
      </div>
      <span class="status-badge" :data-state="status?.status ?? 'unknown'">{{ stateLabel }}</span>
    </header>

    <p class="mt-6 text-xs text-gray-500 dark:text-dark-300">{{ t('channelMonitorV3.cards.requestWindow', { minutes: status?.window_minutes ?? 180 }) }}</p>
    <dl class="mt-4 grid grid-cols-3 gap-3">
      <div v-for="metric in metrics" :key="metric.key" class="min-w-0">
        <dt class="text-[11px] leading-5 text-gray-500 dark:text-dark-300">{{ t(`channelMonitorV3.cards.${metric.key}`) }}</dt>
        <dd class="mt-1 font-mono text-xl font-semibold tabular-nums tracking-tight text-gray-900 dark:text-dark-50 sm:text-2xl" :data-metric="metric.key">{{ metric.value }}</dd>
      </div>
    </dl>

    <div class="mt-6 border-t border-gray-200 pt-4 dark:border-dark-600">
      <div class="mb-3 flex items-center justify-between gap-3 text-[11px] text-gray-500 dark:text-dark-300">
        <span>{{ t('channelMonitorV3.cards.slots', { count: status?.cells.length ?? 18 }) }}</span>
        <span>{{ checkedLabel }}</span>
      </div>
      <div class="status-timeline" :aria-label="t('modelPlaza.availability.history')">
        <button v-for="(cell, index) in cells" :key="cell.start" type="button" :data-state="cell.status" :aria-label="cellLabel(cell)" :title="cellLabel(cell)" @focus="selected = index" @blur="selected = null" @mouseenter="selected = index" @mouseleave="selected = null" @click="selected = index"></button>
      </div>
      <div class="mt-2 flex justify-between font-mono text-[10px] tracking-wide text-gray-400 dark:text-dark-400" aria-hidden="true"><span>{{ t('channelMonitorV3.cards.past') }}</span><span>{{ t('channelMonitorV3.cards.now') }}</span></div>
      <p class="mt-4 min-h-5 text-xs leading-5 text-gray-500 dark:text-dark-300" role="status">{{ selected !== null && cells[selected] ? cellLabel(cells[selected]) : sampleLabel }}</p>
    </div>

    <footer class="mt-4 border-t border-gray-200 pt-4 text-xs leading-5 text-gray-500 dark:border-dark-600 dark:text-dark-300">
      <template v-if="status?.probe_enabled">
        <span>{{ t('channelMonitorV3.cards.probeEvery') }}</span>
      </template>
      <span v-else>{{ t('channelMonitorV3.cards.probeOff') }}</span>
    </footer>
  </article>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { ModelStatusGroup } from '@/api/modelStatus'
import type { PlazaGroupAvailability } from '@/api/modelPlaza'

const props = defineProps<{ group: ModelStatusGroup }>()
const { t, locale } = useI18n()
const selected = ref<number | null>(null)
const status = computed(() => props.group.availability)
const percent = (value: number | null | undefined) => value == null || !Number.isFinite(value) ? '—' : `${value.toFixed(1)}%`
const requestReady = computed(() => status.value?.request_sample_state === 'ready')
const metrics = computed(() => [
  { key: 'cache', value: requestReady.value ? percent(status.value?.cache_read_rate) : '—' },
  { key: 'success', value: requestReady.value ? percent(status.value?.request_success_rate) : '—' },
  { key: 'ttft', value: requestReady.value && status.value?.ttft_p50_ms != null ? `${(status.value.ttft_p50_ms / 1000).toFixed(1)}s` : '—' },
])
const stateLabel = computed(() => t(`channelMonitorV3.cards.state.${status.value?.status ?? 'unknown'}`))
const time = (value: string) => new Date(value).toLocaleTimeString(locale.value, { hour: '2-digit', minute: '2-digit' })
const checkedLabel = computed(() => status.value?.checked_at ? t(`channelMonitorV3.cards.${status.value.source === 'probe' ? 'checked' : 'observed'}`, { time: time(status.value.checked_at) }) : t('channelMonitorV3.cards.noRecent'))
const sampleLabel = computed(() => {
  const key = status.value?.request_sample_state
  return t(`channelMonitorV3.cards.${key === 'ready' ? 'requestReady' : key === 'insufficient' ? 'insufficient' : key === 'stale' || !status.value ? 'unavailable' : 'waiting'}`, { count: status.value?.min_requests ?? 20 })
})
const cells = computed<PlazaGroupAvailability['cells']>(() => status.value?.cells ?? Array.from({ length: 18 }, (_, i) => ({ start: `unknown-${i}`, status: 'unknown', source: 'none' })))
const cellLabel = (cell: PlazaGroupAvailability['cells'][number]) => `${Number.isNaN(Date.parse(cell.start)) ? '' : time(cell.start) + ' · '}${t(`channelMonitorV3.cards.state.${cell.status}`)} · ${t(`channelMonitorV3.cards.${cell.source === 'probe' ? 'probeSource' : cell.source === 'requests' ? 'requestSource' : 'noData'}`)}`
</script>

<style scoped>
.status-card { @apply min-w-0 rounded-2xl border border-gray-200 bg-white p-5 dark:border-dark-600 dark:bg-dark-800; }
.status-platform { @apply flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-gray-100 text-gray-700 dark:bg-dark-600 dark:text-dark-100; }
.status-badge { @apply inline-flex shrink-0 items-center rounded-full bg-gray-100 px-2.5 py-1 text-[11px] font-medium text-gray-600 dark:bg-dark-600 dark:text-dark-200; }
.status-badge[data-state="operational"] { @apply bg-emerald-50 text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-300; }
.status-badge[data-state="observed"] { @apply bg-emerald-50 text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-300; }
.status-badge[data-state="errors_observed"] { @apply bg-amber-50 text-amber-800 dark:bg-amber-950/50 dark:text-amber-300; }
.status-badge[data-state="degraded"] { @apply bg-amber-50 text-amber-800 dark:bg-amber-950/50 dark:text-amber-300; }
.status-badge[data-state="down"] { @apply bg-red-50 text-red-700 dark:bg-red-950/50 dark:text-red-300; }
.status-timeline { display: grid; grid-template-columns: repeat(18, minmax(0, 1fr)); gap: 4px; }
.status-timeline > button { @apply h-3 min-w-0 rounded-[3px] bg-gray-200 outline-offset-4 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:bg-dark-500; }
.status-timeline > button[data-state="operational"] { @apply bg-emerald-500 dark:bg-emerald-400; }
.status-timeline > button[data-state="observed"] { @apply bg-emerald-500 dark:bg-emerald-400; }
.status-timeline > button[data-state="errors_observed"] { @apply bg-amber-500; }
.status-timeline > button[data-state="degraded"] { @apply bg-amber-500; }
.status-timeline > button[data-state="down"] { @apply bg-red-500 dark:bg-red-400; }
</style>
