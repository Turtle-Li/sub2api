<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import ShopGuideContent from '@/components/shop/ShopGuideContent.vue'

const route = useRoute()
const daypass = computed(() => route.path === '/shop/help/deepseek-daypass')
const mobileNav = ref<HTMLDetailsElement | null>(null)
const returnLink = computed(() => {
  for (const [key, path, label] of [['order', 'orders', '返回订单'], ['product', 'products', '返回商品']] as const) {
    const value = route.query?.[key]
    if (typeof value === 'string' && /^\d+$/.test(value) && Number.isSafeInteger(Number(value)) && Number(value) > 0) {
      return { to: `/shop/${path}/${Number(value)}`, label }
    }
  }
  return { to: '/shop', label: '返回商城' }
})
async function goToSection(id: string) {
  const mobileWasOpen = mobileNav.value?.open
  if (mobileNav.value) mobileNav.value.open = false
  await nextTick()
  const target = document.getElementById(id)
  target?.scrollIntoView({ block: 'start', behavior: 'instant' })
  if (mobileWasOpen) mobileNav.value?.querySelector('summary')?.focus({ preventScroll: true })
  if (id === 'guide-content') target?.focus({ preventScroll: true })
}
const sections = computed(() => daypass.value ? [
  { id: 'receive', title: '领取 API Key' },
  { id: 'configure', title: '配置客户端' },
  { id: 'limits', title: '并发与 IP 限制' },
  { id: 'expiry', title: '有效期与售后' }
] : [
  { id: 'prepare', title: '开始前准备' },
  { id: 'email-login', title: '登录微软邮箱' },
  { id: 'chatgpt-login', title: '登录 ChatGPT' },
  { id: 'account-security', title: '账号安全设置' },
  { id: 'checklist', title: '完成检查' },
  { id: 'faq', title: '常见问题' },
  { id: 'warranty', title: '售后与质保' }
])
</script>
<template>
  <AppLayout>
    <div class='guide-page'>
      <a class='guide-skip' href='#guide-content' @click.prevent='goToSection("guide-content")'>跳到指南正文</a>

      <header class='guide-header'>
        <RouterLink :to='returnLink.to' class='guide-back'>
          <svg aria-hidden='true' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='1.75'><path d='m14 6-6 6 6 6' /></svg>
          {{ returnLink.label }}
        </RouterLink>
        <h1>{{ daypass ? 'DeepSeek API 使用指南' : 'ChatGPT Plus 成品号使用指南' }}</h1>
      </header>

      <div class='guide-layout'>
        <aside class='guide-sidebar'>
          <nav class='guide-desktop-nav' aria-label='指南章节'>
            <p class='guide-nav-title'>章节目录</p>
            <ol class='guide-nav-list'>
              <li v-for='(section, index) in sections' :key='section.id'>
                <a :href='`#${section.id}`' @click.prevent='goToSection(section.id)'>
                  <span class='guide-nav-number' aria-hidden='true'>{{ index + 1 }}</span>
                  <span>{{ section.title }}</span>
                </a>
              </li>
            </ol>
          </nav>
        </aside>

        <div class='guide-reading-column'>
          <details ref='mobileNav' class='guide-mobile-nav'>
            <summary>
              <span>本章导航</span>
              <span class='guide-mobile-nav-hint'>选择章节</span>
            </summary>
            <nav aria-label='手机指南章节'>
              <ol class='guide-nav-list'>
                <li v-for='(section, index) in sections' :key='section.id'>
                  <a :href='`#${section.id}`' @click.prevent='goToSection(section.id)'>
                    <span class='guide-nav-number' aria-hidden='true'>{{ index + 1 }}</span>
                    <span>{{ section.title }}</span>
                  </a>
                </li>
              </ol>
            </nav>
          </details>

          <ShopGuideContent id='guide-content' class='guide-article' tabindex='-1' :guide-key="daypass ? 'deepseek-daypass' : 'chatgpt-plus-account'" />
        </div>
      </div>
    </div>
  </AppLayout>
