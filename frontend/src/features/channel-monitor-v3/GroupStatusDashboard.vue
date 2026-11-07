<template>
  <section class="mx-auto w-full max-w-[1680px] pb-10" data-testid="model-status-dashboard">
    <header class="flex flex-wrap items-start justify-between gap-5">
      <div><h1 class="text-2xl font-bold tracking-tight text-gray-900 dark:text-dark-50">{{ t('channelMonitorV3.cards.title') }}</h1><p class="mt-2 text-sm text-gray-500 dark:text-dark-300">{{ t('channelMonitorV3.cards.description') }}</p></div>
      <div class="flex items-center gap-3">
        <button v-if="auth.isAdmin" type="button" class="btn btn-secondary btn-sm" :aria-expanded="manageOpen" aria-controls="model-status-controls" data-testid="status-manage" @click="manageOpen = !manageOpen"><Icon name="edit" size="sm" />{{ t('channelMonitorV3.cards.manage') }}</button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="load"><Icon name="refresh" size="sm" />{{ t('common.refresh') }}</button>
      </div>
    </header>
    <GroupStatusControls v-if="auth.isAdmin && manageOpen" id="model-status-controls" class="mt-6" @saved="load" @close="manageOpen = false" />
    <div class="my-7 flex flex-wrap items-center justify-between gap-3">
      <label class="relative block w-full sm:w-72"><span class="sr-only">{{ t('channelMonitorV3.cards.search') }}</span><Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-3 text-gray-400" /><input v-model="query" type="search" class="input pl-9" :placeholder="t('channelMonitorV3.cards.search')" /></label>
      <span class="text-xs text-gray-500 dark:text-dark-300" aria-live="off">{{ t('channelMonitorV3.cards.refreshIn', { seconds: remaining }) }}</span>
    </div>
    <p v-if="error" class="mb-6 rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/40 dark:text-red-200" role="alert">{{ t('channelMonitorV3.cards.loadFailed') }}</p>
    <div v-if="loading && !data" class="grid gap-5 md:grid-cols-2 xl:grid-cols-3" aria-busy="true"><div v-for="n in 3" :key="n" class="h-80 animate-pulse rounded-2xl bg-gray-200 dark:bg-dark-800"></div></div>
    <p v-else-if="sections.length === 0" class="py-16 text-center text-sm text-gray-500 dark:text-dark-300">{{ t(query ? 'channelMonitorV3.cards.noMatch' : 'channelMonitorV3.cards.empty') }}</p>
    <section v-for="section in sections" :key="section.platform" class="mb-9" :aria-label="section.label">
      <h2 class="mb-4 flex items-center gap-2.5 text-lg font-semibold text-gray-900 dark:text-dark-100"><PlatformIcon :platform="section.platform" size="lg" />{{ section.label }}<span class="rounded-full bg-gray-200 px-2 py-0.5 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-dark-200">{{ section.groups.length }}</span></h2>
      <div class="grid items-start gap-5 md:grid-cols-2 xl:grid-cols-3"><GroupStatusCard v-for="group in section.groups" :key="group.id" :group="group" /></div>
    </section>
    <details class="mt-3 max-w-3xl text-xs leading-6 text-gray-500 dark:text-dark-300"><summary class="w-fit cursor-pointer rounded focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500">{{ t('channelMonitorV3.cards.rules') }}</summary><p class="mt-2">{{ t('channelMonitorV3.cards.rulesDetail') }}</p></details>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { getModelStatus, type ModelStatusGroup, type ModelStatusResponse } from '@/api/modelStatus'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { platformLabel } from '@/utils/platformColors'
import GroupStatusCard from './GroupStatusCard.vue'
import GroupStatusControls from './GroupStatusControls.vue'

const { t } = useI18n()
const auth = useAuthStore()
const app = useAppStore()
const data = ref<ModelStatusResponse | null>(null)
const loading = ref(false)
const error = ref(false)
const query = ref('')
const manageOpen = ref(false)
const remaining = ref(60)
const controller = new AbortController()
let timer: ReturnType<typeof setInterval> | undefined
const sections = computed(() => {
  const groups = new Map<string, ModelStatusGroup[]>()
  const term = query.value.trim().toLocaleLowerCase()
  for (const group of data.value?.groups ?? []) {
    if (term && !`${group.name} ${group.availability?.probe_model ?? ''}`.toLocaleLowerCase().includes(term)) continue
    const list = groups.get(group.platform) ?? []
    list.push(group)
    groups.set(group.platform, list)
  }
  const order = ['openai', 'anthropic', 'deepseek', 'gemini', 'kimi', 'minimax', 'zhipu']
  return [...groups].map(([platform, items]) => ({ platform, label: platform === 'openai' ? 'GPT' : platform === 'anthropic' ? 'Claude' : platformLabel(platform), groups: items })).sort((a, b) => (order.includes(a.platform) ? order.indexOf(a.platform) : order.length) - (order.includes(b.platform) ? order.indexOf(b.platform) : order.length))
})
async function load() {
  if (loading.value || document.hidden) return
  loading.value = true
  try {
    const next = await getModelStatus(controller.signal)
    if (controller.signal.aborted) return
    data.value = next
    error.value = false
  } catch {
    if (controller.signal.aborted) return
    error.value = true
    if (data.value) data.value = { ...data.value, groups: data.value.groups.map(group => ({ ...group, availability: null })) }
  } finally { if (!controller.signal.aborted) { loading.value = false; remaining.value = 60 } }
}
function visible() { if (!document.hidden) void load() }
onMounted(() => {
  void app.fetchPublicSettings()
  void load()
  timer = setInterval(() => {
    if (document.hidden || loading.value) return
    remaining.value--
    if (remaining.value <= 0) void load()
  }, 1000)
  document.addEventListener('visibilitychange', visible)
})
onBeforeUnmount(() => { controller.abort(); clearInterval(timer); document.removeEventListener('visibilitychange', visible) })
</script>
