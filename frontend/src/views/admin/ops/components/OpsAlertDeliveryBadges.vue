<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AlertEvent, OpsAlertDeliveryStatus } from '@/api/admin/ops'

const props = defineProps<{ event: AlertEvent }>()
const { t } = useI18n()

const channels = computed(() => [
  { name: 'email', state: props.event.email_delivery_status ?? (props.event.email_sent ? 'sent' : 'unknown') },
  { name: 'feishu', state: props.event.feishu_delivery_status ?? (props.event.feishu_sent ? 'sent' : 'unknown') }
] as const)

const colors: Record<OpsAlertDeliveryStatus, string> = {
  disabled: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300',
  pending: 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300',
  sent: 'bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-300',
  failed: 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-300',
  unknown: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
}
</script>

<template>
  <div class="flex flex-wrap gap-1.5 text-[11px]">
    <span
      v-for="channel in channels"
      :key="channel.name"
      class="whitespace-nowrap rounded-md px-2 py-1 font-medium"
      :class="colors[channel.state]"
      :data-notification-channel="channel.name"
      :data-notification-state="channel.state"
    >{{ t(`admin.ops.alertEvents.notifications.${channel.name}`) }} · {{ t(`admin.ops.alertEvents.notifications.${channel.state}`) }}</span>
  </div>
</template>
