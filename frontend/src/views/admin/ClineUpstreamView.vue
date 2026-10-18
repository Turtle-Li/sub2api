<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Header -->
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">
            {{ t('admin.clineUpstream.title') }}
          </h1>
          <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.clineUpstream.description') }}
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="loadingState || !selectedAccountId"
            data-testid="cline-upstream-refresh"
            @click="loadState"
          >
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': loadingState }" />
            <span>{{ t('admin.clineUpstream.refresh') }}</span>
          </button>
          <button
            type="button"
            class="btn btn-primary"
            :disabled="probing || !state?.supported"
            data-testid="cline-upstream-probe-all"
            @click="probeAll"
          >
            <Icon name="beaker" size="sm" />
            <span>{{ probing ? t('admin.clineUpstream.probing') : t('admin.clineUpstream.probeAll') }}</span>
          </button>
        </div>
      </div>

      <p class="rounded-xl border border-blue-100 bg-blue-50/60 p-3 text-xs leading-relaxed text-blue-800 dark:border-blue-900/40 dark:bg-blue-950/30 dark:text-blue-200">
        {{ t('admin.clineUpstream.pipelineHint') }}
      </p>
      <p class="text-xs text-gray-500 dark:text-gray-400">
        {{ t('admin.clineUpstream.probeCostHint') }}
      </p>

      <!-- Account picker -->
      <div class="card flex flex-wrap items-center gap-3 p-4 dark:bg-dark-800">
        <label class="text-sm font-medium text-gray-700 dark:text-gray-300" for="cline-upstream-account">
          {{ t('admin.clineUpstream.accountLabel') }}
        </label>
        <select
          id="cline-upstream-account"
          v-model="selectedAccountId"
          class="input min-w-56"
          data-testid="cline-upstream-account"
          :disabled="loadingAccounts || accounts.length === 0"
        >
          <option :value="null" disabled>{{ t('admin.clineUpstream.selectAccount') }}</option>
          <option v-for="account in accounts" :key="account.id" :value="account.id">
            {{ account.name }}
          </option>
        </select>
        <span v-if="state" class="text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.clineUpstream.configuredCount', { count: configuredCount }) }}
          · {{ t('admin.clineUpstream.lastFetched', { time: fetchedAtLabel }) }}
        </span>
      </div>

      <!-- States -->
      <div v-if="loadingAccounts" class="flex min-h-32 items-center justify-center text-sm text-gray-500">
        <Icon name="refresh" size="md" class="mr-2 animate-spin text-primary-500" />
        {{ t('admin.clineUpstream.loadingAccounts') }}
      </div>

      <div
        v-else-if="accounts.length === 0"
        class="card flex min-h-40 flex-col items-center justify-center border-dashed border-gray-300 p-8 text-center dark:border-dark-600 dark:bg-dark-800"
        data-testid="cline-upstream-no-accounts"
      >
        <Icon name="server" size="xl" class="text-gray-400" />
        <p class="mt-3 font-medium text-gray-800 dark:text-gray-200">
          {{ t('admin.clineUpstream.noAccounts') }}
        </p>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.clineUpstream.noAccountsHint') }}
        </p>
      </div>

      <template v-else>
        <div
          v-if="loadError"
          role="alert"
          class="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-950/30 dark:text-red-300"
          data-testid="cline-upstream-error"
        >
          {{ loadError }}
        </div>

        <div
          v-if="state && !state.supported"
          class="rounded-xl border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-900/40 dark:bg-amber-950/30 dark:text-amber-200"
          data-testid="cline-upstream-unsupported"
        >
          <p class="font-medium">{{ t('admin.clineUpstream.unsupported') }}</p>
          <p class="mt-1 text-xs">{{ t('admin.clineUpstream.unsupportedHint') }}</p>
        </div>

        <div
          v-if="state?.models_error"
          class="rounded-xl border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800 dark:border-amber-900/40 dark:bg-amber-950/30 dark:text-amber-200"
        >
          {{ t('admin.clineUpstream.modelsError', { error: state.models_error }) }}
        </div>

        <div
          v-if="state && state.models.length === 0"
          class="card flex min-h-40 flex-col items-center justify-center border-dashed border-gray-300 p-8 text-center dark:border-dark-600 dark:bg-dark-800"
          data-testid="cline-upstream-empty"
        >
          <Icon name="cpu" size="xl" class="text-gray-400" />
          <p class="mt-3 font-medium text-gray-800 dark:text-gray-200">
            {{ t('admin.clineUpstream.emptyModels') }}
          </p>
          <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.clineUpstream.emptyModelsHint') }}
          </p>
        </div>

        <div v-else-if="state" class="card overflow-hidden dark:bg-dark-800">
          <div class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700" data-testid="cline-upstream-table">
              <thead class="bg-gray-50 dark:bg-dark-900/40">
                <tr>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.clineUpstream.columns.model') }}
                  </th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.clineUpstream.columns.source') }}
                  </th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.clineUpstream.columns.pipeline') }}
                  </th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.clineUpstream.columns.upstreams') }}
                  </th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.clineUpstream.columns.lastProvider') }}
                  </th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.clineUpstream.columns.mode') }}
                  </th>
                  <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.clineUpstream.columns.actions') }}
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="item in state.models" :key="item.model" :data-testid="`cline-upstream-row-${item.model}`">
                  <td class="px-4 py-3">
                    <div class="font-mono text-xs font-medium text-gray-900 dark:text-white">{{ item.model }}</div>
                    <div v-if="item.meta.canonical_slug && item.meta.canonical_slug !== item.model" class="mt-0.5 text-[11px] text-gray-400">
                      {{ item.meta.canonical_slug }}
                    </div>
                  </td>
                  <td class="px-4 py-3">
                    <span class="badge badge-gray">{{ sourceLabel(item.source) }}</span>
                  </td>
                  <td class="px-4 py-3">
                    <span :class="pipelineClass(item.meta.pipeline)">
                      {{ pipelineLabel(item.meta.pipeline) }}
                    </span>
                  </td>
                  <td class="px-4 py-3">
                    <div v-if="item.meta.upstreams && item.meta.upstreams.length" class="flex flex-wrap gap-1">
                      <span
                        v-for="upstream in item.meta.upstreams"
                        :key="upstream"
                        class="badge"
                        :class="statusClass(item.meta.statuses?.[upstream]?.status)"
                        :title="item.meta.statuses?.[upstream]?.note || ''"
                      >
                        {{ upstream }}
                      </span>
                    </div>
                    <span v-else class="text-xs text-gray-400">{{ t('admin.clineUpstream.noUpstreams') }}</span>
                  </td>
                  <td class="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                    {{ item.meta.last_provider || '—' }}
                  </td>
                  <td class="px-4 py-3">
                    <span :class="modeClass(item.pin.mode)">{{ modeLabel(item.pin.mode) }}</span>
                    <div v-if="item.candidates.length" class="mt-1 font-mono text-[11px] text-gray-400">
                      {{ item.candidates.join(' > ') }}
                    </div>
                  </td>
                  <td class="px-4 py-3">
                    <div class="flex flex-wrap items-center justify-end gap-2">
                      <button
                        type="button"
                        class="btn btn-secondary btn-sm"
                        :disabled="!state.supported || busyModel === item.model"
                        :title="t('admin.clineUpstream.actions.probeModel')"
                        @click="probeModel(item.model)"
                      >
                        <Icon name="beaker" size="xs" />
                        <span>{{ t('admin.clineUpstream.actions.probe') }}</span>
                      </button>
                      <button
                        type="button"
                        class="btn btn-secondary btn-sm"
                        :disabled="!state.supported || busyModel === item.model || !(item.meta.upstreams && item.meta.upstreams.length)"
                        :title="t('admin.clineUpstream.actions.validateModel')"
                        @click="validateModel(item.model)"
                      >
                        <Icon name="check" size="xs" />
                        <span>{{ t('admin.clineUpstream.actions.validate') }}</span>
                      </button>
                      <button
                        type="button"
                        class="btn btn-primary btn-sm"
                        @click="openDialog(item)"
                      >
                        <Icon name="edit" size="xs" />
                        <span>{{ t('admin.clineUpstream.actions.configure') }}</span>
                      </button>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </template>
    </div>

    <!-- Per-model pin dialog -->
    <BaseDialog
      :show="editingModel !== null"
      :title="dialogTitle"
      width="extra-wide"
      mobile-sheet
      @close="closeDialog"
    >
      <div class="space-y-5">
        <!-- Mode -->
        <div>
          <div class="text-sm font-medium text-gray-800 dark:text-gray-200">
            {{ t('admin.clineUpstream.dialog.mode') }}
          </div>
          <div class="mt-2 flex flex-wrap gap-2">
            <button
              v-for="mode in pinModes"
              :key="mode"
              type="button"
              class="btn btn-sm"
              :class="draft.mode === mode ? 'btn-primary' : 'btn-secondary'"
              :data-testid="`cline-upstream-mode-${mode}`"
              @click="draft.mode = mode"
            >
              {{ modeLabel(mode) }}
            </button>
          </div>
          <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ modeHint(draft.mode) }}</p>
        </div>

        <!-- Upstreams -->
        <div>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="text-sm font-medium text-gray-800 dark:text-gray-200">
              {{ t('admin.clineUpstream.dialog.upstreams') }}
            </div>
            <span class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.clineUpstream.dialog.selected', { count: draft.upstreams.length }) }}
            </span>
          </div>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.clineUpstream.dialog.upstreamsHint') }}
          </p>

          <div v-if="knownUpstreams.length === 0" class="mt-3 rounded-xl border border-dashed border-gray-300 p-4 text-center text-xs text-gray-500 dark:border-dark-600">
            {{ t('admin.clineUpstream.dialog.knownUpstreamsEmpty') }}
          </div>

          <div v-else class="mt-3 space-y-2">
            <div
              v-for="upstream in knownUpstreams"
              :key="upstream"
              class="flex flex-wrap items-center gap-3 rounded-xl border border-gray-200 px-3 py-2 dark:border-dark-700"
              :data-testid="`cline-upstream-option-${upstream}`"
            >
              <label class="flex items-center gap-2 text-sm text-gray-800 dark:text-gray-200">
                <input
                  type="checkbox"
                  class="h-4 w-4"
                  :checked="draft.upstreams.includes(upstream)"
                  @change="toggleUpstream(upstream)"
                />
                <span class="font-mono text-xs">{{ upstream }}</span>
              </label>
              <span class="badge" :class="statusClass(metaStatus(upstream))">
                {{ statusLabel(metaStatus(upstream)) }}
              </span>
              <label class="flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
                <input
                  type="checkbox"
                  class="h-4 w-4"
                  :checked="draft.exclude.includes(upstream)"
                  @change="toggleExclude(upstream)"
                />
                <span>{{ t('admin.clineUpstream.dialog.exclude') }}</span>
              </label>
              <div v-if="draft.upstreams.includes(upstream)" class="ml-auto flex items-center gap-1">
                <span class="font-mono text-[11px] text-gray-400">
                  #{{ draft.upstreams.indexOf(upstream) + 1 }}
                </span>
                <button
                  type="button"
                  class="rounded p-1 text-gray-400 hover:text-gray-700 disabled:opacity-30 dark:hover:text-gray-200"
                  :disabled="draft.upstreams.indexOf(upstream) === 0"
                  :title="t('admin.clineUpstream.dialog.upstreamMoveUp')"
                  @click="moveUpstream(upstream, -1)"
                >
                  <Icon name="arrowUp" size="xs" />
                </button>
                <button
                  type="button"
                  class="rounded p-1 text-gray-400 hover:text-gray-700 disabled:opacity-30 dark:hover:text-gray-200"
                  :disabled="draft.upstreams.indexOf(upstream) === draft.upstreams.length - 1"
                  :title="t('admin.clineUpstream.dialog.upstreamMoveDown')"
                  @click="moveUpstream(upstream, 1)"
                >
                  <Icon name="arrowDown" size="xs" />
                </button>
              </div>
            </div>
          </div>

          <p v-if="!meta?.upstreams?.length" class="mt-2 text-xs text-amber-600 dark:text-amber-400">
            {{ t('admin.clineUpstream.dialog.needProbe') }}
          </p>
          <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.clineUpstream.dialog.excludeHint') }}
          </p>
        </div>

        <!-- Sort -->
        <div>
          <div class="text-sm font-medium text-gray-800 dark:text-gray-200">
            {{ t('admin.clineUpstream.dialog.sort') }}
          </div>
          <select v-model="draft.sort" class="input mt-2" data-testid="cline-upstream-sort">
            <option value="">{{ t('admin.clineUpstream.dialog.sortDefault') }}</option>
            <option value="cost">{{ t('admin.clineUpstream.dialog.sortCost') }}</option>
            <option value="ttft">{{ t('admin.clineUpstream.dialog.sortTtft') }}</option>
            <option value="tps">{{ t('admin.clineUpstream.dialog.sortTps') }}</option>
          </select>
        </div>

        <!-- Test -->
        <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="text-sm font-medium text-gray-800 dark:text-gray-200">
              {{ t('admin.clineUpstream.dialog.testResultTitle') }}
            </div>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="testing || !state?.supported"
              data-testid="cline-upstream-test"
              @click="runTest"
            >
              <Icon name="play" size="xs" />
              <span>{{ testing ? t('admin.clineUpstream.dialog.testing') : t('admin.clineUpstream.dialog.testWithDraft') }}</span>
            </button>
          </div>

          <div v-if="testResult" class="mt-3 space-y-1 text-xs" data-testid="cline-upstream-test-result">
            <p v-if="!testResult.ok" class="text-red-600 dark:text-red-400">
              {{ t('admin.clineUpstream.dialog.testFailed') }}: {{ testResult.error }}
            </p>
            <template v-else>
              <p class="text-gray-700 dark:text-gray-300">
                <span class="font-medium">{{ t('admin.clineUpstream.dialog.testActual') }}:</span>
                <span class="ml-1 font-mono">{{ testResult.actual_provider || '—' }}</span>
                <span class="ml-2 text-gray-400">{{ t('admin.clineUpstream.dialog.testMs', { ms: testResult.ms }) }}</span>
              </p>
              <p class="text-gray-700 dark:text-gray-300">
                <span class="font-medium">{{ t('admin.clineUpstream.dialog.testCandidates') }}:</span>
                <span class="ml-1 font-mono">{{ (testResult.candidates || []).join(' > ') || '—' }}</span>
              </p>
              <p v-if="testResult.content" class="text-gray-500 dark:text-gray-400">
                {{ t('admin.clineUpstream.dialog.testContent') }}: {{ testResult.content }}
              </p>
            </template>
          </div>
        </div>
      </div>

      <template #footer>
        <button type="button" class="btn btn-secondary" @click="closeDialog">
          {{ t('admin.clineUpstream.actions.cancel') }}
        </button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="saving"
          data-testid="cline-upstream-save"
          @click="savePins"
        >
          <Icon name="check" size="sm" />
          <span>{{ saving ? t('admin.clineUpstream.actions.saving') : t('admin.clineUpstream.actions.save') }}</span>
        </button>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { adminAPI } from '@/api/admin'
