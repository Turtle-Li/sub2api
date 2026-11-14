<script setup lang="ts">
import { useAppStore } from '@/stores/app'
import type { ShopPassDurationDays } from '@/types/shop'
withDefaults(defineProps<{ showDeliveryTime?: boolean; automatic?: boolean; daypass?: boolean; concurrency?: number; durationDays?: ShopPassDurationDays }>(), { showDeliveryTime: true, automatic: false, daypass: false })
const app = useAppStore()
</script>
<template>
  <aside v-if="showDeliveryTime || automatic || app.contactInfo" class="space-y-2 text-xs leading-6 text-gray-500 dark:text-gray-400" aria-label="交付与客服">
    <p v-if="automatic && daypass">本单有效期 {{ durationDays === 7 ? '7 天' : '24 小时' }}。续费沿用所选卡的 Key，另购发放新 Key。{{ concurrency ? `所选 ${concurrency} 并发` : '默认 2 并发' }}在本单生效后启用。换网络前请停止调用 15 分钟。</p>
    <p v-else-if="automatic">付款确认后自动处理，交付结果可在订单中查看。处理异常时请联系客服，勿重复购买。</p>
    <p v-else-if="showDeliveryTime">5分钟–2小时操作完成，着急可联系客服</p>
    <p v-if="app.contactInfo" class="whitespace-pre-wrap break-words">联系客服：{{ app.contactInfo }}</p>
  </aside>
</template>
