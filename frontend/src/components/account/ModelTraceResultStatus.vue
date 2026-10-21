<template>
  <div class="flex flex-wrap items-center gap-1.5 text-xs" role="status">
    <span
      v-if="record.match_status"
      class="rounded px-1.5 py-0.5 font-medium"
      :class="statusClass"
    >{{ t(`admin.modelTrace.verdict.${record.match_status}`) }}</span>
    <span v-if="record.account_paused" class="text-rose-700 dark:text-rose-300">
      {{ t('admin.modelTrace.callsPaused') }}
    </span>
    <span
      v-if="record.notification_status"
      :class="record.notification_status === 'failed' ? 'text-rose-700 dark:text-rose-300' : 'text-gray-600 dark:text-gray-300'"
    >{{ t(`admin.modelTrace.notification.${record.notification_status}`) }}</span>
    <span v-if="record.action_error" class="text-rose-700 dark:text-rose-300" :title="record.action_error">
      {{ t('admin.modelTrace.actionError') }}
    </span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ModelTraceRecord } from '@/api/admin/modeltrace'

const props = defineProps<{ record: ModelTraceRecord }>()
const { t } = useI18n()
const statusClass = computed(() => ({
  match: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300',
  mismatch: 'bg-rose-50 text-rose-700 dark:bg-rose-900/30 dark:text-rose-300',
  uncovered: 'bg-amber-50 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300',
  inconclusive: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300',
})[props.record.match_status || 'inconclusive'])
</script>
