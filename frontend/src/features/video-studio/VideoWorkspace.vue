<template>
  <div class="video-studio vs-workspace">
    <VideoComposer ref="composer" :source="source" :user-id="userId" :mobile="mobile" :held-id="heldId"
      :failed="heldStatus === 'failed'" :unconfirmed="!!heldId && outstanding.includes(heldId)" :example="example" :reserve="reserve"
      :canvas-draft="canvasDraft" :can-return-canvas="canReturnCanvas"
      @accepted="accept" @rejected="release" @new-draft="newDraft" @lookup="lookup"
      @clear-canvas-draft="clearCanvasDraft" @retry-canvas-draft="syncCanvasDraft" @return-canvas="returnToCanvas" @canvas-conflict="handleCanvasConflict" />
    <VideoGallery ref="gallery" :example="example" :mobile="mobile" :outstanding="outstanding" :canvas-enabled="canvasEnabled"
      @resolved="settle" @example="useExample" @reuse="reuseJob" @canvas="openAddToCanvas" />
    <div v-if="mobile" class="vs-mobile-create">
      <span>{{ heldId ? '原任务已保留，展开后可查询状态' : '描述画面主体、运镜和节奏…' }}</span>
      <button ref="launcher" type="button" class="vs-primary" aria-haspopup="dialog" aria-controls="vs-composer"
        @click="composer?.open(launcher)">{{ heldId ? '查看提交' : '展开 ↑' }}</button>
    </div>
    <AddToCanvasDialog
      v-if="canvasEnabled && canvasDialogOpen"
      :open="canvasDialogOpen"
      :payload="canvasPayload"
      @close="closeCanvasDialog"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMediaQuery } from '@vueuse/core'
import AddToCanvasDialog from '@/features/creative-canvas/AddToCanvasDialog.vue'
import { isCanvasUuid } from '@/features/creative-canvas/document'
import { clearCanvasDraftHandoff } from '@/features/creative-canvas/handoff'
import type { AddToCanvasPayload } from '@/features/creative-canvas/types'
import { clearImage, pendingImage } from '@/features/video-studio/source'
import type { VideoJob } from '@/features/video-studio/api'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { parseCanvasDraftQuery, stripCanvasDraftQuery, verifyCanvasDraft, type CanvasDraftState } from './canvasDraft'
import VideoComposer from './VideoComposer.vue'
import VideoGallery from './VideoGallery.vue'
import './studio.css'

const props = defineProps<{ userId: number }>()
const route = useRoute()
const router = useRouter()
const desktop = useMediaQuery('(min-width: 821px)')
const mobile = computed(() => !desktop.value)
const canvasEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.creativeCanvas))
const composer = ref<InstanceType<typeof VideoComposer> | null>(null)
const gallery = ref<InstanceType<typeof VideoGallery> | null>(null)
const launcher = ref<HTMLButtonElement>()
const canvasRoute = computed(() => parseCanvasDraftQuery(route?.query as Record<string, unknown> | undefined))
const incoming = canvasRoute.value.present ? null : pendingImage(props.userId)
const source = shallowRef(incoming?.userId === props.userId ? incoming : null)
const example = '镜头缓慢推近，杯口的蒸汽向窗边飘，杯身的图案保持不变。'

const canvasDraft = shallowRef<CanvasDraftState>({ status: 'none' })
const lastValidCanvas = shallowRef<{ readonly canvasId: string; readonly nodeId: string } | null>(null)
const canReturnCanvas = computed(() => canvasEnabled.value && (canvasDraft.value.status === 'ready' || Boolean(heldId.value && lastValidCanvas.value)))
const canvasDialogOpen = ref(false)
const canvasPayload = shallowRef<AddToCanvasPayload | null>(null)
let verifyController: AbortController | undefined

// A reload between POST and its receipt must offer GET by the same ID, not a fresh paid intent.
// Only IDs live in tab storage; the list and every task status still come from the server.
const key = `sub2:video-studio:intents:${props.userId}`
const heldId = ref('')
const heldStatus = ref<VideoJob['status']>()
const outstanding = ref<string[]>([])
const isId = (value: unknown): value is string => typeof value === 'string' && /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i.test(value)
try {
  const state = JSON.parse(sessionStorage.getItem(key) || '{}')
  heldId.value = isId(state.held) ? state.held : ''
  if (Array.isArray(state.pending)) outstanding.value = state.pending.filter(isId)
} catch { /* Private browsing can make session storage unavailable. */ }
function remember() {
  try { sessionStorage.setItem(key, JSON.stringify({ held: heldId.value, pending: outstanding.value })); return true } catch { return false }
}
// The Image Studio action starts a separate, unpaid draft; any unresolved original stays queryable.
// Never clear heldId on a canvas-origin route reload so an unfinished canvas intent stays query-only.
if (!canvasRoute.value.present && source.value && heldId.value) { heldId.value = ''; remember() }

const modelLabel = (id: string) => ({ 'minimax/h3': 'H3', 'minimax/h3-fast': 'H3 Fast', 'muse-video': 'Muse' })[id] || id

function closeCanvasDialog() {
  canvasDialogOpen.value = false
  canvasPayload.value = null
}

function openAddToCanvas(job: VideoJob) {
  if (!canvasEnabled.value || job.status !== 'completed' || !isCanvasUuid(job.id)) return
  canvasPayload.value = {
    kind: 'video',
    videoId: job.id,
    title: `${modelLabel(job.model)} 视频作品`,
  }
  canvasDialogOpen.value = true
}

