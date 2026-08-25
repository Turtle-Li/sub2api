<template>
  <header class="hairline-b sticky top-0 z-30" :style="{ background: 'var(--bg)' }">
    <nav class="mx-auto flex h-[60px] max-w-[1180px] items-center gap-6 px-5 sm:px-8">
      <router-link to="/home" class="flex min-w-0 shrink-0 items-center gap-2.5">
        <img
          :src="siteLogo || '/turtleroute-mark.png'"
          alt=""
          class="h-7 w-7 shrink-0 object-contain"
        />
        <span class="truncate text-[15px] font-semibold tracking-tight">{{ siteName }}</span>
      </router-link>

      <!-- 桌面主导航 -->
      <div class="hidden flex-1 items-center gap-7 lg:flex">
        <router-link
          v-for="item in navItems"
          :key="item.key"
          :to="item.to"
          class="navlink"
          :class="{ 'is-current': isCurrent(item.to) }"
        >
          {{ item.label }}
        </router-link>
      </div>

      <div class="ml-auto flex shrink-0 items-center gap-1">
        <LocaleSwitcher />
        <button
          class="icon-btn"
          :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          @click="toggleTheme"
        >
          <Icon v-if="isDark" name="sun" size="md" />
          <Icon v-else name="moon" size="md" />
        </button>
        <router-link :to="primaryDestination" class="btn-solid ml-2">
          {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
        </router-link>
        <!-- 多页站点在窄屏必须有导航入口，否则除首页外无处可去。
             用 .nav-toggle 而不是 `icon-btn lg:hidden`：site.css 里的规则
             带 .tr 前缀，优先级高于 Tailwind 的 .hidden。 -->
        <button
          class="nav-toggle"
          :aria-expanded="menuOpen"
          :aria-label="menuOpen ? t('site.nav.closeMenu') : t('site.nav.openMenu')"
          @click="menuOpen = !menuOpen"
        >
          <Icon :name="menuOpen ? 'x' : 'menu'" size="md" />
        </button>
      </div>
    </nav>
  </header>

  <!-- 移动端菜单：全屏索引清单，沿用编辑体，不做圆角抽屉 -->
  <div
    v-if="menuOpen"
    class="fixed inset-x-0 bottom-0 top-[60px] z-40 overflow-y-auto lg:hidden"
    :style="{ background: 'var(--bg)' }"
  >
    <nav class="px-5 pb-16 pt-2 sm:px-8">
      <ul class="hairline-t">
        <li v-for="(item, i) in navItems" :key="item.key" class="hairline-b">
          <router-link :to="item.to" class="indexrow">
            <span class="meta shrink-0 tabular-nums">{{ String(i + 1).padStart(2, '0') }}</span>
            <span class="indexrow-label" :class="{ 'text-[color:var(--accent)]': isCurrent(item.to) }">
              {{ item.label }}
            </span>
            <span class="indexrow-desc">{{ t(`site.nav.desc.${item.key}`) }}</span>
            <span class="arrow shrink-0" aria-hidden="true">→</span>
          </router-link>
        </li>
      </ul>

      <p class="meta mt-10">{{ t('site.nav.moreLabel') }}</p>
      <ul class="hairline-t mt-3">
        <li v-for="entry in secondaryEntries" :key="entry.key" class="hairline-b">
          <component
            :is="entry.to ? 'router-link' : 'a'"
            :to="entry.to"
            :href="entry.href"
            :target="entry.href ? '_blank' : undefined"
            :rel="entry.href ? 'noopener noreferrer' : undefined"
            class="flex items-center justify-between gap-4 py-3.5"
          >
            <span class="text-[14px]">{{ entry.label }}</span>
            <span class="arrow meta shrink-0" aria-hidden="true">→</span>
          </component>
        </li>
      </ul>
    </nav>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import { useSite, GITHUB_URL } from './useSite'

const route = useRoute()
const {
  t,
  siteName,
  siteLogo,
  isAuthenticated,
  primaryDestination,
  statusDestination,
  showModelPlazaEntry,
  navItems,
} = useSite()

// 主题：初始值由 main.ts 在启动时写好，这里只负责切换（与 AppSidebar 一致）
const isDark = ref(document.documentElement.classList.contains('dark'))
function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

const menuOpen = ref(false)

/** /docs 有子路由，所以用前缀匹配而不是全等。 */
function isCurrent(path: string): boolean {
  return route.path === path || route.path.startsWith(`${path}/`)
}

const secondaryEntries = computed(() => {
  const entries: Array<{ key: string; label: string; to?: string; href?: string }> = [
    { key: 'console', label: t('site.nav.desc.console'), to: primaryDestination.value },
    { key: 'status', label: t('site.footer.links.status'), to: statusDestination.value },
  ]
  if (showModelPlazaEntry.value) {
    entries.push({ key: 'plaza', label: t('site.footer.links.plaza'), to: '/model-plaza' })
  }
  entries.push({ key: 'github', label: 'GitHub', href: GITHUB_URL })
  return entries
})

// 导航后关闭菜单，否则跳转完还盖着一层
watch(
  () => route.fullPath,
  () => {
    menuOpen.value = false
  },
)

// 菜单打开时锁住背景滚动
watch(menuOpen, (open) => {
  document.body.style.overflow = open ? 'hidden' : ''
})
onBeforeUnmount(() => {
  document.body.style.overflow = ''
})
</script>
