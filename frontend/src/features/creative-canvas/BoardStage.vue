<template>
  <div class="cc-stage-wrap">
    <VueFlow
      v-model:nodes="flowNodes"
      v-model:edges="flowEdges"
      class="cc-flow"
      :default-viewport="doc.viewport"
      :min-zoom="0.2"
      :max-zoom="2.2"
      :nodes-draggable="true"
      :nodes-connectable="true"
      :elements-selectable="true"
      :delete-key-code="null"
      :is-valid-connection="checkConnection"
      @node-click="onNodeClick"
      @pane-click="emit('selectNode', null)"
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
          :selected="selectedNodeId === slotProps.id"
          :poster-cache="posterCache"
          :poster-revision="posterRevision"
          :durations="durations"
          @preview-job="(job, trigger) => emit('previewJob', job, trigger)"
        />
      </template>

      <template #node-video-draft="slotProps">
        <VideoDraftCanvasNode
          :node="nodeOf(slotProps.data)"
          :source-node="sourceNodeOf(slotProps.id)"
          :source-asset="sourceAssetOf(slotProps.id)"
          :takes="takesOf(slotProps.id)"
          :active-take="activeTakeOf(slotProps.id)"
          :branch-label="branchLabels.get(slotProps.id) || '视频分支'"
          :branch-index="branchIndices.get(slotProps.id)"
          :selected="selectedNodeId === slotProps.id"
          :poster-cache="posterCache"
          :poster-revision="posterRevision"
          :durations="durations"
          :thumb-cache="thumbCache"
          @open-in-video-studio="emit('openInVideoStudio', $event)"
          @compare="emit('compare', $event)"
          @pin-take="(draftId, reqId) => emit('pinTake', draftId, reqId)"
          @preview-job="(job, trigger) => emit('previewJob', job, trigger)"
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

    <div class="cc-zoom-bar" role="toolbar" aria-label="画布缩放控制">
      <button type="button" class="cc-btn is-sm" aria-label="缩小" @click="onZoomOut">−</button>
      <span class="cc-zoom-label">{{ zoomPercent }}%</span>
      <button type="button" class="cc-btn is-sm" aria-label="放大" @click="onZoomIn">+</button>
      <button type="button" class="cc-btn is-sm" title="适配全部节点" @click="onFitView">全览</button>
    </div>

    <slot name="overlay" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, shallowRef, watch } from 'vue'
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
import { findDerivedDraftNodes, findSourceImageNode, isValidCanvasConnection } from './document'
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
  CanvasTake,
  CanvasViewport,
} from './types'

const props = defineProps<{
  boardId: string
  doc: CanvasDocument
  assets: Readonly<Record<string, CanvasAssetStatus>>
  attempts: readonly CanvasAttempt[]
  selectedNodeId: string | null
  posterCache: VideoPosterCache
  posterRevision: number
  durations: WorkspaceDurationStore
  thumbCache: CanvasImageThumbCache
}>()

const emit = defineEmits<{
  selectNode: [id: string | null]
  moveNodes: [points: { id: string; x: number; y: number }[]]
  connect: [sourceId: string, targetId: string]
  newDraftAt: [imageNodeId: string, at: { x: number; y: number }]
  createDraft: [imageNodeId: string]
  compare: [imageNodeId: string]
  openInVideoStudio: [draftNodeId: string]
  pinTake: [draftNodeId: string, requestId: string]
  previewJob: [job: VideoJob, trigger: HTMLElement]
  updateTitle: [nodeId: string, title: string]
  updatePrompt: [nodeId: string, prompt: string]
  commitEdit: []
  changeViewport: [viewport: CanvasViewport]
}>()

const { viewport, setViewport, zoomIn, zoomOut, fitView, screenToFlowPosition } = setupVueFlow()
let syncingViewport = false

const branchLabels = computed(() => buildBranchLabelMap(props.doc))
const branchIndices = computed(() => buildBranchIndexMap(props.doc))
const flowNodes = shallowRef<Node<FlowNodeData>[]>([])
const flowEdges = shallowRef<Edge[]>([])
const zoomPercent = computed(() => Math.round((viewport.value.zoom || 1) * 100))

watch(
  [() => props.boardId, () => props.doc, () => props.selectedNodeId] as const,
  ([boardId, doc, selectedId], previous) => {
    const boardChanged = Boolean(previous && previous[0] !== boardId)
    const labels = branchLabels.value
    const indices = branchIndices.value
    flowNodes.value = reconcileFlowNodes(boardChanged ? [] : flowNodes.value, doc, selectedId)
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

function branchCountOf(imageNodeId: string): number {
  return findDerivedDraftNodes(props.doc, imageNodeId).length
}

function sourceNodeOf(draftNodeId: string): CanvasNodeDoc | null {
  return findSourceImageNode(props.doc, draftNodeId)
}

function sourceAssetOf(draftNodeId: string): CanvasAssetStatus | undefined {
  const src = sourceNodeOf(draftNodeId)
  return src ? props.assets[src.id] : undefined
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
  return isValidCanvasConnection(props.doc, connection.source, connection.target)
}

function onNodeClick(payload: NodeMouseEvent) {
  emit('selectNode', payload.node.id)
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
  if (!isValidCanvasConnection(props.doc, connection.source, connection.target)) return
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
  if (!sourceDoc || sourceDoc.kind !== 'image') return
  if (props.assets[sourceDoc.id]?.available === false) return
  const client = pointerClientXY(payload.event)
  if (!client) return
  const pos = screenToFlowPosition(client)
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

function getSuggestedPosition(): { x: number; y: number } {
  const z = viewport.value.zoom || 1
  return {
    x: Math.round((220 - viewport.value.x) / z),
    y: Math.round((160 - viewport.value.y) / z),
  }
}

defineExpose({ getSuggestedPosition })
</script>
