<script setup lang="ts">
import { computed, ref } from 'vue'
const props = defineProps<{ content: string }>()
const shown = ref(false)
const message = ref('')
const lines = computed(() => props.content.split('\n').filter(line => line.trim()))
async function copy(value: string) {
  try { await navigator.clipboard.writeText(value); message.value = '已复制，请妥善保管。' }
  catch { message.value = '复制失败，请显示资料后手动复制。' }
}
</script>
<template>
  <section class="card space-y-4 p-6">
    <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="text-lg font-bold">账号已交付</h2><div class="flex gap-2"><button class="btn btn-secondary btn-sm" :aria-pressed="shown" @click="shown = !shown">{{ shown ? '隐藏资料' : '显示资料' }}</button><button class="btn btn-primary btn-sm" @click="copy(content)">复制整份资料</button></div></div>
    <p class="text-sm text-gray-500 dark:text-gray-400">请妥善保管账号资料，仅在可信设备上查看。</p>
    <ul class="divide-y divide-gray-100 dark:divide-dark-700"><li v-for="(line, index) in lines" :key="index" class="flex items-start justify-between gap-3 py-3"><span class="min-w-0 whitespace-pre-wrap break-all font-mono text-sm">{{ shown ? line : `资料 ${index + 1} · ••••••••` }}</span><button class="btn btn-secondary btn-sm shrink-0" :aria-label="`复制第 ${index + 1} 项资料`" @click="copy(line)">复制</button></li></ul>
    <p v-if="message" role="status" class="text-sm text-primary-700 dark:text-primary-300">{{ message }}</p>
  </section>
</template>
