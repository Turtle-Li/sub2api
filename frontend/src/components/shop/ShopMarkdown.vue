<script setup lang="ts">
import { computed } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
const props = defineProps<{ content: string }>()
const html = computed(() => DOMPurify.sanitize(marked.parse(props.content, { async: false, gfm: true }), { FORBID_TAGS: ['form', 'input', 'button', 'iframe', 'style'] }))
</script>
<template><article class="shop-markdown prose max-w-none break-words dark:prose-invert" v-html="html" /></template>
<style scoped>
.shop-markdown :deep(img) { max-width: 100%; height: auto; border-radius: 0.75rem; margin: 1.5rem 0; }
.shop-markdown :deep(table) { display: block; max-width: 100%; overflow-x: auto; }
.shop-markdown :deep(td), .shop-markdown :deep(th) { padding: 0.5rem; border: 1px solid currentColor; }
.shop-markdown :deep(pre) { overflow-x: auto; white-space: pre-wrap; }
.shop-markdown :deep(h1), .shop-markdown :deep(h2), .shop-markdown :deep(h3) { font-weight: 700; margin: 1.75rem 0 0.75rem; scroll-margin-top: 5rem; }
.shop-markdown :deep(h1) { font-size: 1.75rem; }
.shop-markdown :deep(h2) { font-size: 1.35rem; }
.shop-markdown :deep(p), .shop-markdown :deep(ul), .shop-markdown :deep(ol) { margin: 0.75rem 0; line-height: 1.8; }
.shop-markdown :deep(ul), .shop-markdown :deep(ol) { padding-left: 1.5rem; list-style: revert; }
.shop-markdown :deep(a) { text-decoration: underline; text-underline-offset: 3px; }
</style>
