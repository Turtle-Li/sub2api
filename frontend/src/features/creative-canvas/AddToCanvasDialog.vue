<template>
  <dialog
    v-if="open"
    ref="dialogRef"
    class="cc-modal-backdrop cc-add-dialog"
    role="dialog"
    aria-modal="true"
    aria-labelledby="cc-add-dialog-title"
    @cancel="onDialogCancel"
    @close="onDialogClose"
    @keydown="onDialogKeydown"
    @click.self="close"
  >
    <div ref="cardRef" class="cc-modal-card" style="width: min(460px, 100%)" tabindex="-1">
      <div class="cc-modal-head">
        <h3 id="cc-add-dialog-title">加入创作画布</h3>
        <button ref="closeBtnRef" type="button" class="cc-btn is-sm" :disabled="submitting" @click="close">关闭</button>
      </div>

      <p class="cc-meta">
        {{ payload?.kind === 'video' ? '将此已完成视频的引用加入画布（仅保存任务 ID，不复制视频文件）。' : '将此生图原图的授权定位加入画布，可在画布中派生多条视频草稿分支。' }}
      </p>

      <div v-if="loading" class="cc-meta" role="status">正在读取你的画布列表…</div>

      <template v-else>
        <div v-if="boards.length" class="cc-dialog-list" role="radiogroup" aria-label="选择目标画布">
          <button
            v-for="board in boards"
            :key="board.id"
            type="button"
            class="cc-dialog-option"
            :class="{ 'is-selected': mode === 'existing' && selectedId === board.id }"
            @click="selectExisting(board.id)"
          >
            <span><strong>{{ board.title }}</strong></span>
            <span class="cc-meta">v{{ board.revision }}</span>
          </button>
        </div>

        <button
          type="button"
          class="cc-dialog-option"
          :class="{ 'is-selected': mode === 'new' }"
          @click="mode = 'new'"
        >
          <span><strong>+ 新建画布并加入</strong></span>
          <span class="cc-meta">创建新画布</span>
        </button>

        <label v-if="mode === 'new'" class="cc-kv" style="display: grid; gap: 5px">
          <b>新画布名称</b>
          <input
            v-model="newTitle"
            class="cc-note-input"
            type="text"
            maxlength="100"
            placeholder="例如：海岸灯塔分镜草稿"
            @keydown.enter.prevent="confirm"
          />
        </label>
      </template>

      <p v-if="error" class="cc-meta" style="color: var(--cc-error)" role="alert">{{ error }}</p>

      <div v-if="doneBoardId" class="cc-banner" role="status">
        <span>已成功加入画布「{{ doneBoardTitle }}」。</span>
        <div class="cc-node-foot">
          <button type="button" class="cc-btn is-primary is-sm" @click="openBoard">打开画布</button>
          <button type="button" class="cc-btn is-sm" @click="close">留在当前页</button>
        </div>
      </div>

      <div v-else class="cc-node-foot" style="justify-content: flex-end">
        <button type="button" class="cc-btn" :disabled="submitting" @click="close">取消</button>
        <button
          type="button"
          class="cc-btn is-primary"
          :disabled="loading || submitting || !canSubmit"
          @click="confirm"
        >
          {{ submitting ? '正在加入…' : mode === 'new' ? '新建并加入画布' : '加入所选画布' }}
        </button>
      </div>
    </div>
  </dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { creativeCanvasApi, getApiErrorMessage, isCanvasConflictError } from './api'
import { createCanvasUuid, MAX_BOARD_TITLE_LENGTH, MAX_CANVAS_NODES, MAX_NODE_TITLE_LENGTH } from './document'
import { placeNewCanvasNode } from './upstreamAdapter'
import type { AddToCanvasPayload, CanvasNodeDoc, CanvasSummary } from './types'
import './canvas.css'

const props = defineProps<{
  open: boolean
  payload: AddToCanvasPayload | null
}>()

const emit = defineEmits<{
  close: []
  added: [boardId: string, nodeId: string]
}>()

const router = useRouter()
const loading = ref(false)
const submitting = ref(false)
const error = ref('')
const boards = ref<CanvasSummary[]>([])
const mode = ref<'existing' | 'new'>('existing')
const selectedId = ref('')
const newTitle = ref('')
const doneBoardId = ref('')
const doneNodeId = ref('')
const doneBoardTitle = ref('')
const dialogRef = ref<HTMLDialogElement>()
const cardRef = ref<HTMLElement>()
const closeBtnRef = ref<HTMLButtonElement>()
let controller: AbortController | undefined
let previouslyFocused: HTMLElement | null = null
let skipFocusRestore = false
let openCycle = 0
let closeEmittedForCycle = false
const programmaticallyClosedDialogs = new WeakSet<HTMLDialogElement>()

