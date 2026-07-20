<template>
  <div class="space-y-6">
    <div class="card">
      <div class="flex flex-wrap items-start justify-between gap-3 border-b border-gray-100 px-6 py-4 dark:border-dark-700">
        <div>
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
            {{ t('admin.settings.attachmentGatewayR2.title') }}
          </h2>
          <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.settings.attachmentGatewayR2.description') }}
          </p>
        </div>
        <span
          class="rounded-full px-2.5 py-1 text-xs font-medium"
          :class="statusClass"
          data-testid="attachment-r2-status"
        >
          {{ statusText }}
        </span>
      </div>

      <div v-if="loading" class="flex items-center gap-2 p-6 text-sm text-gray-500 dark:text-gray-400">
        <div class="h-4 w-4 animate-spin rounded-full border-b-2 border-primary-600"></div>
        {{ t('common.loading') }}
      </div>

      <div v-else class="space-y-5 p-6">
        <div class="rounded-lg border border-blue-200 bg-blue-50 p-4 text-sm text-blue-800 dark:border-blue-900 dark:bg-blue-900/20 dark:text-blue-200">
          <p class="font-medium">{{ t('admin.settings.attachmentGatewayR2.safetyTitle') }}</p>
          <p class="mt-1">{{ t('admin.settings.attachmentGatewayR2.safetyHint') }}</p>
        </div>

        <div class="flex items-center justify-between gap-4">
          <div>
            <label class="font-medium text-gray-900 dark:text-white">
              {{ t('admin.settings.attachmentGatewayR2.enabled') }}
            </label>
            <p class="text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.attachmentGatewayR2.enabledHint') }}
            </p>
          </div>
          <Toggle v-model="form.enabled" data-testid="attachment-r2-enabled" />
        </div>

        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div>
            <label class="input-label">{{ t('admin.settings.attachmentGatewayR2.endpoint') }}</label>
            <input
              v-model.trim="form.endpoint"
              class="input w-full"
              placeholder="https://<account_id>.r2.cloudflarestorage.com"
              autocomplete="off"
            />
          </div>
          <div>
            <label class="input-label">{{ t('admin.settings.attachmentGatewayR2.region') }}</label>
            <input v-model.trim="form.region" class="input w-full" placeholder="auto" autocomplete="off" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.settings.attachmentGatewayR2.bucket') }}</label>
            <input v-model.trim="form.bucket" class="input w-full" autocomplete="off" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.settings.attachmentGatewayR2.prefix') }}</label>
            <input v-model.trim="form.prefix" class="input w-full" placeholder="sub2api/" autocomplete="off" />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.attachmentGatewayR2.prefixHint') }}
            </p>
          </div>
          <div>
            <label class="input-label">{{ t('admin.settings.attachmentGatewayR2.accessKeyId') }}</label>
            <input v-model.trim="form.access_key_id" class="input w-full" autocomplete="off" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.settings.attachmentGatewayR2.secretAccessKey') }}</label>
            <input
              v-model="form.secret_access_key"
              type="password"
              class="input w-full"
              :placeholder="secretConfigured ? t('admin.settings.attachmentGatewayR2.secretConfigured') : ''"
              autocomplete="new-password"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.attachmentGatewayR2.secretHint') }}
            </p>
          </div>
          <div>
            <label class="input-label">{{ t('admin.settings.attachmentGatewayR2.presignExpiry') }}</label>
            <input
              v-model.number="form.presign_expiry_minutes"
              type="number"
              min="5"
              max="10080"
              class="input w-full"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.attachmentGatewayR2.presignExpiryHint') }}
            </p>
          </div>
          <label class="mt-6 inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
            <input v-model="form.force_path_style" type="checkbox" />
            <span>{{ t('admin.settings.attachmentGatewayR2.forcePathStyle') }}</span>
          </label>
        </div>

        <div class="rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-900/20 dark:text-amber-200">
          <p>{{ t('admin.settings.attachmentGatewayR2.permissionHint') }}</p>
          <p class="mt-1">{{ t('admin.settings.attachmentGatewayR2.probeHint') }}</p>
          <p class="mt-1">{{ t('admin.settings.attachmentGatewayR2.lifecycleHint') }}</p>
        </div>

        <div class="flex flex-wrap gap-2">
          <button type="button" class="btn btn-secondary" :disabled="testing || saving" @click="testConnection">
            {{ testing ? t('common.loading') : t('admin.settings.attachmentGatewayR2.testConnection') }}
          </button>
          <button type="button" class="btn btn-primary" :disabled="saving || testing" @click="saveConfig">
            {{ saving ? t('common.loading') : t('common.save') }}
          </button>
        </div>
      </div>
    </div>

    <TotpStepUpDialog :controller="stepUp" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import type { AttachmentGatewayR2Config } from '@/api/admin/attachmentGateway'
