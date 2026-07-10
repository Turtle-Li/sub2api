<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  opsAPI,
  type OpsNetworkInterfaceInfo,
  type OpsNetworkBandwidthSettings,
  type OpsNetworkBandwidthSummary,
  type OpsNetworkBandwidthSummaryResponse
} from '@/api/admin/ops'

interface Props {
  refreshToken: number
}

type SettingsPayload = Partial<OpsNetworkBandwidthSettings> & {
  clear_rx_limit?: boolean
  clear_tx_limit?: boolean
}

const props = defineProps<Props>()
const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const settingsLoading = ref(false)
const saving = ref(false)
const errorMessage = ref('')
const formError = ref('')
const showSettings = ref(false)
const response = ref<OpsNetworkBandwidthSummaryResponse | null>(null)
const settings = ref<OpsNetworkBandwidthSettings | null>(null)
const interfaces = ref<OpsNetworkInterfaceInfo[]>([])
const defaultIface = ref('')

const bandwidthRefreshIntervalMs = 5_000
let bandwidthRefreshTimer: ReturnType<typeof setInterval> | null = null

const enabledInput = ref(true)
const ifaceInput = ref('')
const rxLimitInput = ref('')
const txLimitInput = ref('')
const retryProtectionEnabledInput = ref(false)
const retryProtectionTriggerInput = ref('90')
const retryProtectionMinBodyMBInput = ref('5')
const retryProtectionWindowInput = ref('60')
const retryProtectionMaxRepeatsInput = ref('1')
const retryAdvancedOpen = ref(false)

const recommendedRetryProtection = {
  triggerPercent: 90,
  minBodyMB: 5,
  windowSeconds: 60,
  maxRepeats: 1
}

const summary = computed<OpsNetworkBandwidthSummary | null>(() => response.value?.summary ?? null)
const enabled = computed(() => response.value?.enabled !== false && summary.value?.enabled !== false)
const hasRxLimit = computed(() => typeof summary.value?.rx_limit_mbps === 'number' && Number.isFinite(summary.value.rx_limit_mbps))
const hasTxLimit = computed(() => typeof summary.value?.tx_limit_mbps === 'number' && Number.isFinite(summary.value.tx_limit_mbps))
const hasAnyLimit = computed(() => hasRxLimit.value || hasTxLimit.value)
const hasPacketSignal = computed(() => {
  const item = summary.value
  if (!item) return false
  return Boolean(item.rx_dropped_delta || item.tx_dropped_delta || item.rx_errors_delta || item.tx_errors_delta)
})
const txUtilization = computed(() => normalizePercent(summary.value?.tx_utilization_percent))
const rxUtilization = computed(() => normalizePercent(summary.value?.rx_utilization_percent))
const primaryUtilization = computed(() => normalizePercent(summary.value?.max_utilization_percent) ?? txUtilization.value ?? rxUtilization.value)
const limitValue = computed(() => {
  if (hasTxLimit.value) return summary.value?.tx_limit_mbps ?? null
  if (hasRxLimit.value) return summary.value?.rx_limit_mbps ?? null
  return null
})
const limitDirection = computed(() => {
  if (hasTxLimit.value) return t('admin.ops.network.tx')
  if (hasRxLimit.value) return t('admin.ops.network.rx')
  return ''
})
const ifaceOptions = computed(() => {
  const items = [...interfaces.value]
  if (ifaceInput.value && !items.some((item) => item.name === ifaceInput.value)) {
    items.unshift({ name: ifaceInput.value, state: 'unknown', is_default: false })
  }
  return items
})
const retryLimitInfo = computed(() => {
  const rxLimit = parseOptionalPositive(rxLimitInput.value)
  const txLimit = parseOptionalPositive(txLimitInput.value)
  const rx = typeof rxLimit === 'number' ? rxLimit : null
  const tx = typeof txLimit === 'number' ? txLimit : null
  if (rx === null && tx === null) return null
  if (rx !== null && (tx === null || rx <= tx)) {
    return { mbps: rx, direction: t('admin.ops.network.rx') }
  }
  return { mbps: tx as number, direction: t('admin.ops.network.tx') }
})
const retryEstimate = computed(() => {
  const limit = retryLimitInfo.value
  const trigger = Number(retryProtectionTriggerInput.value)
  const minBodyMB = Number(retryProtectionMinBodyMBInput.value)
  const windowSeconds = Number(retryProtectionWindowInput.value)
  const maxRepeats = Number(retryProtectionMaxRepeatsInput.value)
  if (!limit || !Number.isFinite(trigger) || !Number.isFinite(minBodyMB) || !Number.isFinite(windowSeconds) || !Number.isFinite(maxRepeats)) {
    return null
  }
  if (limit.mbps <= 0 || trigger <= 0 || windowSeconds <= 0 || maxRepeats <= 0) return null
  const budgetBytes = limit.mbps * 1_000_000 * windowSeconds * (trigger / 100) / 8
  const dynamicCandidateBytes = budgetBytes / (Math.floor(maxRepeats) + 2)
  const minCandidateBytes = minBodyMB * 1024 * 1024
  const effectiveCandidateBytes = Math.max(minCandidateBytes, dynamicCandidateBytes)
  return {
    limitMbps: limit.mbps,
    direction: limit.direction,
    budgetBytes,
    dynamicCandidateBytes,
    effectiveCandidateBytes,
    blockAttempt: Math.floor(maxRepeats) + 2
  }
})
const retryUsesRecommendedSettings = computed(() => (
  Number(retryProtectionTriggerInput.value) === recommendedRetryProtection.triggerPercent
  && Number(retryProtectionMinBodyMBInput.value) === recommendedRetryProtection.minBodyMB
  && Number(retryProtectionWindowInput.value) === recommendedRetryProtection.windowSeconds
  && Number(retryProtectionMaxRepeatsInput.value) === recommendedRetryProtection.maxRepeats
))

