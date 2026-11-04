<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { shopAPI } from '@/api/shop'
import { shopDuration, shopModeLabels, type ShopProduct } from '@/types/shop'
import { useAuthStore } from '@/stores/auth'
const auth = useAuthStore()
const products = ref<ShopProduct[]>([])
const category = ref('')
const loading = ref(true)
const error = ref('')
const categories = computed(() => [...new Set(products.value.map(p => p.category).filter(Boolean))])
const visibleProducts = computed(() => products.value.filter(p => !category.value || p.category === category.value))
async function load() {
  loading.value = true
  error.value = ''
  try { products.value = (await shopAPI.products()).data }
  catch { error.value = '商城加载失败，请重试。' }
  finally { loading.value = false }
}
onMounted(load)
</script>
<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6">
      <header class="flex flex-wrap items-center justify-between gap-4">
        <div><h1 class="text-2xl font-bold text-gray-900 dark:text-white">商城</h1><p class="mt-2 text-sm text-gray-500 dark:text-gray-400">选购商品，查看订单与人工交付进度。</p></div>
        <div class="flex flex-wrap gap-2"><RouterLink class="btn btn-secondary" to="/orders">我的订单</RouterLink><RouterLink v-if="auth.isAdmin" class="btn btn-secondary" to="/admin/shop/products">商品配置</RouterLink></div>
      </header>
      <div v-if="categories.length" class="flex flex-wrap gap-2" aria-label="商品分类">
        <button class="btn btn-sm" :class="category === '' ? 'btn-primary' : 'btn-secondary'" :aria-pressed="category === ''" @click="category = ''">全部</button>
        <button v-for="item in categories" :key="item" class="btn btn-sm" :class="category === item ? 'btn-primary' : 'btn-secondary'" :aria-pressed="category === item" @click="category = item">{{ item }}</button>
      </div>
      <p v-if="loading" role="status" class="py-12 text-center text-gray-500 dark:text-gray-400">正在加载商品…</p>
      <div v-else-if="error" role="alert" class="card p-6"><p>{{ error }}</p><button class="btn btn-secondary mt-3" @click="load">重试</button></div>
      <div v-else-if="!visibleProducts.length" class="card px-6 py-16 text-center"><h2 class="text-lg font-semibold">暂无上架商品</h2><p class="mt-2 text-sm text-gray-500 dark:text-gray-400">商品上架后会显示在这里。</p><RouterLink v-if="auth.isAdmin" to="/admin/shop/products" class="btn btn-primary mt-5">配置商品</RouterLink></div>
      <div v-else class="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
        <article v-for="product in visibleProducts" :key="product.id" class="card flex flex-col p-6">
          <p class="text-xs font-medium text-primary-700 dark:text-primary-300">{{ product.category || shopModeLabels[product.fulfillment_mode] }}</p>
          <h2 class="mt-3 break-words text-xl font-bold text-gray-900 dark:text-white">{{ product.name }}</h2>
          <p class="mt-3 flex-1 whitespace-pre-wrap break-words text-sm leading-6 text-gray-500 dark:text-gray-400">{{ product.summary }}</p>
          <div class="my-5 flex items-baseline gap-2"><strong class="text-3xl font-bold text-gray-900 dark:text-white">¥{{ product.price.toFixed(2) }}</strong><span class="text-sm text-gray-500 dark:text-gray-400">/ {{ shopDuration(product) }}</span></div>
          <p class="mb-4 text-xs text-gray-500 dark:text-gray-400">{{ shopModeLabels[product.fulfillment_mode] }}</p>
          <RouterLink :to="`/shop/products/${product.id}`" class="btn btn-primary w-full">查看详情</RouterLink>
          <div class="mt-3 h-6 text-center"><RouterLink v-if="product.guide_key" :to="`/shop/help/${product.guide_key}`" class="text-sm text-primary-700 underline dark:text-primary-300">使用教程</RouterLink></div>
        </article>
      </div>
    </div>
  </AppLayout>
</template>
