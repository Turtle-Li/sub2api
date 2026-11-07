<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ImageGroupSettings from './imageModels/ImageGroupSettings.vue'
import ImageGroupModels from './imageModels/ImageGroupModels.vue'
import ImageGroupAccounts from './imageModels/ImageGroupAccounts.vue'
import ImageModelDirectory from './imageModels/ImageModelDirectory.vue'
import { useImageGroup } from './imageModels/useImageGroup'
const { t } = useI18n()
const { groups, imageGroups, group, draft, catalogue, selectedId, loading, saving, error, saved, dirty,
  select, save, loadGroups, refreshCatalogue } = useImageGroup({ autoSelect: false })
const selectedModel = ref('')
const revision = ref(0)
const configuration = ref<HTMLElement | null>(null)
async function configure(id: number, model: string) {
  if (!canLeave()) return
  selectedModel.value = model
  await select(id)
  await nextTick()
  configuration.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
async function saveConfiguration() {
  await save()
  if (saved.value) revision.value++
}
async function accountChanged() {
  await refreshCatalogue()
  revision.value++
}
function canLeave() {
  return !saving.value && (!dirty.value || window.confirm(t('admin.imageModels.discard')))
}
function changeGroup(event: Event) {
  if (!(event.target instanceof HTMLSelectElement)) return
  const id = Number(event.target.value)
  if (canLeave()) void select(id)
  else event.target.value = String(selectedId.value)
}
function reload() {
  if (!canLeave()) return
  revision.value++
  if (selectedId.value !== null) void select(selectedId.value)
  else void loadGroups()
}
onBeforeRouteLeave(() => canLeave())
</script>

<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6 text-gray-900 dark:text-gray-100">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-sm text-gray-500 dark:text-gray-400">统一管理各平台的生图模型、价格和账号。</p>
        <div class="flex w-full flex-wrap gap-3 sm:w-auto">
          <button type="button" class="btn btn-secondary" :disabled="loading || saving" @click="reload">{{ t('common.refresh') }}</button>
          <RouterLink to="/admin/groups" class="btn btn-secondary">{{ t('admin.imageModels.manageGroups') }}</RouterLink>
          <RouterLink to="/image-studio" class="btn btn-secondary">{{ t('admin.imageModels.openStudio') }}</RouterLink>
        </div>
      </div>
      <ImageModelDirectory :groups="groups" :revision="revision" @configure="configure" />
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
      <p v-if="loading" role="status" class="py-12 text-center text-sm text-gray-500">{{ t('common.loading') }}</p>
      <p v-else-if="!imageGroups.length && !error" class="card p-8 text-center text-sm text-gray-500">{{ t('admin.imageModels.noGroups') }}</p>
      <template v-else-if="group && draft && catalogue">
        <section ref="configuration" class="scroll-mt-24 space-y-4">
        <div class="flex flex-wrap items-start justify-between gap-3"><div><h2 class="text-lg font-semibold">{{ selectedModel || t('admin.imageModels.models') }}</h2><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">当前配置来源：{{ group.name }}。价格与权限沿用此通道现有计费。</p></div><button type="button" class="btn btn-secondary" :disabled="saving" @click="() => { if (canLeave()) { selectedId = null; group = null; draft = null; catalogue = null } }">收起配置</button></div>
        <form class="card p-5 sm:p-6" @submit.prevent="saveConfiguration">
          <fieldset :disabled="saving" class="min-w-0 space-y-6">
            <p class="rounded-lg bg-amber-50 p-3 text-sm text-amber-900 dark:bg-amber-900/20 dark:text-amber-200">此价格配置由通道内的模型共用。需要不同单价的模型，请在高级路由中使用独立生图分组。</p>
            <ImageGroupSettings v-model="draft" :platform="group.platform" :rate="group.rate_multiplier" />
            <details class="rounded-lg border border-gray-200 p-4 dark:border-dark-600"><summary class="cursor-pointer text-sm font-medium">高级：内部分组路由与模型规则</summary><label class="mt-4 block max-w-sm space-y-2 text-sm"><span>{{ t('admin.imageModels.selectGroup') }}</span><select :value="selectedId" class="input" :disabled="saving || !imageGroups.length" @change="changeGroup"><option v-for="item in imageGroups.filter(item => item.allow_image_generation || item.allow_batch_image_generation || item.id === selectedId)" :key="item.id" :value="item.id">{{ item.name }} · {{ item.platform }}{{ item.status === 'inactive' ? ` · ${t('admin.imageModels.inactive')}` : '' }}</option></select></label><ImageGroupModels v-model="draft" :catalogue="catalogue" :dirty="dirty" /></details>
            <div class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-200 pt-5 dark:border-dark-600">
              <p role="status" class="text-sm" :class="dirty ? 'text-amber-700 dark:text-amber-300' : 'text-gray-500 dark:text-gray-400'">
                {{ t(dirty ? 'admin.imageModels.unsaved' : saved ? 'admin.imageModels.saved' : 'admin.imageModels.currentSettings') }}
              </p>
              <button type="submit" class="btn btn-primary" :disabled="saving || !dirty">{{ t(saving ? 'common.saving' : 'admin.imageModels.save') }}</button>
            </div>
          </fieldset>
        </form>
        <div class="card p-5 sm:p-6">
          <ImageGroupAccounts :key="group.id" :group-id="group.id" :groups="groups" :candidates="catalogue.candidates" @changed="accountChanged" />
        </div>
        </section>
      </template>
    </div>
  </AppLayout>
</template>
