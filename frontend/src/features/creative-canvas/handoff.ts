import { getStoredUserID } from '@/api/tokenRefresh'
import { observeVideoOwner } from '@/features/video-studio/api'
import { useAuthStore } from '@/stores/auth'
import { isCanvasUuid, MAX_BOARD_TITLE_LENGTH, MAX_NODE_TITLE_LENGTH, MAX_PROMPT_LENGTH } from './document'
import type { CanvasDraftHandoff } from './types'

interface StagedHandoff {
  readonly handoff: CanvasDraftHandoff
  readonly dispose: () => void
}

const STORAGE_KEY_PREFIX = 'sub2:creative-canvas:handoff:'

let staged: StagedHandoff | null = null

function signedInAs(userId: number): boolean {
  const auth = useAuthStore()
  return (
    auth.isAuthenticated &&
    auth.user?.id === userId &&
    getStoredUserID() === userId &&
    !!localStorage.getItem('auth_token')
  )
}

function storageKey(userId: number): string {
  return `${STORAGE_KEY_PREFIX}${userId}`
}

export function clearCanvasDraftHandoff(userId?: number): void {
  const previous = staged
  staged = null
  previous?.dispose()
  try {
    if (userId !== undefined) {
      sessionStorage.removeItem(storageKey(userId))
    } else if (previous) {
      sessionStorage.removeItem(storageKey(previous.handoff.userId))
    }
  } catch {
    /* sessionStorage may be unavailable in private browsing */
  }
}

/**
 * Stages an owner-bound canvas draft handoff for Video Studio.
 * Only IDs and revision live in tab storage; NEVER stores raw media, prompt, URLs, or credentials.
 */
export function stageCanvasDraftHandoff(
  userId: number,
  input: Omit<CanvasDraftHandoff, 'userId'>,
): CanvasDraftHandoff {
  if (!signedInAs(userId)) {
    throw new TypeError('登录账号已变更，无法交接画布草稿。')
  }
  if (
    !isCanvasUuid(input.canvasId) ||
    !isCanvasUuid(input.nodeId) ||
    !Number.isInteger(input.revision) ||
    input.revision < 0
  ) {
    throw new TypeError('无效的画布草稿或修订号标识。')
  }

  clearCanvasDraftHandoff(userId)

  const handoff: CanvasDraftHandoff = Object.freeze({
    userId,
    canvasId: input.canvasId,
    nodeId: input.nodeId,
    revision: input.revision,
    canvasTitle: input.canvasTitle.slice(0, MAX_BOARD_TITLE_LENGTH),
    draftTitle: input.draftTitle.slice(0, MAX_NODE_TITLE_LENGTH),
    prompt: input.prompt.slice(0, MAX_PROMPT_LENGTH),
    expectsSourceImage: Boolean(input.expectsSourceImage),
    ...(input.expectsTailFrame !== undefined ? { expectsTailFrame: Boolean(input.expectsTailFrame) } : {}),
    ...(input.continuationMode ? { continuationMode: input.continuationMode } : {}),
    ...(input.sourceSummary ? { sourceSummary: input.sourceSummary.slice(0, MAX_NODE_TITLE_LENGTH) } : {}),
  })

  const stop = observeVideoOwner(userId, () => {
    if (staged?.handoff === handoff) clearCanvasDraftHandoff(userId)
  })

  staged = { handoff, dispose: stop }

  try {
    // Store ONLY IDs + revision in sessionStorage (no prompt, no media, no titles).
    sessionStorage.setItem(
      storageKey(userId),
      JSON.stringify({
        canvasId: handoff.canvasId,
        nodeId: handoff.nodeId,
        revision: handoff.revision,
      }),
    )
  } catch {
    /* ignore storage errors */
  }

  return handoff
}

/**
 * Returns the in-memory staged handoff if and only if it belongs to the signed-in owner
 * and matches the exact canvasId + nodeId from the route.
 * Returns null after a page reload so Video Studio MUST re-fetch the authoritative board
 * from the server (restoring revision, authorized source image, and prompt).
 */
export function readStagedCanvasDraftHandoff(
  userId: number,
  canvasId: string,
  nodeId: string,
): CanvasDraftHandoff | null {
  if (!signedInAs(userId) || !isCanvasUuid(canvasId) || !isCanvasUuid(nodeId)) {
    return null
  }
  if (!staged) return null
  if (staged.handoff.userId !== userId) {
    clearCanvasDraftHandoff()
    return null
  }
  if (staged.handoff.canvasId === canvasId && staged.handoff.nodeId === nodeId) {
    return staged.handoff
  }
  return null
}

export function readStoredCanvasBindingMeta(
  userId: number,
  canvasId: string,
  nodeId: string,
): { readonly canvasId: string; readonly nodeId: string; readonly revision: number } | null {
  if (!signedInAs(userId) || !isCanvasUuid(canvasId) || !isCanvasUuid(nodeId)) {
    return null
  }
  try {
    const raw = sessionStorage.getItem(storageKey(userId))
    if (!raw) return null
    const parsed = JSON.parse(raw) as { canvasId?: unknown; nodeId?: unknown; revision?: unknown }
    if (
      parsed.canvasId === canvasId &&
      parsed.nodeId === nodeId &&
      typeof parsed.revision === 'number' &&
      Number.isInteger(parsed.revision) &&
      parsed.revision >= 0
    ) {
      return { canvasId, nodeId, revision: parsed.revision }
    }
  } catch {
    /* ignore storage parse errors */
  }
  return null
}