async function loadData() {
  loading.value = true
  errorMessage.value = ''
  try {
    response.value = await opsAPI.getNetworkBandwidthSummary()
  } catch (err: any) {
    console.error('[OpsNetworkBandwidthCard] Failed to load data', err)
    errorMessage.value = err?.response?.data?.detail || t('admin.ops.network.loadFailed')
  } finally {
    loading.value = false
  }
}

function refreshLatestSample() {
  if (document.visibilityState === 'hidden' || loading.value || !enabled.value) return
  void loadData()
}

function startBandwidthAutoRefresh() {
  if (bandwidthRefreshTimer) return
  bandwidthRefreshTimer = setInterval(refreshLatestSample, bandwidthRefreshIntervalMs)
}

function stopBandwidthAutoRefresh() {
  if (!bandwidthRefreshTimer) return
  clearInterval(bandwidthRefreshTimer)
  bandwidthRefreshTimer = null
}

function handleVisibilityChange() {
  if (document.visibilityState === 'visible') {
    refreshLatestSample()
  }
}

async function openSettings() {
  showSettings.value = true
  formError.value = ''
  retryAdvancedOpen.value = false
  settingsLoading.value = true
  try {
    const [data, ifaceData] = await Promise.all([
      opsAPI.getNetworkBandwidthSettings(),
      opsAPI.getNetworkInterfaces().catch((err) => {
        console.error('[OpsNetworkBandwidthCard] Failed to load interfaces', err)
        return { interfaces: [], default_iface: '' }
      })
    ])
    settings.value = data
    interfaces.value = ifaceData.interfaces || []
    defaultIface.value = ifaceData.default_iface || ''
    enabledInput.value = data.enabled
    ifaceInput.value = data.iface || ''
    rxLimitInput.value = formatInputNumber(data.rx_limit_mbps)
    txLimitInput.value = formatInputNumber(data.tx_limit_mbps)
    retryProtectionEnabledInput.value = Boolean(data.abnormal_retry_protection_enabled)
    retryProtectionTriggerInput.value = formatInputNumber(data.abnormal_retry_protection_trigger_percent || 90)
    retryProtectionMinBodyMBInput.value = formatInputNumber(bytesToMB(data.abnormal_retry_protection_min_body_bytes || 5 * 1024 * 1024))
    retryProtectionWindowInput.value = formatInputNumber(data.abnormal_retry_protection_window_seconds || 60)
    retryProtectionMaxRepeatsInput.value = formatInputNumber(data.abnormal_retry_protection_max_repeats || 1)
  } catch (err: any) {
    console.error('[OpsNetworkBandwidthCard] Failed to load settings', err)
    formError.value = err?.response?.data?.detail || t('admin.ops.network.settingsLoadFailed')
  } finally {
    settingsLoading.value = false
  }
}

