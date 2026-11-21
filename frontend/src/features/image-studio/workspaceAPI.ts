import type { BatchImageJob, BatchImageModel, BatchImageSubmitRequest } from '@/api/batchImage'
import { studioAPI, taskDisplay, type StudioTask, type GalleryFilters } from './api'
import { cachedStudioModels, cacheStudioModels, forgetStudioModels } from './cache'
import { createThumbnailLoader } from './thumbnailLoader'

export const modelIdentity = (model: Pick<BatchImageModel, 'provider' | 'id'>) => `${model.provider}:${model.id}`
type Client = ReturnType<typeof studioAPI>

export interface StudioOriginal {
  readonly route: number // 0 = session wallet; otherwise the original public key ID, never its value.
  readonly batchId: string
  readonly itemId: string
  readonly index: number
  readonly expiresAt?: number // Unix milliseconds
}

// Legacy custom IDs are arbitrary text. Only opaque, single-segment IDs can cross workspaces.
const safeAssetId = /^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$/
export function isSafeStudioOriginal({ route, batchId, itemId, index, expiresAt }: StudioOriginal): boolean {
  return Number.isSafeInteger(route) && route >= 0 && Number.isSafeInteger(index) && index >= 0 &&
    typeof batchId === 'string' && safeAssetId.test(batchId) && typeof itemId === 'string' && safeAssetId.test(itemId) &&
    (expiresAt === undefined || (Number.isFinite(expiresAt) && expiresAt > Date.now()))
}

