<template>
  <AppLayout>
    <section class="cc-workspace" :class="{ 'is-focus-mode': focusMode }" aria-label="无限画布工作区">
      <BoardTopbar
        ref="topbarRef"
        :boards="editor.boards.value"
        :board="editor.board.value"
        :title="editor.title.value"
        :dirty="editor.dirty.value"
        :busy="editor.busy.value"
        :saving="editor.saving.value"
        :conflict="editor.conflict.value"
        :save-error-kind="editor.saveErrorKind.value"
        :save-unconfirmed="editor.saveUnconfirmed.value"
        :last-saved-at="editor.lastSavedAt.value"
        :has-saved-in-session="editor.hasSavedInSession.value"
        :focus-mode="focusMode"
        :can-undo="editor.canUndo.value"
        :can-redo="editor.canRedo.value"
        :can-create-draft="Boolean(activeBranchSourceId)"
        :create-draft-label="topbarCreateLabel"
        :can-compare="Boolean(compareTargetNodeId)"
        :searchable-nodes="searchableNodes"
        @select-board="handleSelectBoard"
        @open-create="createModalOpen = true"
        @rename="editor.rename"
        @add-note="handleAddNote"
        @create-draft="handleCreateDraftFromTopbar"
        @add-root-draft="handleAddRootDraft"
        @compare="handleCompareFromTopbar"
        @undo="editor.undo()"
        @redo="editor.redo()"
        @save="handleSave"
        @reload-board="handleReloadBoard"
        @toggle-focus="focusMode = !focusMode"
        @delete-board="handleDeleteBoard"
        @locate-node="handleLocateNode"
      />

      <div v-if="editor.conflict.value" class="cc-banner" role="alert">
        <span>此画布在另一处更新 · 本次修改已保留，未覆盖服务端新版本。</span>
        <button type="button" class="cc-btn is-sm" :disabled="editor.busy.value || editor.saving.value" @click="handleReloadBoard">
          重新加载最新画布
        </button>
      </div>

      <div v-else-if="editor.error.value" class="cc-banner is-error" role="alert">
        <span>{{ editor.error.value }}</span>
        <div class="cc-banner-actions">
          <button type="button" class="cc-btn is-sm" :disabled="editor.busy.value || editor.saving.value" @click="handleRetry">
            {{ editor.board.value && (editor.dirty.value || editor.saveUnconfirmed.value) ? '重试保存' : '重试' }}
          </button>
          <button
            v-if="editor.board.value && editor.saveUnconfirmed.value"
            type="button"
            class="cc-btn is-sm"
            :disabled="editor.busy.value || editor.saving.value"
            @click="handleReloadBoard"
          >
            重新加载最新画布
          </button>
        </div>
      </div>

      <BoardEmptyState
        v-if="!editor.board.value"
        :boards="editor.boards.value"
        :busy="editor.busy.value"
        :error="editor.error.value"
        @open-board="handleSelectBoard"
        @create-board="handleCreateBoard"
        @retry="handleRetry"
      />

      <BoardStage
        v-else
        ref="stageRef"
        :board-id="editor.board.value.id"
        :doc="editor.doc.value"
        :assets="editor.board.value.assets"
        :inputs="editor.board.value.inputs"
        :attempts="editor.board.value.attempts"
        :selected-node-id="selectedNodeId"
        :poster-cache="posterCache"
        :poster-revision="posterRevision"
        :durations="durations"
        :thumb-cache="thumbCache"
        @select-node="handleSelectNode"
        @move-nodes="editor.moveNodes"
        @connect="editor.connect"
        @new-draft-at="handleNewDraftAt"
        @create-draft="handleCreateDraft"
        @compare="openCompareModal"
        @open-in-video-studio="handleOpenInVideoStudio"
        @pin-take="editor.pin"
        @preview-job="handlePreviewJob"
        @inspect-draft="handleInspectDraft"
        @update-title="handleUpdateTitle"
        @update-prompt="handleUpdatePrompt"
        @commit-edit="editor.commit()"
        @change-viewport="editor.changeViewport"
      >
        <template #overlay>
          <div
            v-if="editor.doc.value.nodes.length === 0"
            class="cc-empty-stage-guide"
            role="region"
            aria-label="空白画布新手引导"
          >
            <div class="cc-empty-stage-card">
              <span class="cc-pill is-draft">视频分镜编排 · 编排与自动保存不扣费</span>
              <h2>从文字、已有原图或成片开始编排</h2>
              <p>画布用于多分支对比与固定尾帧续镜，不支持在画布内直接生成图片；仅在进入视频工作台点击确认后才提交扣费。</p>
              <div class="cc-empty-stage-actions">
                <button type="button" class="cc-btn is-primary" @click="handleAddRootDraft">
                  + 用文字做视频分镜
                </button>
                <button type="button" class="cc-btn" @click="router.push('/image-studio')">
                  去生图工作台做图 / 加入原图
                </button>
                <button type="button" class="cc-btn" @click="router.push('/video-studio')">
                  从视频工作台导入成片
                </button>
              </div>
            </div>
          </div>

          <nav
            v-if="selectedNode && selectedUpstreamChain.length"
            class="cc-lineage-hud"
            aria-label="当前节点上游溯源"
          >
            <span class="cc-lineage-hud-label">上游：</span>
            <template v-for="(anc, idx) in selectedUpstreamChain" :key="anc.id">
              <button
                type="button"
                class="cc-upstream-chip"
                @click="handleLocateNode(anc.id)"
              >
                {{ anc.title || '上游节点' }}
              </button>
              <span v-if="idx < selectedUpstreamChain.length - 1" class="cc-upstream-sep" aria-hidden="true">→</span>
            </template>
            <button
              type="button"
              class="cc-btn is-sm"
              @click="handleLocateNode(selectedUpstreamChain[selectedUpstreamChain.length - 1].id)"
            >
              定位上一镜
            </button>
          </nav>

          <CanvasInspector
            v-if="inspectorOpen && selectedNode"
            :node="selectedNode"
            :asset="editor.board.value.assets[selectedNode.id]"
            :resolved-input="editor.board.value.inputs?.[selectedNode.id]"
            :source-node="selectedSourceNode"
            :upstream-chain="selectedUpstreamChain"
            :provenance-detail="selectedProvenanceDetail"
            :compare-parent-id="selectedCompareParentId"
            :branch-count="selectedBranchCount"
            :takes="selectedTakes"
            :durations="durations"
            @close="closeInspector"
            @locate-node="handleLocateNode"
            @update-title="handleUpdateTitle"
            @update-prompt="handleUpdatePrompt"
            @commit-edit="editor.commit()"
            @create-draft="handleCreateDraft"
            @compare="openCompareModal"
            @open-in-video-studio="handleOpenInVideoStudio"
            @pin-take="editor.pin"
            @preview-job="handlePreviewJob"
            @remove-node="handleRemoveNode"
          />
          <button
            v-else-if="selectedNode"
            type="button"
            class="cc-btn is-primary cc-inspector-trigger"
            aria-label="打开节点属性面板"
            @click="openInspectorForSelected"
          >
            属性 · {{ selectedNode.title || '已选节点' }}
          </button>
        </template>
      </BoardStage>
    </section>

    <BranchCompareModal
      :open="Boolean(compareParentNode)"
      :heading="compareParentNode?.title || '同源分镜'"
      :source-thumb-url="compareSourceThumbUrl"
      :branches="compareEntries"
      :poster-cache="posterCache"
      :poster-revision="posterRevision"
      :durations="durations"
      @close="compareParentId = null"
      @pin-take="editor.pin"
      @open-in-video-studio="handleOpenInVideoStudio"
      @preview-job="handlePreviewJob"
    />

    <BoardCreateModal
      :open="createModalOpen"
      :busy="editor.busy.value"
      @close="createModalOpen = false"
      @confirm="handleCreateBoard"
    />

    <VideoDelivery
      ref="deliveryRef"
      :mobile="isMobile"
      :cache="posterCache"
      :durations="durations"
      :jobs="deliveryJobs"
      :allow-reuse="false"
      :canvas-context="deliveryCanvasContext"
      @expired="handleJobExpired"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAuthStore } from '@/stores/auth'
