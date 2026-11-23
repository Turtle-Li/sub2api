<template>
  <div class="video-studio vs-workspace wb-root">
    <nav v-if="canReturnCanvas" class="vs-canvas-return-bar" aria-label="画布上下文导航">
      <div class="vs-canvas-return-copy">
        <span class="vs-canvas-return-dot" :data-state="canvasDraft.status" aria-hidden="true"></span>
        <strong>{{ canvasBarHeading }}</strong>
        <span v-if="canvasBarSubtext" class="vs-canvas-return-sub">{{ canvasBarSubtext }}</span>
      </div>
      <div class="vs-canvas-return-actions">
        <button
          v-if="mobile"
          type="button"
          class="vs-text"
          @click="composer?.open(launcher)"
        >
          {{ heldId ? '查看提交状态' : '打开草稿设置' }}
        </button>
        <button
          type="button"
          class="vs-button vs-return-canvas-btn"
          aria-label="返回画布"
          @click="returnToCanvas"
        >
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 3.5 5.5 8 10 12.5" /></svg>
          返回画布
        </button>
      </div>
    </nav>
    <VideoComposer ref="composer" :source="source" :user-id="userId" :mobile="mobile" :held-id="heldId"
      :failed="heldStatus === 'failed'" :unconfirmed="!!heldId && outstanding.includes(heldId)" :example="example" :reserve="reserve"
      :canvas-draft="canvasDraft" :can-return-canvas="canReturnCanvas"
      @accepted="accept" @rejected="release" @new-draft="newDraft" @next-canvas-take="nextCanvasTake" @lookup="lookup"
      @clear-canvas-draft="clearCanvasDraft" @retry-canvas-draft="syncCanvasDraft" @return-canvas="returnToCanvas" @canvas-conflict="handleCanvasConflict" @models-loaded="syncModelLabels" />
    <VideoGallery ref="gallery" :example="example" :mobile="mobile" :outstanding="outstanding" :canvas-enabled="canvasEnabled" :model-labels="modelLabels"
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
import { clearCanvasDraftHandoff, readStagedCanvasDraftHandoff } from '@/features/creative-canvas/handoff'
import type { AddToCanvasPayload } from '@/features/creative-canvas/types'
import { clearImage, pendingImage } from '@/features/video-studio/source'
import { videoModelDisplayName, type VideoJob, type VideoModel } from '@/features/video-studio/api'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { parseCanvasDraftQuery, stripCanvasDraftQuery, verifyCanvasDraft, type CanvasDraftState, type CanvasRouteRef } from './canvasDraft'
import VideoComposer from './VideoComposer.vue'
import VideoGallery from './VideoGallery.vue'
import './studio.css'
import '@/styles/studio-workbench.css'

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
const returnTarget = computed(() => {
  if (!canvasEnabled.value) return null
  if (canvasRoute.value.valid) {
    return { canvasId: canvasRoute.value.canvasId, nodeId: canvasRoute.value.nodeId }
  }
  if (heldId.value && lastValidCanvas.value) {
    return lastValidCanvas.value
  }
  return null
})
const canReturnCanvas = computed(() => Boolean(returnTarget.value))
const canvasBarHeading = computed(() => {
  const state = canvasDraft.value
  if (state.status === 'ready') {
    return `画布「${state.canvasTitle}」· ${state.draftTitle}`
  }
  if (state.status === 'verifying') return '正在核验画布分镜草稿…'
  if (state.status === 'conflict') return '画布版本已变更 · 未提交'
  if (state.status === 'source-unavailable') return '画布来源素材不可用 · 未提交'
  if (state.status === 'error' || state.status === 'invalid') return '画布草稿暂不可提交'
  if (heldId.value) return '画布分镜任务已锁定原编号'
  return '来自创作画布'
})
const canvasBarSubtext = computed(() => {
  const state = canvasDraft.value
  if (heldId.value) return '返回或刷新不会重复提交'
  if (state.status === 'ready') return state.sourceSummary || `修订号 v${state.revision}`
  if (state.status === 'verifying') return '可随时返回原画布与原节点'
  if (state.status !== 'none') return '可返回原画布查看或调整分镜'
  return ''
})
const canvasDialogOpen = ref(false)
const canvasPayload = shallowRef<AddToCanvasPayload | null>(null)
let verifyController: AbortController | undefined

// A reload between POST and its receipt must offer GET by the same ID, not a fresh paid intent.
// Scope receipts per (canvasId, nodeId) vs standalone so completing branch A never locks branch B,
// while preserving all unresolved `pending` IDs across the tab until explicitly settled.
const key = `sub2:video-studio:intents:${props.userId}`
const scopeKeyFor = (refInfo: CanvasRouteRef) =>
  refInfo.valid ? `canvas:${refInfo.canvasId}:${refInfo.nodeId}` : 'standalone'
