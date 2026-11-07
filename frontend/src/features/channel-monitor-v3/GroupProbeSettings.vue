<template>
  <section class="card !rounded-2xl p-5" data-testid="group-probe-settings">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h3 class="font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV3.probes.title') }}</h3>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.probes.description') }}</p>
      </div>
      <label class="flex items-center gap-2 text-sm"><input v-model="draft.enabled" type="checkbox" />{{ t('channelMonitorV3.probes.enabled') }}</label>
    </div>
    <p class="mt-2 text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.probes.cost') }}</p>
    <div class="mt-4 space-y-3">
      <div v-for="(target, index) in draft.groups" :key="index" class="grid items-end gap-3 rounded-xl border border-gray-200 p-3 dark:border-dark-700 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto_auto]">
        <div>
          <label :for="`probe-group-${index}`" class="input-label">{{ t('channelMonitorV3.probes.group') }}</label>
          <select :id="`probe-group-${index}`" v-model.number="target.group_id" class="input">
            <option v-for="group in eligibleGroups" :key="group.id" :value="group.id">{{ group.name }}</option>
          </select>
        </div>
        <div>
          <label :for="`probe-model-${index}`" class="input-label">{{ t('channelMonitorV3.probes.model') }}</label>
          <input :id="`probe-model-${index}`" v-model.trim="target.model" class="input" maxlength="200" :placeholder="t('channelMonitorV3.probes.modelHint')" required />
        </div>
        <label class="flex min-h-10 items-center gap-2 text-sm"><input v-model="target.enabled" type="checkbox" />{{ t('channelMonitorV3.probes.groupEnabled') }}</label>
        <button type="button" class="btn btn-secondary btn-sm min-h-10" @click="draft.groups.splice(index, 1)">{{ t('common.delete') }}</button>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="draft.groups.length >= 10 || !nextGroup" @click="addGroup">{{ t('channelMonitorV3.probes.add') }}</button>
    </div>
    <p class="mt-3 text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.probes.saveHint') }}</p>
    <slot name="footer" />
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorV3GroupProbeConfig } from '@/api/channelMonitorV3'
import type { AdminGroup } from '@/types'
const draft = defineModel<MonitorV3GroupProbeConfig>({ required: true })
const props = defineProps<{ groups: AdminGroup[] }>()
const { t } = useI18n()
const eligibleGroups = computed(() => props.groups.filter(group => ['openai', 'anthropic'].includes(group.platform) && group.status === 'active'))
const nextGroup = computed(() => eligibleGroups.value.find(group => !draft.value.groups.some(target => target.group_id === group.id)))
function addGroup() { if (nextGroup.value) draft.value.groups.push({ group_id: nextGroup.value.id, model: '', enabled: false }) }
</script>