import type { VideoJob } from '@/features/video-studio/api'
import VideoDelivery, { type DeliveryCanvasContext } from '@/features/video-studio/VideoDelivery.vue'
import { createWorkspaceDurationStore } from '@/features/video-studio/mediaDuration'
import { createVideoPosterCache } from '@/features/video-studio/posterCache'
import '@/features/creative-canvas/canvas.css'
import BoardCreateModal from '@/features/creative-canvas/BoardCreateModal.vue'
import BoardEmptyState from '@/features/creative-canvas/BoardEmptyState.vue'
import BoardStage from '@/features/creative-canvas/BoardStage.vue'
import BoardTopbar, { type SearchableNodeEntry } from '@/features/creative-canvas/BoardTopbar.vue'
import BranchCompareModal from '@/features/creative-canvas/BranchCompareModal.vue'
import CanvasInspector from '@/features/creative-canvas/CanvasInspector.vue'
import {
  findDerivedDraftNodes,
  findParentMediaEdge,
  findSourceImageNode,
  findUpstreamChain,
  isCanvasUuid,
} from '@/features/creative-canvas/document'
import { createCanvasImageThumbCache } from '@/features/creative-canvas/imageThumbCache'
import {
  activeTakeFromList,
  buildBranchLabelMap,
  buildCompareEntries,
  buildTakesForDraft,
  collectBoardVideoJobs,
  describeDraftProvenance,
  resolveCompareImageId,
} from '@/features/creative-canvas/screenModel'
import { useCanvasEditor } from '@/features/creative-canvas/useCanvasEditor'

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const editor = useCanvasEditor()

