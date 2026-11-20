<template><AppLayout><div ref="host" /></AppLayout></template>
<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAuthStore } from '@/stores/auth'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { Studio } from '@/features/image-studio/Studio'
import type { StudioOriginal } from '@/features/image-studio/workspaceAPI'
import { clearImage, pendingImage, prepareImage } from '@/features/video-studio/source'

const host = ref<HTMLElement>()
const auth = useAuthStore()
const router = useRouter()
const videoEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.videoStudio))
let root: Root | undefined

async function openVideo(userId: number, original: StudioOriginal) {
  if (!videoEnabled.value || auth.user?.id !== userId) throw new Error('当前不能带入原图。')
  const source = prepareImage(userId, original)
  try {
    await router.push({ name: 'VideoStudio' })
    if (router.currentRoute.value.name !== 'VideoStudio') throw new Error('未能打开视频工作台。')
  } catch (error) {
    if (pendingImage(userId) === source) clearImage()
    throw error
  }
}
function render() {
  const userId = auth.user?.id
  root?.render(userId ? createElement(Studio, { key: userId, userId,
    onVideo: videoEnabled.value ? (original: StudioOriginal) => openVideo(userId, original) : undefined,
  }) : null)
}
onMounted(() => { if (host.value) { root = createRoot(host.value); render() } })
watch([() => auth.user?.id, videoEnabled], render, { flush: 'sync' })
onBeforeUnmount(() => root?.unmount())
</script>
