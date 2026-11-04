<script setup lang="ts">
import { ref, watch } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import OrderStatusBadge from '@/components/payment/OrderStatusBadge.vue'
import { shopAPI } from '@/api/shop'
import { shopStatusLabels, type ShopOrderDetail } from '@/types/shop'
const orders = ref<ShopOrderDetail[]>([])
const status = ref('')
const page = ref(1)
const total = ref(0)
const loading = ref(false)
const error = ref('')
let generation = 0
async function load() {
  const current = ++generation
  loading.value = true
  error.value = ''
  try {
    const response = await shopAPI.adminOrders({ page: page.value, page_size: 20, status: status.value })
    if (current === generation) { orders.value = response.data.items; total.value = response.data.total }
  } catch { if (current === generation) error.value = '队列加载失败，请重试。' }
  finally { if (current === generation) loading.value = false }
}
function filter() { if (page.value !== 1) page.value = 1; else void load() }
watch(page, load, { immediate: true })
</script>
<template>
  <AppLayout><div class="mx-auto max-w-6xl space-y-5">
    <header class="flex flex-wrap items-center justify-between gap-3"><h1 class="text-2xl font-bold">人工交付队列</h1><div class="flex gap-2"><RouterLink to="/admin/shop/products" class="btn btn-secondary">商品配置</RouterLink><RouterLink to="/admin/orders" class="btn btn-secondary">全部订单</RouterLink></div></header>
    <div class="card flex flex-wrap items-center gap-3 p-4"><label class="flex items-center gap-3 whitespace-nowrap text-sm">交付状态<select v-model="status" class="input" @change="filter"><option value="">全部状态</option><option v-for="(label, value) in shopStatusLabels" :key="value" :value="value">{{ label }}</option></select></label><button class="btn btn-secondary" :disabled="loading" @click="load">刷新</button></div>
    <p v-if="error" role="alert" class="text-red-600">{{ error }}</p><p v-if="loading" role="status">正在加载队列…</p>
    <div v-else-if="!orders.length" class="card py-16 text-center text-gray-500 dark:text-gray-400">当前没有符合条件的订单</div>
    <div v-else class="space-y-3">
      <article v-for="order in orders" :key="order.order_id" class="card flex flex-col gap-4 p-5 sm:flex-row sm:items-center sm:justify-between">
        <div class="min-w-0 flex-1">
          <p class="break-words font-semibold">{{ order.product.name }}</p>
          <p class="mt-1 break-all text-sm text-gray-500 dark:text-gray-400">#{{ order.order_id }} · {{ order.user_email || `用户 #${order.user_id}` }}</p>
          <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ new Date(order.created_at).toLocaleString() }} · {{ order.input_submitted_at ? '资料已提交' : '尚无用户资料' }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-3 sm:gap-4">
          <div class="text-sm"><p class="font-semibold">¥{{ order.pay_amount.toFixed(2) }}</p><div class="mt-1"><OrderStatusBadge :status="order.payment_status" /></div></div>
          <span class="badge badge-primary">{{ shopStatusLabels[order.fulfillment_status] }}</span>
          <RouterLink :to="`/admin/shop/orders/${order.order_id}`" class="btn btn-primary btn-sm">处理订单</RouterLink>
        </div>
      </article>
    </div>
    <nav v-if="total > 20" class="flex items-center justify-end gap-3" aria-label="订单分页"><span class="text-sm text-gray-500 dark:text-gray-400">第 {{ page }} 页 · 共 {{ total }} 条</span><button class="btn btn-secondary" :disabled="page <= 1 || loading" @click="page--">上一页</button><button class="btn btn-secondary" :disabled="page * 20 >= total || loading" @click="page++">下一页</button></nav>
  </div></AppLayout>
</template>
