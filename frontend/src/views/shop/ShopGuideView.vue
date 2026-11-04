<script setup lang="ts">
import DOMPurify from 'dompurify'
import AppLayout from '@/components/layout/AppLayout.vue'
import content from '@/content/shop/chatgpt-plus-account.html?raw'

const html = DOMPurify.sanitize(content)
const sections = [
  { id: 'prepare', title: '开始前准备' },
  { id: 'email-login', title: '登录微软邮箱' },
  { id: 'chatgpt-login', title: '登录 ChatGPT' },
  { id: 'account-security', title: '账号安全设置' },
  { id: 'checklist', title: '完成检查' },
  { id: 'faq', title: '常见问题' },
  { id: 'warranty', title: '售后与质保' }
]
</script>
<template>
  <AppLayout>
    <div class="guide-page mx-auto max-w-6xl">
      <RouterLink to="/shop" class="guide-back">← 返回商城</RouterLink>
      <header class="guide-header">
        <h1>ChatGPT Plus 成品号使用指南</h1>
        <p>从首次登录到安全设置，按步骤完成即可。</p>
      </header>
      <div class="guide-layout">
        <aside class="guide-sidebar">
          <nav aria-label="教程目录" class="guide-desktop-nav">
            <p>本页内容</p>
            <ol><li v-for="section in sections" :key="section.id"><a :href="`#${section.id}`">{{ section.title }}</a></li></ol>
          </nav>
          <details class="guide-mobile-nav">
            <summary>跳转到章节</summary>
            <nav aria-label="教程目录"><ol><li v-for="section in sections" :key="section.id"><a :href="`#${section.id}`">{{ section.title }}</a></li></ol></nav>
          </details>
        </aside>
        <article class="account-guide" aria-label="成品号操作步骤" v-html="html" />
      </div>
    </div>
  </AppLayout>
</template>