function openNativeDialog(el: HTMLDialogElement | undefined) {
  if (!el) return
  programmaticallyClosedDialogs.delete(el)
  if (el.open || el.hasAttribute('open')) return
  if (el.isConnected && typeof el.showModal === 'function') {
    try {
      el.showModal()
      return
    } catch {
      if (el.open) return
    }
  }
  el.setAttribute('open', '')
}

function closeNativeDialog(el: HTMLDialogElement | undefined) {
  if (!el) return
  const isNativeOpen = el.open || el.hasAttribute('open')
  if (!isNativeOpen) return
  programmaticallyClosedDialogs.add(el)
  if (typeof el.close === 'function') {
    try {
      el.close()
      return
    } catch {
      // Fallback for partial/mocked dialog implementations in test DOMs.
    }
  }
  el.removeAttribute('open')
}

function isTopmostDialog(): boolean {
  const el = dialogRef.value
  if (!el || typeof document === 'undefined') return true
  if (el.contains(document.activeElement)) return true
  const dialogs = Array.from(document.querySelectorAll('dialog[open], [role="dialog"]'))
  return dialogs.length === 0 || dialogs[dialogs.length - 1] === el
}

function onDialogKeydown(event: KeyboardEvent) {
  if (!props.open || event.key !== 'Escape' || event.defaultPrevented) return
  event.preventDefault()
  event.stopPropagation()
  close()
}

function onDialogCancel(event: Event) {
  if (event.target !== dialogRef.value) return
  event.preventDefault()
  close()
}

function onDialogClose(event: Event) {
  const el = dialogRef.value
  if (!el || event.target !== el) return
  if (programmaticallyClosedDialogs.has(el)) {
    programmaticallyClosedDialogs.delete(el)
    return
  }
  if (!props.open || closeEmittedForCycle || submitting.value) return
  close()
}

function onKeydown(event: KeyboardEvent) {
  if (!props.open || event.key !== 'Escape' || event.defaultPrevented) return
  if (!isTopmostDialog()) return
  event.preventDefault()
  event.stopPropagation()
  close()
}

function restoreFocus() {
  const target = previouslyFocused
  previouslyFocused = null
  if (skipFocusRestore) {
    skipFocusRestore = false
    return
  }
  if (target && target.isConnected) {
    target.focus()
  }
}

const canSubmit = computed(() => {
  if (!props.payload) return false
  if (mode.value === 'new') return newTitle.value.trim().length > 0
  return selectedId.value.length > 0
})

function selectExisting(id: string) {
  mode.value = 'existing'
  selectedId.value = id
}

function resetState() {
  controller?.abort()
  controller = undefined
  error.value = ''
  doneBoardId.value = ''
  doneNodeId.value = ''
  doneBoardTitle.value = ''
  submitting.value = false
}

async function loadBoards() {
  resetState()
  loading.value = true
  controller = new AbortController()
  try {
    const items = await creativeCanvasApi.list(controller.signal)
    boards.value = items
    if (items.length > 0) {
      mode.value = 'existing'
      selectedId.value = items[0].id
    } else {
      mode.value = 'new'
      selectedId.value = ''
    }
    const defaultLabel = props.payload?.title?.trim().slice(0, 32)
    newTitle.value = (defaultLabel ? `${defaultLabel} · 画布` : '新建创作画布').slice(0, MAX_BOARD_TITLE_LENGTH)
  } catch (err) {
    if (!controller?.signal.aborted) {
      error.value = getApiErrorMessage(err, '无法读取画布列表，请稍后重试。')
    }
  } finally {
    loading.value = false
  }
}

watch(
  () => props.open,
  async isOpen => {
    const cycle = ++openCycle
    if (isOpen) {
      closeEmittedForCycle = false
      skipFocusRestore = false
      if (typeof document !== 'undefined') {
        previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null
      }
      if (typeof window !== 'undefined') {
        window.addEventListener('keydown', onKeydown)
      }
      if (props.payload) {
        void loadBoards()
      }
      await nextTick()
      if (!props.open || cycle !== openCycle) return
      openNativeDialog(dialogRef.value)
      ;(closeBtnRef.value ?? cardRef.value)?.focus()
    } else {
      if (typeof window !== 'undefined') {
        window.removeEventListener('keydown', onKeydown)
      }
      closeNativeDialog(dialogRef.value)
      resetState()
      restoreFocus()
    }
  },
  { immediate: true },
)

