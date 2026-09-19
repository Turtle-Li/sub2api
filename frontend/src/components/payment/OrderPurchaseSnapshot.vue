<template>
  <section :class="['space-y-3', props.showTitle && 'border-t border-gray-200 pt-4 dark:border-dark-600']">
    <h3 v-if="props.showTitle" class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('payment.orderOps.snapshot') }}</h3>
    <p v-if="!order.product_snapshot" class="text-sm text-gray-600 dark:text-gray-300">{{ t('payment.orderOps.legacySnapshot') }}</p>
    <template v-else>
      <p class="break-words text-sm font-medium">{{ purchaseName(order) || t(`payment.admin.${order.order_type}Order`) }}</p>
      <p v-if="order.product_snapshot.description" class="break-words text-sm text-gray-600 dark:text-gray-300">{{ order.product_snapshot.description }}</p>
      <ul v-if="order.product_snapshot.features?.length" class="list-inside list-disc space-y-1 text-sm text-gray-600 dark:text-gray-300"><li v-for="feature in order.product_snapshot.features" :key="feature">{{ feature }}</li></ul>
      <dl class="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
        <div v-if="props.showFinancials && order.product_snapshot.price != null"><dt class="text-gray-500 dark:text-gray-400">{{ t(order.order_type === 'reset_card' ? 'payment.orderOps.resetCardTotalPrice' : 'payment.orderOps.listPrice') }}</dt><dd>{{ order.product_snapshot.currency || (order.order_type === 'balance' ? order.currency : 'USD') }} {{ order.product_snapshot.price.toFixed(2) }}</dd></div>
        <template v-for="period in ['daily', 'weekly', 'monthly'] as const" :key="period"><div v-if="order.product_snapshot[`${period}_limit_usd`] != null"><dt class="text-gray-500 dark:text-gray-400">{{ t(`payment.orderOps.${period}Quota`) }}</dt><dd>{{ order.product_snapshot[`${period}_limit_usd`]! > 0 ? `$${order.product_snapshot[`${period}_limit_usd`]}` : t('payment.planCard.unlimited') }}</dd></div></template>

        <div v-if="order.product_snapshot.subscription_days != null">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.duration') }}</dt>
          <dd>{{ t('payment.orderOps.days', { count: order.product_snapshot.subscription_days }) }}</dd>
        </div>
        <div v-if="order.product_snapshot.credited_amount != null">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.creditedAmount') }}</dt>
          <dd>${{ order.product_snapshot.credited_amount.toFixed(2) }}</dd>
        </div>
        <template v-if="order.order_type === 'reset_card'">
          <div v-if="order.product_snapshot.quantity != null">
            <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.resetCardQuantity') }}</dt>
            <dd>{{ order.product_snapshot.quantity }}</dd>
          </div>
          <div v-if="order.product_snapshot.unit_price != null">
            <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.resetCardUnitPrice') }}</dt>
            <dd>{{ order.product_snapshot.currency || order.currency || 'CNY' }} {{ order.product_snapshot.unit_price.toFixed(2) }}</dd>
          </div>
          <div v-if="typeof order.product_snapshot.use_on_purchase === 'boolean'">
            <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.resetCardUseOnPurchase') }}</dt>
            <dd>{{ order.product_snapshot.use_on_purchase ? t('common.yes') : t('common.no') }}</dd>
          </div>
        </template>
        <div v-if="benefits?.balance_bonus">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.balanceBonus') }}</dt>
          <dd>${{ benefits.balance_bonus.toFixed(2) }}</dd>
        </div>
        <div v-if="benefits?.concurrency">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.concurrency') }}</dt>
          <dd>{{ benefits.concurrency }}</dd>
        </div>
        <template v-if="props.showFinancials && paymentDiscount">
          <div>
            <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.paymentCoupon') }}</dt>
            <dd><code class="font-mono">{{ paymentDiscount.code }}</code></dd>
          </div>
          <div>
            <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.originalPayment') }}</dt>
            <dd>{{ formatDiscountAmount(paymentDiscount.original_amount, paymentDiscount.currency) }}</dd>
          </div>
          <div>
            <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.paymentDiscount') }}</dt>
            <dd class="text-emerald-700 dark:text-emerald-300">-{{ formatDiscountAmount(paymentDiscount.discount_amount, paymentDiscount.currency) }}</dd>
          </div>
          <div>
            <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.finalPayment') }}</dt>
            <dd class="font-medium">{{ formatDiscountAmount(paymentDiscount.pay_amount, paymentDiscount.currency) }}</dd>
          </div>
        </template>
        <div v-if="benefits?.reset_card_count">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.resetCards') }}</dt>
          <dd v-if="monthlyResetCardDelivery">{{ monthlyResetCardDelivery }}</dd>
          <dd v-else>{{ benefits.reset_card_count }}<span v-if="benefits.reset_card_expiry_days"> · {{ t('payment.orderOps.days', { count: benefits.reset_card_expiry_days }) }}</span></dd>
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
import { monthlyResetCardDeliveryLabel } from './validity'
import { formatPaymentAmount } from './currency'
const props = withDefaults(defineProps<{
  order: PaymentOrder
  /** Parent details may provide their own section heading. */
  showTitle?: boolean
  /** Parent details may place immutable money facts in a dedicated group. */
  showFinancials?: boolean
}>(), {
  showTitle: true,
  showFinancials: true,
})
const { t } = useI18n()
const benefits = computed(() => props.order.product_snapshot?.entitlements)
const paymentDiscount = computed(() => props.order.product_snapshot?.payment_discount)
const monthlyResetCardDelivery = computed(() => benefits.value
  ? monthlyResetCardDeliveryLabel(benefits.value, t)
  : '')

function formatDiscountAmount(value: string, currency: string): string {
  return formatPaymentAmount(Number(value), currency)
}
</script>
