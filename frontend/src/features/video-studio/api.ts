import { effectScope, watch } from 'vue'
import apiClient, { boundedBlobConfig } from '@/api/client'
import { getStoredUserID } from '@/api/tokenRefresh'
import { useAuthStore } from '@/stores/auth'
import { hasRasterSignature } from '@/utils/batchImageArchive'

export type VideoReferenceKind = 'image' | 'video' | 'audio' | 'file'
export type VideoDurationMode = 'automatic' | 'selectable'

export interface VideoModel {
  readonly id: string
  readonly label: string
  readonly provider: 'muse' | 'goodput'
  readonly duration_mode?: VideoDurationMode
  readonly min_seconds: number
  readonly max_seconds: number
  readonly default_seconds: number
  readonly sizes: string[]
  readonly supports_frames: boolean
  readonly supports_seed: boolean
  readonly max_references: number
  readonly reference_kinds?: VideoReferenceKind[]
  readonly muse_options?: {
    readonly resolution_max_bytes: number
    readonly extra_max_bytes: number
    readonly timeout_max: number
    readonly native_size_max_bytes: number
  }
  readonly quotes: Record<string, number>
  readonly currency: 'USD' | 'CNY'
  readonly quote_id: string
}

export function videoDurationMode(model: Pick<VideoModel, 'id' | 'provider' | 'duration_mode'>): VideoDurationMode {
  if (model.duration_mode === 'automatic' || model.provider === 'muse' || model.id === 'muse-video') return 'automatic'
  return 'selectable'
}

const canonicalVideoModelLabels: Readonly<Record<string, string>> = {
  'muse-video': 'Muse',
  'minimax/h3': 'H3',
  'minimax/h3-fast': 'H3 Fast',
}

/** Single public display-name resolver across picker, trigger, gallery, detail, and canvas. */
export function videoModelDisplayName(
  model: Pick<VideoModel, 'id' | 'label'> | string,
  knownLabels?: Readonly<Record<string, string>>,
): string {
  if (typeof model === 'string') {
    const trimmed = model.trim()
    return knownLabels?.[trimmed]?.trim() || canonicalVideoModelLabels[trimmed] || trimmed
  }
  const label = typeof model.label === 'string' ? model.label.trim() : ''
  if (label) return label
  return knownLabels?.[model.id]?.trim() || canonicalVideoModelLabels[model.id] || model.id
}

export interface VideoSubmission {
  readonly request_id: string
  readonly model: string
  readonly quote_id: string
  readonly prompt: string
  readonly seconds?: number
  readonly size?: string
  readonly seed?: number
  readonly first_frame?: string
  readonly last_frame?: string
  readonly references?: string[]
  readonly native_size?: string
  readonly resolution?: string
  readonly timeout?: number
  readonly extra?: string
}

export interface VideoJobParameters {
  readonly material_mode: 'text' | 'references' | 'first_frame' | 'first_last_frame'
  readonly reference_count: number
  readonly duration_mode?: VideoDurationMode
  readonly seed?: number
  readonly reference_kinds?: VideoReferenceKind[]
  readonly resolution?: string
  readonly native_size?: string
  readonly timeout?: number
  readonly has_extra?: boolean
}

export interface VideoJob {
  readonly id: string
  readonly model: string
  readonly status: 'submitting' | 'queued' | 'running' | 'completed' | 'failed' | 'uncertain' | 'expired'
  readonly seconds: number
  readonly actual_duration_ms?: number
  readonly size: string
  readonly created_at: string
  readonly expires_at?: string
  readonly detail?: string
  readonly billing_review?: boolean
  readonly has_poster?: boolean
  readonly poster_expires_at?: string
  readonly charged_amount?: number
  readonly charged_currency?: string
  readonly parameters?: VideoJobParameters
}

const base = '/video-studio'
export const VIDEO_CONTENT_MAX_BYTES = 100_000_000 // Both provider download adapters use this decimal limit.
export const VIDEO_IMAGE_MAX_BYTES = 8 * 1024 * 1024
export const VIDEO_POSTER_MAX_BYTES = 128 * 1024
const videoTypes = new Set(['video/mp4', 'video/quicktime', 'video/webm', 'video/3gpp'])
const imageTypes = new Set(['image/png', 'image/jpeg', 'image/webp'])

