<template>
  <div ref="stageEl" class="cc-stage-wrap">
    <VueFlow
      v-model:nodes="flowNodes"
      v-model:edges="flowEdges"
      class="cc-flow"
      :pro-options="{ hideAttribution: true }"
      :default-viewport="doc.viewport"
      :min-zoom="0.2"
      :max-zoom="2.2"
      :nodes-draggable="true"
      :nodes-connectable="true"
      :elements-selectable="true"
      :selection-on-drag="boxSelectMode"
      :pan-on-drag="!boxSelectMode"
      selection-key-code="Shift"
      multi-selection-key-code="Shift"
      :delete-key-code="null"
      :is-valid-connection="checkConnection"
      @node-click="onNodeClick"
      @pane-click="onPaneClick"
      @node-drag-stop="onNodeDragStop"
      @connect="onConnect"
      @connect-end="onConnectEnd"
      @move-end="onMoveEnd"
    >
      <template #node-image="slotProps">
        <ImageCanvasNode
          :node="nodeOf(slotProps.data)"
          :asset="assets[slotProps.id]"
          :selected="selectedNodeId === slotProps.id"
          :branch-count="branchCountOf(slotProps.id)"
          :thumb-cache="thumbCache"
          @create-draft="emit('createDraft', $event)"
          @compare="emit('compare', $event)"
        />
      </template>

      <template #node-video="slotProps">
        <VideoCanvasNode
          :node="nodeOf(slotProps.data)"
          :asset="assets[slotProps.id]"
          :selected="Boolean(slotProps.selected) || selectedNodeId === slotProps.id"
          :branch-count="branchCountOf(slotProps.id)"
          :poster-cache="posterCache"
          :poster-revision="posterRevision"
          :durations="durations"
          @preview-job="(job, trigger, nodeId) => emit('previewJob', job, trigger, nodeId)"
          @create-draft="emit('createDraft', $event)"
          @compare="emit('compare', $event)"
        />
      </template>

      <template #node-video-draft="slotProps">
        <VideoDraftCanvasNode
          :node="nodeOf(slotProps.data)"
          :source-node="sourceNodeOf(slotProps.id)"
          :source-asset="sourceAssetOf(slotProps.id)"
          :resolved-input="inputs[slotProps.id]"
          :provenance-badge="provenanceBadgeOf(slotProps.id)"
          :compare-parent-id="compareParentOf(slotProps.id)"
          :takes="takesOf(slotProps.id)"
          :active-take="activeTakeOf(slotProps.id)"
          :branch-label="branchLabels.get(slotProps.id) || '视频分镜'"
          :branch-index="branchIndices.get(slotProps.id)"
          :selected="Boolean(slotProps.selected) || selectedNodeId === slotProps.id"
          :poster-cache="posterCache"
          :poster-revision="posterRevision"
          :durations="durations"
          :thumb-cache="thumbCache"
          @open-in-video-studio="emit('openInVideoStudio', $event)"
          @compare="emit('compare', $event)"
          @pin-take="(draftId, reqId) => emit('pinTake', draftId, reqId)"
          @preview-job="(job, trigger, nodeId) => emit('previewJob', job, trigger, nodeId)"
          @inspect-draft="(draftId, trigger) => emit('inspectDraft', draftId, trigger)"
          @create-draft="emit('createDraft', $event)"
        />
      </template>

      <template #node-note="slotProps">
        <NoteCanvasNode
          :node="nodeOf(slotProps.data)"
          :selected="selectedNodeId === slotProps.id"
          @update-title="(id, title) => emit('updateTitle', id, title)"
          @update-prompt="(id, prompt) => emit('updatePrompt', id, prompt)"
          @commit-edit="emit('commitEdit')"
        />
      </template>
    </VueFlow>

    <div class="cc-zoom-bar" role="toolbar" aria-label="画布视图与框选控制">
      <button type="button" class="cc-btn is-sm" aria-label="缩小" @click="onZoomOut">−</button>
      <span class="cc-zoom-label">{{ zoomPercent }}%</span>
      <button type="button" class="cc-btn is-sm" aria-label="放大" @click="onZoomIn">+</button>
      <button type="button" class="cc-btn is-sm" title="适配全部节点" @click="onFitView">全览</button>
      <button
        type="button"
        class="cc-btn is-sm"
        :class="{ 'is-active': boxSelectMode }"
        :aria-pressed="boxSelectMode"
        title="切换拖拽框选节点（也可按住 Shift 拖拽框选）"
        @click="boxSelectMode = !boxSelectMode"
      >
        框选
      </button>
      <button
        v-if="selectedNodeId"
        type="button"
        class="cc-btn is-sm"
        title="将视口定位到当前选中节点"
        @click="focusNode(selectedNodeId)"
      >
        定位当前
      </button>
      <span v-if="multiSelectedCount > 1" class="cc-multi-select-pill" role="status">
        已选 {{ multiSelectedCount }} 项 · 可批量拖动
      </span>
    </div>

    <slot name="overlay" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, shallowRef, watch } from 'vue'