import {
  getClineUpstreams,
  probeClineUpstreams,
  testClineUpstreams,
  updateClineUpstreams,
  validateClineUpstreams,
  type ClinePinMode,
  type ClineUpstreamModelState,
  type ClineUpstreamPin,
  type ClineUpstreamState,
  type ClineUpstreamTestResult
} from '@/api/admin/clineUpstream'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import type { AccountListItem } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()
const route = useRoute()

const pinModes: ClinePinMode[] = ['off', 'strict', 'preferred']

const accounts = ref<AccountListItem[]>([])
const selectedAccountId = ref<number | null>(null)
const state = ref<ClineUpstreamState | null>(null)
const loadingAccounts = ref(false)
const loadingState = ref(false)
const loadError = ref('')
const probing = ref(false)
const busyModel = ref('')
const saving = ref(false)

const editingModel = ref<string | null>(null)
const draft = reactive<ClineUpstreamPin>({
  mode: 'off',
  upstreams: [],
  exclude: [],
  sort: ''
})
const testing = ref(false)
const testResult = ref<ClineUpstreamTestResult | null>(null)

const configuredCount = computed(() => asList(state.value?.models).filter((item) => item.configured).length)
const fetchedAtLabel = computed(() => {
  const seconds = state.value?.fetched_at || 0
  return seconds ? formatDateTime(new Date(seconds * 1000)) : '—'
})

