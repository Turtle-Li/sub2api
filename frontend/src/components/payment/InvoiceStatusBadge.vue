<template>
  <span class="inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium" :class="statusClass">
    {{ statusLabel }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { InvoiceStatus } from '@/types/payment'

const props = defineProps<{ status: InvoiceStatus }>()
const { t } = useI18n()

const statusClassMap: Record<InvoiceStatus, string> = {
  PENDING: 'bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300',
  PROCESSING: 'bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300',
  ISSUED: 'bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-300',
  REJECTED: 'bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-300',
}

const statusClass = computed(() => statusClassMap[props.status])
const statusLabel = computed(() => t(`payment.invoice.status.${props.status.toLowerCase()}`))
</script>