import {
  VueFlow,
  setupVueFlow,
  type ConnectEndEvent,
  type Connection,
  type Edge,
  type Node,
  type NodeDragEvent,
  type NodeMouseEvent,
  type Viewport,
} from '@xyflow/vue'
import '@xyflow/vue/dist/style.css'
import type { VideoJob } from '@/features/video-studio/api'
import type { VideoPosterCache } from '@/features/video-studio/posterCache'
import type { WorkspaceDurationStore } from '@/features/video-studio/mediaDuration'
import {
  DEFAULT_NODE_HEIGHT,
  DEFAULT_NODE_WIDTH,
  findDerivedDraftNodes,
  findParentMediaEdge,
  findSourceImageNode,
  isValidCanvasConnection,
} from './document'
import type { CanvasImageThumbCache } from './imageThumbCache'
import ImageCanvasNode from './nodes/ImageCanvasNode.vue'
import NoteCanvasNode from './nodes/NoteCanvasNode.vue'
import VideoCanvasNode from './nodes/VideoCanvasNode.vue'
import VideoDraftCanvasNode from './nodes/VideoDraftCanvasNode.vue'
import {
  activeTakeFromList,
  buildBranchIndexMap,
  buildBranchLabelMap,
  buildTakesForDraft,
  describeDraftProvenance,
  isViewportEqual,
  reconcileFlowEdges,
  reconcileFlowNodes,
  type FlowNodeData,
} from './screenModel'
import type {
  CanvasAssetStatus,
  CanvasAttempt,
  CanvasDocument,
  CanvasNodeDoc,
  CanvasResolvedInput,
  CanvasTake,
  CanvasViewport,
} from './types'

const props = withDefaults(defineProps<{
  boardId: string
  doc: CanvasDocument
  assets: Readonly<Record<string, CanvasAssetStatus>>
  inputs?: Readonly<Record<string, CanvasResolvedInput>>
  attempts: readonly CanvasAttempt[]
  selectedNodeId: string | null
  posterCache: VideoPosterCache
  posterRevision: number
  durations: WorkspaceDurationStore
  thumbCache: CanvasImageThumbCache
}>(), {
  inputs: () => ({}),
})

const emit = defineEmits<{
  selectNode: [id: string | null]
  moveNodes: [points: { id: string; x: number; y: number }[]]
  connect: [sourceId: string, targetId: string]
  newDraftAt: [sourceNodeId: string, at: { x: number; y: number }]
  createDraft: [sourceNodeId: string]
  compare: [parentNodeId: string]
  openInVideoStudio: [draftNodeId: string]
  pinTake: [draftNodeId: string, requestId: string]
  previewJob: [job: VideoJob, trigger: HTMLElement, nodeId?: string]
  inspectDraft: [draftNodeId: string, trigger: HTMLElement]
  updateTitle: [nodeId: string, title: string]
  updatePrompt: [nodeId: string, prompt: string]
  commitEdit: []
  changeViewport: [viewport: CanvasViewport]
}>()

