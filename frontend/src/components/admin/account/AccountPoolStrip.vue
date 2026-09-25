<template>
  <div class="mt-3" data-testid="account-pool-strip">
    <div class="flex items-center gap-3">
      <button
        type="button"
        class="flex min-w-0 flex-1 items-center gap-2 rounded-lg px-1 py-1 text-left text-sm hover:bg-gray-50 dark:hover:bg-dark-800"
        :aria-expanded="expanded"
        data-testid="account-pool-strip-toggle"
        @click="expanded = !expanded"
      >
        <Icon :name="expanded ? 'chevronDown' : 'chevronRight'" size="sm" class="flex-shrink-0 text-gray-400" />
        <span class="font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.pools.title') }}</span>
        <span class="text-gray-500 dark:text-gray-400">{{ pools.length }}</span>
        <span class="truncate text-xs text-gray-500 dark:text-gray-400" data-testid="account-pool-strip-summary">
          · {{ t('admin.accounts.pools.stats.total') }} {{ totals.total }}
          · <span class="text-emerald-600 dark:text-emerald-400">{{ t('admin.accounts.pools.stats.normal') }} {{ totals.normal }}</span>
          <template v-if="totals.rate_limited > 0">
            · <span class="text-amber-600 dark:text-amber-400">{{ t('admin.accounts.pools.stats.rateLimited') }} {{ totals.rate_limited }}</span>
          </template>
          <template v-if="totals.error > 0">
            · <span class="text-red-600 dark:text-red-400">{{ t('admin.accounts.pools.stats.error') }} {{ totals.error }}</span>
          </template>
          <template v-if="totals.otherUnavailable > 0">
            · {{ t('admin.accounts.pools.stats.otherUnavailable') }} {{ totals.otherUnavailable }}
          </template>
        </span>
      </button>
      <button
        type="button"
        class="flex flex-shrink-0 items-center gap-1 text-xs text-gray-500 hover:text-primary-600 dark:text-gray-400 dark:hover:text-primary-400"
        data-testid="account-pool-create"
        @click="$emit('create')"
      >
        <Icon name="plus" size="sm" />
        {{ t('admin.accounts.pools.create') }}
      </button>
    </div>

    <div v-if="!expanded" class="mt-2 flex gap-2 overflow-x-auto pb-1">
      <button
        v-for="pool in pools"
        :key="pool.id"
        type="button"
        class="flex flex-shrink-0 items-center gap-1.5 rounded-lg border border-gray-200 bg-white px-2.5 py-1 text-xs shadow-sm transition-colors hover:border-primary-300 dark:border-dark-700 dark:bg-dark-800 dark:hover:border-primary-700"
        :data-testid="`account-pool-chip-${pool.id}`"
        @click="$emit('open', pool)"
      >
        <PlatformIcon :platform="pool.platform as GroupPlatform" size="xs" />
        <span class="max-w-[10rem] truncate font-medium text-gray-800 dark:text-gray-100" :title="pool.name">{{ pool.name }}</span>
        <span class="text-gray-500 dark:text-gray-400">{{ pool.stats.total }}</span>
        <span class="text-emerald-600 dark:text-emerald-400">✓{{ pool.stats.normal }}</span>
        <span v-if="pool.stats.rate_limited > 0" class="text-amber-600 dark:text-amber-400">⏱{{ pool.stats.rate_limited }}</span>
        <span v-if="pool.stats.error > 0" class="text-red-600 dark:text-red-400">✕{{ pool.stats.error }}</span>
      </button>
    </div>

    <div v-else class="mt-2 flex items-stretch gap-3 overflow-x-auto pb-1">
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
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { AccountPool } from '@/api/admin/accountPools'
import type { GroupPlatform } from '@/types'
import { formatCompactNumber } from '@/utils/format'
import { poolStatusSegments } from './accountPoolStats'

const props = defineProps<{
  pools: AccountPool[]
}>()

defineEmits<{
  open: [pool: AccountPool]
  create: []
}>()

const { t } = useI18n()

// Collapsed by default on every visit: one summary line plus compact chips.
const expanded = ref(false)

const totals = computed(() => {
  const sum = { total: 0, normal: 0, rate_limited: 0, error: 0, otherUnavailable: 0 }
  for (const pool of props.pools) {
    const stats = pool.stats
    sum.total += stats.total
    sum.normal += stats.normal
    sum.rate_limited += stats.rate_limited
    sum.error += stats.error
    sum.otherUnavailable += stats.temp_unschedulable + stats.unschedulable + stats.inactive
  }
  return sum
})

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
