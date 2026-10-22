<template>
  <section class="card p-6" aria-labelledby="paid-fallback-ops-heading">
    <h3 id="paid-fallback-ops-heading" class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.paidFallback.operations') }}</h3>
    <p class="mt-1 max-w-3xl text-sm text-gray-600 dark:text-gray-400">{{ t('admin.paidFallback.notificationHint') }}</p>
    <p v-if="loading" role="status" class="mt-4 text-sm">{{ t('common.loading') }}</p>
    <div v-if="loadError" role="alert" class="mt-4 text-sm text-red-700 dark:text-red-300">
      <p>{{ loadError }}</p>
      <button type="button" class="btn btn-secondary mt-2" @click="load">{{ t('admin.paidFallback.retry') }}</button>
    </div>
    <fieldset v-if="loaded" :disabled="saving || loading" class="mt-5 space-y-4">
      <div class="flex items-center gap-3">
        <Toggle v-model="form.enabled" :disabled="saving || loading" :aria-label="t('admin.paidFallback.notifyEnabled')" />
        <span class="text-sm font-medium">{{ t('admin.paidFallback.notifyEnabled') }}</span>
      </div>
      <div class="grid gap-4 md:grid-cols-3">
        <div>
          <label for="paid-reminder-minutes" class="input-label">{{ t('admin.paidFallback.reminderMinutes') }}</label>
          <input id="paid-reminder-minutes" v-model.number="form.reminder_minutes" class="input" type="number" min="5" max="1440" step="1">
        </div>
        <div>
          <label for="paid-recovery-seconds" class="input-label">{{ t('admin.paidFallback.recoverySeconds') }}</label>
          <input id="paid-recovery-seconds" v-model.number="form.recovery_quiet_seconds" class="input" type="number" min="30" max="3600" step="1">
        </div>
        <div>
          <label for="paid-daily-cost" class="input-label">{{ t('admin.paidFallback.dailyCost') }}</label>
          <input id="paid-daily-cost" v-model="threshold" class="input" type="number" min="0.000001" max="1000000000" step="any" :placeholder="t('admin.paidFallback.unset')">
        </div>
      </div>
      <p class="text-sm text-gray-600 dark:text-gray-400">{{ t('admin.paidFallback.thresholdHint') }}</p>
      <p v-if="!valid" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ t('admin.paidFallback.invalidNotifications') }}</p>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p role="status" class="text-sm text-gray-600 dark:text-gray-400">{{ t(dirty ? 'admin.paidFallback.unsaved' : 'admin.paidFallback.savedState') }}</p>
        <button type="button" class="btn btn-primary" :disabled="saving || loading || !valid || !dirty" data-testid="paid-notifications-save" @click="save">{{ saving ? t('common.saving') : t('common.save') }}</button>
      </div>
    </fieldset>
    <div class="mt-7 border-t border-gray-200 pt-5 dark:border-dark-600">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h4 class="text-sm font-semibold">{{ t('admin.paidFallback.recentActivity') }}</h4>
        <button type="button" class="btn btn-secondary" :disabled="reportLoading" @click="refreshReport">{{ t('admin.paidFallback.refresh') }}</button>
      </div>
      <p class="mt-2 max-w-3xl text-sm text-gray-600 dark:text-gray-400">{{ t('admin.paidFallback.costHint') }}</p>
      <p v-if="reportError" role="alert" class="mt-3 text-sm text-red-700 dark:text-red-300">{{ reportError }}</p>
      <p v-if="reportLoading" role="status" class="mt-3 text-sm">{{ t('common.loading') }}</p>
      <template v-else-if="report">
        <p v-if="!report.attempts.length && !report.incidents.length" class="mt-4 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.paidFallback.noActivity') }}</p>
        <ul v-if="report.incidents.length" class="mt-4 divide-y divide-gray-200 dark:divide-dark-600">
          <li v-for="incident in report.incidents" :key="incident.id" class="py-3 text-sm">
            <div class="flex flex-wrap justify-between gap-2 font-medium">
              <span class="break-all">#{{ incident.group_id }} · {{ incident.public_model }}</span>
              <span>{{ t(incident.resolved_at ? 'admin.paidFallback.recovered' : 'admin.paidFallback.activeIncident') }}</span>
            </div>
            <p class="mt-1 text-gray-600 dark:text-gray-400">{{ t('admin.paidFallback.incidentCost', { attempts: incident.attempts, cost: incident.recorded_cost_usd.toFixed(8), unknown: incident.unknown_cost_attempts }) }}</p>
          </li>
        </ul>
        <div v-if="report.attempts.length" class="mt-4 overflow-x-auto">
          <table class="w-full text-left text-sm">
            <caption class="sr-only">{{ t('admin.paidFallback.recentAttempts') }}</caption>
            <thead class="border-b border-gray-200 dark:border-dark-600"><tr>
              <th class="px-2 py-2">{{ t('usage.time') }}</th><th class="px-2 py-2">{{ t('admin.usage.account') }}</th><th class="px-2 py-2">{{ t('usage.requestedModel') }}</th><th class="px-2 py-2">{{ t('admin.paidFallback.transport') }}</th><th class="px-2 py-2">{{ t('admin.paidFallback.result') }}</th>
            </tr></thead>
            <tbody><tr v-for="attempt in report.attempts" :key="attempt.id" class="border-b border-gray-100 dark:border-dark-700">
              <td class="whitespace-nowrap px-2 py-2">{{ formatDateTime(attempt.started_at) }}</td><td class="px-2 py-2">#{{ attempt.account_id }}</td><td class="max-w-xs break-all px-2 py-2">{{ attempt.public_model }}</td><td class="px-2 py-2">{{ attempt.transport }}</td><td class="px-2 py-2">{{ t(`admin.paidFallback.outcomes.${attempt.outcome}`) }} {{ attempt.http_status || '' }}</td>
            </tr></tbody>
          </table>
        </div>
      </template>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import type { PaidFallbackNotificationSettings, PaidFallbackReport } from '@/api/admin/settings'