const editingEntry = computed<ClineUpstreamModelState | null>(
  () => asList(state.value?.models).find((item) => item.model === editingModel.value) || null
)
const meta = computed(() => editingEntry.value?.meta || null)
const dialogTitle = computed(() =>
  t('admin.clineUpstream.dialog.title', { model: editingModel.value || '' })
)
const knownUpstreams = computed(() => {
  const seen = new Set<string>()
  const out: string[] = []
  const push = (value: string) => {
    const trimmed = (value || '').trim()
    if (!trimmed || seen.has(trimmed)) return
    seen.add(trimmed)
    out.push(trimmed)
  }
  ;(meta.value?.upstreams || []).forEach(push)
  draft.upstreams.forEach(push)
  draft.exclude.forEach(push)
  return out
})

function errorMessage(error: unknown, fallback: string): string {
  const candidate = error as { response?: { data?: { message?: string; detail?: string } }; message?: string }
  return candidate?.response?.data?.message || candidate?.response?.data?.detail || candidate?.message || fallback
}

/**
 * 后端列表字段在 Go 里是 nil slice 时会序列化成 null，模板里按数组使用会抛
 * "Cannot read properties of null (reading 'length')"。这里在入口统一补齐，
 * 保证视图层拿到的永远是数组/对象。
 */
