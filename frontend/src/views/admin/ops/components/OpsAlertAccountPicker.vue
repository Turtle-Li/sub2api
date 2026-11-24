<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'

const props = defineProps<{ platform?: string; groupId?: number | null }>()
const selected = defineModel<number[]>({ required: true })
const { t } = useI18n()
const searchId = useId()
const pageSize = 20
const maxSelected = 50
const search = ref('')
const page = ref(1)
const pages = ref(1)
const items = ref<Awaited<ReturnType<typeof adminAPI.accounts.list>>['items']>([])
const labels = reactive(new Map<number, string>())
const selectedIds = computed(() => new Set(selected.value))
const loading = ref(true)
const failed = ref(false)
let active: AbortController | undefined
let debounce: ReturnType<typeof setTimeout> | undefined

function cancelPending() {
  clearTimeout(debounce)
  active?.abort()
  active = undefined
}

async function load(nextPage: number) {
  cancelPending()
  const request = new AbortController()
  active = request
  page.value = nextPage
  loading.value = true
  failed.value = false
  items.value = []
  try {
    const result = await adminAPI.accounts.list(nextPage, pageSize, {
      lite: 'true',
      ...(search.value.trim() && { search: search.value.trim() }),
      ...(props.platform && { platform: props.platform }),
      ...(props.groupId && { group: String(props.groupId) })
    }, { signal: request.signal })
    if (active !== request) return
    items.value = result.items || []
    pages.value = Math.max(1, result.pages || Math.ceil(result.total / pageSize) || 1)
    for (const account of items.value) labels.set(account.id, account.name)
  } catch {
    if (active === request) failed.value = true
  } finally {
    if (active === request) {
      active = undefined
      loading.value = false
    }
  }
}

watch(() => [search.value.trim(), props.platform, props.groupId], (_, old) => {
  cancelPending()
  loading.value = true
  failed.value = false
  page.value = 1
  pages.value = 1
  items.value = []
  if (!old) void load(1)
  else debounce = setTimeout(() => void load(1), 250)
}, { immediate: true })

onBeforeUnmount(cancelPending)

function toggle(id: number) {
  if (selectedIds.value.has(id)) selected.value = selected.value.filter((value) => value !== id)
  else if (selectedIds.value.size < maxSelected) selected.value = [...selected.value, id].sort((a, b) => a - b)
}

function accountLabel(id: number): string {
  const name = labels.get(id)
  return name ? `${name} (#${id})` : `#${id}`
}
</script>

<template>
  <fieldset class="min-w-0 space-y-2 rounded-xl border border-gray-200 p-3 dark:border-dark-600">
    <legend class="px-1 text-xs font-bold text-gray-700 dark:text-gray-200">{{ t('admin.ops.alertRules.form.accounts') }}</legend>
    <div class="flex flex-wrap gap-1.5">
      <button
        v-for="id in selected"
        :key="id"
        type="button"
        class="max-w-full break-words rounded-md bg-gray-100 px-2 py-1 text-xs text-gray-800 hover:bg-gray-200 dark:bg-dark-700 dark:text-gray-100 dark:hover:bg-dark-600"
        :aria-label="t('admin.ops.alertRules.accounts.remove', { account: accountLabel(id) })"
        :data-selected-account="id"
        @click="toggle(id)"
      >{{ accountLabel(id) }} ×</button>
    </div>
    <label :for="searchId" class="sr-only">{{ t('admin.ops.alertRules.accounts.search') }}</label>
    <input :id="searchId" v-model="search" type="search" class="input" :placeholder="t('admin.ops.alertRules.accounts.search')" />
    <div v-if="loading" role="status" class="p-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.alertRules.accounts.loading') }}</div>
    <div v-else-if="failed" role="status" class="flex flex-wrap items-center gap-2 p-2 text-xs text-red-700 dark:text-red-300">
      {{ t('admin.ops.alertRules.accounts.failed') }}
      <button type="button" class="btn btn-sm btn-secondary" @click="load(page)">{{ t('common.refresh') }}</button>
    </div>
    <div v-else-if="items.length" class="grid max-h-44 gap-1 overflow-y-auto sm:grid-cols-2">
      <label v-for="account in items" :key="account.id" class="flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-gray-50 dark:text-gray-200 dark:hover:bg-dark-700">
        <input
          type="checkbox"
          :checked="selectedIds.has(account.id)"
          :disabled="selectedIds.size >= maxSelected && !selectedIds.has(account.id)"
          :value="account.id"
          class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
          @change="toggle(account.id)"
        />
        <span class="break-words">{{ accountLabel(account.id) }}</span>
      </label>
    </div>
    <div v-else role="status" class="p-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.alertRules.accounts.empty') }}</div>
    <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500 dark:text-gray-400">
      <span aria-live="polite">{{ t('admin.ops.alertRules.accounts.selected', { count: selectedIds.size, max: maxSelected }) }}</span>
      <div v-if="pages > 1" class="flex flex-wrap items-center gap-2">
        <button type="button" class="btn btn-sm btn-secondary" :disabled="loading || page <= 1" @click="load(page - 1)">{{ t('admin.ops.alertRules.accounts.previous') }}</button>
        <span>{{ page }} / {{ pages }}</span>
        <button type="button" class="btn btn-sm btn-secondary" :disabled="loading || page >= pages" @click="load(page + 1)">{{ t('admin.ops.alertRules.accounts.next') }}</button>
      </div>
    </div>
  </fieldset>
</template>
