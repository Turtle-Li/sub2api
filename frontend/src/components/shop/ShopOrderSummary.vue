<script setup lang="ts">
import { shopStatusLabels, shopDuration, shopNotificationLabels, shopAutomationLabels, type ShopOrderDetail } from '@/types/shop'
import OrderStatusBadge from '@/components/payment/OrderStatusBadge.vue'
withDefaults(defineProps<{ order: ShopOrderDetail; showGuide?: boolean; showNotification?: boolean }>(), { showGuide: true, showNotification: true })
</script>
<template>
  <section class="rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-gray-900 sm:p-5" aria-labelledby="shop-order-summary-title">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div class="min-w-0">
        <p class="text-xs text-gray-500 dark:text-gray-400">订单 #{{ order.order_id }}</p>
        <h1 id="shop-order-summary-title" class="mt-1 break-words text-xl font-semibold leading-tight text-gray-900 dark:text-gray-100">{{ order.product.name }}</h1>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ shopDuration(order.product) }}<span class="mx-2" aria-hidden="true">·</span><strong class="font-semibold tabular-nums text-gray-900 dark:text-gray-100">¥{{ order.pay_amount.toFixed(2) }}</strong></p>
      </div>
      <div class="flex flex-wrap items-center gap-2 sm:justify-end">
        <OrderStatusBadge :status="order.payment_status" />
        <span class="badge whitespace-nowrap bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-200">进度：{{ order.automation ? shopAutomationLabels[order.automation.status] : order.product.automatic_fulfillment && order.fulfillment_status === 'QUEUED' ? '等待自动处理' : shopStatusLabels[order.fulfillment_status] }}</span>
      </div>
    </div>
    <p v-if="order.status_note" class="mt-4 whitespace-pre-wrap break-words rounded-lg bg-amber-50 p-3 text-sm text-amber-900 dark:bg-amber-950 dark:text-amber-200">{{ order.status_note }}</p>
    <p v-if="showNotification && order.fulfillment_status === 'COMPLETED'" class="mt-3 text-xs text-gray-500 dark:text-gray-400">邮件通知：<span class="text-gray-700 dark:text-gray-300">{{ shopNotificationLabels[order.email_status] }}</span></p>
    <details class="mt-3 border-t border-gray-100 dark:border-gray-800">
      <summary class="flex min-h-11 cursor-pointer items-center justify-between gap-3 rounded-md text-xs font-medium text-gray-600 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500 dark:text-gray-400"><span>订单信息</span><span aria-hidden="true">＋</span></summary>
      <dl class="grid grid-cols-2 gap-x-5 gap-y-4 pb-2 text-xs leading-5">
        <div class="col-span-2 min-w-0"><dt class="text-gray-500 dark:text-gray-400">交易号</dt><dd class="mt-1 break-all font-mono text-gray-700 dark:text-gray-300">{{ order.out_trade_no }}</dd></div>
        <div><dt class="text-gray-500 dark:text-gray-400">创建时间</dt><dd class="mt-1 text-gray-700 dark:text-gray-300">{{ new Date(order.created_at).toLocaleString() }}</dd></div>
        <div v-if="order.paid_at"><dt class="text-gray-500 dark:text-gray-400">付款时间</dt><dd class="mt-1 text-gray-700 dark:text-gray-300">{{ new Date(order.paid_at).toLocaleString() }}</dd></div>
        <div v-if="order.input_submitted_at"><dt class="text-gray-500 dark:text-gray-400">资料提交时间</dt><dd class="mt-1 text-gray-700 dark:text-gray-300">{{ new Date(order.input_submitted_at).toLocaleString() }}</dd></div>
        <div v-if="order.completed_at"><dt class="text-gray-500 dark:text-gray-400">完成时间</dt><dd class="mt-1 text-gray-700 dark:text-gray-300">{{ new Date(order.completed_at).toLocaleString() }}</dd></div>
        <div v-if="showNotification && order.fulfillment_status !== 'COMPLETED'"><dt class="text-gray-500 dark:text-gray-400">邮件通知</dt><dd class="mt-1 text-gray-700 dark:text-gray-300">{{ shopNotificationLabels[order.email_status] }}</dd></div>
      </dl>
    </details>
    <RouterLink v-if="showGuide && order.product.guide_key" :to="`/shop/help/${order.product.guide_key}?order=${order.order_id}`" class="inline-flex min-h-11 items-center rounded-md text-sm font-medium text-primary-700 underline underline-offset-4 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500 dark:text-primary-300">查看使用指南</RouterLink>
  </section>
</template>
<style scoped>
details[open] summary > span:last-child { transform: rotate(45deg); }
</style>
