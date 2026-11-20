<template>
  <AppLayout>
    <VideoWorkspace v-if="auth.isAuthenticated && auth.user && !sessionChanged" :key="auth.user.id" :user-id="auth.user.id" />
    <p v-else class="text-sm text-gray-600 dark:text-gray-300" role="status">{{ sessionChanged ? '登录账号已变化，请刷新后继续。' : '请登录后查看你的视频工作台。' }}</p>
  </AppLayout>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { getStoredUserID } from '@/api/tokenRefresh'
import AppLayout from '@/components/layout/AppLayout.vue'
import { clearCanvasDraftHandoff } from '@/features/creative-canvas/handoff'
import { clearImage } from '@/features/video-studio/source'
import VideoWorkspace from '@/features/video-studio/VideoWorkspace.vue'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const sessionChanged = ref(false)
watch(() => auth.user?.id, (nextId, prevId) => {
  if (prevId !== undefined && nextId !== prevId) {
    clearCanvasDraftHandoff(prevId)
    clearImage()
  }
  sessionChanged.value = false
}, { flush: 'sync' })
function onStorage(e: StorageEvent) {
  if (e.key === null || (e.key === 'auth_token' && !e.newValue) ||
    (e.key === 'auth_user' && getStoredUserID(e.newValue) !== auth.user?.id)) {
    clearCanvasDraftHandoff(auth.user?.id)
    clearImage()
    sessionChanged.value = true
  }
}
onMounted(() => window.addEventListener('storage', onStorage))
onBeforeUnmount(() => window.removeEventListener('storage', onStorage))
</script>
