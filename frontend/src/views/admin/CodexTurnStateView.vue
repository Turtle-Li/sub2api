<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div class="text-xs text-gray-500 dark:text-gray-400" data-test="cts-updated">
          <template v-if="state">
            {{ t('admin.codexTurnState.updatedAt', { time: formatDateTime(new Date(state.generated_at * 1000)) }) }}
          </template>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="btn btn-sm"
            :class="autoRefresh ? 'btn-primary' : 'btn-secondary'"
            data-test="cts-auto-refresh"
            :title="t('admin.codexTurnState.autoRefreshHint', { seconds: REFRESH_INTERVAL_MS / 1000 })"
            @click="autoRefresh = !autoRefresh"
          >
            <Icon name="sync" size="xs" />
            <span>{{ t('admin.codexTurnState.autoRefresh') }}</span>
          </button>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" data-test="cts-refresh" @click="refreshAll">
            <Icon name="refresh" size="xs" :class="{ 'animate-spin': loading }" />
            <span>{{ t('common.refresh') }}</span>
          </button>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="!state" data-test="cts-open-proxies" @click="showProxies = true">
            <Icon name="server" size="xs" />
            <span>{{ t('admin.codexTurnState.actions.manageProxies') }}</span>
          </button>
          <button type="button" class="btn btn-primary btn-sm" :disabled="!state" data-test="cts-open-add" @click="openAdd(null)">
            <Icon name="plus" size="xs" />
            <span>{{ t('admin.codexTurnState.actions.addModel') }}</span>
          </button>
        </div>
      </div>

      <div
        v-if="loadError"
        role="alert"
        class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200"
        data-test="cts-load-error"
      >
        <div>
          <div class="font-medium">{{ loadError }}</div>
          <div class="text-xs opacity-80">{{ state ? t('admin.codexTurnState.staleHint') : t('admin.codexTurnState.unknownHint') }}</div>
        </div>
        <button type="button" class="btn btn-secondary" @click="refreshAll">{{ t('common.refresh') }}</button>
      </div>

      <div
        v-if="state && !state.probing_enabled"
        class="rounded-lg border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-800/60 dark:bg-amber-950/40 dark:text-amber-200"
        data-test="cts-paused"
      >
        {{ t('admin.codexTurnState.pausedBanner') }}
      </div>
      <div
        v-if="state && !state.proxy_count"
        class="rounded-lg border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-800/60 dark:bg-amber-950/40 dark:text-amber-200"
      >
        {{ t('admin.codexTurnState.noProxyBanner') }}
      </div>

      <div v-if="loading && !state" class="flex h-48 items-center justify-center">
        <Icon name="refresh" size="lg" class="animate-spin text-primary-600" />
      </div>

      <template v-if="state">
        <div class="grid grid-cols-2 gap-4 sm:grid-cols-4 xl:grid-cols-7">
          <div v-for="card in overviewCards" :key="card.key" class="rounded-xl border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-700 dark:bg-dark-800">
            <div class="flex items-center gap-3">
              <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg" :class="card.iconClass">
                <Icon :name="card.icon" size="md" />
              </div>
              <div class="min-w-0">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t(`admin.codexTurnState.overview.${card.key}`) }}</p>
                <p class="text-xl font-semibold" :class="card.valueClass" :data-test="`cts-overview-${card.key}`">{{ card.value }}</p>
              </div>
            </div>
          </div>
        </div>

        <section class="card p-5" data-test="cts-settings">
          <div class="flex flex-wrap items-start justify-between gap-6">
            <div class="space-y-1">
              <div class="flex items-center gap-3">
                <Toggle :model-value="state.probing_enabled" data-test="cts-probing-toggle" @update:model-value="setProbing" />
                <span class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.codexTurnState.settings.probing') }}</span>
                <span :class="state.probing_enabled ? 'badge badge-success' : 'badge badge-warning'">
                  {{ state.probing_enabled ? t('admin.codexTurnState.settings.running') : t('admin.codexTurnState.settings.paused') }}
                </span>
              </div>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.codexTurnState.settings.probingHint') }}</p>
              <p class="text-xs text-gray-500 dark:text-gray-400" data-test="cts-timing">{{ timingSummary }}</p>
            </div>
            <form class="flex flex-wrap items-end gap-2" @submit.prevent="saveAdvance">
              <label class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.codexTurnState.settings.advance') }}
                <input
                  v-model.number="advanceInput"
                  type="number"
                  min="1"
                  max="30"
                  step="1"
                  class="input mt-1 w-28"
                  data-test="cts-advance-input"
                  @input="advanceDirty = true"
                />
              </label>
              <button type="submit" class="btn btn-secondary btn-sm" :disabled="savingSettings || !advanceDirty" data-test="cts-advance-save">{{ t('common.save') }}</button>
            </form>
          </div>
          <p v-if="state.settings?.error" class="mt-2 text-xs text-red-600 dark:text-red-400">{{ t('admin.codexTurnState.settings.fileError') }}</p>
        </section>

        <div class="flex flex-wrap items-center gap-3">
          <input v-model="searchQuery" type="text" class="input w-full text-xs sm:w-64" :placeholder="t('admin.codexTurnState.filters.search')" data-test="cts-search" />
          <select v-model="statusFilter" class="input w-full text-xs sm:w-44" data-test="cts-status-filter">
            <option v-for="option in statusFilters" :key="option" :value="option">{{ t(`admin.codexTurnState.filters.${option}`) }}</option>
          </select>
        </div>

        <div v-if="!filteredAccounts.length" class="rounded-xl border border-gray-200 bg-white p-10 text-center text-sm text-gray-500 shadow-sm dark:border-dark-700 dark:bg-dark-800 dark:text-gray-400" data-test="cts-empty">
          {{ state.accounts.length ? t('admin.codexTurnState.filters.noMatch') : t('admin.codexTurnState.slots.empty') }}
        </div>

        <div
          v-for="account in filteredAccounts"
          :key="account.id"
          class="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800"
          data-test="cts-account"
        >
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 bg-gray-50/50 px-5 py-3 dark:border-dark-700 dark:bg-dark-800/80">
            <div class="flex items-center gap-2">
              <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ account.name }}</h3>
              <span class="text-xs text-gray-400">#{{ account.id }}</span>
              <span class="badge badge-gray">{{ t(`admin.codexTurnState.slots.source.${account.source === 'panel' ? 'panel' : 'config'}`) }}</span>
            </div>
            <div class="flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
              <span>{{ t('admin.codexTurnState.slots.advance', { minutes: account.refresh_advance_minutes }) }}</span>
              <button type="button" class="btn btn-secondary btn-sm" @click="openAdd({ id: account.id, name: account.name })">
                <Icon name="plus" size="xs" />
                <span>{{ t('admin.codexTurnState.actions.addModelToAccount') }}</span>
              </button>
            </div>
          </div>

          <div v-if="account.error" class="px-5 py-4 text-sm text-red-600 dark:text-red-400" data-test="cts-account-error">
            {{ t('admin.codexTurnState.slots.accountError', { error: account.error }) }}
          </div>
          <div v-else class="overflow-x-auto">
            <table class="w-full text-left text-sm">
              <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-900/40 dark:text-gray-400">
                <tr>
                  <th class="px-4 py-3">{{ t('admin.codexTurnState.columns.model') }}</th>
                  <th class="px-4 py-3">{{ t('admin.codexTurnState.columns.renewal') }}</th>
                  <th class="px-4 py-3">{{ t('admin.codexTurnState.columns.length') }}</th>
                  <th class="px-4 py-3">{{ t('admin.codexTurnState.columns.remaining') }}</th>
                  <th class="px-4 py-3">{{ t('admin.codexTurnState.columns.nextProbe') }}</th>
                  <th class="px-4 py-3">{{ t('admin.codexTurnState.columns.pinnedAt') }}</th>
                  <th v-if="state.degraded_enabled" class="px-4 py-3">{{ t('admin.codexTurnState.columns.degradation') }}</th>
                  <th class="px-4 py-3">{{ t('admin.codexTurnState.columns.history') }}</th>
                  <th class="px-4 py-3 text-right">{{ t('admin.codexTurnState.columns.actions') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="slot in account.slots" :key="slot.model" data-test="cts-slot-row">
                  <td class="px-4 py-3 font-mono font-medium text-gray-900 dark:text-white">{{ slot.model }}</td>
                  <td class="px-4 py-3">
                    <span :class="toneBadge(slot.renewal.tone)" :data-test="`cts-renewal-${account.id}-${slot.model}`">{{ slot.renewal.label }}</span>
                  </td>
                  <td class="px-4 py-3 font-mono text-xs" :class="slot.expired || slot.length_degraded ? 'text-red-600 dark:text-red-400' : slot.valid ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-400'">
                    {{ slot.state_len || '-' }}<span class="text-gray-400"> / {{ slot.target_len }}</span>
                  </td>
                  <td class="px-4 py-3 text-xs">
                    <template v-if="slot.cookie_present">
                      <div class="font-medium" :class="slot.cookie_remaining_seconds <= 0 ? 'text-red-600 dark:text-red-400' : slot.cookie_remaining_seconds <= 60 ? 'text-amber-600 dark:text-amber-400' : 'text-emerald-600 dark:text-emerald-400'">
                        {{ slot.cookie_remaining_seconds > 0 ? t('admin.codexTurnState.slots.cookie', { time: formatSeconds(slot.cookie_remaining_seconds) }) : t('admin.codexTurnState.slots.cookieExpired') }}
                      </div>
                      <div class="text-gray-400" :title="slot.expires_at || ''">{{ t('admin.codexTurnState.slots.ticket', { time: formatSeconds(slot.remaining_seconds) }) }}</div>
                    </template>
                    <span v-else-if="slot.expired" class="text-red-600 dark:text-red-400">{{ t('admin.codexTurnState.slots.expired') }}</span>
                    <span v-else-if="slot.valid" :title="slot.expires_at || ''">{{ formatSeconds(slot.remaining_seconds) }}</span>
                    <span v-else class="text-red-600 dark:text-red-400">{{ t('admin.codexTurnState.slots.none') }}</span>
                  </td>
                  <td class="px-4 py-3 text-xs">
                    <span v-if="slot.backoff_seconds > 0" class="text-amber-600 dark:text-amber-400">{{ t('admin.codexTurnState.slots.backoff', { time: formatSeconds(slot.backoff_seconds) }) }}</span>
                    <span v-else-if="slot.next_probe_seconds > 0">{{ formatSeconds(slot.next_probe_seconds) }}</span>
                    <span v-else class="text-amber-600 dark:text-amber-400">{{ t('admin.codexTurnState.slots.probing') }}</span>
                  </td>
                  <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-500 dark:text-gray-400">{{ formatTimestamp(slot.pinned_updated_at) }}</td>
                  <td v-if="state.degraded_enabled" class="px-4 py-3 text-xs">
                    <span :class="verdictBadge(slot.degradation)">{{ t(`admin.codexTurnState.verdict.${slot.degradation}`) }}</span>
                    <div v-if="slot.degradation === 'degraded' && slot.recent_degradation" class="mt-1 text-red-500">
                      {{ slot.recent_degradation.sent_model }} → {{ slot.recent_degradation.response_model }} ×{{ slot.recent_degradation.count }}
                    </div>
                  </td>
                  <td class="whitespace-nowrap px-4 py-3 font-mono text-xs text-gray-600 dark:text-gray-300" :title="slot.historyTitle" :data-test="`cts-history-${account.id}-${slot.model}`">
                    {{ slot.historyText }}
                  </td>
                  <td class="whitespace-nowrap px-4 py-3 text-right">
                    <button
                      type="button"
                      class="btn btn-secondary btn-sm mr-2"
                      :disabled="slot.busy"
                      :data-test="`cts-probe-${account.id}-${slot.model}`"
                      @click="probeSlot(account.id, slot.model)"
                    >
                      {{ slot.busy ? t('admin.codexTurnState.actions.probing') : t('admin.codexTurnState.actions.probeNow') }}
                    </button>
                    <button type="button" class="btn btn-danger btn-sm" :data-test="`cts-remove-${account.id}-${slot.model}`" @click="removeSlot(account.id, slot.model)">
                      {{ t('admin.codexTurnState.actions.remove') }}
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <section v-if="state.degraded_enabled" class="card p-5" data-test="cts-degraded">
          <div class="mb-3 flex flex-wrap items-start justify-between gap-3">
            <div>
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.codexTurnState.degraded.title', { window: state.degraded_window || '-' }) }}</h2>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.codexTurnState.degraded.hint') }}</p>
            </div>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="degradedLoading" data-test="cts-degraded-refresh" @click="refreshDegraded">
              {{ t('admin.codexTurnState.degraded.recheck') }}
            </button>
          </div>
          <p v-if="state.degraded_error" class="mb-2 text-xs text-red-600 dark:text-red-400">{{ t('admin.codexTurnState.degraded.queryFailed') }}</p>
          <div class="overflow-x-auto">
            <table class="w-full text-left text-sm">
              <thead class="text-xs uppercase text-gray-500 dark:text-gray-400">
                <tr>
                  <th class="px-3 py-2">{{ t('admin.codexTurnState.columns.account') }}</th>
                  <th class="px-3 py-2">{{ t('admin.codexTurnState.degraded.sentModel') }}</th>
                  <th class="px-3 py-2">{{ t('admin.codexTurnState.degraded.responseModel') }}</th>
                  <th class="px-3 py-2 text-right">{{ t('admin.codexTurnState.degraded.count') }}</th>
                  <th class="px-3 py-2">{{ t('admin.codexTurnState.degraded.lastSeen') }}</th>
                  <th class="px-3 py-2 text-right">TTFT</th>
                  <th class="px-3 py-2 text-right">{{ t('admin.codexTurnState.columns.actions') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-if="!state.degraded.length">
                  <td colspan="7" class="px-3 py-6 text-center text-gray-500 dark:text-gray-400">{{ t('admin.codexTurnState.degraded.empty') }}</td>
                </tr>
                <tr v-for="row in state.degraded" :key="`${row.account_id}:${row.sent_model}:${row.response_model}`" data-test="cts-degraded-row">
                  <td class="px-3 py-2">{{ row.account_name }} <span class="text-xs text-gray-400">#{{ row.account_id }}</span></td>
                  <td class="px-3 py-2 font-mono text-xs">{{ row.sent_model }}</td>
                  <td class="px-3 py-2 font-mono text-xs text-red-600 dark:text-red-400">{{ row.response_model }}</td>
                  <td class="px-3 py-2 text-right">{{ row.count }}</td>
                  <td class="whitespace-nowrap px-3 py-2 text-xs text-gray-500">{{ formatTimestamp(row.last_seen) }}</td>
                  <td class="px-3 py-2 text-right text-xs">{{ row.ttft_avg_ms ? `${Math.round(row.ttft_avg_ms)} ms` : '-' }}</td>
                  <td class="whitespace-nowrap px-3 py-2 text-right">
                    <span v-if="isMonitored(row.account_id, row.sent_model)" class="text-xs text-gray-400">{{ t('admin.codexTurnState.degraded.monitored') }}</span>
                    <button v-else type="button" class="btn btn-secondary btn-sm" :data-test="`cts-degraded-add-${row.account_id}`" @click="addFromDegraded(row)">
                      {{ t('admin.codexTurnState.degraded.add') }}
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <section class="card p-5" data-test="cts-jobs">
          <h2 class="mb-3 text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.codexTurnState.jobs.title') }}</h2>
          <div class="overflow-x-auto">
            <table class="w-full text-left text-sm">
              <thead class="text-xs uppercase text-gray-500 dark:text-gray-400">
                <tr>
                  <th class="px-3 py-2">{{ t('admin.codexTurnState.columns.time') }}</th>
                  <th class="px-3 py-2">{{ t('admin.codexTurnState.jobs.target') }}</th>
                  <th class="px-3 py-2">{{ t('admin.codexTurnState.columns.status') }}</th>
                  <th class="px-3 py-2 text-right">{{ t('admin.codexTurnState.columns.duration') }}</th>
                  <th class="px-3 py-2">{{ t('admin.codexTurnState.jobs.result') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-if="!recentJobs.length">
                  <td colspan="5" class="px-3 py-6 text-center text-gray-500 dark:text-gray-400">{{ t('admin.codexTurnState.jobs.empty') }}</td>
                </tr>
                <tr v-for="job in recentJobs" :key="job.id" data-test="cts-job-row">
                  <td class="whitespace-nowrap px-3 py-2 text-xs text-gray-500">{{ formatDateTime(new Date(job.queued_at * 1000)) }}</td>
                  <td class="px-3 py-2 text-xs">#{{ job.account_id }} <span class="font-mono">{{ job.model || t('admin.codexTurnState.jobs.allModels') }}</span></td>
                  <td class="px-3 py-2"><span :class="jobBadge(job)">{{ t(`admin.codexTurnState.jobs.status.${job.status}`) }}</span></td>
                  <td class="px-3 py-2 text-right text-xs">{{ job.finished_at && job.started_at ? formatSeconds(job.finished_at - job.started_at) : '-' }}</td>
                  <td class="px-3 py-2 text-xs">
                    <span v-if="job.error" class="text-red-600 dark:text-red-400">{{ job.error }}</span>
                    <span v-else>{{ t('admin.codexTurnState.jobs.updated', { count: job.updated }) }}</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <ProbeSourceStatsCard ref="statsCard" />

        <DegradationProbePanel :pinned-accounts="pinnedAccounts" />
      </template>
    </div>

    <AddMonitoredModelDialog :show="showAdd" :preset-account="addPreset" @close="showAdd = false" @saved="onAdded" />
    <ProxySourcesDialog :show="showProxies" @close="showProxies = false" @changed="loadState" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import AddMonitoredModelDialog from '@/components/admin/codexTurnState/AddMonitoredModelDialog.vue'
import DegradationProbePanel from '@/components/admin/codexTurnState/DegradationProbePanel.vue'
import ProbeSourceStatsCard from '@/components/admin/codexTurnState/ProbeSourceStatsCard.vue'
import ProxySourcesDialog from '@/components/admin/codexTurnState/ProxySourcesDialog.vue'
import { describeManagerError } from '@/components/admin/codexTurnState/managerError'
import type { PinnedAccount } from '@/components/admin/codexTurnState/types'
import {
  ACTIVE_JOB_STATUSES,
  addMonitoredModel,
  getDegraded,
  getHistory,
  getJob,
  getState,
  removeMonitoredModel,
  startProbe,
  updateSettings,
  type CodexDegradationVerdict,
  type CodexDegradedRow,
  type CodexProbeHistory,
  type CodexProbeJob,
  type CodexTurnStateModel,
  type CodexTurnStateSnapshot,
} from '@/api/admin/codexTurnState'
import { useAppStore } from '@/stores'
import { formatDateTime } from '@/utils/format'

type Tone = 'ok' | 'warn' | 'bad'
type StatusFilter = 'all' | 'healthy' | 'attention' | 'problem' | 'degraded'

interface LiveSlot extends CodexTurnStateModel {
  renewal: { tone: Tone; label: string }
  busy: boolean
  historyText: string
  historyTitle: string
}

const REFRESH_INTERVAL_MS = 20000
const JOB_POLL_INTERVAL_MS = 2000
const JOB_POLL_LIMIT_MS = 10 * 60 * 1000
const statusFilters: StatusFilter[] = ['all', 'healthy', 'attention', 'problem', 'degraded']

const { t } = useI18n()
const appStore = useAppStore()

const state = ref<CodexTurnStateSnapshot | null>(null)
const history = ref<CodexProbeHistory>({})
const fetchedAt = ref(0)
const now = ref(Date.now())
const loading = ref(false)
const loadError = ref('')
const autoRefresh = ref(true)
const savingSettings = ref(false)
const advanceInput = ref<number | null>(null)
const advanceDirty = ref(false)
const degradedLoading = ref(false)
const searchQuery = ref('')
const statusFilter = ref<StatusFilter>('all')
const showAdd = ref(false)
const addPreset = ref<{ id: number; name: string } | null>(null)
const showProxies = ref(false)
const statsCard = ref<InstanceType<typeof ProbeSourceStatsCard> | null>(null)
/** 页面发起、仍在轮询的探测任务：`<accountId>:<model>` → job id */
const pendingJobs = ref<Record<string, string>>({})

let refreshTimer: ReturnType<typeof setInterval> | undefined
let tickTimer: ReturnType<typeof setInterval> | undefined
const jobTimers = new Set<ReturnType<typeof setTimeout>>()
let loadSequence = 0
let unmounted = false

const slotKey = (accountId: number, model: string) => `${accountId}:${model}`

function formatSeconds(value: number) {
  const total = Math.max(0, Math.ceil(value))
  if (total < 60) return `${total}s`
  if (total < 3600) return `${Math.floor(total / 60)}m ${total % 60}s`
  return `${Math.floor(total / 3600)}h ${Math.floor((total % 3600) / 60)}m`
}

function formatTimestamp(value: string | null | undefined) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : formatDateTime(date)
}

// 与原管理面板一致：用快照后经过的本地时间推算倒计时，不依赖服务器与浏览器时钟一致。
function liveModel(model: CodexTurnStateModel, elapsed: number): CodexTurnStateModel {
  const base = Number.isFinite(model.remaining_seconds) ? model.remaining_seconds : Number(model.remaining_minutes) * 60
  const remaining = Math.max(0, base - elapsed)
  const cookieRemaining = Math.max(0, Math.ceil((model.cookie_remaining_seconds || 0) - elapsed))
  return {
    ...model,
    remaining_seconds: remaining,
    remaining_minutes: remaining / 60,
    cookie_remaining_seconds: cookieRemaining,
    cookie_valid: Boolean(model.cookie_present && cookieRemaining > 0),
    expired: model.expired || (model.valid && base - elapsed <= 0),
    backoff_seconds: Math.max(0, Math.ceil((model.backoff_seconds || 0) - elapsed)),
    next_probe_seconds: Math.max(0, Math.ceil((model.next_probe_seconds || 0) - elapsed)),
  }
}

function renewal(m: CodexTurnStateModel): { tone: Tone; label: string } {
  const r = (tone: Tone, key: string, params: Record<string, string> = {}) => ({ tone, label: t(`admin.codexTurnState.renewal.${key}`, params) })
  if (m.backoff_seconds > 0) return r('warn', 'backoff', { time: formatSeconds(m.backoff_seconds) })
  if (m.cookie_present) {
    if (!m.cookie_valid) return r('bad', 'cookieExpired')
    if (m.cookie_remaining_seconds <= 60) return r('warn', 'cookieRenewing')
    return r('ok', 'cookieOk')
  }
  if (m.expired) return r('bad', 'expired')
  if (!m.valid) return r('bad', 'waitingFirst')
  if (m.length_degraded) return r('warn', 'lengthMismatch')
  if (m.next_probe_seconds <= 0) return r('warn', 'awaitingRenewal')
  return r('ok', 'valid')
}

function historyCell(accountId: number, model: string) {
  const entries = history.value[slotKey(accountId, model)] ?? []
  const text = entries.slice(-10).map((entry) => (entry.outcome === 'saved' ? '✓' : entry.ok ? '◎' : '·')).join(' ') || '-'
  const last = entries[entries.length - 1]
  const title = last
    ? t('admin.codexTurnState.slots.historyTitle', { outcome: last.outcome, len: last.state_len, source: last.source })
    : ''
  return { text, title }
}

function slotBusy(accountId: number, model: string) {
  if (pendingJobs.value[slotKey(accountId, model)]) return true
  return (state.value?.jobs ?? []).some((job) => job.account_id === accountId
    && (!job.model || job.model === model)
    && ACTIVE_JOB_STATUSES.includes(job.status))
}

const liveAccounts = computed(() => {
  if (!state.value) return []
  const elapsed = Math.max(0, (now.value - fetchedAt.value) / 1000)
  return state.value.accounts.map((account) => ({
    ...account,
    slots: (account.error ? [] : account.models).map((model): LiveSlot => {
      const live = liveModel(model, elapsed)
      const cell = historyCell(account.id, model.model)
      return { ...live, renewal: renewal(live), busy: slotBusy(account.id, model.model), historyText: cell.text, historyTitle: cell.title }
    }),
  }))
})

const allSlots = computed(() => liveAccounts.value.flatMap((account) => account.slots))

function matchesStatus(slot: LiveSlot) {
  switch (statusFilter.value) {
    case 'healthy': return slot.renewal.tone === 'ok'
    case 'attention': return slot.renewal.tone === 'warn'
    case 'problem': return slot.renewal.tone === 'bad'
    case 'degraded': return slot.degradation === 'degraded'
    default: return true
  }
}

const filteredAccounts = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  return liveAccounts.value.flatMap((account) => {
    const accountMatch = !query || account.name.toLowerCase().includes(query) || String(account.id) === query.replace(/^#/, '')
    // 读取失败的账号没有槽位，只在“全部 / 异常”下按账号匹配展示。
    if (account.error) return accountMatch && (statusFilter.value === 'all' || statusFilter.value === 'problem') ? [account] : []
    const slots = account.slots.filter((slot) => matchesStatus(slot) && (accountMatch || slot.model.toLowerCase().includes(query)))
    return slots.length ? [{ ...account, slots }] : []
  })
})

const overviewCards = computed(() => {
  const slots = allSlots.value
  const count = (tone: Tone) => slots.filter((slot) => slot.renewal.tone === tone).length
  const failedAccounts = liveAccounts.value.filter((account) => account.error).length
  const degraded = slots.filter((slot) => slot.degradation === 'degraded').length
  const problems = count('bad') + failedAccounts
  return [
    { key: 'accounts', value: liveAccounts.value.length, icon: 'globe' as const, iconClass: 'bg-blue-50 text-blue-600 dark:bg-blue-900/30 dark:text-blue-400', valueClass: 'text-gray-900 dark:text-white' },
    { key: 'models', value: slots.length, icon: 'cube' as const, iconClass: 'bg-indigo-50 text-indigo-600 dark:bg-indigo-900/30 dark:text-indigo-400', valueClass: 'text-gray-900 dark:text-white' },
    { key: 'healthy', value: count('ok'), icon: 'checkCircle' as const, iconClass: 'bg-emerald-50 text-emerald-600 dark:bg-emerald-900/30 dark:text-emerald-400', valueClass: 'text-emerald-600 dark:text-emerald-400' },
    { key: 'attention', value: count('warn'), icon: 'clock' as const, iconClass: 'bg-amber-50 text-amber-600 dark:bg-amber-900/30 dark:text-amber-400', valueClass: 'text-amber-600 dark:text-amber-400' },
    { key: 'problem', value: problems, icon: 'exclamationTriangle' as const, iconClass: 'bg-rose-50 text-rose-600 dark:bg-rose-900/30 dark:text-rose-400', valueClass: problems ? 'text-rose-600 dark:text-rose-400' : 'text-gray-900 dark:text-white' },
    { key: 'degraded', value: state.value?.degraded_enabled ? degraded : '-', icon: 'bolt' as const, iconClass: 'bg-rose-50 text-rose-600 dark:bg-rose-900/30 dark:text-rose-400', valueClass: degraded ? 'text-rose-600 dark:text-rose-400' : 'text-gray-900 dark:text-white' },
    { key: 'proxies', value: `${state.value?.static_proxy_count ?? 0} / ${state.value?.dynamic_provider_count ?? 0}`, icon: 'server' as const, iconClass: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300', valueClass: 'text-gray-900 dark:text-white' },
  ]
})

const timingSummary = computed(() => {
  const timing = state.value?.renewal_timing
  if (!timing || !timing.samples || timing.average_seconds == null || !Number.isFinite(timing.average_seconds)) {
    return t('admin.codexTurnState.settings.timingEmpty')
  }
  return t('admin.codexTurnState.settings.timing', {
    average: formatSeconds(timing.average_seconds),
    samples: timing.samples,
    last: timing.last_seconds == null ? '-' : formatSeconds(timing.last_seconds),
  })
})

const recentJobs = computed<CodexProbeJob[]>(() => (state.value?.jobs ?? []).slice(0, 12))

const pinnedAccounts = computed<PinnedAccount[]>(() => liveAccounts.value
  .map((account) => ({
    id: account.id,
    name: account.name,
    models: account.slots.filter((slot) => slot.valid && !slot.expired && slot.remaining_seconds > 0).map((slot) => slot.model),
  }))
  .filter((account) => account.models.length > 0))

watch(() => state.value?.settings?.refresh_advance_minutes, (minutes) => {
  if (!advanceDirty.value && minutes != null) advanceInput.value = minutes
})

function toneBadge(tone: Tone) {
  return tone === 'ok' ? 'badge badge-success' : tone === 'warn' ? 'badge badge-warning' : 'badge badge-danger'
}

function verdictBadge(verdict: CodexDegradationVerdict) {
  if (verdict === 'degraded') return 'badge badge-danger'
  if (verdict === 'unknown') return 'badge badge-warning'
  return 'badge badge-gray'
}

function jobBadge(job: CodexProbeJob) {
  if (job.status === 'error') return 'badge badge-danger'
  if (job.status === 'done') return job.updated > 0 ? 'badge badge-success' : 'badge badge-gray'
  if (job.status === 'cancelled') return 'badge badge-gray'
  return 'badge badge-warning'
}

function isMonitored(accountId: number, model: string) {
  return !!state.value?.accounts.some((account) => account.id === accountId && account.models.some((item) => item.model === model))
}

async function loadState() {
  const sequence = ++loadSequence
  loading.value = true
  try {
    const snapshot = await getState()
    if (sequence !== loadSequence) return
    state.value = snapshot
    fetchedAt.value = Date.now()
    now.value = fetchedAt.value
    loadError.value = ''
  } catch (err) {
    if (sequence === loadSequence) loadError.value = describeManagerError(err, t, 'admin.codexTurnState.loadFailed')
  } finally {
    if (sequence === loadSequence) loading.value = false
  }
}

async function loadHistory() {
  try {
    history.value = await getHistory()
  } catch {
    // 历史只用于走势展示，失败时保留上一次结果。
  }
}

async function refreshAll() {
  await Promise.all([loadState(), loadHistory()])
  void statsCard.value?.load()
}

async function saveSettings(patch: Parameters<typeof updateSettings>[0], successKey: string) {
  savingSettings.value = true
  try {
    await updateSettings(patch)
    appStore.showSuccess(t(successKey))
    await loadState()
    return true
  } catch (err) {
    appStore.showError(describeManagerError(err, t, 'admin.codexTurnState.saveFailed'))
    return false
  } finally {
    savingSettings.value = false
  }
}

function setProbing(enabled: boolean) {
  if (savingSettings.value) return
  void saveSettings({ probing_enabled: enabled }, enabled ? 'admin.codexTurnState.settings.resumed' : 'admin.codexTurnState.settings.pausedToast')
}

async function saveAdvance() {
  const minutes = advanceInput.value
  if (typeof minutes !== 'number' || !Number.isInteger(minutes) || minutes < 1 || minutes > 30) {
    appStore.showError(t('admin.codexTurnState.settings.advanceInvalid'))
    return
  }
  if (await saveSettings({ refresh_advance_minutes: minutes }, 'admin.codexTurnState.settings.advanceSaved')) advanceDirty.value = false
}

function finishJob(key: string, job: CodexProbeJob | null) {
  const rest = { ...pendingJobs.value }
  delete rest[key]
  pendingJobs.value = rest
  if (job?.status === 'done') appStore.showSuccess(t('admin.codexTurnState.probeResult.done', { count: job.updated }))
  else if (job?.status === 'error') appStore.showError(t('admin.codexTurnState.probeResult.error', { error: job.error || '-' }))
  else if (job?.status === 'cancelled') appStore.showError(t('admin.codexTurnState.probeResult.cancelled'))
  void loadState()
  void loadHistory()
}

function pollJob(key: string, jobId: string, startedAt: number) {
  const timer = setTimeout(async () => {
    jobTimers.delete(timer)
    if (unmounted) return
    let job: CodexProbeJob | null = null
    try {
      job = await getJob(jobId)
    } catch {
      finishJob(key, null)
      return
    }
    if (unmounted) return
    if (ACTIVE_JOB_STATUSES.includes(job.status) && Date.now() - startedAt < JOB_POLL_LIMIT_MS) {
      pollJob(key, jobId, startedAt)
      return
    }
    finishJob(key, ACTIVE_JOB_STATUSES.includes(job.status) ? null : job)
  }, JOB_POLL_INTERVAL_MS)
  jobTimers.add(timer)
}

async function probeSlot(accountId: number, model: string) {
  const key = slotKey(accountId, model)
  if (pendingJobs.value[key]) return
  try {
    const jobId = await startProbe({ account_id: accountId, model, force: true })
    pendingJobs.value = { ...pendingJobs.value, [key]: jobId }
    appStore.showSuccess(t('admin.codexTurnState.probeResult.queued'))
    pollJob(key, jobId, Date.now())
  } catch (err) {
    appStore.showError(describeManagerError(err, t, 'admin.codexTurnState.probeResult.failed'))
  }
}

async function removeSlot(accountId: number, model: string) {
  if (!window.confirm(t('admin.codexTurnState.removeConfirm', { id: accountId, model }))) return
  try {
    await removeMonitoredModel(accountId, model)
    appStore.showSuccess(t('admin.codexTurnState.removed'))
    await loadState()
  } catch (err) {
    appStore.showError(describeManagerError(err, t, 'admin.codexTurnState.saveFailed'))
  }
}

async function addFromDegraded(row: CodexDegradedRow) {
  try {
    await addMonitoredModel({
      account_id: row.account_id,
      name: row.account_name?.trim() || `#${row.account_id}`,
      model: row.sent_model,
      target_state_len: 292,
    })
    appStore.showSuccess(t('admin.codexTurnState.addModel.saved'))
    await loadState()
  } catch (err) {
    appStore.showError(describeManagerError(err, t, 'admin.codexTurnState.saveFailed'))
  }
}

async function refreshDegraded() {
  degradedLoading.value = true
  try {
    const result = await getDegraded(true)
    if (state.value) state.value = { ...state.value, degraded: result.degraded ?? [], degraded_error: result.error ?? '' }
  } catch (err) {
    appStore.showError(describeManagerError(err, t, 'admin.codexTurnState.loadFailed'))
  } finally {
    degradedLoading.value = false
  }
}

function openAdd(preset: { id: number; name: string } | null) {
  addPreset.value = preset
  showAdd.value = true
}

function onAdded() {
  showAdd.value = false
  void loadState()
}

function startRefreshTimer() {
  clearInterval(refreshTimer)
  refreshTimer = autoRefresh.value
    ? setInterval(() => { if (!document.hidden) void loadState() }, REFRESH_INTERVAL_MS)
    : undefined
}

function onVisibilityChange() {
  if (!document.hidden && autoRefresh.value) void loadState()
}

watch(autoRefresh, startRefreshTimer)

onMounted(() => {
  void loadState()
  void loadHistory()
  startRefreshTimer()
  tickTimer = setInterval(() => { if (!document.hidden) now.value = Date.now() }, 1000)
  document.addEventListener('visibilitychange', onVisibilityChange)
})

onUnmounted(() => {
  unmounted = true
  clearInterval(refreshTimer)
  clearInterval(tickTimer)
  jobTimers.forEach((timer) => clearTimeout(timer))
  jobTimers.clear()
  document.removeEventListener('visibilitychange', onVisibilityChange)
})
</script>