function asList<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : []
}

function normalizeState(raw: ClineUpstreamState | null | undefined): ClineUpstreamState | null {
  if (!raw) return null
  return {
    ...raw,
    models: asList(raw.models).map((item) => ({
      ...item,
      candidates: asList(item.candidates),
      pin: {
        mode: item.pin?.mode || 'off',
        upstreams: asList(item.pin?.upstreams),
        exclude: asList(item.pin?.exclude),
        sort: item.pin?.sort || ''
      },
      meta: {
        ...item.meta,
        upstreams: asList(item.meta?.upstreams),
        statuses: item.meta?.statuses || {}
      }
    }))
  }
}

function normalizeTestResult(raw: ClineUpstreamTestResult | null): ClineUpstreamTestResult | null {
  if (!raw) return null
  return {
    ...raw,
    targets: asList(raw.targets),
    exclude: asList(raw.exclude),
    candidates: asList(raw.candidates)
  }
}

function sourceLabel(source: string): string {
  if (source === 'subscription') return t('admin.clineUpstream.source.subscription')
  if (source === 'catalog') return t('admin.clineUpstream.source.catalog')
  return t('admin.clineUpstream.source.configured')
}

function pipelineLabel(pipeline: string): string {
  if (pipeline === 'direct') return t('admin.clineUpstream.pipeline.direct')
  if (pipeline === 'planner') return t('admin.clineUpstream.pipeline.planner')
  return t('admin.clineUpstream.pipeline.unknown')
}

