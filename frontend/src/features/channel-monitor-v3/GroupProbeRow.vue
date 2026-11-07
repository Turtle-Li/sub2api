<template>
  <div class="py-5" :data-status-group="group.id">
    <div class="flex items-start justify-between gap-4">
      <p class="min-w-0 break-words text-sm font-semibold text-gray-900 dark:text-dark-100">{{ group.name }}</p>
      <button type="button" class="btn btn-secondary btn-sm shrink-0" :aria-label="t('channelMonitorV3.probes.removeGroup', { group: group.name })" @click="$emit('remove')">{{ t('channelMonitorV3.probes.remove') }}</button>
    </div>
    <div class="mt-4 grid items-start gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
      <div class="min-w-0">
        <label :for="`status-name-${group.id}`" class="input-label">{{ t('channelMonitorV3.probes.displayName') }}</label>
        <input :id="`status-name-${group.id}`" v-model.trim="target.display_name" class="input" maxlength="100" :placeholder="group.name" />
      </div>
      <div v-if="supportsCheck" class="min-w-0">
        <label :for="`probe-model-${group.id}`" class="input-label">{{ t('channelMonitorV3.probes.model') }}</label>
        <Select :id="`probe-model-${group.id}`" :model-value="target.model" :options="modelOptions" :loading="loading" :disabled="loading" searchable :placeholder="t('channelMonitorV3.probes.modelHint')" :aria-label="t('channelMonitorV3.probes.modelFor', { group: group.name })" :aria-describedby="`probe-help-${group.id}`" :empty-text="t('channelMonitorV3.probes.noModels')" @update:model-value="selectModel" />
        <div :id="`probe-help-${group.id}`" class="mt-2 text-xs leading-6">
          <p v-if="loading" class="text-gray-600 dark:text-dark-200" role="status">{{ t('common.loading') }}</p>
          <div v-else-if="error" class="text-red-700 dark:text-red-300" role="alert"><p>{{ error }}</p><button type="button" class="mt-1 underline underline-offset-2" @click="loadModels">{{ t('common.retry') }}</button></div>
          <p v-else-if="unavailable" class="text-amber-800 dark:text-amber-300">{{ t('channelMonitorV3.probes.modelUnavailable') }}</p>
          <p v-else-if="!models.length" class="text-gray-600 dark:text-dark-200">{{ t('channelMonitorV3.probes.noModels') }}</p>
        </div>
      </div>
      <label v-if="supportsCheck" class="flex min-h-10 items-center gap-2 text-sm md:mt-7"><input v-model="target.enabled" type="checkbox" :disabled="!target.enabled && (loading || !models.includes(target.model))" />{{ t('channelMonitorV3.probes.groupEnabled') }}</label>
    </div>
    <p class="mt-2 text-xs leading-6 text-gray-600 dark:text-dark-200">{{ t(supportsCheck ? 'channelMonitorV3.probes.requestHelp' : 'channelMonitorV3.probes.passiveOnly') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { MonitorV3GroupProbeConfig } from '@/api/channelMonitorV3'
import type { AdminGroup } from '@/types'
import { getPlatformSpec } from '@/constants/platformCatalog'
import { extractApiErrorMessage } from '@/utils/apiError'
import Select from '@/components/common/Select.vue'

const props = defineProps<{ group: AdminGroup; target: MonitorV3GroupProbeConfig['groups'][number] }>()
defineEmits<{ remove: [] }>()
const { t } = useI18n()
const models = ref<string[]>([])
const loading = ref(false)
const error = ref('')
let mounted = true
onBeforeUnmount(() => { mounted = false })
const supportsCheck = computed(() => ['openai', 'anthropic'].includes(props.group.platform) || getPlatformSpec(props.group.platform)?.cn_provider === true)
const unavailable = computed(() => !!props.target.model && !models.value.includes(props.target.model))
const modelOptions = computed(() => [
  ...(unavailable.value ? [{ value: props.target.model, label: props.target.model, disabled: true }] : []),
  ...models.value.map(model => ({ value: model, label: model, disabled: false })),
])
async function loadModels() {
  loading.value = true
  error.value = ''
  try {
    const result = await adminAPI.groups.getModelAllowlistCandidates(props.group.id, undefined, true)
    if (mounted) models.value = result
  } catch (err: unknown) {
    if (mounted) { models.value = []; error.value = extractApiErrorMessage(err, t('channelMonitorV3.probes.modelsFailed')) }
  } finally { if (mounted) loading.value = false }
}
function selectModel(value: string | number | boolean | null) {
  if (typeof value === 'string' && models.value.includes(value)) props.target.model = value
}
onMounted(() => { if (supportsCheck.value) void loadModels() })
</script>
