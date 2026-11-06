<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { shopAPI } from '@/api/shop'
import { shopDuration, type ShopProduct } from '@/types/shop'
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
    <div class="mx-auto max-w-6xl space-y-5">
      <header class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex items-center gap-2.5">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ category || '全部商品' }}</h2>
          <span v-if="!loading && !error" class="text-sm text-gray-500 dark:text-gray-400">{{ visibleProducts.length }} 款</span>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <RouterLink class="btn btn-secondary min-h-[44px] px-4 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500" to="/orders">我的订单</RouterLink>
          <RouterLink v-if="auth.isAdmin" class="btn btn-secondary min-h-[44px] px-4 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500" to="/admin/shop/products">商品配置</RouterLink>
        </div>
      </header>

      <div v-if="categories.length > 1" class="flex flex-wrap items-center gap-2" role="group" aria-label="商品分类">
        <button
          type="button"
          class="btn min-h-[44px] px-4 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500"
          :class="category === '' ? 'border border-transparent bg-primary-600 text-white hover:bg-primary-700 dark:bg-primary-500 dark:text-dark-950 dark:hover:bg-primary-400' : 'btn-secondary'"
          :aria-pressed="category === ''"
          @click="category = ''"
        >
          全部
        </button>
        <button
          v-for="item in categories"
          :key="item"
          type="button"
          class="btn min-h-[44px] px-4 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500"
          :class="category === item ? 'border border-transparent bg-primary-600 text-white hover:bg-primary-700 dark:bg-primary-500 dark:text-dark-950 dark:hover:bg-primary-400' : 'btn-secondary'"
          :aria-pressed="category === item"
          @click="category = item"
        >
          {{ item }}
        </button>
      </div>

      <div v-if="loading" role="status" class="card flex items-center justify-center gap-2.5 py-16 text-sm text-gray-500 dark:text-gray-400">
        <Icon name="clock" size="md" class="animate-spin text-primary-500" aria-hidden="true" />
        <span>正在加载商品…</span>
      </div>

      <div v-else-if="error" role="alert" class="card flex flex-col items-start justify-between gap-4 border-red-200/80 p-6 sm:flex-row sm:items-center dark:border-red-900/40">
        <p class="text-sm text-gray-700 dark:text-gray-200">{{ error }}</p>
        <button type="button" class="btn btn-secondary min-h-[44px] shrink-0 px-5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500" @click="load">重试</button>
      </div>

      <div v-else-if="!visibleProducts.length" class="card px-6 py-12 text-center">
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ category ? '该分类暂无商品' : '暂无上架商品' }}</h2>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ category ? '可以选择其他分类查看。' : '商品上架后会显示在这里。' }}</p>
        <RouterLink v-if="auth.isAdmin && !category" to="/admin/shop/products" class="btn btn-secondary mt-5 min-h-[44px] px-5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500">配置商品</RouterLink>
      </div>

      <section v-else aria-label="商品列表" class="grid grid-cols-1 items-stretch gap-4 sm:grid-cols-[repeat(auto-fill,300px)]">
        <article
          v-for="product in visibleProducts"
          :key="product.id"
          class="flex min-w-0 flex-col overflow-hidden rounded-xl border border-gray-200 bg-white transition-colors hover:border-primary-300 dark:border-dark-700 dark:bg-dark-800 dark:hover:border-primary-700"
        >
          <div class="flex-1 p-4">
            <div class="flex min-h-[44px] items-center gap-3">
              <span class="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-gray-50 text-gray-900 dark:bg-dark-700 dark:text-white" aria-hidden="true">
                <PlatformIcon v-if="product.name.toLowerCase().includes('chatgpt')" platform="openai" size="lg" class="!h-7 !w-7" />
                <Icon v-else :name="product.fulfillment_mode === 'session_topup' ? 'key' : 'user'" size="md" />
              </span>
              <h2 class="min-w-0 break-words text-base font-semibold leading-[22px] text-gray-900 dark:text-white">{{ product.name }}</h2>
            </div>
            <p class="mt-3 min-h-[40px] line-clamp-2 break-words text-sm leading-5 text-gray-600 dark:text-gray-300">{{ product.summary }}</p>
          </div>

          <div class="mt-auto flex items-end justify-between gap-3 border-t border-gray-100 bg-gray-50/60 px-4 py-3 dark:border-dark-700 dark:bg-dark-900/30">
            <div class="min-w-0 pb-1">
              <div class="flex flex-wrap items-baseline gap-x-1 gap-y-1">
                <strong class="text-2xl font-semibold leading-7 tabular-nums tracking-tight text-gray-900 dark:text-white">¥{{ product.price.toFixed(2) }}</strong>
                <span v-if="product.concurrency_options?.some(option => option.weekly_price)" class="text-xs text-gray-500 dark:text-gray-400">起 · 可选周卡</span>
                <span v-else class="text-xs text-gray-500 dark:text-gray-400">/ {{ shopDuration(product) }}</span>
              </div>
            </div>
            <RouterLink
              :to="`/shop/products/${product.id}`"
              :aria-label="'查看' + product.name + '详情'"
              class="inline-flex min-h-[44px] shrink-0 items-center justify-center gap-1.5 rounded-lg bg-primary-700 px-3.5 text-sm font-medium text-white transition-colors hover:bg-primary-800 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:ring-offset-2 dark:bg-primary-400 dark:text-dark-950 dark:hover:bg-primary-300 dark:focus-visible:ring-offset-dark-800"
            >
              <span>查看详情</span>
              <Icon name="arrowRight" size="xs" aria-hidden="true" />
            </RouterLink>
          </div>
        </article>
      </section>
    </div>
  </AppLayout>
</template>
