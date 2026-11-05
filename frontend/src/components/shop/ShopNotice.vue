<script setup lang="ts">
import { useAppStore } from '@/stores/app'
withDefaults(defineProps<{ showDeliveryTime?: boolean; automatic?: boolean; daypass?: boolean; concurrency?: number }>(), { showDeliveryTime: true, automatic: false, daypass: false })
const app = useAppStore()
</script>
<template>
  <aside v-if="showDeliveryTime || automatic || app.contactInfo" class="space-y-2 text-xs leading-6 text-gray-500 dark:text-gray-400" aria-label="交付与客服">
    <p v-if="automatic && daypass">付款后自动发放独立 Key，付款时开始计时，有效期 24 小时。{{ concurrency ? `所选 ${concurrency} 并发` : '默认 2 并发' }}，同一 Key 同时仅限同一 IP；停止调用 15 分钟后可切换网络。</p>
    <p v-else-if="automatic">付款确认后自动处理，交付结果可在订单中查看。处理异常时请联系客服，勿重复购买。</p>
    <p v-else-if="showDeliveryTime">5分钟–2小时操作完成，着急可联系客服</p>
    <p v-if="app.contactInfo" class="whitespace-pre-wrap break-words">联系客服：{{ app.contactInfo }}</p>
  </aside>
</template>
