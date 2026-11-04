<script setup lang="ts">
import { computed, ref } from 'vue'
const props = defineProps<{ content: string }>()
const shown = ref(false)
const message = ref('')
const lines = computed(() => props.content.split('\n').filter(line => line.trim()))
async function copy(value: string) {
  try { await navigator.clipboard.writeText(value); message.value = '已复制，请妥善保管。' }
  catch { message.value = '复制失败，请显示资料后手动复制。' }
}
</script>
<template>
  <section class="rounded-xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-gray-900 sm:p-6" aria-labelledby="shop-delivery-title">
    <div>
      <h2 id="shop-delivery-title" class="text-lg font-semibold text-gray-900 dark:text-gray-100">账号已交付</h2>
    </div>

    <div class="mt-4 flex flex-col gap-2 sm:flex-row sm:flex-wrap">
      <button type="button" class="inline-flex min-h-11 items-center justify-center rounded-md bg-primary-700 px-4 text-sm font-semibold text-white hover:bg-primary-800 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-primary-400 dark:text-gray-950 dark:hover:bg-primary-300 dark:focus:ring-offset-gray-900" :disabled="!content" :aria-expanded="shown" @click="shown = !shown">
        {{ shown ? '隐藏交付内容' : '显示交付内容' }}
      </button>
      <button type="button" class="inline-flex min-h-11 items-center justify-center rounded-md border border-gray-300 bg-white px-4 text-sm font-medium text-gray-800 hover:bg-gray-50 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-200 dark:hover:bg-gray-800 dark:focus:ring-offset-gray-900" :disabled="!content" @click="copy(content)">复制完整原文</button>
    </div>

    <p v-if="!content" class="mt-5 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200" role="alert">订单已标记交付，但未读取到交付内容，请联系站点客服核查。</p>

    <template v-else-if="shown">
      <pre class="mt-5 max-w-full whitespace-pre-wrap break-words rounded-lg border border-gray-200 bg-gray-50 p-4 font-mono text-sm leading-6 text-gray-900 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100">{{ content }}</pre>
      <div class="mt-5 border-t border-gray-200 pt-5 dark:border-gray-800">
        <h3 class="text-sm font-semibold text-gray-900 dark:text-gray-100">逐项复制</h3>
        <ol class="mt-3 space-y-2">
          <li v-for="(line, index) in lines" :key="index" class="flex flex-col gap-2 rounded-lg border border-gray-200 p-3 dark:border-gray-700 sm:flex-row sm:items-center sm:justify-between">
            <span class="min-w-0 flex-1 whitespace-pre-wrap break-words font-mono text-sm text-gray-700 dark:text-gray-300">{{ line }}</span>
            <button type="button" class="inline-flex min-h-11 shrink-0 items-center justify-center rounded-md border border-gray-300 px-4 text-sm font-medium text-gray-700 hover:bg-gray-50 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:border-gray-600 dark:text-gray-200 dark:hover:bg-gray-800 dark:focus:ring-offset-gray-900" :aria-label="`复制第 ${index + 1} 行`" @click="copy(line)">复制第 {{ index + 1 }} 行</button>
          </li>
        </ol>
      </div>
    </template>

    <div v-else-if="content" class="mt-5 rounded-lg border border-dashed border-gray-300 bg-gray-50 p-4 dark:border-gray-700 dark:bg-gray-950">
      <p class="text-sm text-gray-600 dark:text-gray-400">账号信息已隐藏，点击上方按钮查看或复制。</p>
    </div>

    <p v-if="message" class="mt-4 text-sm text-gray-700 dark:text-gray-300" role="status" aria-live="polite">{{ message }}</p>
  </section>
</template>
