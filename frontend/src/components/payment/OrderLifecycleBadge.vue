<template>
  <span class="badge whitespace-nowrap" :class="tone">{{ t(`payment.orderOps.${kind}.${value.toLowerCase()}`) }}</span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FulfillmentStatus, PaymentFactStatus, RefundEntitlementStatus } from '@/types/payment'
const props = defineProps<{
  kind: 'payment' | 'fulfillment' | 'refundEntitlement'
  value: PaymentFactStatus | FulfillmentStatus | RefundEntitlementStatus
}>()
const { t } = useI18n()
const tone = computed(() => {
  if (['PAID', 'FULFILLED', 'RECLAIMED'].includes(props.value)) return 'badge-success'
  if (['FAILED', 'MANUAL_REVIEW', 'HISTORICAL_UNVERIFIED'].includes(props.value)) return 'badge-danger'
  if (['PENDING', 'RECLAIMING'].includes(props.value)) return 'badge-warning'
  return 'badge-secondary'
})
</script>
