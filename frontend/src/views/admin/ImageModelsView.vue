<script setup lang="ts">
import { onBeforeRouteLeave } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ImageGroupSettings from './imageModels/ImageGroupSettings.vue'
import ImageGroupModels from './imageModels/ImageGroupModels.vue'
import ImageGroupAccounts from './imageModels/ImageGroupAccounts.vue'
import { useImageGroup } from './imageModels/useImageGroup'
const { t } = useI18n()
const { groups, imageGroups, group, draft, catalogue, selectedId, loading, saving, error, saved, dirty,
  select, save, loadGroups, refreshCatalogue } = useImageGroup()
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
  if (selectedId.value !== null) void select(selectedId.value)
  else void loadGroups()
}
onBeforeRouteLeave(() => canLeave())
</script>

<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6 text-gray-900 dark:text-gray-100">
      <h1 class="text-xl font-semibold lg:hidden">{{ t('admin.imageModels.title') }}</h1>
      <div class="flex flex-wrap items-end gap-3">
        <label class="w-full min-w-0 shrink-0 space-y-2 sm:w-72">
          <span class="text-sm font-medium">{{ t('admin.imageModels.selectGroup') }}</span>
          <select :value="selectedId" class="input" :disabled="saving || !imageGroups.length" @change="changeGroup">
            <option v-for="item in imageGroups" :key="item.id" :value="item.id">{{ item.name }} · {{ item.platform }}{{ item.status === 'inactive' ? ` · ${t('admin.imageModels.inactive')}` : '' }}</option>
          </select>
        </label>
        <div class="flex w-full flex-wrap gap-3 sm:w-auto">
          <button type="button" class="btn btn-secondary" :disabled="loading || saving" @click="reload">{{ t('common.refresh') }}</button>
          <RouterLink to="/admin/groups" class="btn btn-secondary">{{ t('admin.imageModels.manageGroups') }}</RouterLink>
          <RouterLink to="/image-studio" class="btn btn-secondary">{{ t('admin.imageModels.openStudio') }}</RouterLink>
        </div>
      </div>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
      <p v-if="loading" role="status" class="py-12 text-center text-sm text-gray-500">{{ t('common.loading') }}</p>
      <p v-else-if="!imageGroups.length && !error" class="card p-8 text-center text-sm text-gray-500">{{ t('admin.imageModels.noGroups') }}</p>
      <template v-else-if="group && draft && catalogue">
        <form class="card p-5 sm:p-6" @submit.prevent="save">
          <fieldset :disabled="saving" class="min-w-0 space-y-6">
            <ImageGroupSettings v-model="draft" :platform="group.platform" :rate="group.rate_multiplier" />
            <ImageGroupModels v-model="draft" :catalogue="catalogue" :dirty="dirty" />
            <div class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-200 pt-5 dark:border-dark-600">
              <p role="status" class="text-sm" :class="dirty ? 'text-amber-700 dark:text-amber-300' : 'text-gray-500 dark:text-gray-400'">
                {{ t(dirty ? 'admin.imageModels.unsaved' : saved ? 'admin.imageModels.saved' : 'admin.imageModels.currentSettings') }}
              </p>
              <button type="submit" class="btn btn-primary" :disabled="saving || !dirty">{{ t(saving ? 'common.saving' : 'admin.imageModels.save') }}</button>
            </div>
          </fieldset>
        </form>
        <div class="card p-5 sm:p-6">
          <ImageGroupAccounts :key="group.id" :group-id="group.id" :groups="groups" :candidates="catalogue.candidates" @changed="refreshCatalogue" />
        </div>
      </template>
    </div>
  </AppLayout>
</template>
