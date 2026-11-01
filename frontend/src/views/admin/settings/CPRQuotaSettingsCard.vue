<template>
  <section class="card p-6" data-testid="cpr-quota-settings-card">
    <div class="mb-4">
      <h3 class="text-base font-semibold text-gray-900 dark:text-white">
        {{ t('admin.settings.cprQuota.title') }}
      </h3>
      <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.settings.cprQuota.description') }}
      </p>
    </div>

    <div v-if="loading" class="flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
      <span class="h-4 w-4 animate-spin rounded-full border-2 border-primary-600 border-t-transparent" />
      {{ t('common.loading') }}
    </div>

    <div v-else class="space-y-4">
      <div class="grid gap-4 md:grid-cols-2">
        <div>
          <label for="cpr-quota-base-url" class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.settings.cprQuota.baseUrl') }}
          </label>
          <input
            id="cpr-quota-base-url"
            v-model="baseUrl"
            type="text"
            class="input w-full font-mono"
            placeholder="http://172.26.0.217:18080"
          >
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.settings.cprQuota.baseUrlHint') }}
          </p>
        </div>

        <div>
          <label for="cpr-quota-admin-key" class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.settings.cprQuota.adminApiKey') }}
          </label>
          <input
            id="cpr-quota-admin-key"
            v-model="adminApiKey"
            type="password"
            autocomplete="new-password"
            class="input w-full font-mono"
            :disabled="clearAdminApiKey"
            :placeholder="adminApiKeyConfigured ? t('admin.settings.cprQuota.adminApiKeyConfigured') : 'admin-…'"
          >
          <label v-if="adminApiKeyConfigured" class="mt-1 flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400">
            <input v-model="clearAdminApiKey" type="checkbox" class="rounded border-gray-300">
            {{ t('admin.settings.cprQuota.adminApiKeyClear') }}
          </label>
        </div>
      </div>

      <div class="flex justify-end">
        <button
          type="button"
          class="btn btn-primary"
          data-testid="cpr-quota-save"
          :disabled="saving"
          @click="save"
        >
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import type { CPRQuotaSettings, UpdateCPRQuotaSettingsRequest } from '@/api/admin/settings'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)

const baseUrl = ref('')
const adminApiKey = ref('')
const adminApiKeyConfigured = ref(false)
const clearAdminApiKey = ref(false)

function applySettings(settings: CPRQuotaSettings) {
  baseUrl.value = settings.base_url
  adminApiKeyConfigured.value = settings.admin_api_key_configured
  adminApiKey.value = ''
  clearAdminApiKey.value = false
}

async function load() {
  loading.value = true
  try {
    applySettings(await adminAPI.settings.getCPRQuotaSettings())
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.settings.cprQuota.loadFailed')))
  } finally {
    loading.value = false
  }
}

async function save() {
  const payload: UpdateCPRQuotaSettingsRequest = { base_url: baseUrl.value.trim() }
  if (clearAdminApiKey.value) {
    payload.admin_api_key = ''
  } else if (adminApiKey.value.trim()) {
    payload.admin_api_key = adminApiKey.value.trim()
  }
  saving.value = true
  try {
    applySettings(await adminAPI.settings.updateCPRQuotaSettings(payload))
    appStore.showSuccess(t('admin.settings.cprQuota.saved'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.settings.cprQuota.saveFailed')))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