export function acceptsVideoImage(blob: Blob): boolean {
  return blob.size > 0 && blob.size <= VIDEO_IMAGE_MAX_BYTES && imageTypes.has(blob.type)
}

export function validateVideoImage(value: string): void {
  const prefix = /^data:(image\/(?:png|jpeg|webp));base64,/.exec(value)
  if (!prefix) throw new TypeError('请选择 PNG、JPEG 或 WebP 原图，不支持图片链接。')
  const payload = value.slice(prefix[0].length)
  const bytes = payload.length / 4 * 3 - (payload.endsWith('==') ? 2 : payload.endsWith('=') ? 1 : 0)
  if (bytes > VIDEO_IMAGE_MAX_BYTES) throw new RangeError('每张原图不能超过 8 MiB。')
  if (!payload.length || payload.length % 4 !== 0 || !/^[A-Za-z0-9+/]+={0,2}$/.test(payload) ||
      !hasRasterSignature(Uint8Array.from(atob(payload.slice(0, 16)), c => c.charCodeAt(0)), prefix[1])) {
    throw new TypeError('原图数据无效或与图片格式不符。')
  }
}

export const videoReferenceTypes: Record<VideoReferenceKind, readonly string[]> = {
  image: ['image/png', 'image/jpeg', 'image/webp'],
  video: ['video/mp4', 'video/webm'],
  audio: ['audio/mpeg', 'audio/wav'],
  file: ['application/pdf'],
}

export function videoReferenceKind(value: string): VideoReferenceKind | '' {
  const mime = /^data:([^;,]+);base64,/.exec(value)?.[1]
  return (Object.keys(videoReferenceTypes) as VideoReferenceKind[]).find(kind => videoReferenceTypes[kind].includes(mime || '')) || ''
}

export function validateVideoReference(value: string): void {
  const kind = videoReferenceKind(value)
  if (!kind) throw new TypeError('参考素材须为本地 Base64 图片、MP4/WebM、MP3/WAV 或 PDF，不支持远程地址。')
  if (kind === 'image') { validateVideoImage(value); return }
  const payload = value.slice(value.indexOf(',') + 1)
  const bytes = payload.length / 4 * 3 - (payload.endsWith('==') ? 2 : payload.endsWith('=') ? 1 : 0)
  if (bytes > VIDEO_IMAGE_MAX_BYTES) throw new RangeError('每个参考素材不能超过 8 MiB。')
  if (!payload.length || payload.length % 4 !== 0 || !/^[A-Za-z0-9+/]+={0,2}$/.test(payload)) throw new TypeError('参考素材的 Base64 数据无效。')
  const head = atob(payload.slice(0, 32))
  const valid = kind === 'video'
    ? head.slice(4, 8) === 'ftyp' || head.startsWith('\x1a\x45\xdf\xa3')
    : kind === 'audio'
      ? head.startsWith('ID3') || (head.charCodeAt(0) === 0xff && (head.charCodeAt(1) & 0xe0) === 0xe0) || (head.startsWith('RIFF') && head.slice(8, 12) === 'WAVE')
      : head.startsWith('%PDF-')
  if (!valid) throw new TypeError('参考素材内容与声明格式不符。')
}

/** Outlives the originating image component, but never the draft or active media read. */
export function observeVideoOwner(userId: number, onLoss: () => void): () => void {
  const auth = useAuthStore()
  const scope = effectScope(true)
  scope.run(() => watch(() => auth.isAuthenticated ? auth.user?.id : null, id => {
    if (id !== userId) onLoss()
  }, { flush: 'sync' }))
  const onStorage = (event: StorageEvent) => {
    // Compare the event value too: a peer can log out and back in before this tab runs.
    if (event.key === null || (event.key === 'auth_token' && !event.newValue) ||
        (event.key === 'auth_user' && getStoredUserID(event.newValue) !== userId)) onLoss()
  }
  if (typeof window !== 'undefined') window.addEventListener('storage', onStorage)
  return () => {
    scope.stop()
    if (typeof window !== 'undefined') window.removeEventListener('storage', onStorage)
  }
}

