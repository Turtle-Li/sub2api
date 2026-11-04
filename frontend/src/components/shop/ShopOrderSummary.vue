<script setup lang="ts">
import { shopStatusLabels, shopDuration, shopNotificationLabels, type ShopOrderDetail } from '@/types/shop'
import OrderStatusBadge from '@/components/payment/OrderStatusBadge.vue'
withDefaults(defineProps<{ order: ShopOrderDetail; showGuide?: boolean }>(), { showGuide: true })
</script>
<template>
  <section class="card space-y-5 p-6">
    <div class="flex flex-wrap items-start justify-between gap-3"><div><p class="text-sm text-gray-500 dark:text-gray-400">订单 #{{ order.order_id }}</p><h1 class="mt-2 break-words text-2xl font-bold text-gray-900 dark:text-white">{{ order.product.name }}</h1><p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ shopDuration(order.product) }} · ¥{{ order.pay_amount.toFixed(2) }}</p></div><span class="badge badge-primary">{{ shopStatusLabels[order.fulfillment_status] }}</span></div>
    <dl class="grid gap-4 text-sm sm:grid-cols-2"><div><dt class="text-gray-500 dark:text-gray-400">付款状态</dt><dd class="mt-1"><OrderStatusBadge :status="order.payment_status" /></dd></div><div><dt class="text-gray-500 dark:text-gray-400">邮件通知</dt><dd class="mt-1">{{ shopNotificationLabels[order.email_status] }}</dd></div><div><dt class="text-gray-500 dark:text-gray-400">订单编号</dt><dd class="mt-1 break-all font-mono text-xs">{{ order.out_trade_no }}</dd></div><div v-if="order.completed_at"><dt class="text-gray-500 dark:text-gray-400">完成时间</dt><dd class="mt-1">{{ new Date(order.completed_at).toLocaleString() }}</dd></div></dl>
    <p v-if="order.status_note" class="whitespace-pre-wrap break-words rounded-lg bg-amber-50 p-3 text-sm text-amber-900 dark:bg-amber-950/30 dark:text-amber-200">{{ order.status_note }}</p>
    <RouterLink v-if="showGuide && order.product.guide_key" :to="`/shop/help/${order.product.guide_key}`" class="inline-block text-sm font-medium text-primary-700 underline dark:text-primary-300">查看使用教程 →</RouterLink>
  </section>
</template>