const posterCache = shallowRef(createVideoPosterCache())
const durations = createWorkspaceDurationStore()
const thumbCache = shallowRef(createCanvasImageThumbCache(authStore.user?.id))
const posterRevision = ref(0)

const topbarRef = ref<InstanceType<typeof BoardTopbar>>()
const stageRef = ref<InstanceType<typeof BoardStage>>()
const deliveryRef = ref<InstanceType<typeof VideoDelivery>>()
const selectedNodeId = ref<string | null>(null)
const inspectorOpen = ref(false)
const focusMode = ref(false)
const compareParentId = ref<string | null>(null)
const createModalOpen = ref(false)
const previewedJob = shallowRef<VideoJob | null>(null)
const deliveryCanvasContext = shallowRef<DeliveryCanvasContext | null>(null)
const isMobile = ref(typeof window !== 'undefined' ? window.innerWidth <= 768 : false)
let inspectorOpener: HTMLElement | null = null

const selectedNode = computed(() =>
  selectedNodeId.value
    ? editor.doc.value.nodes.find(n => n.id === selectedNodeId.value) ?? null
    : null,
)
const selectedSourceNode = computed(() =>
  selectedNode.value?.kind === 'video-draft'
    ? findSourceImageNode(editor.doc.value, selectedNode.value.id)
    : null,
)
const selectedUpstreamChain = computed(() =>
  selectedNode.value ? findUpstreamChain(editor.doc.value, selectedNode.value.id) : [],
)
const selectedProvenanceDetail = computed(() =>
  selectedNode.value?.kind === 'video-draft'
    ? describeDraftProvenance(editor.doc.value, selectedNode.value.id, editor.board.value?.attempts ?? []).detail
    : '无（纯文字草稿）',
)
const selectedCompareParentId = computed(() => {
  if (selectedNode.value?.kind !== 'video-draft') return null
  const parent = findParentMediaEdge(editor.doc.value, selectedNode.value.id)
  if (!parent) return null
  return findDerivedDraftNodes(editor.doc.value, parent.sourceNode.id).length > 1
    ? parent.sourceNode.id
    : null
})
const selectedBranchCount = computed(() =>
  selectedNode.value && selectedNode.value.kind !== 'note'
    ? findDerivedDraftNodes(editor.doc.value, selectedNode.value.id).length
    : 0,
)
const selectedTakes = computed(() =>
  selectedNode.value?.kind === 'video-draft'
    ? buildTakesForDraft(selectedNode.value, editor.board.value?.attempts ?? [])
    : [],
)

const activeBranchSourceId = computed(() => {
  const node = selectedNode.value
  if (!node || node.kind === 'note') return null
  if (node.kind === 'image') {
    return editor.board.value?.assets[node.id]?.available === true ? node.id : null
  }
  if (node.kind === 'video') {
    const asset = editor.board.value?.assets[node.id]
    return asset?.available === true && asset.job?.status === 'completed' ? node.id : null
  }
  const activeTake = activeTakeFromList(selectedTakes.value)
  if (activeTake && !activeTake.unavailable && activeTake.job?.status === 'completed') {
    return node.id
  }
  if (editor.board.value?.inputs?.[node.id]?.available === false) return null
  if (selectedSourceNode.value && editor.board.value?.assets[selectedSourceNode.value.id]?.available === false) {
    return null
  }
  return node.id
})

