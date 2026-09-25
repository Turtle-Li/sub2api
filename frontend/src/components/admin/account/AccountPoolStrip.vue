<template>
  <div class="mt-3 flex items-stretch gap-3 overflow-x-auto pb-1" data-testid="account-pool-strip">
    <button
      v-for="pool in pools"
      :key="pool.id"
      type="button"
      class="group flex w-64 flex-shrink-0 flex-col gap-2 rounded-xl border border-gray-200 bg-white p-3 text-left shadow-sm transition-colors hover:border-primary-300 hover:bg-primary-50/40 dark:border-dark-700 dark:bg-dark-800 dark:hover:border-primary-700 dark:hover:bg-primary-900/10"
      :data-testid="`account-pool-card-${pool.id}`"
      @click="$emit('open', pool)"
    >
      <div class="flex items-center gap-2">
        <Icon name="database" size="sm" class="flex-shrink-0 text-primary-500" />
        <span class="min-w-0 flex-1 truncate text-sm font-semibold text-gray-900 dark:text-white" :title="pool.name">{{ pool.name }}</span>
        <span class="inline-flex flex-shrink-0 items-center gap-1 rounded-md bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-dark-700 dark:text-gray-300">
          <PlatformIcon :platform="pool.platform as GroupPlatform" size="xs" />
          {{ pool.stats.total }}
        </span>
      </div>

      <div class="flex h-1.5 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
        <div
          v-for="segment in pool.stats.total > 0 ? poolStatusSegments(pool.stats) : []"
          :key="segment.key"
          :class="segment.barClass"
          :style="{ width: `${(segment.count / pool.stats.total) * 100}%` }"
        />
      </div>

      <div class="flex flex-wrap gap-x-2 gap-y-0.5 text-xs">
        <span class="text-emerald-600 dark:text-emerald-400">{{ t('admin.accounts.pools.stats.normal') }} {{ pool.stats.normal }}</span>
        <span
          v-for="segment in poolStatusSegments(pool.stats).filter(s => s.key !== 'normal')"
          :key="segment.key"
          :class="segment.textClass"
        >
          {{ t(segment.labelKey) }} {{ segment.count }}
        </span>
        <span v-if="pool.stats.expired > 0" class="text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.pools.stats.expired') }} {{ pool.stats.expired }}
        </span>
      </div>

      <div class="space-y-0.5 text-xs text-gray-500 dark:text-gray-400">
        <div v-if="pool.usage?.grok_free" class="flex items-center justify-between gap-2" :title="t('admin.accounts.pools.grokFreeHint')">
          <span>{{ t('admin.accounts.pools.grokFreeQuota') }}</span>
          <span :class="quotaClass(grokFreePercent(pool))">
            {{ formatCompactNumber(pool.usage.grok_free.used_tokens) }} / {{ formatCompactNumber(pool.usage.grok_free.limit_tokens) }}
            ({{ grokFreePercent(pool) }}%)
          </span>
        </div>
        <div v-if="pool.stats.codex" class="flex items-center justify-between gap-2">
          <span>{{ t('admin.accounts.pools.codexQuota') }}</span>
          <span>
            5h <span :class="quotaClass(pool.stats.codex.avg_5h_used_percent)">{{ formatPercent(pool.stats.codex.avg_5h_used_percent) }}</span>
            · 7d <span :class="quotaClass(pool.stats.codex.avg_7d_used_percent)">{{ formatPercent(pool.stats.codex.avg_7d_used_percent) }}</span>
            <template v-if="pool.stats.codex.exhausted > 0">
              · <span class="text-red-500">{{ t('admin.accounts.pools.exhausted', { count: pool.stats.codex.exhausted }) }}</span>
            </template>
          </span>
        </div>
        <div v-if="pool.usage" class="flex items-center justify-between gap-2">
          <span>{{ t('admin.accounts.pools.usageWindow', { hours: pool.usage.window_hours }) }}</span>
          <span>
            {{ t('admin.accounts.pools.requests', { count: formatCompactNumber(pool.usage.requests) }) }}
            · ${{ pool.usage.cost.toFixed(2) }}
          </span>
        </div>
      </div>
    </button>

    <button
      type="button"
      class="flex w-28 flex-shrink-0 flex-col items-center justify-center gap-1 rounded-xl border border-dashed border-gray-300 text-xs text-gray-500 transition-colors hover:border-primary-400 hover:text-primary-600 dark:border-dark-600 dark:text-gray-400 dark:hover:border-primary-600 dark:hover:text-primary-400"
      data-testid="account-pool-create"
      @click="$emit('create')"
    >
      <Icon name="plus" size="md" />
      {{ t('admin.accounts.pools.create') }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { AccountPool } from '@/api/admin/accountPools'
import type { GroupPlatform } from '@/types'
import { formatCompactNumber } from '@/utils/format'
import { poolStatusSegments } from './accountPoolStats'

defineProps<{
  pools: AccountPool[]
}>()

defineEmits<{
  open: [pool: AccountPool]
  create: []
}>()

const { t } = useI18n()

const grokFreePercent = (pool: AccountPool) => {
  const quota = pool.usage?.grok_free
  if (!quota || quota.limit_tokens <= 0) return 0
  return Math.min(100, Math.round((quota.used_tokens / quota.limit_tokens) * 100))
}

const formatPercent = (value?: number | null) => (value == null ? '-' : `${Math.round(value)}%`)

const quotaClass = (value?: number | null) => {
  if (value == null) return ''
  if (value >= 90) return 'text-red-500'
  if (value >= 70) return 'text-amber-500'
  return 'text-emerald-600 dark:text-emerald-400'
}
</script>
