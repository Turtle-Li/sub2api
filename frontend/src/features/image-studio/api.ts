import apiClient from '@/api/client'
import type { BatchImageJob, BatchImageItem, BatchImageModel, BatchImageResultFilesResponse, BatchImageSubmitRequest } from '@/api/batchImage'
import { loadBatchImageArchivePreview } from '@/utils/batchImageArchive'

export interface StudioKey { readonly id: number; readonly name: string; readonly group_id?: number | null; readonly group_name: string; readonly status: string; readonly expired: boolean; readonly quota_exhausted: boolean; readonly can_generate?: boolean; readonly has_history?: boolean }
export type StudioWalletMode = 'on' | 'off' | 'legacy'
export const studioWalletRoute = 0
export interface ItemContinuation { readonly job: BatchImageJob; readonly cursor: number }
export interface StudioTask { readonly id: string; readonly job: BatchImageJob & { readonly error?: BatchImageItem['error'] }; readonly item: BatchImageItem }
export interface GalleryFilters { readonly status: string; readonly search: string; readonly root?: string }
const itemStatuses: Readonly<Record<string, string>> = { pending: '排队中', queued: '排队中', running: '生成中', processing: '生成中', succeeded: '已完成', success: '已完成', completed: '已完成', failed: '失败', cancelled: '已取消', output_deleted: '图片已删除' }
const failureMessages: ReadonlyMap<string, string> = new Map([
  ['RESULT_DELIVERY_FAILED', '生成结果未能交付到作品库。'],
  ['BATCH_IMAGE_DELIVERY_FAILED', '生成结果未能交付到作品库。'],
  ['IMAGE_UPSCALE_STORE_FAILED', '图片处理后未能保存。'],
  ['PROVIDER_ITEM_FAILED', '生成服务未能交付可用图片。'],
  ['SAFETY_BLOCKED', '提示词或参考图未通过生成服务的内容检查。'],
])
export const itemStatusLabel = (status: string): string => itemStatuses[status] || status
export function taskDisplay(task: StudioTask) {
  const { job, item } = task
  const succeeded = ['succeeded', 'success', 'completed'].includes(item.status)
  const archived = item.status === 'result_available' && (job.status === 'completed' || job.status === 'output_deleted')
  const expired = job.output_expires_at != null && job.output_expires_at * 1000 <= Date.now()
  const outputGone = expired || job.status === 'output_deleted' || job.output_deleted_at != null
  if ((succeeded && item.image_count > 0 || archived) && outputGone) {
    return { status: 'output_deleted', label: expired ? '图片已过期' : '图片已清理', message: expired ? '图片保留期已结束，任务记录仍可查看。' : '图片已清理，任务记录仍可查看。', canViewOutput: false, canOpenArchive: false }
  }
  if (archived && job.status === 'completed') {
    return { status: 'archived', label: '归档结果', canViewOutput: false, canOpenArchive: true }
  }
  const unfinished = ['pending', 'queued', 'running', 'processing', 'result_available'].includes(item.status)
  const terminalBatch = ['cancelled', 'failed', 'output_deleted'].includes(job.status)
  const projected = unfinished && terminalBatch
  const status = projected ? job.status : item.status
  const error = item.error || (projected ? job.error : undefined)
  return {
    status,
    label: projected && status === 'failed' ? '任务失败' : status === 'output_deleted' ? '结果已清理' : itemStatusLabel(status),
    message: failureMessages.get(error?.code || '') ?? error?.message,
    canViewOutput: job.status === 'completed' && succeeded && item.image_count > 0 && !outputGone,
    canOpenArchive: false,
    error,
  }
}