</template>
<style scoped>
.guide-page {
  --guide-surface: theme('colors.white');
  --guide-subtle: theme('colors.gray.50');
  --guide-heading: theme('colors.gray.900');
  --guide-text: theme('colors.gray.800');
  --guide-muted: theme('colors.gray.600');
  --guide-border: theme('colors.gray.200');
  --guide-border-strong: theme('colors.gray.300');
  --guide-primary: theme('colors.primary.600');
  --guide-primary-strong: theme('colors.primary.700');
  --guide-primary-soft: theme('colors.primary.50');
  --guide-focus: theme('colors.primary.500');
  width: 100%;
  max-width: 1440px;
  margin: 0 auto;
  padding: 0 0 48px;
  color: var(--guide-text);
}

.dark .guide-page {
  --guide-surface: theme('colors.dark.900');
  --guide-subtle: theme('colors.dark.800');
  --guide-heading: theme('colors.white');
  --guide-text: theme('colors.gray.200');
  --guide-muted: theme('colors.gray.400');
  --guide-border: theme('colors.dark.700');
  --guide-border-strong: theme('colors.dark.600');
  --guide-primary: theme('colors.primary.400');
  --guide-primary-strong: theme('colors.primary.300');
  --guide-primary-soft: theme('colors.dark.800');
  --guide-focus: theme('colors.primary.400');
}

.guide-skip {
  position: absolute;
  top: 8px;
  left: 16px;
  z-index: 20;
  display: inline-flex;
  align-items: center;
  min-height: 44px;
  padding: 0 16px;
  border-radius: 8px;
  background: var(--guide-surface);
  color: var(--guide-primary-strong);
  font-size: 14px;
  font-weight: 600;
  box-shadow: 0 4px 16px rgb(0 0 0 / 0.14);
  transform: translateY(-160%);
}

.guide-skip:focus {
  transform: translateY(0);
}

.guide-header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 24px;
  margin: 0 0 20px;
  padding: 0 0 16px;
  border-bottom: 1px solid var(--guide-border);
}

.guide-back {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 6px;
  min-height: 44px;
  color: var(--guide-muted);
  font-size: 14px;
}

.guide-back:hover {
  color: var(--guide-primary-strong);
}

.guide-back svg {
  width: 18px;
  height: 18px;
}

.guide-header h1 {
  margin: 0;
  color: var(--guide-heading);
  font-size: 24px;
  font-weight: 650;
  line-height: 1.35;
  letter-spacing: -0.02em;
}

.guide-layout {
  display: grid;
  grid-template-columns: 190px minmax(0, 1fr);
  gap: 24px;
  align-items: start;
}

.guide-sidebar,
.guide-reading-column {
  min-width: 0;
}

.guide-sidebar {
  position: sticky;
  top: 80px;
  max-height: calc(100dvh - 96px);
  overflow-y: auto;
}

.guide-nav-title {
  margin: 0 0 12px;
  color: var(--guide-heading);
  font-size: 14px;
  font-weight: 600;
}

.guide-nav-list {
  margin: 0;
  padding: 0;
  list-style: none;
  border-top: 1px solid var(--guide-border);
}

.guide-nav-list li {
  border-bottom: 1px solid var(--guide-border);
}

.guide-nav-list a {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 48px;
  padding: 8px 6px;
  border-radius: 6px;
  color: var(--guide-muted);
  font-size: 14px;
  line-height: 1.45;
  text-decoration: none;
  transition: color 150ms ease, background-color 150ms ease;
}

.guide-nav-list a:hover {
  background: var(--guide-subtle);
  color: var(--guide-heading);
}

