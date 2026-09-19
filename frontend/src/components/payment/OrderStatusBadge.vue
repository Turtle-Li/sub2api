<template>
  <span
    class="inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium"
    :class="statusClass"
  >
    {{ statusLabel }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OrderStatus } from '@/types/payment'

const props = defineProps<{
  status: OrderStatus
  /** The payment service accepted cancellation, but the order is still PENDING. */
  cancellationPending?: boolean
}>()

const { t } = useI18n()

const statusMap: Record<OrderStatus, { key: string; class: string }> = {
  PENDING: { key: 'payment.status.pending', class: 'bg-amber-100 text-amber-800 dark:bg-amber-950/40 dark:text-amber-200' },
  PAID: { key: 'payment.status.paid', class: 'bg-blue-100 text-blue-800 dark:bg-blue-950/40 dark:text-blue-200' },
  RECHARGING: { key: 'payment.status.recharging', class: 'bg-cyan-100 text-cyan-800 dark:bg-cyan-950/40 dark:text-cyan-200' },
  COMPLETED: { key: 'payment.status.completed', class: 'bg-green-100 text-green-800 dark:bg-green-950/40 dark:text-green-200' },
  EXPIRED: { key: 'payment.status.expired', class: 'bg-orange-100 text-orange-800 dark:bg-orange-950/40 dark:text-orange-200' },
  CANCELLED: { key: 'payment.status.cancelled', class: 'bg-rose-100 text-rose-800 dark:bg-rose-950/40 dark:text-rose-200' },
  FAILED: { key: 'payment.status.failed', class: 'bg-red-100 text-red-800 dark:bg-red-950/40 dark:text-red-200' },
  REFUND_REQUESTED: { key: 'payment.status.refund_requested', class: 'bg-purple-100 text-purple-800 dark:bg-purple-950/40 dark:text-purple-200' },
  REFUNDING: { key: 'payment.status.refunding', class: 'bg-purple-100 text-purple-800 dark:bg-purple-950/40 dark:text-purple-200' },
  REFUND_PENDING: { key: 'payment.status.refund_pending', class: 'bg-purple-100 text-purple-800 dark:bg-purple-950/40 dark:text-purple-200' },
  REFUNDED: { key: 'payment.status.refunded', class: 'bg-purple-100 text-purple-800 dark:bg-purple-950/40 dark:text-purple-200' },
  PARTIALLY_REFUNDED: { key: 'payment.status.partially_refunded', class: 'bg-purple-100 text-purple-800 dark:bg-purple-950/40 dark:text-purple-200' },
  REFUND_FAILED: { key: 'payment.status.refund_failed', class: 'bg-red-100 text-red-800 dark:bg-red-950/40 dark:text-red-200' },
}

const displayedStatus = computed(() => {
  if (props.status === 'PENDING' && props.cancellationPending) {
    return {
      key: 'payment.orderOps.cancellationPending',
      class: 'bg-amber-100 text-amber-800 dark:bg-amber-950/40 dark:text-amber-200',
    }
  }
  return statusMap[props.status]
})

const statusLabel = computed(() => {
  const entry = displayedStatus.value
  return entry ? t(entry.key) : props.status
})

const statusClass = computed(() => {
  const entry = displayedStatus.value
  return entry?.class ?? 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-200'
})
</script>
