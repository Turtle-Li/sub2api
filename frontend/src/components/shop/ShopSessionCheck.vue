<script setup lang="ts">
import { computed } from 'vue'
import type { ShopInputCheck } from '@/types/shop'
const props = defineProps<{ check: ShopInputCheck }>()
const planNames: Record<string, string> = { free: 'Free（免费版）', plus: 'ChatGPT Plus', pro: 'ChatGPT Pro', team: 'Team', business: 'Business', enterprise: 'Enterprise', edu: 'Edu', go: 'ChatGPT Go' }
const plan = computed(() => planNames[props.check.plan_type] || '未识别')
const expired = computed(() => !!props.check.expires_at && Date.parse(props.check.expires_at) <= Date.now())
</script>
<template>
  <section class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-800" aria-label="Session 资料初检">
    <h3 class="font-semibold">资料初检</h3>
    <dl class="mt-3 grid gap-3 text-sm sm:grid-cols-2">
      <div class="min-w-0"><dt class="text-gray-500 dark:text-gray-400">目标账号</dt><dd class="mt-1 break-all font-medium">{{ check.account }}</dd></div>
      <div><dt class="text-gray-500 dark:text-gray-400">资料中的当前订阅</dt><dd class="mt-1 font-medium">{{ plan }}</dd></div>
      <div v-if="check.expires_at" class="sm:col-span-2"><dt class="text-gray-500 dark:text-gray-400">资料有效期至</dt><dd class="mt-1" :class="expired ? 'text-amber-700 dark:text-amber-300' : ''">{{ new Date(check.expires_at).toLocaleString() }}{{ expired ? '（已过期，请更新资料）' : '' }}</dd></div>
    </dl>
    <p v-for="warning in check.warnings" :key="warning" class="mt-3 text-sm text-amber-700 dark:text-amber-300">{{ warning }}</p>
    <p class="mt-3 text-xs leading-5 text-gray-500 dark:text-gray-400">仅检查资料格式与信息一致性，未在线验证令牌和官方订阅状态。</p>
  </section>
</template>
