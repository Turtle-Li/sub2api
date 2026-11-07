<template>
  <div data-testid="status-inline-controls">
    <p v-if="loading" class="py-6 text-sm text-gray-500" role="status">{{ t('common.loading') }}</p>
    <p v-if="error" class="mb-4 text-sm text-red-700 dark:text-red-300" role="alert">{{ error }}</p>
    <GroupProbeSettings v-if="draft?.group_probe" v-model="draft.group_probe" :groups="groups">
      <template #footer>
        <div class="mt-5 flex flex-wrap items-center justify-between gap-4 border-t border-gray-200 pt-4 dark:border-dark-700">
          <label class="flex items-center gap-2 text-sm"><input v-model="draft.public_enabled" type="checkbox" />{{ t('channelMonitorV3.cards.publicAccess') }}</label>
          <div class="flex items-center gap-3">
            <span v-if="saved" class="text-sm text-emerald-700 dark:text-emerald-400" role="status">{{ t('channelMonitorV3.admin.saved') }}</span>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="saving" @click="$emit('close')">{{ t('common.close') }}</button>
            <button type="button" class="btn btn-primary btn-sm" :disabled="saving || !dirty" data-testid="status-save-controls" @click="save">{{ saving ? t('common.loading') : t('common.save') }}</button>
          </div>
        </div>
      </template>
    </GroupProbeSettings>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { getSettings, updateConfig, type MonitorV3Config } from '@/api/channelMonitorV3'
import type { AdminGroup } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import GroupProbeSettings from './GroupProbeSettings.vue'

const emit = defineEmits<{ saved: []; close: [] }>()
const { t } = useI18n()
const draft = ref<MonitorV3Config | null>(null)
const groups = ref<AdminGroup[]>([])
const original = ref('')
const error = ref('')
const loading = ref(true)
const saving = ref(false)
const saved = ref(false)
const dirty = computed(() => !!draft.value && JSON.stringify(draft.value) !== original.value)
let mounted = true
onBeforeUnmount(() => { mounted = false })
onMounted(async () => {
  try {
    const [settings, allGroups] = await Promise.all([getSettings(), adminAPI.groups.getAll()])
    if (!mounted) return
    draft.value = { ...settings.config, group_probe: structuredClone(settings.config.group_probe ?? { enabled: false, groups: [] }) }
    groups.value = allGroups
    original.value = JSON.stringify(draft.value)
  } catch (err: unknown) {
    if (mounted) error.value = extractApiErrorMessage(err, t('channelMonitorV3.admin.loadFailed'))
  } finally { if (mounted) loading.value = false }
})
async function save() {
  if (!draft.value || saving.value) return
  saving.value = true
  saved.value = false
  error.value = ''
  try {
    const next = await updateConfig(draft.value)
    if (!mounted) return
    draft.value = next
    original.value = JSON.stringify(next)
    saved.value = true
    emit('saved')
  } catch (err: unknown) {
    if (mounted) error.value = extractApiErrorMessage(err, t('channelMonitorV3.admin.saveFailed'))
  } finally { if (mounted) saving.value = false }
}
</script>
