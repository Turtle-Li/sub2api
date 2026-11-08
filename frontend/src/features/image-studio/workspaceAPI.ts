import type { BatchImageJob, BatchImageModel, BatchImageSubmitRequest } from '@/api/batchImage'
import { studioAPI, type StudioTask, type GalleryFilters } from './api'

export const modelIdentity = (model: Pick<BatchImageModel, 'provider' | 'id'>) => `${model.provider}:${model.id}`
type Client = ReturnType<typeof studioAPI>

// Discovery is read-only. All mutations and assets still pass through the original
// key-scoped session API; a model choice never grants access to another key's task.
export function workspaceAPI(keys: readonly number[], signal: AbortSignal, access?: { readonly modelKeys: readonly number[]; readonly historyKeys: readonly number[] }) {
  const clients = new Map([...new Set(keys)].sort((a, b) => a - b).map(key => [key, studioAPI(key, signal)]))
  const modelKeys = new Set(access?.modelKeys ?? keys)
  const historyKeys = new Set(access?.historyKeys ?? keys)
  const owners = new Map<string, number>()
  const attempts = new Map<string, number>()
  let imageQueue: Promise<void> = Promise.resolve()
  let activeThumbnails = 0
  const thumbnailWaiters: (() => void)[] = []
  const thumbnails = new Map<string, Blob>()
  let thumbnailBytes = 0
  let directory: Promise<{ key: number; models: BatchImageModel[] }[]> | undefined
  let warnings: string[] = []
  let historyWarnings: string[] = []
  async function discover() {
    if (!directory) directory = (async () => {
      const entries = [...clients.entries()].filter(([key]) => modelKeys.has(key))
      const results: { key: number; models: BatchImageModel[] }[] = []
      warnings = []
      // Avoid unbounded catalogue requests for users with many API keys.
      for (let start = 0; start < entries.length; start += 6) {
        await Promise.all(entries.slice(start, start + 6).map(async ([key, client]) => {
          try { results.push({ key, models: await client.models() }) }
          catch (error) {
            if (signal.aborted) throw error
            const status = typeof error === 'object' && error !== null && 'status' in error ? error.status : undefined
            // Non-image keys are expected to fail image admission.
            if (status !== 403) warnings.push('部分模型暂时无法读取，请稍后刷新。')
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
    refreshDirectory: () => { directory = undefined },
    warnings: () => [...new Set([...warnings, ...historyWarnings])],
    async canContinue(task: StudioTask, model: BatchImageModel) {
      try { await route(model, task.job.parent_batch_id || task.job.id); return true } catch { return false }
    },
    async models() {
      const unique = new Map<string, BatchImageModel>()
      for (const entry of await discover()) for (const model of entry.models) if (!unique.has(modelIdentity(model))) unique.set(modelIdentity(model), model)
      return [...unique.values()]
    },
    async tasks(cursor = '', filters?: GalleryFilters) {
      // History does not wait for every model catalogue to finish.
      const entries = [...clients.entries()].filter(([key]) => historyKeys.has(key))
      historyWarnings = []
      const offsets: Record<string, string | null> = cursor ? JSON.parse(cursor) : {}
      const pages: Awaited<ReturnType<Client['tasks']>>[] = []
      const next: Record<string, string | null> = {}
      for (let start = 0; start < entries.length; start += 6) {
        await Promise.all(entries.slice(start, start + 6).map(async ([key, client]) => {
          if (cursor && offsets[key] === null) { next[key] = null; return }
          try {
            const page = filters ? await client.tasks(offsets[key] || '', filters) : await client.tasks(offsets[key] || '')
            remember(key, page.tasks)
            pages.push(page); next[key] = page.nextCursor
          } catch (error) {
            if (signal.aborted) throw error
            const status = typeof error === 'object' && error !== null && 'status' in error ? error.status : undefined
            next[key] = status === 403 ? null : offsets[key] || ''
            if (status !== 403) historyWarnings.push('部分作品暂时无法读取，请刷新重试。')
          }
        }))
      }
      return { tasks: pages.flatMap(page => page.tasks), itemContinuations: pages.flatMap(page => page.itemContinuations), nextCursor: Object.values(next).some(value => value !== null) ? JSON.stringify(next) : null }
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
    image(task: StudioTask, index = 0) {
      // Download admission is shared by user, including reads from different keys.
      const request = imageQueue.then(() => { signal.throwIfAborted(); return owned(task.job).image(task, index) })
      imageQueue = request.then(() => undefined, () => undefined)
      return request
    },
    async thumbnail(task: StudioTask, requestSignal: AbortSignal = signal) {
      signal.throwIfAborted()
      requestSignal.throwIfAborted()
      const cached = thumbnails.get(task.id)
      if (cached) {
        thumbnails.delete(task.id); thumbnails.set(task.id, cached)
        return cached
      }
      // Preview preparation never queues ahead of explicit originals or edits.
      if (activeThumbnails >= 3) await new Promise<void>(resolve => thumbnailWaiters.push(resolve))
      else activeThumbnails++
      try {
        signal.throwIfAborted()
        requestSignal.throwIfAborted()
        const ready = thumbnails.get(task.id)
        if (ready) return ready
        const blob = await owned(task.job).thumbnail(task, requestSignal)
        const completed = thumbnails.get(task.id)
        if (completed) return completed
        if (blob.size <= 16 * 1024 * 1024) {
          thumbnails.set(task.id, blob); thumbnailBytes += blob.size
          for (const [id, previous] of thumbnails) {
            if (thumbnails.size <= 64 && thumbnailBytes <= 16 * 1024 * 1024) break
            thumbnails.delete(id); thumbnailBytes -= previous.size
          }
        }
        return blob
      } finally {
        const next = thumbnailWaiters.shift()
        if (next) next()
        else activeThumbnails--
      }
    },
    async cancel(task: StudioTask) { return owned(task.job).cancel(task) },
  }
}
