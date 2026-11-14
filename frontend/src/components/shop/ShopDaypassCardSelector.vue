<script setup lang="ts">
import Icon from '@/components/icons/Icon.vue'
import type { ShopDaypassCard, ShopDaypassCards } from '@/types/shop'
import { daypassDateTime } from './daypassCheckout'

defineProps<{ inventory: ShopDaypassCards; disabled: boolean }>()
const mode = defineModel<'renew' | 'new'>('mode', { required: true })
const cardId = defineModel<number | null>('cardId', { required: true })
const labels: Record<ShopDaypassCard['status'], string> = {
  active: '使用中',
  scheduled: '待生效',
  expired: '已过期',
}
const plans: Record<ShopDaypassCard['last_duration_days'], string> = { 0: '旧卡', 1: '日卡', 7: '周卡' }
</script>

<template>
  <div class="grid gap-3">
    <fieldset class="min-w-0" :disabled="disabled">
      <legend class="text-sm font-semibold text-gray-900 dark:text-gray-100">购买方式</legend>
      <div class="mt-1 grid grid-cols-2 gap-1 rounded-xl border border-gray-200 bg-gray-50 p-1 dark:border-gray-700 dark:bg-gray-800/70">
        <label class="mode-choice" :class="{ selected: mode === 'renew' }">
          <input v-model="mode" type="radio" name="daypass-mode" value="renew" :disabled="inventory.cards.length === 0" />
          <span><strong>续费已有卡</strong><small>继续用原 Key</small></span>
        </label>
        <label class="mode-choice" :class="{ selected: mode === 'new' }">
          <input v-model="mode" type="radio" name="daypass-mode" value="new" />
          <span><strong>另购一张卡</strong><small>独立新 Key</small></span>
        </label>
      </div>
      <p v-if="!inventory.cards.length" class="mt-1.5 text-xs text-gray-600 dark:text-gray-400">没有可续费的卡。另购会发放新 Key。</p>
    </fieldset>

    <fieldset v-if="mode === 'renew' && inventory.cards.length" class="min-w-0" :disabled="disabled">
      <legend class="text-sm font-semibold text-gray-900 dark:text-gray-100">续费哪张卡？</legend>
      <p v-if="inventory.cards.length > 1 && inventory.cards.every(card => card.status === 'expired')" class="mt-1 text-xs text-gray-600 dark:text-gray-400">
        这些卡已过期，选一张恢复使用。
      </p>
      <div class="mt-1.5 grid gap-2">
        <label v-for="card in inventory.cards" :key="card.card_id" class="card-choice" :class="{ selected: cardId === card.card_id }">
          <input v-model="cardId" type="radio" name="daypass-card" :value="card.card_id" />
          <span class="plan-mark" :class="{ 'plan-mark--active': card.status === 'active' }">
            <span class="sr-only">上次套餐：</span>{{ plans[card.last_duration_days] }}
            <span v-if="card.last_duration_days" aria-hidden="true" class="plan-ticks" :class="{ 'plan-ticks--week': card.last_duration_days === 7 }"></span>
          </span>
          <span class="card-meta">
            <span class="card-id"><strong>卡 {{ card.card_id }}</strong><small>尾号 <span class="font-mono">{{ card.key_suffix }}</span></small></span>
            <span class="card-status" :class="{ 'card-status--active': card.status === 'active' }">{{ labels[card.status] }}</span>
            <span class="card-clock">
              <Icon name="clock" class="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
              <span class="sr-only">到期时间：</span>
              <time :datetime="new Date(card.expires_at * 1000).toISOString()">{{ daypassDateTime(card.expires_at, inventory.server_time) }}</time>
            </span>
            <span v-if="card.next_starts_at" class="card-clock pl-[1.125rem]">生效 {{ daypassDateTime(card.next_starts_at, inventory.server_time) }}</span>
          </span>
        </label>
      </div>
    </fieldset>
  </div>
</template>

<style scoped>
.mode-choice, .card-choice {
  @apply cursor-pointer rounded-lg border border-transparent text-sm text-gray-900 dark:text-gray-100;
}
.mode-choice {
  @apply flex min-h-11 items-start gap-1.5 px-1.5 py-1.5 sm:px-2;
}
.mode-choice strong, .card-id strong { @apply block font-semibold; }
.mode-choice small, .card-id small { @apply block text-xs text-gray-600 dark:text-gray-400; }
.mode-choice input, .card-choice input { @apply shrink-0 accent-primary-700 dark:accent-primary-400; }
.mode-choice input { @apply mt-1; }
.mode-choice.selected {
  @apply border-primary-300 bg-white shadow-sm dark:border-primary-700 dark:bg-gray-900;
}
.mode-choice:has(input:disabled) { @apply cursor-not-allowed opacity-60; }
.mode-choice:has(input:focus-visible), .card-choice:has(input:focus-visible) {
  @apply ring-2 ring-primary-500 ring-offset-2 dark:ring-offset-gray-900;
}
.card-choice {
  @apply grid min-h-16 grid-cols-[auto_2.625rem_minmax(0,1fr)] items-center gap-2 border-gray-200 bg-white p-2 dark:border-gray-700 dark:bg-gray-900;
}
.card-choice.selected {
  @apply border-primary-600 bg-primary-50 ring-1 ring-inset ring-primary-600 dark:border-primary-400 dark:bg-primary-900/30 dark:ring-primary-400;
}
.plan-mark {
  @apply grid min-h-11 content-center justify-items-center gap-1 rounded-lg border border-dashed border-gray-300 bg-white px-1 text-xs font-semibold text-gray-600 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-400;
}
.plan-mark--active {
  @apply border-solid border-primary-300 text-primary-700 dark:border-primary-700 dark:text-primary-300;
}
.plan-ticks { @apply h-0.5 w-full rounded-sm bg-current opacity-75; }
.plan-ticks--week {
  background: linear-gradient(90deg, currentColor calc(100% - 2px), transparent 0) 0 0 / calc((100% + 2px) / 7) 100% repeat-x;
}
.card-meta {
  @apply grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-start gap-x-1 gap-y-0.5;
}
.card-id { @apply flex min-w-0 flex-wrap items-baseline gap-x-1.5; }
.card-status {
  @apply inline-flex items-center gap-1 self-start whitespace-nowrap rounded-full bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-gray-800 dark:text-gray-300;
}
.card-status::before { @apply h-1.5 w-1.5 rounded-full border border-current; content: ''; }
.card-status--active {
  @apply bg-primary-50 text-primary-700 dark:bg-primary-900/60 dark:text-primary-300;
}
.card-status--active::before { @apply bg-current; }
.card-clock { @apply col-span-2 inline-flex min-w-0 items-center gap-1 text-xs tabular-nums text-gray-600 dark:text-gray-300; }
</style>