onMounted(() => {
  if (props.open && dialogRef.value) {
    openNativeDialog(dialogRef.value)
  }
})

onBeforeUnmount(() => {
  openCycle += 1
  controller?.abort()
  if (typeof window !== 'undefined') {
    window.removeEventListener('keydown', onKeydown)
  }
  closeNativeDialog(dialogRef.value)
  restoreFocus()
})

function buildNode(existingNodes: readonly CanvasNodeDoc[]): CanvasNodeDoc {
  const payload = props.payload!
  const id = createCanvasUuid()
  const pos = placeNewCanvasNode({
    existingNodes,
    kind: payload.kind,
  })
  if (payload.kind === 'image') {
    return {
      id,
      kind: 'image',
      x: pos.x,
      y: pos.y,
      title: payload.title?.trim().slice(0, MAX_NODE_TITLE_LENGTH) || `生图作品 · 第 ${payload.original.index + 1} 张`,
      image: {
        route: payload.original.route,
        batchId: payload.original.batchId,
        itemId: payload.original.itemId,
        index: payload.original.index,
      },
    }
  }
  return {
    id,
    kind: 'video',
    x: pos.x,
    y: pos.y,
    title: payload.title?.trim().slice(0, MAX_NODE_TITLE_LENGTH) || '视频作品',
    video_id: payload.videoId,
  }
}

async function confirm() {
  if (!props.payload || submitting.value || !canSubmit.value) return
  submitting.value = true
  error.value = ''
  controller?.abort()
  controller = new AbortController()
  try {
    const target =
      mode.value === 'new'
        ? await creativeCanvasApi.create(newTitle.value.trim(), controller.signal)
        : await creativeCanvasApi.get(selectedId.value, controller.signal)

    if (target.document.nodes.length >= MAX_CANVAS_NODES) {
      error.value = `该画布节点数量已达上限（${MAX_CANVAS_NODES}），请新建画布。`
      return
    }

    const newNode = buildNode(target.document.nodes)
    const saved = await creativeCanvasApi.save(
      target.id,
      {
        revision: target.revision,
        title: target.title,
        document: {
          viewport: target.document.viewport,
          nodes: [...target.document.nodes, newNode],
          edges: [...target.document.edges],
        },
      },
      controller.signal,
    )
    doneBoardId.value = saved.id
    doneNodeId.value = newNode.id
    doneBoardTitle.value = saved.title
    emit('added', saved.id, newNode.id)
  } catch (err) {
    if (controller?.signal.aborted) return
    if (isCanvasConflictError(err)) {
      error.value = '目标画布刚刚在另一标签页被修改，请重新点击确认以获取最新修订号后再加入。'
    } else {
      error.value = getApiErrorMessage(err, '加入画布失败，请稍后重试。')
    }
  } finally {
    submitting.value = false
  }
}

async function openBoard() {
  if (!doneBoardId.value) return
  skipFocusRestore = true
  const query: Record<string, string> = { board: doneBoardId.value }
  if (doneNodeId.value) query.node = doneNodeId.value
  if (!closeEmittedForCycle) {
    closeEmittedForCycle = true
    emit('close')
  }
  await router.push({ name: 'CreativeCanvas', query })
}

function close() {
  if (submitting.value || closeEmittedForCycle) return
  closeEmittedForCycle = true
  emit('close')
}
</script>

<style scoped>
.cc-add-dialog {
  box-sizing: border-box;
  width: 100%;
  height: 100%;
  max-width: none;
  max-height: none;
  margin: 0;
  border: 0;
  color: var(--cc-ink);
  overflow: visible;
}

.cc-add-dialog:not([open]) {
  display: none;
}

.cc-add-dialog:modal {
  background: transparent;
  backdrop-filter: none;
}

.cc-add-dialog::backdrop {
  background: rgba(15, 28, 26, 0.48);
  backdrop-filter: blur(3px);
}

:global(.dark) .cc-add-dialog::backdrop {
  background: rgba(4, 10, 14, 0.68);
}
</style>
