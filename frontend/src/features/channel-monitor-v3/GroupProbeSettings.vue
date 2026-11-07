<template>
  <section class="card !rounded-2xl p-5" data-testid="group-probe-settings">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div><h3 class="font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV3.probes.title') }}</h3><p class="mt-2 max-w-3xl text-sm text-gray-600 dark:text-dark-200">{{ t('channelMonitorV3.probes.description') }}</p></div>
      <label class="flex min-h-10 items-center gap-2 text-sm"><input v-model="draft.enabled" type="checkbox" />{{ t('channelMonitorV3.probes.enabled') }}</label>
    </div>
    <input v-model="query" type="search" class="input mt-5 max-w-md" :placeholder="t('channelMonitorV3.probes.search')" :aria-label="t('channelMonitorV3.probes.search')" />
    <p class="mt-3 text-xs leading-6 text-gray-600 dark:text-dark-200">{{ t('channelMonitorV3.probes.cost') }}</p>
    <section v-for="section in sections" :key="section.platform" class="mt-6" :aria-label="platformLabel(section.platform)">
      <h4 class="mb-3 flex items-center gap-2 text-sm font-semibold"><PlatformIcon :platform="section.platform" size="sm" />{{ platformLabel(section.platform) }} <span class="text-gray-500">{{ section.rows.length }}</span></h4>
      <div class="divide-y divide-gray-200 rounded-xl border border-gray-200 px-4 dark:divide-dark-600 dark:border-dark-600">
        <div v-for="row in section.rows" :key="row.group.id" class="py-4" :data-status-group="row.group.id">
          <div class="grid items-center gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
            <div class="min-w-0"><p class="break-words text-sm font-medium text-gray-900 dark:text-dark-100">{{ row.group.name }}</p><p class="mt-1 text-xs text-gray-500 dark:text-dark-300">{{ t('channelMonitorV3.probes.publicGroup') }}</p></div>
            <div><label :for="`status-name-${row.group.id}`" class="input-label">{{ t('channelMonitorV3.probes.displayName') }}</label><input :id="`status-name-${row.group.id}`" v-model.trim="row.target.display_name" class="input" maxlength="100" :placeholder="row.group.name" /></div>
            <label class="flex min-h-10 items-center gap-2 text-sm"><input v-model="row.target.enabled" type="checkbox" :disabled="!supportsCheck(row.group.platform)" />{{ t('channelMonitorV3.probes.groupEnabled') }}</label>
          </div>
          <details v-if="supportsCheck(row.group.platform)" class="mt-3 text-xs text-gray-600 dark:text-dark-200">
            <summary class="w-fit cursor-pointer rounded py-1 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500">{{ t('channelMonitorV3.probes.requestSettings') }}</summary>
            <div class="mt-3 max-w-xl"><label :for="`probe-model-${row.group.id}`" class="input-label">{{ t('channelMonitorV3.probes.model') }}</label><input :id="`probe-model-${row.group.id}`" v-model.trim="row.target.model" class="input" maxlength="200" :placeholder="t('channelMonitorV3.probes.modelHint')" /><p class="mt-2 leading-6">{{ t('channelMonitorV3.probes.requestHelp') }}</p></div>
          </details>
          <p v-else class="mt-3 text-xs text-gray-500 dark:text-dark-300">{{ t('channelMonitorV3.probes.passiveOnly') }}</p>
        </div>
      </div>
    </section>
    <p v-if="!sections.length" class="py-10 text-center text-sm text-gray-500">{{ t('channelMonitorV3.cards.noMatch') }}</p>
    <p class="mt-5 text-xs leading-6 text-gray-600 dark:text-dark-200">{{ t('channelMonitorV3.probes.saveHint') }}</p>
    <slot name="footer" />
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorV3GroupProbeConfig } from '@/api/channelMonitorV3'
import type { AdminGroup } from '@/types'
import { getPlatformSpec, listPlatformIds } from '@/constants/platformCatalog'
import { platformLabel } from '@/utils/platformColors'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
const draft = defineModel<MonitorV3GroupProbeConfig>({ required: true })
const props = defineProps<{ groups: AdminGroup[] }>()
const { t } = useI18n()
const query = ref('')
const supportsCheck = (platform: string) => ['openai', 'anthropic'].includes(platform) || getPlatformSpec(platform)?.cn_provider === true
const sections = computed(() => {
  const order = listPlatformIds()
  const term = query.value.trim().toLocaleLowerCase()
  const rows = props.groups.filter(g => g.status === 'active' && !g.is_exclusive).flatMap(group => {
    const target = draft.value.groups.find(item => item.group_id === group.id)
    return target && (!term || `${group.name} ${target.display_name ?? ''}`.toLocaleLowerCase().includes(term)) ? [{ group, target }] : []
  }).sort((a, b) => a.group.name.localeCompare(b.group.name))
  const platforms = [...new Set(rows.map(row => row.group.platform))].sort((a, b) => (order.indexOf(a) < 0 ? 999 : order.indexOf(a)) - (order.indexOf(b) < 0 ? 999 : order.indexOf(b)))
  return platforms.map(platform => ({ platform, rows: rows.filter(row => row.group.platform === platform) }))
})
</script>
