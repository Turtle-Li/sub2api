import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { StudioTask } from '../api'
import { createThumbnailLoader, type ThumbnailPriority } from '../thumbnailLoader'

const task = (id: string) => ({ id } as StudioTask)
const image = new Blob(['cover'], { type: 'image/jpeg' })
const revoke = vi.fn()
let sequence = 0
beforeEach(() => {
  sequence = 0; revoke.mockClear()
  vi.stubGlobal('URL', class extends URL {
    static createObjectURL = (_blob: Blob) => 'blob:cover-' + ++sequence
    static revokeObjectURL = revoke
  })
})
afterEach(() => vi.unstubAllGlobals())

describe('workspace thumbnail cache', () => {
  it('starts queued visible work ahead of prefetch and discards far-away work that never started', async () => {
    const releases = new Map<string, () => void>()
    const read = vi.fn((record: StudioTask) => new Promise<Blob>(resolve => releases.set(record.id, () => resolve(image))))
    const previews = createThumbnailLoader(read, new AbortController().signal)
    const first = Array.from({ length: 4 }, (_, index) => previews.load(task(String(index))))
    await vi.waitFor(() => expect(releases.size).toBe(4))
    let farPriority: ThumbnailPriority = 1
    const nearby = previews.load(task('near'), undefined, () => 1)
    const far = expect(previews.load(task('far'), undefined, () => farPriority)).rejects.toMatchObject({ name: 'AbortError' })
    const visible = previews.load(task('visible'))
    farPriority = 0
    releases.get('0')?.()
    await vi.waitFor(() => expect(releases.has('visible')).toBe(true))
    expect(releases.has('near')).toBe(false)
    releases.get('visible')?.()
    await vi.waitFor(() => expect(releases.has('near')).toBe(true))
    for (const release of releases.values()) release()
    await Promise.all([...first, visible, nearby, far])
    expect(releases.has('far')).toBe(false)
  })

  it('retains the same object URL for cached covers, evicts only the least recent, and revokes on scope exit', async () => {
    const scope = new AbortController()
    const read = vi.fn(async () => image)
    const previews = createThumbnailLoader(read, scope.signal)
    await Promise.all(Array.from({ length: 192 }, (_, index) => previews.load(task(String(index)))))
    const first = previews.url('0'), second = previews.url('1')
    expect(first).toMatch(/^blob:/)
    const invalidated = vi.fn()
    const unsubscribe = previews.subscribe('1', invalidated)
    await previews.load(task('0'))
    expect(read).toHaveBeenCalledTimes(192)
    await previews.load(task('later'))
    expect(previews.url('0')).toBe(first)
    expect(previews.url('1')).toBe('')
    expect(revoke).toHaveBeenCalledWith(second)
    expect(invalidated).toHaveBeenCalledOnce()
    unsubscribe()
    scope.abort()
    expect(previews.url('0')).toBe('')
    expect(revoke).toHaveBeenCalledWith(first)
    await expect(previews.load(task('0'))).rejects.toMatchObject({ name: 'AbortError' })
  })

  it('evicts by bytes before hitting the item limit', async () => {
    const large = new Blob([new Uint8Array(6 * 1024 * 1024)])
    const read = vi.fn(async () => large)
    const previews = createThumbnailLoader(read, new AbortController().signal)
    await previews.load(task('a')); await previews.load(task('b')); await previews.load(task('c'))
    expect(previews.url('a')).toBe('')
    expect(previews.url('b')).toMatch(/^blob:/)
    expect(previews.url('c')).toMatch(/^blob:/)
    expect(revoke).toHaveBeenCalledOnce()
  })
})