const stageEl = ref<HTMLElement>()
const boxSelectMode = ref(false)
const { viewport, setViewport, zoomIn, zoomOut, fitView, screenToFlowPosition } = setupVueFlow()
let syncingViewport = false
let suppressNextPaneClickUntil = 0

const branchLabels = computed(() => buildBranchLabelMap(props.doc, props.attempts))
const branchIndices = computed(() => buildBranchIndexMap(props.doc))
const flowNodes = shallowRef<Node<FlowNodeData>[]>([])
const flowEdges = shallowRef<Edge[]>([])
const zoomPercent = computed(() => Math.round((viewport.value.zoom || 1) * 100))
const multiSelectedCount = computed(() => flowNodes.value.filter(n => n.selected).length)

watch(
  [() => props.boardId, () => props.doc, () => props.selectedNodeId, () => props.attempts] as const,
  ([boardId, doc, selectedId], previous) => {
    const boardChanged = Boolean(previous && previous[0] !== boardId)
    const selectionChanged = Boolean(previous && previous[2] !== selectedId)
    const existingMulti =
      !boardChanged && !selectionChanged && multiSelectedCount.value > 1
        ? new Set(flowNodes.value.filter(n => n.selected).map(n => n.id))
        : undefined
    const labels = branchLabels.value
    const indices = branchIndices.value
    flowNodes.value = reconcileFlowNodes(
      boardChanged ? [] : flowNodes.value,
      doc,
      selectedId,
      existingMulti,
    )
    flowEdges.value = reconcileFlowEdges(
      boardChanged ? [] : flowEdges.value,
      doc,
      selectedId,
      labels,
      indices,
    )
  },
  { immediate: true },
)

function nodeOf(data: unknown): CanvasNodeDoc {
  return (data as FlowNodeData).node
}

function branchCountOf(sourceNodeId: string): number {
  return findDerivedDraftNodes(props.doc, sourceNodeId).length
}

function sourceNodeOf(draftNodeId: string): CanvasNodeDoc | null {
  return findSourceImageNode(props.doc, draftNodeId)
}

function sourceAssetOf(draftNodeId: string): CanvasAssetStatus | undefined {
  const src = sourceNodeOf(draftNodeId)
  return src ? props.assets[src.id] : undefined
}

function provenanceBadgeOf(draftNodeId: string): string {
  return describeDraftProvenance(props.doc, draftNodeId, props.attempts).badge
}

function compareParentOf(draftNodeId: string): string | null {
  const parent = findParentMediaEdge(props.doc, draftNodeId)
  if (!parent) return null
  return findDerivedDraftNodes(props.doc, parent.sourceNode.id).length > 1 ? parent.sourceNode.id : null
}

function takesOf(draftNodeId: string): CanvasTake[] {
  const draft = props.doc.nodes.find(n => n.id === draftNodeId)
  return draft ? buildTakesForDraft(draft, props.attempts) : []
}

function activeTakeOf(draftNodeId: string): CanvasTake | null {
  return activeTakeFromList(takesOf(draftNodeId))
}

function checkConnection(connection: Edge | Connection): boolean {
  if ('id' in connection && typeof connection.id === 'string') {
    return (
      connection.id === `${connection.source}->${connection.target}` &&
      props.doc.edges.some(
        edge => edge.source === connection.source && edge.target === connection.target,
      )
    )
  }
  return isValidCanvasConnection(props.doc, connection.source, connection.target, props.attempts)
}

function onNodeClick(payload: NodeMouseEvent) {
  emit('selectNode', payload.node.id)
}

