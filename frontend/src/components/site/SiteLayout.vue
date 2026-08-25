<template>
  <div class="tr flex min-h-screen flex-col">
    <SiteHeader />
    <main class="flex-1">
      <slot />
    </main>
    <SiteFooter />
  </div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { useAuthStore, useAppStore } from '@/stores'
import SiteHeader from './SiteHeader.vue'
import SiteFooter from './SiteFooter.vue'

// 站点视觉系统。按仓库既有约定由需要它的组件导入，不进全局 style.css，
// 这样后台打包时不会带上（参照 AppLayout 导入 onboarding.css）。
import '@/styles/site.css'

const authStore = useAuthStore()
const appStore = useAppStore()

onMounted(() => {
  authStore.checkAuth()
  // 有 __APP_CONFIG__ 注入时命中缓存，不会真的发请求
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
})
</script>
