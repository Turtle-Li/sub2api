<script setup lang="ts">
import { reactive } from 'vue'
import QuotaLimitCard from '../../src/components/account/QuotaLimitCard.vue'
import AccountUsageCell from '../../src/components/account/AccountUsageCell.vue'
import AccountStatusIndicator from '../../src/components/account/AccountStatusIndicator.vue'
import type { Account } from '../../src/types'
import type { QuotaResetMode } from '../../src/constants/account'

const form = reactive({
  totalLimit: null as number | null, dailyLimit: null as number | null,
  dailyRequestLimit: 100 as number | null, weeklyLimit: null as number | null,
  dailyResetMode: 'fixed' as QuotaResetMode | null, dailyResetHour: 0 as number | null,
  weeklyResetMode: null as QuotaResetMode | null, weeklyResetDay: null as number | null,
  weeklyResetHour: null as number | null, resetTimezone: 'Asia/Shanghai' as string | null,
})
const account = reactive({
  id: -1, name: '公益生图站（示例）', platform: 'openai', type: 'apikey',
  status: 'active', schedulable: true, concurrency: 1, priority: 1,
  quota_daily_request_limit: 100, quota_daily_request_used: 7,
  extra: { quota_daily_request_start: new Date().toISOString() },
} as Account)

function savePreview() {
  account.quota_daily_request_limit = form.dailyRequestLimit ?? 0
}

function toggleTheme() {
  document.documentElement.classList.toggle('dark')
}
</script>

<template>
  <main style="max-width: 760px; margin: 0 auto; padding: 24px 16px" class="text-gray-900 dark:text-gray-100">
    <h1 class="mb-2 text-xl font-semibold">每日调用次数 · 本地验证</h1>
    <p class="mb-4 text-sm text-gray-500">使用真实配额组件与示例数据。保存仅更新本页预览。</p>
    <form @submit.prevent="savePreview">
      <QuotaLimitCard
        v-bind="form"
        @update:total-limit="form.totalLimit = $event"
        @update:daily-limit="form.dailyLimit = $event"
        @update:daily-request-limit="form.dailyRequestLimit = $event"
        @update:weekly-limit="form.weeklyLimit = $event"
        @update:daily-reset-mode="form.dailyResetMode = $event"
        @update:daily-reset-hour="form.dailyResetHour = $event"
        @update:weekly-reset-mode="form.weeklyResetMode = $event"
        @update:weekly-reset-day="form.weeklyResetDay = $event"
        @update:weekly-reset-hour="form.weeklyResetHour = $event"
        @update:reset-timezone="form.resetTimezone = $event"
      />
      <button type="submit" class="btn btn-primary mt-4">保存预览</button>
    </form>
    <section class="mt-6 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
      <h2 class="mb-3 font-medium">{{ account.name }}</h2>
      <AccountStatusIndicator :account="account" />
      <div class="mt-3"><AccountUsageCell :account="account" /></div>
      <div class="mt-4 flex flex-wrap gap-2">
        <button class="btn btn-secondary" @click="account.quota_daily_request_used = account.quota_daily_request_limit ?? 0">模拟已用满</button>
        <button class="btn btn-secondary" @click="account.quota_daily_request_used = 0">模拟次日重置</button>
        <button class="btn btn-secondary" @click="toggleTheme">切换深浅色</button>
      </div>
    </section>
  </main>
</template>
