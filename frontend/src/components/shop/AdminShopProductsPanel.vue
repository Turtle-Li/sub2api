<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import DataTable from '@/components/common/DataTable.vue'
import Icon from '@/components/icons/Icon.vue'
import ShopProductEditor from './ShopProductEditor.vue'
import { shopAPI } from '@/api/shop'
import { extractApiErrorMessage } from '@/utils/apiError'
import { shopPriceRange } from '@/utils/shopSku'
import { shopDuration, type ShopIntegrationOption, type ShopProduct, type ShopProductInput } from '@/types/shop'
const products = ref<ShopProduct[]>([])
const integrations = ref<ShopIntegrationOption[]>([])
const selected = ref<ShopProduct | null>(null)
const editing = ref(false)
const busy = ref(false)
const loading = ref(false)
const error = ref('')
const saveError = ref('')
const search = ref('')
const filter = ref('all')
const visible = computed(() => products.value.filter(product => `${product.name} ${product.category}`.toLowerCase().includes(search.value.trim().toLowerCase()) && (filter.value === 'all' || product.enabled === (filter.value === 'enabled'))))
const columns = [{ key: 'name', label: '商品' }, { key: 'price', label: '价格 / 规格' }, { key: 'delivery', label: '交付方式' }, { key: 'enabled', label: '状态' }, { key: 'sort_order', label: '排序' }, { key: 'actions', label: '操作' }]
function skuCount(product: ShopProduct): string {
  const count = product.skus?.length || product.concurrency_options?.reduce((total, option) => total + (option.weekly_price ? 2 : 1), 0)
  return count ? `${count} 个规格` : shopDuration(product)
}
async function load() {
  loading.value = true; error.value = ''
  const results = await Promise.allSettled([shopAPI.adminProducts(), shopAPI.actionIntegrations()])
  if (results[0].status === 'fulfilled') products.value = results[0].value.data
  else error.value = extractApiErrorMessage(results[0].reason, '商品加载失败，请重试。')
  if (results[1].status === 'fulfilled') integrations.value = results[1].value.data
  else error.value ||= '自动交付服务加载失败，请刷新后重试。'
  loading.value = false
}
function edit(product: ShopProduct | null) { selected.value = product; saveError.value = ''; editing.value = true }
async function save(input: ShopProductInput) {
  busy.value = true; saveError.value = ''
  try {
    const saved = (await shopAPI.saveProduct(selected.value?.id ?? null, input)).data
    products.value = [...products.value.filter(product => product.id !== saved.id), saved].sort((a, b) => a.sort_order - b.sort_order || a.id - b.id)
    editing.value = false
  } catch (err: unknown) { saveError.value = extractApiErrorMessage(err, '保存失败，请稍后重试。当前修改已保留。') }
  finally { busy.value = false }
}
onMounted(load)
</script>
<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div class="flex min-w-0 flex-wrap items-center gap-2"><input v-model="search" type="search" aria-label="搜索商城商品" placeholder="搜索商品名称或分类" class="input w-60 max-w-full" /><select v-model="filter" aria-label="商品状态" class="input w-28"><option value="all">全部状态</option><option value="enabled">已上架</option><option value="disabled">已下架</option></select></div>
      <div class="flex flex-wrap items-center gap-2"><RouterLink to="/admin/shop/orders" class="btn btn-secondary">交付订单</RouterLink><button type="button" class="btn btn-secondary" :disabled="loading" aria-label="刷新商城商品" @click="load"><Icon name="refresh" size="md" :class="loading && 'animate-spin'" /></button><button type="button" :disabled="loading" class="btn btn-primary" @click="edit(null)">新增商品</button></div>
    </div>
    <p class="text-sm text-gray-500 dark:text-gray-400">{{ products.length }} 件商品，{{ products.filter(product => product.enabled).length }} 件已上架。价格按 SKU 设置，支持人工与自动交付。</p>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
    <DataTable :columns="columns" :data="visible" :loading="loading">
      <template #cell-name="{ row }"><div class="max-w-xs"><div class="font-medium text-gray-900 dark:text-white">{{ row.name }}</div><div class="mt-1 text-xs text-gray-500">{{ row.category || '未分类' }} <span class="mx-1">·</span> #{{ row.id }}</div></div></template>
      <template #cell-price="{ row }"><span class="font-medium tabular-nums text-gray-900 dark:text-white">¥{{ shopPriceRange(row).price.toFixed(2) }}</span><span v-if="shopPriceRange(row).multiple" class="ml-1 text-xs text-gray-500">起</span><div class="mt-1 text-xs text-gray-500">{{ skuCount(row) }}</div></template>
      <template #cell-delivery="{ row }"><span :class="['badge', row.payment_actions?.length ? 'badge-primary' : 'badge-gray']">{{ row.payment_actions?.length ? '自动交付' : '人工交付' }}</span><div class="mt-1 text-xs text-gray-500">{{ row.payment_actions?.length ? (row.payment_actions[0].adapter === 'newapi.daypass.v1' ? 'DeepSeek API Key' : '链接 / 回调') : row.fulfillment_mode === 'session_topup' ? '代充' : '成品号' }}</div></template>
      <template #cell-enabled="{ value }"><span :class="['badge', value ? 'badge-success' : 'badge-gray']">{{ value ? '已上架' : '已下架' }}</span></template>
      <template #cell-actions="{ row }"><button type="button" class="inline-flex min-h-10 items-center gap-1.5 rounded-lg px-2 text-sm text-primary-600 hover:bg-primary-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-primary-400 dark:hover:bg-primary-900/20" :aria-label="`编辑 ${row.name}`" @click="edit(row)"><Icon name="edit" size="sm" />编辑</button></template>
      <template #empty><div class="py-5 text-center"><p class="font-medium">{{ search || filter !== 'all' ? '没有匹配的商品' : '还没有商城商品' }}</p><p class="mt-2 text-sm text-gray-500">{{ search || filter !== 'all' ? '试试其他名称或状态。' : '创建商品后，设置规格、价格和交付方式。' }}</p></div></template>
    </DataTable>
    <BaseDialog :show="editing" :title="selected ? `编辑商品 · ${selected.name}` : '新增商品'" width="extra-wide" :close-on-click-outside="false" :close-on-escape="!busy" :show-close-button="!busy" @close="editing = false">
      <ShopProductEditor v-if="editing" :product="selected" :busy="busy" :integrations="integrations" @save="save" />
      <p v-if="saveError" role="alert" class="mt-4 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ saveError }}</p>
      <template #footer><span class="mr-auto hidden text-xs text-gray-500 sm:block">{{ selected ? `编辑版本 ${selected.revision}` : '创建后可在商城展示' }}</span><button type="button" :disabled="busy" class="btn btn-secondary" @click="editing = false">取消</button><button type="submit" form="shop-product-editor" :disabled="busy" class="btn btn-primary">{{ busy ? '正在保存…' : '保存商品' }}</button></template>
    </BaseDialog>
  </div>
</template>