import Toggle from '@/components/common/Toggle.vue'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'

const { t } = useI18n()
const app = useAppStore()
const form = reactive<PaidFallbackNotificationSettings>({ enabled: false, reminder_minutes: 30, recovery_quiet_seconds: 60, daily_cost_usd: null })
const threshold = ref('')
const loaded = ref(false)
const loading = ref(false)
const saving = ref(false)
const loadError = ref('')
const saved = ref('')
const report = ref<PaidFallbackReport | null>(null)
const reportLoading = ref(false)
const reportError = ref('')
const payload = computed(() => ({ ...form, daily_cost_usd: String(threshold.value).trim() === '' ? null : Number(threshold.value) }))
const valid = computed(() => Number.isInteger(form.reminder_minutes) && form.reminder_minutes >= 5 && form.reminder_minutes <= 1440 && Number.isInteger(form.recovery_quiet_seconds) && form.recovery_quiet_seconds >= 30 && form.recovery_quiet_seconds <= 3600 && (payload.value.daily_cost_usd === null || Number.isFinite(payload.value.daily_cost_usd) && payload.value.daily_cost_usd > 0 && payload.value.daily_cost_usd <= 1e9))
const dirty = computed(() => JSON.stringify(payload.value) !== saved.value)
function apply(value: PaidFallbackNotificationSettings) {
  Object.assign(form, value)
  threshold.value = value.daily_cost_usd == null ? '' : String(value.daily_cost_usd)
  saved.value = JSON.stringify(payload.value)
  loaded.value = true
}
async function load() {
  loading.value = true; loadError.value = ''
  try { apply(await adminAPI.settings.getPaidFallbackNotifications()) }
  catch (error) { loadError.value = extractApiErrorMessage(error, t('admin.paidFallback.notificationLoadFailed')) }
  finally { loading.value = false }
}
async function save() {
  if (!loaded.value || !valid.value || saving.value) return
  saving.value = true
  try { apply(await adminAPI.settings.updatePaidFallbackNotifications(payload.value)); app.showSuccess(t('admin.paidFallback.saved')) }
  catch (error) { app.showError(extractApiErrorMessage(error, t('admin.paidFallback.saveFailed'))) }
  finally { saving.value = false }
}
async function refreshReport() {
  if (reportLoading.value) return
  reportLoading.value = true; reportError.value = ''
  try { report.value = await adminAPI.settings.getPaidFallbackReport() }
  catch (error) { reportError.value = extractApiErrorMessage(error, t('admin.paidFallback.reportFailed')) }
  finally { reportLoading.value = false }
}
onMounted(() => { void load(); void refreshReport() })
</script>
