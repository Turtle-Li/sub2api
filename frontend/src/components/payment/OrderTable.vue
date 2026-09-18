<template>
  <DataTable :columns="columns" :data="orders" :loading="loading">
    <template #cell-purchase="{ row }">
      <div class="max-w-56">
        <p class="whitespace-normal break-words text-sm font-medium text-gray-900 dark:text-white">{{ purchaseName(row) || t(`payment.admin.${row.order_type}Order`, row.order_type) }}</p>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">#{{ row.id }} <span aria-hidden="true">·</span> {{ compactOrderNumber(row.out_trade_no) }}</p>
      </div>
    </template>
    <template v-if="showUser" #cell-user_email="{ value, row }">
      <div class="max-w-44 whitespace-normal break-words text-sm">
        <p class="text-gray-900 dark:text-white">{{ row.user_name || value || '#' + row.user_id }}</p>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">#{{ row.user_id }}<span v-if="row.user_name && value"> · {{ value }}</span></p>
      </div>
    </template>
    <template #cell-pay_amount="{ value, row }">
      <div class="text-sm tabular-nums">
        <span class="font-semibold text-gray-900 dark:text-white">{{ currencySymbol(row.currency) }}{{ value.toFixed(2) }}</span>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.methods.' + row.payment_type, row.payment_type) }}</p>
        <p v-if="row.order_type === 'balance'" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.creditedAmount') }} ${{ row.amount.toFixed(2) }}</p>
      </div>
    </template>
    <template #cell-payment_status="{ row }">
      <div v-if="showUser">
        <OrderStatusBadge v-if="hasCurrentPaymentOutcome(row)" :status="row.status" />
        <OrderLifecycleBadge v-else kind="payment" :value="paymentFact(row)" />
      </div>
      <div v-else class="space-y-1">
        <OrderLifecycleBadge kind="payment" :value="paymentFact(row)" />
        <div v-if="hasCurrentPaymentOutcome(row)">
          <OrderStatusBadge :status="row.status" />
        </div>
      </div>
    </template>
    <template #cell-fulfillment_status="{ row }">
      <div v-if="showUser" class="space-y-1">
        <OrderLifecycleBadge
          v-if="hasRefundEntitlementStatus(row)"
          kind="refundEntitlement"
          :value="row.refund_entitlement_status || 'NOT_APPLICABLE'"
        />
        <OrderLifecycleBadge v-else kind="fulfillment" :value="fulfillmentFact(row)" />
        <p v-if="refundBlockerKey(row)" class="max-w-52 text-xs leading-5" :class="refundBlockerClass(row)">
          {{ t(refundBlockerKey(row)) }}
        </p>
      </div>
      <div v-else :class="hasRefundHandling(row) ? 'space-y-2' : 'space-y-1'">
        <div>
          <p v-if="hasRefundHandling(row)" class="mb-1 text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.issuanceRecord') }}</p>
          <OrderLifecycleBadge kind="fulfillment" :value="fulfillmentFact(row)" />
        </div>
        <div v-if="hasRefundHandling(row)" class="border-t border-gray-100 pt-2 dark:border-dark-600">
          <p class="mb-1 text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.orderOps.refundHandling') }}</p>
          <div class="space-y-1">
            <OrderLifecycleBadge
              v-if="hasRefundEntitlementStatus(row)"
              kind="refundEntitlement"
              :value="row.refund_entitlement_status || 'NOT_APPLICABLE'"
            />
            <p
              v-if="row.refund_recovery?.state === 'WAITING_PROVIDER_BALANCE'"
              class="max-w-52 text-xs leading-5 text-amber-700 dark:text-amber-300"
            >
              {{ t('payment.admin.refundMerchantBalanceInsufficientShort') }}
            </p>
            <p
              v-else-if="row.refund_recovery?.state === 'RETRY_QUEUED'"
              class="max-w-52 text-xs leading-5 text-blue-700 dark:text-blue-300"
            >
              {{ t('payment.admin.refundRetryQueuedShort') }}
            </p>
            <p v-if="row.needs_manual_review" class="text-xs text-red-700 dark:text-red-300">{{ t('payment.orderOps.refundReviewRequired') }}</p>
          </div>
        </div>
      </div>
    </template>
    <template #cell-invoice="{ row }">
      <div v-if="row.invoice" class="space-y-1">
        <InvoiceStatusBadge :status="row.invoice.status" />
        <div v-if="['ISSUED', 'REJECTED'].includes(row.invoice.status)"><InvoiceEmailDeliveryBadge :status="row.invoice.email_delivery_status" /></div>
      </div>
      <span v-else class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.invoice.admin.notRequested') }}</span>
    </template>
    <template #cell-created_at="{ value }">
      <time :datetime="value" :title="formatOrderDateTime(value)" class="text-xs text-gray-600 dark:text-gray-300"><span class="block">{{ new Date(value).toLocaleDateString() }}</span><span class="mt-1 block">{{ new Date(value).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false }) }}</span></time>
    </template>
    <template #cell-actions="{ row }"><slot name="actions" :row="row" /></template>
  </DataTable>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PaymentOrder } from '@/types/payment'
