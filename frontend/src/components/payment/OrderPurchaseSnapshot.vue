<template>
  <section class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600">
    <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('payment.orderOps.snapshot') }}</h3>
    <p v-if="!order.product_snapshot" class="text-sm text-gray-600 dark:text-gray-300">{{ t('payment.orderOps.legacySnapshot') }}</p>
    <template v-else>
      <p class="break-words text-sm font-medium">{{ purchaseName(order) || t(`payment.admin.${order.order_type}Order`) }}</p>
      <p v-if="order.product_snapshot.description" class="break-words text-sm text-gray-600 dark:text-gray-300">{{ order.product_snapshot.description }}</p>
      <ul v-if="order.product_snapshot.features?.length" class="list-inside list-disc space-y-1 text-sm text-gray-600 dark:text-gray-300"><li v-for="feature in order.product_snapshot.features" :key="feature">{{ feature }}</li></ul>
      <dl class="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
        <div v-if="order.product_snapshot.price != null"><dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.listPrice') }}</dt><dd>{{ order.product_snapshot.currency || (order.order_type === 'balance' ? order.currency : 'USD') }} {{ order.product_snapshot.price.toFixed(2) }}</dd></div>
        <template v-for="period in ['daily', 'weekly', 'monthly'] as const" :key="period"><div v-if="order.product_snapshot[`${period}_limit_usd`] != null"><dt class="text-gray-500 dark:text-gray-400">{{ t(`payment.orderOps.${period}Quota`) }}</dt><dd>{{ order.product_snapshot[`${period}_limit_usd`]! > 0 ? `$${order.product_snapshot[`${period}_limit_usd`]}` : t('payment.planCard.unlimited') }}</dd></div></template>

        <div v-if="order.product_snapshot.subscription_days != null">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.duration') }}</dt>
          <dd>{{ t('payment.orderOps.days', { count: order.product_snapshot.subscription_days }) }}</dd>
        </div>
        <div v-if="order.product_snapshot.credited_amount != null">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.creditedAmount') }}</dt>
          <dd>${{ order.product_snapshot.credited_amount.toFixed(2) }}</dd>
        </div>
        <div v-if="benefits?.balance_bonus">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.balanceBonus') }}</dt>
          <dd>${{ benefits.balance_bonus.toFixed(2) }}</dd>
        </div>
        <div v-if="benefits?.concurrency">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.concurrency') }}</dt>
          <dd>{{ benefits.concurrency }}</dd>
        </div>
        <div v-if="benefits?.reset_card_count">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.resetCards') }}</dt>
          <dd>{{ benefits.reset_card_count }}<span v-if="benefits.reset_card_expiry_days"> · {{ t('payment.orderOps.days', { count: benefits.reset_card_expiry_days }) }}</span></dd>
        </div>
      </dl>
      <p v-if="benefits?.message" class="break-words text-sm text-gray-600 dark:text-gray-300">{{ benefits.message }}</p>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.snapshotHelp') }}</p>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PaymentOrder } from '@/types/payment'
import { purchaseName } from './orderPresentation'
const props = defineProps<{ order: PaymentOrder }>()
const { t } = useI18n()
const benefits = computed(() => props.order.product_snapshot?.entitlements)
</script>
