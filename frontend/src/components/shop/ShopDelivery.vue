<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { parseShopAccountDelivery } from '@/utils/shopAccountDelivery'

const props = defineProps<{ content: string; apiKey?: boolean }>()
const shown = ref(false)
const copied = ref('')
const error = ref('')
const credentials = computed(() => props.apiKey ? null : parseShopAccountDelivery(props.content))
const lines = computed(() => props.content.split('\n').filter(line => line.trim()))
let copyAttempt = 0
watch(() => [props.content, props.apiKey] as const, () => {
  copyAttempt++
  shown.value = false
  copied.value = ''
  error.value = ''
}, { flush: 'sync' })
async function copy(key: string, value: string) {
  const attempt = ++copyAttempt
  copied.value = ''
  error.value = ''
  try {
    await navigator.clipboard.writeText(value)
    if (attempt === copyAttempt) copied.value = key
  } catch {
    if (attempt === copyAttempt) error.value = '复制失败，请显示交付内容后手动复制。'
  }
}
</script>
<template>
  <section class="rounded-xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-gray-900 sm:p-6" aria-labelledby="shop-delivery-title">
    <h2 id="shop-delivery-title" class="text-lg font-semibold text-gray-900 dark:text-gray-100">{{ apiKey ? 'API Key 已交付' : '账号已交付' }}</h2>
    <div class="mt-4 flex flex-wrap gap-2">
      <button type="button" class="btn btn-primary min-h-11" :disabled="!content" :aria-expanded="shown" @click="shown = !shown">
        {{ shown ? '隐藏交付内容' : '显示交付内容' }}
      </button>
      <button type="button" class="btn btn-secondary min-h-11" :disabled="!content" @click="copy('raw', content)">
        {{ copied === 'raw' ? '已复制' : '复制完整原文' }}
      </button>
    </div>

    <p v-if="!content" class="mt-5 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200" role="alert">订单已标记交付，但未读取到交付内容，请联系站点客服核查。</p>
    <dl v-else-if="credentials" class="mt-5 divide-y divide-gray-200 rounded-lg border border-gray-200 px-4 dark:divide-gray-700 dark:border-gray-700" aria-label="账号与密码">
      <div v-for="field in (['account', 'password'] as const)" :key="field" class="flex flex-wrap items-center gap-x-4 gap-y-2 py-4">
        <div class="min-w-0 flex-1 basis-40">
          <dt class="text-sm text-gray-500 dark:text-gray-400">{{ field === 'account' ? '账号' : '密码' }}</dt>
          <dd v-if="shown" class="mt-1 whitespace-pre-wrap break-all font-mono text-sm leading-6 text-gray-900 dark:text-gray-100">{{ credentials[field] }}</dd>
          <dd v-else class="mt-1 text-sm tracking-widest text-gray-500 dark:text-gray-400" aria-label="已隐藏">••••••••</dd>
        </div>
        <button type="button" class="btn btn-secondary min-h-11 shrink-0" :aria-label="copied === field ? `${field === 'account' ? '账号' : '密码'}已复制` : undefined" @click="copy(field, credentials[field])">
          {{ copied === field ? '已复制' : field === 'account' ? '复制账号' : '复制密码' }}
        </button>
      </div>
    </dl>
    <template v-else-if="shown">
      <pre class="mt-5 max-w-full whitespace-pre-wrap break-words rounded-lg border border-gray-200 bg-gray-50 p-4 font-mono text-sm leading-6 text-gray-900 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100">{{ content }}</pre>
      <div class="mt-5 border-t border-gray-200 pt-5 dark:border-gray-800">
        <h3 class="text-sm font-semibold text-gray-900 dark:text-gray-100">逐项复制</h3>
        <ol class="mt-3 space-y-2">
          <li v-for="(line, index) in lines" :key="index" class="flex flex-wrap items-center gap-3 rounded-lg border border-gray-200 p-3 dark:border-gray-700">
            <span class="min-w-0 flex-1 basis-40 whitespace-pre-wrap break-all font-mono text-sm text-gray-700 dark:text-gray-300">{{ line }}</span>
            <button type="button" class="btn btn-secondary min-h-11 shrink-0" @click="copy(`line-${index}`, line)">{{ copied === `line-${index}` ? '已复制' : `复制第${index + 1}行` }}</button>
          </li>
        </ol>
      </div>
    </template>

    <div v-else class="mt-5 rounded-lg border border-dashed border-gray-300 bg-gray-50 p-4 dark:border-gray-700 dark:bg-gray-950">
      <p class="text-sm text-gray-600 dark:text-gray-400">{{ apiKey ? 'API Key 已隐藏，点击上方按钮查看或复制。' : '账号信息已隐藏，点击上方按钮查看或复制。' }}</p>
    </div>
    <span class="sr-only" role="status" aria-live="polite">{{ copied ? '已复制' : '' }}</span>
    <p v-if="error" class="mt-4 text-sm text-red-700 dark:text-red-300" role="alert">{{ error }}</p>
  </section>
</template>