<style scoped>
.guide-page {
  --guide-ink: theme('colors.gray.900');
  --guide-muted: theme('colors.gray.600');
  --guide-line: theme('colors.gray.200');
  --guide-paper: theme('colors.white');
  --guide-soft: theme('colors.gray.50');
  --guide-accent: theme('colors.primary.700');
  --guide-tint: theme('colors.primary.50');
  color: var(--guide-ink);
}
.dark .guide-page {
  --guide-ink: theme('colors.gray.100');
  --guide-muted: theme('colors.gray.300');
  --guide-line: theme('colors.dark.700');
  --guide-paper: theme('colors.dark.900');
  --guide-soft: theme('colors.dark.800');
  --guide-accent: theme('colors.primary.300');
  --guide-tint: theme('colors.primary.950');
}
.guide-back { color: var(--guide-accent); font-size: .875rem; }
.guide-back:hover { text-decoration: underline; text-underline-offset: 4px; }
.guide-header { padding: 1.75rem 0 2.5rem; }
.guide-header h1 { max-width: 24ch; font-size: clamp(1.65rem, 2.5vw, 2.25rem); font-weight: 750; line-height: 1.35; letter-spacing: -.025em; text-wrap: balance; }
.guide-header p { margin-top: .8rem; color: var(--guide-muted); line-height: 1.7; }
.guide-layout { display: grid; grid-template-columns: 10.5rem minmax(0, 1fr); align-items: start; gap: 2.5rem; }
.guide-sidebar { position: sticky; top: 6rem; }
.guide-desktop-nav p { margin-bottom: .7rem; color: var(--guide-muted); font-size: .75rem; }
.guide-desktop-nav a { display: block; padding: .65rem 0; font-size: .875rem; color: var(--guide-muted); line-height: 1.6; }
.guide-desktop-nav a:hover { color: var(--guide-accent); }
.guide-mobile-nav { display: none; }
.account-guide { min-width: 0; padding: 2rem 2.5rem; border: 1px solid var(--guide-line); border-radius: 16px; background: var(--guide-paper); font-size: .9375rem; line-height: 1.9; overflow-wrap: anywhere; }
.account-guide :deep(p) { margin: .8rem 0; }
.account-guide :deep(strong) { font-weight: 650; }
.account-guide :deep(a) { color: var(--guide-accent); text-decoration: underline; text-underline-offset: 4px; }
.account-guide :deep(a:hover) { text-decoration-thickness: 2px; }
.guide-page :deep(a:focus-visible), .guide-page :deep(summary:focus-visible) { outline: 2px solid var(--guide-accent); outline-offset: 4px; border-radius: 4px; }
.account-guide :deep(.guide-overview > p) { margin-top: 0; color: var(--guide-muted); }
.account-guide :deep(.guide-route) { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 1rem; padding: 1.4rem 0 .5rem; list-style: none; }
.account-guide :deep(.guide-route a) { display: block; text-decoration: none; }
.account-guide :deep(.guide-route span) { display: inline-flex; align-items: center; justify-content: center; width: 1.75rem; height: 1.75rem; border-radius: 50%; background: var(--guide-tint); color: var(--guide-accent); font-size: .875rem; font-weight: 700; }
.account-guide :deep(.guide-route strong) { display: block; margin-top: .6rem; color: var(--guide-ink); font-size: .9375rem; }
.account-guide :deep(.guide-route small) { display: block; margin-top: .3rem; font-size: .75rem; line-height: 1.7; color: var(--guide-muted); }
.account-guide :deep(.guide-section) { padding-top: 2.5rem; margin-top: 2.5rem; border-top: 1px solid var(--guide-line); scroll-margin-top: 6rem; }
.account-guide :deep(h2) { display: flex; align-items: center; gap: .75rem; margin-bottom: 1.25rem; font-size: 1.4rem; font-weight: 750; line-height: 1.5; letter-spacing: -.015em; }
.account-guide :deep(.guide-section-number) { display: inline-flex; align-items: center; justify-content: center; flex: 0 0 2rem; height: 2rem; border-radius: 8px; color: var(--guide-accent); background: var(--guide-tint); font-size: 1rem; font-weight: 700; }
.account-guide :deep(h3) { margin: 1.75rem 0 .75rem; font-size: 1.0625rem; font-weight: 700; line-height: 1.7; }
.account-guide :deep(h4) { margin: 1.5rem 0 .6rem; font-size: .9375rem; font-weight: 700; }
.account-guide :deep(.guide-step + .guide-step) { margin-top: 2rem; }
.account-guide :deep(ol:not(.guide-route)), .account-guide :deep(ul:not(.guide-checklist)) { padding-left: 1.4rem; }
.account-guide :deep(ol:not(.guide-route)) { list-style: decimal; }
.account-guide :deep(ul:not(.guide-checklist)) { list-style: disc; }
.account-guide :deep(li + li) { margin-top: .5rem; }
.account-guide :deep(.guide-route li + li) { margin-top: 0; }
.account-guide :deep(.guide-preparation > div) { display: grid; grid-template-columns: 5.5rem minmax(0, 1fr); gap: 1rem; padding: .9rem 0; border-bottom: 1px solid var(--guide-line); }
.account-guide :deep(.guide-preparation dt) { font-weight: 650; }
.account-guide :deep(.guide-preparation dd) { color: var(--guide-muted); }
.account-guide :deep(.guide-hint) { color: var(--guide-muted); font-size: .8125rem; line-height: 1.85; }
.account-guide :deep(kbd), .account-guide :deep(code) { padding: .1rem .3rem; border: 1px solid var(--guide-line); border-radius: 4px; background: var(--guide-soft); font-size: .8em; }
.account-guide :deep(.guide-callout) { margin: 1.25rem 0; padding: 1rem 1.25rem; border-radius: 12px; background: var(--guide-tint); color: var(--guide-accent); }
.account-guide :deep(.guide-callout > strong) { display: block; }
.account-guide :deep(.guide-callout p) { margin: .3rem 0 0; font-size: .875rem; line-height: 1.8; }
.account-guide :deep(figure) { margin: 1.4rem 0 1.6rem; }
.account-guide :deep(figure > a) { display: block; cursor: zoom-in; }
.account-guide :deep(img) { display: block; width: 100%; height: auto; border: 1px solid var(--guide-line); border-radius: 10px; }
.account-guide :deep(figcaption) { margin-top: .65rem; color: var(--guide-muted); font-size: .75rem; line-height: 1.7; text-align: center; }
.account-guide :deep(.guide-table-wrap) { max-width: 100%; overflow-x: auto; margin: 1rem 0; }
.account-guide :deep(table) { width: 100%; border-collapse: collapse; font-size: .875rem; line-height: 1.8; text-align: left; }
.account-guide :deep(th) { padding: .8rem 1rem; background: var(--guide-soft); font-weight: 650; }
.account-guide :deep(td) { padding: .8rem 1rem; border-bottom: 1px solid var(--guide-line); vertical-align: top; }
.account-guide :deep(th:first-child), .account-guide :deep(td:first-child) { width: 38%; }
.account-guide :deep(.guide-methods), .account-guide :deep(.guide-faq) { margin: 1rem 0; border-top: 1px solid var(--guide-line); }
.account-guide :deep(details) { border-bottom: 1px solid var(--guide-line); }
.account-guide :deep(summary) { padding: 1rem 0; font-size: .9375rem; font-weight: 600; cursor: pointer; }
.account-guide :deep(summary::marker) { color: var(--guide-accent); }
.account-guide :deep(.guide-details-body) { padding: 0 0 1rem; }
.account-guide :deep(.guide-details-body > p:first-child) { margin-top: 0; }
.account-guide :deep(.guide-faq .guide-details-body) { color: var(--guide-muted); }
.account-guide :deep(.guide-checklist) { padding: 0; list-style: none; }
.account-guide :deep(.guide-checklist li) { position: relative; padding-left: 1.8rem; }
.account-guide :deep(.guide-checklist li::before) { content: ''; position: absolute; top: .48rem; left: .1rem; width: .75rem; height: .45rem; border-left: 2px solid var(--guide-accent); border-bottom: 2px solid var(--guide-accent); transform: rotate(-45deg); }
.account-guide :deep(.guide-warranty) { font-size: .875rem; color: var(--guide-muted); }
.account-guide :deep(.guide-warranty h2), .account-guide :deep(.guide-warranty h3) { color: var(--guide-ink); }
@media (max-width: 1023px) {
  .guide-layout { grid-template-columns: minmax(0, 1fr); gap: 1.5rem; }
  .guide-sidebar { position: static; }
  .guide-desktop-nav { display: none; }
  .guide-mobile-nav { display: block; padding: .8rem 1rem; border: 1px solid var(--guide-line); border-radius: 12px; background: var(--guide-paper); font-size: .875rem; }
  .guide-mobile-nav summary { cursor: pointer; color: var(--guide-accent); font-weight: 600; }
  .guide-mobile-nav nav { margin-top: .75rem; }
  .guide-mobile-nav a { display: block; padding: .6rem 0; color: var(--guide-muted); }
  .guide-header { padding-bottom: 1.75rem; }
  .account-guide { padding: 1.75rem; }
}
@media (max-width: 639px) {
  .guide-header h1 { font-size: 1.65rem; }
  .guide-header p { font-size: .875rem; }
  .account-guide { padding: 1.25rem; border-radius: 12px; font-size: .9375rem; line-height: 1.85; }
  .account-guide :deep(.guide-route) { grid-template-columns: minmax(0, 1fr); gap: 1rem; }
  .account-guide :deep(.guide-route a) { display: grid; grid-template-columns: 1.75rem minmax(0, 1fr); column-gap: .8rem; }
  .account-guide :deep(.guide-route span) { grid-row: span 2; }
  .account-guide :deep(.guide-route strong) { margin-top: 0; }
  .account-guide :deep(.guide-route small) { margin-top: .15rem; }
  .account-guide :deep(.guide-section) { padding-top: 2rem; margin-top: 2rem; }
  .account-guide :deep(h2) { font-size: 1.2rem; }
  .account-guide :deep(.guide-preparation > div) { grid-template-columns: minmax(0, 1fr); gap: .3rem; }
  .account-guide :deep(.guide-callout) { padding: .9rem 1rem; }
  .account-guide :deep(th), .account-guide :deep(td) { padding: .65rem .5rem; }
}
</style>
