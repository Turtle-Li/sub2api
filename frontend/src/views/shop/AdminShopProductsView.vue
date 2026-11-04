<script setup lang="ts">
import { onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ShopProductEditor from '@/components/shop/ShopProductEditor.vue'
import { shopAPI } from '@/api/shop'
import { shopDuration, shopDeliveryLabel, type ShopIntegrationOption, type ShopProduct, type ShopProductInput } from '@/types/shop'
const products = ref<ShopProduct[]>([])
const integrations = ref<ShopIntegrationOption[]>([])
const selected = ref<ShopProduct | null>(null)
const editing = ref(false)
const busy = ref(false)
const loading = ref(false)
const error = ref('')
async function load() {
  loading.value = true
  error.value = ''
  try { products.value = (await shopAPI.adminProducts()).data; integrations.value = (await shopAPI.actionIntegrations()).data }
  catch { error.value = '商品加载失败，请重试。' }
  finally { loading.value = false }
}
function edit(product: ShopProduct | null) { selected.value = product; editing.value = true; error.value = '' }
async function save(input: ShopProductInput) {
  busy.value = true
  error.value = ''
  try {
    const saved = (await shopAPI.saveProduct(selected.value?.id ?? null, input)).data
    products.value = [...products.value.filter(p => p.id !== saved.id), saved].sort((a, b) => a.sort_order - b.sort_order)
    editing.value = false
  } catch { error.value = '保存失败。请检查必填项；若其他管理员已修改商品，请关闭并刷新后重试。' }
  finally { busy.value = false }
}
onMounted(load)
</script>
<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-[1180px] py-2 sm:px-4 sm:py-4">
      <nav aria-label="商店管理" class="mb-6 flex min-h-11 items-center gap-5 text-sm">
        <RouterLink to="/admin/shop/orders" class="inline-flex min-h-11 items-center text-gray-600 hover:text-gray-900 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:text-gray-300 dark:hover:text-white dark:focus:ring-offset-gray-950">处理队列</RouterLink>
        <RouterLink to="/admin/shop/products" class="inline-flex min-h-11 items-center font-semibold text-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:text-primary-400 dark:focus:ring-offset-gray-950" aria-current="page">商品管理</RouterLink>
      </nav>

      <header class="mb-5 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 class="text-2xl font-semibold tracking-tight text-gray-950 dark:text-gray-50">商店商品</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">配置商品价格、时长、付款后操作和使用指南。</p>
        </div>
        <button type="button" :disabled="busy || loading" class="min-h-11 shrink-0 rounded-lg bg-primary-600 px-4 text-sm font-medium text-white hover:bg-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:focus:ring-offset-gray-950" @click="edit(null)">新增商品</button>
      </header>

      <div class="mb-5 flex flex-wrap gap-x-5 gap-y-2 text-sm text-gray-600 dark:text-gray-300" aria-label="商品状态概览">
        <span>全部 {{ products.length }}</span>
        <span>已上线 {{ products.filter(product => product.enabled).length }}</span>
        <span>草稿 / 停用 {{ products.filter(product => !product.enabled).length }}</span>
      </div>

      <div v-if="error && !editing" role="alert" class="mb-4 flex flex-col gap-3 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-800 sm:flex-row sm:items-center sm:justify-between dark:border-red-900/70 dark:bg-red-950/40 dark:text-red-200">
        <span>{{ error }}</span>
        <button type="button" :disabled="loading" class="min-h-11 rounded-lg border border-red-300 px-4 font-medium focus:outline-none focus:ring-2 focus:ring-red-500 disabled:opacity-50 dark:border-red-800" @click="load">重新加载</button>
      </div>

      <section aria-label="商品列表" :aria-busy="loading" class="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900">
        <div v-if="loading" class="p-8 text-center text-sm text-gray-500 dark:text-gray-400">正在加载商品…</div>
        <div v-else-if="products.length === 0 && !error" class="p-8 text-center">
          <p class="font-medium text-gray-800 dark:text-gray-100">暂无商品</p>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">新商品默认一个月、价格为 0，并保持停用状态。</p>
          <button type="button" class="mt-4 min-h-11 rounded-lg border border-primary-600 px-4 text-sm font-medium text-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:text-primary-300" @click="edit(null)">新增商品</button>
        </div>

        <template v-else-if="products.length > 0">
          <div class="hidden md:block">
            <table class="w-full text-left text-sm">
              <caption class="sr-only">人工服务商品配置</caption>
              <thead class="border-b border-gray-200 bg-gray-50 text-xs font-medium text-gray-500 dark:border-gray-800 dark:bg-gray-950/60 dark:text-gray-400">
                <tr>
                  <th scope="col" class="px-4 py-3">名称 / 分类</th>
                  <th scope="col" class="px-3 py-3">履约模式</th>
                  <th scope="col" class="px-3 py-3">价格 / 时长</th>
                  <th scope="col" class="px-3 py-3">排序</th>
                  <th scope="col" class="px-3 py-3">状态</th>
                  <th scope="col" class="px-3 py-3">更新时间</th>
                  <th scope="col" class="px-4 py-3 text-right">操作</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-gray-800">
                <tr v-for="product in products" :key="product.id" class="align-middle hover:bg-gray-50/80 dark:hover:bg-gray-800/40">
                  <td class="max-w-xs px-4 py-3.5"><p class="break-words font-medium text-gray-900 dark:text-gray-100">{{ product.name || '未命名商品' }}</p><p class="mt-1 break-words text-xs text-gray-500 dark:text-gray-400">{{ product.category || '未设置分类' }}</p></td>
                  <td class="px-3 py-3.5 text-gray-700 dark:text-gray-200">{{ shopDeliveryLabel(product) }}</td>
                  <td class="whitespace-nowrap px-3 py-3.5"><p class="font-medium text-gray-900 dark:text-gray-100">¥{{ product.price.toFixed(2) }}</p><p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ shopDuration(product) }}</p></td>
                  <td class="px-3 py-3.5 text-gray-700 dark:text-gray-200">{{ product.sort_order }}</td>
                  <td class="px-3 py-3.5"><span class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium" :class="product.enabled ? 'bg-primary-50 text-primary-800 dark:bg-primary-950/60 dark:text-primary-200' : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'"><span class="h-1.5 w-1.5 rounded-full" :class="product.enabled ? 'bg-primary-500' : 'bg-gray-400'" aria-hidden="true"></span>{{ product.enabled ? '已上线' : '草稿 / 停用' }}</span></td>
                  <td class="px-3 py-3.5 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ new Date(product.updated_at).toLocaleString('zh-CN') }}</td>
                  <td class="px-4 py-3.5 text-right"><button type="button" :disabled="busy" class="min-h-11 rounded-lg border border-gray-300 px-4 text-sm font-medium text-gray-700 hover:border-primary-500 hover:text-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 disabled:opacity-50 dark:border-gray-700 dark:text-gray-200 dark:hover:text-primary-300" @click="edit(product)">编辑</button></td>
                </tr>
              </tbody>
            </table>
          </div>

          <ul class="divide-y divide-gray-100 md:hidden dark:divide-gray-800">
            <li v-for="product in products" :key="product.id" class="p-4">
              <div class="flex items-start justify-between gap-3">
                <div class="min-w-0"><h2 class="break-words font-medium text-gray-900 dark:text-gray-100">{{ product.name || '未命名商品' }}</h2><p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ product.category || '未设置分类' }}</p></div>
                <span class="shrink-0 text-xs font-medium" :class="product.enabled ? 'text-primary-700 dark:text-primary-300' : 'text-gray-500 dark:text-gray-400'">{{ product.enabled ? '已上线' : '草稿 / 停用' }}</span>
              </div>
              <dl class="mt-3 grid grid-cols-[4.5rem_minmax(0,1fr)] gap-x-3 gap-y-2 text-sm">
                <dt class="text-gray-500 dark:text-gray-400">价格</dt><dd class="text-gray-800 dark:text-gray-200">¥{{ product.price.toFixed(2) }} / {{ shopDuration(product) }}</dd>
                <dt class="text-gray-500 dark:text-gray-400">模式</dt><dd class="text-gray-800 dark:text-gray-200">{{ shopDeliveryLabel(product) }}</dd>
                <dt class="text-gray-500 dark:text-gray-400">排序</dt><dd class="text-gray-800 dark:text-gray-200">{{ product.sort_order }}</dd>
                <dt class="text-gray-500 dark:text-gray-400">更新</dt><dd class="text-gray-800 dark:text-gray-200">{{ new Date(product.updated_at).toLocaleString('zh-CN') }}</dd>
              </dl>
              <button type="button" :disabled="busy" class="mt-4 min-h-11 w-full rounded-lg border border-gray-300 px-4 text-sm font-medium text-gray-700 focus:outline-none focus:ring-2 focus:ring-primary-500 disabled:opacity-50 dark:border-gray-700 dark:text-gray-200" @click="edit(product)">编辑商品</button>
            </li>
          </ul>
        </template>
      </section>

      <BaseDialog :show="editing" :title="selected ? '编辑商品' : '新增商品'" width="wide" :close-on-click-outside="!busy" :close-on-escape="!busy" :show-close-button="!busy" @close="editing = false">
        <div>
          <div v-if="error" role="alert" class="mb-4 rounded-lg border border-red-200 bg-red-50 p-3 text-sm leading-6 text-red-800 dark:border-red-900/70 dark:bg-red-950/40 dark:text-red-200">{{ error }}</div>
          <ShopProductEditor :key="selected?.id ?? 'new'" :product="selected" :busy="busy" :integrations="integrations" @save="save" />
        </div>
        <template #footer>
          <div class="flex flex-wrap items-center justify-between gap-3">
            <p class="text-xs text-gray-500 dark:text-gray-400">保存失败时保留输入，版本冲突请刷新后重试。</p>
            <div class="flex gap-2">
              <button type="button" class="btn btn-secondary min-h-11" :disabled="busy" @click="editing = false">取消</button>
              <button type="submit" form="shop-product-editor" class="btn btn-primary min-h-11" :disabled="busy">{{ busy ? '保存中…' : '保存商品' }}</button>
            </div>
          </div>
        </template>
      </BaseDialog>
    </div>
  </AppLayout>
</template>
