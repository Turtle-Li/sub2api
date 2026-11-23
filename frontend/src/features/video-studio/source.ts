import { getStoredUserID } from '@/api/tokenRefresh'
import { extractBoundedVideoFrame } from '@/features/creative-canvas/upstreamAdapter'
import { studioAPI, readImage as toDataURL } from '@/features/image-studio/api'
import { isSafeStudioOriginal, type StudioOriginal } from '@/features/image-studio/workspaceAPI'
import { useAuthStore } from '@/stores/auth'
import {
  acceptsVideoImage,
  observeVideoOwner,
  validateVideoImage,
  videoStudio,
  VIDEO_IMAGE_MAX_BYTES,
} from './api'

export interface VideoSource {
  readonly userId: number
  readonly label: string
}

export interface VideoTailFrameLocator {
  readonly videoId: string
  readonly label?: string
  readonly expiresAt?: number
}

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const ZERO_UUID = '00000000-0000-0000-0000-000000000000'
export const TAIL_FRAME_DECODE_TIMEOUT_MS = 15_000
export const TAIL_FRAME_MAX_PIXELS = 40_000_000

type DraftTarget =
  | { readonly kind: 'image'; readonly original: StudioOriginal }
  | { readonly kind: 'video-tail'; readonly videoId: string }

// Tab-local and deliberately lost on reload. The UI only receives the opaque frozen handle.
interface Draft {
  readonly source: VideoSource
  readonly target: DraftTarget
  readonly deadline: number
  readonly controller: AbortController
  readonly dispose: () => void
  reading: boolean
}

let draft: Draft | null = null
const tailFrameSources = new WeakSet<VideoSource>()
const draftLifetime = 10 * 60_000

function signedInAs(userId: number): boolean {
  const auth = useAuthStore()
  return (
    auth.isAuthenticated &&
    auth.user?.id === userId &&
    getStoredUserID() === userId &&
    !!localStorage.getItem('auth_token')
  )
}

function isValidVideoUuid(value: unknown): value is string {
  return typeof value === 'string' && UUID_RE.test(value) && value !== ZERO_UUID
}

function isValidOptionalExpiry(expiresAt: number | undefined): boolean {
  return expiresAt === undefined || (Number.isFinite(expiresAt) && expiresAt > Date.now())
}

export function isTailFrameSource(source: VideoSource | null | undefined): boolean {
  return Boolean(source && tailFrameSources.has(source))
}

export function clearImage(): void {
  const previous = draft
  draft = null
  previous?.dispose()
  previous?.controller.abort()
}

function installDraft(userId: number, label: string, target: DraftTarget, expiresAt?: number): VideoSource {
  clearImage()
  const source: VideoSource = Object.freeze({ userId, label })
  if (target.kind === 'video-tail') {
    tailFrameSources.add(source)
  }
  const deadline = Math.min(expiresAt ?? Infinity, Date.now() + draftLifetime)
  const controller = new AbortController()
  const clear = () => {
    if (draft?.source === source) clearImage()
  }
  const stop = observeVideoOwner(userId, clear)
  const timer = window.setTimeout(clear, Math.max(0, deadline - Date.now()))
  draft = {
    source,
    target,
    deadline,
    controller,
    reading: false,
    dispose() {
      stop()
      window.clearTimeout(timer)
    },
  }
  return source
}

export function prepareImage(userId: number, locator: StudioOriginal): VideoSource {
  if (!signedInAs(userId) || !isSafeStudioOriginal(locator)) {
    throw new TypeError('原图已失效，请重新选择本人的作品。')
  }
  // Never retain the task/preview object, a callback into the image workspace, or caller-supplied extra fields.
  const { route, batchId, itemId, index, expiresAt } = locator
  return installDraft(
    userId,
    `生图作品 · 第 ${index + 1} 张`,
    { kind: 'image', original: { route, batchId, itemId, index } },
    expiresAt,
  )
}

/**
 * Stages a pinned Video Studio job ID so its real tail frame PNG is extracted locally
 * ONLY upon explicit paid confirmation in Video Studio. Never accepts URLs or bytes.
 */
export function prepareVideoTailFrame(userId: number, locator: VideoTailFrameLocator): VideoSource {
  if (
    !signedInAs(userId) ||
    !locator ||
    !isValidVideoUuid(locator.videoId) ||
    !isValidOptionalExpiry(locator.expiresAt)
  ) {
    throw new TypeError('续镜关联的视频作品已失效或无权访问，请返回画布重新确认。')
  }
  const cleanLabel = locator.label?.trim().slice(0, 96) || '上一镜成片尾帧（PNG）'
  return installDraft(
    userId,
    cleanLabel,
    { kind: 'video-tail', videoId: locator.videoId },
    locator.expiresAt,
  )
}

