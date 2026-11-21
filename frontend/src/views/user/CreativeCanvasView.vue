<template>
  <AppLayout>
    <section class="cc-workspace" aria-label="无限画布工作区">
      <BoardTopbar
        :boards="editor.boards.value"
        :board="editor.board.value"
        :title="editor.title.value"
        :dirty="editor.dirty.value"
        :busy="editor.busy.value"
        :conflict="editor.conflict.value"
        :can-undo="editor.canUndo.value"
        :can-redo="editor.canRedo.value"
        :can-create-draft="Boolean(activeImageId)"
        :can-compare="Boolean(compareTargetImageId)"
        @select-board="handleSelectBoard"
        @open-create="createModalOpen = true"
        @rename="editor.rename"
        @add-note="handleAddNote"
        @create-draft="handleCreateDraftFromTopbar"
        @compare="handleCompareFromTopbar"
        @undo="editor.undo()"
        @redo="editor.redo()"
        @save="handleSave"
        @delete-board="handleDeleteBoard"
      />

      <div v-if="editor.conflict.value" class="cc-banner" role="alert">
        <span>该画布已在其他标签页更新，请重新加载最新版本后再继续保存。</span>
        <button type="button" class="cc-btn is-sm" :disabled="editor.busy.value" @click="handleReloadBoard">
          重新加载最新画布
        </button>
      </div>

      <div v-else-if="editor.error.value" class="cc-banner is-error" role="alert">
        <span>{{ editor.error.value }}</span>
        <button type="button" class="cc-btn is-sm" :disabled="editor.busy.value" @click="handleRetry">
          重试
        </button>
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
        @update-title="handleUpdateTitle"
        @update-prompt="handleUpdatePrompt"
        @commit-edit="editor.commit()"
        @change-viewport="editor.changeViewport"
      >
        <template #overlay>
          <CanvasInspector
            v-if="inspectorOpen && selectedNode"
            :node="selectedNode"
            :asset="editor.board.value.assets[selectedNode.id]"
            :source-node="selectedSourceNode"
            :branch-count="selectedBranchCount"
            :takes="selectedTakes"
            :durations="durations"
            @close="inspectorOpen = false"
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
            @click="inspectorOpen = true"
          >
            属性 · {{ selectedNode.title || '已选节点' }}
          </button>
        </template>
      </BoardStage>
    </section>

    <BranchCompareModal
      :open="Boolean(compareImageNode)"
      :heading="compareImageNode?.title || '图片原图'"
      :source-thumb-url="compareSourceThumbUrl"
      :branches="compareEntries"
      :poster-cache="posterCache"
      :poster-revision="posterRevision"
      :durations="durations"
      @close="compareImageId = null"
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
      :jobs="boardVideoJobs"
      @expired="handleJobExpired"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAuthStore } from '@/stores/auth'
import type { VideoJob } from '@/features/video-studio/api'
import VideoDelivery from '@/features/video-studio/VideoDelivery.vue'
import { createWorkspaceDurationStore } from '@/features/video-studio/mediaDuration'
import { createVideoPosterCache } from '@/features/video-studio/posterCache'
import '@/features/creative-canvas/canvas.css'
import BoardCreateModal from '@/features/creative-canvas/BoardCreateModal.vue'
import BoardEmptyState from '@/features/creative-canvas/BoardEmptyState.vue'
import BoardStage from '@/features/creative-canvas/BoardStage.vue'
import BoardTopbar from '@/features/creative-canvas/BoardTopbar.vue'
import BranchCompareModal from '@/features/creative-canvas/BranchCompareModal.vue'
import CanvasInspector from '@/features/creative-canvas/CanvasInspector.vue'
import { findDerivedDraftNodes, findSourceImageNode } from '@/features/creative-canvas/document'
import { createCanvasImageThumbCache } from '@/features/creative-canvas/imageThumbCache'
import {
  buildCompareEntries,
  buildTakesForDraft,
  collectBoardVideoJobs,
  resolveCompareImageId,
} from '@/features/creative-canvas/screenModel'
import { useCanvasEditor } from '@/features/creative-canvas/useCanvasEditor'

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const editor = useCanvasEditor()

const posterCache = shallowRef(createVideoPosterCache())
const durations = createWorkspaceDurationStore()
const thumbCache = shallowRef(createCanvasImageThumbCache())
const posterRevision = ref(0)

const stageRef = ref<InstanceType<typeof BoardStage>>()
const deliveryRef = ref<InstanceType<typeof VideoDelivery>>()
const selectedNodeId = ref<string | null>(null)
const inspectorOpen = ref(false)
const compareImageId = ref<string | null>(null)
const createModalOpen = ref(false)
const isMobile = ref(typeof window !== 'undefined' ? window.innerWidth <= 768 : false)

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
const selectedBranchCount = computed(() =>
  selectedNode.value?.kind === 'image'
    ? findDerivedDraftNodes(editor.doc.value, selectedNode.value.id).length
    : 0,
)
const selectedTakes = computed(() =>
  selectedNode.value?.kind === 'video-draft'
    ? buildTakesForDraft(selectedNode.value, editor.board.value?.attempts ?? [])
    : [],
)

