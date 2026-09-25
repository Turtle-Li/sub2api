<template>
  <AppLayout>
    <div class="space-y-4">
      <div v-if="loadError" role="alert" class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200">
        <span>{{ loadError }}</span>
        <button type="button" class="btn btn-secondary" @click="load">{{ t('common.refresh') }}</button>
      </div>

      <section class="card p-5">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="space-y-1">
            <div class="flex items-center gap-3">
              <Toggle data-test="bps-global-toggle" :model-value="config.enabled" @update:model-value="setEnabled" />
              <span class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.bpsUpstream.globalSwitch') }}</span>
              <span :class="config.enabled ? 'badge badge-success' : 'badge badge-gray'">
                {{ config.enabled ? t('admin.bpsUpstream.enabled') : t('admin.bpsUpstream.disabled') }}
              </span>
            </div>
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.bpsUpstream.globalHint') }}</p>
            <p v-if="policy" class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.bpsUpstream.policy', { models: policy.models.join(' / '), threshold: policy.breaker_threshold, minutes: Math.round(policy.breaker_open_seconds / 60), statuses: policy.immediate_breaker_status.join('/') }) }}
            </p>
          </div>
          <div class="flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span v-if="startedAt">{{ t('admin.bpsUpstream.monitorSince', { time: formatDateTime(startedAt) }) }}</span>
            <label class="flex items-center gap-1">
              <input v-model="autoRefresh" type="checkbox" data-test="bps-auto-refresh" />
              {{ t('admin.bpsUpstream.autoRefresh') }}
            </label>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="load">{{ t('common.refresh') }}</button>
          </div>
        </div>

        <div class="mt-4 grid grid-cols-2 gap-3 md:grid-cols-5">
          <div v-for="item in totals" :key="item.key" class="rounded-lg bg-gray-50 p-3 dark:bg-dark-700">
            <div class="text-xs text-gray-500 dark:text-gray-400">{{ t(`admin.bpsUpstream.totals.${item.key}`) }}</div>
            <div class="mt-1 text-xl font-semibold" :class="item.tone" :data-test="`bps-total-${item.key}`">{{ item.value }}</div>
          </div>
        </div>
        <p class="mt-2 text-xs text-gray-400 dark:text-gray-500">{{ t('admin.bpsUpstream.monitorHint') }}</p>
      </section>

      <section class="card p-5">
        <div class="mb-3 flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.bpsUpstream.accounts') }}</h2>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.bpsUpstream.accountsHint') }}</p>
          </div>
          <div class="flex w-full items-end gap-2 md:w-auto">
            <div class="min-w-[260px] flex-1">
              <Select
                v-model="pickerValue"
                :options="pickerOptions"
                :placeholder="t('admin.bpsUpstream.pickerPlaceholder')"
                remote
                :loading="pickerLoading"
                data-test="bps-account-picker"
                @search="searchAccounts"
              />
            </div>
            <button type="button" class="btn btn-primary" data-test="bps-add-account" :disabled="!pickerValue || saving" @click="addAccount">
              {{ t('admin.bpsUpstream.add') }}
            </button>
          </div>
        </div>

        <div class="overflow-x-auto">
          <table class="w-full text-left text-sm">
            <thead class="text-xs uppercase text-gray-500 dark:text-gray-400">
              <tr>
                <th class="px-3 py-2">{{ t('admin.bpsUpstream.columns.account') }}</th>
                <th class="px-3 py-2">{{ t('admin.bpsUpstream.columns.status') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.bpsUpstream.columns.success') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.bpsUpstream.columns.fallback') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.bpsUpstream.columns.afterOutput') }}</th>
                <th class="px-3 py-2">{{ t('admin.bpsUpstream.columns.skipped') }}</th>
                <th class="px-3 py-2">{{ t('admin.bpsUpstream.columns.breaker') }}</th>
                <th class="px-3 py-2">{{ t('admin.bpsUpstream.columns.lastFailure') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.bpsUpstream.columns.actions') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-if="!accounts.length">
                <td colspan="9" class="px-3 py-6 text-center text-gray-500 dark:text-gray-400">{{ t('admin.bpsUpstream.noAccounts') }}</td>
              </tr>
              <tr v-for="account in accounts" :key="account.id" data-test="bps-account-row">
                <td class="px-3 py-2">
                  <div class="font-medium text-gray-900 dark:text-white">{{ account.missing ? t('admin.bpsUpstream.missing') : account.name }}</div>
                  <div class="text-xs text-gray-500">#{{ account.id }}<span v-if="!account.missing"> · {{ account.type }}</span></div>
                </td>
                <td class="px-3 py-2">
                  <span v-if="account.missing" class="badge badge-danger">{{ t('admin.bpsUpstream.missing') }}</span>
                  <span v-else-if="!account.eligible" class="badge badge-warning">{{ t('admin.bpsUpstream.ineligible') }}</span>
                  <span v-else :class="account.status === 'active' && account.schedulable ? 'badge badge-success' : 'badge badge-gray'">
                    {{ account.status }}{{ account.schedulable ? '' : ` · ${t('admin.bpsUpstream.unschedulable')}` }}
                  </span>
                </td>
                <td class="px-3 py-2 text-right text-emerald-600 dark:text-emerald-400">{{ account.stats?.successes ?? 0 }}</td>
                <td class="px-3 py-2 text-right text-amber-600 dark:text-amber-400">{{ account.stats?.fallbacks ?? 0 }}</td>
                <td class="px-3 py-2 text-right text-red-600 dark:text-red-400">{{ account.stats?.errors_after_output ?? 0 }}</td>
                <td class="px-3 py-2 text-xs text-gray-600 dark:text-gray-300">{{ formatSkips(account.stats?.skipped) }}</td>
                <td class="px-3 py-2 text-xs">
                  <span v-if="breakerOpen(account.stats)" class="badge badge-danger" :title="formatDateTime(account.stats?.breaker_open_until)">
                    {{ t('admin.bpsUpstream.breakerOpen', { time: formatDateTime(account.stats?.breaker_open_until) }) }}
                  </span>
                  <span v-else class="text-gray-500">{{ t('admin.bpsUpstream.breakerClosed', { failures: account.stats?.breaker_failures ?? 0 }) }}</span>
                </td>
                <td class="max-w-[280px] px-3 py-2 text-xs text-gray-600 dark:text-gray-300">
                  <template v-if="account.stats?.last_failure_at">
                    <div>{{ formatDateTime(account.stats.last_failure_at) }}</div>
                    <div class="truncate" :title="account.stats.last_failure_reason">{{ account.stats.last_failure_reason }}</div>
                  </template>
                  <span v-else>-</span>
                </td>
                <td class="whitespace-nowrap px-3 py-2 text-right">
                  <button v-if="breakerOpen(account.stats)" type="button" class="btn btn-secondary btn-sm mr-2" @click="reset(account.id)">
                    {{ t('admin.bpsUpstream.resetBreaker') }}
                  </button>
                  <button type="button" class="btn btn-danger btn-sm" data-test="bps-remove-account" :disabled="saving" @click="removeAccount(account.id)">
                    {{ t('admin.bpsUpstream.remove') }}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <p v-if="unlisted.length" class="mt-3 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.bpsUpstream.unlisted', { ids: unlisted.map((item) => `#${item.account_id}`).join(', ') }) }}
        </p>
      </section>

      <section class="card p-5">
        <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.bpsUpstream.events') }}</h2>
          <div class="flex gap-1">
            <button
              v-for="filter in outcomeFilters"
              :key="filter"
              type="button"
              class="btn btn-sm"
              :class="outcomeFilter === filter ? 'btn-primary' : 'btn-secondary'"
              @click="outcomeFilter = filter"
            >
              {{ t(`admin.bpsUpstream.outcomes.${filter}`) }}
            </button>
          </div>
        </div>
        <div class="overflow-x-auto">
          <table class="w-full text-left text-sm">
            <thead class="text-xs uppercase text-gray-500 dark:text-gray-400">
              <tr>
                <th class="px-3 py-2">{{ t('admin.bpsUpstream.columns.time') }}</th>
                <th class="px-3 py-2">{{ t('admin.bpsUpstream.columns.account') }}</th>
                <th class="px-3 py-2">{{ t('admin.bpsUpstream.columns.model') }}</th>
                <th class="px-3 py-2">{{ t('admin.bpsUpstream.columns.outcome') }}</th>
                <th class="px-3 py-2">{{ t('admin.bpsUpstream.columns.effort') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.bpsUpstream.columns.duration') }}</th>
                <th class="px-3 py-2">{{ t('admin.bpsUpstream.columns.reason') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-if="!filteredEvents.length">
                <td colspan="7" class="px-3 py-6 text-center text-gray-500 dark:text-gray-400">{{ t('admin.bpsUpstream.noEvents') }}</td>
              </tr>
              <tr v-for="(event, index) in filteredEvents" :key="`${event.time}-${index}`" data-test="bps-event-row">
                <td class="whitespace-nowrap px-3 py-2 text-xs">{{ formatDateTime(event.time) }}</td>
                <td class="px-3 py-2 text-xs">{{ accountLabel(event.account_id) }}</td>
                <td class="px-3 py-2 text-xs">{{ event.model || '-' }}</td>
                <td class="px-3 py-2"><span :class="outcomeClass(event.outcome)">{{ t(`admin.bpsUpstream.outcomes.${event.outcome}`) }}</span></td>
                <td class="px-3 py-2 text-xs">
                  <template v-if="event.requested_effort || event.applied_effort">
                    {{ event.requested_effort || '-' }}<span v-if="event.applied_effort && event.applied_effort !== event.requested_effort"> → {{ event.applied_effort }}</span>
                  </template>
                  <span v-else>-</span>
                </td>
                <td class="px-3 py-2 text-right text-xs">{{ event.duration_ms ? `${event.duration_ms} ms` : '-' }}</td>
                <td class="max-w-[360px] px-3 py-2 text-xs text-gray-600 dark:text-gray-300">
                  <span v-if="event.reason">{{ reasonLabel(event.reason) }}</span>
                  <span v-if="event.status_code"> · HTTP {{ event.status_code }}</span>
                  <div v-if="event.detail" class="truncate text-gray-400" :title="event.detail">{{ event.detail }}</div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import { list as listAccounts } from '@/api/admin/accounts'
import {
  getOverview,
  resetBreaker,
  updateConfig,
  type BPSAccountStats,
  type BPSEvent,
  type BPSOutcome,
  type BPSUpstreamAccount,
  type BPSUpstreamConfig,
  type BPSUpstreamPolicy,
} from '@/api/admin/bpsUpstream'
import { useAppStore } from '@/stores'
import { formatDateTime } from '@/utils/format'

const REFRESH_INTERVAL_MS = 10000
const eligibleAccountTypes = ['oauth', 'setup-token'] as const
const outcomeFilters = ['all', 'success', 'fallback', 'error_after_output', 'skipped'] as const
type OutcomeFilter = (typeof outcomeFilters)[number]

const { t, te } = useI18n()
const appStore = useAppStore()

const config = ref<BPSUpstreamConfig>({ enabled: false, account_ids: [] })
const policy = ref<BPSUpstreamPolicy | null>(null)
const accounts = ref<BPSUpstreamAccount[]>([])
const unlisted = ref<BPSAccountStats[]>([])
const events = ref<BPSEvent[]>([])
const startedAt = ref('')
const now = ref(Date.now())
const loading = ref(false)
const saving = ref(false)
const loadError = ref('')
const autoRefresh = ref(true)
const outcomeFilter = ref<OutcomeFilter>('all')
let refreshTimer: ReturnType<typeof setInterval> | undefined
let loadSequence = 0

const pickerValue = ref<string | null>(null)
const pickerAccounts = ref<{ id: number; name: string }[]>([])
const pickerLoading = ref(false)
let pickerAbort: AbortController | null = null

const pickerOptions = computed(() => {
  const listed = new Set(config.value.account_ids)
  return pickerAccounts.value
    .filter((account) => !listed.has(account.id))
    .map((account) => ({ value: String(account.id), label: `${account.name} (#${account.id})` }))
})

const allStats = computed(() => [
  ...accounts.value.map((account) => account.stats).filter((stats): stats is BPSAccountStats => !!stats),
  ...unlisted.value,
])

const totals = computed(() => {
  const sum = (pick: (stats: BPSAccountStats) => number) => allStats.value.reduce((acc, stats) => acc + pick(stats), 0)
  const skipped = sum((stats) => Object.values(stats.skipped ?? {}).reduce((acc, value) => acc + value, 0))
  const breakers = accounts.value.filter((account) => breakerOpen(account.stats)).length
  return [
    { key: 'success', value: sum((stats) => stats.successes), tone: 'text-emerald-600 dark:text-emerald-400' },
    { key: 'fallback', value: sum((stats) => stats.fallbacks), tone: 'text-amber-600 dark:text-amber-400' },
    { key: 'afterOutput', value: sum((stats) => stats.errors_after_output), tone: 'text-red-600 dark:text-red-400' },
    { key: 'skipped', value: skipped, tone: 'text-gray-700 dark:text-gray-200' },
    { key: 'breakers', value: breakers, tone: breakers ? 'text-red-600 dark:text-red-400' : 'text-gray-700 dark:text-gray-200' },
  ]
})

const filteredEvents = computed(() =>
  outcomeFilter.value === 'all' ? events.value : events.value.filter((event) => event.outcome === outcomeFilter.value),
)

const accountNames = computed(() => new Map(accounts.value.filter((a) => !a.missing).map((a) => [a.id, a.name])))

function accountLabel(id: number) {
  const name = accountNames.value.get(id)
  return name ? `${name} (#${id})` : `#${id}`
}

function breakerOpen(stats?: BPSAccountStats) {
  return !!stats?.breaker_open_until && new Date(stats.breaker_open_until).getTime() > now.value
}

function reasonLabel(reason: string) {
  const key = `admin.bpsUpstream.reasons.${reason}`
  return te(key) ? t(key) : reason
}

function formatSkips(skipped?: Record<string, number>) {
  const entries = Object.entries(skipped ?? {}).filter(([, count]) => count > 0)
  if (!entries.length) return '-'
  return entries.sort((a, b) => b[1] - a[1]).map(([reason, count]) => `${reasonLabel(reason)} ${count}`).join(' · ')
}

function outcomeClass(outcome: BPSOutcome) {
  switch (outcome) {
    case 'success': return 'badge badge-success'
    case 'fallback': return 'badge badge-warning'
    case 'error_after_output': return 'badge badge-danger'
    default: return 'badge badge-gray'
  }
}

function errorMessage(err: unknown, fallback: string) {
  const message = (err as { message?: string } | null)?.message
  return message || fallback
}

async function load() {
  const sequence = ++loadSequence
  loading.value = true
  try {
    const overview = await getOverview()
    if (sequence !== loadSequence) return
    config.value = overview.config
    policy.value = overview.policy
    accounts.value = overview.accounts ?? []
    unlisted.value = overview.unlisted_stats ?? []
    events.value = overview.events ?? []
    startedAt.value = overview.monitor_started_at
    now.value = overview.now ? new Date(overview.now).getTime() : Date.now()
    loadError.value = ''
  } catch (err) {
    if (sequence === loadSequence) loadError.value = errorMessage(err, t('admin.bpsUpstream.loadFailed'))
  } finally {
    if (sequence === loadSequence) loading.value = false
  }
}

async function save(next: BPSUpstreamConfig) {
  saving.value = true
  try {
    config.value = await updateConfig(next)
    appStore.showSuccess(t('admin.bpsUpstream.saved'))
    await load()
    return true
  } catch (err) {
    appStore.showError(errorMessage(err, t('admin.bpsUpstream.saveFailed')))
    return false
  } finally {
    saving.value = false
  }
}

function setEnabled(enabled: boolean) {
  void save({ ...config.value, enabled })
}

async function addAccount() {
  const id = Number(pickerValue.value)
  if (!Number.isInteger(id) || id <= 0) return
  if (await save({ ...config.value, account_ids: [...config.value.account_ids, id] })) pickerValue.value = null
}

function removeAccount(id: number) {
  if (!window.confirm(t('admin.bpsUpstream.removeConfirm', { id }))) return
  void save({ ...config.value, account_ids: config.value.account_ids.filter((item) => item !== id) })
}

async function reset(id: number) {
  try {
    await resetBreaker(id)
    appStore.showSuccess(t('admin.bpsUpstream.breakerReset'))
    await load()
  } catch (err) {
    appStore.showError(errorMessage(err, t('admin.bpsUpstream.saveFailed')))
  }
}

async function searchAccounts(search = '') {
  pickerAbort?.abort()
  const controller = new AbortController()
  pickerAbort = controller
  pickerLoading.value = true
  try {
    const results = await Promise.all(eligibleAccountTypes.map((type) => listAccounts(
      1,
      50,
      { platform: 'openai', type, lite: 'true', ...(search ? { search } : {}) },
      { signal: controller.signal },
    )))
    if (controller.signal.aborted) return
    const unique = new Map<number, { id: number; name: string }>()
    for (const result of results) {
      for (const account of result.items) unique.set(account.id, { id: account.id, name: account.name })
    }
    pickerAccounts.value = [...unique.values()]
  } catch {
    if (!controller.signal.aborted) pickerAccounts.value = []
  } finally {
    if (pickerAbort === controller) pickerLoading.value = false
  }
}

function startTimer() {
  clearInterval(refreshTimer)
  refreshTimer = autoRefresh.value ? setInterval(() => { if (!document.hidden) void load() }, REFRESH_INTERVAL_MS) : undefined
}

watch(autoRefresh, startTimer)

onMounted(() => {
  void load()
  void searchAccounts()
  startTimer()
})

onUnmounted(() => {
  clearInterval(refreshTimer)
  pickerAbort?.abort()
})
</script>