const currentScopeKey = computed(() => scopeKeyFor(canvasRoute.value))
const heldByScope = ref<Record<string, string>>({})
const statusById = ref<Record<string, VideoJob['status']>>({})
const outstanding = ref<string[]>([])
const isId = (value: unknown): value is string => typeof value === 'string' && /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i.test(value)
try {
  const state = JSON.parse(sessionStorage.getItem(key) || '{}')
  if (Array.isArray(state.pending)) outstanding.value = state.pending.filter(isId)
  if (state.heldByScope && typeof state.heldByScope === 'object' && !Array.isArray(state.heldByScope)) {
    const restored: Record<string, string> = {}
    for (const [scope, val] of Object.entries(state.heldByScope as Record<string, unknown>)) {
      if (typeof scope === 'string' && isId(val)) restored[scope] = val
    }
    heldByScope.value = restored
  } else if (isId(state.held)) {
    heldByScope.value = { [currentScopeKey.value]: state.held }
  }
} catch { /* Private browsing can make session storage unavailable. */ }
const heldId = computed(() => heldByScope.value[currentScopeKey.value] || '')
const heldStatus = computed(() => (heldId.value ? statusById.value[heldId.value] : undefined))
function setScopedHeld(scope: string, id: string) {
  const next = { ...heldByScope.value }
  if (id) next[scope] = id
  else delete next[scope]
  heldByScope.value = next
}
function remember() {
  try {
    sessionStorage.setItem(key, JSON.stringify({
      held: heldId.value,
      heldByScope: heldByScope.value,
      pending: outstanding.value,
    }))
    return true
  } catch { return false }
}
// The Image Studio action starts a separate, unpaid standalone draft; any unresolved original stays queryable in `outstanding`.
if (!canvasRoute.value.present && source.value && heldByScope.value.standalone) {
  setScopedHeld('standalone', '')
  remember()
}

const modelLabels = shallowRef<Record<string, string>>({})
function syncModelLabels(items: readonly VideoModel[]) {
  const next: Record<string, string> = {}
  for (const item of items) {
    const label = videoModelDisplayName(item)
    if (label) next[item.id] = label
  }
  modelLabels.value = next
}

function closeCanvasDialog() {
  canvasDialogOpen.value = false
  canvasPayload.value = null
}

function openAddToCanvas(job: VideoJob) {
  if (!canvasEnabled.value || job.status !== 'completed' || !isCanvasUuid(job.id)) return
  canvasPayload.value = {
    kind: 'video',
    videoId: job.id,
    title: `${videoModelDisplayName(job.model, modelLabels.value)} 视频作品`,
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

  const scope = scopeKeyFor(refInfo)
  const existingScopedHeld = heldByScope.value[scope] || ''
  // If user freshly entered this node from Creative Canvas (`staged` present) and the previous attempt
  // on this node already settled (not in `outstanding`), unlock the form so a new take (v2+) can be created.
  // An unresolved (`outstanding`) attempt ALWAYS stays locked & query-only across reloads and re-entries.
  if (
    existingScopedHeld &&
    !outstanding.value.includes(existingScopedHeld) &&
    readStagedCanvasDraftHandoff(props.userId, refInfo.canvasId, refInfo.nodeId)
  ) {
    setScopedHeld(scope, '')
    remember()
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
      heldId: heldByScope.value[scope] || undefined,
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
  const target = returnTarget.value
  if (!target) return
  clearCanvasDraftHandoff(props.userId)
  clearImage()
  void router.push({ name: 'CreativeCanvas', query: { board: target.canvasId, node: target.nodeId } })
}

function reserve(id: string) {
  const scope = currentScopeKey.value
  const previousHeld = { ...heldByScope.value }
  const previousOutstanding = outstanding.value
  const nextStatus = { ...statusById.value }
  delete nextStatus[id]
  statusById.value = nextStatus
  setScopedHeld(scope, id)
  outstanding.value = [id, ...previousOutstanding.filter(item => item !== id)]
  if (!remember()) {
    heldByScope.value = previousHeld
    outstanding.value = previousOutstanding
    throw new Error('无法保存原任务号。请允许此站点使用标签页存储后再试；未提交视频。')
  }
}
function resolve(id: string) {
  outstanding.value = outstanding.value.filter(item => item !== id)
  remember()
}
function release(id: string) {
  const next = { ...heldByScope.value }
  for (const [scope, val] of Object.entries(next)) {
    if (val === id) delete next[scope]
  }
  heldByScope.value = next
  resolve(id)
}
function settle(job: VideoJob) {
  statusById.value = { ...statusById.value, [job.id]: job.status }
  if (job.status !== 'uncertain') resolve(job.id)
}
function accept(job: VideoJob) {
  if (canvasDraft.value.status === 'ready') {
    lastValidCanvas.value = { canvasId: canvasDraft.value.canvasId, nodeId: canvasDraft.value.canvasId ? canvasDraft.value.nodeId : '' }
    clearCanvasDraftHandoff(props.userId)
  }
  settle(job)
  gallery.value?.adopt(job)
  composer.value?.close()
}
function newDraft() {
  setScopedHeld(currentScopeKey.value, '')
  lastValidCanvas.value = null
  remember()
}
function nextCanvasTake() {
  if (heldId.value && outstanding.value.includes(heldId.value)) return
  setScopedHeld(currentScopeKey.value, '')
  remember()
  void syncCanvasDraft()
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
