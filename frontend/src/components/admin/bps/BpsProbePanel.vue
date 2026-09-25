<template>
  <section class="card p-5" data-test="bps-probe-panel">
    <div class="mb-3 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.bpsUpstream.probe.title') }}</h2>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.bpsUpstream.probe.hint') }}</p>
      </div>
    </div>

    <div class="grid gap-4 lg:grid-cols-2">
      <div class="space-y-2">
        <div class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.bpsUpstream.probe.accounts') }}</div>
        <div class="max-h-44 space-y-1 overflow-y-auto rounded-lg border border-gray-200 p-2 dark:border-dark-600">
          <p v-if="!accountChoices.length" class="px-1 py-2 text-xs text-gray-500">{{ t('admin.bpsUpstream.probe.noAccounts') }}</p>
          <label v-for="account in accountChoices" :key="account.id" class="flex items-center gap-2 px-1 text-sm text-gray-700 dark:text-gray-200">
            <input v-model="selectedIds" type="checkbox" :value="account.id" :data-test="`bps-probe-account-${account.id}`" />
            <span class="truncate">{{ account.name }}</span>
            <span class="text-xs text-gray-400">#{{ account.id }}</span>
          </label>
        </div>
        <div class="flex items-end gap-2">
          <div class="min-w-0 flex-1">
            <Select
              v-model="extraValue"
              :options="extraOptions"
              :placeholder="t('admin.bpsUpstream.probe.extraPlaceholder')"
              remote
              :loading="extraLoading"
              data-test="bps-probe-extra-picker"
              @search="searchExtra"
            />
          </div>
          <button type="button" class="btn btn-secondary" :disabled="!extraValue" data-test="bps-probe-extra-add" @click="addExtra">
            {{ t('admin.bpsUpstream.probe.addAccount') }}
          </button>
        </div>
      </div>

      <div class="space-y-3">
        <div class="flex flex-wrap items-center gap-4 text-sm text-gray-700 dark:text-gray-200">
          <span class="font-medium">{{ t('admin.bpsUpstream.probe.paths') }}</span>
          <label v-for="path in probePaths" :key="path" class="flex items-center gap-1">
            <input v-model="selectedPaths" type="checkbox" :value="path" :data-test="`bps-probe-path-${path}`" />
            {{ t(`admin.bpsUpstream.probe.path.${path}`) }}
          </label>
        </div>
        <div class="grid grid-cols-2 gap-2">
          <label class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.bpsUpstream.probe.model') }}
            <select v-model="model" class="input mt-1" data-test="bps-probe-model">
              <option v-for="item in modelChoices" :key="item" :value="item">{{ item }}</option>
            </select>
          </label>
          <label class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.bpsUpstream.probe.effort') }}
            <select v-model="effort" class="input mt-1" data-test="bps-probe-effort">
              <option v-for="item in efforts" :key="item" :value="item">{{ item || t('admin.bpsUpstream.probe.effortDefault') }}</option>
            </select>
          </label>
        </div>
        <div>
          <div class="mb-1 flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span>{{ t('admin.bpsUpstream.probe.prompt') }}</span>
            <button
              v-for="preset in presets"
              :key="preset"
              type="button"
              class="rounded bg-gray-100 px-2 py-0.5 hover:bg-gray-200 dark:bg-dark-700 dark:hover:bg-dark-600"
              :data-test="`bps-probe-preset-${preset}`"
              @click="prompt = t(`admin.bpsUpstream.probe.presets.${preset}.text`)"
            >
              {{ t(`admin.bpsUpstream.probe.presets.${preset}.label`) }}
            </button>
          </div>
          <textarea v-model="prompt" rows="5" class="input font-mono text-xs" :maxlength="MAX_PROMPT" data-test="bps-probe-prompt" />
        </div>
        <div class="flex items-center justify-between gap-2">
          <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.bpsUpstream.probe.runCount', { count: runCount }) }}</span>
          <button type="button" class="btn btn-primary" :disabled="!canRun" data-test="bps-probe-run" @click="run">
            {{ submitting ? t('admin.bpsUpstream.probe.submitting') : t('admin.bpsUpstream.probe.run') }}
          </button>
        </div>
      </div>
    </div>

    <div class="mt-5 mb-2 flex flex-wrap items-center justify-between gap-2">
      <div class="text-sm font-medium text-gray-700 dark:text-gray-200">
        {{ t('admin.bpsUpstream.probe.results') }}
        <span class="ml-1 text-xs font-normal text-gray-400">{{ t('admin.bpsUpstream.probe.retention') }}</span>
      </div>
      <div class="flex gap-2">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="loadResults">{{ t('common.refresh') }}</button>
        <button type="button" class="btn btn-danger btn-sm" :disabled="!results.length" data-test="bps-probe-clear" @click="clearAll">
          {{ t('admin.bpsUpstream.probe.clearAll') }}
        </button>
      </div>
    </div>
    <div class="overflow-x-auto">
      <table class="w-full text-left text-sm">
        <thead class="text-xs uppercase text-gray-500 dark:text-gray-400">
          <tr>
            <th class="px-3 py-2">{{ t('admin.bpsUpstream.columns.time') }}</th>
            <th class="px-3 py-2">{{ t('admin.bpsUpstream.columns.account') }}</th>
            <th class="px-3 py-2">{{ t('admin.bpsUpstream.probe.paths') }}</th>
            <th class="px-3 py-2">{{ t('admin.bpsUpstream.columns.effort') }}</th>
            <th class="whitespace-nowrap px-3 py-2">{{ t('admin.bpsUpstream.columns.status') }}</th>
            <th class="px-3 py-2 text-right">{{ t('admin.bpsUpstream.columns.duration') }}</th>
            <th class="px-3 py-2 text-right">{{ t('admin.bpsUpstream.probe.tokens') }}</th>
            <th class="px-3 py-2">{{ t('admin.bpsUpstream.probe.preview') }}</th>
            <th class="px-3 py-2 text-right">{{ t('admin.bpsUpstream.columns.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-if="!results.length">
            <td colspan="9" class="px-3 py-6 text-center text-gray-500 dark:text-gray-400">{{ t('admin.bpsUpstream.probe.empty') }}</td>
          </tr>
          <tr v-for="row in results" :key="row.id" data-test="bps-probe-row">
            <td class="whitespace-nowrap px-3 py-2 text-xs text-gray-600 dark:text-gray-300">{{ formatDateTime(row.created_at) }}</td>
            <td class="px-3 py-2">
              <div class="text-gray-900 dark:text-white">{{ row.account_name || '-' }}</div>
              <div class="text-xs text-gray-500">#{{ row.account_id }}</div>
            </td>
            <td class="whitespace-nowrap px-3 py-2">
              <span :class="row.path === 'bps' ? 'badge badge-primary' : 'badge badge-gray'">{{ t(`admin.bpsUpstream.probe.path.${row.path}`) }}</span>
            </td>
            <td class="whitespace-nowrap px-3 py-2 text-xs text-gray-600 dark:text-gray-300">
              <div>{{ row.model }}</div>
              <div>{{ formatEffort(row) }}</div>
            </td>
            <td class="whitespace-nowrap px-3 py-2">
              <span :class="statusClass(row.status)" :data-test="`bps-probe-status-${row.id}`">{{ t(`admin.bpsUpstream.probe.status.${row.status}`) }}</span>
            </td>
            <td class="whitespace-nowrap px-3 py-2 text-right text-xs">{{ row.duration_ms ? `${(row.duration_ms / 1000).toFixed(1)}s` : '-' }}</td>
            <td class="whitespace-nowrap px-3 py-2 text-right text-xs" :title="t('admin.bpsUpstream.probe.tokensTitle')">
              {{ row.input_tokens }} / {{ row.output_tokens }} / {{ row.reasoning_tokens }}
            </td>
            <td class="max-w-[320px] px-3 py-2 text-xs text-gray-600 dark:text-gray-300">
              <div v-if="row.error_message" class="truncate text-red-600 dark:text-red-400" :title="row.error_message">{{ row.error_message }}</div>
              <div class="truncate" :title="row.content_preview">{{ row.content_preview || (row.error_message ? '' : '-') }}</div>
            </td>
            <td class="whitespace-nowrap px-3 py-2 text-right">
              <button type="button" class="btn btn-secondary btn-sm mr-2" :disabled="!row.content_length && !row.error_message" :data-test="`bps-probe-view-${row.id}`" @click="view(row.id)">
                {{ t('admin.bpsUpstream.probe.view') }}
              </button>
              <button type="button" class="btn btn-danger btn-sm" :data-test="`bps-probe-delete-${row.id}`" @click="remove(row.id)">
                {{ t('admin.bpsUpstream.remove') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <BaseDialog :show="!!detail" :title="t('admin.bpsUpstream.probe.detailTitle', { id: detail?.id ?? '' })" width="extra-wide" @close="detail = null">
      <div v-if="detail" class="space-y-3 text-sm" data-test="bps-probe-detail">
        <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
          <span>{{ detail.account_name }} #{{ detail.account_id }}</span>
          <span>{{ t(`admin.bpsUpstream.probe.path.${detail.path}`) }}</span>
          <span>{{ detail.model }} · {{ formatEffort(detail) }}</span>
          <span>{{ t(`admin.bpsUpstream.probe.status.${detail.status}`) }}</span>
        </div>
        <details class="rounded bg-gray-50 p-2 text-xs dark:bg-dark-700">
          <summary class="cursor-pointer text-gray-600 dark:text-gray-300">{{ t('admin.bpsUpstream.probe.prompt') }}</summary>
          <pre class="mt-2 whitespace-pre-wrap break-words">{{ detail.prompt }}</pre>
        </details>
        <div v-if="detail.error_message" class="rounded bg-red-50 p-2 text-xs text-red-700 dark:bg-red-950 dark:text-red-300">{{ detail.error_message }}</div>
        <div v-if="detailHtml" class="flex items-center gap-2">
          <button type="button" class="btn btn-primary btn-sm" data-test="bps-probe-open-html" @click="openHtml">{{ t('admin.bpsUpstream.probe.openHtml') }}</button>
          <button type="button" class="btn btn-secondary btn-sm" @click="showInlineHtml = !showInlineHtml">
            {{ showInlineHtml ? t('admin.bpsUpstream.probe.showText') : t('admin.bpsUpstream.probe.showHtml') }}
          </button>
        </div>
        <iframe
          v-if="detailHtml && showInlineHtml"
          :srcdoc="detailHtml"
          sandbox="allow-scripts allow-forms allow-modals"
          class="h-[60vh] w-full rounded border border-gray-200 bg-white dark:border-dark-600"
          data-test="bps-probe-inline-html"
        />
        <pre v-else class="max-h-[60vh] overflow-auto whitespace-pre-wrap break-words rounded bg-gray-50 p-3 font-mono text-xs text-gray-800 dark:bg-dark-800 dark:text-gray-100">{{ detail.content || '-' }}</pre>
      </div>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { list as listAccounts } from '@/api/admin/accounts'
import {
  createProbes,
  deleteAllProbes,
  deleteProbe,
  getProbe,
  listProbes,
  type BPSProbePath,
  type BPSProbeResult,
  type BPSProbeStatus,
} from '@/api/admin/bpsUpstream'
import { useAppStore } from '@/stores'
import { formatDateTime } from '@/utils/format'
import { extractHtmlDocument, openHtmlPreviewWindow } from '@/utils/htmlPreview'

interface AccountChoice { id: number; name: string }

const props = defineProps<{
  listedAccounts: AccountChoice[]
  models: string[]
}>()

const POLL_INTERVAL_MS = 3000
const MAX_PROMPT = 8000
const DEFAULT_MODEL = 'gpt-5.6-terra'
const probePaths: BPSProbePath[] = ['bps', 'native']
const efforts = ['', 'none', 'minimal', 'low', 'medium', 'high', 'xhigh']
const presets = ['reasoning', 'html', 'code'] as const

const { t } = useI18n()
const appStore = useAppStore()

const selectedIds = ref<number[]>([])
const selectedPaths = ref<BPSProbePath[]>(['bps', 'native'])
const model = ref(DEFAULT_MODEL)
const effort = ref('high')
const prompt = ref(t('admin.bpsUpstream.probe.presets.reasoning.text'))
const submitting = ref(false)
const extraAccounts = ref<AccountChoice[]>([])
const extraValue = ref<string | null>(null)
const extraSearchResults = ref<AccountChoice[]>([])
const extraLoading = ref(false)
let extraAbort: AbortController | null = null

const results = ref<BPSProbeResult[]>([])
const loading = ref(false)
const detail = ref<BPSProbeResult | null>(null)
const showInlineHtml = ref(true)
let pollTimer: ReturnType<typeof setTimeout> | undefined
let unmounted = false

const accountChoices = computed(() => {
  const unique = new Map<number, AccountChoice>()
  for (const account of [...props.listedAccounts, ...extraAccounts.value]) unique.set(account.id, account)
  return [...unique.values()]
})

const extraOptions = computed(() => {
  const shown = new Set(accountChoices.value.map((account) => account.id))
  return extraSearchResults.value
    .filter((account) => !shown.has(account.id))
    .map((account) => ({ value: String(account.id), label: `${account.name} (#${account.id})` }))
})

const modelChoices = computed(() => (props.models.length ? props.models : [DEFAULT_MODEL]))
const runCount = computed(() => selectedIds.value.length * selectedPaths.value.length)
const canRun = computed(() => !submitting.value && runCount.value > 0 && !!prompt.value.trim() && !!model.value)
const hasPending = computed(() => results.value.some((row) => row.status === 'queued' || row.status === 'running'))
const detailHtml = computed(() => extractHtmlDocument(detail.value?.content))

watch(modelChoices, (choices) => {
  if (!choices.includes(model.value)) model.value = choices.includes(DEFAULT_MODEL) ? DEFAULT_MODEL : choices[0]
}, { immediate: true })

function errorMessage(err: unknown, fallback: string) {
  return (err as { message?: string } | null)?.message || fallback
}

function formatEffort(row: BPSProbeResult) {
  const requested = row.effort || t('admin.bpsUpstream.probe.effortDefault')
  return row.applied_effort && row.applied_effort !== row.effort ? `${requested} → ${row.applied_effort}` : requested
}

function statusClass(status: BPSProbeStatus) {
  switch (status) {
    case 'succeeded': return 'badge badge-success'
    case 'failed': return 'badge badge-danger'
    case 'running': return 'badge badge-primary'
    default: return 'badge badge-gray'
  }
}

function schedulePoll() {
  clearTimeout(pollTimer)
  pollTimer = undefined
  if (!unmounted && hasPending.value) pollTimer = setTimeout(() => { void loadResults() }, POLL_INTERVAL_MS)
}

async function loadResults() {
  loading.value = true
  try {
    results.value = await listProbes()
  } catch (err) {
    appStore.showError(errorMessage(err, t('admin.bpsUpstream.loadFailed')))
  } finally {
    loading.value = false
    schedulePoll()
  }
}

async function run() {
  if (!canRun.value) return
  submitting.value = true
  try {
    await createProbes({
      account_ids: selectedIds.value,
      paths: selectedPaths.value,
      model: model.value,
      effort: effort.value,
      prompt: prompt.value.trim(),
    })
    appStore.showSuccess(t('admin.bpsUpstream.probe.started', { count: runCount.value }))
    await loadResults()
  } catch (err) {
    appStore.showError(errorMessage(err, t('admin.bpsUpstream.probe.runFailed')))
  } finally {
    submitting.value = false
  }
}

async function view(id: number) {
  try {
    detail.value = await getProbe(id)
    showInlineHtml.value = true
  } catch (err) {
    appStore.showError(errorMessage(err, t('admin.bpsUpstream.loadFailed')))
  }
}

function openHtml() {
  if (!detailHtml.value || !detail.value) return
  if (!openHtmlPreviewWindow(detailHtml.value, `probe #${detail.value.id}`)) {
    appStore.showError(t('admin.bpsUpstream.probe.popupBlocked'))
  }
}

async function remove(id: number) {
  try {
    await deleteProbe(id)
    results.value = results.value.filter((row) => row.id !== id)
    if (detail.value?.id === id) detail.value = null
  } catch (err) {
    appStore.showError(errorMessage(err, t('admin.bpsUpstream.saveFailed')))
  }
}

async function clearAll() {
  if (!window.confirm(t('admin.bpsUpstream.probe.clearConfirm'))) return
  try {
    const deleted = await deleteAllProbes()
    appStore.showSuccess(t('admin.bpsUpstream.probe.cleared', { count: deleted }))
    detail.value = null
    await loadResults()
  } catch (err) {
    appStore.showError(errorMessage(err, t('admin.bpsUpstream.saveFailed')))
  }
}

function addExtra() {
  const id = Number(extraValue.value)
  const account = extraSearchResults.value.find((item) => item.id === id)
  if (!account) return
  extraAccounts.value = [...extraAccounts.value, account]
  if (!selectedIds.value.includes(id)) selectedIds.value = [...selectedIds.value, id]
  extraValue.value = null
}

async function searchExtra(search = '') {
  extraAbort?.abort()
  const controller = new AbortController()
  extraAbort = controller
  extraLoading.value = true
  try {
    const result = await listAccounts(
      1,
      50,
      { platform: 'openai', type: 'oauth', lite: 'true', ...(search ? { search } : {}) },
      { signal: controller.signal },
    )
    if (controller.signal.aborted) return
    extraSearchResults.value = result.items.map((account) => ({ id: account.id, name: account.name }))
  } catch {
    if (!controller.signal.aborted) extraSearchResults.value = []
  } finally {
    if (extraAbort === controller) extraLoading.value = false
  }
}

onMounted(() => {
  void loadResults()
  void searchExtra()
})

onUnmounted(() => {
  unmounted = true
  clearTimeout(pollTimer)
  extraAbort?.abort()
})
</script>