function session(signal?: AbortSignal) {
  signal?.throwIfAborted()
  const auth = useAuthStore()
  if (!auth.isAuthenticated || !auth.user) throw new DOMException('请先登录视频工作台。', 'AbortError')
  return { signal, sessionUserId: auth.user.id }
}

function jobPath(id: string) {
  if (!id || id === '.' || id === '..') throw new TypeError('无效的视频任务编号。')
  return `${base}/jobs/${encodeURIComponent(id)}`
}

export const videoStudio = {
  async models(signal?: AbortSignal) {
    return (await apiClient.get<{ enabled: boolean; items: VideoModel[] }>(`${base}/models`, session(signal))).data
  },
  async list(cursor?: string, signal?: AbortSignal) {
    return (await apiClient.get<{ items: VideoJob[]; next_cursor?: string }>(`${base}/jobs`, {
      ...session(signal), params: { cursor },
    })).data
  },
  async create(body: VideoSubmission, signal?: AbortSignal) {
    const config = session(signal)
    if (!/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(body.request_id) ||
        body.request_id === '00000000-0000-0000-0000-000000000000') throw new TypeError('无效的视频提交编号。')
    if (body.first_frame !== undefined) validateVideoImage(body.first_frame)
    if (body.last_frame !== undefined) validateVideoImage(body.last_frame)
    for (const reference of body.references ?? []) validateVideoReference(reference)
    if ((body.model === 'minimax/h3' || body.model === 'minimax/h3-fast') &&
        new TextEncoder().encode(JSON.stringify(body)).byteLength > VIDEO_IMAGE_MAX_BYTES) {
      throw new RangeError('H3 整个请求（含 Base64 和提示词）不能超过 8 MiB。')
    }
    return (await apiClient.post<VideoJob>(`${base}/jobs`, body, {
      ...config, skipAuthReplay: true, timeout: 120_000,
      adapter: 'fetch', fetchOptions: { redirect: 'error' },
    })).data
  },
  async get(id: string, signal?: AbortSignal) {
    return (await apiClient.get<VideoJob>(jobPath(id), session(signal))).data
  },
  async poster(id: string, signal?: AbortSignal): Promise<Blob> {
    const config = session(signal)
    const controller = new AbortController()
    const abort = () => controller.abort(signal?.reason)
    const stop = observeVideoOwner(config.sessionUserId, abort)
    signal?.addEventListener('abort', abort, { once: true })
    try {
      const { data } = await apiClient.get<Blob>(`${jobPath(id)}/poster`, {
        ...config, ...boundedBlobConfig(VIDEO_POSTER_MAX_BYTES), signal: controller.signal,
      })
      controller.signal.throwIfAborted()
      if (!(data?.size > 0) || data.size > VIDEO_POSTER_MAX_BYTES || data.type !== 'image/jpeg') {
        throw new TypeError('未收到有效的 JPEG 封面帧。')
      }
      return data
    } finally {
      signal?.removeEventListener('abort', abort)
      stop()
    }
  },
  // No prefetch, public media URL, or object URL here. The player/download owner disposes its own URL.
  async content(id: string, signal?: AbortSignal): Promise<Blob> {
    const config = session(signal)
    const controller = new AbortController()
    const abort = () => controller.abort(signal?.reason)
    const stop = observeVideoOwner(config.sessionUserId, abort)
    signal?.addEventListener('abort', abort, { once: true })
    try {
      const { data } = await apiClient.get<Blob>(`${jobPath(id)}/content`, {
        ...config, ...boundedBlobConfig(VIDEO_CONTENT_MAX_BYTES), signal: controller.signal, timeout: 120_000,
      })
      controller.signal.throwIfAborted()
      if (data?.size > VIDEO_CONTENT_MAX_BYTES) throw new RangeError('视频超过 100 MB 读取上限。')
      if (!(data?.size > 0) || !videoTypes.has(data.type)) throw new TypeError('未收到可用的视频文件。')
      return data
    } finally {
      signal?.removeEventListener('abort', abort)
      stop()
    }
  },
}
