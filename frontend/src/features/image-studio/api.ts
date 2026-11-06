import apiClient from '@/api/client'
import type { BatchImageJob, BatchImageItem, BatchImageModel, BatchImageSubmitRequest } from '@/api/batchImage'

export interface StudioKey { readonly id: number; readonly name: string; readonly group_name: string; readonly status: string; readonly expired: boolean; readonly quota_exhausted: boolean }
export interface ItemContinuation { readonly job: BatchImageJob; readonly cursor: number }
export interface StudioTask { readonly id: string; readonly job: BatchImageJob & { readonly error?: BatchImageItem['error'] }; readonly item: BatchImageItem }
const itemStatuses: Readonly<Record<string, string>> = { pending: '排队中', queued: '排队中', running: '生成中', processing: '生成中', success: '已完成', completed: '已完成', failed: '失败', cancelled: '已取消', output_deleted: '图片已删除' }
export const itemStatusLabel = (status: string): string => itemStatuses[status] || status
export function taskDisplay(task: StudioTask) {
  const unfinished = ['pending', 'queued', 'running', 'processing', 'result_available'].includes(task.item.status)
  const terminalBatch = ['cancelled', 'failed', 'output_deleted'].includes(task.job.status)
  const projected = unfinished && terminalBatch
  const status = projected ? task.job.status : task.item.status
  return {
    status,
    label: projected && status === 'failed' ? '任务失败' : status === 'output_deleted' ? '结果已清理' : itemStatusLabel(status),
    message: task.item.error?.message || (projected ? task.job.error?.message : undefined),
  }
}
const base = '/image-studio'
export const taskRoot = (task: StudioTask) => task.job.parent_batch_id || task.job.id
export const taskPending = (task: StudioTask) => !['completed', 'failed', 'cancelled', 'output_deleted'].includes(task.job.status)
export const taskId = (job: BatchImageJob, item: BatchImageItem) => `${job.id}/${item.custom_id}`
export async function listKeys(signal: AbortSignal): Promise<StudioKey[]> {
  const result: StudioKey[] = []
  for (let page = 1; ; page++) {
    const { data } = await apiClient.get<{ items: StudioKey[]; pages: number }>(`${base}/keys`, { params: { page, page_size: 100 }, signal })
    result.push(...data.items)
    if (page >= data.pages) return result
  }
}
export function studioAPI(key: number, signal: AbortSignal) {
  const config = { headers: { 'X-API-Key-ID': String(key) }, signal }
  const items = async (job: BatchImageJob, cursor = 0) => {
    const { data } = await apiClient.get<{ data: BatchImageItem[]; has_more: boolean }>(`${base}/batches/${encodeURIComponent(job.id)}/items`, { ...config, params: { limit: 100, cursor: String(cursor) } })
    return { tasks: data.data.map(item => ({ id: taskId(job, item), job, item })), next: data.has_more && data.data.length ? { job, cursor: cursor + data.data.length } : null }
  }
  return {
    items,
    async models() { return (await apiClient.get<{ data: BatchImageModel[] }>(`${base}/batches/models`, config)).data.data },
    async tasks(cursor = '') {
      const tasks: StudioTask[] = []
        const { data } = await apiClient.get<{ data: BatchImageJob[]; has_more: boolean }>(`${base}/batches`, { ...config, params: { limit: 12, cursor } })
        const itemContinuations: ItemContinuation[] = []
        for (const job of data.data) {
          const page = await items(job)
          tasks.push(...page.tasks)
          if (page.next) itemContinuations.push(page.next)
        }
        return { tasks, itemContinuations, nextCursor: data.has_more && data.data.length ? String(Number(cursor || 0) + data.data.length) : null }

    },
    async refreshPending(tasks: StudioTask[]) {
      const pending = [...new Set(tasks.filter(taskPending).map(task => task.job.id))]
      const updated = new Map<string, StudioTask>()
      for (const id of pending) {
        const job = (await apiClient.get<BatchImageJob>(`${base}/batches/${encodeURIComponent(id)}`, config)).data
        let offset = 0
        const loadedCount = tasks.filter(task => task.job.id === id).length
        while (offset < loadedCount) {
          const page = (await apiClient.get<{ data: BatchImageItem[]; has_more: boolean }>(`${base}/batches/${encodeURIComponent(id)}/items`, { ...config, params: { limit: 100, cursor: String(offset) } })).data
          for (const item of page.data) updated.set(taskId(job, item), { id: taskId(job, item), job, item })
          if (!page.has_more || !page.data.length) break
          offset += page.data.length
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
    async cancel(task: StudioTask) { return (await apiClient.post<StudioTask['job']>(`${base}/batches/${encodeURIComponent(task.job.id)}/cancel`, {}, config)).data },
  }
}
export function readImage(file: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => typeof reader.result === 'string' ? resolve(reader.result) : reject(new Error('无法读取图片'))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}