export function isImageOutputGone(error: unknown): boolean {
  return typeof error === 'object' && error !== null && 'status' in error && error.status === 410
}
const studioBase = '/image-studio'
export const taskRoot = (task: StudioTask) => task.job.parent_batch_id || task.job.id
export const taskPending = (task: StudioTask) => !['completed', 'failed', 'cancelled', 'output_deleted'].includes(task.job.status)
export const taskId = (job: BatchImageJob, item: BatchImageItem) => `${job.id}/${item.custom_id}`
export async function listKeys(signal: AbortSignal, walletMode?: (mode: StudioWalletMode) => void): Promise<StudioKey[]> {
  const result: StudioKey[] = []
  for (let page = 1; ; page++) {
    const { data, headers } = await apiClient.get<{ items: StudioKey[]; pages: number }>(`${studioBase}/keys`, { params: { page, page_size: 100 }, signal })
    if (page === 1) {
      const mode: unknown = headers?.['x-image-studio-wallet']
      walletMode?.(mode === 'on' || mode === 'off' ? mode : 'legacy')
    }
    result.push(...data.items)
    if (page >= data.pages) return result
  }
}
export function studioAPI(route: number, signal: AbortSignal) {
  const wallet = route === studioWalletRoute
  const base = wallet ? `${studioBase}/wallet` : studioBase
  const config = { headers: wallet ? {} : { 'X-API-Key-ID': String(route) }, signal }
  const items = async (job: BatchImageJob, cursor = 0) => {
    const { data } = await apiClient.get<{ data: BatchImageItem[]; has_more: boolean }>(`${base}/batches/${encodeURIComponent(job.id)}/items`, { ...config, params: { limit: 100, cursor: String(cursor) } })
    return { tasks: data.data.map(item => ({ id: taskId(job, item), job, item })), next: data.has_more && data.data.length ? { job, cursor: cursor + data.data.length } : null }
  }
  return {
    items,
    async models() { return (await apiClient.get<{ data: BatchImageModel[] }>(`${base}/batches/models`, config)).data.data },
    async tasks(cursor = '', filters: GalleryFilters = { status: 'all', search: '' }) {
      const { data } = await apiClient.get<{ data: Omit<StudioTask, 'id'>[]; next_cursor?: string }>(`${base}/batches/gallery`, { ...config, params: { limit: 24, cursor, status: filters.status, q: filters.search, ...(filters.root ? { root: filters.root } : {}) } })
      return { tasks: data.data.map(({ job, item }) => ({ id: taskId(job, item), job, item })), itemContinuations: [] as ItemContinuation[], nextCursor: data.next_cursor || null }
    },
    async refreshPending(tasks: StudioTask[]) {
      const pending = [...new Set(tasks.filter(taskPending).map(task => task.job.id))]
      const updated = new Map<string, StudioTask>()
      for (const id of pending) {
        const job = (await apiClient.get<BatchImageJob>(`${base}/batches/${encodeURIComponent(id)}`, config)).data
        const loaded = tasks.filter(task => task.job.id === id).map(task => task.item.custom_id)
        for (let offset = 0; offset < loaded.length; offset += 100) {
          const params = new URLSearchParams({ limit: '100' })
          for (const customId of loaded.slice(offset, offset + 100)) params.append('custom_id', customId)
          const page = (await apiClient.get<{ data: BatchImageItem[]; has_more: boolean }>(`${base}/batches/${encodeURIComponent(id)}/items`, { ...config, params })).data
          for (const item of page.data) updated.set(taskId(job, item), { id: taskId(job, item), job, item })
        }
      }
      return tasks.map(task => updated.get(task.id) || task)
    },
    async submit(body: BatchImageSubmitRequest, idempotencyKey: string) {
      return (await apiClient.post<BatchImageJob>(`${base}/batches`, body, { ...config, timeout: 120000, headers: { ...config.headers, 'Idempotency-Key': idempotencyKey } })).data
    },
    async image(task: StudioTask, index = 0) {
      return (await apiClient.get<Blob>(`${base}/batches/${encodeURIComponent(task.job.id)}/items/${encodeURIComponent(task.item.custom_id)}/content`, { ...config, responseType: 'blob', params: { image_index: index } })).data
    },
    archivePreview(task: StudioTask, requestSignal: AbortSignal) {
      return withImageReadSignal(signal, requestSignal, readSignal => loadBatchImageArchivePreview(async () => {
        readSignal.throwIfAborted()
        return (await apiClient.get<BatchImageResultFilesResponse>(`${base}/batches/${encodeURIComponent(task.job.id)}/result-files`, { ...config, signal: readSignal })).data.data
      }, task.item.custom_id, readSignal))
    },
    thumbnail(task: StudioTask, requestSignal: AbortSignal = signal) {
      return withImageReadSignal(signal, requestSignal, async readSignal => {
        for (let attempt = 0; ; attempt++) {
          try {
            readSignal.throwIfAborted()
            return (await apiClient.get<Blob>(`${base}/batches/${encodeURIComponent(task.job.id)}/items/${encodeURIComponent(task.item.custom_id)}/thumbnail`, { ...config, signal: readSignal, responseType: 'blob', timeout: 120000 })).data
          } catch (error) {
            if (readSignal.aborted || attempt >= 4 || typeof error !== 'object' || error === null || !('status' in error) || (error.status !== 503 && error.status !== 429)) throw error
            await waitForThumbnail(2000 * 2 ** attempt, readSignal)
          }
        }
      })
    },
    async cancel(task: StudioTask) { return (await apiClient.post<StudioTask['job']>(`${base}/batches/${encodeURIComponent(task.job.id)}/cancel`, {}, config)).data },
  }
}

async function withImageReadSignal<T>(workspace: AbortSignal, request: AbortSignal, read: (signal: AbortSignal) => Promise<T>): Promise<T> {
  workspace.throwIfAborted()
  request.throwIfAborted()
  if (workspace === request) return read(workspace)
  const controller = new AbortController()
  const abort = () => controller.abort(workspace.aborted ? workspace.reason : request.reason)
  const sources = [workspace, request]
  for (const source of sources) source.addEventListener('abort', abort, { once: true })
  try {
    return await read(controller.signal)
  } finally {
    for (const source of sources) source.removeEventListener('abort', abort)
  }
}

function waitForThumbnail(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const abort = () => {
      clearTimeout(timer)
      signal.removeEventListener('abort', abort)
      reject(signal.reason)
    }
    const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve() }, ms)
    signal.addEventListener('abort', abort, { once: true })
    if (signal.aborted) abort()
  })
}
export function readImage(file: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => typeof reader.result === 'string' ? resolve(reader.result) : reject(new Error('无法读取图片'))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}
