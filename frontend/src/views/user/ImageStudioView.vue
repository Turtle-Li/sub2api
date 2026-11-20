<template>
  <AppLayout>
    <div ref="host" />
    <AddToCanvasDialog
      v-if="canvasEnabled && canvasDialogOpen"
      :open="canvasDialogOpen"
      :payload="canvasPayload"
      @close="closeCanvasDialog"
    />
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, nextTick, onMounted, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useRouter } from 'vue-router'
import { createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAuthStore } from '@/stores/auth'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { Studio } from '@/features/image-studio/Studio'
import { isSafeStudioOriginal, type StudioOriginal } from '@/features/image-studio/workspaceAPI'
import AddToCanvasDialog from '@/features/creative-canvas/AddToCanvasDialog.vue'
import type { AddToCanvasPayload } from '@/features/creative-canvas/types'
import { clearImage, pendingImage, prepareImage } from '@/features/video-studio/source'

const host = ref<HTMLElement>()
const auth = useAuthStore()
const router = useRouter()
const videoEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.videoStudio))
const canvasEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.creativeCanvas))
const canvasDialogOpen = ref(false)
const canvasPayload = shallowRef<AddToCanvasPayload | null>(null)
let root: Root | undefined

function closeCanvasDialog() {
  canvasDialogOpen.value = false
  canvasPayload.value = null
}

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

async function openCanvas(userId: number, original: StudioOriginal) {
  if (!canvasEnabled.value || auth.user?.id !== userId) throw new Error('当前不能将原图加入创作画布。')
  if (!isSafeStudioOriginal(original)) throw new Error('原图定位无效或已过期，无法加入画布。')
  canvasPayload.value = {
    kind: 'image',
    original: {
      route: original.route,
      batchId: original.batchId,
      itemId: original.itemId,
      index: original.index,
      ...(original.expiresAt !== undefined ? { expiresAt: original.expiresAt } : {}),
    },
    title: `生图作品 · 第 ${original.index + 1} 张`,
  }
  canvasDialogOpen.value = true
  await nextTick()
}

function render() {
  const userId = auth.user?.id
  root?.render(userId ? createElement(Studio, {
    key: userId,
    userId,
    onVideo: videoEnabled.value ? (original: StudioOriginal) => openVideo(userId, original) : undefined,
    onCanvas: canvasEnabled.value ? (original: StudioOriginal) => openCanvas(userId, original) : undefined,
  }) : null)
}
onMounted(() => { if (host.value) { root = createRoot(host.value); render() } })
watch([() => auth.user?.id, videoEnabled, canvasEnabled], ([nextUser, , nextCanvas], [prevUser]) => {
  if (!nextCanvas || !nextUser || nextUser !== prevUser) closeCanvasDialog()
  render()
}, { flush: 'sync' })
onBeforeUnmount(() => { closeCanvasDialog(); root?.unmount() })
</script>
