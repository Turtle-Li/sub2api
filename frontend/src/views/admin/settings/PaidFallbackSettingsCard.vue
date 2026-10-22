<template>
  <section class="card p-6" aria-labelledby="paid-fallback-heading">
    <h3 id="paid-fallback-heading" class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.paidFallback.title') }}</h3>
    <p id="paid-fallback-description" class="mt-1 max-w-3xl text-sm text-gray-600 dark:text-gray-400">{{ t('admin.paidFallback.description') }}</p>
    <p v-if="loading" role="status" class="mt-4 text-sm">{{ t('common.loading') }}</p>
    <div v-if="loadError" role="alert" class="mt-4 text-sm text-red-700 dark:text-red-300">
      <p>{{ loadError }}</p>
      <button type="button" class="btn btn-secondary mt-2" :disabled="busy" @click="load">{{ t('admin.paidFallback.retry') }}</button>
    </div>
    <fieldset v-if="loaded" :disabled="busy" class="mt-5 space-y-5">
      <div class="flex items-center gap-3">
        <Toggle v-model="form.enabled" :disabled="busy" :aria-label="t('admin.paidFallback.enabled')" aria-describedby="paid-fallback-description" data-testid="paid-fallback-toggle" />
        <span class="text-sm font-medium">{{ t(form.enabled ? 'admin.paidFallback.enabled' : 'admin.paidFallback.disabled') }}</span>
      </div>
      <div class="max-w-xl">
        <div>
          <label for="paid-fallback-group" class="mb-2 block text-sm font-medium">{{ t('admin.paidFallback.groups') }}</label>
          <Select id="paid-fallback-group" :model-value="null" :options="groupOptions" :disabled="busy || !!groupsError" searchable :aria-label="t('admin.paidFallback.groups')" @update:model-value="addGroup" />
          <div v-if="groupsError" role="alert" class="mt-2 text-sm text-red-700 dark:text-red-300"><p>{{ groupsError }}</p><button type="button" class="btn btn-secondary mt-2" @click="loadGroups">{{ t('admin.paidFallback.retry') }}</button></div>
          <ul class="mt-2 flex flex-wrap gap-2" :aria-label="t('admin.paidFallback.selectedGroups')">
            <li v-for="id in form.group_ids" :key="id" class="inline-flex items-center gap-2 rounded-md bg-gray-100 px-2 py-1 text-sm dark:bg-dark-700">
              {{ groupNames.get(id) || `#${id}` }}
              <button type="button" class="flex min-h-8 min-w-8 items-center justify-center rounded focus-visible:ring-2 focus-visible:ring-primary-500" :aria-label="t('admin.paidFallback.remove', { name: groupNames.get(id) || `#${id}` })" @click="form.group_ids = form.group_ids.filter(value => value !== id)"><Icon name="x" size="sm" /></button>
            </li>
          </ul>
        </div>
      </div>
      <p v-if="form.enabled && !validScope" role="alert" class="text-sm text-amber-800 dark:text-amber-200">{{ t('admin.paidFallback.scopeRequired') }}</p>
      <p role="status" class="text-sm text-gray-600 dark:text-gray-400">{{ t(dirty ? 'admin.paidFallback.unsaved' : 'admin.paidFallback.savedState') }}</p>
    </fieldset>
    <div class="mt-5 flex flex-wrap items-center justify-between gap-3">
      <button type="button" class="btn btn-secondary" data-testid="paid-fallback-disable" :disabled="busy" @click="disable">{{ t('admin.paidFallback.disableNow') }}</button>
      <button v-if="loaded" type="button" class="btn btn-primary" data-testid="paid-fallback-save" :disabled="busy || !dirty || (form.enabled && !validScope)" @click="save">{{ saving ? t('common.saving') : t('common.save') }}</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import type { PaidFallbackSettings } from '@/api/admin/settings'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const app = useAppStore()
const form = reactive<PaidFallbackSettings>({ enabled: false, group_ids: [] })
const loading = ref(false)
const loaded = ref(false)
const saving = ref(false)
const loadError = ref('')
const groupsError = ref('')
const saved = ref('')
const groupNames = reactive(new Map<number, string>())
const busy = computed(() => loading.value || saving.value)
const validScope = computed(() => form.group_ids.length > 0)
const snapshot = () => JSON.stringify({ enabled: form.enabled, group_ids: [...form.group_ids].sort((a,b) => a-b) })
const dirty = computed(() => saved.value !== snapshot())
const groupOptions = computed(() => [...groupNames].filter(([id]) => !form.group_ids.includes(id)).map(([value, label]) => ({ value, label })))

function apply(value: PaidFallbackSettings) {
  Object.assign(form, { enabled: value.enabled, group_ids: [...(value.group_ids || [])] })
  saved.value = snapshot()
  loaded.value = true
  loadError.value = ''
}
function addGroup(value: unknown) { if (typeof value === 'number' && !form.group_ids.includes(value)) form.group_ids.push(value) }
async function load() {
  loading.value = true
  loadError.value = ''
  try { apply(await adminAPI.settings.getPaidFallbackSettings()) }
  catch (error) { loadError.value = extractApiErrorMessage(error, t('admin.paidFallback.loadFailed')) }
  finally { loading.value = false }
}
async function save() {
  if (busy.value || !loaded.value || (form.enabled && !validScope.value)) return
  saving.value = true
  try {
    apply(await adminAPI.settings.updatePaidFallbackSettings({ enabled: form.enabled, group_ids: [...form.group_ids] }))
    app.showSuccess(t('admin.paidFallback.saved'))
  } catch (error) { app.showError(extractApiErrorMessage(error, t('admin.paidFallback.saveFailed'))) }
  finally { saving.value = false }
}
async function disable() {
  if (busy.value) return
  saving.value = true
  try {
    apply(await adminAPI.settings.disablePaidFallback())
    app.showSuccess(t('admin.paidFallback.disabledNow'))
  } catch (error) { app.showError(extractApiErrorMessage(error, t('admin.paidFallback.saveFailed'))) }
  finally { saving.value = false }
}
async function loadGroups() {
  groupsError.value = ''
  try { (await adminAPI.groups.getAllIncludingInactive()).forEach(group => groupNames.set(group.id, `${group.name} (#${group.id})`)) }
  catch (error) { groupsError.value = extractApiErrorMessage(error, t('admin.paidFallback.groupsFailed')) }
}
onMounted(() => { void load(); void loadGroups() })
</script>