const topbarCreateLabel = computed(() => {
  const node = selectedNode.value
  if (!node || !activeBranchSourceId.value) return '+ 文字视频分镜'
  if (node.kind === 'image') return '+ 视频草稿'
  if (node.kind === 'video') return '+ 尾帧续镜'
  const activeTake = activeTakeFromList(selectedTakes.value)
  if (activeTake && !activeTake.unavailable && activeTake.job?.status === 'completed') {
    return `+ v${activeTake.takeNumber} 尾帧续镜`
  }
  return '+ 沿用素材续镜'
})

const compareTargetNodeId = computed(() =>
  resolveCompareImageId(editor.doc.value, selectedNodeId.value),
)

const compareParentNode = computed(() =>
  compareParentId.value
    ? editor.doc.value.nodes.find(n => n.id === compareParentId.value && n.kind !== 'note') ?? null
    : null,
)

const compareSourceThumbUrl = computed(() => {
  const parent = compareParentNode.value
  if (!parent || parent.kind !== 'image') return ''
  if (editor.board.value?.assets[parent.id]?.available === false) return ''
  const thumb = thumbCache.value.get(parent.image)
  return thumb.status === 'ready' ? thumb.url : ''
})

const compareEntries = computed(() =>
  compareParentNode.value
    ? buildCompareEntries(editor.doc.value, compareParentNode.value.id, editor.board.value?.attempts ?? [])
    : [],
)

const searchableNodes = computed<SearchableNodeEntry[]>(() => {
  const labels = buildBranchLabelMap(editor.doc.value, editor.board.value?.attempts ?? [])
  return editor.doc.value.nodes.map(node => ({
    id: node.id,
    kind: node.kind,
    title: node.title || (node.kind === 'image' ? '图片原图' : node.kind === 'video' ? '视频作品' : node.kind === 'video-draft' ? '视频分镜草稿' : '灵感便签'),
    subtitle: [labels.get(node.id), node.prompt?.slice(0, 36)].filter(Boolean).join(' · '),
  }))
})

const boardVideoJobs = computed(() => collectBoardVideoJobs(editor.board.value))
const deliveryJobs = computed(() =>
  previewedJob.value ? [previewedJob.value] : boardVideoJobs.value,
)

function routeBoardId(): string | undefined {
  const raw = route.query.board
  const val = Array.isArray(raw) ? raw[0] : raw
  return typeof val === 'string' && val.trim() ? val.trim() : undefined
}

function routeNodeId(): string | undefined {
  const raw = route.query.node
  const val = Array.isArray(raw) ? raw[0] : raw
  return typeof val === 'string' && isCanvasUuid(val.trim()) ? val.trim() : undefined
}

function confirmDiscardIfDirty(message = '当前画布有未保存或未确认的修改，继续操作可能丢失这些更改。确定要继续吗？'): boolean {
  if (!editor.dirty.value && !editor.saving.value && !editor.saveUnconfirmed.value && !editor.conflict.value) return true
  return window.confirm(message)
}

async function openAndRestoreSelection(boardId?: string, requestedNodeId?: string) {
  await editor.open(boardId)
  if (requestedNodeId && editor.doc.value.nodes.some(n => n.id === requestedNodeId)) {
    selectedNodeId.value = requestedNodeId
    if (!isMobile.value) {
      inspectorOpen.value = true
    }
  }
}

watch(
  () => [authStore.user?.id, routeBoardId(), routeNodeId()] as const,
  ([userId, boardId, targetNode], previous) => {
    if (previous && previous[0] !== userId) {
      posterCache.value.dispose()
      thumbCache.value.dispose()
      posterCache.value = createVideoPosterCache()
      thumbCache.value = createCanvasImageThumbCache(userId)
      durations.clear()
    }
    if (!userId) return
    if (previous && previous[0] === userId && previous[1] === boardId && editor.board.value?.id === boardId) {
      if (targetNode && editor.doc.value.nodes.some(n => n.id === targetNode)) {
        handleLocateNode(targetNode)
      }
      return
    }
    selectedNodeId.value = null
    inspectorOpen.value = false
    compareParentId.value = null
    void openAndRestoreSelection(boardId, targetNode)
  },
  { immediate: true },
)

watch(
  () => editor.doc.value.nodes,
  nodes => {
    if (selectedNodeId.value && !nodes.some(n => n.id === selectedNodeId.value)) {
      selectedNodeId.value = null
      inspectorOpen.value = false
    }
    if (compareParentId.value && !nodes.some(n => n.id === compareParentId.value)) {
      compareParentId.value = null
    }
  },
)

function closeInspector() {
  inspectorOpen.value = false
  const target = inspectorOpener
  inspectorOpener = null
  if (target?.isConnected) {
    void nextTick(() => target.focus())
  }
}