const activeImageId = computed(() => {
  const node = selectedNode.value
  if (node?.kind === 'image') {
    return editor.board.value?.assets[node.id]?.available === false ? null : node.id
  }
  if (node?.kind === 'video-draft' && selectedSourceNode.value) {
    return editor.board.value?.assets[selectedSourceNode.value.id]?.available === false
      ? null
      : selectedSourceNode.value.id
  }
  return null
})

const compareTargetImageId = computed(() =>
  resolveCompareImageId(editor.doc.value, selectedNodeId.value),
)

const compareImageNode = computed(() =>
  compareImageId.value
    ? editor.doc.value.nodes.find(n => n.id === compareImageId.value && n.kind === 'image') ?? null
    : null,
)

const compareSourceThumbUrl = computed(() => {
  const img = compareImageNode.value
  if (!img) return ''
  if (editor.board.value?.assets[img.id]?.available === false) return ''
  const thumb = thumbCache.value.get(img.image)
  return thumb.status === 'ready' ? thumb.url : ''
})

const compareEntries = computed(() =>
  compareImageNode.value
    ? buildCompareEntries(editor.doc.value, compareImageNode.value.id, editor.board.value?.attempts ?? [])
    : [],
)

const boardVideoJobs = computed(() => collectBoardVideoJobs(editor.board.value))

function routeBoardId(): string | undefined {
  const raw = route.query.board
  const val = Array.isArray(raw) ? raw[0] : raw
  return typeof val === 'string' && val.trim() ? val.trim() : undefined
}

function confirmDiscardIfDirty(message = '当前画布有未保存的修改，继续操作将丢失这些更改。确定要继续吗？'): boolean {
  if (!editor.dirty.value) return true
  return window.confirm(message)
}

watch(
  () => [authStore.user?.id, routeBoardId()] as const,
  ([userId, boardId], previous) => {
    if (previous && previous[0] !== userId) {
      posterCache.value.dispose()
      thumbCache.value.dispose()
      posterCache.value = createVideoPosterCache()
      thumbCache.value = createCanvasImageThumbCache()
      durations.clear()
    }
    if (!userId) return
    selectedNodeId.value = null
    inspectorOpen.value = false
    compareImageId.value = null
    void editor.open(boardId)
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
    if (compareImageId.value && !nodes.some(n => n.id === compareImageId.value)) {
      compareImageId.value = null
    }
  },
)

function handleSelectNode(id: string | null) {
  selectedNodeId.value = id
  if (!id) {
    inspectorOpen.value = false
    return
  }
  if (!isMobile.value) {
    inspectorOpen.value = true
  }
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
  const at = stageRef.value?.getSuggestedPosition()
  const id = editor.addNote(at)
  if (id) handleSelectNode(id)
}

function handleCreateDraft(imageNodeId: string) {
  const id = editor.newDraft(imageNodeId)
  if (id) handleSelectNode(id)
}

function handleNewDraftAt(imageNodeId: string, at: { x: number; y: number }) {
  const id = editor.newDraft(imageNodeId, at)
  if (id) handleSelectNode(id)
}

function handleCreateDraftFromTopbar() {
  if (!activeImageId.value) return
  handleCreateDraft(activeImageId.value)
}

function openCompareModal(imageNodeId: string) {
  compareImageId.value = imageNodeId
}

function handleCompareFromTopbar() {
  if (!compareTargetImageId.value) return
  openCompareModal(compareTargetImageId.value)
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
  compareImageId.value = null
  await editor.toStudio(draftNodeId)
}

function handlePreviewJob(job: VideoJob, trigger: HTMLElement) {
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
  if (event.key === 'Escape' && !event.defaultPrevented) {
    if (createModalOpen.value) {
      event.preventDefault()
      createModalOpen.value = false
      return
    }
    if (compareImageId.value) {
      event.preventDefault()
      compareImageId.value = null
      return
    }
    if (inspectorOpen.value) {
      event.preventDefault()
      inspectorOpen.value = false
      return
    }
  }
  if (!editor.board.value) return
  if (compareImageId.value || createModalOpen.value) return
  const mod = event.metaKey || event.ctrlKey
  const key = event.key.toLowerCase()
  if (mod && key === 's') {
    event.preventDefault()
    if (editor.dirty.value && !editor.busy.value) void handleSave()
    return
  }
  if (isEditableTarget(event.target)) return
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
  if (!editor.dirty.value) return
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