// Discovery is read-only. Each mutation and asset stays in its server-owned
// wallet or legacy-key scope; a model choice never grants access to another task.
export function workspaceAPI(keys: readonly number[], signal: AbortSignal, access?: { readonly modelKeys: readonly number[]; readonly historyKeys: readonly number[]; readonly userId?: number }) {
  const clients = new Map([...new Set(keys)].sort((a, b) => a - b).map(key => [key, studioAPI(key, signal)]))
  const modelKeys = new Set(access?.modelKeys ?? keys)
  const historyKeys = new Set(access?.historyKeys ?? keys)
  const owners = new Map<string, number>()
  const attempts = new Map<string, number>()
  let imageQueue: Promise<void> = Promise.resolve()
  const previews = createThumbnailLoader((task, requestSignal) => owned(task.job).thumbnail(task, requestSignal), signal)
  let directory: Promise<{ key: number; models: BatchImageModel[] }[]> | undefined
  let directoryVersion = 0
  const displayed = new Map([...clients.keys()].filter(key => modelKeys.has(key)).map(key => [key, cachedStudioModels(access?.userId, key) || []]))
  const listeners = new Set<(models: BatchImageModel[]) => void>()
  function visibleModels() {
    const unique = new Map<string, BatchImageModel>()
    for (const [, models] of [...displayed].sort(([a], [b]) => a - b)) for (const model of models) if (!unique.has(modelIdentity(model))) unique.set(modelIdentity(model), model)
    return [...unique.values()]
  }
  function publish() { for (const listener of listeners) listener(visibleModels()) }
  let warnings: string[] = []
  async function discover() {
    if (!directory) directory = (async () => {
      const version = directoryVersion
      const entries = [...clients.entries()].filter(([key]) => modelKeys.has(key))
      const results: { key: number; models: BatchImageModel[] }[] = []
      warnings = []
      // Avoid unbounded catalogue requests for users with many API keys.
      for (let start = 0; start < entries.length; start += 6) {
        await Promise.all(entries.slice(start, start + 6).map(async ([key, client]) => {
          try {
            const models = await client.models()
            signal.throwIfAborted()
            results.push({ key, models })
            if (version === directoryVersion) {
              displayed.set(key, models); cacheStudioModels(access?.userId, key, models); publish()
            }
          }
          catch (error) {
            if (signal.aborted) throw error
            const status = typeof error === 'object' && error !== null && 'status' in error ? error.status : undefined
            // Non-image keys are expected to fail image admission.
            if (version !== directoryVersion) return
            if (status === 403) { displayed.delete(key); forgetStudioModels(access?.userId, key); publish() }
            else warnings.push('部分模型暂时无法更新，显示最近记录；提交时会重新校验。')
          }
        }))
      }
      return results.sort((a, b) => a.key - b.key)
    })()
    return directory
  }
  function owned(job: BatchImageJob): Client {
    const key = owners.get(job.id)
    const client = key === undefined ? undefined : clients.get(key)
    if (!client) throw new Error('找不到此任务的原始通道，请刷新任务后重试。')
    return client
  }
  function remember(key: number, tasks: StudioTask[]) {
    for (const task of tasks) {
      owners.set(task.job.id, key)
      if (task.job.parent_batch_id) owners.set(task.job.parent_batch_id, key)
    }
  }
  async function route(model: Pick<BatchImageModel, 'provider' | 'id'>, parent?: string) {
    const routes = (await discover()).filter(entry => entry.models.some(item => modelIdentity(item) === modelIdentity(model)))
    const key = parent ? owners.get(parent) : routes[0]?.key
    if (key === undefined || !routes.some(entry => entry.key === key)) throw new Error('此模型无法使用原作品的通道，请返回画廊后引用图片创建新作品。')
    return key
  }
  return {
    refreshDirectory: () => { directoryVersion++; directory = undefined },
    cachedModels: visibleModels,
    warnings: () => [...new Set(warnings)],
    async canContinue(task: StudioTask, model: BatchImageModel) {
      try { await route(model, task.job.parent_batch_id || task.job.id); return true } catch { return false }
    },
    async models(onUpdate?: (models: BatchImageModel[]) => void) {
      if (onUpdate) listeners.add(onUpdate)
      try { await discover(); return visibleModels() }
      finally { if (onUpdate) listeners.delete(onUpdate) }
    },
    async tasks(cursor = '', filters?: GalleryFilters) {
      // History does not wait for every model catalogue to finish.
      const entries = [...clients.entries()].filter(([key]) => historyKeys.has(key))
      const offsets: Record<string, string | null> = cursor ? JSON.parse(cursor) : {}
      const pages: Awaited<ReturnType<Client['tasks']>>[] = []
      const next: Record<string, string | null> = {}
      const denied = new Set<number>()
      let incomplete = false
      for (let start = 0; start < entries.length; start += 6) {
        await Promise.all(entries.slice(start, start + 6).map(async ([key, client]) => {
          if (cursor && offsets[key] === null) { next[key] = null; return }
          try {
            const page = filters ? await client.tasks(offsets[key] || '', filters) : await client.tasks(offsets[key] || '')
            signal.throwIfAborted()
            remember(key, page.tasks)
            pages.push(page); next[key] = page.nextCursor
          } catch (error) {
            if (signal.aborted) throw error
            const status = typeof error === 'object' && error !== null && 'status' in error ? error.status : undefined
            next[key] = status === 403 ? null : offsets[key] || ''
            if (status === 403) denied.add(key)
            else incomplete = true
          }
        }))
      }
      const deniedBatchIds = denied.size ? new Set([...owners].filter(([, key]) => denied.has(key)).map(([id]) => id)) : new Set<string>()
      return { tasks: pages.flatMap(page => page.tasks), itemContinuations: pages.flatMap(page => page.itemContinuations), nextCursor: Object.values(next).some(value => value !== null) ? JSON.stringify(next) : null, incomplete, deniedBatchIds }
    },
    async items(job: BatchImageJob, cursor = 0) { return owned(job).items(job, cursor) },
    async refreshPending(tasks: StudioTask[]) {
      const updates: StudioTask[] = []
      for (const [key, client] of clients) {
        const selected = tasks.filter(task => owners.get(task.job.id) === key)
        if (selected.length) updates.push(...await client.refreshPending(selected))
      }
      return updates
    },
    async submit(body: BatchImageSubmitRequest, idempotencyKey: string) {
      let key = attempts.get(idempotencyKey)
      if (key === undefined) {
        key = await route({ id: body.model, provider: body.provider || '' }, body.parent_batch_id)
        attempts.set(idempotencyKey, key)
      }
      // Never fail over after an ambiguous POST, even if the catalogue changes.
      const job = await clients.get(key)!.submit(body, idempotencyKey)
      owners.set(job.id, key)
      historyKeys.add(key)
      attempts.delete(idempotencyKey)
      return job
    },
    originalLocator(task: StudioTask, index: number): StudioOriginal | null {
      const route = owners.get(task.job.id)
      if (signal.aborted || route === undefined || !taskDisplay(task).canViewOutput || index >= task.item.image_count) return null
      const locator = { route, batchId: task.job.id, itemId: task.item.custom_id, index,
        ...(task.job.output_expires_at != null && { expiresAt: task.job.output_expires_at * 1000 }) }
      return isSafeStudioOriginal(locator) ? locator : null
    },
    image(task: StudioTask, index = 0) {
      // Download admission is shared by user, including reads from different keys.
      const request = imageQueue.then(() => { signal.throwIfAborted(); return owned(task.job).image(task, index) })
      imageQueue = request.then(() => undefined, () => undefined)
      return request
    },
    archivePreview(task: StudioTask, requestSignal: AbortSignal) {
      return owned(task.job).archivePreview(task, requestSignal)
    },
    previews,
    async cancel(task: StudioTask) { return owned(task.job).cancel(task) },
  }
}