export function pendingImage(userId: number): VideoSource | null {
  if (!draft) return null
  if (draft.deadline <= Date.now() || !signedInAs(draft.source.userId)) {
    clearImage()
    return null
  }
  return draft.source.userId === userId ? draft.source : null
}

async function extractBoundedTailFramePng(
  blob: Blob,
  videoId: string,
  signal: AbortSignal,
): Promise<File> {
  signal.throwIfAborted()
  const objectUrl = URL.createObjectURL(blob)
  const extractAbort = new AbortController()
  const timer = setTimeout(() => {
    extractAbort.abort(new RangeError('提取上一镜成片尾帧超时，未提交视频。'))
  }, TAIL_FRAME_DECODE_TIMEOUT_MS)
  const onCallerAbort = () => {
    clearTimeout(timer)
    extractAbort.abort(signal.reason ?? new DOMException('已取消尾帧提取。', 'AbortError'))
  }
  if (signal.aborted) {
    onCallerAbort()
  } else {
    signal.addEventListener('abort', onCallerAbort, { once: true })
  }

  try {
    const extracted = await extractBoundedVideoFrame({
      url: objectUrl,
      position: 'last',
      filename: `tail-frame-${videoId}.png`,
      maxPixels: TAIL_FRAME_MAX_PIXELS,
      signal: extractAbort.signal,
    })
    signal.throwIfAborted()
    extractAbort.signal.throwIfAborted()
    if (
      extracted.width <= 0 ||
      extracted.height <= 0 ||
      extracted.width * extracted.height > TAIL_FRAME_MAX_PIXELS
    ) {
      throw new RangeError('上一镜成片画面像素超出安全上限，无法提取尾帧。')
    }
    if (extracted.file.type !== 'image/png' || extracted.file.size <= 0) {
      throw new TypeError('未能提取有效的 PNG 尾帧图像。')
    }
    if (extracted.file.size > VIDEO_IMAGE_MAX_BYTES) {
      throw new RangeError('提取的真实尾帧 PNG 超过 8 MiB 上限，未降级或提交视频。')
    }
    return extracted.file
  } finally {
    if (timer !== undefined) clearTimeout(timer)
    signal.removeEventListener('abort', onCallerAbort)
    URL.revokeObjectURL(objectUrl)
  }
}

/** Consume once, never using the gallery's thumbnail/poster, earlier bytes, or the currently selected key. */
export async function readImage(source: VideoSource, signal: AbortSignal): Promise<string> {
  signal.throwIfAborted()
  const current = draft
  const isTail = isTailFrameSource(source)
  if (!current || current.source !== source || pendingImage(source.userId) !== source || current.reading) {
    throw new TypeError(
      isTail ? '续镜尾帧授权草稿已失效，请返回画布重新进入。' : '原图草稿已失效，请返回生图工作台重新选择。',
    )
  }
  current.reading = true
  const combined = new AbortController()
  const abortCombined = () => combined.abort(signal.reason ?? current.controller.signal.reason)
  const clear = () => {
    abortCombined()
    if (draft === current) clearImage()
  }
  signal.addEventListener('abort', clear, { once: true })
  current.controller.signal.addEventListener('abort', abortCombined, { once: true })
  const readSignal = combined.signal
  const check = () => {
    signal.throwIfAborted()
    readSignal.throwIfAborted()
    if (pendingImage(source.userId) !== source) {
      throw new TypeError(isTail ? '续镜来源已过期或登录账号已变更。' : '原图已过期或登录账号已变更。')
    }
  }
  try {
    if (current.target.kind === 'image') {
      const { route, batchId, itemId, index } = current.target.original
      const image = await studioAPI(route, readSignal).image(
        { job: { id: batchId }, item: { custom_id: itemId } },
        index,
        { maxBytes: VIDEO_IMAGE_MAX_BYTES, userId: source.userId },
      )
      check()
      if (!acceptsVideoImage(image)) throw new TypeError('原图须为 PNG、JPEG 或 WebP，且不超过 8 MiB。')
      const data = await toDataURL(image, readSignal)
      check()
      validateVideoImage(data)
      return data
    }

    const { videoId } = current.target
    const videoBlob = await videoStudio.content(videoId, readSignal)
    check()
    const framePng = await extractBoundedTailFramePng(videoBlob, videoId, readSignal)
    check()
    if (!acceptsVideoImage(framePng)) {
      throw new RangeError('提取的真实尾帧 PNG 须不超过 8 MiB。')
    }
    const data = await toDataURL(framePng, readSignal)
    check()
    validateVideoImage(data)
    return data
  } finally {
    signal.removeEventListener('abort', clear)
    current.controller.signal.removeEventListener('abort', abortCombined)
    clear()
  }
}
