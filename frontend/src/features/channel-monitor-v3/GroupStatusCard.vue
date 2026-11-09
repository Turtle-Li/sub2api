<template>
  <article class="status-card" data-testid="model-status-card" :data-group-id="group.id">
    <header class="flex items-start justify-between gap-3">
      <div class="flex min-w-0 items-center gap-3">
        <span class="status-platform" aria-hidden="true"><PlatformIcon :platform="group.platform" size="lg" /></span>
        <h3 class="min-w-0 break-words text-base font-semibold leading-snug text-gray-900 dark:text-dark-50" :title="group.name">{{ group.name }}</h3>
      </div>
      <span class="status-badge" :data-state="quota?.state === 'exhausted' ? 'quota' : status?.status ?? 'unknown'">{{ stateLabel }}</span>
    </header>

    <div v-if="quota" class="status-quota" data-testid="pool-quota-notice">
      <p class="font-medium">{{ t(`channelMonitorV3.cards.quota.${quota.state}`) }}</p>
      <p class="mt-1 text-[11px] leading-5"><template v-if="quota.remaining !== null && quota.remaining > 0">{{ t('channelMonitorV3.cards.quota.recovery') }} <span class="font-mono tabular-nums" data-testid="quota-countdown">{{ quota.clock }}</span></template><template v-else>{{ t(`channelMonitorV3.cards.quota.${quota.remaining === 0 ? 'rechecking' : 'unknownReset'}`) }}</template></p>
    </div>

    <p class="mt-6 text-xs text-gray-500 dark:text-dark-300">{{ t('channelMonitorV3.cards.requestWindow', { minutes: status?.window_minutes ?? 180 }) }}</p>
    <dl class="mt-4 grid gap-3" :class="cacheVisible ? 'grid-cols-3' : 'grid-cols-2'">
      <div v-for="metric in metrics" :key="metric.key" class="min-w-0">
        <dt class="text-[11px] leading-5 text-gray-500 dark:text-dark-300">{{ t(`channelMonitorV3.cards.${metric.label}`) }}<span class="block text-[10px]">{{ t(`channelMonitorV3.cards.${metric.scope}`) }}</span></dt>
        <dd class="mt-1 font-semibold tabular-nums tracking-tight text-gray-900 dark:text-dark-50" :class="metric.key === 'cache' && !realCacheReady ? 'text-base leading-8' : 'font-mono text-xl sm:text-2xl'" :data-metric="metric.key" :title="metric.key === 'cache' && realCacheReady ? cacheHourTitle : undefined">{{ metric.value }}</dd>
        <span v-if="metric.key === 'cache'" class="mt-1 block text-[10px] leading-4 text-gray-500 dark:text-dark-300">{{ realCacheReady ? cacheHourLabel : t('channelMonitorV3.cards.cacheCapabilityWindow') }}</span>
      </div>
    </dl>

    <div class="mt-6 border-t border-gray-200 pt-4 dark:border-dark-600">
      <div class="mb-3 flex items-center justify-between gap-3 text-[11px] text-gray-500 dark:text-dark-300">
        <span>{{ t('channelMonitorV3.cards.slots', { count: cells.length }) }}</span>
        <span>{{ checkedLabel }}</span>
      </div>
      <div class="status-timeline" :style="{ '--status-cell-count': cells.length }" :aria-label="t('channelMonitorV3.cards.slots', { count: cells.length })">
        <button v-for="(cell, index) in cells" :key="cell.start" type="button" :tabindex="index === Math.min(focused, cells.length - 1) ? 0 : -1" :data-state="cell.status" :aria-label="cellLabel(cell)" :title="cellLabel(cell)" @keydown="moveSlot($event, index)" @focus="selected = index; focused = index" @blur="selected = null" @mouseenter="selected = index" @mouseleave="selected = null" @click="selected = index; focused = index"></button>
      </div>
      <div class="mt-2 flex justify-between font-mono text-[10px] tracking-wide text-gray-400 dark:text-dark-400" aria-hidden="true"><span>{{ t('channelMonitorV3.cards.past') }}</span><span>{{ t('channelMonitorV3.cards.now') }}</span></div>
      <p class="mt-4 min-h-5 text-xs leading-5 text-gray-500 dark:text-dark-300" role="status">{{ selected !== null && cells[selected] ? cellLabel(cells[selected]) : sampleLabel }}</p>
    </div>

  </article>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { ModelStatusGroup } from '@/api/modelStatus'
