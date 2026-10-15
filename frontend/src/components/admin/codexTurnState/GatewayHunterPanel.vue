<template>
  <div class="ghp" data-test="ghp">
    <header class="ghp-header">
      <div>
        <div class="ghp-eyebrow">SUB2API / CODEX GATEWAY</div>
        <h1 class="ghp-title">{{ t('admin.codexTurnState.hunter.title') }}</h1>
        <p class="ghp-subtitle">{{ t('admin.codexTurnState.hunter.subtitle') }}</p>
      </div>
      <div class="ghp-header-status">
        <span class="ghp-pill" :class="state?.running ? 'is-ok' : 'is-muted'" data-test="ghp-running">
          {{ state?.running ? t('admin.codexTurnState.hunter.running') : t('admin.codexTurnState.hunter.stopped') }}
        </span>
        <span class="ghp-freshness" :class="{ 'is-bad': loadError }">
          <template v-if="lastLoadedAt">{{ t('admin.codexTurnState.hunter.refreshedAgo', { seconds: secondsSince(lastLoadedAt / 1000) }) }}</template>
          <template v-if="paused"> {{ t('admin.codexTurnState.hunter.pausedSuffix') }}</template>
        </span>
        <button type="button" class="ghp-btn" data-test="ghp-pause" @click="paused = !paused">
          {{ paused ? t('admin.codexTurnState.hunter.resume') : t('admin.codexTurnState.hunter.pause') }}
        </button>
      </div>
    </header>

    <nav class="ghp-tabs" role="tablist">
      <button
        v-for="tab in tabs"
        :key="tab"
        type="button"
        role="tab"
        class="ghp-tab"
        :class="{ 'is-active': activeTab === tab }"
        :aria-selected="activeTab === tab"
        :data-test="`ghp-tab-${tab}`"
        @click="activeTab = tab"
      >
        {{ t(`admin.codexTurnState.hunter.tabs.${tab}`) }}
      </button>
    </nav>

    <div v-if="loadError" role="alert" class="ghp-notice is-bad" data-test="ghp-error">{{ loadError }}</div>
    <div v-if="state?.last_error" class="ghp-notice is-warn">{{ t('admin.codexTurnState.hunter.lastError') }}：{{ state.last_error }}</div>

    <template v-if="state && activeTab === 'overview'">
      <section class="ghp-card ghp-summary">
        <div class="ghp-summary-item">
          <div class="ghp-label">{{ t('admin.codexTurnState.hunter.summary.pool') }}</div>
          <div class="ghp-big" data-test="ghp-pool">{{ state.gateways.length }}</div>
          <div class="ghp-hint">{{ t('admin.codexTurnState.hunter.summary.poolHint', { scouts: roleCount('scout') }) }}</div>
        </div>
        <div class="ghp-summary-item">
          <div class="ghp-label">{{ t('admin.codexTurnState.hunter.summary.hot') }}</div>
          <div class="ghp-big" :class="{ 'is-ok': hotGateways.length > 0 }" data-test="ghp-hot">{{ hotGateways.length }}</div>
          <div class="ghp-hint">{{ hotGateways.map((g) => g.gateway).join(' · ') || t('admin.codexTurnState.hunter.summary.noHot') }}</div>
        </div>
        <div class="ghp-summary-item">
          <div class="ghp-label">{{ t('admin.codexTurnState.hunter.summary.status') }}</div>
          <div class="ghp-strong">{{ state.running ? t('admin.codexTurnState.hunter.running') : t('admin.codexTurnState.hunter.stopped') }}</div>
          <div class="ghp-hint">
            <template v-if="state.started_at">{{ t('admin.codexTurnState.hunter.summary.uptime', { duration: formatDuration(state.counters.running_seconds) }) }}</template>
            <template v-else>{{ state.config.enabled ? t('admin.codexTurnState.hunter.summary.waitingLeader') : t('admin.codexTurnState.hunter.summary.disabled') }}</template>
          </div>
        </div>
        <div class="ghp-summary-item">
          <div class="ghp-label">{{ t('admin.codexTurnState.hunter.summary.coverage') }}</div>
          <div class="ghp-big">{{ coveragePercent }}</div>
          <div class="ghp-hint">{{ t('admin.codexTurnState.hunter.summary.coverageHint', { covered: formatDuration(state.counters.covered_seconds) }) }}</div>
        </div>
      </section>

      <section class="ghp-card">
        <div class="ghp-card-head">
          <h2 class="ghp-h2">{{ t('admin.codexTurnState.hunter.hotTitle') }}</h2>
          <span class="ghp-hint">{{ t('admin.codexTurnState.hunter.hotHint') }}</span>
        </div>
        <div class="ghp-table-wrap">
          <table class="ghp-table">
            <thead>
              <tr>
                <th>{{ t('admin.codexTurnState.hunter.columns.gateway') }}</th>
                <th>{{ t('admin.codexTurnState.hunter.columns.hotFor') }}</th>
                <th v-for="acc in probeAccounts" :key="acc.id">#{{ acc.id }} {{ acc.name }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="hotGateways.length === 0">
                <td :colspan="2 + probeAccounts.length" class="ghp-empty">{{ t('admin.codexTurnState.hunter.noHotRows') }}</td>
              </tr>
              <tr v-for="gw in hotGateways" :key="gw.gateway" class="is-hot" :data-test="`ghp-hot-${gw.gateway}`">
                <td class="ghp-mono">{{ gw.gateway }}</td>
                <td>{{ formatDuration(secondsSince(gw.hot_since ?? 0)) }}</td>
                <td v-for="acc in probeAccounts" :key="acc.id">
                  <span class="ghp-tier" :class="tierClass(gw.hot_accounts[String(acc.id)])">{{ tierLabel(gw.hot_accounts[String(acc.id)]) }}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="ghp-counters">
        <div v-for="item in counterCards" :key="item.key" class="ghp-card ghp-counter">
          <span class="ghp-counter-label">{{ item.label }}</span>
          <span class="ghp-counter-value">{{ item.value }}</span>
        </div>
      </section>

      <section class="ghp-card">
        <div class="ghp-card-head">
          <h2 class="ghp-h2">{{ t('admin.codexTurnState.hunter.accountsTitle') }}</h2>
        </div>
        <div class="ghp-table-wrap">
          <table class="ghp-table">
            <thead>
              <tr>
                <th>{{ t('admin.codexTurnState.hunter.columns.account') }}</th>
                <th>{{ t('admin.codexTurnState.hunter.columns.role') }}</th>
                <th>{{ t('admin.codexTurnState.hunter.columns.usage') }}</th>
                <th>{{ t('admin.codexTurnState.hunter.columns.probesToday') }}</th>
                <th>{{ t('admin.codexTurnState.hunter.columns.status') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="state.accounts.length === 0">
                <td colspan="5" class="ghp-empty">{{ t('admin.codexTurnState.hunter.noAccounts') }}</td>
              </tr>
              <tr v-for="acc in state.accounts" :key="acc.id">
                <td>#{{ acc.id }} {{ acc.name }}</td>
                <td>{{ t(`admin.codexTurnState.hunter.roles.${acc.role}`) }}</td>
                <td class="ghp-mono">{{ acc.role === 'scout' ? '—' : `${formatPercent(acc.primary_used_percent)} / ${formatPercent(acc.secondary_used_percent)}` }}</td>
                <td>{{ acc.role === 'scout' ? '—' : acc.probes_today }}</td>
                <td>
                  <span v-if="acc.retired" class="ghp-tier is-warn">{{ reasonLabel(acc.retired_reason) }}</span>
                  <span v-else class="ghp-tier is-ok">{{ t('admin.codexTurnState.hunter.available') }}</span>
                  <span v-if="acc.last_error" class="ghp-hint ghp-error-text" :title="acc.last_error">{{ acc.last_error }}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="ghp-card">
        <div class="ghp-card-head">
          <h2 class="ghp-h2">{{ t('admin.codexTurnState.hunter.windowsTitle') }}</h2>
          <span class="ghp-hint">{{ t('admin.codexTurnState.hunter.windowsHint') }}</span>
        </div>
        <div class="ghp-table-wrap">
          <table class="ghp-table">
            <thead>
              <tr>
                <th>{{ t('admin.codexTurnState.hunter.columns.gateway') }}</th>
                <th>{{ t('admin.codexTurnState.hunter.columns.start') }}</th>
                <th>{{ t('admin.codexTurnState.hunter.columns.duration') }}</th>
                <th>{{ t('admin.codexTurnState.hunter.columns.foundBy') }}</th>
                <th>{{ t('admin.codexTurnState.hunter.columns.confirmedBy') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="state.windows.length === 0">
                <td colspan="5" class="ghp-empty">{{ t('admin.codexTurnState.hunter.noWindows') }}</td>
              </tr>
              <tr v-for="w in state.windows" :key="`${w.gateway}-${w.start}`" :class="{ 'is-hot': w.end === null }">
                <td class="ghp-mono">{{ w.gateway }}</td>
                <td>{{ formatClock(w.start) }}</td>
                <td>
                  {{ formatDuration(w.duration_seconds) }}
                  <span v-if="w.end === null" class="ghp-tier is-ok">{{ t('admin.codexTurnState.hunter.ongoing') }}</span>
                </td>
                <td>#{{ w.found_by }}</td>
                <td>{{ w.confirmed_by.map((id) => `#${id}`).join(' ') }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="ghp-card">
        <div class="ghp-card-head">
          <h2 class="ghp-h2">{{ t('admin.codexTurnState.hunter.activityTitle') }}</h2>
          <span class="ghp-hint">{{ t('admin.codexTurnState.hunter.activityHint') }}</span>
        </div>
        <div class="ghp-log" data-test="ghp-log">
          <div v-if="state.probes.length === 0" class="ghp-empty">{{ t('admin.codexTurnState.hunter.noProbes') }}</div>
          <div v-for="(p, i) in state.probes.slice(0, 80)" :key="`${p.at}-${i}`" class="ghp-log-line">
            <span class="ghp-faint">{{ formatClock(p.at) }}</span>
            <span class="ghp-phase">{{ t(`admin.codexTurnState.hunter.phases.${p.phase}`) }}</span>
            <span>#{{ p.account_id }}</span>
            <span class="ghp-mono">{{ p.gateway }}</span>
            <span class="ghp-tier" :class="tierClass(p.tier)">{{ tierLabel(p.tier) }}</span>
            <span class="ghp-faint">{{ p.answer || (p.http_status ? `HTTP ${p.http_status}` : '') }}</span>
            <span class="ghp-faint">{{ (p.total_ms / 1000).toFixed(1) }}s</span>
          </div>
        </div>
      </section>
    </template>

    <section v-if="state && activeTab === 'gateways'" class="ghp-card">
      <div class="ghp-card-head">
        <h2 class="ghp-h2">{{ t('admin.codexTurnState.hunter.poolTitle') }}</h2>
        <span class="ghp-hint">{{ t('admin.codexTurnState.hunter.poolHint') }}</span>
      </div>
      <div class="ghp-table-wrap">
        <table class="ghp-table">
          <thead>
            <tr>
              <th>{{ t('admin.codexTurnState.hunter.columns.gateway') }}</th>
              <th>{{ t('admin.codexTurnState.hunter.columns.probes') }}</th>
              <th>{{ t('admin.codexTurnState.hunter.columns.hits') }}</th>
              <th>{{ t('admin.codexTurnState.hunter.columns.lastTier') }}</th>
              <th>{{ t('admin.codexTurnState.hunter.columns.lastProbe') }}</th>
              <th>{{ t('admin.codexTurnState.hunter.columns.scouted') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="state.gateways.length === 0">
              <td colspan="6" class="ghp-empty">{{ t('admin.codexTurnState.hunter.noGateways') }}</td>
            </tr>
            <tr v-for="gw in state.gateways" :key="gw.gateway" :class="{ 'is-hot': gw.hot_since !== null }" :data-test="`ghp-gw-${gw.gateway}`">
              <td class="ghp-mono">{{ gw.gateway }}</td>
              <td>{{ gw.probes }}</td>
              <td>{{ gw.hits }}</td>
              <td><span class="ghp-tier" :class="tierClass(gw.last_tier)">{{ tierLabel(gw.last_tier) }}</span></td>
              <td>{{ gw.last_probe_at ? t('admin.codexTurnState.hunter.ago', { duration: formatDuration(secondsSince(gw.last_probe_at)) }) : '—' }}</td>
              <td>{{ t('admin.codexTurnState.hunter.ago', { duration: formatDuration(secondsSince(gw.scouted_at)) }) }} · #{{ gw.scout_account_id }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section v-if="state && activeTab === 'settings'" class="ghp-card" data-test="ghp-settings">
      <div class="ghp-card-head">
        <h2 class="ghp-h2">{{ t('admin.codexTurnState.hunter.settingsTitle') }}</h2>
        <div class="ghp-actions">
          <button v-if="state.config.enabled" type="button" class="ghp-btn" :disabled="busy" data-test="ghp-stop" @click="toggleRunning(false)">
            {{ t('admin.codexTurnState.hunter.stop') }}
          </button>
          <button v-else type="button" class="ghp-btn is-primary" :disabled="busy || dirty" data-test="ghp-start" @click="toggleRunning(true)">
            {{ t('admin.codexTurnState.hunter.start') }}
          </button>
        </div>
      </div>
      <form class="ghp-form" @submit.prevent="saveConfig">
        <label v-for="field in idFields" :key="field" class="ghp-field">
          <span class="ghp-label">{{ t(`admin.codexTurnState.hunter.fields.${field}`) }}</span>
          <input v-model="draft[field]" class="ghp-input" :data-test="`ghp-field-${field}`" />
          <span class="ghp-hint">{{ t(`admin.codexTurnState.hunter.fieldHints.${field}`) }}</span>
        </label>
        <label v-for="field in numberFields" :key="field" class="ghp-field">
          <span class="ghp-label">{{ t(`admin.codexTurnState.hunter.fields.${field}`) }}</span>
          <input v-model.number="draft[field]" type="number" min="0" class="ghp-input" :data-test="`ghp-field-${field}`" />
          <span class="ghp-hint">{{ t(`admin.codexTurnState.hunter.fieldHints.${field}`) }}</span>
        </label>
        <label class="ghp-field">
          <span class="ghp-label">{{ t('admin.codexTurnState.hunter.fields.model') }}</span>
          <input v-model="draft.model" class="ghp-input" data-test="ghp-field-model" />
          <span class="ghp-hint">{{ t('admin.codexTurnState.hunter.fieldHints.model') }}</span>
        </label>
        <div class="ghp-form-actions">
          <span v-if="dirty" class="ghp-hint">{{ t('admin.codexTurnState.hunter.unsaved') }}</span>
          <button type="button" class="ghp-btn" :disabled="!dirty || busy" @click="resetDraft">{{ t('admin.codexTurnState.hunter.discard') }}</button>
          <button type="submit" class="ghp-btn is-primary" :disabled="!dirty || busy" data-test="ghp-save">{{ t('admin.codexTurnState.hunter.save') }}</button>
        </div>
      </form>
    </section>

    <div v-if="!state && !loadError" class="ghp-card ghp-empty">{{ t('common.loading') }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  codexGatewayHunterAPI,
  type GatewayHunterConfig,
  type GatewayHunterState,
  type GatewayTier,
} from '@/api/admin/codexGatewayHunter'

// 满血网关监控：读取后端监控状态（5 秒轮询，标签页隐藏或手动暂停时停止），并管理运行配置。
const REFRESH_INTERVAL_MS = 5000

const { t, te } = useI18n()
const appStore = useAppStore()

const tabs = ['overview', 'gateways', 'settings'] as const
type Tab = (typeof tabs)[number]
const activeTab = ref<Tab>('overview')

const state = ref<GatewayHunterState | null>(null)
const loadError = ref('')
const lastLoadedAt = ref(0)
const now = ref(Date.now())
const paused = ref(false)
const busy = ref(false)

const idFields = ['hunter_account_ids', 'tracker_account_ids'] as const
const numberFields = [
  'scout_group_id',
  'scout_proxy_id',
  'probe_proxy_id',
  'workers',
  'min_probe_interval_seconds',
  'scout_interval_seconds',
  'tracker_daily_budget',
] as const

interface ConfigDraft {
  model: string
  hunter_account_ids: string
  tracker_account_ids: string
  scout_group_id: number
  scout_proxy_id: number
  probe_proxy_id: number
  workers: number
  min_probe_interval_seconds: number
  scout_interval_seconds: number
  tracker_daily_budget: number
}

const draft = reactive<ConfigDraft>({
  model: '',
  hunter_account_ids: '',
  tracker_account_ids: '',
  scout_group_id: 0,
  scout_proxy_id: 0,
  probe_proxy_id: 0,
  workers: 0,
  min_probe_interval_seconds: 0,
  scout_interval_seconds: 0,
  tracker_daily_budget: 0,
})

const toDraft = (cfg: GatewayHunterConfig): ConfigDraft => ({
  model: cfg.model,
  hunter_account_ids: cfg.hunter_account_ids.join(', '),
  tracker_account_ids: cfg.tracker_account_ids.join(', '),
  scout_group_id: cfg.scout_group_id,
  scout_proxy_id: cfg.scout_proxy_id,
  probe_proxy_id: cfg.probe_proxy_id,
  workers: cfg.workers,
  min_probe_interval_seconds: cfg.min_probe_interval_seconds,
  scout_interval_seconds: cfg.scout_interval_seconds,
  tracker_daily_budget: cfg.tracker_daily_budget,
})

const parseIDs = (value: string): number[] =>
  value
    .split(/[\s,，]+/)
    .map((item) => Number(item))
    .filter((id) => Number.isInteger(id) && id > 0)

const fromDraft = (enabled: boolean, source: ConfigDraft = draft): GatewayHunterConfig => ({
  enabled,
  model: source.model.trim(),
  hunter_account_ids: parseIDs(source.hunter_account_ids),
  tracker_account_ids: parseIDs(source.tracker_account_ids),
  scout_group_id: Number(source.scout_group_id) || 0,
  scout_proxy_id: Number(source.scout_proxy_id) || 0,
  probe_proxy_id: Number(source.probe_proxy_id) || 0,
  workers: Number(source.workers) || 0,
  min_probe_interval_seconds: Number(source.min_probe_interval_seconds) || 0,
  scout_interval_seconds: Number(source.scout_interval_seconds) || 0,
  tracker_daily_budget: Number(source.tracker_daily_budget) || 0,
})

// 比较时两边都经过同一次 draft 往返，字段顺序与格式一致。
const configKey = (cfg: GatewayHunterConfig) => JSON.stringify(fromDraft(cfg.enabled, toDraft(cfg)))
const draftDiffers = (cfg: GatewayHunterConfig) => JSON.stringify(fromDraft(cfg.enabled)) !== configKey(cfg)

const dirty = computed(() => (state.value ? draftDiffers(state.value.config) : false))

const resetDraft = () => {
  if (state.value) Object.assign(draft, toDraft(state.value.config))
}

// 轮询不覆盖未保存的草稿。
watch(
  () => state.value?.config,
  (cfg, previous) => {
    if (!cfg) return
    if (!previous || !draftDiffers(previous)) {
      Object.assign(draft, toDraft(cfg))
    }
  },
)

const hotGateways = computed(() => (state.value?.gateways ?? []).filter((gw) => gw.hot_since !== null))
const probeAccounts = computed(() => (state.value?.accounts ?? []).filter((acc) => acc.role !== 'scout'))
const roleCount = (role: string) => (state.value?.accounts ?? []).filter((acc) => acc.role === role).length

const coveragePercent = computed(() => {
  const counters = state.value?.counters
  if (!counters || counters.running_seconds <= 0) return '—'
  return `${((counters.covered_seconds / counters.running_seconds) * 100).toFixed(1)}%`
})

const ratio = (hits: number, total: number) => `${hits} / ${total}`
const counterCards = computed(() => {
  const c = state.value?.counters
  if (!c) return []
  return [
    { key: 'hunt', label: t('admin.codexTurnState.hunter.counters.hunt'), value: ratio(c.hunt_hits, c.hunts) },
    { key: 'track', label: t('admin.codexTurnState.hunter.counters.track'), value: ratio(c.track_hits, c.tracks) },
    { key: 'bare', label: t('admin.codexTurnState.hunter.counters.bare'), value: ratio(c.bare_hits, c.bare_probes) },
    { key: 'windows', label: t('admin.codexTurnState.hunter.counters.windows'), value: String(c.windows) },
    { key: 'scout', label: t('admin.codexTurnState.hunter.counters.scout'), value: ratio(c.scouts - c.scout_errors, c.scouts) },
  ]
})

const secondsSince = (unixSeconds: number) => Math.max(0, Math.round(now.value / 1000 - unixSeconds))

const formatDuration = (seconds: number) => {
  const s = Math.max(0, Math.round(seconds))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m${String(s % 60).padStart(2, '0')}s`
  return `${Math.floor(s / 3600)}h${String(Math.floor((s % 3600) / 60)).padStart(2, '0')}m`
}

const formatClock = (unixSeconds: number) => new Date(unixSeconds * 1000).toLocaleTimeString([], { hour12: false })

const formatPercent = (value: number | null) => (value === null ? '—' : `${value}%`)

const tierClass = (tier: GatewayTier | '' | undefined) => {
  switch (tier) {
    case 'full':
      return 'is-ok'
    case 'pruned':
      return 'is-warn'
    case 'degraded':
    case 'error':
      return 'is-bad'
    default:
      return 'is-muted'
  }
}

const tierLabel = (tier: GatewayTier | '' | undefined) =>
  tier ? t(`admin.codexTurnState.hunter.tiers.${tier}`) : '—'

const reasonLabel = (reason: string) => {
  const key = `admin.codexTurnState.hunter.reasons.${reason}`
  return te(key) ? t(key) : reason
}

let loadVersion = 0
const load = async () => {
  const version = ++loadVersion
  try {
    const next = await codexGatewayHunterAPI.getState()
    if (version !== loadVersion) return
    state.value = next
    loadError.value = ''
    lastLoadedAt.value = Date.now()
  } catch (error) {
    if (version === loadVersion) loadError.value = extractApiErrorMessage(error, t('admin.codexTurnState.hunter.loadFailed'))
  }
}

const saveConfig = async () => {
  if (!state.value) return
  busy.value = true
  try {
    await codexGatewayHunterAPI.updateConfig(fromDraft(state.value.config.enabled))
    appStore.showSuccess(t('admin.codexTurnState.hunter.saved'))
    const fresh = await codexGatewayHunterAPI.getState()
    state.value = fresh
    Object.assign(draft, toDraft(fresh.config))
    lastLoadedAt.value = Date.now()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.codexTurnState.hunter.saveFailed')))
  } finally {
    busy.value = false
  }
}

const toggleRunning = async (enabled: boolean) => {
  busy.value = true
  try {
    state.value = enabled ? await codexGatewayHunterAPI.start() : await codexGatewayHunterAPI.stop()
    lastLoadedAt.value = Date.now()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.codexTurnState.hunter.saveFailed')))
  } finally {
    busy.value = false
  }
}

let refreshTimer: ReturnType<typeof setInterval> | undefined
let tickTimer: ReturnType<typeof setInterval> | undefined

const shouldPoll = () => !paused.value && document.visibilityState !== 'hidden'
const onVisibilityChange = () => {
  if (shouldPoll()) load()
}

onMounted(() => {
  load()
  refreshTimer = setInterval(() => {
    if (shouldPoll()) load()
  }, REFRESH_INTERVAL_MS)
  tickTimer = setInterval(() => {
    now.value = Date.now()
  }, 1000)
  document.addEventListener('visibilitychange', onVisibilityChange)
})

onUnmounted(() => {
  clearInterval(refreshTimer)
  clearInterval(tickTimer)
  document.removeEventListener('visibilitychange', onVisibilityChange)
})
</script>

<style scoped>
/* 视觉沿用 CPA codex-turn-state 控制台：白底、灰绿细边框、低饱和状态色、深色主按钮、下划线标签页。 */
.ghp {
  --ghp-fg: #28302a;
  --ghp-muted: #707a70;
  --ghp-faint: #899285;
  --ghp-border: #e3e7e1;
  --ghp-card: #f7f8f5;
  --ghp-raised: #fff;
  --ghp-ok: #3e654d;
  --ghp-ok-bg: #edf4ee;
  --ghp-warn: #947536;
  --ghp-warn-bg: #faf4e7;
  --ghp-bad: #b35449;
  --ghp-bad-bg: #fcf0ee;
  --ghp-accent: #3b523a;
  color: var(--ghp-fg);
  font-size: 14px;
  line-height: 1.6;
  display: flex;
  flex-direction: column;
  gap: 20px;
}
:global(.dark) .ghp {
  --ghp-fg: #e6e8ec;
  --ghp-muted: #9aa1ab;
  --ghp-faint: #6b7280;
  --ghp-border: #2b2f36;
  --ghp-card: #1a1d22;
  --ghp-raised: #1f232a;
  --ghp-ok: #86c39b;
  --ghp-ok-bg: #12291b;
  --ghp-warn: #e2c27a;
  --ghp-warn-bg: #2e2410;
  --ghp-bad: #f08a80;
  --ghp-bad-bg: #2e1615;
  --ghp-accent: #c9d8c5;
}
.ghp-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  flex-wrap: wrap;
}
.ghp-eyebrow {
  font-size: 10px;
  font-weight: 650;
  letter-spacing: 1.6px;
  color: var(--ghp-muted);
  margin-bottom: 8px;
}
.ghp-title {
  font-size: 28px;
  line-height: 1.3;
  letter-spacing: -0.5px;
  font-weight: 650;
}
.ghp-subtitle {
  font-size: 12px;
  color: var(--ghp-muted);
  margin-top: 8px;
}
.ghp-header-status {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}
.ghp-freshness {
  font-size: 12px;
  color: var(--ghp-muted);
}
.ghp-freshness.is-bad {
  color: var(--ghp-bad);
}
.ghp-pill {
  font-size: 12px;
  padding: 2px 10px;
  border-radius: 4px;
}
.ghp-pill.is-ok {
  background: var(--ghp-ok-bg);
  color: var(--ghp-ok);
}
.ghp-pill.is-muted {
  background: var(--ghp-card);
  color: var(--ghp-muted);
}
.ghp-btn {
  border: 1px solid var(--ghp-border);
  background: var(--ghp-raised);
  color: var(--ghp-fg);
  border-radius: 6px;
  padding: 8px 14px;
  font-size: 13px;
  font-weight: 600;
}
.ghp-btn:hover:not(:disabled) {
  border-color: var(--ghp-faint);
}
.ghp-btn.is-primary {
  background: var(--ghp-accent);
  border-color: var(--ghp-accent);
  color: var(--ghp-raised);
}
.ghp-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.ghp-tabs {
  display: flex;
  gap: 28px;
  border-bottom: 1px solid var(--ghp-border);
}
.ghp-tab {
  padding: 10px 2px;
  color: var(--ghp-muted);
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
}
.ghp-tab.is-active {
  color: var(--ghp-fg);
  font-weight: 650;
  border-bottom-color: var(--ghp-accent);
}
.ghp-notice {
  border-radius: 6px;
  padding: 10px 14px;
  font-size: 13px;
}
.ghp-notice.is-bad {
  background: var(--ghp-bad-bg);
  color: var(--ghp-bad);
}
.ghp-notice.is-warn {
  background: var(--ghp-warn-bg);
  color: var(--ghp-warn);
}
.ghp-card {
  border: 1px solid var(--ghp-border);
  background: var(--ghp-raised);
  border-radius: 8px;
  overflow: hidden;
}
.ghp-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 18px 24px;
  flex-wrap: wrap;
}
.ghp-h2 {
  font-size: 15px;
  font-weight: 650;
}
.ghp-summary {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  padding: 22px 0;
}
.ghp-summary-item {
  padding: 0 24px;
  border-left: 1px solid var(--ghp-border);
  min-width: 0;
}
.ghp-summary-item:first-child {
  border-left: 0;
}
.ghp-label {
  font-size: 12px;
  color: var(--ghp-muted);
}
.ghp-big {
  font-size: 28px;
  font-weight: 700;
  line-height: 1.5;
}
.ghp-strong {
  font-size: 20px;
  font-weight: 700;
  line-height: 1.9;
}
.ghp-big.is-ok {
  color: var(--ghp-ok);
}
.ghp-hint {
  font-size: 11px;
  color: var(--ghp-muted);
  overflow-wrap: anywhere;
}
.ghp-error-text {
  display: block;
  color: var(--ghp-bad);
  max-width: 320px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ghp-table-wrap {
  overflow-x: auto;
}
.ghp-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.ghp-table th {
  text-align: left;
  font-weight: 500;
  color: var(--ghp-muted);
  background: var(--ghp-card);
  padding: 10px 24px;
  border-top: 1px solid var(--ghp-border);
  border-bottom: 1px solid var(--ghp-border);
  white-space: nowrap;
}
.ghp-table td {
  padding: 12px 24px;
  border-bottom: 1px solid var(--ghp-border);
  vertical-align: middle;
}
.ghp-table tr:last-child td {
  border-bottom: 0;
}
.ghp-table tr.is-hot td {
  background: var(--ghp-ok-bg);
}
.ghp-empty {
  text-align: center;
  color: var(--ghp-faint);
  padding: 24px;
}
.ghp-mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
}
.ghp-tier {
  display: inline-block;
  font-size: 12px;
  padding: 0 8px;
  border-radius: 4px;
  white-space: nowrap;
}
.ghp-tier.is-ok {
  background: var(--ghp-ok-bg);
  color: var(--ghp-ok);
}
.ghp-tier.is-warn {
  background: var(--ghp-warn-bg);
  color: var(--ghp-warn);
}
.ghp-tier.is-bad {
  background: var(--ghp-bad-bg);
  color: var(--ghp-bad);
}
.ghp-tier.is-muted {
  color: var(--ghp-faint);
}
.ghp-counters {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 14px;
}
.ghp-counter {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  gap: 8px;
}
.ghp-counter-label {
  font-size: 11px;
  letter-spacing: 0.4px;
  color: var(--ghp-muted);
}
.ghp-counter-value {
  font-size: 22px;
  font-weight: 700;
  white-space: nowrap;
}
.ghp-log {
  border-top: 1px solid var(--ghp-border);
  max-height: 360px;
  overflow-y: auto;
  padding: 8px 24px 14px;
  font-size: 12px;
}
.ghp-log-line {
  display: flex;
  gap: 12px;
  padding: 3px 0;
  white-space: nowrap;
}
.ghp-phase {
  min-width: 36px;
  color: var(--ghp-muted);
}
.ghp-faint {
  color: var(--ghp-faint);
}
.ghp-actions {
  display: flex;
  gap: 8px;
}
.ghp-form {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 18px 28px;
  padding: 4px 24px 22px;
}
.ghp-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.ghp-input {
  border: 1px solid var(--ghp-border);
  background: var(--ghp-raised);
  color: var(--ghp-fg);
  border-radius: 6px;
  padding: 7px 10px;
  font-size: 13px;
}
.ghp-input:focus {
  outline: none;
  border-color: var(--ghp-accent);
}
.ghp-form-actions {
  grid-column: 1 / -1;
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 10px;
}
@media (max-width: 900px) {
  .ghp-summary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    row-gap: 18px;
  }
  .ghp-summary-item:nth-child(3) {
    border-left: 0;
  }
  .ghp-counters {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .ghp-form {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