function onPaneClick() {
  if (Date.now() < suppressNextPaneClickUntil) {
    suppressNextPaneClickUntil = 0
    return
  }
  emit('selectNode', null)
}

function onNodeDragStop(payload: NodeDragEvent) {
  const moved = payload.nodes.length ? payload.nodes : [payload.node]
  const points = moved.map(n => ({
    id: n.id,
    x: n.position.x,
    y: n.position.y,
  }))
  if (points.length) emit('moveNodes', points)
}

function onConnect(connection: Connection) {
  if (!isValidCanvasConnection(props.doc, connection.source, connection.target, props.attempts)) return
  emit('connect', connection.source, connection.target)
}

function pointerClientXY(event: MouseEvent | TouchEvent): { x: number; y: number } | null {
  if ('changedTouches' in event && event.changedTouches.length) {
    const touch = event.changedTouches[0]
    return { x: touch.clientX, y: touch.clientY }
  }
  if ('clientX' in event) {
    return { x: event.clientX, y: event.clientY }
  }
  return null
}

function onConnectEnd(payload: ConnectEndEvent) {
  const state = payload.connectionState
  if (state.isValid || state.toNode) return
  if (state.fromHandle?.type !== 'source' || !state.fromNode) return
  const sourceDoc = props.doc.nodes.find(n => n.id === state.fromNode?.id)
  if (
    !sourceDoc ||
    (sourceDoc.kind !== 'image' && sourceDoc.kind !== 'video' && sourceDoc.kind !== 'video-draft')
  ) {
    return
  }
  if (sourceDoc.kind === 'image' && props.assets[sourceDoc.id]?.available !== true) return
  if (
    sourceDoc.kind === 'video' &&
    (props.assets[sourceDoc.id]?.available !== true || props.assets[sourceDoc.id]?.job?.status !== 'completed')
  ) {
    return
  }
  const client = pointerClientXY(payload.event)
  if (!client) return
  const pos = screenToFlowPosition(client)
  suppressNextPaneClickUntil = Date.now() + 280
  emit('newDraftAt', sourceDoc.id, { x: Math.round(pos.x), y: Math.round(pos.y) })
}

function onMoveEnd(payload: { event: MouseEvent | TouchEvent | null; viewport: Viewport }) {
  if (syncingViewport || !payload.event) return
  const next: CanvasViewport = {
    x: payload.viewport.x,
    y: payload.viewport.y,
    zoom: payload.viewport.zoom,
  }
  if (isViewportEqual(next, props.doc.viewport)) return
  emit('changeViewport', next)
}

async function applySavedViewport(target: CanvasViewport) {
  if (isViewportEqual(viewport.value, target)) return
  syncingViewport = true
  try {
    await nextTick()
    await setViewport({ x: target.x, y: target.y, zoom: target.zoom })
  } finally {
    syncingViewport = false
  }
}

watch(
  () => [props.boardId, props.doc.viewport.x, props.doc.viewport.y, props.doc.viewport.zoom] as const,
  ([, x, y, zoom]) => {
    void applySavedViewport({ x, y, zoom })
  },
  { immediate: true },
)

async function commitViewportAfterControl(run: () => Promise<boolean>) {
  syncingViewport = true
  try {
    await run()
  } finally {
    syncingViewport = false
  }
  const next: CanvasViewport = {
    x: viewport.value.x,
    y: viewport.value.y,
    zoom: viewport.value.zoom,
  }
  if (!isViewportEqual(next, props.doc.viewport)) {
    emit('changeViewport', next)
  }
}

function onZoomIn() {
  void commitViewportAfterControl(() => zoomIn({ duration: 150 }))
}

function onZoomOut() {
  void commitViewportAfterControl(() => zoomOut({ duration: 150 }))
}

function onFitView() {
  void commitViewportAfterControl(() => fitView({ padding: 0.14, maxZoom: 1.15, duration: 180 }))
}

