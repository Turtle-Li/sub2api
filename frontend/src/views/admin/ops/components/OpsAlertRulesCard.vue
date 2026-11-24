<script setup lang="ts">
import { computed, onMounted, reactive, ref, useId } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import OpsAlertAccountPicker from './OpsAlertAccountPicker.vue'
import { adminAPI } from '@/api'
import { opsAPI } from '@/api/admin/ops'
import type { AlertRule, MetricType, Operator } from '../types'
import type { CreateOpsAlertRuleRequest, OpsAlertRuleFilters, OpsSeverity } from '@/api/admin/ops'
import { formatDateTime } from '../utils/opsFormatters'

const { t } = useI18n()
const appStore = useAppStore()

// 与 DataTable 一致：< 768px 切换为卡片视图，避免宽表在移动端被截断。
const isDesktopViewport = useMediaQuery('(min-width: 768px)')

const loading = ref(false)
const rules = ref<AlertRule[]>([])

async function load() {
  loading.value = true
  try {
    rules.value = await opsAPI.listAlertRules()
  } catch (err: any) {
    console.error('[OpsAlertRulesCard] Failed to load rules', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.alertRules.loadFailed'))
    rules.value = []
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  load()
  loadGroups()
  loadBandwidthAlertAvailability()
})

const sortedRules = computed(() => {
  return [...rules.value].sort((a, b) => (b.id || 0) - (a.id || 0))
})

const showEditor = ref(false)
const saving = ref(false)
const editingId = ref<number | null>(null)
const draft = ref<AlertRule | null>(null)
const formId = useId()
const filterText = reactive({ status: '', codes: '', keywords: '', emails: '' })

type MetricGroup = 'system' | 'group' | 'account'

const BANDWIDTH_UTILIZATION_METRIC: MetricType = 'bandwidth_utilization'
const LEGACY_BANDWIDTH_UTILIZATION_METRIC: MetricType = 'network_bandwidth_utilization_percent'
const GOODPUT_VIDEO_INCIDENT_METRIC: MetricType = 'goodput_video_incident'
const ACCOUNT_UPSTREAM_ERROR_METRIC: MetricType = 'account_upstream_error'
const isGoodPutIncident = computed(() => draft.value?.metric_type === GOODPUT_VIDEO_INCIDENT_METRIC)
const isAccountUpstreamError = computed(() => draft.value?.metric_type === ACCOUNT_UPSTREAM_ERROR_METRIC)
const usesExtendedWindow = computed(() => isGoodPutIncident.value || isAccountUpstreamError.value)

const accountMetricTypes = new Set<MetricType>([
  ACCOUNT_UPSTREAM_ERROR_METRIC, 'account_rate_limited_count', 'account_error_count',
  'account_error_ratio', 'account_temp_unscheduled_count', 'overload_account_count'
])
const usesAccountScope = computed(() => !!draft.value && accountMetricTypes.has(draft.value.metric_type))
const draftAccountIds = computed<number[]>({
  get: () => draft.value?.filters?.account_ids ?? [],
  set: (accountIds) => {
    if (draft.value) draft.value.filters = { ...draft.value.filters, account_ids: accountIds }
  }
})

interface MetricDefinition {
  type: MetricType
  group: MetricGroup
  label: string
  description: string
  recommendedOperator: Operator
  recommendedThreshold: number
  unit?: string
  disabled?: boolean
  hidden?: boolean
}

const groupMetricTypes = new Set<MetricType>([
  'group_available_accounts',
  'group_available_ratio',
  'group_rate_limit_ratio'
])

function isBandwidthUtilizationMetric(metricType: unknown): boolean {
  return metricType === BANDWIDTH_UTILIZATION_METRIC || metricType === LEGACY_BANDWIDTH_UTILIZATION_METRIC
}

function parsePositiveInt(value: unknown): number | null {
  if (value == null) return null
  if (typeof value === 'boolean') return null
  const n = Number(value)
  return Number.isSafeInteger(n) && n > 0 ? n : null
}

const groupOptionsBase = ref<SelectOption[]>([])
const bandwidthLimitConfigured = ref(false)

function hasConfiguredBandwidthLimit(settings: Awaited<ReturnType<typeof opsAPI.getNetworkBandwidthSettings>> | null): boolean {
  if (!settings) return false
  const rx = settings.rx_limit_mbps
  const tx = settings.tx_limit_mbps
  return (typeof rx === 'number' && Number.isFinite(rx) && rx > 0) || (typeof tx === 'number' && Number.isFinite(tx) && tx > 0)
}

async function loadBandwidthAlertAvailability() {
  try {
    const settings = await opsAPI.getNetworkBandwidthSettings()
    bandwidthLimitConfigured.value = hasConfiguredBandwidthLimit(settings)
  } catch (err) {
    console.error('[OpsAlertRulesCard] Failed to load bandwidth settings', err)
    bandwidthLimitConfigured.value = false
  }
}

async function loadGroups() {
  try {
    const list = await adminAPI.groups.getAll()
    groupOptionsBase.value = list.map((g) => ({ value: g.id, label: g.name }))
  } catch (err) {
    console.error('[OpsAlertRulesCard] Failed to load groups', err)
    groupOptionsBase.value = []
  }
}

const isGroupMetricSelected = computed(() => {
  const metricType = draft.value?.metric_type
  return metricType ? groupMetricTypes.has(metricType) : false
})

const draftGroupId = computed<number | null>({
  get() {
    return parsePositiveInt(draft.value?.filters?.group_id)
  },
  set(value) {
    if (!draft.value) return
    if (value == null) {
      if (!draft.value.filters) return
      delete draft.value.filters.group_id
      if (Object.keys(draft.value.filters).length === 0) {
        delete draft.value.filters
      }
      return
    }
    if (!draft.value.filters) draft.value.filters = {}
    draft.value.filters.group_id = value
  }
})

const groupOptions = computed<SelectOption[]>(() => {
  if (isGroupMetricSelected.value) return groupOptionsBase.value
  return [{ value: null, label: t('admin.ops.alertRules.form.allGroups') }, ...groupOptionsBase.value]
})

const metricDefinitions = computed(() => {
  return [
    // System-level metrics
    {
      type: 'success_rate',
      group: 'system',
      label: t('admin.ops.alertRules.metrics.successRate'),
      description: t('admin.ops.alertRules.metricDescriptions.successRate'),
      recommendedOperator: '<',
      recommendedThreshold: 99,
      unit: '%'
    },
    {
      type: 'error_rate',
      group: 'system',
      label: t('admin.ops.alertRules.metrics.errorRate'),
      description: t('admin.ops.alertRules.metricDescriptions.errorRate'),
      recommendedOperator: '>',
      recommendedThreshold: 1,
      unit: '%'
    },
    {
      type: 'upstream_error_rate',
      group: 'system',
      label: t('admin.ops.alertRules.metrics.upstreamErrorRate'),
      description: t('admin.ops.alertRules.metricDescriptions.upstreamErrorRate'),
      recommendedOperator: '>',
      recommendedThreshold: 1,
      unit: '%'
    },
    {
      type: 'cpu_usage_percent',
      group: 'system',
      label: t('admin.ops.alertRules.metrics.cpu'),
      description: t('admin.ops.alertRules.metricDescriptions.cpu'),
      recommendedOperator: '>',
      recommendedThreshold: 80,
      unit: '%'
    },
    {
      type: 'memory_usage_percent',
      group: 'system',
      label: t('admin.ops.alertRules.metrics.memory'),
      description: t('admin.ops.alertRules.metricDescriptions.memory'),
      recommendedOperator: '>',
      recommendedThreshold: 80,
      unit: '%'
    },
    {
      type: BANDWIDTH_UTILIZATION_METRIC,
      group: 'system',
      label: t('admin.ops.alertRules.metrics.networkBandwidthUtilization'),
      description: t('admin.ops.alertRules.metricDescriptions.networkBandwidthUtilization'),
      recommendedOperator: '>',
      recommendedThreshold: 90,
      unit: '%',
      disabled: !bandwidthLimitConfigured.value,
      hidden: !bandwidthLimitConfigured.value && !isBandwidthUtilizationMetric(draft.value?.metric_type)
    },
    {
      type: 'concurrency_queue_depth',
      group: 'system',
      label: t('admin.ops.alertRules.metrics.queueDepth'),
      description: t('admin.ops.alertRules.metricDescriptions.queueDepth'),
      recommendedOperator: '>',
      recommendedThreshold: 10
    },
    {
      type: GOODPUT_VIDEO_INCIDENT_METRIC,
      group: 'system',
      label: t('admin.ops.alertRules.metrics.goodPutVideoIncident'),
      description: t('admin.ops.alertRules.metricDescriptions.goodPutVideoIncident'),
      recommendedOperator: '>',
      recommendedThreshold: 0
    },

    // Group-level metrics (requires group_id filter)
    {
      type: 'group_available_accounts',
      group: 'group',
      label: t('admin.ops.alertRules.metrics.groupAvailableAccounts'),
      description: t('admin.ops.alertRules.metricDescriptions.groupAvailableAccounts'),
      recommendedOperator: '<',
      recommendedThreshold: 1
    },
    {
      type: 'group_available_ratio',
      group: 'group',
      label: t('admin.ops.alertRules.metrics.groupAvailableRatio'),
      description: t('admin.ops.alertRules.metricDescriptions.groupAvailableRatio'),
      recommendedOperator: '<',
      recommendedThreshold: 50,
      unit: '%'
    },
    {
      type: 'group_rate_limit_ratio',
      group: 'group',
      label: t('admin.ops.alertRules.metrics.groupRateLimitRatio'),
      description: t('admin.ops.alertRules.metricDescriptions.groupRateLimitRatio'),
      recommendedOperator: '>',
      recommendedThreshold: 10,
      unit: '%'
    },

    // Account-level metrics
    {
      type: ACCOUNT_UPSTREAM_ERROR_METRIC,
      group: 'account',
      label: t('admin.ops.alertRules.metrics.accountUpstreamError'),
      description: t('admin.ops.alertRules.metricDescriptions.accountUpstreamError'),
      recommendedOperator: '>',
      recommendedThreshold: 0
    },
    {
      type: 'account_rate_limited_count',
      group: 'account',
      label: t('admin.ops.alertRules.metrics.accountRateLimitedCount'),
      description: t('admin.ops.alertRules.metricDescriptions.accountRateLimitedCount'),
      recommendedOperator: '>',
      recommendedThreshold: 0
    },
    {
      type: 'account_error_count',
      group: 'account',
      label: t('admin.ops.alertRules.metrics.accountErrorCount'),
      description: t('admin.ops.alertRules.metricDescriptions.accountErrorCount'),
      recommendedOperator: '>',
      recommendedThreshold: 0
    },
    {
      type: 'account_error_ratio',
      group: 'account',
      label: t('admin.ops.alertRules.metrics.accountErrorRatio'),
      description: t('admin.ops.alertRules.metricDescriptions.accountErrorRatio'),
      recommendedOperator: '>',
      recommendedThreshold: 5,
      unit: '%'
    },
    {
      type: 'account_temp_unscheduled_count',
      group: 'account',
      label: t('admin.ops.alertRules.metrics.accountTempUnscheduledCount'),
      description: t('admin.ops.alertRules.metricDescriptions.accountTempUnscheduledCount'),
      recommendedOperator: '>',
      recommendedThreshold: 0
    },
    {
      type: 'overload_account_count',
      group: 'account',
      label: t('admin.ops.alertRules.metrics.overloadAccountCount'),
      description: t('admin.ops.alertRules.metricDescriptions.overloadAccountCount'),
      recommendedOperator: '>',
      recommendedThreshold: 0
    }
  ] satisfies MetricDefinition[]
})

const selectedMetricDefinition = computed(() => {
  const metricType = draft.value?.metric_type
  if (!metricType) return null
  if (isBandwidthUtilizationMetric(metricType)) {
    return metricDefinitions.value.find((m) => m.type === BANDWIDTH_UTILIZATION_METRIC) ?? null
  }
  return metricDefinitions.value.find((m) => m.type === metricType) ?? null
})

function changeMetric(value: string | number | boolean | null) {
  const rule = draft.value
  const definition = metricDefinitions.value.find((m) => m.type === value && !m.disabled)
  if (!rule || !definition || rule.metric_type === definition.type) return
  rule.metric_type = definition.type
  rule.operator = definition.recommendedOperator
  rule.threshold = definition.recommendedThreshold
  const filters = { ...rule.filters }
  if (!accountMetricTypes.has(definition.type)) delete filters.account_ids
  if (definition.type !== ACCOUNT_UPSTREAM_ERROR_METRIC) {
    delete filters.upstream_status_codes
    delete filters.provider_codes
    delete filters.error_keywords
    Object.assign(filterText, { status: '', codes: '', keywords: '' })
  }
  if (definition.type === ACCOUNT_UPSTREAM_ERROR_METRIC || (!usesExtendedWindow.value && rule.window_minutes === 15)) {
    rule.window_minutes = 5
  }
  rule.filters = filters
}

function formatMetricType(metricType: string): string {
  return isBandwidthUtilizationMetric(metricType) ? BANDWIDTH_UTILIZATION_METRIC : metricType
}

const metricOptions = computed(() => {
  const buildGroup = (group: MetricGroup): SelectOption[] => {
    const items = metricDefinitions.value.filter((m) => m.group === group && !m.hidden)
    if (items.length === 0) return []
    const headerValue = `__group__${group}`
    return [
      {
        value: headerValue,
        label: t(`admin.ops.alertRules.metricGroups.${group}`),
        disabled: true,
        kind: 'group'
      },
      ...items.map((m) => ({ value: m.type, label: m.label, disabled: m.disabled }))
    ]
  }

  return [...buildGroup('system'), ...buildGroup('group'), ...buildGroup('account')]
})

const operatorOptions = computed(() => {
  const ops: Operator[] = ['>', '>=', '<', '<=', '==', '!=']
  return ops.map((o) => ({ value: o, label: o }))
})

const severityOptions = computed(() => {
  const sev: OpsSeverity[] = ['P0', 'P1', 'P2', 'P3']
  return sev.map((s) => ({ value: s, label: s }))
})

const windowOptions = computed(() => {
  const windows = usesExtendedWindow.value ? [1, 5, 15, 60] : [1, 5, 60]
  return windows.map((m) => ({ value: m, label: `${m}m` }))
})

function newRuleDraft(): AlertRule {
  return {
    name: '',
    description: '',
    enabled: true,
    metric_type: 'error_rate',
    operator: '>',
    threshold: 1,
    window_minutes: 1,
    sustained_minutes: 2,
    severity: 'P1',
    cooldown_minutes: 10,
    notify_email: true,
    notify_feishu: false,
    notify_emails: [],
    custom_message: ''
  }
}

function initializeEditor(rule: AlertRule) {
  draft.value = { ...rule, filters: { ...rule.filters }, custom_message: rule.custom_message ?? '' }
  filterText.status = rule.filters?.upstream_status_codes?.join(', ') ?? ''
  filterText.codes = rule.filters?.provider_codes?.join(', ') ?? ''
  filterText.keywords = rule.filters?.error_keywords?.join('\n') ?? ''
  filterText.emails = rule.notify_emails?.join('\n') ?? ''
}

async function openCreate() {
  await loadBandwidthAlertAvailability()
  editingId.value = null
  initializeEditor(newRuleDraft())
  showEditor.value = true
}

async function openEdit(rule: AlertRule) {
  await loadBandwidthAlertAvailability()
  editingId.value = rule.id ?? null
  initializeEditor(rule)
  if (draft.value && draft.value.metric_type === LEGACY_BANDWIDTH_UTILIZATION_METRIC) {
    draft.value.metric_type = BANDWIDTH_UTILIZATION_METRIC
  }
  showEditor.value = true
}

function splitTokens(input: string, separator: RegExp): string[] {
  return input.split(separator).map((item) => item.trim()).filter(Boolean)
}

const parsedFilterText = computed(() => ({
  status: splitTokens(filterText.status, /[\s,，]+/),
  codes: splitTokens(filterText.codes, /[\s,，]+/),
  keywords: splitTokens(filterText.keywords, /\r?\n/),
  emails: splitTokens(filterText.emails, /[\r\n,，]+/)
}))

function buildRuleRequest(rule: AlertRule): CreateOpsAlertRuleRequest {
  const { id, last_triggered_at, created_at, updated_at, ...fields } = rule
  const filters: OpsAlertRuleFilters = { ...rule.filters }
  if (accountMetricTypes.has(rule.metric_type) && draftAccountIds.value.length) {
    filters.account_ids = [...new Set(draftAccountIds.value)].sort((a, b) => a - b)
  } else delete filters.account_ids
  delete filters.upstream_status_codes
  delete filters.provider_codes
  delete filters.error_keywords
  const parsed = parsedFilterText.value
  if (rule.metric_type === ACCOUNT_UPSTREAM_ERROR_METRIC) {
    if (parsed.status.length) filters.upstream_status_codes = [...new Set(parsed.status.map(Number))].sort((a, b) => a - b)
    if (parsed.codes.length) filters.provider_codes = [...new Set(parsed.codes)].sort()
    if (parsed.keywords.length) filters.error_keywords = [...new Set(parsed.keywords)].sort()
  }
  const emailOverrides = [...new Map(parsed.emails.map((address) => [address.toLowerCase(), address])).values()]
  return {
    ...fields,
    filters: Object.keys(filters).length ? filters : null,
    notify_feishu: rule.notify_feishu ?? false,
    notify_emails: emailOverrides,
    custom_message: (rule.custom_message ?? '').trim()
  }
}

const editorValidation = computed(() => {
  const errors: string[] = []
  const r = draft.value
  if (!r) return { valid: true, errors }
  if (!r.name || !r.name.trim()) errors.push(t('admin.ops.alertRules.validation.nameRequired'))
  if (!r.metric_type) errors.push(t('admin.ops.alertRules.validation.metricRequired'))
  if (isBandwidthUtilizationMetric(r.metric_type) && !bandwidthLimitConfigured.value) {
    errors.push(t('admin.ops.alertRules.validation.bandwidthLimitRequired'))
  }
  if (groupMetricTypes.has(r.metric_type) && !parsePositiveInt(r.filters?.group_id)) {
    errors.push(t('admin.ops.alertRules.validation.groupIdRequired'))
  }
  if (!r.operator) errors.push(t('admin.ops.alertRules.validation.operatorRequired'))
  if (!(typeof r.threshold === 'number' && Number.isFinite(r.threshold)))
    errors.push(t('admin.ops.alertRules.validation.thresholdRequired'))
  if (!windowOptions.value.some((option) => option.value === r.window_minutes)) {
    errors.push(t(usesExtendedWindow.value ? 'admin.ops.alertRules.validation.extendedWindowRange' : 'admin.ops.alertRules.validation.windowRange'))
  }
  if (!(typeof r.sustained_minutes === 'number' && Number.isInteger(r.sustained_minutes) && r.sustained_minutes >= 1 && r.sustained_minutes <= 1440)) {
    errors.push(t('admin.ops.alertRules.validation.sustainedRange'))
  }
  if (!(typeof r.cooldown_minutes === 'number' && Number.isInteger(r.cooldown_minutes) && r.cooldown_minutes >= 0 && r.cooldown_minutes <= 1440)) {
    errors.push(t('admin.ops.alertRules.validation.cooldownRange'))
  }
  const parsed = parsedFilterText.value
  if (isAccountUpstreamError.value) {
    if (!draftAccountIds.value.length && !draftGroupId.value && !parsed.status.length && !parsed.codes.length) {
      errors.push(t('admin.ops.alertRules.validation.upstreamScopeRequired'))
    }
    if (parsed.status.length > 20 || parsed.status.some((code) => !/^[45]\d{2}$/.test(code))) {
      errors.push(t('admin.ops.alertRules.validation.upstreamStatuses'))
    }
    if (parsed.codes.length > 20 || parsed.codes.some((code) => !/^[a-zA-Z0-9_.:-]{1,64}$/.test(code))) {
      errors.push(t('admin.ops.alertRules.validation.providerCodes'))
    }
    if (parsed.keywords.length > 20 || parsed.keywords.some((word) => [...word].length > 128 || /\p{Cc}/u.test(word))) {
      errors.push(t('admin.ops.alertRules.validation.keywords'))
    }
  }
  if (parsed.emails.length > 20 || parsed.emails.some((address) => !/^[^\s<>@]+@[^\s<>@]+$/.test(address))) {
    errors.push(t('admin.ops.alertRules.validation.notifyEmails'))
  }
  if ([...(r.custom_message ?? '')].length > 500) errors.push(t('admin.ops.alertRules.validation.customMessage'))
  return { valid: errors.length === 0, errors }
})

async function save() {
  if (!draft.value) return
  if (!editorValidation.value.valid) {
    appStore.showError(editorValidation.value.errors[0] || t('admin.ops.alertRules.validation.invalid'))
    return
  }
  saving.value = true
  try {
    const input = buildRuleRequest(draft.value)
    if (editingId.value) {
      await opsAPI.updateAlertRule(editingId.value, input)
    } else {
      await opsAPI.createAlertRule(input)
    }
    showEditor.value = false
    draft.value = null
    editingId.value = null
    await load()
    appStore.showSuccess(t('admin.ops.alertRules.saveSuccess'))
  } catch (err: any) {
    console.error('[OpsAlertRulesCard] Failed to save rule', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.alertRules.saveFailed'))
  } finally {
    saving.value = false
  }
}

const showDeleteConfirm = ref(false)
const pendingDelete = ref<AlertRule | null>(null)

function requestDelete(rule: AlertRule) {
  pendingDelete.value = rule
  showDeleteConfirm.value = true
}

async function confirmDelete() {
  if (!pendingDelete.value?.id) return
  try {
    await opsAPI.deleteAlertRule(pendingDelete.value.id)
    showDeleteConfirm.value = false
    pendingDelete.value = null
    await load()
    appStore.showSuccess(t('admin.ops.alertRules.deleteSuccess'))
  } catch (err: any) {
    console.error('[OpsAlertRulesCard] Failed to delete rule', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.alertRules.deleteFailed'))
  }
}

function cancelDelete() {
  showDeleteConfirm.value = false
  pendingDelete.value = null
}
</script>

<template>
  <div class="rounded-3xl bg-white p-6 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700">
    <div class="mb-4 flex flex-wrap items-start justify-between gap-3 sm:gap-4">
      <div>
        <h3 class="text-sm font-bold text-gray-900 dark:text-white">{{ t('admin.ops.alertRules.title') }}</h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.alertRules.description') }}</p>
      </div>

      <div class="flex items-center gap-2">
        <button class="btn btn-sm btn-primary" :disabled="loading" @click="openCreate">
          {{ t('admin.ops.alertRules.create') }}
        </button>
        <button
          class="flex items-center gap-1.5 rounded-lg bg-gray-100 px-3 py-1.5 text-xs font-bold text-gray-700 transition-colors hover:bg-gray-200 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600"
          :disabled="loading"
          @click="load"
        >
          <svg class="h-3.5 w-3.5" :class="{ 'animate-spin': loading }" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          {{ t('common.refresh') }}
        </button>
      </div>
    </div>

    <div v-if="loading" class="py-10 text-center text-sm text-gray-500 dark:text-gray-400">
      {{ t('admin.ops.alertRules.loading') }}
    </div>

    <div v-else-if="sortedRules.length === 0" class="rounded-xl border border-dashed border-gray-200 p-8 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
      {{ t('admin.ops.alertRules.empty') }}
    </div>

    <div v-else class="max-h-[520px] overflow-hidden rounded-xl border border-gray-200 dark:border-dark-700">
      <div class="max-h-[520px] overflow-y-auto">
        <div v-if="!isDesktopViewport" class="divide-y divide-gray-100 dark:divide-dark-800">
          <div v-for="row in sortedRules" :key="row.id" class="space-y-2 p-4">
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <div class="text-xs font-bold text-gray-900 dark:text-white">{{ row.name }}</div>
                <div v-if="row.description" class="mt-0.5 line-clamp-2 text-[11px] text-gray-500 dark:text-gray-400">
                  {{ row.description }}
                </div>
              </div>
              <span class="shrink-0 text-xs font-bold text-gray-700 dark:text-gray-200">{{ row.severity }}</span>
            </div>
            <div class="text-xs text-gray-700 dark:text-gray-200">
              <span class="font-mono">{{ row.metric_type }}</span>
              <span class="mx-1 text-gray-400">{{ row.operator }}</span>
              <span class="font-mono">{{ row.threshold }}</span>
            </div>
            <div class="flex items-center justify-between gap-2">
              <span class="text-xs text-gray-700 dark:text-gray-200">
                {{ row.enabled ? t('common.enabled') : t('common.disabled') }}
              </span>
              <div class="flex items-center gap-2">
                <button class="btn btn-sm btn-secondary" @click="openEdit(row)">{{ t('common.edit') }}</button>
                <button class="btn btn-sm btn-danger" @click="requestDelete(row)">{{ t('common.delete') }}</button>
              </div>
            </div>
            <div v-if="row.updated_at" class="text-[10px] text-gray-400">
              {{ formatDateTime(row.updated_at) }}
            </div>
          </div>
        </div>
        <table v-else class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
          <thead class="sticky top-0 z-10 bg-gray-50 dark:bg-dark-900">
            <tr>
              <th class="px-4 py-3 text-left text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                {{ t('admin.ops.alertRules.table.name') }}
              </th>
              <th class="px-4 py-3 text-left text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                {{ t('admin.ops.alertRules.table.metric') }}
              </th>
              <th class="px-4 py-3 text-left text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                {{ t('admin.ops.alertRules.table.severity') }}
              </th>
              <th class="px-4 py-3 text-left text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                {{ t('admin.ops.alertRules.table.enabled') }}
              </th>
              <th class="px-4 py-3 text-right text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                {{ t('admin.ops.alertRules.table.actions') }}
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-800">
            <tr v-for="row in sortedRules" :key="row.id" class="hover:bg-gray-50 dark:hover:bg-dark-700/50">
              <td class="px-4 py-3">
                <div class="text-xs font-bold text-gray-900 dark:text-white">{{ row.name }}</div>
                <div v-if="row.description" class="mt-0.5 line-clamp-2 text-[11px] text-gray-500 dark:text-gray-400">
                  {{ row.description }}
                </div>
                <div v-if="row.updated_at" class="mt-1 text-[10px] text-gray-400">
                  {{ formatDateTime(row.updated_at) }}
                </div>
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-700 dark:text-gray-200">
                <span class="font-mono">{{ formatMetricType(row.metric_type) }}</span>
                <span class="mx-1 text-gray-400">{{ row.operator }}</span>
                <span class="font-mono">{{ row.threshold }}</span>
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-xs font-bold text-gray-700 dark:text-gray-200">
                {{ row.severity }}
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-700 dark:text-gray-200">
                {{ row.enabled ? t('common.enabled') : t('common.disabled') }}
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-right text-xs">
                <button class="btn btn-sm btn-secondary" @click="openEdit(row)">{{ t('common.edit') }}</button>
                <button class="ml-2 btn btn-sm btn-danger" @click="requestDelete(row)">{{ t('common.delete') }}</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <BaseDialog
      :show="showEditor"
      :title="editingId ? t('admin.ops.alertRules.editTitle') : t('admin.ops.alertRules.createTitle')"
      width="wide"
      @close="showEditor = false"
    >
      <div v-if="draft" class="space-y-4">
        <div v-if="!editorValidation.valid" class="rounded-xl bg-red-50 p-4 text-xs text-red-700 dark:bg-red-900/30 dark:text-red-300">
          <div class="font-bold">{{ t('admin.ops.alertRules.validation.title') }}</div>
          <ul class="mt-1 list-disc pl-5">
            <li v-for="e in editorValidation.errors" :key="e">{{ e }}</li>
          </ul>
        </div>

        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div class="md:col-span-2">
            <label class="input-label">{{ t('admin.ops.alertRules.form.name') }}</label>
            <input v-model="draft!.name" class="input" type="text" />
          </div>

          <div class="md:col-span-2">
            <label class="input-label">{{ t('admin.ops.alertRules.form.description') }}</label>
            <input v-model="draft!.description" class="input" type="text" />
          </div>

          <div>
            <label class="input-label">{{ t('admin.ops.alertRules.form.metric') }}</label>
            <Select :model-value="draft.metric_type" :options="metricOptions" @update:model-value="changeMetric" />
            <div v-if="selectedMetricDefinition" class="mt-1 space-y-0.5 text-xs text-gray-500 dark:text-gray-400">
              <p>{{ selectedMetricDefinition.description }}</p>
              <p v-if="isBandwidthUtilizationMetric(draft!.metric_type) && !bandwidthLimitConfigured" class="font-semibold text-amber-600 dark:text-amber-300">
                {{ t('admin.ops.alertRules.hints.bandwidthLimitRequired') }}
              </p>
              <p>
                {{
                  t('admin.ops.alertRules.hints.recommended', {
                    operator: selectedMetricDefinition.recommendedOperator,
                    threshold: selectedMetricDefinition.recommendedThreshold,
                    unit: selectedMetricDefinition.unit || ''
                  })
                }}
              </p>
            </div>
          </div>

          <div v-if="!isGoodPutIncident">
            <label class="input-label">{{ t('admin.ops.alertRules.form.operator') }}</label>
            <Select v-model="draft!.operator" :options="operatorOptions" />
          </div>

          <div class="md:col-span-2">
            <label class="input-label">
              {{ t('admin.ops.alertRules.form.groupId') }}
              <span v-if="isGroupMetricSelected" class="ml-1 text-red-500">*</span>
            </label>
            <Select
              v-model="draftGroupId"
              :options="groupOptions"
              searchable
              :placeholder="t('admin.ops.alertRules.form.groupPlaceholder')"
              :error="isGroupMetricSelected && !draftGroupId"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ isGroupMetricSelected ? t('admin.ops.alertRules.hints.groupRequired') : t('admin.ops.alertRules.hints.groupOptional') }}
            </p>
          </div>

          <OpsAlertAccountPicker
            v-if="showEditor && usesAccountScope"
            v-model="draftAccountIds"
            :platform="draft.filters?.platform"
            :group-id="draftGroupId"
            class="md:col-span-2"
          />

          <template v-if="isAccountUpstreamError">
            <div>
              <label :for="`${formId}-status`" class="input-label">{{ t('admin.ops.alertRules.form.upstreamStatuses') }}</label>
              <input :id="`${formId}-status`" v-model="filterText.status" class="input" type="text" placeholder="400, 402" />
            </div>
            <div>
              <label :for="`${formId}-codes`" class="input-label">{{ t('admin.ops.alertRules.form.providerCodes') }}</label>
              <input :id="`${formId}-codes`" v-model="filterText.codes" class="input" type="text" placeholder="insufficient_quota" />
            </div>
            <div class="md:col-span-2">
              <label :for="`${formId}-keywords`" class="input-label">{{ t('admin.ops.alertRules.form.keywords') }}</label>
              <textarea :id="`${formId}-keywords`" v-model="filterText.keywords" class="input min-h-16 resize-y" rows="2" :placeholder="t('admin.ops.alertRules.hints.onePerLine')" />
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.alertRules.hints.sameAttempt') }}</p>
            </div>
          </template>

          <div v-if="!isGoodPutIncident">
            <label class="input-label">{{ t('admin.ops.alertRules.form.threshold') }}</label>
            <input v-model.number="draft!.threshold" class="input" type="number" />
          </div>

          <div>
            <label class="input-label">{{ t('admin.ops.alertRules.form.severity') }}</label>
            <Select v-model="draft!.severity" :options="severityOptions" />
          </div>

          <div>
            <label class="input-label">{{ t('admin.ops.alertRules.form.window') }}</label>
            <Select v-model="draft!.window_minutes" :options="windowOptions" />
          </div>

          <div v-if="!isGoodPutIncident">
            <label class="input-label">{{ t('admin.ops.alertRules.form.sustained') }}</label>
            <input v-model.number="draft!.sustained_minutes" class="input" type="number" min="1" max="1440" />
          </div>

          <div v-if="!isGoodPutIncident">
            <label class="input-label">{{ t('admin.ops.alertRules.form.cooldown') }}</label>
            <input v-model.number="draft!.cooldown_minutes" class="input" type="number" min="0" max="1440" />
          </div>

          <div class="flex items-center justify-between rounded-xl bg-gray-50 px-4 py-3 dark:bg-dark-800/50 md:col-span-2">
            <span class="text-xs font-bold text-gray-700 dark:text-gray-200">{{ t('admin.ops.alertRules.form.enabled') }}</span>
            <input v-model="draft!.enabled" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
          </div>

          <label class="flex items-center justify-between rounded-xl bg-gray-50 px-4 py-3 dark:bg-dark-800/50 md:col-span-2">
            <span class="text-xs font-bold text-gray-700 dark:text-gray-200">{{ t('admin.ops.alertRules.form.notifyEmail') }}</span>
            <input v-model="draft!.notify_email" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
          </label>
          <div v-if="draft.notify_email" class="md:col-span-2">
            <label :for="`${formId}-emails`" class="input-label">{{ t('admin.ops.alertRules.form.notifyEmails') }}</label>
            <textarea :id="`${formId}-emails`" v-model="filterText.emails" class="input min-h-16 resize-y" rows="2" :placeholder="t('admin.ops.alertRules.hints.onePerLine')" />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.alertRules.hints.notifyEmails') }}</p>
          </div>
          <label v-if="!isGoodPutIncident" class="flex items-center justify-between gap-4 rounded-xl bg-gray-50 px-4 py-3 dark:bg-dark-800/50 md:col-span-2">
            <span>
              <span class="block text-xs font-bold text-gray-700 dark:text-gray-200">{{ t('admin.ops.alertRules.form.notifyFeishu') }}</span>
              <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.alertRules.hints.notifyFeishu') }}</span>
            </span>
            <input v-model="draft.notify_feishu" type="checkbox" class="h-4 w-4 shrink-0 rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
          </label>
          <div v-if="!isGoodPutIncident" class="md:col-span-2">
            <label :for="`${formId}-custom-message`" class="input-label">{{ t('admin.ops.alertRules.form.customMessage') }}</label>
            <textarea :id="`${formId}-custom-message`" v-model="draft.custom_message" class="input min-h-16 resize-y" rows="2" />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.alertRules.hints.customMessage') }}</p>
          </div>
        </div>
      </div>

      <template #footer>
        <div class="flex items-center justify-end gap-2">
          <button class="btn btn-secondary" :disabled="saving" @click="showEditor = false">
            {{ t('common.cancel') }}
          </button>
          <button class="btn btn-primary" :disabled="saving" @click="save">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="showDeleteConfirm"
      :title="t('admin.ops.alertRules.deleteConfirmTitle')"
      :message="t('admin.ops.alertRules.deleteConfirmMessage')"
      :confirmText="t('common.delete')"
      :cancelText="t('common.cancel')"
      @confirm="confirmDelete"
      @cancel="cancelDelete"
    />
  </div>
</template>
