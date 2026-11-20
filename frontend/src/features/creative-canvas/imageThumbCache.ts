import { shallowRef } from 'vue'
import { studioAPI } from '@/features/image-studio/api'
import type { CanvasImageLocator } from './types'

export type ImageThumbStatus = 'idle' | 'loading' | 'ready' | 'non-first-index' | 'unavailable' | 'error'

interface Entry {
  readonly key: string
  status: ImageThumbStatus
  url: string
  controller?: AbortController
}

export function imageLocatorKey(locator: CanvasImageLocator): string {
  return `${locator.route}:${locator.batchId}:${locator.itemId}:${locator.index}`
}

/**
 * Memory-only private thumbnail cache for Creative Canvas image nodes.
 * - Uses Image Studio's authenticated `/thumbnail` endpoint ONLY when `index === 0`.
 * - Never calls `/thumbnail` for `index > 0` (avoids showing the 1st image by mistake).
 * - Never passes thumbnails to Video Studio as originals.
 */
export function createCanvasImageThumbCache() {
  const version = shallowRef(0)
  const entries = new Map<string, Entry>()
  const lifetime = new AbortController()

  function bump() {
    version.value += 1
  }

  function get(locator: CanvasImageLocator | undefined): { status: ImageThumbStatus; url: string } {
    version.value
    if (!locator) return { status: 'unavailable', url: '' }
    if (locator.index > 0) return { status: 'non-first-index', url: '' }
    const entry = entries.get(imageLocatorKey(locator))
    if (!entry) return { status: 'idle', url: '' }
    return { status: entry.status, url: entry.url }
  }

  async function ensure(locator: CanvasImageLocator | undefined, available = true): Promise<void> {
    if (lifetime.signal.aborted || !locator) return
    const key = imageLocatorKey(locator)
    if (!available) {
      const existing = entries.get(key)
      if (existing?.url) URL.revokeObjectURL(existing.url)
      existing?.controller?.abort()
      entries.set(key, { key, status: 'unavailable', url: '' })
      bump()
      return
    }
    if (locator.index > 0) {
      // Backend `/thumbnail` serves index 0 only; never misuse it for index > 0.
      if (!entries.has(key)) {
        entries.set(key, { key, status: 'non-first-index', url: '' })
        bump()
      }
      return
    }
    const existing = entries.get(key)
    if (existing && (existing.status === 'ready' || existing.status === 'loading')) return

    const controller = new AbortController()
    const entry: Entry = { key, status: 'loading', url: '', controller }
    entries.set(key, entry)
    bump()

    try {
      const api = studioAPI(locator.route, lifetime.signal)
      const blob = await api.thumbnail(
        {
          id: `${locator.batchId}/${locator.itemId}`,
          job: { id: locator.batchId } as Parameters<typeof api.thumbnail>[0]['job'],
          item: { custom_id: locator.itemId } as Parameters<typeof api.thumbnail>[0]['item'],
        },
        controller.signal,
      )
      if (lifetime.signal.aborted || controller.signal.aborted) return
      if (!blob || !blob.size) {
        entry.status = 'unavailable'
        bump()
        return
      }
      entry.url = URL.createObjectURL(blob)
      entry.status = 'ready'
      bump()
    } catch (error) {
      if (lifetime.signal.aborted || controller.signal.aborted) return
      const status =
        typeof error === 'object' && error !== null && 'status' in error
          ? Number((error as { status?: unknown }).status)
          : 0
      entry.status = status === 401 || status === 403 || status === 404 || status === 410 ? 'unavailable' : 'error'
      bump()
    } finally {
      entry.controller = undefined
    }
  }

  function invalidate(locator: CanvasImageLocator) {
    const key = imageLocatorKey(locator)
    const entry = entries.get(key)
    if (!entry) return
    entry.controller?.abort()
    if (entry.url) URL.revokeObjectURL(entry.url)
    entries.delete(key)
    bump()
  }

  function dispose() {
    lifetime.abort()
    for (const entry of entries.values()) {
      entry.controller?.abort()
      if (entry.url) URL.revokeObjectURL(entry.url)
    }
    entries.clear()
    bump()
  }

  return { get, ensure, invalidate, dispose }
}

export type CanvasImageThumbCache = ReturnType<typeof createCanvasImageThumbCache>
