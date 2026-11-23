import { computed, getCurrentScope, onScopeDispose, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useManualRefHistory } from '@vueuse/core'
import {
  creativeCanvasApi,
  getApiErrorMessage,
  getApiErrorStatus,
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

export const CANVAS_AUTOSAVE_DEBOUNCE_MS = 1500
export const CANVAS_AUTOSAVE_MAX_WAIT_MS = 10_000
export type CanvasSaveErrorKind = 'offline' | 'conflict' | 'error' | null

function isLikelyOfflineSaveError(error: unknown): boolean {
  const status = getApiErrorStatus(error)
  if (status >= 400 && status < 500) return false
  if (typeof navigator !== 'undefined' && navigator.onLine === false) return true
  if (!error || typeof error !== 'object') return false
  const err = error as { status?: unknown; code?: unknown; message?: unknown; response?: { status?: unknown } }
  if (status === 0 || status === 502 || status === 503 || status === 504) {
    const code = typeof err.code === 'string' ? err.code : ''
    const msg = typeof err.message === 'string' ? err.message : ''
    if (status > 0) return true
    if (
      code === 'ERR_NETWORK' ||
      code === 'ECONNABORTED' ||
      code === 'ETIMEDOUT' ||
      /network|offline|fetch|timeout|reset|离线|断网/i.test(msg)
    ) {
      return true
    }
  }
  return false
}

function isAmbiguousSaveError(error: unknown): boolean {
  const status = getApiErrorStatus(error)
  return !Number.isFinite(status) || status === 0 || status === 408 || status >= 500
}

function toSummary(canvas: CreativeCanvas): CanvasSummary {
  return {
    id: canvas.id,
    title: canvas.title,
    revision: canvas.revision,
    updated_at: canvas.updated_at,
  }
}

function contentPayloadJson(docValue: CanvasDocument): string {
  const sanitized = sanitizeDocument(docValue)
  return JSON.stringify({ nodes: sanitized.nodes, edges: sanitized.edges })
}

export function useCanvasEditor() {
  const router = useRouter()
  const boards = ref<CanvasSummary[]>([])
  const board = ref<CreativeCanvas | null>(null)
  const doc = ref<CanvasDocument>(emptyCanvasDocument())
  const title = ref('')
  const busy = ref(false)
  const saving = ref(false)
  const conflict = ref(false)
  const saveUnconfirmed = ref(false)
  const saveErrorKind = ref<CanvasSaveErrorKind>(null)
  const lastSavedAt = ref('')
  const hasSavedInSession = ref(false)
  const error = ref('')

  let debounceTimer: ReturnType<typeof setTimeout> | null = null
  let maxWaitTimer: ReturnType<typeof setTimeout> | null = null
  let followUpTimer: ReturnType<typeof setTimeout> | null = null
  let inFlightSave: Promise<boolean> | null = null
  let followUpAfterInFlight = false
  let syncingBaseline = false
  let boardLoadToken = 0

  const { commit, undo, redo, canUndo, canRedo, clear } = useManualRefHistory(doc, {
    clone: cloneCanvasDocument,
    capacity: 50,
  })

  const dirty = computed(() => isCanvasDirty(board.value, doc.value, title.value))
  const contentDirty = computed(() => {
    const current = board.value
    if (!current) return false
    if (title.value !== current.title) return true
    return contentPayloadJson(doc.value) !== JSON.stringify({
      nodes: current.document.nodes,
      edges: current.document.edges,
    })
  })
  const needsSync = computed(() => Boolean(board.value) && (dirty.value || saveUnconfirmed.value))
  const needsContentAutosave = computed(() =>
    Boolean(board.value) && (contentDirty.value || saveUnconfirmed.value),
  )
  const contentSignature = computed(() =>
    board.value ? `${title.value}\n${contentPayloadJson(doc.value)}` : '',
  )

  function clearAutosaveTimers(): void {
    if (debounceTimer !== null) {
      clearTimeout(debounceTimer)
      debounceTimer = null
    }
    if (maxWaitTimer !== null) {
      clearTimeout(maxWaitTimer)
      maxWaitTimer = null
    }
    if (followUpTimer !== null) {
      clearTimeout(followUpTimer)
      followUpTimer = null
    }
    followUpAfterInFlight = false
  }

  function resetHistoryBaseline(nextDoc: CanvasDocument): void {
    doc.value = nextDoc
    commit()
    clear()
  }

  function resetBoardState(): void {
    boardLoadToken += 1
    clearAutosaveTimers()
    inFlightSave = null
    syncingBaseline = true
    board.value = null
    title.value = ''
    conflict.value = false
    saveUnconfirmed.value = false
    saveErrorKind.value = null
    lastSavedAt.value = ''
    hasSavedInSession.value = false
    resetHistoryBaseline(emptyCanvasDocument())
    syncingBaseline = false
  }

  function clearAllForOwnerLoss(): void {
    boards.value = []
    resetBoardState()
    busy.value = false
    saving.value = false
    error.value = ''
    clearCanvasDraftHandoff()
  }

  const session = useCanvasSession(clearAllForOwnerLoss)
  const ops = useCanvasDocOps(board, doc, commit)

  function upsertSummary(canvas: CreativeCanvas): void {
    const summary = toSummary(canvas)
    const index = boards.value.findIndex(item => item.id === summary.id)
    if (index >= 0) {
      const next = [...boards.value]
      next[index] = summary
      boards.value = next
      return
    }
    boards.value = [summary, ...boards.value]
  }

  function applyLoadedBoard(loaded: CreativeCanvas): void {
    boardLoadToken += 1
    clearAutosaveTimers()
    inFlightSave = null
    syncingBaseline = true
    board.value = loaded
    title.value = loaded.title
    conflict.value = false
    saveUnconfirmed.value = false
    saveErrorKind.value = null
    lastSavedAt.value = loaded.updated_at || ''
    hasSavedInSession.value = false
    resetHistoryBaseline(cloneCanvasDocument(loaded.document))
    upsertSummary(loaded)
    syncingBaseline = false
  }

  async function open(id?: string): Promise<void> {
    boardLoadToken += 1
    clearAutosaveTimers()
    inFlightSave = null
    saving.value = false
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
    boardLoadToken += 1
    clearAutosaveTimers()
    inFlightSave = null
    saving.value = false
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

  async function executeSingleSave(loadToken: number): Promise<boolean> {
    if (loadToken !== boardLoadToken) return false
    const current = board.value
    if (!current || conflict.value) return false
    if (!isCanvasDirty(current, doc.value, title.value) && !saveUnconfirmed.value) {
      saveErrorKind.value = null
      return true
    }
    const run = session.startRun()
    if (!run) {
      clearAllForOwnerLoss()
      error.value = '请先登录创作画布。'
      return false
    }
    const wasUnconfirmed = saveUnconfirmed.value
    saving.value = true
    error.value = ''
    saveErrorKind.value = null
    const sentDoc = sanitizeDocument(doc.value)
    const sentDocJson = JSON.stringify(sentDoc)
    const sentTitle = title.value
    const normalizedSentTitle = sentTitle.trim().slice(0, MAX_BOARD_TITLE_LENGTH) || '未命名画布'
    try {
      const saved = await creativeCanvasApi.save(
        current.id,
        { revision: current.revision, title: sentTitle, document: sentDoc },
        run.signal,
      )
      if (loadToken !== boardLoadToken || !session.isCurrent(run) || board.value?.id !== current.id) {
        return false
      }
      syncingBaseline = true
      board.value = saved
      conflict.value = false
      saveUnconfirmed.value = false
      saveErrorKind.value = null
      lastSavedAt.value = saved.updated_at || lastSavedAt.value
      hasSavedInSession.value = true
      if (title.value === sentTitle) title.value = saved.title
      if (
        JSON.stringify(sanitizeDocument(doc.value)) === sentDocJson &&
        JSON.stringify(saved.document) !== sentDocJson
      ) {
        doc.value = cloneCanvasDocument(saved.document)
      }
      upsertSummary(saved)
      syncingBaseline = false
      return true
    } catch (err) {
      if (loadToken !== boardLoadToken || !session.isCurrent(run) || isAbortError(err)) {
        return false
      }
      if (isCanvasConflictError(err)) {
        // When retrying after an earlier unconfirmed write, read-only GET verifies whether
        // the server's newer revision already matches our exact current content (never overwrites).
        if (wasUnconfirmed) {
          try {
            const remote = await creativeCanvasApi.get(current.id, run.signal)
            if (loadToken !== boardLoadToken || !session.isCurrent(run) || board.value?.id !== current.id) {
              return false
            }
            const remoteMatchesSent =
              remote.revision > current.revision &&
              remote.title === normalizedSentTitle &&
              JSON.stringify(sanitizeDocument(remote.document)) === sentDocJson
            const localUnchangedDuringFlight =
              title.value === sentTitle &&
              JSON.stringify(sanitizeDocument(doc.value)) === sentDocJson
            if (remoteMatchesSent && localUnchangedDuringFlight) {
              syncingBaseline = true
              board.value = remote
              conflict.value = false
              saveUnconfirmed.value = false
              saveErrorKind.value = null
              error.value = ''
              lastSavedAt.value = remote.updated_at || lastSavedAt.value
              hasSavedInSession.value = true
              title.value = remote.title
              if (JSON.stringify(remote.document) !== sentDocJson) {
                doc.value = cloneCanvasDocument(remote.document)
              }
              upsertSummary(remote)
              syncingBaseline = false
              return true
            }
          } catch (reconcileErr) {
            if (loadToken !== boardLoadToken || !session.isCurrent(run) || isAbortError(reconcileErr)) {
              return false
            }
            // Fall through to explicit conflict state: original revision is already known stale.
          }
        }
        conflict.value = true
        saveUnconfirmed.value = false
        saveErrorKind.value = 'conflict'
        clearAutosaveTimers()
        error.value = '此画布在另一处更新 · 本次修改已保留'
        return false
      }
      if (isAmbiguousSaveError(err)) {
        saveUnconfirmed.value = true
      }
      if (isLikelyOfflineSaveError(err)) {
        saveErrorKind.value = 'offline'
        clearAutosaveTimers()
        error.value = saveUnconfirmed.value
          ? '连接中断或离线 · 服务端是否已保存先前改动尚未确认，网络恢复后请重试核验。'
          : '离线 · 尚未同步到服务器，网络恢复后可重试保存。'
        return false
      }
      saveErrorKind.value = 'error'
      clearAutosaveTimers()
      error.value = saveUnconfirmed.value
        ? '保存结果未确认 · 服务端可能已写入先前改动，请重试核验或重新加载最新画布。'
        : getApiErrorMessage(err, '保存画布失败，请稍后重试。')
      return false
    } finally {
      syncingBaseline = false
    }
  }

  function reconcileAfterSave(loadToken: number, boardId: string, succeeded: boolean): void {
    if (loadToken !== boardLoadToken) return
    if (!succeeded || !board.value || board.value.id !== boardId) return
    if (conflict.value || saveErrorKind.value) return
    const hidden = typeof document !== 'undefined' && document.visibilityState === 'hidden'
    if (hidden) {
      clearAutosaveTimers()
      if (needsSync.value && !inFlightSave) void save()
      return
    }
    if (!needsContentAutosave.value) {
      clearAutosaveTimers()
      return
    }
    if (followUpAfterInFlight) {
      clearAutosaveTimers()
      followUpTimer = setTimeout(() => {
        followUpTimer = null
        if (
          loadToken === boardLoadToken &&
          board.value?.id === boardId &&
          needsContentAutosave.value &&
          !conflict.value &&
          !saveErrorKind.value &&
          !inFlightSave
        ) {
          void save()
        }
      }, 40)
      return
    }
    // Re-arm autosave whenever local content edits remain dirty against the updated server baseline,
    // even if an earlier edit reverted to the pre-flight baseline and cleared timers mid-flight.
    if (debounceTimer === null && maxWaitTimer === null && followUpTimer === null) {
      scheduleAutosave()
    }
  }

  async function save(): Promise<boolean> {
    clearAutosaveTimers()
    if (!board.value) {
      if (session.currentUserId() === null) {
        clearAllForOwnerLoss()
        error.value = '请先登录创作画布。'
      }
      return false
    }
    if (conflict.value) return false
    const startToken = boardLoadToken
    while (inFlightSave) {
      const prevOk = await inFlightSave
      if (startToken !== boardLoadToken || !board.value || conflict.value) return false
      if (!prevOk) return false
      if (!needsSync.value && !inFlightSave) return true
    }
    if (startToken !== boardLoadToken || !board.value || conflict.value) return false
    if (!needsSync.value) {
      saveErrorKind.value = null
      error.value = ''
      return true
    }
    clearAutosaveTimers()
    const loadToken = boardLoadToken
    const boardId = board.value.id
    let promise: Promise<boolean> | null = null
    promise = (async () => {
      let singleOk = false
      try {
        singleOk = await executeSingleSave(loadToken)
        return singleOk
      } finally {
        if (inFlightSave === promise) inFlightSave = null
        if (loadToken === boardLoadToken) saving.value = false
        reconcileAfterSave(loadToken, boardId, singleOk)
      }
    })()
    inFlightSave = promise
    return promise
  }

  function triggerAutosaveTick(): void {
    if (debounceTimer !== null) {
      clearTimeout(debounceTimer)
      debounceTimer = null
    }
    if (maxWaitTimer !== null) {
      clearTimeout(maxWaitTimer)
      maxWaitTimer = null
    }
    if (!board.value || !needsContentAutosave.value || conflict.value) return
    if (inFlightSave) {
      followUpAfterInFlight = true
      return
    }
    void save()
  }

  function scheduleAutosave(): void {
    if (!board.value || !needsContentAutosave.value || conflict.value) {
      clearAutosaveTimers()
      return
    }
    if (debounceTimer !== null) clearTimeout(debounceTimer)
    debounceTimer = setTimeout(triggerAutosaveTick, CANVAS_AUTOSAVE_DEBOUNCE_MS)
    if (maxWaitTimer === null) {
      maxWaitTimer = setTimeout(triggerAutosaveTick, CANVAS_AUTOSAVE_MAX_WAIT_MS)
    }
  }

  watch(contentSignature, () => {
    if (syncingBaseline || !board.value) return
    if (conflict.value) return
    if (!needsContentAutosave.value) {
      clearAutosaveTimers()
      if (!needsSync.value) {
        saveErrorKind.value = null
        error.value = ''
      }
      return
    }
    if (saveErrorKind.value) {
      if (typeof navigator !== 'undefined' && navigator.onLine === false && saveErrorKind.value === 'offline') {
        clearAutosaveTimers()
        return
      }
      saveErrorKind.value = null
      error.value = ''
    }
    scheduleAutosave()
  })

  function onVisibilityChange(): void {
    if (typeof document === 'undefined' || document.visibilityState !== 'hidden') return
    if (!board.value || conflict.value) return
    if (inFlightSave) {
      followUpAfterInFlight = true
      return
    }
    if (!needsSync.value || saveErrorKind.value) return
    void save()
  }

  function onBrowserOnline(): void {
    if (!board.value || !needsContentAutosave.value || conflict.value || inFlightSave) return
    if (saveErrorKind.value === 'offline' || saveUnconfirmed.value) {
      saveErrorKind.value = null
      error.value = ''
      scheduleAutosave()
    }
  }

  if (typeof window !== 'undefined' && typeof document !== 'undefined' && getCurrentScope()) {
    document.addEventListener('visibilitychange', onVisibilityChange)
    window.addEventListener('online', onBrowserOnline)
    onScopeDispose(() => {
      clearAutosaveTimers()
      document.removeEventListener('visibilitychange', onVisibilityChange)
      window.removeEventListener('online', onBrowserOnline)
    })
  } else if (getCurrentScope()) {
    onScopeDispose(() => {
      clearAutosaveTimers()
    })
  }

  async function removeBoard(): Promise<boolean> {
    boardLoadToken += 1
    clearAutosaveTimers()
    inFlightSave = null
    saving.value = false
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
    saving,
    saveUnconfirmed,
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
    contentDirty,
    busy,
    saving,
    conflict,
    saveUnconfirmed,
    saveErrorKind,
    lastSavedAt,
    hasSavedInSession,
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
