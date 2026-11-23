import { shallowRef } from 'vue'
import { studioAPI } from '@/features/image-studio/api'
import { VIDEO_IMAGE_MAX_BYTES } from '@/features/video-studio/api'
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
 * Memory-only private preview cache for Creative Canvas image nodes.
 * - Uses Image Studio's authenticated `/thumbnail` endpoint when `index === 0`.
 * - For `index > 0`, reads the exact authorized original (`image_index = locator.index`) into a revocable memory `blob:` URL
 *   so multi-image slots display their true image without ever misusing index 0's thumbnail or persisting URLs/bytes in CanvasDocument.
 */
export function createCanvasImageThumbCache(userId?: number) {
  const version = shallowRef(0)
  const entries = new Map<string, Entry>()
  const lifetime = new AbortController()

  function bump() {
    version.value += 1
  }

  function get(locator: CanvasImageLocator | undefined): { status: ImageThumbStatus; url: string } {
    version.value
    if (!locator) return { status: 'unavailable', url: '' }
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
    const existing = entries.get(key)
    if (existing && (existing.status === 'ready' || existing.status === 'loading')) return

    const controller = new AbortController()
    const onLifetimeAbort = () => controller.abort(lifetime.signal.reason)
    lifetime.signal.addEventListener('abort', onLifetimeAbort, { once: true })
    const entry: Entry = { key, status: 'loading', url: '', controller }
    entries.set(key, entry)
    bump()

    try {
      const blob = locator.index === 0
        ? await studioAPI(locator.route, lifetime.signal).thumbnail(
            {
              id: `${locator.batchId}/${locator.itemId}`,
              job: { id: locator.batchId } as Parameters<ReturnType<typeof studioAPI>['thumbnail']>[0]['job'],
              item: { custom_id: locator.itemId } as Parameters<ReturnType<typeof studioAPI>['thumbnail']>[0]['item'],
            },
            controller.signal,
          )
        : await studioAPI(locator.route, controller.signal).image(
            {
              job: { id: locator.batchId },
              item: { custom_id: locator.itemId },
            },
            locator.index,
            typeof userId === 'number' && userId > 0 ? { maxBytes: VIDEO_IMAGE_MAX_BYTES, userId } : undefined,
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
      lifetime.signal.removeEventListener('abort', onLifetimeAbort)
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
