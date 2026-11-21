import { getStoredUserID } from '@/api/tokenRefresh'
import { studioAPI, readImage as toDataURL } from '@/features/image-studio/api'
import { isSafeStudioOriginal, type StudioOriginal } from '@/features/image-studio/workspaceAPI'
import { useAuthStore } from '@/stores/auth'
import { acceptsVideoImage, observeVideoOwner, validateVideoImage, VIDEO_IMAGE_MAX_BYTES } from './api'

export interface VideoSource { readonly userId: number; readonly label: string }

// Tab-local and deliberately lost on reload. The UI only receives the opaque frozen handle.
interface Draft {
  readonly source: VideoSource
  readonly original: StudioOriginal
  readonly deadline: number
  readonly controller: AbortController
  readonly dispose: () => void
  reading: boolean
}
let draft: Draft | null = null
const draftLifetime = 10 * 60_000

function signedInAs(userId: number): boolean {
  const auth = useAuthStore()
  return auth.isAuthenticated && auth.user?.id === userId && getStoredUserID() === userId && !!localStorage.getItem('auth_token')
}

export function clearImage(): void {
  const previous = draft
  draft = null
  previous?.dispose()
  previous?.controller.abort()
}

export function prepareImage(userId: number, locator: StudioOriginal): VideoSource {
  if (!signedInAs(userId) || !isSafeStudioOriginal(locator)) throw new TypeError('原图已失效，请重新选择本人的作品。')
  clearImage()
  // Never retain the task/preview object, a callback into the image workspace, or caller-supplied extra fields.
  const { route, batchId, itemId, index, expiresAt } = locator
  const source: VideoSource = Object.freeze({ userId, label: `生图作品 · 第 ${index + 1} 张` })
  const deadline = Math.min(expiresAt ?? Infinity, Date.now() + draftLifetime)
  const controller = new AbortController()
  const clear = () => { if (draft?.source === source) clearImage() }
  const stop = observeVideoOwner(userId, clear)
  const timer = window.setTimeout(clear, deadline - Date.now())
  draft = { source, original: { route, batchId, itemId, index }, deadline, controller, reading: false,
    dispose() { stop(); window.clearTimeout(timer) },
  }
  return source
}

export function pendingImage(userId: number): VideoSource | null {
  if (!draft) return null
  if (draft.deadline <= Date.now() || !signedInAs(draft.source.userId)) { clearImage(); return null }
  return draft.source.userId === userId ? draft.source : null
}

/** Consume once, never using the gallery's thumbnail, earlier bytes, or the currently selected key. */
export async function readImage(source: VideoSource, signal: AbortSignal): Promise<string> {
  signal.throwIfAborted()
  const current = draft
  if (!current || current.source !== source || pendingImage(source.userId) !== source || current.reading) {
    throw new TypeError('原图草稿已失效，请返回生图工作台重新选择。')
  }
  current.reading = true
  const clear = () => { if (draft === current) clearImage() }
  signal.addEventListener('abort', clear, { once: true })
  const readSignal = current.controller.signal
  const check = () => {
    signal.throwIfAborted()
    readSignal.throwIfAborted()
    if (pendingImage(source.userId) !== source) throw new TypeError('原图已过期或登录账号已变更。')
  }
  try {
    const { route, batchId, itemId, index } = current.original
    const image = await studioAPI(route, readSignal).image({ job: { id: batchId }, item: { custom_id: itemId } }, index,
      { maxBytes: VIDEO_IMAGE_MAX_BYTES, userId: source.userId })
    check()
    if (!acceptsVideoImage(image)) throw new TypeError('原图须为 PNG、JPEG 或 WebP，且不超过 8 MiB。')
    const data = await toDataURL(image, readSignal)
    check()
    validateVideoImage(data)
    return data
  } finally {
    signal.removeEventListener('abort', clear)
    clear()
  }
}
