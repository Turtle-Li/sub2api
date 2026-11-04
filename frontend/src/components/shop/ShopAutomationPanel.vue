<script setup lang="ts">
import { shopAutomationLabels, type ShopAutomationStatus } from '@/types/shop'
defineProps<{ automation: ShopAutomationStatus; busy: boolean }>()
defineEmits<{ retry: []; revoke: [] }>()
</script>
<template>
  <section aria-labelledby="automation-title" class="rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-gray-900">
    <h2 id="automation-title" class="text-base font-semibold text-gray-900 dark:text-gray-100">付款后操作</h2>
    <p class="mt-2 text-sm font-medium text-gray-800 dark:text-gray-200">{{ shopAutomationLabels[automation.status] }}</p>
    <dl class="mt-4 space-y-2 text-sm">
      <div class="flex justify-between gap-3"><dt class="text-gray-500 dark:text-gray-400">已尝试</dt><dd class="text-gray-900 dark:text-gray-100">{{ automation.attempts ?? 0 }} 次</dd></div>
      <div v-if="automation.next_attempt_at && automation.status === 'PENDING'" class="flex flex-col gap-1"><dt class="text-gray-500 dark:text-gray-400">下次尝试</dt><dd class="text-gray-900 dark:text-gray-100">{{ new Date(automation.next_attempt_at).toLocaleString('zh-CN') }}</dd></div>
      <div v-if="automation.error_kind" class="flex flex-col gap-1"><dt class="text-gray-500 dark:text-gray-400">待处理原因</dt><dd class="break-all text-gray-900 dark:text-gray-100">{{ automation.error_kind }}</dd></div>
    </dl>
    <p v-if="automation.revoke_required" class="mt-4 rounded-lg bg-amber-50 p-3 text-sm leading-6 text-amber-900 dark:bg-amber-950 dark:text-amber-200">退款前请先撤销此订单的授权，并确认状态为“授权已撤销”。撤销不会自动退款。</p>
    <p class="mt-4 text-xs leading-6 text-gray-500 dark:text-gray-400">重试使用原订单和原任务，不会重新开始有效期。通知重试与交付操作独立。</p>
    <div class="mt-3 flex flex-col gap-2 sm:flex-row sm:flex-wrap">
      <button v-if="automation.can_retry" type="button" :disabled="busy" class="btn btn-secondary min-h-11" @click="$emit('retry')">重试原任务</button>
      <button v-if="automation.can_revoke" type="button" :disabled="busy" class="btn btn-secondary min-h-11 text-red-700 dark:text-red-300" @click="$emit('revoke')">撤销此订单授权</button>
    </div>
  </section>
</template>
