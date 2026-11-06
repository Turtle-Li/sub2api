<script setup lang="ts">
import { computed } from 'vue'
import DOMPurify from 'dompurify'
import chatgptGuide from '@/content/shop/chatgpt-plus-account.html?raw'
import deepseekGuide from '@/content/shop/deepseek-daypass.html?raw'
import type { ShopProduct } from '@/types/shop'

type GuideKey = Exclude<ShopProduct['guide_key'], ''>

const props = defineProps<{ guideKey: GuideKey; embedded?: boolean }>()
const sources: Record<GuideKey, string> = {
  'chatgpt-plus-account': chatgptGuide,
  'deepseek-daypass': deepseekGuide,
}
const html = computed(() => {
  const source = sources[props.guideKey]
  if (typeof source !== 'string') return ''
  const markup = props.embedded
    ? source.replace(/<(\/?)h([2-4])(?=[\s>])/g, (_tag, slash: string, level: string) => `<${slash}h${Number(level) + 2}`)
    : source
  return DOMPurify.sanitize(markup)
})
</script>

<template>
  <div v-if="embedded" class="inline-guide" v-html="html" />
  <article v-else v-html="html" />
</template>

<style scoped>
.inline-guide {
  @apply min-w-0 break-words text-sm leading-6 text-gray-700 dark:text-gray-300;
}
.inline-guide :deep(.guide-section) { @apply scroll-mt-24; }
.inline-guide :deep(.guide-section + .guide-section) { @apply mt-4 border-t border-gray-200 pt-4 dark:border-gray-800; }
.inline-guide :deep(h4) { @apply text-base font-semibold text-gray-900 dark:text-gray-100; }
.inline-guide :deep(h5), .inline-guide :deep(h6) { @apply mt-3 font-semibold text-gray-900 dark:text-gray-100; }
.inline-guide :deep(.guide-section-number) { @apply mr-2 text-primary-700 dark:text-primary-300; }
.inline-guide :deep(p), .inline-guide :deep(ul), .inline-guide :deep(ol), .inline-guide :deep(dl) { @apply mt-2; }
.inline-guide :deep(ul) { @apply list-disc pl-5; }
.inline-guide :deep(ol) { @apply list-decimal pl-5; }
.inline-guide :deep(a) { @apply text-primary-700 underline underline-offset-2 dark:text-primary-300; }
.inline-guide :deep(dl:not(.guide-preparation)) {
  @apply grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 rounded-md bg-gray-50 px-3 py-2 dark:bg-gray-800/70;
}
.inline-guide :deep(dt) { @apply font-medium text-gray-900 dark:text-gray-100; }
.inline-guide :deep(.guide-preparation > div + div) { @apply mt-2; }
.inline-guide :deep(code), .inline-guide :deep(kbd) { @apply rounded bg-gray-100 px-1 text-xs dark:bg-gray-800; }
.inline-guide :deep(code) {
  @apply inline-block max-w-full align-baseline;
  overflow-wrap: anywhere;
}
.inline-guide :deep(.guide-hint), .inline-guide :deep(.guide-callout) { @apply mt-2 rounded-md bg-gray-50 px-3 py-2 dark:bg-gray-800/70; }
.inline-guide :deep(.guide-callout > p) { @apply mt-0; }
.inline-guide :deep(figure) { @apply my-3; }
.inline-guide :deep(figure img) { @apply h-auto max-w-full rounded-md border border-gray-200 dark:border-gray-800; }
.inline-guide :deep(figcaption) { @apply mt-1 text-xs text-gray-600 dark:text-gray-400; }
.inline-guide :deep(.guide-table-wrap) { @apply mt-2 max-w-full overflow-x-auto; }
.inline-guide :deep(th), .inline-guide :deep(td) { @apply border border-gray-200 p-2 text-left dark:border-gray-700; }
.inline-guide :deep(details) { @apply mt-2 border-t border-gray-200 dark:border-gray-800; }
.inline-guide :deep(summary) { @apply min-h-11 cursor-pointer py-2 font-medium text-gray-900 dark:text-gray-100; }
</style>
