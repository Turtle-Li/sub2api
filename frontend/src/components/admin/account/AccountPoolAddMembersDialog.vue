<template>
  <BaseDialog :show="show" :title="t('admin.accounts.pools.addMembersTitle', { name: pool?.name ?? '' })" width="wide" @close="$emit('close')">
    <div v-if="pool" class="space-y-3" data-testid="account-pool-add-members">
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accounts.pools.addMembersHint') }}</p>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <SearchInput
          v-model="search"
          class="w-full sm:w-64"
          :placeholder="t('admin.accounts.searchAccounts')"
          @search="reload"
        />
        <div class="flex flex-wrap items-center gap-2 text-sm">
          <span v-if="selected.size > 0" class="font-medium text-primary-700 dark:text-primary-300">
            {{ t('admin.accounts.bulkActions.selected', { count: selected.size }) }}
          </span>
          <button v-if="selected.size > 0" class="text-xs text-primary-600 hover:underline" @click="selected = new Set()">
            {{ t('admin.accounts.bulkActions.clear') }}
          </button>
          <button
            v-if="total > selected.size"
            class="text-xs text-primary-600 hover:underline disabled:opacity-60"
            :disabled="selectingAll"
            data-testid="account-pool-add-select-all"
            @click="selectAllResults"
          >
            {{ selectingAll ? t('admin.accounts.bulkActions.selectingAll') : t('admin.accounts.bulkActions.selectAllResults', { count: total }) }}
          </button>
        </div>
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
              <th class="px-3 py-2">{{ t('admin.accounts.columns.groups') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
            <tr v-if="loading && items.length === 0">
              <td colspan="4" class="px-3 py-8 text-center text-gray-400">{{ t('common.loading') }}</td>
            </tr>
            <tr v-else-if="items.length === 0">
              <td colspan="4" class="px-3 py-8 text-center text-gray-400">{{ t('admin.accounts.pools.addMembersEmpty') }}</td>
            </tr>
            <tr
              v-for="row in items"
              :key="row.id"
              class="cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-800/60"
              :data-testid="`account-pool-add-row-${row.id}`"
              @click="toggleRow(row.id)"
            >
              <td class="px-3 py-2">
                <input type="checkbox" class="rounded border-gray-300 text-primary-600" :checked="selected.has(row.id)" @click.stop @change="toggleRow(row.id)" />
              </td>
              <td class="max-w-[320px] px-3 py-2">
                <div class="truncate font-medium text-gray-900 dark:text-white" :title="row.name">{{ row.name }}</div>
              </td>
              <td class="px-3 py-2"><AccountStatusIndicator :account="row as Account" /></td>
              <td class="px-3 py-2"><AccountGroupsCell :groups="groupsForRow(row)" :max-display="3" /></td>
            </tr>
          </tbody>
        </table>
      </div>

      <Pagination
        v-if="total > 0"
        :total="total"
        :page="page"
        :page-size="pageSize"
        @update:page="(value: number) => { page = value; load() }"
        @update:page-size="(value: number) => { pageSize = value; page = 1; load() }"
      />
    </div>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="$emit('close')">{{ t('common.cancel') }}</button>
        <button
          type="button"
          class="btn btn-primary"
          data-testid="account-pool-add-submit"
          :disabled="submitting || selected.size === 0"
          @click="submit"
        >
          {{ submitting ? t('common.saving') : t('admin.accounts.pools.addMembersSubmit', { count: selected.size }) }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountGroupsCell from '@/components/account/AccountGroupsCell.vue'
import { adminAPI } from '@/api/admin'
import type { AccountPool } from '@/api/admin/accountPools'
import type { Account, AccountListItem, AdminGroup } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { fetchAllAccountIds } from '@/utils/accountSelection'

// Picks existing accounts outside any pool and moves them into this pool.
const props = defineProps<{
  show: boolean
  pool: AccountPool | null
  groups: AdminGroup[]
}>()

const emit = defineEmits<{
  close: []
  added: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const items = ref<AccountListItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const search = ref('')
const selected = ref(new Set<number>())
const selectingAll = ref(false)
const submitting = ref(false)
let loadVersion = 0
// Backend accepts at most 5000 ids per add-members request.
const ADD_CHUNK_SIZE = 1000

const groupsByID = computed(() => new Map(props.groups.map(group => [group.id, group])))
const groupsForRow = (row: AccountListItem): AdminGroup[] =>
  (row.group_ids ?? []).map(id => groupsByID.value.get(id)).filter((group): group is AdminGroup => Boolean(group))

const candidateFilters = () => ({
  platform: props.pool?.platform ?? '',
  pool: 'none',
  search: search.value.trim(),
  lite: '1',
  sort_by: 'name',
  sort_order: 'asc' as const
})

const load = async () => {
  if (!props.pool) return
  const version = ++loadVersion
  loading.value = true
  try {
    const result = await adminAPI.accounts.list(page.value, pageSize.value, candidateFilters())
    if (version !== loadVersion) return
    items.value = result.items ?? []
    total.value = result.total ?? 0
  } catch (error) {
    if (version === loadVersion) appStore.showError(extractApiErrorMessage(error, t('admin.accounts.pools.loadFailed')))
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

const reload = () => {
  page.value = 1
  return load()
}

watch(
  () => [props.show, props.pool?.id] as const,
  ([show]) => {
    if (!show) return
    search.value = ''
    items.value = []
    total.value = 0
    selected.value = new Set()
    reload()
  },
  { immediate: true }
)

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
      candidateFilters()
    )
    selected.value = new Set(ids)
  } catch (error) {
    appStore.showError(t('admin.accounts.bulkActions.selectAllFailed'))
  } finally {
    selectingAll.value = false
  }
}

const submit = async () => {
  if (!props.pool || selected.value.size === 0) return
  submitting.value = true
  try {
    const ids = Array.from(selected.value)
    let affected = 0
    for (let i = 0; i < ids.length; i += ADD_CHUNK_SIZE) {
      affected += (await adminAPI.accountPools.addMembers(props.pool.id, ids.slice(i, i + ADD_CHUNK_SIZE))).affected
    }
    appStore.showSuccess(t('admin.accounts.pools.assignSuccess', { count: affected }))
    emit('added')
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.pools.assignFailed')))
  } finally {
    submitting.value = false
  }
}
</script>
