<template>
  <BaseDialog :show="show" :title="t('admin.codexTurnState.proxies.title')" width="wide" @close="emit('close')">
    <div class="space-y-4 text-sm">
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.codexTurnState.proxies.hint') }}</p>
      <div v-if="loadError" role="alert" class="rounded-lg border border-red-200 bg-red-50 p-3 text-xs text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200">
        {{ loadError }}
      </div>

      <div class="overflow-x-auto">
        <table class="w-full text-left text-sm">
          <thead class="text-xs uppercase text-gray-500 dark:text-gray-400">
            <tr>
              <th class="px-3 py-2">{{ t('admin.codexTurnState.proxies.columns.name') }}</th>
              <th class="px-3 py-2">{{ t('admin.codexTurnState.proxies.columns.type') }}</th>
              <th class="px-3 py-2">{{ t('admin.codexTurnState.proxies.columns.origin') }}</th>
              <th class="px-3 py-2 text-right">{{ t('admin.codexTurnState.proxies.columns.count') }}</th>
              <th class="px-3 py-2">{{ t('admin.codexTurnState.proxies.columns.status') }}</th>
              <th class="px-3 py-2 text-right">{{ t('admin.codexTurnState.columns.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-if="!sources.length">
              <td colspan="6" class="px-3 py-6 text-center text-gray-500 dark:text-gray-400">
                {{ loading ? t('common.loading') : t('admin.codexTurnState.proxies.empty') }}
              </td>
            </tr>
            <tr v-for="source in sources" :key="source.id" data-test="cts-proxy-row">
              <td class="px-3 py-2 font-medium text-gray-900 dark:text-white">{{ source.name }}</td>
              <td class="px-3 py-2 text-xs">{{ label('types', source.type) }}</td>
              <td class="px-3 py-2 text-xs">{{ t(`admin.codexTurnState.proxies.origins.${source.origin === 'base' ? 'base' : 'managed'}`) }}</td>
              <td class="px-3 py-2 text-right text-xs">{{ source.endpoint_count ?? source.count ?? '-' }}</td>
              <td class="px-3 py-2">
                <span :class="statusClass(source)">{{ label('statuses', source.status) }}</span>
              </td>
              <td class="whitespace-nowrap px-3 py-2 text-right">
                <span v-if="source.read_only || source.origin === 'base'" class="text-xs text-gray-400">{{ t('admin.codexTurnState.proxies.readOnly') }}</span>
                <template v-else>
                  <button type="button" class="btn btn-secondary btn-sm mr-2" :disabled="busy" :data-test="`cts-proxy-toggle-${source.id}`" @click="toggle(source)">
                    {{ source.enabled === false ? t('admin.codexTurnState.proxies.enable') : t('admin.codexTurnState.proxies.disable') }}
                  </button>
                  <button type="button" class="btn btn-danger btn-sm" :disabled="busy" :data-test="`cts-proxy-remove-${source.id}`" @click="remove(source)">
                    {{ t('admin.codexTurnState.actions.delete') }}
                  </button>
                </template>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <details class="rounded-lg border border-gray-200 dark:border-dark-600">
        <summary class="cursor-pointer px-3 py-2 font-medium text-gray-700 dark:text-gray-200">{{ t('admin.codexTurnState.proxies.importTitle') }}</summary>
        <form class="space-y-3 border-t border-gray-200 p-3 dark:border-dark-600" data-test="cts-proxy-import" @submit.prevent="importSource">
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.codexTurnState.proxies.form.name') }}
              <input v-model="form.name" class="input mt-1" maxlength="64" placeholder="static-pool" data-test="cts-proxy-name" />
            </label>
            <label class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.codexTurnState.proxies.form.type') }}
              <select v-model="form.type" class="input mt-1" data-test="cts-proxy-type">
                <option v-for="type in sourceTypes" :key="type" :value="type">{{ label('types', type) }}</option>
              </select>
            </label>
          </div>
          <label class="block text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.codexTurnState.proxies.form.content') }}
            <textarea
              v-model="form.content"
              rows="4"
              class="input mt-1 font-mono text-xs"
              spellcheck="false"
              autocomplete="off"
              :placeholder="t(`admin.codexTurnState.proxies.form.placeholders.${form.type}`)"
              data-test="cts-proxy-content"
            />
          </label>
          <div v-if="form.type === 'extract'" class="grid gap-3 sm:grid-cols-2">
            <label class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.codexTurnState.proxies.form.username') }}
              <input v-model="form.username" class="input mt-1" autocomplete="off" />
            </label>
            <label class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.codexTurnState.proxies.form.password') }}
              <input v-model="form.password" type="password" class="input mt-1" autocomplete="new-password" />
            </label>
          </div>
          <p class="text-xs text-gray-400 dark:text-gray-500">{{ t('admin.codexTurnState.proxies.form.hint') }}</p>
          <div class="flex justify-end">
            <button type="submit" class="btn btn-primary btn-sm" :disabled="!canImport" data-test="cts-proxy-submit">{{ t('admin.codexTurnState.proxies.form.submit') }}</button>
          </div>
        </form>
      </details>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import {
  createProxySource,
  deleteProxySource,
  listProxySources,
  setProxySourceEnabled,
  type CodexProxySource,
  type CodexProxySourceType,
} from '@/api/admin/codexTurnState'
import { useAppStore } from '@/stores'
import { describeManagerError } from './managerError'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'changed'): void
}>()

const SOURCE_NAME_RE = /^[a-z][a-z0-9_-]{0,63}$/
const sourceTypes: CodexProxySourceType[] = ['static', 'rotating', 'extract']

const { t, te } = useI18n()
const appStore = useAppStore()

const sources = ref<CodexProxySource[]>([])
const loading = ref(false)
const busy = ref(false)
const loadError = ref('')
const form = reactive({ name: '', type: 'static' as CodexProxySourceType, content: '', username: '', password: '' })

const canImport = computed(() => !busy.value && SOURCE_NAME_RE.test(form.name.trim()) && !!form.content.trim())

watch(() => props.show, (show) => { if (show) void load() }, { immediate: true })

function label(group: 'types' | 'statuses', value: string) {
  const key = `admin.codexTurnState.proxies.${group}.${value}`
  return te(key) ? t(key) : value
}

function statusClass(source: CodexProxySource) {
  switch (source.status) {
    case 'loaded': return 'badge badge-success'
    case 'error':
    case 'unavailable': return 'badge badge-danger'
    case 'pending':
    case 'deferred': return 'badge badge-warning'
    default: return 'badge badge-gray'
  }
}

async function load() {
  loading.value = true
  try {
    sources.value = await listProxySources()
    loadError.value = ''
  } catch (err) {
    loadError.value = describeManagerError(err, t, 'admin.codexTurnState.proxies.loadFailed')
  } finally {
    loading.value = false
  }
}

async function mutate(action: () => Promise<void>, successKey: string) {
  busy.value = true
  try {
    await action()
    appStore.showSuccess(t(successKey))
    emit('changed')
    await load()
    return true
  } catch (err) {
    appStore.showError(describeManagerError(err, t, 'admin.codexTurnState.saveFailed'))
    return false
  } finally {
    busy.value = false
  }
}

function toggle(source: CodexProxySource) {
  void mutate(() => setProxySourceEnabled(source.id, source.enabled === false), 'admin.codexTurnState.proxies.saved')
}

function remove(source: CodexProxySource) {
  if (!window.confirm(t('admin.codexTurnState.proxies.removeConfirm', { name: source.name }))) return
  void mutate(() => deleteProxySource(source.id), 'admin.codexTurnState.proxies.removed')
}

async function importSource() {
  if (!canImport.value) return
  const extract = form.type === 'extract'
  const ok = await mutate(() => createProxySource({
    name: form.name.trim(),
    type: form.type,
    content: form.content,
    username: extract ? form.username : '',
    password: extract ? form.password : '',
  }), 'admin.codexTurnState.proxies.imported')
  if (ok) {
    form.content = ''
    form.username = ''
    form.password = ''
  }
}
</script>