async function syncCanvasDraft() {
  verifyController?.abort()
  verifyController = undefined

  const refInfo = canvasRoute.value
  if (!refInfo.present) {
    if (canvasDraft.value.status !== 'none') {
      canvasDraft.value = { status: 'none' }
      if (!heldId.value) lastValidCanvas.value = null
    }
    return
  }

  if (!refInfo.valid) {
    clearImage()
    source.value = null
    canvasDraft.value = {
      status: 'invalid',
      canvasId: refInfo.canvasId,
      nodeId: refInfo.nodeId,
      message: '画布草稿链接参数无效或不完整，未载入画布草稿。',
      retryable: false,
    }
    if (!heldId.value) lastValidCanvas.value = null
    return
  }

  canvasDraft.value = { status: 'verifying', canvasId: refInfo.canvasId, nodeId: refInfo.nodeId }
  source.value = null
  const controller = new AbortController()
  verifyController = controller

  try {
    const result = await verifyCanvasDraft({
      userId: props.userId,
      canvasId: refInfo.canvasId,
      nodeId: refInfo.nodeId,
      canvasEnabled: canvasEnabled.value,
      signal: controller.signal,
      heldId: heldId.value || undefined,
    })
    if (controller.signal.aborted) return
    canvasDraft.value = result.state
    source.value = result.source
    if (result.state.status === 'ready') {
      lastValidCanvas.value = { canvasId: result.state.canvasId, nodeId: result.state.nodeId }
      if (mobile.value && !heldId.value) composer.value?.open(launcher.value)
    } else if (!heldId.value) {
      lastValidCanvas.value = null
    }
  } catch {
    if (controller.signal.aborted) return
    canvasDraft.value = {
      status: 'error',
      canvasId: refInfo.canvasId,
      nodeId: refInfo.nodeId,
      message: '无法验证画布草稿，请重试；未允许以空白草稿提交。',
      retryable: true,
    }
  } finally {
    if (verifyController === controller) verifyController = undefined
  }
}

function clearCanvasDraft() {
  verifyController?.abort()
  verifyController = undefined
  clearCanvasDraftHandoff(props.userId)
  clearImage()
  source.value = null
  canvasDraft.value = { status: 'none' }
  if (!heldId.value) lastValidCanvas.value = null
  if (route && router && canvasRoute.value.present) {
    void router.replace({
      name: 'VideoStudio',
      query: stripCanvasDraftQuery(route.query as Record<string, unknown>) as Record<string, string>,
    })
  }
}

function handleCanvasConflict(message: string) {
  if (canvasDraft.value.status === 'ready') {
    canvasDraft.value = {
      status: 'conflict',
      canvasId: canvasDraft.value.canvasId,
      nodeId: canvasDraft.value.nodeId,
      message,
      retryable: false,
    }
  }
}

function returnToCanvas() {
  if (!canvasEnabled.value || !router) return
  const target = canvasDraft.value.status === 'ready'
    ? { canvasId: canvasDraft.value.canvasId, nodeId: canvasDraft.value.nodeId }
    : lastValidCanvas.value
  if (!target) return
  clearCanvasDraftHandoff(props.userId)
  clearImage()
  void router.push({ name: 'CreativeCanvas', query: { board: target.canvasId, node: target.nodeId } })
}

function reserve(id: string) {
  const previous = outstanding.value
  heldId.value = id; heldStatus.value = undefined
  outstanding.value = [id, ...previous.filter(item => item !== id)]
  if (!remember()) {
    heldId.value = ''; outstanding.value = previous
    throw new Error('无法保存原任务号。请允许此站点使用标签页存储后再试；未提交视频。')
  }
}
function resolve(id: string) {
  outstanding.value = outstanding.value.filter(item => item !== id)
  remember()
}
function release(id: string) {
  if (heldId.value === id) heldId.value = ''
  resolve(id)
}
function settle(job: VideoJob) {
  if (heldId.value === job.id) heldStatus.value = job.status
  if (job.status !== 'uncertain') resolve(job.id)
}
function accept(job: VideoJob) {
  if (canvasDraft.value.status === 'ready') {
    lastValidCanvas.value = { canvasId: canvasDraft.value.canvasId, nodeId: canvasDraft.value.nodeId }
    clearCanvasDraftHandoff(props.userId)
  }
  settle(job)
  gallery.value?.adopt(job)
  composer.value?.close()
}
function newDraft() {
  heldId.value = ''
  lastValidCanvas.value = null
  remember()
}
function lookup(id: string) {
  composer.value?.close()
  gallery.value?.lookup(id)
}
function useExample(opener: HTMLElement) { composer.value?.useExample(opener) }
function reuseJob(job: VideoJob, opener?: HTMLElement) { composer.value?.reuse(job, opener) }

watch(
  [() => canvasRoute.value.present, () => canvasRoute.value.canvasId, () => canvasRoute.value.nodeId, canvasEnabled],
  ([, , , enabled]) => {
    if (!enabled) closeCanvasDialog()
    void syncCanvasDraft()
  },
  { immediate: true },
)
onMounted(() => { if (mobile.value && source.value && !heldId.value) composer.value?.open(launcher.value) })
onBeforeUnmount(() => {
  verifyController?.abort()
  verifyController = undefined
  closeCanvasDialog()
})
</script>