import Toggle from '@/components/common/Toggle.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpBlocked, isStepUpCancelled, stepUpBlockReason, useStepUp } from '@/composables/useStepUp'
import { useAppStore } from '@/stores'

const { t } = useI18n()
const appStore = useAppStore()
const stepUp = useStepUp()
const loading = ref(true)
const saving = ref(false)
const testing = ref(false)
const secretConfigured = ref(false)
const configured = ref(false)

const form = reactive<AttachmentGatewayR2Config>({
  enabled: false,
  endpoint: '',
  region: 'auto',
  bucket: '',
  access_key_id: '',
  secret_access_key: '',
  prefix: '',
  force_path_style: false,
  presign_expiry_minutes: 60,
})

const statusText = computed(() => {
  if (!configured.value) return t('admin.settings.attachmentGatewayR2.statusUnconfigured')
  if (!form.enabled) return t('admin.settings.attachmentGatewayR2.statusDisabled')
  return t('admin.settings.attachmentGatewayR2.statusEnabled')
})

const statusClass = computed(() => {
  if (!configured.value) return 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300'
  if (!form.enabled) return 'bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-200'
  return 'bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-200'
})

function applyConfig(config: AttachmentGatewayR2Config) {
  form.enabled = Boolean(config.enabled)
  form.endpoint = config.endpoint || ''
  form.region = config.region || 'auto'
  form.bucket = config.bucket || ''
  form.access_key_id = config.access_key_id || ''
  form.secret_access_key = ''
  form.prefix = config.prefix || ''
  form.force_path_style = Boolean(config.force_path_style)
  form.presign_expiry_minutes = config.presign_expiry_minutes || 60
  secretConfigured.value = Boolean(config.secret_configured)
  configured.value = Boolean(config.configured)
}

function requestConfig(): AttachmentGatewayR2Config {
  return {
    enabled: form.enabled,
    endpoint: form.endpoint,
    region: form.region,
    bucket: form.bucket,
    access_key_id: form.access_key_id,
    secret_access_key: form.secret_access_key || '',
    prefix: form.prefix,
    force_path_style: form.force_path_style,
    presign_expiry_minutes: Number(form.presign_expiry_minutes) || 60,
  }
}

function errorMessage(error: unknown): string {
  const candidate = error as { message?: string; response?: { data?: { message?: string } } }
  return candidate.response?.data?.message || candidate.message || t('errors.networkError')
}

function reportStepUpBlocked(error: unknown): boolean {
  if (!isStepUpBlocked(error)) return false
  appStore.showError(
    stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
      ? t('stepUp.adminApiKeyForbidden')
      : t('stepUp.notEnabled'),
  )
  return true
}

async function loadConfig() {
  loading.value = true
  try {
    applyConfig(await adminAPI.attachmentGateway.getR2Config())
  } catch (error) {
    appStore.showError(errorMessage(error))
  } finally {
    loading.value = false
  }
}

async function saveConfig() {
  saving.value = true
  try {
    const saved = await stepUp.run(() => adminAPI.attachmentGateway.updateR2Config(requestConfig()))
    applyConfig(saved)
    appStore.showSuccess(t('admin.settings.attachmentGatewayR2.saved'))
  } catch (error) {
    if (isStepUpCancelled(error) || reportStepUpBlocked(error)) return
    appStore.showError(errorMessage(error))
  } finally {
    saving.value = false
  }
}

async function testConnection() {
  testing.value = true
  try {
    const result = await adminAPI.attachmentGateway.testR2Connection(requestConfig())
    if (result.ok) {
      appStore.showSuccess(result.message || t('admin.settings.attachmentGatewayR2.testSuccess'))
    } else {
      appStore.showError(result.message || t('admin.settings.attachmentGatewayR2.testFailed'))
    }
  } catch (error) {
    appStore.showError(errorMessage(error))
  } finally {
    testing.value = false
  }
}

onMounted(loadConfig)
</script>
