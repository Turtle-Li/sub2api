import apiClient from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { isCanvasUuid, MAX_BOARD_TITLE_LENGTH, sanitizeDocument } from './document'
import type { CanvasDocument, CanvasSummary, CreativeCanvas } from './types'

const base = '/creative-canvases'

function sessionConfig(signal?: AbortSignal) {
  signal?.throwIfAborted()
  const auth = useAuthStore()
  if (!auth.isAuthenticated || !auth.user) {
    throw new DOMException('请先登录创作画布。', 'AbortError')
  }
  return { signal, sessionUserId: auth.user.id }
}

function canvasPath(id: string): string {
  if (!isCanvasUuid(id)) {
    throw new TypeError('无效的画布编号。')
  }
  return `${base}/${encodeURIComponent(id)}`
}

function normalizeCanvas(raw: CreativeCanvas): CreativeCanvas {
  return {
    id: raw.id,
    title: raw.title || '未命名画布',
    revision: Number.isFinite(raw.revision) ? raw.revision : 0,
    updated_at: raw.updated_at || '',
    document: sanitizeDocument(raw.document),
    assets: raw.assets && typeof raw.assets === 'object' ? raw.assets : {},
    attempts: Array.isArray(raw.attempts) ? raw.attempts : [],
  }
}

export interface ApiErrorShape {
  readonly status?: number
  readonly code?: string | number
  readonly reason?: string
  readonly message?: string
  readonly response?: {
    readonly status?: number
    readonly data?: { readonly message?: string; readonly code?: string | number }
  }
}

export function getApiErrorStatus(error: unknown): number {
  if (!error || typeof error !== 'object') return 0
  const err = error as ApiErrorShape
  return err.status || err.response?.status || 0
}

export function getApiErrorMessage(error: unknown, fallback = '请求失败，请稍后重试。'): string {
  if (error instanceof Error && error.message) return error.message
  if (error && typeof error === 'object') {
    const err = error as ApiErrorShape
    const msg = err.response?.data?.message || err.message
    if (typeof msg === 'string' && msg.trim()) return msg.trim()
  }
  return fallback
}

export function isCanvasConflictError(error: unknown): boolean {
  return getApiErrorStatus(error) === 409
}

export const creativeCanvasApi = {
  async list(signal?: AbortSignal): Promise<CanvasSummary[]> {
    const { data } = await apiClient.get<{ items: CanvasSummary[] }>(base, sessionConfig(signal))
    return Array.isArray(data?.items) ? data.items : []
  },

  async create(title: string, signal?: AbortSignal): Promise<CreativeCanvas> {
    const cleanTitle = title.trim().slice(0, MAX_BOARD_TITLE_LENGTH) || '未命名画布'
    const { data } = await apiClient.post<CreativeCanvas>(
      base,
      { title: cleanTitle },
      sessionConfig(signal),
    )
    return normalizeCanvas(data)
  },

  async get(id: string, signal?: AbortSignal): Promise<CreativeCanvas> {
    const { data } = await apiClient.get<CreativeCanvas>(canvasPath(id), sessionConfig(signal))
    return normalizeCanvas(data)
  },

  async save(
    id: string,
    payload: { readonly revision: number; readonly title: string; readonly document: CanvasDocument },
    signal?: AbortSignal,
  ): Promise<CreativeCanvas> {
    const cleanTitle = payload.title.trim().slice(0, MAX_BOARD_TITLE_LENGTH) || '未命名画布'
    const cleanDoc = sanitizeDocument(payload.document)
    const { data } = await apiClient.put<CreativeCanvas>(
      canvasPath(id),
      {
        revision: payload.revision,
        title: cleanTitle,
        document: cleanDoc,
      },
      sessionConfig(signal),
    )
    return normalizeCanvas(data)
  },

  async remove(id: string, revision?: number, signal?: AbortSignal): Promise<void> {
    const config = sessionConfig(signal)
    await apiClient.delete(canvasPath(id), {
      ...config,
      params: revision !== undefined ? { revision } : undefined,
    })
  },

  /**
   * Idempotent binding of a Video Studio request_id to a specific video-draft node at an exact board revision.
   * Called strictly BEFORE videoStudio.create; non-replayable and never bills.
   * Rejects with 409 if the board changed in another tab.
   */
  async bindAttempt(
    canvasId: string,
    nodeId: string,
    requestId: string,
    revision: number,
    signal?: AbortSignal,
  ): Promise<void> {
    if (
      !isCanvasUuid(canvasId) ||
      !isCanvasUuid(nodeId) ||
      !isCanvasUuid(requestId) ||
      !Number.isInteger(revision) ||
      revision < 0
    ) {
      throw new TypeError('无效的画布分支、版本或提交编号。')
    }
    const config = sessionConfig(signal)
    await apiClient.post(
      `${canvasPath(canvasId)}/nodes/${encodeURIComponent(nodeId)}/attempts`,
      { request_id: requestId, revision },
      {
        ...config,
        skipAuthReplay: true,
      },
    )
  },
}
