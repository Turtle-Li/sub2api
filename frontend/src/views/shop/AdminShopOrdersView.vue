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
  <AppLayout>
    <div class="mx-auto w-full max-w-[1180px] py-2 sm:px-4 sm:py-4">
      <nav aria-label="商店管理" class="mb-6 flex min-h-11 items-center gap-5 text-sm">
        <RouterLink to="/admin/shop/orders" class="inline-flex min-h-11 items-center font-semibold text-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:text-primary-400 dark:focus:ring-offset-gray-950" aria-current="page">处理队列</RouterLink>
        <RouterLink to="/admin/shop/products" class="inline-flex min-h-11 items-center text-gray-600 hover:text-gray-900 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:text-gray-300 dark:hover:text-white dark:focus:ring-offset-gray-950">商品管理</RouterLink>
      </nav>

      <header class="mb-5 flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 class="text-2xl font-semibold tracking-tight text-gray-950 dark:text-gray-50">商店履约队列</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">查看资料提交和人工处理进度。</p>
        </div>
        <p class="text-sm text-gray-600 dark:text-gray-300">共 {{ total }} 笔订单</p>
      </header>

      <form class="mb-5 rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-gray-900" role="search" aria-label="筛选履约队列" @submit.prevent="filter">
        <div class="flex flex-col gap-3 sm:flex-row sm:items-end">
          <div class="min-w-0 flex-1 sm:max-w-xs">
            <label for="queue-status" class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-200">履约状态</label>
            <select id="queue-status" v-model="status" class="min-h-11 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100">
              <option value="">全部状态</option>
              <option v-for="(label, value) in shopStatusLabels" :key="value" :value="value">{{ label }}</option>
            </select>
          </div>
          <div class="flex flex-wrap gap-2">
            <button type="submit" :disabled="loading" class="min-h-11 rounded-lg border border-gray-300 bg-white px-4 text-sm font-medium text-gray-700 hover:bg-gray-50 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-200 dark:hover:bg-gray-800 dark:focus:ring-offset-gray-950">应用筛选</button>
            <button type="button" :disabled="loading" class="inline-flex min-h-11 items-center justify-center rounded-lg bg-primary-600 px-4 text-sm font-medium text-white hover:bg-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-60 dark:focus:ring-offset-gray-950" @click="load">{{ loading ? '刷新中…' : '刷新' }}</button>
          </div>
        </div>
      </form>

      <div v-if="error" role="alert" class="mb-4 flex flex-col gap-3 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-800 sm:flex-row sm:items-center sm:justify-between dark:border-red-900/70 dark:bg-red-950/40 dark:text-red-200">
        <span>{{ error }}</span>
        <button type="button" :disabled="loading" class="min-h-11 shrink-0 rounded-lg border border-red-300 px-4 font-medium focus:outline-none focus:ring-2 focus:ring-red-500 disabled:opacity-50 dark:border-red-800" @click="load">重新加载</button>
      </div>

      <section aria-label="订单处理结果" :aria-busy="loading" class="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900">
        <div v-if="loading && orders.length === 0" class="p-8 text-center text-sm text-gray-500 dark:text-gray-400">正在加载订单队列…</div>
        <div v-else-if="!loading && orders.length === 0" class="p-8 text-center">
          <p class="font-medium text-gray-800 dark:text-gray-100">{{ status ? '当前筛选没有订单' : '队列暂无订单' }}</p>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ status ? '可切换履约状态后重新查询。' : '新订单出现后会显示在这里。' }}</p>
        </div>

        <template v-else>
          <div class="hidden xl:block">
            <table class="w-full text-left text-sm">
              <caption class="sr-only">商店订单处理队列</caption>
              <thead class="border-b border-gray-200 bg-gray-50 text-xs font-medium text-gray-500 dark:border-gray-800 dark:bg-gray-950/60 dark:text-gray-400">
                <tr>
                  <th scope="col" class="px-4 py-3">商品 / 买家</th>
                  <th scope="col" class="px-3 py-3">订单</th>
                  <th scope="col" class="px-3 py-3">日期</th>
                  <th scope="col" class="px-3 py-3">金额</th>
                  <th scope="col" class="px-3 py-3">支付</th>
                  <th scope="col" class="px-3 py-3">履约</th>
                  <th scope="col" class="px-4 py-3 text-right">操作</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-gray-800" :class="loading ? 'opacity-60' : ''">
                <tr v-for="order in orders" :key="order.order_id" class="align-middle hover:bg-gray-50/80 dark:hover:bg-gray-800/40">
                  <td class="px-4 py-3.5">
                    <p class="break-words font-medium text-gray-900 dark:text-gray-100">{{ order.product.name }}</p>
                    <p class="mt-1 max-w-40 truncate text-xs text-gray-500 dark:text-gray-400">{{ order.user_email || `用户 #${order.user_id}` }}</p>
                  </td>
                  <td class="px-3 py-3.5">
                    <p class="font-medium text-gray-800 dark:text-gray-200">#{{ order.order_id }}</p>
                    <p class="mt-1 max-w-36 truncate text-xs text-gray-500 dark:text-gray-400" :title="order.out_trade_no">{{ order.out_trade_no }}</p>
                  </td>
                  <td class="px-3 py-3.5 text-xs leading-5 text-gray-600 dark:text-gray-300">{{ new Date(order.created_at).toLocaleString('zh-CN') }}</td>
                  <td class="px-3 py-3.5 font-medium text-gray-900 dark:text-gray-100">¥{{ order.pay_amount.toFixed(2) }}</td>
                  <td class="whitespace-nowrap px-3 py-3.5"><OrderStatusBadge :status="order.payment_status" /></td>
                  <td class="px-3 py-3.5">
                    <span class="inline-flex items-center gap-1.5 whitespace-nowrap rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-700 dark:bg-gray-800 dark:text-gray-200"><span class="h-1.5 w-1.5 rounded-full bg-primary-500" aria-hidden="true"></span>{{ shopStatusLabels[order.fulfillment_status] }}</span>
                  </td>
                  <td class="px-4 py-3.5 text-right">
                    <RouterLink :to="`/admin/shop/orders/${order.order_id}`" class="inline-flex min-h-11 items-center justify-center rounded-lg border border-primary-600 px-3 text-sm font-medium text-primary-700 hover:bg-primary-50 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:border-primary-500 dark:text-primary-300 dark:hover:bg-primary-950/40 dark:focus:ring-offset-gray-900">打开处理</RouterLink>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <ul class="divide-y divide-gray-100 xl:hidden dark:divide-gray-800" :class="loading ? 'opacity-60' : ''">
            <li v-for="order in orders" :key="order.order_id" class="p-4">
              <div class="flex items-start justify-between gap-3">
                <div class="min-w-0">
                  <h2 class="break-words font-medium text-gray-900 dark:text-gray-100">{{ order.product.name }}</h2>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">订单 #{{ order.order_id }}</p>
                </div>
                <p class="shrink-0 font-semibold text-gray-900 dark:text-gray-100">¥{{ order.pay_amount.toFixed(2) }}</p>
              </div>
              <dl class="mt-3 grid grid-cols-[4.5rem_minmax(0,1fr)] gap-x-3 gap-y-2 text-sm">
                <dt class="text-gray-500 dark:text-gray-400">买家</dt><dd class="min-w-0 break-all text-gray-800 dark:text-gray-200">{{ order.user_email || `用户 #${order.user_id}` }}</dd>
                <dt class="text-gray-500 dark:text-gray-400">创建时间</dt><dd class="text-gray-800 dark:text-gray-200">{{ new Date(order.created_at).toLocaleString('zh-CN') }}</dd>
                <dt class="text-gray-500 dark:text-gray-400">支付状态</dt><dd><OrderStatusBadge :status="order.payment_status" /></dd>
                <dt class="text-gray-500 dark:text-gray-400">履约状态</dt><dd class="text-gray-800 dark:text-gray-200">{{ shopStatusLabels[order.fulfillment_status] }}</dd>
              </dl>
              <RouterLink :to="`/admin/shop/orders/${order.order_id}`" class="mt-4 flex min-h-11 items-center justify-center rounded-lg border border-primary-600 px-4 text-sm font-medium text-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:border-primary-500 dark:text-primary-300">打开处理</RouterLink>
            </li>
          </ul>
        </template>
      </section>

      <nav v-if="total > 0" aria-label="队列分页" class="mt-5 flex flex-wrap items-center justify-between gap-3">
        <button type="button" :disabled="loading || page <= 1" class="min-h-11 rounded-lg border border-gray-300 bg-white px-4 text-sm font-medium text-gray-700 focus:outline-none focus:ring-2 focus:ring-primary-500 disabled:cursor-not-allowed disabled:opacity-40 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-200" @click="page = Math.max(1, page - 1)">上一页</button>
        <p class="text-sm text-gray-600 dark:text-gray-300" aria-live="polite">第 {{ page }} / {{ Math.max(1, Math.ceil(total / 20)) }} 页</p>
        <button type="button" :disabled="loading || page >= Math.ceil(total / 20)" class="min-h-11 rounded-lg border border-gray-300 bg-white px-4 text-sm font-medium text-gray-700 focus:outline-none focus:ring-2 focus:ring-primary-500 disabled:cursor-not-allowed disabled:opacity-40 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-200" @click="page = page + 1">下一页</button>
      </nav>
    </div>
  </AppLayout>
</template>
