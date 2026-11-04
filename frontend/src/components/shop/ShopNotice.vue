<script setup lang="ts">
import { useAppStore } from '@/stores/app'
withDefaults(defineProps<{ showDeliveryTime?: boolean }>(), { showDeliveryTime: true })
const app = useAppStore()
</script>
<template>
  <aside v-if="showDeliveryTime || app.contactInfo" class="space-y-2 text-xs leading-6 text-gray-500 dark:text-gray-400" aria-label="交付与客服">
    <p v-if="showDeliveryTime">5分钟–2小时操作完成，着急可联系客服</p>
    <p v-if="app.contactInfo" class="whitespace-pre-wrap break-words">联系客服：{{ app.contactInfo }}</p>
  </aside>
</template>