async function saveSettings() {
  formError.value = ''
  const rxLimit = parseOptionalPositive(rxLimitInput.value)
  const txLimit = parseOptionalPositive(txLimitInput.value)
  const parsedRetryTrigger = parseRequiredNumber(retryProtectionTriggerInput.value, 1, 100)
  const parsedRetryMinBodyMB = parseRequiredNumber(retryProtectionMinBodyMBInput.value, 0.001, 256)
  const parsedRetryWindow = parseRequiredInteger(retryProtectionWindowInput.value, 1, 3600)
  const parsedRetryMaxRepeats = parseRequiredInteger(retryProtectionMaxRepeatsInput.value, 1, 10)
  const retrySettingsValid = parsedRetryTrigger !== undefined
    && parsedRetryMinBodyMB !== undefined
    && parsedRetryWindow !== undefined
    && parsedRetryMaxRepeats !== undefined

  if (rxLimit === undefined || txLimit === undefined || (retryProtectionEnabledInput.value && !retrySettingsValid)) {
    formError.value = t('admin.ops.network.invalidSettings')
    return
  }

  const retryTrigger = parsedRetryTrigger ?? recommendedRetryProtection.triggerPercent
  const retryMinBodyMB = parsedRetryMinBodyMB ?? recommendedRetryProtection.minBodyMB
  const retryWindow = parsedRetryWindow ?? recommendedRetryProtection.windowSeconds
  const retryMaxRepeats = parsedRetryMaxRepeats ?? recommendedRetryProtection.maxRepeats

  const hasManualLimit = rxLimit !== null || txLimit !== null
  const payload: SettingsPayload = {
    enabled: enabledInput.value,
    iface: ifaceInput.value.trim(),
    limit_source: hasManualLimit ? 'manual' : 'unknown',
    abnormal_retry_protection_enabled: retryProtectionEnabledInput.value,
    abnormal_retry_protection_trigger_percent: retryTrigger,
    abnormal_retry_protection_min_body_bytes: Math.round(retryMinBodyMB * 1024 * 1024),
    abnormal_retry_protection_window_seconds: retryWindow,
    abnormal_retry_protection_max_repeats: retryMaxRepeats
  }

  if (rxLimit === null) {
    payload.clear_rx_limit = true
  } else {
    payload.rx_limit_mbps = rxLimit
  }
  if (txLimit === null) {
    payload.clear_tx_limit = true
  } else {
    payload.tx_limit_mbps = txLimit
  }

  saving.value = true
  try {
    settings.value = await opsAPI.updateNetworkBandwidthSettings(payload)
    showSettings.value = false
    appStore.showSuccess(t('admin.ops.network.settingsSaveSuccess'))
    await loadData()
  } catch (err: any) {
    console.error('[OpsNetworkBandwidthCard] Failed to save settings', err)
    formError.value = err?.message || err?.response?.data?.message || err?.response?.data?.detail || t('admin.ops.network.settingsSaveFailed')
    appStore.showError(formError.value)
  } finally {
    saving.value = false
  }
}

watch(
  () => props.refreshToken,
  () => {
    loadData()
  }
)

watch(
  () => enabled.value,
  (value) => {
    if (value && !summary.value) {
      loadData()
    }
  },
  { immediate: true }
)

