<template>
  <BaseDialog
    :show="show"
    :title="pool ? pool.name : t('admin.accounts.pools.title')"
    width="full"
    :close-on-escape="!childDialogOpen"
    :close-on-click-outside="!childDialogOpen"
    @close="$emit('close')"
  >
    <div v-if="pool" class="space-y-4" data-testid="account-pool-modal">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex min-w-0 items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
          <PlatformIcon :platform="pool.platform as GroupPlatform" />
          <span>{{ platformLabel(pool.platform) }}</span>
          <span v-if="pool.notes" class="truncate" :title="pool.notes">· {{ pool.notes }}</span>
        </div>
        <div class="flex flex-wrap gap-2">
          <button class="btn btn-secondary btn-sm" @click="refreshAll">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          </button>
          <button class="btn btn-secondary btn-sm" @click="$emit('edit-pool', pool)">{{ t('admin.accounts.pools.edit') }}</button>
          <button class="btn btn-primary btn-sm" :disabled="pool.stats.total === 0" @click="openBulkEdit('pool')">
            {{ t('admin.accounts.pools.editAllMembers') }}
          </button>
          <button
            v-if="pool.stats.error > 0"
            class="btn btn-warning btn-sm"
            data-testid="account-pool-purge-error"
            @click="showPurgeErrorDialog = true"
          >
            {{ t('admin.accounts.pools.purgeError', { count: pool.stats.error }) }}
          </button>
          <button class="btn btn-danger btn-sm" @click="openDissolve">{{ t('admin.accounts.pools.dissolve') }}</button>
        </div>
      </div>

      <!-- Stat cards double as status filters. -->
      <div class="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-7">
        <button
          type="button"
          :class="statCardClass(filters.status === '')"
          @click="setStatusFilter('')"
        >
          <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.pools.stats.total') }}</span>
          <span class="text-lg font-semibold text-gray-900 dark:text-white">{{ pool.stats.total }}</span>
        </button>
        <button
          v-for="bucket in poolStatusBuckets(pool.stats)"
          :key="bucket.key"
          type="button"
          :class="statCardClass(filters.status === bucket.filter)"
          :data-testid="`account-pool-stat-${bucket.key}`"
          @click="setStatusFilter(bucket.filter)"
        >
          <span class="text-xs text-gray-500 dark:text-gray-400">{{ t(bucket.labelKey) }}</span>
          <span :class="['text-lg font-semibold', bucket.count > 0 ? bucket.textClass : 'text-gray-300 dark:text-dark-600']">{{ bucket.count }}</span>
        </button>
      </div>

      <div
        v-if="pool.usage || pool.stats.codex || pool.stats.expired > 0"
        class="flex flex-wrap gap-x-6 gap-y-1 rounded-lg bg-gray-50 px-3 py-2 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300"
      >
        <span v-if="pool.usage">
          {{ t('admin.accounts.pools.usageWindow', { hours: pool.usage.window_hours }) }}:
          {{ t('admin.accounts.pools.requests', { count: formatCompactNumber(pool.usage.requests) }) }}
          · {{ formatCompactNumber(pool.usage.tokens) }} tokens · ${{ pool.usage.cost.toFixed(2) }}
        </span>
        <span v-if="pool.usage?.grok_free" :title="t('admin.accounts.pools.grokFreeHint')">
          {{ t('admin.accounts.pools.grokFreeQuota') }}:
          {{ formatCompactNumber(pool.usage.grok_free.used_tokens) }} / {{ formatCompactNumber(pool.usage.grok_free.limit_tokens) }}
          <template v-if="pool.usage.grok_free.near_limit > 0">
            · <span class="text-amber-600">{{ t('admin.accounts.pools.nearLimit', { count: pool.usage.grok_free.near_limit }) }}</span>
          </template>
        </span>
        <span v-if="pool.stats.codex">
          {{ t('admin.accounts.pools.codexQuota') }}:
          5h {{ formatPercent(pool.stats.codex.avg_5h_used_percent) }} · 7d {{ formatPercent(pool.stats.codex.avg_7d_used_percent) }}
          <template v-if="pool.stats.codex.exhausted > 0">
            · <span class="text-red-500">{{ t('admin.accounts.pools.exhausted', { count: pool.stats.codex.exhausted }) }}</span>
          </template>
        </span>
        <span v-if="pool.stats.expired > 0">{{ t('admin.accounts.pools.stats.expired') }}: {{ pool.stats.expired }}</span>
        <span v-if="pool.usage" class="text-gray-400">{{ t('admin.accounts.pools.usageUpdated', { time: formatRelativeTime(pool.usage.updated_at) }) }}</span>
      </div>

      <div class="flex flex-wrap items-center justify-between gap-3">
        <SearchInput
          v-model="filters.search"
          class="w-full sm:w-64"
          :placeholder="t('admin.accounts.searchAccounts')"
          @search="reloadMembers"
        />
        <div class="flex flex-wrap items-center gap-2 text-sm">
          <template v-if="selectedIds.length > 0">
            <span class="font-medium text-primary-700 dark:text-primary-300">
              {{ t('admin.accounts.bulkActions.selected', { count: selectedIds.length }) }}
            </span>
            <button class="text-xs text-primary-600 hover:underline" @click="clearSelection">{{ t('admin.accounts.bulkActions.clear') }}</button>
          </template>
          <button
            v-if="total > selectedIds.length"
            class="text-xs text-primary-600 hover:underline disabled:opacity-60"
            :disabled="selectingAll"
            @click="selectAllResults"
          >
            {{ selectingAll ? t('admin.accounts.bulkActions.selectingAll') : t('admin.accounts.bulkActions.selectAllResults', { count: total }) }}
          </button>
        </div>
      </div>

      <div v-if="selectedIds.length > 0" class="flex flex-wrap gap-2 rounded-lg bg-primary-50 p-2 dark:bg-primary-900/20">
        <button class="btn btn-success btn-sm" @click="toggleSchedulable(true)">{{ t('admin.accounts.bulkActions.enableScheduling') }}</button>
        <button class="btn btn-warning btn-sm" @click="toggleSchedulable(false)">{{ t('admin.accounts.bulkActions.disableScheduling') }}</button>
        <button class="btn btn-secondary btn-sm" @click="runBatch('reset')">{{ t('admin.accounts.bulkActions.resetStatus') }}</button>
        <button class="btn btn-secondary btn-sm" @click="runBatch('refresh')">{{ t('admin.accounts.bulkActions.refreshToken') }}</button>
        <button class="btn btn-primary btn-sm" @click="openBulkEdit('selected')">{{ t('admin.accounts.bulkActions.edit') }}</button>
        <button class="btn btn-secondary btn-sm" @click="showAssignDialog = true">{{ t('admin.accounts.pools.moveToPool') }}</button>
        <button class="btn btn-secondary btn-sm" data-testid="account-pool-release" @click="releaseSelected">{{ t('admin.accounts.pools.release') }}</button>
        <button class="btn btn-danger btn-sm" @click="runBatch('delete')">{{ t('admin.accounts.bulkActions.delete') }}</button>
      </div>

      <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-dark-800 dark:text-gray-400">
            <tr>
              <th class="w-10 px-3 py-2">
                <input
                  type="checkbox"
                  class="rounded border-gray-300 text-primary-600"
                  :checked="items.length > 0 && items.every(item => selected.has(item.id))"
                  @change="togglePage(($event.target as HTMLInputElement).checked)"
                />
              </th>
              <th class="px-3 py-2">{{ t('admin.accounts.columns.name') }}</th>
              <th class="px-3 py-2">{{ t('admin.accounts.columns.status') }}</th>
              <th class="px-3 py-2">{{ t('admin.accounts.columns.schedulable') }}</th>
              <th class="px-3 py-2">{{ t('admin.accounts.columns.groups') }}</th>
              <th class="px-3 py-2">{{ t('admin.accounts.columns.lastUsed') }}</th>
              <th class="px-3 py-2 text-right">{{ t('admin.accounts.columns.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
            <tr v-if="loading && items.length === 0">
              <td colspan="7" class="px-3 py-8 text-center text-gray-400">{{ t('common.loading') }}</td>
            </tr>
            <tr v-else-if="items.length === 0">
              <td colspan="7" class="px-3 py-8 text-center text-gray-400">{{ t('admin.accounts.pools.noMembers') }}</td>
            </tr>
            <tr v-for="row in items" :key="row.id" class="hover:bg-gray-50 dark:hover:bg-dark-800/60">
              <td class="px-3 py-2">
                <input type="checkbox" class="rounded border-gray-300 text-primary-600" :checked="selected.has(row.id)" @change="toggleRow(row.id)" />
              </td>
              <td class="max-w-[260px] px-3 py-2">
                <div class="truncate font-medium text-gray-900 dark:text-white" :title="row.name">{{ row.name }}</div>
                <div v-if="row.error_message" class="truncate text-xs text-red-500" :title="row.error_message">{{ row.error_message }}</div>
              </td>
              <td class="px-3 py-2"><AccountStatusIndicator :account="row as Account" /></td>
              <td class="px-3 py-2">
                <span :class="row.schedulable ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-400'">
                  {{ row.schedulable ? t('common.enabled') : t('common.disabled') }}
                </span>
              </td>
              <td class="px-3 py-2"><AccountGroupsCell :groups="groupsForRow(row)" :max-display="3" /></td>
              <td class="whitespace-nowrap px-3 py-2 text-xs text-gray-500 dark:text-gray-400">
                {{ row.last_used_at ? formatRelativeTime(row.last_used_at) : '-' }}
              </td>
              <td class="px-3 py-2 text-right">
                <button class="text-xs text-primary-600 hover:underline" @click="$emit('edit-account', row as Account)">{{ t('common.edit') }}</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <Pagination
        v-if="total > 0"
        :total="total"
        :page="page"
        :page-size="pageSize"
        @update:page="(value: number) => { page = value; loadMembers() }"
        @update:page-size="(value: number) => { pageSize = value; page = 1; loadMembers() }"
      />
    </div>
  </BaseDialog>

  <BulkEditAccountModal
    :show="showBulkEdit"
    :account-ids="bulkEditTarget?.mode === 'selected' ? selectedIds : []"
    :selected-platforms="pool ? [pool.platform as AccountPlatform] : []"
    :selected-types="bulkEditTypes"
    :target="bulkEditTarget ?? undefined"
    :proxies="proxies"
    :groups="groups"
    @close="showBulkEdit = false"
    @updated="handleBulkUpdated"
  />
  <AccountPoolAssignDialog
    v-if="pool"
    :show="showAssignDialog"
    :platform="pool.platform"
    :account-ids="selectedIds"
    :exclude-pool-id="pool.id"
    @close="showAssignDialog = false"
    @assigned="handleMoved"
  />
  <ConfirmDialog
    :show="showPurgeErrorDialog"
    :title="t('admin.accounts.pools.purgeErrorTitle')"
    :message="t('admin.accounts.pools.purgeErrorConfirm', { count: pool?.stats.error ?? 0 })"
    :danger="true"
    :confirm-text="t('common.delete')"
    @confirm="purgeErrorAccounts"
    @cancel="showPurgeErrorDialog = false"
  />
  <ConfirmDialog
    :show="showDissolveDialog"
    :title="t('admin.accounts.pools.dissolveTitle')"
    :message="t('admin.accounts.pools.dissolveConfirm', { name: pool?.name ?? '' })"
    :danger="true"
    :confirm-text="t('admin.accounts.pools.dissolve')"
    @confirm="dissolvePool"
    @cancel="showDissolveDialog = false"
  >
    <label class="flex items-center gap-2 text-sm text-red-600 dark:text-red-400">
      <input v-model="dissolveDeleteMembers" type="checkbox" class="h-4 w-4 rounded border-gray-300" />
      <span>{{ t('admin.accounts.pools.dissolveDeleteMembers', { count: pool?.stats.total ?? 0 }) }}</span>
    </label>
  </ConfirmDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountGroupsCell from '@/components/account/AccountGroupsCell.vue'
import { BulkEditAccountModal } from '@/components/account'
import AccountPoolAssignDialog from './AccountPoolAssignDialog.vue'
import { adminAPI } from '@/api/admin'
import type { AccountPool } from '@/api/admin/accountPools'
import type { Account, AccountListItem, AccountPlatform, AccountType, AdminGroup, GroupPlatform, Proxy as AccountProxy } from '@/types'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { fetchAllAccountIds } from '@/utils/accountSelection'
import { formatCompactNumber, formatRelativeTime } from '@/utils/format'
import { poolStatusBuckets } from './accountPoolStats'

const props = defineProps<{
  show: boolean
  poolId: number | null
  groups: AdminGroup[]
  proxies: AccountProxy[]
}>()

const emit = defineEmits<{
  close: []
  /** Pool membership, stats or member accounts changed. */
  changed: []
  dissolved: [poolId: number]
  'edit-pool': [pool: AccountPool]
  'edit-account': [account: Account]
}>()

const { t } = useI18n()
const appStore = useAppStore()

const BATCH_CHUNK_SIZE = 200

const pool = ref<AccountPool | null>(null)
const items = ref<AccountListItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const filters = reactive({ status: '', search: '' })
const selected = ref(new Set<number>())
const selectingAll = ref(false)
const showBulkEdit = ref(false)
const bulkEditTarget = ref<{
  mode: 'selected' | 'filtered'
  filters?: Record<string, unknown>
  previewCount?: number
  selectedPlatforms?: AccountPlatform[]
  selectedTypes?: AccountType[]
} | null>(null)
const showAssignDialog = ref(false)
const showPurgeErrorDialog = ref(false)
const showDissolveDialog = ref(false)
const dissolveDeleteMembers = ref(false)
let loadVersion = 0

const selectedIds = computed(() => Array.from(selected.value))
const childDialogOpen = computed(() =>
  showBulkEdit.value || showAssignDialog.value || showPurgeErrorDialog.value || showDissolveDialog.value
)
const groupsByID = computed(() => new Map(props.groups.map(group => [group.id, group])))
const groupsForRow = (row: AccountListItem): AdminGroup[] =>
  (row.group_ids ?? []).map(id => groupsByID.value.get(id)).filter((group): group is AdminGroup => Boolean(group))
const bulkEditTypes = computed<AccountType[]>(() => Array.from(new Set(items.value.map(item => item.type))))

const platformLabel = (platform: string) =>
  CONCRETE_PLATFORM_OPTIONS.find(option => option.value === platform)?.label ?? platform
const formatPercent = (value?: number | null) => (value == null ? '-' : `${Math.round(value)}%`)
const statCardClass = (active: boolean) => [
  'flex flex-col items-start rounded-lg border px-3 py-2 text-left transition-colors',
  active
    ? 'border-primary-400 bg-primary-50 dark:border-primary-600 dark:bg-primary-900/20'
    : 'border-gray-200 hover:border-primary-300 dark:border-dark-700 dark:hover:border-primary-700'
]

const memberFilters = () => ({
  pool: props.poolId != null ? String(props.poolId) : '',
  status: filters.status,
  search: filters.search.trim(),
  lite: '1',
  sort_by: 'name',
  sort_order: 'asc' as const
})

const loadPool = async () => {
  if (props.poolId == null) return
  pool.value = await adminAPI.accountPools.get(props.poolId)
}

const loadMembers = async () => {
  if (props.poolId == null) return
  const version = ++loadVersion
  loading.value = true
  try {
    const result = await adminAPI.accounts.list(page.value, pageSize.value, memberFilters())
    if (version !== loadVersion) return
    items.value = result.items ?? []
    total.value = result.total ?? 0
  } catch (error) {
    if (version === loadVersion) appStore.showError(extractApiErrorMessage(error, t('admin.accounts.pools.loadFailed')))
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

const reloadMembers = () => {
  page.value = 1
  clearSelection()
  return loadMembers()
}

const refreshAll = async () => {
  try {
    await Promise.all([loadPool(), loadMembers()])
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.pools.loadFailed')))
  }
}

/** Refresh after a mutation and let the parent refresh its strip / list. */
const afterMutation = async () => {
  emit('changed')
  await refreshAll()
}

watch(
  () => [props.show, props.poolId] as const,
  ([show]) => {
    if (!show) return
    pool.value = null
    items.value = []
    total.value = 0
    filters.status = ''
    filters.search = ''
    page.value = 1
    clearSelection()
    refreshAll()
  },
  { immediate: true }
)

const setStatusFilter = (status: string) => {
  filters.status = status
  reloadMembers()
}

function clearSelection() {
  selected.value = new Set()
}
const toggleRow = (id: number) => {
  const next = new Set(selected.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selected.value = next
}
const togglePage = (checked: boolean) => {
  const next = new Set(selected.value)
  items.value.forEach(item => (checked ? next.add(item.id) : next.delete(item.id)))
  selected.value = next
}
const selectAllResults = async () => {
  selectingAll.value = true
  try {
    const ids = await fetchAllAccountIds(
      (p, size, requestFilters) => adminAPI.accounts.list(p, size, requestFilters),
      memberFilters()
    )
    selected.value = new Set(ids)
  } catch (error) {
    appStore.showError(t('admin.accounts.bulkActions.selectAllFailed'))
  } finally {
    selectingAll.value = false
  }
}

const chunked = (ids: number[]) => {
  const chunks: number[][] = []
  for (let i = 0; i < ids.length; i += BATCH_CHUNK_SIZE) chunks.push(ids.slice(i, i + BATCH_CHUNK_SIZE))
  return chunks
}

/** Runs a batch endpoint in bounded chunks and aggregates success/failure. */
const runChunked = async (ids: number[], fn: (chunk: number[]) => Promise<{ success: number; failed: number }>) => {
  let success = 0
  let failed = 0
  for (const chunk of chunked(ids)) {
    const result = await fn(chunk)
    success += result.success
    failed += result.failed
  }
  return { success, failed }
}

const reportBatch = (result: { success: number; failed: number }, successKey: string) => {
  if (result.failed > 0) {
    appStore.showError(t('admin.accounts.bulkActions.partialSuccess', result))
  } else {
    appStore.showSuccess(t(successKey, { count: result.success }))
  }
}

const runBatch = async (action: 'reset' | 'refresh' | 'delete') => {
  const ids = selectedIds.value
  if (ids.length === 0) return
  const confirmMessage = action === 'delete'
    ? t('admin.accounts.bulkActions.confirmDelete', { count: ids.length })
    : t('common.confirm')
  if (!confirm(confirmMessage)) return
  try {
    if (action === 'reset') {
      reportBatch(await runChunked(ids, adminAPI.accounts.batchClearError), 'admin.accounts.bulkActions.resetStatusSuccess')
    } else if (action === 'refresh') {
      reportBatch(await runChunked(ids, adminAPI.accounts.batchRefresh), 'admin.accounts.bulkActions.refreshTokenSuccess')
    } else {
      reportBatch(await runChunked(ids, adminAPI.accounts.batchDelete), 'admin.accounts.bulkActions.deleteSuccess')
    }
    clearSelection()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  }
  await afterMutation()
}

const toggleSchedulable = async (schedulable: boolean) => {
  const ids = selectedIds.value
  if (ids.length === 0) return
  try {
    const result = await runChunked(ids, chunk => adminAPI.accounts.bulkUpdate(chunk, { schedulable }))
    reportBatch(
      result,
      schedulable ? 'admin.accounts.bulkSchedulableEnabled' : 'admin.accounts.bulkSchedulableDisabled'
    )
    clearSelection()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  }
  await afterMutation()
}

const releaseSelected = async () => {
  const ids = selectedIds.value
  if (ids.length === 0 || !confirm(t('admin.accounts.pools.releaseConfirm', { count: ids.length }))) return
  try {
    const result = await adminAPI.accountPools.removeMembers(ids)
    appStore.showSuccess(t('admin.accounts.pools.releaseSuccess', { count: result.affected }))
    clearSelection()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  }
  await afterMutation()
}

const handleMoved = async () => {
  showAssignDialog.value = false
  clearSelection()
  await afterMutation()
}

const openBulkEdit = (mode: 'selected' | 'pool') => {
  if (!pool.value) return
  const platforms = [pool.value.platform as AccountPlatform]
  bulkEditTarget.value = mode === 'selected'
    ? { mode: 'selected', selectedPlatforms: platforms, selectedTypes: bulkEditTypes.value }
    : {
        mode: 'filtered',
        // Pool-level edits are plain bulk updates over every member account.
        filters: { pool: String(pool.value.id) },
        previewCount: pool.value.stats.total,
        selectedPlatforms: platforms,
        selectedTypes: bulkEditTypes.value
      }
  showBulkEdit.value = true
}

const handleBulkUpdated = async () => {
  showBulkEdit.value = false
  bulkEditTarget.value = null
  clearSelection()
  await afterMutation()
}

const purgeErrorAccounts = async () => {
  showPurgeErrorDialog.value = false
  if (!pool.value) return
  try {
    const ids = await adminAPI.accountPools.listAccountIds(pool.value.id, 'error')
    if (ids.length > 0) {
      reportBatch(await runChunked(ids, adminAPI.accounts.batchDelete), 'admin.accounts.bulkActions.deleteSuccess')
    }
    clearSelection()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  }
  await afterMutation()
}

const openDissolve = () => {
  dissolveDeleteMembers.value = false
  showDissolveDialog.value = true
}

const dissolvePool = async () => {
  showDissolveDialog.value = false
  const target = pool.value
  if (!target) return
  try {
    if (dissolveDeleteMembers.value) {
      const ids = await adminAPI.accountPools.listAccountIds(target.id)
      const result = await runChunked(ids, adminAPI.accounts.batchDelete)
      if (result.failed > 0) {
        // Keep the pool so the remaining members stay grouped for a retry.
        appStore.showError(t('admin.accounts.bulkActions.partialSuccess', result))
        await afterMutation()
        return
      }
    }
    await adminAPI.accountPools.delete(target.id)
    appStore.showSuccess(t('admin.accounts.pools.dissolved', { name: target.name }))
    emit('dissolved', target.id)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
    await afterMutation()
  }
}

defineExpose({ refreshAll })
</script>