function pipelineClass(pipeline: string): string {
  const base = 'badge'
  if (pipeline === 'direct') return `${base} badge-primary`
  if (pipeline === 'planner') return `${base} badge-purple`
  return `${base} badge-gray`
}

function modeLabel(mode: string): string {
  if (mode === 'strict') return t('admin.clineUpstream.mode.strict')
  if (mode === 'preferred') return t('admin.clineUpstream.mode.preferred')
  return t('admin.clineUpstream.mode.off')
}

function modeClass(mode: string): string {
  const base = 'badge'
  if (mode === 'strict') return `${base} badge-success`
  if (mode === 'preferred') return `${base} badge-primary`
  return `${base} badge-gray`
}

function modeHint(mode: string): string {
  if (mode === 'strict') return t('admin.clineUpstream.dialog.modeHintStrict')
  if (mode === 'preferred') return t('admin.clineUpstream.dialog.modeHintPreferred')
  return t('admin.clineUpstream.dialog.modeHintOff')
}

function statusLabel(status?: string): string {
  switch (status) {
    case 'ok':
      return t('admin.clineUpstream.status.ok')
    case 'limited':
      return t('admin.clineUpstream.status.limited')
    case 'bad':
      return t('admin.clineUpstream.status.bad')
    case 'auth':
      return t('admin.clineUpstream.status.auth')
    default:
      return t('admin.clineUpstream.status.unknown')
  }
}

function statusClass(status?: string): string {
  const base = 'badge'
  switch (status) {
    case 'ok':
      return `${base} badge-success`
    case 'limited':
      return `${base} badge-warning`
    case 'bad':
      return `${base} badge-danger`
    case 'auth':
      return `${base} badge-danger`
    default:
      return `${base} badge-gray`
  }
}

function metaStatus(upstream: string): string | undefined {
  return meta.value?.statuses?.[upstream]?.status
}

async function loadAccounts() {
  loadingAccounts.value = true
  try {
    const response = await adminAPI.accounts.list(1, 200, { platform: 'cline' })
    accounts.value = response.items || []
    // 支持从账号列表的「Cline 上游钉住」入口带 ?account=<id> 直达。
    const requested = Number(route.query.account)
    if (Number.isFinite(requested) && accounts.value.some((item) => item.id === requested)) {
      selectedAccountId.value = requested
    }
    if (accounts.value.length && selectedAccountId.value === null) {
      selectedAccountId.value = accounts.value[0].id
    }
  } catch (error) {
    loadError.value = errorMessage(error, t('admin.clineUpstream.messages.loadFailed'))
  } finally {
    loadingAccounts.value = false
  }
}