function openInspectorForSelected() {
  inspectorOpen.value = true
  if (selectedNodeId.value) {
    void stageRef.value?.ensureNodeVisible(selectedNodeId.value, { avoidInspector: !isMobile.value })
  }
}

function handleSelectNode(id: string | null) {
  selectedNodeId.value = id
  if (!id) {
    inspectorOpen.value = false
    return
  }
  if (!isMobile.value) {
    inspectorOpen.value = true
  }
  void stageRef.value?.ensureNodeVisible(id, { avoidInspector: !isMobile.value && inspectorOpen.value })
}

function handleInspectDraft(draftId: string, trigger?: HTMLElement) {
  inspectorOpener = trigger ?? null
  selectedNodeId.value = draftId
  inspectorOpen.value = true
  void stageRef.value?.ensureNodeVisible(draftId, { avoidInspector: !isMobile.value })
}

function handleLocateNode(nodeId: string) {
  if (!editor.doc.value.nodes.some(n => n.id === nodeId)) return
  selectedNodeId.value = nodeId
  if (!isMobile.value) {
    inspectorOpen.value = true
  }
  void stageRef.value?.focusNode(nodeId, !isMobile.value)
}

async function handleSelectBoard(nextId: string) {
  const currentId = editor.board.value?.id || ''
  if (nextId === currentId) return
  if (!confirmDiscardIfDirty()) return
  const query = { ...route.query }
  if (nextId) {
    query.board = nextId
  } else {
    delete query.board
  }
  await router.replace({ query })
}

async function handleCreateBoard(title: string) {
  if (!confirmDiscardIfDirty()) return
  createModalOpen.value = false
  await editor.create(title)
  const createdId = editor.board.value?.id
  if (createdId && routeBoardId() !== createdId) {
    await router.replace({ query: { ...route.query, board: createdId } })
  }
}

async function handleSave() {
  await editor.save()
}

async function handleReloadBoard() {
  if (!confirmDiscardIfDirty('重新加载将覆盖当前未保存修改，确定继续吗？')) return
  posterRevision.value += 1
  await editor.open(routeBoardId())
}

async function handleRetry() {
  if (editor.board.value && (editor.dirty.value || editor.saveUnconfirmed.value) && !editor.conflict.value) {
    await editor.save()
    return
  }
  posterRevision.value += 1
  await editor.open(routeBoardId())
}

async function handleDeleteBoard() {
  if (!editor.board.value) return
  const ok = window.confirm(
    `确定删除画布「${editor.title.value || '未命名画布'}」吗？画布中的生图与视频原作品不会被删除。`,
  )
  if (!ok) return
  const removed = await editor.removeBoard()
  if (removed) {
    const query = { ...route.query }
    delete query.board
    await router.replace({ query })
  }
}

function handleAddNote() {
  const at = stageRef.value?.getSuggestedPosition(!isMobile.value)
  const id = editor.addNote(at)
  if (id) handleSelectNode(id)
}

function handleAddRootDraft() {
  const at = stageRef.value?.getSuggestedPosition(!isMobile.value)
  const id = editor.addRootDraft(at)
  if (id) handleSelectNode(id)
}

function handleCreateDraft(sourceNodeId: string) {
  const id = editor.newDraft(sourceNodeId)
  if (id) handleSelectNode(id)
}

function handleNewDraftAt(sourceNodeId: string, at: { x: number; y: number }) {
  const id = editor.newDraft(sourceNodeId, at)
  if (id) handleSelectNode(id)
}

function handleCreateDraftFromTopbar() {
  if (!activeBranchSourceId.value) return
  handleCreateDraft(activeBranchSourceId.value)
}

function openCompareModal(parentNodeId: string) {
  compareParentId.value = parentNodeId
}

function handleCompareFromTopbar() {
  if (!compareTargetNodeId.value) return
  openCompareModal(compareTargetNodeId.value)
}

function handleUpdateTitle(nodeId: string, title: string) {
  editor.changeNode(nodeId, { title })
}

function handleUpdatePrompt(nodeId: string, prompt: string) {
  editor.changeNode(nodeId, { prompt })
}

function handleRemoveNode(nodeId: string) {
  editor.removeNode(nodeId)
  if (selectedNodeId.value === nodeId) {
    selectedNodeId.value = null
    inspectorOpen.value = false
  }
}

async function handleOpenInVideoStudio(draftNodeId: string) {
  compareParentId.value = null
  await editor.toStudio(draftNodeId)
}

