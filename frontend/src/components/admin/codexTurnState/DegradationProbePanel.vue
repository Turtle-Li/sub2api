<template>
  <section class="card p-5" data-test="degradation-probe-panel">
    <div class="mb-3 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.codexTurnState.probe.title') }}</h2>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.codexTurnState.probe.hint') }}</p>
      </div>
    </div>

    <div class="grid gap-4 lg:grid-cols-2">
      <div class="space-y-2">
        <label class="block text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.codexTurnState.probe.model') }}
          <select v-model="model" class="input mt-1" :disabled="!modelChoices.length" data-test="degradation-probe-model">
            <option v-for="item in modelChoices" :key="item" :value="item">{{ item }}</option>
          </select>
        </label>
        <div class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.codexTurnState.probe.accounts') }}</div>
        <div class="max-h-44 space-y-1 overflow-y-auto rounded-lg border border-gray-200 p-2 dark:border-dark-600">
          <p v-if="!accountChoices.length" class="px-1 py-2 text-xs text-gray-500" data-test="degradation-probe-no-accounts">
            {{ t('admin.codexTurnState.probe.noAccounts') }}
          </p>
          <label v-for="account in accountChoices" :key="account.id" class="flex items-center gap-2 px-1 text-sm text-gray-700 dark:text-gray-200">
            <input v-model="selectedIds" type="checkbox" :value="account.id" :data-test="`degradation-probe-account-${account.id}`" />
            <span class="truncate">{{ account.name }}</span>
            <span class="text-xs text-gray-400">#{{ account.id }}</span>
          </label>
        </div>
        <p class="text-xs text-gray-400 dark:text-gray-500">{{ t('admin.codexTurnState.probe.accountsHint') }}</p>
      </div>

      <div class="space-y-3">
        <label class="block text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.codexTurnState.probe.effort') }}
          <select v-model="effort" class="input mt-1" data-test="degradation-probe-effort">
            <option v-for="item in efforts" :key="item" :value="item">{{ item || t('admin.codexTurnState.probe.effortDefault') }}</option>
          </select>
        </label>
        <div>
          <div class="mb-1 flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span>{{ t('admin.codexTurnState.probe.prompt') }}</span>
            <button
              v-for="preset in presets"
              :key="preset"
              type="button"
              class="rounded bg-gray-100 px-2 py-0.5 hover:bg-gray-200 dark:bg-dark-700 dark:hover:bg-dark-600"
              :data-test="`degradation-probe-preset-${preset}`"
              @click="prompt = t(`admin.codexTurnState.probe.presets.${preset}.text`)"
            >
              {{ t(`admin.codexTurnState.probe.presets.${preset}.label`) }}
            </button>
          </div>
          <textarea v-model="prompt" rows="5" class="input font-mono text-xs" :maxlength="MAX_PROMPT" data-test="degradation-probe-prompt" />
        </div>
        <div class="flex items-center justify-between gap-2">
          <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.codexTurnState.probe.runCount', { count: selectedIds.length }) }}</span>
          <button type="button" class="btn btn-primary" :disabled="!canRun" data-test="degradation-probe-run" @click="run">
            {{ submitting ? t('admin.codexTurnState.probe.submitting') : t('admin.codexTurnState.probe.run') }}
          </button>
        </div>
      </div>
    </div>

    <div class="mb-2 mt-5 flex flex-wrap items-center justify-between gap-2">
      <div class="text-sm font-medium text-gray-700 dark:text-gray-200">
        {{ t('admin.codexTurnState.probe.results') }}
        <span class="ml-1 text-xs font-normal text-gray-400">{{ t('admin.codexTurnState.probe.retention') }}</span>
      </div>
      <div class="flex gap-2">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="loadResults">{{ t('common.refresh') }}</button>
        <button type="button" class="btn btn-danger btn-sm" :disabled="!results.length" data-test="degradation-probe-clear" @click="clearAll">
          {{ t('admin.codexTurnState.probe.clearAll') }}
        </button>
      </div>
    </div>
    <div class="overflow-x-auto">
      <table class="w-full text-left text-sm">
        <thead class="text-xs uppercase text-gray-500 dark:text-gray-400">
          <tr>
            <th class="px-3 py-2">{{ t('admin.codexTurnState.columns.time') }}</th>
            <th class="px-3 py-2">{{ t('admin.codexTurnState.columns.account') }}</th>
            <th class="px-3 py-2">{{ t('admin.codexTurnState.columns.effort') }}</th>
            <th class="whitespace-nowrap px-3 py-2">{{ t('admin.codexTurnState.columns.status') }}</th>
            <th class="px-3 py-2 text-right">{{ t('admin.codexTurnState.columns.duration') }}</th>
            <th class="px-3 py-2 text-right">{{ t('admin.codexTurnState.probe.tokens') }}</th>
            <th class="px-3 py-2">{{ t('admin.codexTurnState.probe.preview') }}</th>
            <th class="px-3 py-2 text-right">{{ t('admin.codexTurnState.columns.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-if="!results.length">
            <td colspan="8" class="px-3 py-6 text-center text-gray-500 dark:text-gray-400">{{ t('admin.codexTurnState.probe.empty') }}</td>
          </tr>
          <tr v-for="row in results" :key="row.id" data-test="degradation-probe-row">
            <td class="whitespace-nowrap px-3 py-2 text-xs text-gray-600 dark:text-gray-300">{{ formatDateTime(row.created_at) }}</td>
            <td class="px-3 py-2">
              <div class="text-gray-900 dark:text-white">{{ row.account_name || '-' }}</div>
              <div class="text-xs text-gray-500">#{{ row.account_id }}</div>
            </td>
            <td class="whitespace-nowrap px-3 py-2 text-xs text-gray-600 dark:text-gray-300">
              <div>{{ row.model }}</div>
              <div>{{ formatEffort(row) }}</div>
            </td>
            <td class="whitespace-nowrap px-3 py-2">
              <span :class="statusClass(row.status)" :data-test="`degradation-probe-status-${row.id}`">{{ t(`admin.codexTurnState.probe.status.${row.status}`) }}</span>
            </td>
            <td class="whitespace-nowrap px-3 py-2 text-right text-xs">{{ row.duration_ms ? `${(row.duration_ms / 1000).toFixed(1)}s` : '-' }}</td>
            <td class="whitespace-nowrap px-3 py-2 text-right text-xs" :title="t('admin.codexTurnState.probe.tokensTitle')">
              {{ row.input_tokens }} / {{ row.output_tokens }} / {{ row.reasoning_tokens }}
            </td>
            <td class="max-w-[320px] px-3 py-2 text-xs text-gray-600 dark:text-gray-300">
              <div v-if="row.error_message" class="truncate text-red-600 dark:text-red-400" :title="row.error_message">{{ row.error_message }}</div>
              <div class="truncate" :title="row.content_preview">{{ row.content_preview || (row.error_message ? '' : '-') }}</div>
            </td>
            <td class="whitespace-nowrap px-3 py-2 text-right">
              <button type="button" class="btn btn-secondary btn-sm mr-2" :disabled="!row.content_length && !row.error_message" :data-test="`degradation-probe-view-${row.id}`" @click="view(row.id)">
                {{ t('admin.codexTurnState.probe.view') }}
              </button>
              <button type="button" class="btn btn-danger btn-sm" :data-test="`degradation-probe-delete-${row.id}`" @click="remove(row.id)">
                {{ t('admin.codexTurnState.actions.delete') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <BaseDialog :show="!!detail" :title="t('admin.codexTurnState.probe.detailTitle', { id: detail?.id ?? '' })" width="extra-wide" @close="detail = null">
      <div v-if="detail" class="space-y-3 text-sm" data-test="degradation-probe-detail">
        <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
          <span>{{ detail.account_name }} #{{ detail.account_id }}</span>
          <span>{{ detail.model }} · {{ formatEffort(detail) }}</span>
          <span>{{ t(`admin.codexTurnState.probe.status.${detail.status}`) }}</span>
        </div>
        <details class="rounded bg-gray-50 p-2 text-xs dark:bg-dark-700">
          <summary class="cursor-pointer text-gray-600 dark:text-gray-300">{{ t('admin.codexTurnState.probe.prompt') }}</summary>
          <pre class="mt-2 whitespace-pre-wrap break-words">{{ detail.prompt }}</pre>
        </details>
        <div v-if="detail.error_message" class="rounded bg-red-50 p-2 text-xs text-red-700 dark:bg-red-950 dark:text-red-300">{{ detail.error_message }}</div>
        <div v-if="detailHtml" class="flex items-center gap-2">
          <button type="button" class="btn btn-primary btn-sm" data-test="degradation-probe-open-html" @click="openHtml">{{ t('admin.codexTurnState.probe.openHtml') }}</button>
          <button type="button" class="btn btn-secondary btn-sm" @click="showInlineHtml = !showInlineHtml">
            {{ showInlineHtml ? t('admin.codexTurnState.probe.showText') : t('admin.codexTurnState.probe.showHtml') }}
          </button>
        </div>
        <iframe
          v-if="detailHtml && showInlineHtml"
          :srcdoc="detailHtml"
          sandbox="allow-scripts allow-forms allow-modals"
          class="h-[60vh] w-full rounded border border-gray-200 bg-white dark:border-dark-600"
          data-test="degradation-probe-inline-html"
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
import {
  NO_PINNED_STATE_CODE,
  createProbes,
  deleteAllProbes,
  deleteProbe,
  getProbe,
  listProbes,
  type DegradationProbeResult,
  type DegradationProbeStatus,
} from '@/api/admin/codexDegradationProbe'
import { useAppStore } from '@/stores'
import { formatDateTime } from '@/utils/format'
import { extractHtmlDocument, openHtmlPreviewWindow } from '@/utils/htmlPreview'
import type { PinnedAccount } from './types'

const props = defineProps<{
  pinnedAccounts: PinnedAccount[]
}>()

const POLL_INTERVAL_MS = 3000
const MAX_PROMPT = 8000
const DEFAULT_MODEL = 'gpt-6-astra'
const efforts = ['', 'none', 'minimal', 'low', 'medium', 'high', 'xhigh']
const presets = ['pelican', 'reasoning', 'html', 'code'] as const

const { t } = useI18n()
const appStore = useAppStore()

const selectedIds = ref<number[]>([])
const model = ref('')
const effort = ref('medium')
const prompt = ref(t('admin.codexTurnState.probe.presets.pelican.text'))
const submitting = ref(false)

const results = ref<DegradationProbeResult[]>([])
const loading = ref(false)
const detail = ref<DegradationProbeResult | null>(null)
const showInlineHtml = ref(true)
let pollTimer: ReturnType<typeof setTimeout> | undefined
let unmounted = false

const modelChoices = computed(() => [...new Set(props.pinnedAccounts.flatMap((account) => account.models))].sort())
const accountChoices = computed(() => props.pinnedAccounts.filter((account) => account.models.includes(model.value)))
const canRun = computed(() => !submitting.value && selectedIds.value.length > 0 && !!prompt.value.trim() && !!model.value)
const hasPending = computed(() => results.value.some((row) => row.status === 'queued' || row.status === 'running'))
const detailHtml = computed(() => extractHtmlDocument(detail.value?.content))

watch(modelChoices, (choices) => {
  if (choices.includes(model.value)) return
  model.value = choices.includes(DEFAULT_MODEL) ? DEFAULT_MODEL : choices[0] ?? ''
}, { immediate: true })

// 票据过期或换模型后，只保留仍有有效票据的已选账号。
watch(accountChoices, (choices) => {
  const eligible = new Set(choices.map((account) => account.id))
  if (selectedIds.value.some((id) => !eligible.has(id))) selectedIds.value = selectedIds.value.filter((id) => eligible.has(id))
})

function errorMessage(err: unknown, fallback: string) {
  return (err as { message?: string } | null)?.message || fallback
}

function formatEffort(row: DegradationProbeResult) {
  const requested = row.effort || t('admin.codexTurnState.probe.effortDefault')
  return row.applied_effort && row.applied_effort !== row.effort ? `${requested} → ${row.applied_effort}` : requested
}

function statusClass(status: DegradationProbeStatus) {
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
    appStore.showError(errorMessage(err, t('admin.codexTurnState.probe.loadFailed')))
  } finally {
    loading.value = false
    schedulePoll()
  }
}

async function run() {
  if (!canRun.value) return
  submitting.value = true
  const count = selectedIds.value.length
  try {
    await createProbes({
      account_ids: selectedIds.value,
      model: model.value,
      effort: effort.value,
      prompt: prompt.value.trim(),
    })
    appStore.showSuccess(t('admin.codexTurnState.probe.started', { count }))
    await loadResults()
  } catch (err) {
    if ((err as { code?: unknown } | null)?.code === NO_PINNED_STATE_CODE) {
      appStore.showError(t('admin.codexTurnState.probe.noPinnedState'))
    } else {
      appStore.showError(errorMessage(err, t('admin.codexTurnState.probe.runFailed')))
    }
  } finally {
    submitting.value = false
  }
}

async function view(id: number) {
  try {
    detail.value = await getProbe(id)
    showInlineHtml.value = true
  } catch (err) {
    appStore.showError(errorMessage(err, t('admin.codexTurnState.probe.loadFailed')))
  }
}

function openHtml() {
  if (!detailHtml.value || !detail.value) return
  if (!openHtmlPreviewWindow(detailHtml.value, `probe #${detail.value.id}`)) {
    appStore.showError(t('admin.codexTurnState.probe.popupBlocked'))
  }
}

async function remove(id: number) {
  try {
    await deleteProbe(id)
    results.value = results.value.filter((row) => row.id !== id)
    if (detail.value?.id === id) detail.value = null
  } catch (err) {
    appStore.showError(errorMessage(err, t('admin.codexTurnState.saveFailed')))
  }
}

async function clearAll() {
  if (!window.confirm(t('admin.codexTurnState.probe.clearConfirm'))) return
  try {
    const deleted = await deleteAllProbes()
    appStore.showSuccess(t('admin.codexTurnState.probe.cleared', { count: deleted }))
    detail.value = null
    await loadResults()
  } catch (err) {
    appStore.showError(errorMessage(err, t('admin.codexTurnState.saveFailed')))
  }
}

onMounted(() => {
  void loadResults()
})

onUnmounted(() => {
  unmounted = true
  clearTimeout(pollTimer)
})
</script>