async function focusNode(nodeId: string, avoidInspector = false) {
  const target = props.doc.nodes.find(n => n.id === nodeId)
  if (!target) return
  const rect = stageEl.value?.getBoundingClientRect()
  const width = rect && rect.width > 0 ? rect.width : 960
  const height = rect && rect.height > 0 ? rect.height : 600
  const safeWidth = width > 768 && avoidInspector ? Math.max(360, width - 316) : width
  const z = Math.max(0.75, Math.min(1.15, viewport.value.zoom || 0.9))
  const cx = target.x + DEFAULT_NODE_WIDTH / 2
  const cy = target.y + DEFAULT_NODE_HEIGHT[target.kind] / 2
  const nextX = Math.round((safeWidth / 2 - cx * z) * 100) / 100
  const nextY = Math.round(((height - 36) / 2 - cy * z) * 100) / 100
  await commitViewportAfterControl(() => setViewport({ x: nextX, y: nextY, zoom: z }, { duration: 220 }))
}

async function ensureNodeVisible(nodeId: string, options?: { avoidInspector?: boolean }) {
  await nextTick()
  const target = props.doc.nodes.find(n => n.id === nodeId)
  if (!target) return
  const rect = stageEl.value?.getBoundingClientRect()
  if (!rect || rect.width <= 0 || rect.height <= 0) return
  const z = viewport.value.zoom || 1
  const isCompact = rect.width <= 768
  const cardW = (isCompact ? 272 : DEFAULT_NODE_WIDTH) * z
  const cardH = DEFAULT_NODE_HEIGHT[target.kind] * z
  const portAllowance = target.kind === 'note' ? 12 : 42
  const left = target.x * z + viewport.value.x
  const right = left + cardW + portAllowance
  const top = target.y * z + viewport.value.y
  const bottom = top + cardH

  const minX = isCompact ? 12 : 24
  const maxX = rect.width - (!isCompact && options?.avoidInspector ? 320 : isCompact ? 12 : 24)
  const minY = isCompact ? 14 : 20
  const maxY = rect.height - 58

  let dx = 0
  if (right > maxX) {
    dx = maxX - right
    if (left + dx < minX) dx = minX - left
  } else if (left < minX) {
    dx = minX - left
  }

  let dy = 0
  if (bottom > maxY) {
    dy = maxY - bottom
    if (top + dy < minY) dy = minY - top
  } else if (top < minY) {
    dy = minY - top
  }

  if (Math.abs(dx) > 4 || Math.abs(dy) > 4) {
    await commitViewportAfterControl(() =>
      setViewport(
        {
          x: Math.round((viewport.value.x + dx) * 100) / 100,
          y: Math.round((viewport.value.y + dy) * 100) / 100,
          zoom: z,
        },
        { duration: 180 },
      ),
    )
  }
}

function getSuggestedPosition(avoidInspector = false): { x: number; y: number } {
  const z = viewport.value.zoom || 1
  const rect = stageEl.value?.getBoundingClientRect()
  if (!rect || rect.width <= 0 || rect.height <= 0) {
    return {
      x: Math.round((80 - viewport.value.x) / z),
      y: Math.round((72 - viewport.value.y) / z),
    }
  }
  const isCompact = rect.width <= 768
  const cardW = (isCompact ? 272 : DEFAULT_NODE_WIDTH) * z
  const safeWidth = !isCompact && avoidInspector ? Math.max(340, rect.width - 320) : rect.width
  const screenX = isCompact
    ? Math.max(12, Math.round((safeWidth - 16 - cardW) / 2))
    : Math.max(36, Math.min(160, Math.round((safeWidth - cardW) * 0.25)))
  const screenY = isCompact ? 24 : 52
  return {
    x: Math.round((screenX - viewport.value.x) / z),
    y: Math.round((screenY - viewport.value.y) / z),
  }
}

defineExpose({ getSuggestedPosition, focusNode, ensureNodeVisible })
</script>
