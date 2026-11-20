import type { StudioTask } from './api'

export type ThumbnailPriority = 0 | 1 | 2
// Leave same-origin connections for page metadata and explicit originals on HTTP/1.1.
const maxActive = 4
const maxEntries = 192
const maxBytes = 16 * 1024 * 1024
type WaitingCover = { priority: () => ThumbnailPriority; start: () => void; cancel: (reason: unknown) => void }

// One memory-only cache and queue per authenticated workspace. Originals never use it.
export function createThumbnailLoader(read: (task: StudioTask, signal: AbortSignal) => Promise<Blob>, scope: AbortSignal) {
  const cache = new Map<string, { url: string; bytes: number }>()
  const listeners = new Map<string, Set<() => void>>()
  const waiting = new Set<WaitingCover>()
  let usedBytes = 0
  let active = 0
  let scheduled = false

  function notify(id: string) { for (const listener of listeners.get(id) ?? []) listener() }
  function touch(id: string) {
    const entry = cache.get(id)
    if (entry) { cache.delete(id); cache.set(id, entry) }
    return entry
  }
  function remove(id: string) {
    const entry = cache.get(id)
    if (!entry) return
    cache.delete(id); usedBytes -= entry.bytes
    URL.revokeObjectURL(entry.url)
    notify(id)
  }
  function remember(id: string, blob: Blob) {
    if (touch(id)) return
    if (!blob.size || blob.size > maxBytes) throw new Error('预览图大小异常。')
    const url = URL.createObjectURL(blob)
    cache.set(id, { url, bytes: blob.size }); usedBytes += blob.size
    for (const previous of cache.keys()) {
      if (cache.size <= maxEntries && usedBytes <= maxBytes) break
      remove(previous)
    }
    notify(id)
  }
  function subscribe(id: string, listener: () => void) {
    const set = listeners.get(id) ?? new Set()
    set.add(listener); listeners.set(id, set)
    return () => { set.delete(listener); if (!set.size) listeners.delete(id) }
  }

  function drainSoon() {
    if (scheduled || scope.aborted) return
    scheduled = true
    queueMicrotask(() => {
      scheduled = false
      // Drop work that hasn't started and has scrolled away. Started reads may finish.
      const skipped = new DOMException('Preview no longer near the viewport', 'AbortError')
      for (const entry of waiting) if (!entry.priority()) entry.cancel(skipped)
      while (!scope.aborted && active < maxActive && waiting.size) {
        let next: WaitingCover | undefined
        for (const entry of waiting) {
          if (entry.priority() === 2) { next = entry; break }
          next ??= entry
        }
        if (!next) break
        next.start()
      }
    })
  }

  async function load(task: StudioTask, signal: AbortSignal = scope, priority: () => ThumbnailPriority = () => 2) {
    scope.throwIfAborted(); signal.throwIfAborted()
    if (touch(task.id)) return
    await new Promise<void>((resolve, reject) => {
      const dequeue = () => { waiting.delete(entry); signal.removeEventListener('abort', onAbort) }
      const onAbort = () => entry.cancel(signal.reason)
      const entry = {
        priority,
        cancel: (reason: unknown) => { dequeue(); reject(reason) },
        start: () => {
          dequeue()
          active++
          void (async () => {
            try {
              scope.throwIfAborted(); signal.throwIfAborted()
              if (!touch(task.id)) {
                const blob = await read(task, signal)
                scope.throwIfAborted(); signal.throwIfAborted()
                remember(task.id, blob)
              }
              resolve()
            } catch (error) { reject(error) }
            finally { active--; drainSoon() }
          })()
        },
      }
      waiting.add(entry)
      if (signal !== scope) signal.addEventListener('abort', onAbort, { once: true })
      drainSoon()
    })
  }

  scope.addEventListener('abort', () => {
    for (const entry of waiting) entry.cancel(scope.reason)
    for (const id of cache.keys()) remove(id)
  }, { once: true })
  return { load, remove, touch, subscribe, url: (id: string) => cache.get(id)?.url ?? '' }
}

export type ThumbnailLoader = ReturnType<typeof createThumbnailLoader>
