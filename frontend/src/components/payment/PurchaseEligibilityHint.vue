<template>
  <span v-if="eligibility?.can_purchase === false" class="flex items-start gap-1.5 text-xs leading-relaxed text-gray-600 dark:text-dark-300">
    <Icon name="lock" size="xs" class="mt-0.5 shrink-0" />
    <span>{{ eligibility.reason === 'minimum_recharge'
      ? t('payment.eligibility.minimum', { required: format(eligibility.required_total_recharge), current: format(eligibility.current_total_recharge) })
      : t('payment.eligibility.unavailable') }}</span>
  </span>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { PurchaseEligibility } from '@/types/payment'
defineProps<{ eligibility?: PurchaseEligibility }>()
const { t } = useI18n()
const format = (amount?: number) => Number.isFinite(amount) ? Number(amount).toLocaleString('zh-CN', { maximumFractionDigits: 2 }) : '0'
</script>
