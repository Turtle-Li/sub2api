<script setup lang="ts">
import { onMounted, ref } from 'vue'
import ScheduledTestsPanel from '../../src/components/admin/account/ScheduledTestsPanel.vue'
import { adminAPI } from '../../src/api/admin'
import type { ScheduledTestPlan, ScheduledTestResult } from '../../src/types'

const show = ref(false)
const now = new Date().toISOString()
let plans: ScheduledTestPlan[] = [{
  id: 1, account_id: -1, model_id: 'small-chat-model', cron_expression: '0 * * * *',
  enabled: true, max_results: 50, auto_recover: false, idle_hours: 48,
  last_activity_at: new Date(Date.now() - 54 * 3600000).toISOString(),
  last_success_at: null, last_run_at: null, next_run_at: now, created_at: now, updated_at: now,
}]
adminAPI.scheduledTests.listByAccount = async () => structuredClone(plans)
adminAPI.scheduledTests.listResults = async (id) => [{
  id: 1, plan_id: id, status: 'success', response_text: '本地示例响应', error_message: '',
  latency_ms: 230, started_at: now, finished_at: now, created_at: now,
} satisfies ScheduledTestResult]
adminAPI.scheduledTests.create = async (data) => {
  if (data.idle_hours && data.enabled && plans.some(p => p.enabled && p.idle_hours)) {
    throw new Error('每个账号最多启用一个保号计划')
  }
  const plan: ScheduledTestPlan = {
    id: Date.now(), account_id: data.account_id, model_id: data.model_id,
    cron_expression: data.cron_expression, enabled: data.enabled ?? true,
    max_results: data.max_results ?? 50, auto_recover: data.auto_recover ?? false,
    idle_hours: data.idle_hours ?? 0, last_activity_at: now,
    last_run_at: null, next_run_at: now, created_at: now, updated_at: now,
  }
  plans.push(plan)
  return structuredClone(plan)
}
adminAPI.scheduledTests.update = async (id, patch) => {
  const plan = plans.find(p => p.id === id)!
  Object.assign(plan, patch)
  return structuredClone(plan)
}
adminAPI.scheduledTests.delete = async id => { plans = plans.filter(p => p.id !== id) }
onMounted(() => { show.value = true })
</script>

<template>
  <main class="p-6 text-gray-900 dark:text-gray-100">
    <h1 class="text-xl font-semibold">账号自动保号 · 本地验证</h1>
    <p class="my-3 text-sm text-gray-500">真实定时测试组件与内存示例数据，保存仅影响本页。</p>
    <button class="btn btn-primary" @click="show = true">打开定时测试</button>
    <ScheduledTestsPanel :show="show" :account-id="-1" :model-options="[{ value: 'small-chat-model', label: 'small-chat-model' }]" @close="show = false" />
  </main>
</template>
