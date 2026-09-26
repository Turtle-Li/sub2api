<template>
  <div class="mt-3" data-testid="proxy-pool-strip">
    <div class="flex items-center gap-3">
      <button
        type="button"
        class="flex min-w-0 flex-1 items-center gap-2 rounded-lg px-1 py-1 text-left text-sm hover:bg-gray-50 dark:hover:bg-dark-800"
        :aria-expanded="expanded"
        data-testid="proxy-pool-strip-toggle"
        @click="expanded = !expanded"
      >
        <Icon :name="expanded ? 'chevronDown' : 'chevronRight'" size="sm" class="flex-shrink-0 text-gray-400" />
        <span class="font-medium text-gray-900 dark:text-white">{{ t('admin.proxies.pools.title') }}</span>
        <span class="text-gray-500 dark:text-gray-400">{{ pools.length }}</span>
        <span class="truncate text-xs text-gray-500 dark:text-gray-400">
          · {{ t('admin.proxies.pools.stats.total') }} {{ totals.total }}
          · <span class="text-emerald-600 dark:text-emerald-400">{{ t('admin.proxies.pools.stats.available') }} {{ totals.available }}</span>
          <template v-if="totals.unavailable > 0">
            · <span class="text-amber-600 dark:text-amber-400">{{ t('admin.proxies.pools.stats.unavailable') }} {{ totals.unavailable }}</span>
          </template>
          <template v-if="totals.boundAccounts > 0">
            · {{ t('admin.proxies.pools.stats.boundAccounts') }} {{ totals.boundAccounts }}
          </template>
        </span>
      </button>
      <button
        type="button"
        class="flex flex-shrink-0 items-center gap-1 text-xs text-gray-500 hover:text-primary-600 dark:text-gray-400 dark:hover:text-primary-400"
        data-testid="proxy-pool-create"
        @click="$emit('create')"
      >
        <Icon name="plus" size="sm" />
        {{ t('admin.proxies.pools.create') }}
      </button>
    </div>

    <div v-if="!expanded" class="mt-2 flex gap-2 overflow-x-auto pb-1">
      <button
        v-for="pool in pools"
        :key="pool.id"
        type="button"
        class="flex flex-shrink-0 items-center gap-1.5 rounded-lg border border-gray-200 bg-white px-2.5 py-1 text-xs shadow-sm transition-colors hover:border-primary-300 dark:border-dark-700 dark:bg-dark-800 dark:hover:border-primary-700"
        :data-testid="`proxy-pool-chip-${pool.id}`"
        @click="$emit('open', pool)"
      >
        <Icon name="server" size="sm" class="text-primary-500" />
        <span class="max-w-[12rem] truncate font-medium text-gray-800 dark:text-gray-100" :title="pool.name">{{ pool.name }}</span>
        <span class="text-gray-500 dark:text-gray-400">{{ pool.stats.total }}</span>
        <span class="text-emerald-600 dark:text-emerald-400">✓{{ pool.stats.available }}</span>
        <span v-if="pool.stats.unavailable > 0" class="text-amber-600 dark:text-amber-400">!{{ pool.stats.unavailable }}</span>
      </button>
    </div>

    <div v-else class="mt-2 flex items-stretch gap-3 overflow-x-auto pb-1">
      <button
        v-for="pool in pools"
        :key="pool.id"
        type="button"
        class="group flex w-64 flex-shrink-0 flex-col gap-2 rounded-lg border border-gray-200 bg-white p-3 text-left shadow-sm transition-colors hover:border-primary-300 hover:bg-primary-50/40 dark:border-dark-700 dark:bg-dark-800 dark:hover:border-primary-700 dark:hover:bg-primary-900/10"
        :data-testid="`proxy-pool-card-${pool.id}`"
        @click="$emit('open', pool)"
      >
        <div class="flex items-center gap-2">
          <Icon name="server" size="sm" class="flex-shrink-0 text-primary-500" />
          <span class="min-w-0 flex-1 truncate text-sm font-semibold text-gray-900 dark:text-white" :title="pool.name">{{ pool.name }}</span>
          <span class="rounded-md bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ pool.stats.total }}</span>
        </div>
        <div class="flex h-1.5 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
          <div class="bg-emerald-500" :style="{ width: percent(pool.stats.available, pool.stats.total) }" />
          <div class="bg-amber-500" :style="{ width: percent(pool.stats.unavailable, pool.stats.total) }" />
        </div>
        <div class="flex flex-wrap gap-x-3 gap-y-1 text-xs">
          <span class="text-emerald-600 dark:text-emerald-400">{{ t('admin.proxies.pools.stats.available') }} {{ pool.stats.available }}</span>
          <span v-if="pool.stats.unavailable > 0" class="text-amber-600 dark:text-amber-400">{{ t('admin.proxies.pools.stats.unavailable') }} {{ pool.stats.unavailable }}</span>
          <span class="text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.stats.boundAccounts') }} {{ pool.stats.bound_accounts }}</span>
        </div>
        <p v-if="pool.notes" class="line-clamp-2 text-xs text-gray-500 dark:text-gray-400">{{ pool.notes }}</p>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { ProxyPool } from '@/types'

const props = defineProps<{ pools: ProxyPool[] }>()

defineEmits<{
  open: [pool: ProxyPool]
  create: []
}>()

const { t } = useI18n()
const expanded = ref(false)

const totals = computed(() => props.pools.reduce(
  (sum, pool) => ({
    total: sum.total + pool.stats.total,
    available: sum.available + pool.stats.available,
    unavailable: sum.unavailable + pool.stats.unavailable,
    boundAccounts: sum.boundAccounts + pool.stats.bound_accounts
  }),
  { total: 0, available: 0, unavailable: 0, boundAccounts: 0 }
))

const percent = (value: number, total: number) => total > 0 ? `${(value / total) * 100}%` : '0%'
</script>
