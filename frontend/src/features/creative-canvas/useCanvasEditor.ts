import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useManualRefHistory } from '@vueuse/core'
import {
  creativeCanvasApi,
  getApiErrorMessage,
  isCanvasConflictError,
} from './api'
import {
  cloneCanvasDocument,
  emptyCanvasDocument,
  isCanvasUuid,
  MAX_BOARD_TITLE_LENGTH,
  sanitizeDocument,
} from './document'
import { clearCanvasDraftHandoff } from './handoff'
import type { CanvasDocument, CanvasSummary, CreativeCanvas } from './types'
import { isCanvasDirty, useCanvasDocOps } from './useCanvasDocOps'
import { useCanvasHandoff } from './useCanvasHandoff'
import { isAbortError, useCanvasSession } from './useCanvasSession'

function toSummary(canvas: CreativeCanvas): CanvasSummary {
  return {
    id: canvas.id,
    title: canvas.title,
    revision: canvas.revision,
    updated_at: canvas.updated_at,
  }
}

export function useCanvasEditor() {
  const router = useRouter()
  const boards = ref<CanvasSummary[]>([])
  const board = ref<CreativeCanvas | null>(null)
  const doc = ref<CanvasDocument>(emptyCanvasDocument())
  const title = ref('')
  const busy = ref(false)
  const conflict = ref(false)
  const error = ref('')

  const { commit, undo, redo, canUndo, canRedo, clear } = useManualRefHistory(doc, {
    clone: cloneCanvasDocument,
    capacity: 50,
  })

  const dirty = computed(() => isCanvasDirty(board.value, doc.value, title.value))

  function resetHistoryBaseline(nextDoc: CanvasDocument): void {
    doc.value = nextDoc
    commit()
    clear()
  }

  function resetBoardState(): void {
    board.value = null
    title.value = ''
    conflict.value = false
    resetHistoryBaseline(emptyCanvasDocument())
  }

  function clearAllForOwnerLoss(): void {
    boards.value = []
    resetBoardState()
    busy.value = false
    error.value = ''
    clearCanvasDraftHandoff()
  }

  const session = useCanvasSession(clearAllForOwnerLoss)
  const ops = useCanvasDocOps(board, doc, commit)

  function upsertSummary(canvas: CreativeCanvas): void {
    const summary = toSummary(canvas)
    boards.value = [summary, ...boards.value.filter(item => item.id !== summary.id)]
  }

  function applyLoadedBoard(loaded: CreativeCanvas): void {
    board.value = loaded
    title.value = loaded.title
    conflict.value = false
    resetHistoryBaseline(cloneCanvasDocument(loaded.document))
    upsertSummary(loaded)
  }

  async function open(id?: string): Promise<void> {
    const run = session.startRun()
    if (!run) {
      clearAllForOwnerLoss()
      error.value = '请先登录创作画布。'
      return
    }
    busy.value = true
    error.value = ''
    try {
      if (!id) {
        const items = await creativeCanvasApi.list(run.signal)
        if (!session.isCurrent(run)) return
        boards.value = items
        resetBoardState()
        return
      }
      if (!isCanvasUuid(id)) {
        resetBoardState()
        error.value = '无效的画布编号。'
        return
      }
      const listPromise = boards.value.length
        ? Promise.resolve(boards.value)
        : creativeCanvasApi.list(run.signal).catch(() => boards.value)
      const [loaded, items] = await Promise.all([
        creativeCanvasApi.get(id, run.signal),
        listPromise,
      ])
      if (!session.isCurrent(run)) return
      boards.value = items
      applyLoadedBoard(loaded)
    } catch (err) {
      if (!session.isCurrent(run) || isAbortError(err)) return
      error.value = getApiErrorMessage(err, '加载画布失败，请稍后重试。')
    } finally {
      if (session.isCurrent(run)) busy.value = false
    }
  }

  async function create(boardTitle: string): Promise<CreativeCanvas | null> {
    const run = session.startRun()
    if (!run) {
      clearAllForOwnerLoss()
      error.value = '请先登录创作画布。'
      return null
    }
    busy.value = true
    error.value = ''
    try {
      const created = await creativeCanvasApi.create(boardTitle, run.signal)
      if (!session.isCurrent(run)) return null
      applyLoadedBoard(created)
      return created
    } catch (err) {
      if (!session.isCurrent(run) || isAbortError(err)) return null
      error.value = getApiErrorMessage(err, '新建画布失败，请稍后重试。')
      return null
    } finally {
      if (session.isCurrent(run)) busy.value = false
    }
  }

  async function save(): Promise<boolean> {
    const current = board.value
    const run = session.startRun()
    if (!run || !current) {
      if (!run) {
        clearAllForOwnerLoss()
        error.value = '请先登录创作画布。'
      }
      return false
    }
    busy.value = true
    error.value = ''
    const sentDoc = sanitizeDocument(doc.value)
    const sentDocJson = JSON.stringify(sentDoc)
    const sentTitle = title.value
    try {
      const saved = await creativeCanvasApi.save(
        current.id,
        { revision: current.revision, title: sentTitle, document: sentDoc },
        run.signal,
      )
      if (!session.isCurrent(run) || board.value?.id !== current.id) return false
      board.value = saved
      conflict.value = false
      if (title.value === sentTitle) title.value = saved.title
      if (
        JSON.stringify(sanitizeDocument(doc.value)) === sentDocJson &&
        JSON.stringify(saved.document) !== sentDocJson
      ) {
        doc.value = cloneCanvasDocument(saved.document)
      }
      upsertSummary(saved)
      return true
    } catch (err) {
      if (!session.isCurrent(run) || isAbortError(err)) return false
      if (isCanvasConflictError(err)) {
        conflict.value = true
        error.value = '画布已在其他标签页更新，已保留本地修改。'
        return false
      }
      error.value = getApiErrorMessage(err, '保存画布失败，请稍后重试。')
      return false
    } finally {
      if (session.isCurrent(run)) busy.value = false
    }
  }

  async function removeBoard(): Promise<boolean> {
    const current = board.value
    const run = session.startRun()
    if (!run || !current) {
      if (!run) {
        clearAllForOwnerLoss()
        error.value = '请先登录创作画布。'
      }
      return false
    }
    busy.value = true
    error.value = ''
    try {
      await creativeCanvasApi.remove(current.id, current.revision, run.signal)
      if (!session.isCurrent(run)) return false
      boards.value = boards.value.filter(item => item.id !== current.id)
      resetBoardState()
      return true
    } catch (err) {
      if (!session.isCurrent(run) || isAbortError(err)) return false
      if (isCanvasConflictError(err)) {
        conflict.value = true
        error.value = '画布版本已变化，请刷新后再删除。'
        return false
      }
      error.value = getApiErrorMessage(err, '删除画布失败，请稍后重试。')
      return false
    } finally {
      if (session.isCurrent(run)) busy.value = false
    }
  }

  function rename(nextTitle: string): void {
    if (!board.value) return
    title.value = nextTitle.slice(0, MAX_BOARD_TITLE_LENGTH)
  }

  const { toStudio } = useCanvasHandoff({
    board,
    doc,
    dirty,
    conflict,
    error,
    currentUserId: session.currentUserId,
    save,
    router,
  })

  return {
    boards,
    board,
    doc,
    title,
    dirty,
    busy,
    conflict,
    error,
    canUndo,
    canRedo,
    open,
    create,
    save,
    removeBoard,
    rename,
    commit,
    undo,
    redo,
    toStudio,
    ...ops,
  }
}
