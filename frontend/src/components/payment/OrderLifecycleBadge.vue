<template>
  <span class="badge whitespace-nowrap" :class="tone">{{ t(`payment.orderOps.${kind}.${value.toLowerCase()}`) }}</span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FulfillmentStatus, PaymentFactStatus } from '@/types/payment'
const props = defineProps<{ kind: 'payment' | 'fulfillment'; value: PaymentFactStatus | FulfillmentStatus }>()
const { t } = useI18n()
const tone = computed(() => {
  if (['PAID', 'FULFILLED'].includes(props.value)) return 'badge-success'
  if (['FAILED', 'MANUAL_REVIEW'].includes(props.value)) return 'badge-danger'
  if (props.value === 'PENDING') return 'badge-warning'
  return 'badge-secondary'
})
</script>
