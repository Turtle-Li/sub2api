<script setup lang="ts">
import { onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ShopProductEditor from '@/components/shop/ShopProductEditor.vue'
import { shopAPI } from '@/api/shop'
import { shopDuration, shopModeLabels, type ShopProduct, type ShopProductInput } from '@/types/shop'
const products = ref<ShopProduct[]>([])
const selected = ref<ShopProduct | null>(null)
const editing = ref(false)
const busy = ref(false)
const loading = ref(false)
const error = ref('')
async function load() {
  loading.value = true
  error.value = ''
  try { products.value = (await shopAPI.adminProducts()).data }
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
  <AppLayout><div class="mx-auto max-w-6xl space-y-5">
    <header class="flex flex-wrap items-center justify-between gap-3"><div><h1 class="text-2xl font-bold">商城商品配置</h1><p class="mt-2 text-sm text-gray-500 dark:text-gray-400">名称、分类与商品内容由后台维护。新商品默认一个月，保存为草稿后可随时上架。</p></div><div class="flex gap-2"><RouterLink to="/admin/shop/orders" class="btn btn-secondary">人工交付队列</RouterLink><button class="btn btn-primary" @click="edit(null)">新增商品</button></div></header>
    <p v-if="error && !editing" role="alert" class="text-red-600">{{ error }} <button class="underline" @click="load">重试</button></p>
    <p v-if="loading" role="status">正在加载商品…</p>
    <div v-else-if="!products.length" class="card py-16 text-center"><h2 class="text-lg font-semibold">还没有商城商品</h2><p class="mt-2 text-sm text-gray-500 dark:text-gray-400">填写商品内容和售价后上架，无需预先配置库存。</p><button class="btn btn-primary mt-4" @click="edit(null)">新增商品</button></div>
    <div v-else class="grid gap-4 md:grid-cols-2 xl:grid-cols-3"><article v-for="product in products" :key="product.id" class="card space-y-3 p-5"><div class="flex items-center justify-between gap-2"><span class="text-xs text-primary-700 dark:text-primary-300">{{ product.category || '未分类' }}</span><span class="badge" :class="product.enabled ? 'badge-success' : 'badge-gray'">{{ product.enabled ? '已上架' : '草稿' }}</span></div><h2 class="break-words text-lg font-semibold">{{ product.name }}</h2><p class="text-sm text-gray-500 dark:text-gray-400">{{ shopModeLabels[product.fulfillment_mode] }}</p><p class="font-semibold">¥{{ product.price.toFixed(2) }} / {{ shopDuration(product) }}</p><button class="btn btn-secondary w-full" @click="edit(product)">编辑商品</button></article></div>
  </div>
  <BaseDialog :show="editing" :title="selected ? '编辑商城商品' : '新增商城商品'" width="wide" :close-on-click-outside="!busy" :show-close-button="!busy" @close="editing = false"><p v-if="error" role="alert" class="mb-4 text-sm text-red-600">{{ error }}</p><ShopProductEditor :key="selected?.id ?? 'new'" :product="selected" :busy="busy" @save="save" /></BaseDialog>
  </AppLayout>
</template>
