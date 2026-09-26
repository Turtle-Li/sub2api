<template>
  <BaseDialog
    :show="show"
    :title="pool ? pool.name : t('admin.proxies.pools.title')"
    width="full"
    :close-on-escape="!showReleaseDialog && !showDissolveDialog"
    :close-on-click-outside="!showReleaseDialog && !showDissolveDialog"
    @close="$emit('close')"
  >
    <div v-if="pool" class="space-y-4" data-testid="proxy-pool-modal">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="min-w-0 truncate text-sm text-gray-500 dark:text-gray-400" :title="pool.notes || undefined">
          {{ pool.notes || t('admin.proxies.pools.memberHint') }}
        </p>
        <div class="flex flex-wrap gap-2">
          <button class="btn btn-secondary btn-sm" @click="refreshAll">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          </button>
          <button class="btn btn-secondary btn-sm" @click="$emit('edit-pool', pool)">{{ t('common.edit') }}</button>
          <button class="btn btn-danger btn-sm" @click="showDissolveDialog = true">{{ t('admin.proxies.pools.dissolve') }}</button>
        </div>
      </div>

      <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
        <div class="rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-700">
          <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.stats.total') }}</div>
          <div class="text-lg font-semibold text-gray-900 dark:text-white">{{ pool.stats.total }}</div>
        </div>
        <div class="rounded-lg border border-emerald-200 px-3 py-2 dark:border-emerald-800">
          <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.stats.available') }}</div>
          <div class="text-lg font-semibold text-emerald-600 dark:text-emerald-400">{{ pool.stats.available }}</div>
        </div>
        <div class="rounded-lg border border-amber-200 px-3 py-2 dark:border-amber-800">
          <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.stats.unavailable') }}</div>
          <div class="text-lg font-semibold text-amber-600 dark:text-amber-400">{{ pool.stats.unavailable }}</div>
        </div>
        <div class="rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-700">
          <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.stats.boundAccounts') }}</div>
          <div class="text-lg font-semibold text-gray-900 dark:text-white">{{ pool.stats.bound_accounts }}</div>
        </div>
      </div>

      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex flex-1 flex-wrap gap-2">
          <input
            v-model="search"
            type="text"
            class="input w-full sm:w-64"
            :placeholder="t('admin.proxies.searchProxies')"
            @input="debouncedReload"
          />
          <select v-model="status" class="input w-full sm:w-40" @change="reloadMembers">
            <option value="">{{ t('admin.proxies.allStatus') }}</option>
            <option value="active">{{ t('admin.accounts.status.active') }}</option>
            <option value="inactive">{{ t('admin.accounts.status.inactive') }}</option>
            <option value="expired">{{ t('admin.proxies.expired') }}</option>
          </select>
        </div>
        <button
          class="btn btn-secondary btn-sm"
          :disabled="selectedIds.length === 0"
          @click="showReleaseDialog = true"
        >
          {{ t('admin.proxies.pools.releaseSelected', { count: selectedIds.length }) }}
        </button>
      </div>

      <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-800 dark:text-dark-400">
            <tr>
              <th class="w-10 px-3 py-2 text-left">
                <input type="checkbox" :checked="allPageSelected" @change="togglePageFromEvent" />
              </th>
              <th class="px-3 py-2 text-left">{{ t('admin.proxies.columns.name') }}</th>
              <th class="px-3 py-2 text-left">{{ t('admin.proxies.columns.protocol') }}</th>
              <th class="px-3 py-2 text-left">{{ t('admin.proxies.columns.address') }}</th>
              <th class="px-3 py-2 text-left">{{ t('admin.proxies.columns.status') }}</th>
              <th class="px-3 py-2 text-left">{{ t('admin.proxies.columns.accounts') }}</th>
              <th class="px-3 py-2 text-right">{{ t('admin.proxies.columns.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
            <tr v-if="loading">
              <td colspan="7" class="px-3 py-8 text-center text-gray-500">{{ t('common.loading') }}</td>
            </tr>
            <tr v-else-if="items.length === 0">
              <td colspan="7" class="px-3 py-8 text-center text-gray-500">{{ t('admin.proxies.pools.empty') }}</td>
            </tr>
            <template v-else>
              <tr v-for="proxy in items" :key="proxy.id">
                <td class="px-3 py-2">
                  <input type="checkbox" :checked="selected.has(proxy.id)" @change="toggleRow(proxy.id)" />
                </td>
                <td class="px-3 py-2 font-medium text-gray-900 dark:text-white">{{ proxy.name }}</td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-300">{{ proxy.protocol.toUpperCase() }}</td>
                <td class="px-3 py-2 font-mono text-xs text-gray-600 dark:text-gray-300">{{ proxy.host }}:{{ proxy.port }}</td>
                <td class="px-3 py-2">
                  <span :class="['badge', proxy.status === 'active' ? 'badge-success' : proxy.status === 'expired' ? 'badge-danger' : 'badge-gray']">
                    {{ proxy.status }}
                  </span>
                </td>
                <td class="px-3 py-2">
                  <button
                    v-if="(proxy.account_count || 0) > 0"
                    class="text-primary-600 hover:underline dark:text-primary-400"
                    @click="$emit('open-accounts', proxy)"
                  >
                    {{ proxy.account_count }}
                  </button>
                  <span v-else>0</span>
                </td>
                <td class="px-3 py-2 text-right">
                  <button class="text-primary-600 hover:underline dark:text-primary-400" @click="$emit('edit-proxy', proxy)">
                    {{ t('common.edit') }}
                  </button>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>

      <Pagination
        v-if="total > 0"
        :page="page"
        :total="total"
        :page-size="pageSize"
        @update:page="handlePageChange"
        @update:pageSize="handlePageSizeChange"
      />
    </div>

    <ConfirmDialog
      :show="showReleaseDialog"
      :title="t('admin.proxies.pools.releaseTitle')"
      :message="t('admin.proxies.pools.releaseConfirm', { count: selectedIds.length })"
      :confirm-text="t('admin.proxies.pools.release')"
      :cancel-text="t('common.cancel')"
      @confirm="releaseSelected"
      @cancel="showReleaseDialog = false"
    />
    <ConfirmDialog
      :show="showDissolveDialog"
      :title="t('admin.proxies.pools.dissolveTitle')"
      :message="t('admin.proxies.pools.dissolveConfirm', { name: pool?.name || '' })"
      :confirm-text="t('admin.proxies.pools.dissolve')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="dissolvePool"
      @cancel="showDissolveDialog = false"
    />
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import { adminAPI } from '@/api/admin'
import type { Proxy, ProxyPool } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ show: boolean; poolId: number | null }>()
const emit = defineEmits<{
  close: []
  changed: []
  dissolved: [poolId: number]
  'edit-pool': [pool: ProxyPool]
  'edit-proxy': [proxy: Proxy]
  'open-accounts': [proxy: Proxy]
}>()

const { t } = useI18n()
const appStore = useAppStore()
const pool = ref<ProxyPool | null>(null)
const items = ref<Proxy[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const search = ref('')
const status = ref('')
const loading = ref(false)
const selected = ref(new Set<number>())
const showReleaseDialog = ref(false)
const showDissolveDialog = ref(false)
let loadVersion = 0
let searchTimer: ReturnType<typeof setTimeout> | undefined

const selectedIds = computed(() => Array.from(selected.value))
const allPageSelected = computed(() => items.value.length > 0 && items.value.every(proxy => selected.value.has(proxy.id)))

const loadPool = async () => {
  if (props.poolId == null) return
  pool.value = await adminAPI.proxyPools.get(props.poolId)
}

const loadMembers = async () => {
  if (props.poolId == null) return
  const version = ++loadVersion
  loading.value = true
  try {
    const result = await adminAPI.proxies.list(page.value, pageSize.value, {
      pool: String(props.poolId),
      search: search.value.trim() || undefined,
      status: (status.value || undefined) as 'active' | 'inactive' | 'expired' | undefined,
      sort_by: 'id',
      sort_order: 'asc'
    })
    if (version !== loadVersion) return
    items.value = result.items ?? []
    total.value = result.total ?? 0
  } catch (error) {
    if (version === loadVersion) appStore.showError(extractApiErrorMessage(error, t('admin.proxies.failedToLoad')))
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

const refreshAll = async () => {
  try {
    await Promise.all([loadPool(), loadMembers()])
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.failedToLoad')))
  }
}

const clearSelection = () => { selected.value = new Set() }
const reloadMembers = () => {
  page.value = 1
  clearSelection()
  void loadMembers()
}
const debouncedReload = () => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(reloadMembers, 300)
}
const toggleRow = (id: number) => {
  const next = new Set(selected.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selected.value = next
}
const togglePage = (checked: boolean) => {
  const next = new Set(selected.value)
  items.value.forEach(proxy => checked ? next.add(proxy.id) : next.delete(proxy.id))
  selected.value = next
}
const togglePageFromEvent = (event: Event) => {
  togglePage((event.target as HTMLInputElement).checked)
}
const handlePageChange = (value: number) => { page.value = value; clearSelection(); void loadMembers() }
const handlePageSizeChange = (value: number) => { pageSize.value = value; page.value = 1; clearSelection(); void loadMembers() }

const releaseSelected = async () => {
  showReleaseDialog.value = false
  const ids = selectedIds.value
  if (ids.length === 0) return
  try {
    const result = await adminAPI.proxyPools.removeMembers(ids)
    appStore.showSuccess(t('admin.proxies.pools.releaseSuccess', { count: result.affected }))
    clearSelection()
    emit('changed')
    await refreshAll()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.pools.batchFailed')))
  }
}

const dissolvePool = async () => {
  showDissolveDialog.value = false
  const target = pool.value
  if (!target) return
  try {
    await adminAPI.proxyPools.delete(target.id)
    appStore.showSuccess(t('admin.proxies.pools.dissolved', { name: target.name }))
    emit('dissolved', target.id)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.pools.saveFailed')))
  }
}

watch(
  () => [props.show, props.poolId] as const,
  ([show]) => {
    if (!show) return
    pool.value = null
    items.value = []
    total.value = 0
    page.value = 1
    search.value = ''
    status.value = ''
    clearSelection()
    void refreshAll()
  },
  { immediate: true }
)

onUnmounted(() => clearTimeout(searchTimer))
defineExpose({ refreshAll })
</script>