function handlePreviewJob(job: VideoJob, trigger: HTMLElement, nodeId?: string) {
  previewedJob.value = job
  if (nodeId) {
    selectedNodeId.value = nodeId
    const node = editor.doc.value.nodes.find(n => n.id === nodeId)
    if (node?.kind === 'video-draft') {
      const takes = buildTakesForDraft(node, editor.board.value?.attempts ?? [])
      const matched = takes.find(item => item.requestId === job.id)
      const prov = describeDraftProvenance(editor.doc.value, node.id, editor.board.value?.attempts ?? [])
      deliveryCanvasContext.value = {
        nodeTitle: node.title || '视频分镜',
        takeLabel: matched ? `v${matched.takeNumber}${matched.isPinned ? ' · 已固定展示' : ' · 当前选中'}` : '分镜成片',
        prompt: node.prompt || '',
        provenance: prov.detail,
      }
    } else if (node?.kind === 'video') {
      deliveryCanvasContext.value = {
        nodeTitle: node.title || '导入视频作品',
        takeLabel: '画布导入作品',
        provenance: '已授权的工作台成片引用，可在画布就地审片或用尾帧续接下一镜',
      }
    } else {
      deliveryCanvasContext.value = null
    }
  } else {
    deliveryCanvasContext.value = null
  }
  void deliveryRef.value?.open(job, 'details', trigger)
}

function handleJobExpired(jobId: string) {
  posterCache.value.invalidate(jobId)
}

function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  const tag = target.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target.isContentEditable
}

function onKeydown(event: KeyboardEvent) {
  if (typeof document !== 'undefined' && document.querySelector('dialog.vs-delivery-dialog[open]')) {
    return
  }
  if (event.key === 'Escape' && !event.defaultPrevented) {
    if (topbarRef.value?.closeMenus()) {
      event.preventDefault()
      return
    }
    if (createModalOpen.value) {
      event.preventDefault()
      createModalOpen.value = false
      return
    }
    if (compareParentId.value) {
      event.preventDefault()
      compareParentId.value = null
      return
    }
    if (inspectorOpen.value) {
      event.preventDefault()
      closeInspector()
      return
    }
    if (focusMode.value) {
      event.preventDefault()
      focusMode.value = false
      return
    }
  }
  if (!editor.board.value) return
  if (compareParentId.value || createModalOpen.value) return
  const mod = event.metaKey || event.ctrlKey
  const key = event.key.toLowerCase()
  if (mod && key === 's') {
    event.preventDefault()
    if ((editor.dirty.value || editor.saving.value || editor.saveUnconfirmed.value) && !editor.busy.value && !editor.conflict.value) {
      void handleSave()
    }
    return
  }
  if ((mod && key === 'f') || (!mod && event.key === '/' && !isEditableTarget(event.target))) {
    event.preventDefault()
    topbarRef.value?.focusSearch()
    return
  }
  if (isEditableTarget(event.target)) return
  if (!mod && key === 'b' && activeBranchSourceId.value) {
    event.preventDefault()
    handleCreateDraft(activeBranchSourceId.value)
    return
  }
  if (mod && key === 'z' && !event.shiftKey) {
    event.preventDefault()
    if (editor.canUndo.value) editor.undo()
    return
  }
  if ((mod && key === 'z' && event.shiftKey) || (mod && key === 'y')) {
    event.preventDefault()
    if (editor.canRedo.value) editor.redo()
    return
  }
  if ((event.key === 'Delete' || event.key === 'Backspace') && selectedNodeId.value) {
    event.preventDefault()
    handleRemoveNode(selectedNodeId.value)
  }
}

function onResize() {
  const nextMobile = window.innerWidth <= 768
  if (nextMobile && !isMobile.value) {
    inspectorOpen.value = false
  }
  isMobile.value = nextMobile
}

function onBeforeUnload(event: BeforeUnloadEvent) {
  if (!editor.dirty.value && !editor.saving.value && !editor.saveUnconfirmed.value && !editor.conflict.value) return
  event.preventDefault()
  event.returnValue = ''
}

onBeforeRouteLeave(() => confirmDiscardIfDirty())

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  window.addEventListener('resize', onResize)
  window.addEventListener('beforeunload', onBeforeUnload)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  window.removeEventListener('resize', onResize)
  window.removeEventListener('beforeunload', onBeforeUnload)
  posterCache.value.dispose()
  thumbCache.value.dispose()
  durations.clear()
})
</script>