import type { PlazaGroupAvailability } from '@/api/modelPlaza'
import { cacheCapability, quotaNotice } from './statusPresentation'

const props = defineProps<{ group: ModelStatusGroup }>()
const { t, locale } = useI18n()
const selected = ref<number | null>(null)
const focused = ref(71)
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
onMounted(() => { focused.value = cells.value.length - 1; clock = setInterval(() => { now.value = Date.now() }, 1000) })
onUnmounted(() => { if (clock) clearInterval(clock) })
const status = computed(() => props.group.availability)
const validPercent = (value: number | null | undefined): value is number => typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 100
const validTime = (value: number | null | undefined): value is number => typeof value === 'number' && Number.isFinite(value) && value >= 0
const percent = (value: number | null | undefined) => validPercent(value) ? `${value.toFixed(1)}%` : '—'
const seconds = (value: number) => `${(value / 1000).toFixed(1)}s`
const requestReady = computed(() => status.value?.request_sample_state === 'ready')
const probeReady = computed(() => status.value?.probe_metrics_version === 1 && status.value.probe_enabled && status.value.probe_sample_state === 'ready')
const realCacheReady = computed(() => {
  const s = status.value
  const start = Date.parse(s?.cache_hour_start ?? ''), end = Date.parse(s?.cache_hour_end ?? '')
  return s?.cache_sample_state === 'ready' && s.cache_scope === 'shared_account_pool' && s.cache_statistic === 'best_complete_hour' && s.cache_lookback_hours === 24 && Number.isFinite(start) && start % 3600000 === 0 && end - start === 3600000 && typeof s.cache_peak_hour_rate === 'number' && Number.isFinite(s.cache_peak_hour_rate) && s.cache_peak_hour_rate >= 0 && s.cache_peak_hour_rate <= 100
})
const capability = computed(() => cacheCapability(status.value, now.value))
const quota = computed(() => quotaNotice(status.value, now.value))
const cacheVisible = computed(() => realCacheReady.value || capability.value !== null)
const metrics = computed(() => {
  const s = status.value
  const realSuccess = requestReady.value && validPercent(s?.request_success_rate)
  const realTime = requestReady.value && validTime(s?.ttft_p50_ms)
  const probeSuccess = !realSuccess && probeReady.value && validPercent(s?.probe_success_rate)
  const probeTTFT = !s?.shared_probe && !realTime && probeReady.value && validTime(s?.probe_ttft_p50_ms)
  const probeLatency = !s?.shared_probe && !realTime && !probeTTFT && probeReady.value && validTime(s?.probe_latency_p50_ms)
  return [
    ...(cacheVisible.value ? [{ key: 'cache', label: realCacheReady.value ? 'cache' : 'cacheCapability', scope: realCacheReady.value ? 'cacheScope' : 'cacheCapabilityScope', value: realCacheReady.value ? percent(s?.cache_peak_hour_rate) : t(`channelMonitorV3.cards.cache${capability.value === 'verified' ? 'Verified' : 'Pending'}`), probe: false }] : []),
    { key: 'success', label: probeSuccess ? 'probeSuccess' : 'success', scope: probeSuccess ? 'probeScope' : 'groupScope', value: realSuccess ? percent(s?.request_success_rate) : probeSuccess ? percent(s?.probe_success_rate) : '—', probe: probeSuccess },
    { key: 'ttft', label: probeTTFT ? 'probeTTFT' : probeLatency ? 'probeLatency' : 'ttft', scope: probeTTFT || probeLatency ? 'probeScope' : 'groupScope', value: realTime && validTime(s?.ttft_p50_ms) ? seconds(s.ttft_p50_ms) : probeTTFT && validTime(s?.probe_ttft_p50_ms) ? seconds(s.probe_ttft_p50_ms) : probeLatency && validTime(s?.probe_latency_p50_ms) ? seconds(s.probe_latency_p50_ms) : '—', probe: probeTTFT || probeLatency },
  ]
})
const stateLabel = computed(() => quota.value?.state === 'exhausted' ? t('channelMonitorV3.cards.quota.badge') : t(`channelMonitorV3.cards.state.${status.value?.status ?? 'unknown'}`))
const time = (value: string) => new Date(value).toLocaleTimeString(locale.value, { hour: '2-digit', minute: '2-digit' })
const cacheHourLabel = computed(() => realCacheReady.value ? `${new Date(status.value!.cache_hour_start!).toLocaleDateString(locale.value, { month: '2-digit', day: '2-digit' })} ${time(status.value!.cache_hour_start!)}–${time(status.value!.cache_hour_end!)}` : '')
const cacheHourTitle = computed(() => realCacheReady.value ? t('channelMonitorV3.cards.cacheHour', { start: new Date(status.value!.cache_hour_start!).toLocaleString(locale.value), end: new Date(status.value!.cache_hour_end!).toLocaleString(locale.value) }) : '')
const checkedLabel = computed(() => status.value?.checked_at ? t(`channelMonitorV3.cards.${status.value.source === 'probe' || status.value.source === 'shared_probe' ? 'checked' : 'observed'}`, { time: time(status.value.checked_at) }) : t('channelMonitorV3.cards.noRecent'))
const sampleLabel = computed(() => {
  if (metrics.value.some(metric => metric.probe)) return ''
  const key = status.value?.request_sample_state
  return t(`channelMonitorV3.cards.${key === 'ready' ? 'requestReady' : key === 'insufficient' ? 'insufficient' : key === 'stale' || !status.value ? 'unavailable' : 'waiting'}`, { count: status.value?.min_requests ?? 20 })
})
const cells = computed<PlazaGroupAvailability['cells']>(() => status.value?.cells.length ? status.value.cells : Array.from({ length: 72 }, (_, i) => ({ start: `unknown-${i}`, status: 'unknown', source: 'none' })))
const cellLabel = (cell: PlazaGroupAvailability['cells'][number]) => `${Number.isNaN(Date.parse(cell.start)) ? '' : time(cell.start) + ' · '}${t(`channelMonitorV3.cards.state.${cell.status}`)}`
function moveSlot(event: KeyboardEvent, index: number) {
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? cells.value.length - 1 : event.key === 'ArrowLeft' ? Math.max(0, index - 1) : event.key === 'ArrowRight' ? Math.min(cells.value.length - 1, index + 1) : null
  if (next === null) return
  event.preventDefault()
  ;(event.currentTarget as HTMLElement).parentElement?.querySelectorAll<HTMLButtonElement>('button')[next]?.focus()
}
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
.status-timeline { display: grid; grid-template-columns: repeat(var(--status-cell-count, 72), minmax(0, 1fr)); gap: 2px; }
.status-timeline > button { @apply h-5 min-w-0 rounded-[1px] bg-gray-200 outline-offset-4 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:bg-dark-500; }
.status-quota { @apply mt-5 rounded-lg bg-amber-50 p-3 text-xs leading-5 text-amber-900 dark:bg-amber-950/50 dark:text-amber-200; }
.status-badge[data-state="quota"] { @apply bg-amber-50 text-amber-900 dark:bg-amber-950/50 dark:text-amber-200; }
.status-timeline > button[data-state="operational"] { @apply bg-emerald-500 dark:bg-emerald-400; }
.status-timeline > button[data-state="observed"] { @apply bg-emerald-500 dark:bg-emerald-400; }
.status-timeline > button[data-state="errors_observed"] { @apply bg-amber-500; }
.status-timeline > button[data-state="degraded"] { @apply bg-amber-500; }
.status-timeline > button[data-state="down"] { @apply bg-red-500 dark:bg-red-400; }
</style>
