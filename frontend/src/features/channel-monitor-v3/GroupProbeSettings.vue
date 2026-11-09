<template>
  <section class="card !rounded-2xl p-5" data-testid="group-probe-settings">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div class="max-w-3xl">
        <h3 class="font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV3.probes.title') }}</h3>
        <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-200">{{ t('channelMonitorV3.probes.description') }}</p>
      </div>
      <label class="flex min-h-10 items-center gap-2 text-sm"><input v-model="draft.enabled" type="checkbox" />{{ t('channelMonitorV3.probes.enabled') }}</label>
    </div>
    <div class="mt-5 flex flex-col gap-3 sm:flex-row sm:items-end">
      <div class="w-full sm:max-w-xl">
        <label for="status-add-group" class="input-label">{{ t('channelMonitorV3.probes.group') }}</label>
        <Select id="status-add-group" v-model="selectedGroupId" :options="addOptions" searchable :placeholder="t('channelMonitorV3.probes.selectGroup')" :aria-label="t('channelMonitorV3.probes.selectGroup')" :empty-text="t('channelMonitorV3.probes.allAdded')" />
      </div>
      <button type="button" class="btn btn-secondary min-h-10 shrink-0" data-testid="status-add-group" :disabled="typeof selectedGroupId !== 'number'" @click="addGroup">{{ t('channelMonitorV3.probes.add') }}</button>
    </div>
    <p class="mt-3 text-xs leading-6 text-gray-600 dark:text-dark-200">{{ t('channelMonitorV3.probes.cost') }}</p>
    <section v-for="section in sections" :key="section.platform" class="mt-6" :aria-label="platformLabel(section.platform)">
      <h4 class="mb-3 flex items-center gap-2 text-sm font-semibold"><PlatformIcon :platform="section.platform" size="sm" />{{ platformLabel(section.platform) }} <span class="text-gray-500">{{ section.rows.length }}</span></h4>
      <div class="divide-y divide-gray-200 border-y border-gray-200 dark:divide-dark-600 dark:border-dark-600">
        <GroupProbeRow v-for="row in section.rows" :key="row.group.id" :group="row.group" :target="row.target" :sources="sourceOptions" @remove="removeGroup(row.target)" />
      </div>
    </section>
    <p v-if="!sections.length" class="py-10 text-center text-sm leading-6 text-gray-600 dark:text-dark-200" data-testid="status-groups-empty">{{ t('channelMonitorV3.probes.empty') }}</p>
    <p class="mt-5 text-xs leading-6 text-gray-600 dark:text-dark-200">{{ t('channelMonitorV3.probes.saveHint') }}</p>
    <slot name="footer" />
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorV3GroupProbeConfig } from '@/api/channelMonitorV3'
import type { AdminGroup } from '@/types'
import { listPlatformIds } from '@/constants/platformCatalog'
import { platformLabel } from '@/utils/platformColors'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Select from '@/components/common/Select.vue'
import GroupProbeRow from './GroupProbeRow.vue'

const draft = defineModel<MonitorV3GroupProbeConfig>({ required: true })
const props = defineProps<{ groups: AdminGroup[] }>()
const { t } = useI18n()
const selectedGroupId = ref<string | number | boolean | null>(null)
const publicGroups = computed(() => props.groups.filter(group => group.status === 'active' && !group.is_exclusive))
const isListed = (target: MonitorV3GroupProbeConfig['groups'][number]) => target.listed ?? target.enabled
const sourceOptions = computed(() => draft.value.groups.flatMap(target => {
  const group = publicGroups.value.find(item => item.id === target.group_id)
  return group && isListed(target) && target.enabled && !target.source_group_id
    ? [{ value: group.id, label: target.display_name || group.name }] : []
}))
const addOptions = computed(() => publicGroups.value
  .filter(group => !draft.value.groups.some(target => target.group_id === group.id && isListed(target)))
  .map(group => ({ value: group.id, label: `${platformLabel(group.platform)} · ${group.name}` })))
const sections = computed(() => {
  const order = listPlatformIds()
  const rows = publicGroups.value.flatMap(group => {
    const target = draft.value.groups.find(item => item.group_id === group.id && isListed(item))
    return target ? [{ group, target }] : []
  }).sort((a, b) => a.group.name.localeCompare(b.group.name))
  const platforms = [...new Set(rows.map(row => row.group.platform))].sort((a, b) => (order.indexOf(a) < 0 ? 999 : order.indexOf(a)) - (order.indexOf(b) < 0 ? 999 : order.indexOf(b)))
  return platforms.map(platform => ({ platform, rows: rows.filter(row => row.group.platform === platform) }))
})
function addGroup() {
  const id = selectedGroupId.value
  if (typeof id !== 'number' || !addOptions.value.some(option => option.value === id)) return
  const existing = draft.value.groups.find(target => target.group_id === id)
  if (existing) { existing.listed = true; existing.enabled = false }
  else draft.value.groups.push({ group_id: id, model: '', enabled: false, listed: true })
  selectedGroupId.value = null
}
function removeGroup(target: MonitorV3GroupProbeConfig['groups'][number]) {
  target.listed = false
  target.enabled = false
}
</script>
