<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
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
      <header class="flex flex-wrap items-center justify-between gap-4 border-b border-gray-200/80 pb-5 dark:border-dark-700/80">
        <div>
          <h1 class="text-2xl font-bold tracking-tight text-gray-900 dark:text-white">商城</h1>
          <p class="mt-1.5 text-sm text-gray-500 dark:text-gray-400">选购商品，查看订单与人工交付进度。</p>
        </div>
        <div class="flex flex-wrap items-center gap-2.5">
          <RouterLink class="btn btn-secondary min-h-[44px] px-4 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500" to="/orders">我的订单</RouterLink>
          <RouterLink v-if="auth.isAdmin" class="btn btn-secondary min-h-[44px] px-4 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500" to="/admin/shop/products">商品配置</RouterLink>
        </div>
      </header>

      <div v-if="categories.length" class="flex flex-wrap items-center gap-2" aria-label="商品分类">
        <button
          class="btn min-h-[44px] px-4 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500"
          :class="category === '' ? 'btn-primary' : 'btn-secondary'"
          :aria-pressed="category === ''"
          @click="category = ''"
        >
          全部
        </button>
        <button
          v-for="item in categories"
          :key="item"
          class="btn min-h-[44px] px-4 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500"
          :class="category === item ? 'btn-primary' : 'btn-secondary'"
          :aria-pressed="category === item"
          @click="category = item"
        >
          {{ item }}
        </button>
      </div>

      <div v-if="loading" role="status" class="card flex items-center justify-center gap-2.5 py-16 text-sm text-gray-500 dark:text-gray-400">
        <Icon name="clock" size="md" class="animate-spin text-primary-500" />
        <span>正在加载商品…</span>
      </div>

      <div v-else-if="error" role="alert" class="card flex flex-col items-start justify-between gap-4 border-red-200/80 p-6 sm:flex-row sm:items-center dark:border-red-900/40">
        <p class="text-sm text-gray-700 dark:text-gray-200">{{ error }}</p>
        <button class="btn btn-secondary min-h-[44px] shrink-0 px-5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500" @click="load">重试</button>
      </div>

      <div v-else-if="!visibleProducts.length" class="card px-6 py-16 text-center">
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">暂无上架商品</h2>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">商品上架后会显示在这里。</p>
        <RouterLink v-if="auth.isAdmin" to="/admin/shop/products" class="btn btn-primary mt-5 min-h-[44px] px-5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500">配置商品</RouterLink>
      </div>

      <div
        v-else
        class="grid grid-cols-1 gap-6"
        :class="visibleProducts.length === 2 ? 'md:grid-cols-2' : 'md:grid-cols-2 xl:grid-cols-3'"
      >
        <article
          v-for="product in visibleProducts"
          :key="product.id"
          class="card flex flex-col justify-between border border-gray-200/80 p-6 transition-colors hover:border-primary-500/40 dark:border-dark-700 dark:hover:border-primary-500/40"
        >
          <div class="flex-1">
            <h2 class="break-words text-xl font-bold leading-snug text-gray-900 dark:text-white">{{ product.name }}</h2>
            <div class="mt-3 flex flex-wrap items-center gap-2">
              <span
                v-if="product.category"
                class="inline-flex items-center rounded-md bg-primary-50 px-2.5 py-1 text-xs font-medium text-primary-700 dark:bg-primary-950/60 dark:text-primary-300"
              >
                {{ product.category }}
              </span>
              <span class="inline-flex items-center gap-1.5 rounded-md bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-300">
                <Icon :name="product.fulfillment_mode === 'session_topup' ? 'key' : 'user'" size="xs" class="text-primary-600 dark:text-primary-400" />
                <span>{{ shopModeLabels[product.fulfillment_mode] }}</span>
              </span>
            </div>
            <p class="mt-4 whitespace-pre-wrap break-words text-sm leading-6 text-gray-600 dark:text-gray-300">{{ product.summary }}</p>
            <RouterLink
              v-if="product.guide_key"
              :to="`/shop/help/${product.guide_key}`"
              class="mt-3 inline-flex min-h-[44px] items-center gap-2 rounded-lg text-sm font-medium text-primary-700 hover:text-primary-800 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-primary-300 dark:hover:text-primary-200"
            >
              <Icon name="book" size="sm" aria-hidden="true" />
              <span>使用教程</span>
              <span class="rounded bg-primary-50 px-1.5 py-0.5 text-xs font-semibold text-primary-800 dark:bg-primary-900/50 dark:text-primary-200">必看</span>
              <Icon name="chevronRight" size="xs" aria-hidden="true" />
            </RouterLink>
          </div>

          <div class="mt-6 border-t border-gray-100 pt-5 dark:border-dark-700/80">
            <div class="flex items-baseline justify-between gap-2">
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">价格</span>
              <div class="flex items-baseline gap-1.5">
                <strong class="text-2xl font-bold tabular-nums text-gray-900 dark:text-white">¥{{ product.price.toFixed(2) }}</strong>
                <span class="text-sm text-gray-500 dark:text-gray-400">/ {{ shopDuration(product) }}</span>
              </div>
            </div>

            <div class="mt-4 flex flex-col gap-2.5">
              <RouterLink
                :to="`/shop/products/${product.id}`"
                class="btn btn-primary min-h-[44px] w-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500"
              >
                <span>查看详情</span>
                <Icon name="chevronRight" size="sm" />
              </RouterLink>
            </div>
          </div>
        </article>
      </div>
    </div>
  </AppLayout>
</template>
