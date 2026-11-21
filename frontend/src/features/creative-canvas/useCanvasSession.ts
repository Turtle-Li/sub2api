import { getCurrentScope, onScopeDispose } from 'vue'
import { observeVideoOwner } from '@/features/video-studio/api'
import { useAuthStore } from '@/stores/auth'

export interface CanvasRunContext {
  readonly seq: number
  readonly userId: number
  readonly signal: AbortSignal
}

export function isAbortError(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const err = error as { name?: string; code?: string }
  return err.name === 'AbortError' || err.name === 'CanceledError' || err.code === 'ERR_CANCELED'
}

export function useCanvasSession(onOwnerLoss: () => void) {
  const auth = useAuthStore()
  let seq = 0
  let activeController: AbortController | null = null
  let stopOwnerWatch: (() => void) | null = null
  let watchedUserId: number | null = null

  function currentUserId(): number | null {
    return auth.isAuthenticated && auth.user ? auth.user.id : null
  }

  function abortPending(): void {
    activeController?.abort()
    activeController = null
  }

  function stopWatchingOwner(): void {
    stopOwnerWatch?.()
    stopOwnerWatch = null
    watchedUserId = null
  }

  function ensureOwnerWatch(userId: number): void {
    if (watchedUserId === userId && stopOwnerWatch) return
    stopWatchingOwner()
    watchedUserId = userId
    stopOwnerWatch = observeVideoOwner(userId, () => {
      seq += 1
      abortPending()
      stopWatchingOwner()
      onOwnerLoss()
    })
  }

  function startRun(): CanvasRunContext | null {
    const userId = currentUserId()
    if (userId === null) {
      seq += 1
      abortPending()
      stopWatchingOwner()
      return null
    }
    ensureOwnerWatch(userId)
    abortPending()
    const controller = new AbortController()
    activeController = controller
    seq += 1
    return { seq, userId, signal: controller.signal }
  }

  function isCurrent(run: CanvasRunContext): boolean {
    return (
      run.seq === seq &&
      !run.signal.aborted &&
      currentUserId() === run.userId
    )
  }

  if (getCurrentScope()) {
    onScopeDispose(() => {
      seq += 1
      abortPending()
      stopWatchingOwner()
    })
  }

  return { currentUserId, startRun, isCurrent, abortPending, stopWatchingOwner }
}