async function loadState() {
  if (!selectedAccountId.value) return
  loadingState.value = true
  loadError.value = ''
  try {
    state.value = normalizeState(await getClineUpstreams(selectedAccountId.value))
  } catch (error) {
    state.value = null
    loadError.value = errorMessage(error, t('admin.clineUpstream.messages.loadFailed'))
    appStore.showError(loadError.value)
  } finally {
    loadingState.value = false
  }
}

async function probeAll() {
  if (!selectedAccountId.value) return
  probing.value = true
  try {
    const response = await probeClineUpstreams(selectedAccountId.value)
    appStore.showSuccess(t('admin.clineUpstream.messages.probed', { count: asList(response?.results).length }))
    await loadState()
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.clineUpstream.messages.probeFailed')))
  } finally {
    probing.value = false
  }
}

async function probeModel(model: string) {
  if (!selectedAccountId.value) return
  busyModel.value = model
  try {
    await probeClineUpstreams(selectedAccountId.value, [model])
    appStore.showSuccess(t('admin.clineUpstream.messages.probed', { count: 1 }))
    await loadState()
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.clineUpstream.messages.probeFailed')))
  } finally {
    busyModel.value = ''
  }
}

async function validateModel(model: string) {
  if (!selectedAccountId.value) return
  busyModel.value = model
  try {
    const result = await validateClineUpstreams(selectedAccountId.value, model)
    appStore.showSuccess(
      t('admin.clineUpstream.messages.validated', { count: Object.keys(result?.statuses || {}).length })
    )
    await loadState()
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.clineUpstream.messages.validateFailed')))
  } finally {
    busyModel.value = ''
  }
}

function openDialog(item: ClineUpstreamModelState) {
  editingModel.value = item.model
  draft.mode = item.pin?.mode || 'off'
  draft.upstreams = [...asList(item.pin?.upstreams)]
  draft.exclude = [...asList(item.pin?.exclude)]
  draft.sort = item.pin.sort || ''
  testResult.value = null
}

function closeDialog() {
  editingModel.value = null
  testResult.value = null
}

function toggleUpstream(upstream: string) {
  if (draft.upstreams.includes(upstream)) {
    draft.upstreams = draft.upstreams.filter((value) => value !== upstream)
    return
  }
  draft.upstreams = [...draft.upstreams, upstream]
  draft.exclude = draft.exclude.filter((value) => value !== upstream)
}

function toggleExclude(upstream: string) {
  if (draft.exclude.includes(upstream)) {
    draft.exclude = draft.exclude.filter((value) => value !== upstream)
    return
  }
  draft.exclude = [...draft.exclude, upstream]
  draft.upstreams = draft.upstreams.filter((value) => value !== upstream)
}

function moveUpstream(upstream: string, delta: number) {
  const index = draft.upstreams.indexOf(upstream)
  const next = index + delta
  if (index < 0 || next < 0 || next >= draft.upstreams.length) return
  const list = [...draft.upstreams]
  list.splice(index, 1)
  list.splice(next, 0, upstream)
  draft.upstreams = list
}

async function runTest() {
  if (!selectedAccountId.value || !editingModel.value) return
  testing.value = true
  testResult.value = null
  try {
    testResult.value = normalizeTestResult(
      await testClineUpstreams(selectedAccountId.value, {
        model: editingModel.value,
        mode: draft.mode,
        upstreams: [...draft.upstreams],
        exclude: [...draft.exclude],
        sort: draft.sort
      })
    )
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.clineUpstream.messages.probeFailed')))
  } finally {
    testing.value = false
  }
}

async function savePins() {
  if (!selectedAccountId.value || !state.value || !editingModel.value) return
  saving.value = true
  try {
    const pins: Record<string, ClineUpstreamPin> = {}
    for (const item of asList(state.value.models)) {
      const source = item.model === editingModel.value ? { ...draft } : item.pin
      const isEmpty =
        source.mode === 'off' && source.upstreams.length === 0 && source.exclude.length === 0 && !source.sort
      if (isEmpty) continue
      pins[item.model] = {
        mode: source.mode,
        upstreams: [...source.upstreams],
        exclude: [...source.exclude],
        sort: source.sort
      }
    }
    state.value = normalizeState(await updateClineUpstreams(selectedAccountId.value, pins))
    appStore.showSuccess(t('admin.clineUpstream.messages.saved'))
    closeDialog()
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.clineUpstream.messages.saveFailed')))
  } finally {
    saving.value = false
  }
}

watch(selectedAccountId, () => {
  state.value = null
  void loadState()
})

onMounted(async () => {
  await loadAccounts()
  if (selectedAccountId.value) {
    await loadState()
  }
})
</script>