watch(retryProtectionEnabledInput, (value) => {
  if (!value) retryAdvancedOpen.value = false
})

onMounted(() => {
  startBandwidthAutoRefresh()
  document.addEventListener('visibilitychange', handleVisibilityChange)
})

onUnmounted(() => {
  stopBandwidthAutoRefresh()
  document.removeEventListener('visibilitychange', handleVisibilityChange)
})

function normalizePercent(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function formatMbps(value: unknown): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '0.00'
  if (value >= 100) return value.toFixed(0)
  if (value >= 10) return value.toFixed(1)
  return value.toFixed(2)
}

function formatBytes(value: unknown): string {
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let n = value
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n >= 10 ? n.toFixed(1) : n.toFixed(2)} ${units[i]}`
}

function formatPercent(value: number | null): string {
  if (value === null) return '--'
  return `${Math.round(value)}%`
}

function formatLimitSummary(): string {
  if (!hasAnyLimit.value || limitValue.value === null) return t('admin.ops.network.noLimitShort')
  return `${formatPercent(primaryUtilization.value)} / ${formatMbps(limitValue.value)} Mbps`
}

function formatInputNumber(value: unknown): string {
  return typeof value === 'number' && Number.isFinite(value) ? String(value) : ''
}

function bytesToMB(value: unknown): number {
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) return 5
  return Math.round((value / 1024 / 1024) * 1000) / 1000
}

function applyRecommendedRetryProtection() {
  retryProtectionTriggerInput.value = String(recommendedRetryProtection.triggerPercent)
  retryProtectionMinBodyMBInput.value = String(recommendedRetryProtection.minBodyMB)
  retryProtectionWindowInput.value = String(recommendedRetryProtection.windowSeconds)
  retryProtectionMaxRepeatsInput.value = String(recommendedRetryProtection.maxRepeats)
}

function parseOptionalPositive(value: unknown): number | null | undefined {
  const trimmed = String(value ?? '').trim()
  if (!trimmed) return null
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed) || parsed <= 0 || parsed > 1_000_000) return undefined
  return parsed
}

function parseRequiredNumber(value: unknown, min: number, max: number): number | undefined {
  const parsed = Number(String(value ?? '').trim())
  if (!Number.isFinite(parsed) || parsed < min || parsed > max) return undefined
  return parsed
}

function parseRequiredInteger(value: unknown, min: number, max: number): number | undefined {
  const parsed = Number(String(value ?? '').trim())
  if (!Number.isInteger(parsed) || parsed < min || parsed > max) return undefined
  return parsed
}

function barWidth(value: number | null): string {
  return `width: ${Math.min(100, Math.max(0, value ?? 0))}%`
}

function statusClass(status?: string): string {
  switch (status) {
    case 'critical':
      return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
    case 'warning':
      return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400'
    case 'ok':
      return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400'
    case 'warming_up':
      return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400'
    default:
      return 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300'
  }
}

function barClass(value: number | null): string {
  if (value !== null && value >= 95) return 'bg-red-500 dark:bg-red-600'
  if (value !== null && value >= 80) return 'bg-amber-500 dark:bg-amber-600'
  if (value !== null) return 'bg-green-500 dark:bg-green-600'
  return 'bg-cyan-500 dark:bg-cyan-400'
}

function formatIfaceOption(item: OpsNetworkInterfaceInfo): string {
  const tags: string[] = []
  if (item.is_default) tags.push(t('admin.ops.network.defaultIface'))
  if (item.state) tags.push(item.state)
  return tags.length > 0 ? `${item.name} (${tags.join(' / ')})` : item.name
}
</script>

<template>
  <div class="flex h-full flex-col rounded-3xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700">
    <div class="mb-4 flex shrink-0 items-center justify-between gap-3">
      <h3 class="flex min-w-0 items-center gap-2 text-sm font-bold text-gray-900 dark:text-white">
        <Icon name="globe" size="sm" class="shrink-0 text-cyan-500" :stroke-width="2" />
        <span class="truncate">{{ t('admin.ops.network.title') }}</span>
      </h3>
      <div class="flex shrink-0 items-center gap-2">
        <span v-if="summary" class="rounded-full px-2 py-1 text-[10px] font-bold" :class="statusClass(summary.status)">
          {{ t(`admin.ops.network.status.${summary.status}`) }}
        </span>
        <button
          data-testid="network-settings-button"
          class="rounded-lg bg-gray-100 p-1.5 text-gray-600 transition-colors hover:bg-gray-200 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600"
          :disabled="settingsLoading || saving"
          :title="t('admin.ops.network.settings')"
          @click="openSettings"
        >
          <Icon name="cog" size="xs" :stroke-width="2" />
        </button>
        <button
          class="rounded-lg bg-gray-100 p-1.5 text-gray-600 transition-colors hover:bg-gray-200 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600"
          :disabled="loading"
          :title="t('common.refresh')"
          @click="loadData"
        >
          <Icon name="refresh" size="xs" :stroke-width="2" :class="{ 'animate-spin': loading }" />
        </button>
      </div>
    </div>

    <div v-if="errorMessage" class="mb-3 shrink-0 rounded-xl bg-red-50 p-2.5 text-xs text-red-600 dark:bg-red-900/20 dark:text-red-400">
      {{ errorMessage }}
    </div>

    <div
      v-if="!enabled"
      class="flex flex-1 items-center justify-center rounded-xl border border-dashed border-gray-200 px-4 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400"
    >
      {{ t('admin.ops.network.disabledHint') }}
    </div>

    <div v-else-if="summary" class="flex min-h-0 flex-1 flex-col">
      <div class="grid grid-cols-2 gap-3">
        <div class="min-w-0 rounded-xl bg-cyan-50/70 px-3 py-2.5 dark:bg-cyan-950/20">
          <div class="mb-1 flex items-center justify-between gap-2">
            <div class="text-xs font-bold text-cyan-600 dark:text-cyan-300">{{ t('admin.ops.network.rx') }}</div>
            <div class="truncate text-[11px] text-gray-500 dark:text-gray-400">{{ formatBytes(summary.rx_bytes_delta) }}</div>
          </div>
          <div class="min-w-0">
            <span class="block truncate font-mono text-2xl font-black leading-none text-gray-900 dark:text-white">{{ formatMbps(summary.rx_mbps) }}</span>
            <span class="mt-1 block text-[11px] font-semibold text-gray-500 dark:text-gray-400">Mbps</span>
          </div>
        </div>
        <div class="min-w-0 rounded-xl bg-emerald-50/70 px-3 py-2.5 dark:bg-emerald-950/20">
          <div class="mb-1 flex items-center justify-between gap-2">
            <div class="text-xs font-bold text-emerald-600 dark:text-emerald-300">{{ t('admin.ops.network.tx') }}</div>
            <div class="truncate text-[11px] text-gray-500 dark:text-gray-400">{{ formatBytes(summary.tx_bytes_delta) }}</div>
          </div>
          <div class="min-w-0">
            <span class="block truncate font-mono text-2xl font-black leading-none text-gray-900 dark:text-white">{{ formatMbps(summary.tx_mbps) }}</span>
            <span class="mt-1 block text-[11px] font-semibold text-gray-500 dark:text-gray-400">Mbps</span>
          </div>
        </div>
      </div>

      <div class="mt-5 space-y-2 border-t border-gray-100 pt-4 dark:border-dark-700">
        <div class="flex items-center justify-between gap-3">
          <span class="text-xs font-semibold text-gray-500 dark:text-gray-400">
            {{ hasAnyLimit ? `${limitDirection} ${t('admin.ops.network.limit')}` : t('admin.ops.network.limit') }}
          </span>
          <span class="min-w-0 truncate text-right font-mono text-sm font-bold text-gray-900 dark:text-white">
            {{ formatLimitSummary() }}
          </span>
        </div>
        <div class="h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
          <div class="h-full rounded-full transition-all duration-300" :class="barClass(primaryUtilization)" :style="barWidth(primaryUtilization)"></div>
        </div>
        <div class="flex items-center justify-between gap-3 text-[11px] text-gray-500 dark:text-gray-400">
          <span class="truncate">{{ t('admin.ops.network.iface') }} {{ summary.iface || '--' }}</span>
          <span v-if="hasAnyLimit" class="shrink-0">{{ t('admin.ops.network.manualLimit') }}</span>
          <span v-else class="shrink-0">{{ t('admin.ops.network.observing') }}</span>
        </div>
      </div>

      <div class="mt-4 grid grid-cols-3 gap-3 border-t border-gray-100 pt-4 text-[11px] dark:border-dark-700">
        <div class="min-w-0">
          <div class="text-gray-400">{{ t('admin.ops.network.avg1m') }}</div>
          <div class="mt-1 truncate font-mono font-bold text-gray-800 dark:text-gray-100">
            {{ formatMbps(summary.rx_avg_1m_mbps) }} / {{ formatMbps(summary.tx_avg_1m_mbps) }}
          </div>
        </div>
        <div class="min-w-0">
          <div class="text-gray-400">{{ t('admin.ops.network.avg5m') }}</div>
          <div class="mt-1 truncate font-mono font-bold text-gray-800 dark:text-gray-100">
            {{ formatMbps(summary.rx_avg_5m_mbps) }} / {{ formatMbps(summary.tx_avg_5m_mbps) }}
          </div>
        </div>
        <div class="min-w-0">
          <div class="text-gray-400">{{ t('admin.ops.network.peak1h') }}</div>
          <div class="mt-1 truncate font-mono font-bold text-gray-800 dark:text-gray-100">
            {{ formatMbps(summary.rx_peak_1h_mbps) }} / {{ formatMbps(summary.tx_peak_1h_mbps) }}
          </div>
        </div>
      </div>

      <div
        v-if="hasPacketSignal"
        class="mt-4 rounded-xl bg-amber-50 p-2.5 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
      >
        {{ t('admin.ops.network.packetWarning') }}
      </div>
    </div>

    <div v-else class="flex flex-1 items-center justify-center rounded-xl border border-dashed border-gray-200 px-4 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
      {{ loading ? t('common.loading') : t('admin.ops.network.empty') }}
    </div>

    <BaseDialog :show="showSettings" :title="t('admin.ops.network.settings')" width="normal" @close="showSettings = false">
      <form class="space-y-5" @submit.prevent="saveSettings">
        <div v-if="formError" class="rounded-xl bg-red-50 p-3 text-sm text-red-600 dark:bg-red-900/20 dark:text-red-400">
          {{ formError }}
        </div>

        <div v-if="settingsLoading" class="flex items-center justify-center py-8 text-sm text-gray-500 dark:text-gray-400">
          <Icon name="refresh" size="sm" class="mr-2 animate-spin" />
          {{ t('common.loading') }}
        </div>

        <template v-else>
          <label class="flex items-center justify-between gap-4 rounded-xl bg-gray-50 px-4 py-3 dark:bg-dark-900">
            <span>
              <span class="block text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.ops.network.enableMonitor') }}</span>
              <span class="mt-0.5 block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.network.enableMonitorHint') }}</span>
            </span>
            <input v-model="enabledInput" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
          </label>

          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <label class="block">
              <span class="mb-1.5 block text-xs font-semibold text-gray-600 dark:text-gray-300">{{ t('admin.ops.network.txLimit') }}</span>
              <div class="relative">
                <input
                  v-model="txLimitInput"
                  type="number"
                  min="0"
                  step="0.1"
                  class="input pr-14"
                  :placeholder="t('admin.ops.network.limitPlaceholder')"
                />
                <span class="pointer-events-none absolute inset-y-0 right-3 flex items-center text-xs text-gray-400">Mbps</span>
              </div>
            </label>

            <label class="block">
              <span class="mb-1.5 block text-xs font-semibold text-gray-600 dark:text-gray-300">{{ t('admin.ops.network.rxLimit') }}</span>
              <div class="relative">
                <input
                  v-model="rxLimitInput"
                  type="number"
                  min="0"
                  step="0.1"
                  class="input pr-14"
                  :placeholder="t('admin.ops.network.limitPlaceholder')"
                />
                <span class="pointer-events-none absolute inset-y-0 right-3 flex items-center text-xs text-gray-400">Mbps</span>
              </div>
            </label>
          </div>

          <div
            data-testid="retry-protection-section"
            class="rounded-xl border p-4 transition-colors"
            :class="retryProtectionEnabledInput
              ? 'border-amber-200 bg-amber-50/70 dark:border-amber-900/60 dark:bg-amber-950/20'
              : 'border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-900'"
          >
            <div class="flex items-start justify-between gap-4">
              <div class="min-w-0">
                <div class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.ops.network.retryProtection') }}</div>
                <p class="mt-0.5 text-xs leading-5 text-gray-600 dark:text-gray-400">{{ t('admin.ops.network.retryProtectionHint') }}</p>
              </div>
              <Toggle v-model="retryProtectionEnabledInput" data-testid="retry-protection-toggle" class="mt-0.5" />
            </div>

            <div v-if="retryProtectionEnabledInput" class="mt-4 space-y-4 border-t border-amber-200/80 pt-4 dark:border-amber-900/60">
              <div v-if="retryEstimate" class="flex gap-3 text-xs leading-5 text-gray-600 dark:text-gray-300">
                <Icon name="shield" size="sm" class="mt-0.5 shrink-0 text-amber-600 dark:text-amber-400" />
                <div class="min-w-0">
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="font-semibold text-gray-900 dark:text-white">{{ t('admin.ops.network.retryCurrentStrategy') }}</span>
                    <span class="rounded bg-amber-100 px-1.5 py-0.5 text-[11px] font-medium text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">
                      {{ retryUsesRecommendedSettings ? t('admin.ops.network.retryPresetRecommended') : t('admin.ops.network.retryPresetCustom') }}
                    </span>
                  </div>
                  <p class="mt-1">
                    {{ t('admin.ops.network.retryStrategySummary', {
                      trigger: retryProtectionTriggerInput,
                      window: retryProtectionWindowInput,
                      repeats: retryProtectionMaxRepeatsInput
                    }) }}
                  </p>
                  <p class="mt-1 text-gray-500 dark:text-gray-400">
                    {{ t('admin.ops.network.retryEstimateSummary', {
                      direction: retryEstimate.direction,
                      limit: formatMbps(retryEstimate.limitMbps),
                      candidate: formatBytes(retryEstimate.effectiveCandidateBytes),
                      budget: formatBytes(retryEstimate.budgetBytes),
                      count: retryEstimate.blockAttempt
                    }) }}
                  </p>
                </div>
              </div>

              <div v-else class="flex gap-2 text-xs leading-5 text-amber-700 dark:text-amber-300">
                <Icon name="infoCircle" size="sm" class="mt-0.5 shrink-0" />
                <span>{{ t('admin.ops.network.retryNeedsLimitHint') }}</span>
              </div>

              <button
                data-testid="retry-advanced-toggle"
                type="button"
                class="flex w-full items-center justify-between border-t border-amber-200/80 pt-3 text-left text-sm font-medium text-gray-700 hover:text-gray-900 dark:border-amber-900/60 dark:text-gray-300 dark:hover:text-white"
                :aria-expanded="retryAdvancedOpen"
                @click="retryAdvancedOpen = !retryAdvancedOpen"
              >
                <span class="flex items-center gap-2">
                  <Icon name="cog" size="sm" />
                  {{ t('admin.ops.network.retryAdvancedSettings') }}
                </span>
                <Icon :name="retryAdvancedOpen ? 'chevronUp' : 'chevronDown'" size="sm" />
              </button>

              <div v-if="retryAdvancedOpen" class="space-y-4 border-t border-amber-200/80 pt-4 dark:border-amber-900/60">
                <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                  <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.ops.network.retryAdvancedHint') }}</p>
                  <button type="button" class="btn btn-secondary btn-sm shrink-0 self-start" @click="applyRecommendedRetryProtection">
                    {{ t('admin.ops.network.retryUseRecommended') }}
                  </button>
                </div>

                <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <label class="block">
                    <span class="mb-1.5 block text-xs font-semibold text-gray-600 dark:text-gray-300">{{ t('admin.ops.network.retryTriggerPercent') }}</span>
                    <div class="relative">
                      <input v-model="retryProtectionTriggerInput" type="number" min="1" max="100" step="1" class="input pr-10" />
                      <span class="pointer-events-none absolute inset-y-0 right-3 flex items-center text-xs text-gray-400">%</span>
                    </div>
                  </label>

                  <label class="block">
                    <span class="mb-1.5 block text-xs font-semibold text-gray-600 dark:text-gray-300">{{ t('admin.ops.network.retryMinBody') }}</span>
                    <div class="relative">
                      <input data-testid="retry-min-body-input" v-model="retryProtectionMinBodyMBInput" type="number" min="0.001" max="256" step="0.001" class="input pr-12" />
                      <span class="pointer-events-none absolute inset-y-0 right-3 flex items-center text-xs text-gray-400">MB</span>
                    </div>
                    <p class="mt-1 text-[11px] leading-4 text-gray-500 dark:text-gray-400">{{ t('admin.ops.network.retryMinBodyHint') }}</p>
                  </label>

                  <label class="block">
                    <span class="mb-1.5 block text-xs font-semibold text-gray-600 dark:text-gray-300">{{ t('admin.ops.network.retryWindow') }}</span>
                    <div class="relative">
                      <input v-model="retryProtectionWindowInput" type="number" min="1" max="3600" step="1" class="input pr-10" />
                      <span class="pointer-events-none absolute inset-y-0 right-3 flex items-center text-xs text-gray-400">s</span>
                    </div>
                  </label>

                  <label class="block">
                    <span class="mb-1.5 block text-xs font-semibold text-gray-600 dark:text-gray-300">{{ t('admin.ops.network.retryMaxRepeats') }}</span>
                    <input v-model="retryProtectionMaxRepeatsInput" type="number" min="1" max="10" step="1" class="input" />
                  </label>
                </div>
              </div>

              <div class="flex gap-2 text-xs leading-5 text-amber-800 dark:text-amber-200">
                <Icon name="exclamationTriangle" size="sm" class="mt-0.5 shrink-0" />
                <span>{{ t('admin.ops.network.retryProtectionWarning') }}</span>
              </div>
            </div>
          </div>

          <label class="block">
            <span class="mb-1.5 block text-xs font-semibold text-gray-600 dark:text-gray-300">{{ t('admin.ops.network.iface') }}</span>
            <select v-model="ifaceInput" class="input">
              <option value="">
                {{ t('admin.ops.network.ifaceAuto', { iface: defaultIface || t('admin.ops.network.ifaceUnknown') }) }}
              </option>
              <option v-for="item in ifaceOptions" :key="item.name" :value="item.name">
                {{ formatIfaceOption(item) }}
              </option>
            </select>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.ops.network.ifaceHint') }}
            </p>
          </label>

          <div class="rounded-xl bg-cyan-50 px-4 py-3 text-xs leading-5 text-gray-600 dark:bg-cyan-950/20 dark:text-gray-400">
            {{ t('admin.ops.network.manualLimitHint') }}
          </div>
        </template>

        <div class="flex items-center justify-end gap-2 border-t border-gray-100 pt-4 dark:border-dark-700">
          <button type="button" class="btn btn-secondary" :disabled="saving" @click="showSettings = false">
            {{ t('common.cancel') }}
          </button>
          <button type="submit" class="btn btn-primary" :disabled="saving || settingsLoading">
            <Icon v-if="saving" name="refresh" size="xs" class="mr-1.5 animate-spin" />
            {{ t('common.save') }}
          </button>
        </div>
      </form>
    </BaseDialog>
  </div>
</template>