.guide-nav-number {
  display: inline-flex;
  flex: 0 0 24px;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: 1px solid var(--guide-border);
  border-radius: 50%;
  color: var(--guide-primary);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.guide-mobile-nav {
  display: none;
}

.guide-article {
  min-width: 0;
  padding: 24px 28px 32px;
  border: 1px solid var(--guide-border);
  border-radius: 12px;
  background: var(--guide-surface);
  color: var(--guide-text);
  font-size: 16px;
  line-height: 1.85;
  overflow-wrap: break-word;
}

.guide-article:focus {
  outline: none;
}

.guide-article :deep(p),
.guide-article :deep(ul),
.guide-article :deep(ol),
.guide-article :deep(dl),
.guide-article :deep(blockquote) {
  margin-top: 0;
  margin-bottom: 18px;
}

.guide-article :deep(ul),
.guide-article :deep(ol) {
  padding-left: 1.5em;
}

.guide-article :deep(li + li) {
  margin-top: 7px;
}

.guide-article :deep(a) {
  color: var(--guide-primary-strong);
  text-decoration: underline;
  text-decoration-thickness: 1px;
  text-underline-offset: 3px;
}

.guide-article :deep(a:hover) {
  text-decoration-thickness: 2px;
}

.guide-article :deep(a:focus-visible) {
  outline: 2px solid var(--guide-focus);
  outline-offset: 3px;
  border-radius: 3px;
}

.guide-article :deep(strong) {
  color: var(--guide-heading);
  font-weight: 600;
}

.guide-article :deep(.guide-overview) {
  padding-bottom: 34px;
  border-bottom: 1px solid var(--guide-border);
}

.guide-article :deep(.guide-overview > p) {
  margin-bottom: 24px;
  color: var(--guide-muted);
  font-size: 16px;
  line-height: 1.8;
}

.guide-article :deep(.guide-route) {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  margin: 0;
  padding: 0;
  overflow: hidden;
  border: 1px solid var(--guide-border);
  border-radius: 10px;
  background: var(--guide-subtle);
  list-style: none;
}

.guide-article :deep(.guide-route li) {
  min-width: 0;
  margin: 0;
}

.guide-article :deep(.guide-route li + li) {
  margin: 0;
  border-left: 1px solid var(--guide-border);
}

.guide-article :deep(.guide-route a) {
  display: grid;
  grid-template-columns: 26px minmax(0, 1fr);
  column-gap: 10px;
  align-content: center;
  min-height: 112px;
  padding: 18px 16px;
  color: var(--guide-text);
  text-decoration: none;
}

.guide-article :deep(.guide-route a:hover) {
  background: var(--guide-primary-soft);
  text-decoration: none;
}

.guide-article :deep(.guide-route a:focus-visible) {
  outline-offset: -3px;
}

.guide-article :deep(.guide-route a > span) {
  grid-row: 1 / span 2;
  padding-top: 2px;
  color: var(--guide-primary);
  font-size: 13px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.guide-article :deep(.guide-route strong) {
  grid-column: 2;
  font-size: 15px;
  line-height: 1.5;
}

.guide-article :deep(.guide-route small) {
  grid-column: 2;
  margin-top: 5px;
  color: var(--guide-muted);
  font-size: 12px;
  line-height: 1.55;
}

.guide-article :deep(.guide-section) {
  min-width: 0;
  padding: 42px 0;
  border-bottom: 1px solid var(--guide-border);
  scroll-margin-top: 88px;
}

.guide-article :deep(.guide-section:last-child) {
  padding-bottom: 0;
  border-bottom: 0;
}

.guide-article :deep(.guide-section:first-child) {
  padding-top: 0;
}

.guide-article :deep(.guide-section h2) {
  display: flex;
  align-items: baseline;
  gap: 12px;
  margin: 0 0 24px;
  color: var(--guide-heading);
  font-size: 24px;
  font-weight: 650;
  line-height: 1.4;
  letter-spacing: -0.015em;
}

.guide-article :deep(.guide-section-number) {
  flex: none;
  color: var(--guide-primary);
  font-size: 13px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  letter-spacing: 0.04em;
}

.guide-article :deep(h3) {
  margin: 30px 0 12px;
  color: var(--guide-heading);
  font-size: 19px;
  font-weight: 600;
  line-height: 1.5;
}

.guide-article :deep(h4) {
  margin: 24px 0 10px;
  color: var(--guide-heading);
  font-size: 16px;
  font-weight: 600;
  line-height: 1.55;
}

.guide-article :deep(.guide-step) {
  margin: 24px 0;
  padding: 3px 0 3px 20px;
  border-left: 2px solid var(--guide-border-strong);
}

.guide-article :deep(.guide-step > :first-child) {
  margin-top: 0;
}

.guide-article :deep(.guide-step > :last-child) {
  margin-bottom: 0;
}

.guide-article :deep(.guide-preparation) {
  margin: 22px 0;
  border-bottom: 1px solid var(--guide-border);
}

.guide-article :deep(.guide-preparation > div) {
  display: grid;
  grid-template-columns: 108px minmax(0, 1fr);
  gap: 16px;
}

.guide-article :deep(.guide-preparation dt),
.guide-article :deep(.guide-preparation dd) {
  min-width: 0;
  margin: 0;
  padding: 13px 0;
  border-top: 1px solid var(--guide-border);
}

.guide-article :deep(.guide-preparation dt) {
  padding-right: 18px;
  color: var(--guide-heading);
  font-size: 14px;
  font-weight: 600;
}

.guide-article :deep(.guide-preparation dd) {
  color: var(--guide-text);
}

.guide-article :deep(.guide-hint) {
  margin: 18px 0;
  padding: 12px 16px;
  border-left: 3px solid var(--guide-primary);
  background: var(--guide-subtle);
  color: var(--guide-muted);
  font-size: 14px;
  line-height: 1.75;
}

.guide-article :deep(.guide-callout) {
  margin: 22px 0;
  padding: 16px 18px;
  border: 1px solid var(--guide-border);
  border-radius: 8px;
  background: var(--guide-primary-soft);
}

.guide-article :deep(.guide-callout > :last-child),
.guide-article :deep(.guide-hint > :last-child) {
  margin-bottom: 0;
}

.guide-article :deep(code),
.guide-article :deep(kbd) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.88em;
}

.guide-article :deep(code) {
  padding: 2px 5px;
  border: 1px solid var(--guide-border);
  border-radius: 4px;
  background: var(--guide-subtle);
  color: var(--guide-heading);
  overflow-wrap: anywhere;
}

.guide-article :deep(kbd) {
  display: inline-block;
  min-width: 1.8em;
  padding: 1px 6px;
  border: 1px solid var(--guide-border-strong);
  border-bottom-width: 2px;
  border-radius: 5px;
  background: var(--guide-surface);
  color: var(--guide-heading);
  line-height: 1.6;
  text-align: center;
  white-space: nowrap;
}

.guide-article :deep(pre) {
  max-width: 100%;
  margin: 18px 0;
  padding: 16px;
  overflow-x: auto;
  border: 1px solid var(--guide-border);
  border-radius: 8px;
  background: var(--guide-subtle);
  overscroll-behavior-inline: contain;
  -webkit-overflow-scrolling: touch;
}

.guide-article :deep(pre code) {
  padding: 0;
  border: 0;
  background: transparent;
  overflow-wrap: normal;
}

.guide-article :deep(figure) {
  margin: 28px 0;
}

.guide-article :deep(figure > a) {
  display: block;
  max-width: 100%;
  border-radius: 8px;
  text-align: center;
  text-decoration: none;
  cursor: zoom-in;
}

.guide-article :deep(figure img) {
  display: block;
  width: auto;
  max-width: 100%;
  height: auto;
  margin: 0 auto;
  border: 1px solid var(--guide-border);
  border-radius: 8px;
  background: #fff;
  opacity: 1;
  filter: none;
  mix-blend-mode: normal;
  image-rendering: auto;
}

.guide-article :deep(figcaption) {
  margin-top: 10px;
  color: var(--guide-muted);
  font-size: 13px;
  line-height: 1.65;
}

.guide-article :deep(.guide-table-wrap) {
  max-width: 100%;
  margin: 22px 0;
  overflow-x: auto;
  border: 1px solid var(--guide-border);
  border-radius: 8px;
  overscroll-behavior-inline: contain;
  -webkit-overflow-scrolling: touch;
}

.guide-article :deep(.guide-table-wrap table) {
  width: 100%;
  min-width: 540px;
  margin: 0;
  border-collapse: collapse;
  background: var(--guide-surface);
  font-size: 14px;
  line-height: 1.65;
  text-align: left;
}

.guide-article :deep(.guide-table-wrap th),
.guide-article :deep(.guide-table-wrap td) {
  padding: 12px 14px;
  border-bottom: 1px solid var(--guide-border);
  vertical-align: top;
}

.guide-article :deep(.guide-table-wrap th) {
  background: var(--guide-subtle);
  color: var(--guide-heading);
  font-weight: 600;
}

.guide-article :deep(.guide-table-wrap tr:last-child td) {
  border-bottom: 0;
}

.guide-article :deep(.guide-methods),
.guide-article :deep(.guide-faq) {
  margin: 22px 0;
  border-bottom: 1px solid var(--guide-border);
}

.guide-article :deep(.guide-methods details),
.guide-article :deep(.guide-faq details) {
  border-top: 1px solid var(--guide-border);
}

.guide-article :deep(.guide-methods summary),
.guide-article :deep(.guide-faq summary) {
  position: relative;
  display: flex;
  align-items: center;
  min-height: 52px;
  padding: 13px 38px 13px 0;
  color: var(--guide-heading);
  font-size: 15px;
  font-weight: 600;
  line-height: 1.55;
  cursor: pointer;
  list-style: none;
}

.guide-article :deep(.guide-methods summary::-webkit-details-marker),
.guide-article :deep(.guide-faq summary::-webkit-details-marker) {
  display: none;
}

.guide-article :deep(.guide-methods summary::after),
.guide-article :deep(.guide-faq summary::after) {
  content: '';
  position: absolute;
  right: 10px;
  top: 50%;
  width: 7px;
  height: 7px;
  border-right: 1.5px solid var(--guide-muted);
  border-bottom: 1.5px solid var(--guide-muted);
  transform: translateY(-70%) rotate(45deg);
}

.guide-article :deep(.guide-methods details[open] summary::after),
.guide-article :deep(.guide-faq details[open] summary::after) {
  transform: translateY(-20%) rotate(225deg);
}

.guide-article :deep(.guide-methods summary:focus-visible),
.guide-article :deep(.guide-faq summary:focus-visible) {
  outline: 2px solid var(--guide-focus);
  outline-offset: 3px;
  border-radius: 4px;
}

.guide-article :deep(.guide-details-body) {
  padding: 0 0 20px;
  color: var(--guide-text);
  font-size: 15px;
}

.guide-article :deep(.guide-details-body > :last-child) {
  margin-bottom: 0;
}

.guide-article :deep(.guide-checklist) {
  margin: 20px 0;
  padding: 0;
  list-style: none;
}

.guide-article :deep(.guide-checklist li) {
  position: relative;
  padding-left: 30px;
}

.guide-article :deep(.guide-checklist li::before) {
  content: '';
  position: absolute;
  top: 0.55em;
  left: 2px;
  width: 14px;
  height: 14px;
  border: 1.5px solid var(--guide-border-strong);
  border-radius: 3px;
  background: var(--guide-surface);
}

.guide-article :deep(.guide-warranty) {
  margin-top: 22px;
  padding: 18px 20px;
  border: 1px solid var(--guide-border);
  border-radius: 8px;
  background: var(--guide-subtle);
  font-size: 14px;
}

.guide-article :deep(.guide-warranty > :last-child) {
  margin-bottom: 0;
}

.guide-nav-list a:focus-visible,
.guide-back:focus-visible,
.guide-mobile-nav > summary:focus-visible,
.guide-skip:focus-visible {
  outline: 2px solid var(--guide-focus);
  outline-offset: 3px;
}

@media (max-width: 900px) {
  .guide-page {
    max-width: none;
  }

  .guide-layout {
    display: block;
  }

  .guide-sidebar {
    display: none;
  }

  .guide-reading-column {
    width: 100%;
  }

  .guide-article :deep(.guide-section) {
    scroll-margin-top: 148px;
  }

  .guide-mobile-nav {
    position: sticky;
    top: 72px;
    z-index: 20;
    display: block;
    margin-bottom: 16px;
    border: 1px solid var(--guide-border);
    border-radius: 10px;
    background: var(--guide-surface);
  }

  .guide-mobile-nav > summary {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    min-height: 52px;
    padding: 12px 42px 12px 16px;
    color: var(--guide-heading);
    font-size: 14px;
    font-weight: 600;
    cursor: pointer;
    list-style: none;
  }

  .guide-mobile-nav > summary::-webkit-details-marker {
    display: none;
  }

  .guide-mobile-nav > summary::after {
    content: '';
    position: absolute;
    right: 18px;
    top: 50%;
    width: 7px;
    height: 7px;
    border-right: 1.5px solid var(--guide-muted);
    border-bottom: 1.5px solid var(--guide-muted);
    transform: translateY(-70%) rotate(45deg);
  }

  .guide-mobile-nav[open] > summary {
    border-bottom: 1px solid var(--guide-border);
  }

  .guide-mobile-nav[open] > summary::after {
    transform: translateY(-20%) rotate(225deg);
  }

  .guide-mobile-nav-hint {
    color: var(--guide-muted);
    font-size: 12px;
    font-weight: 400;
  }

  .guide-mobile-nav nav {
    max-height: min(60dvh, 400px);
    overflow-y: auto;
    padding: 0 12px 12px;
  }

  .guide-mobile-nav .guide-nav-list {
    border-top: 0;
  }

  .guide-mobile-nav .guide-nav-list a {
    min-height: 48px;
    padding-right: 10px;
    padding-left: 10px;
  }
}

@media (max-width: 640px) {
  .guide-page {
    padding-bottom: 32px;
  }

  .guide-header {
    gap: 4px 16px;
    margin-bottom: 16px;
    padding-bottom: 12px;
  }

  .guide-header h1 {
    font-size: 20px;
    line-height: 1.4;
  }

  .guide-article {
    padding: 20px 16px 24px;
    border-radius: 10px;
    font-size: 16px;
    line-height: 1.8;
  }

  .guide-article :deep(.guide-overview) {
    padding-bottom: 26px;
  }

  .guide-article :deep(.guide-overview > p) {
    font-size: 15px;
  }

  .guide-article :deep(.guide-route) {
    grid-template-columns: 1fr;
  }

  .guide-article :deep(.guide-route li + li) {
    border-top: 1px solid var(--guide-border);
    border-left: 0;
  }

  .guide-article :deep(.guide-route a) {
    min-height: 82px;
    padding: 15px 16px;
  }

  .guide-article :deep(.guide-section) {
    padding: 34px 0;
    scroll-margin-top: 148px;
  }

  .guide-article :deep(.guide-section h2) {
    align-items: flex-start;
    gap: 9px;
    margin-bottom: 20px;
    font-size: 22px;
  }

  .guide-article :deep(.guide-section-number) {
    padding-top: 4px;
    font-size: 12px;
  }

  .guide-article :deep(h3) {
    margin-top: 26px;
    font-size: 18px;
  }

  .guide-article :deep(.guide-step) {
    padding-left: 15px;
  }

  .guide-article :deep(.guide-preparation > div) {
    display: block;
  }

  .guide-article :deep(.guide-preparation dt) {
    padding: 12px 0 3px;
    border-top: 1px solid var(--guide-border);
  }

  .guide-article :deep(.guide-preparation dd) {
    padding: 0 0 13px;
    border-top: 0;
  }

  .guide-article :deep(figure) {
    margin: 24px 0;
  }

  .guide-article :deep(.guide-callout),
  .guide-article :deep(.guide-warranty) {
    padding: 15px 16px;
  }

  .guide-article :deep(.guide-methods summary),
  .guide-article :deep(.guide-faq summary) {
    min-height: 52px;
    padding-right: 34px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .guide-nav-list a {
    transition: none;
  }
}
</style>