import type { Column } from '@/components/common/types'
import DataTable from '@/components/common/DataTable.vue'
import OrderStatusBadge from '@/components/payment/OrderStatusBadge.vue'
import OrderLifecycleBadge from './OrderLifecycleBadge.vue'
import InvoiceStatusBadge from './InvoiceStatusBadge.vue'
import InvoiceEmailDeliveryBadge from './InvoiceEmailDeliveryBadge.vue'
import { currencySymbol } from './currency'
import { formatOrderDateTime } from './orderUtils'
import { compactOrderNumber, purchaseName, paymentFact, fulfillmentFact } from './orderPresentation'
const { t } = useI18n()
const props = defineProps<{ orders: PaymentOrder[]; loading: boolean; showUser?: boolean }>()
function hasRefundEntitlementStatus(order: PaymentOrder): boolean {
  return Boolean(order.refund_entitlement_status && order.refund_entitlement_status !== 'NOT_APPLICABLE')
}
function hasCurrentPaymentOutcome(order: PaymentOrder): boolean {
  return order.status.startsWith('REFUND') || order.status === 'PARTIALLY_REFUNDED' || ['EXPIRED', 'CANCELLED'].includes(order.status)
}
function hasRefundHandling(order: PaymentOrder): boolean {
  return hasRefundEntitlementStatus(order) || Boolean(order.refund_recovery?.state) || Boolean(order.needs_manual_review)
}
function refundBlockerKey(order: PaymentOrder): string {
  if (order.refund_recovery?.state === 'WAITING_PROVIDER_BALANCE') return 'payment.admin.refundMerchantBalanceInsufficientCompact'
  if (order.refund_recovery?.state === 'RETRY_QUEUED') return 'payment.admin.refundRetryQueuedCompact'
  if (order.needs_manual_review) return 'payment.admin.refundManualReviewCompact'
  return ''
}
function refundBlockerClass(order: PaymentOrder): string {
  if (order.refund_recovery?.state === 'WAITING_PROVIDER_BALANCE') return 'text-amber-700 dark:text-amber-300'
  if (order.refund_recovery?.state === 'RETRY_QUEUED') return 'text-blue-700 dark:text-blue-300'
  return 'text-red-700 dark:text-red-300'
}
const columns = computed((): Column[] => [
  { key: 'purchase', label: t('payment.orderOps.purchase') },
  ...(props.showUser ? [{ key: 'user_email', label: t('payment.admin.colUser') }] : []),
  { key: 'pay_amount', label: t('payment.orders.payAmount') },
  { key: 'payment_status', label: t('payment.orderOps.paymentLabel') },
  { key: 'fulfillment_status', label: t('payment.orderOps.fulfillmentLabel') },
  { key: 'invoice', label: t('payment.invoice.currentStatus') },
  { key: 'created_at', label: t('payment.orders.createdAt') },
  { key: 'actions', label: t('payment.orders.actions') },
])
</script>
