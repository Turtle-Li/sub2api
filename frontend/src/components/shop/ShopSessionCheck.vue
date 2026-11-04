<script setup lang="ts">
import { computed } from 'vue'
import type { ShopInputCheck } from '@/types/shop'
const props = defineProps<{ check: ShopInputCheck; accountName?: string; local?: boolean }>()
const planNames: Record<string, string> = { free: 'Free（免费版）', plus: 'ChatGPT Plus', pro: 'ChatGPT Pro', team: 'Team', business: 'Business', enterprise: 'Enterprise', edu: 'Edu', go: 'ChatGPT Go' }
const plan = computed(() => planNames[props.check.plan_type] || '未识别')
const expired = computed(() => !!props.check.expires_at && Date.parse(props.check.expires_at) <= Date.now())
</script>
<template>
  <section class="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-gray-700 dark:bg-gray-800" aria-labelledby="shop-session-check-title">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h3 id="shop-session-check-title" class="text-base font-semibold text-gray-900 dark:text-gray-100">{{ local ? '本地初步识别' : '资料初步检查' }}</h3>
    </div>

    <dl class="mt-4 grid gap-4 text-sm sm:grid-cols-3">
      <div v-if="accountName" class="min-w-0 sm:col-span-3">
        <dt class="text-xs text-gray-500 dark:text-gray-400">账号名称</dt>
        <dd class="mt-1 break-words font-medium text-gray-900 dark:text-gray-100">{{ accountName }}</dd>
      </div>
      <div class="min-w-0 sm:col-span-3">
        <dt class="text-xs text-gray-500 dark:text-gray-400">识别到的账号</dt>
        <dd class="mt-1 break-all font-medium text-gray-900 dark:text-gray-100">{{ check.account || '未识别' }}</dd>
      </div>
      <div class="min-w-0">
        <dt class="text-xs text-gray-500 dark:text-gray-400">订阅类型</dt>
        <dd class="mt-1 break-words text-gray-800 dark:text-gray-200">{{ plan }}</dd>
      </div>
      <div class="min-w-0 sm:col-span-2">
        <dt class="text-xs text-gray-500 dark:text-gray-400">Session 有效期</dt>
        <dd class="mt-1 break-words text-gray-800 dark:text-gray-200">
          {{ check.expires_at || '未提供' }}
          <span v-if="expired" class="ml-2 font-medium text-amber-700 dark:text-amber-300">已过期</span>
        </dd>
      </div>
    </dl>

    <div v-if="check.warnings.length" class="mt-4 rounded-md border border-amber-200 bg-amber-50 p-3 dark:border-amber-900 dark:bg-amber-950">
      <h4 class="text-sm font-medium text-amber-900 dark:text-amber-100">请留意</h4>
      <ul class="mt-2 list-disc space-y-1 pl-5 text-sm leading-6 text-amber-800 dark:text-amber-200">
        <li v-for="(warning, index) in check.warnings" :key="index" class="break-words">{{ warning }}</li>
      </ul>
    </div>

    <p class="mt-4 text-xs leading-5 text-gray-600 dark:text-gray-400">{{ local ? '仅在当前浏览器解析，尚未提交；实际订阅以 ChatGPT 为准。' : '仅根据提交内容识别账号信息，实际订阅以 ChatGPT 为准。' }}</p>
  </section>
</template>
