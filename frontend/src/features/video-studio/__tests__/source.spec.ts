import { createPinia, setActivePinia } from 'pinia'
import { effectScope } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import apiClient from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { workspaceAPI } from '@/features/image-studio/workspaceAPI'
import { studioWalletRoute } from '@/features/image-studio/api'
import * as upstreamAdapterModule from '@/features/creative-canvas/upstreamAdapter'
import { VIDEO_IMAGE_MAX_BYTES } from '../api'
import {
  clearImage,
  isTailFrameSource,
  pendingImage,
  prepareImage,
  prepareVideoTailFrame,
  readImage,
  TAIL_FRAME_MAX_PIXELS,
} from '../source'

vi.mock('@/i18n', () => ({ getLocale: () => 'zh-CN' }))

const owner = 17
const locator = { route: 43, batchId: 'imgbatch_child', itemId: 'item_000002', index: 1 }
const png = new Blob([Uint8Array.of(0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a)], { type: 'image/png' })
function signIn(id: number) {
  localStorage.setItem('auth_user', JSON.stringify({ id })); localStorage.setItem('auth_token', 'fixture-session')
  useAuthStore().$patch({ user: { id }, token: 'fixture-session' })
}
beforeEach(() => { setActivePinia(createPinia()); signIn(owner) })
afterEach(() => { clearImage(); vi.restoreAllMocks(); vi.useRealTimers(); localStorage.clear(); sessionStorage.clear() })

describe('owner-bound, one-use image handoff', () => {
  it.each([43, studioWalletRoute])('resolves the selected original index via the exact source route (%i), never the root or a preview', async route => {
    const get = vi.spyOn(apiClient, 'get')
    const post = vi.spyOn(apiClient, 'post').mockRejectedValue(new Error('unexpected paid dispatch'))
    const workspace = new AbortController()
    const gallery = workspaceAPI([1, route], workspace.signal, { userId: owner, modelKeys: [1], historyKeys: [route] })
    const liveUntil = Math.floor(Date.now() / 1000) + 300
    get.mockResolvedValueOnce({ data: { data: [{
      job: { id: locator.batchId, parent_batch_id: 'imgbatch_root', status: 'completed', output_expires_at: liveUntil },
      item: { custom_id: locator.itemId, status: 'succeeded', image_count: 2, prompt_preview: 'private text https://preview.invalid' },
    }] } })
    const [task] = (await gallery.tasks()).tasks
    const original = gallery.originalLocator(task, 1)
    expect(original).toEqual({ ...locator, route, expiresAt: liveUntil * 1000 })
    if (!original) throw new Error('missing source')
    expect(gallery.originalLocator(task, 2)).toBeNull()
    expect(gallery.originalLocator({ ...task, knownOutputGone: true }, 1)).toBeNull()
    expect(gallery.originalLocator({ ...task, item: { ...task.item, status: 'result_available' } }, 0)).toBeNull()
    expect(gallery.originalLocator({ ...task, job: { ...task.job, id: 'unknown-owner' } }, 0)).toBeNull()
    const source = prepareImage(owner, original)
    expect(source).toEqual({ userId: owner, label: '生图作品 · 第 2 张' })
    expect(pendingImage(owner)).toBe(source); expect(pendingImage(18)).toBeNull()
    expect(get).toHaveBeenCalledTimes(1); expect(post).not.toHaveBeenCalled()
    expect(localStorage.length).toBe(2); expect(sessionStorage.length).toBe(0)
    workspace.abort() // navigating away must not keep or reuse the old workspace client
    get.mockResolvedValueOnce({ data: png })
    await expect(readImage(source, new AbortController().signal)).resolves.toBe('data:image/png;base64,iVBORw0KGgo=')
    const base = route === studioWalletRoute ? '/image-studio/wallet' : '/image-studio'
    expect(get).toHaveBeenLastCalledWith(`${base}/batches/imgbatch_child/items/item_000002/content`, expect.objectContaining({
      headers: route === studioWalletRoute ? {} : { 'X-API-Key-ID': '43' }, params: { image_index: 1 },
      sessionUserId: owner, responseType: 'blob', maxContentLength: VIDEO_IMAGE_MAX_BYTES,
    }))
    expect(pendingImage(owner)).toBeNull()
    await expect(readImage(source, new AbortController().signal)).rejects.toThrow()
    expect(get).toHaveBeenCalledTimes(2); expect(post).not.toHaveBeenCalled()
  })

  it.each([{ route: -1 }, { index: -1 }, { index: 0.5 }, { expiresAt: NaN }, { expiresAt: 1 },
    { batchId: '..' }, { itemId: 'https://private.invalid/token' }, { itemId: 'data:image/png;base64,eA==' }, { itemId: '%2e%2e' }])(
    'never stores an invalid/expired source locator (%o)', fields => {
      expect(() => prepareImage(owner, { ...locator, ...fields })).toThrow()
      expect(pendingImage(owner)).toBeNull()
    })

  it('does not trust copied display metadata or a superseded source', async () => {
    const get = vi.spyOn(apiClient, 'get')
    const first = prepareImage(owner, locator)
    const next = prepareImage(owner, { ...locator, index: 0 })
    for (const stale of [first, { ...next }, { ...next, userId: 18 }]) {
      await expect(readImage(stale, new AbortController().signal)).rejects.toThrow()
    }
    expect(pendingImage(owner)).toBe(next); expect(get).not.toHaveBeenCalled()
  })

  it.each(['output expiry', 'draft TTL'])('clears the draft before reading after %s', async kind => {
    vi.useFakeTimers()
    const get = vi.spyOn(apiClient, 'get')
    const source = prepareImage(owner, { ...locator, expiresAt: Date.now() + (kind === 'output expiry' ? 100 : 60 * 60_000) })
    await vi.advanceTimersByTimeAsync(kind === 'output expiry' ? 101 : 10 * 60_000)
    expect(pendingImage(owner)).toBeNull(); await expect(readImage(source, new AbortController().signal)).rejects.toThrow()
    expect(get).not.toHaveBeenCalled()
  })

  it.each(['logout', 'local replacement', 'peer logout', 'peer replacement'])('invalidates an unused draft on %s, even if the original account signs back in', async change => {
    const host = effectScope()
    const source = host.run(() => prepareImage(owner, locator))
    if (!source) throw new Error('missing source')
    host.stop() // original host has unmounted before account change
    if (change === 'logout') useAuthStore().$patch({ user: null, token: null })
    else if (change === 'local replacement') signIn(18)
    else window.dispatchEvent(new StorageEvent('storage', { key: 'auth_user', newValue: change === 'peer logout' ? null : JSON.stringify({ id: 18 }) }))
    signIn(owner)
    expect(pendingImage(owner)).toBeNull(); await expect(readImage(source, new AbortController().signal)).rejects.toThrow()
  })

  it('retains a draft across ordinary same-user auth refreshes', () => {
    const source = prepareImage(owner, locator)
    const updated = JSON.stringify({ id: owner, balance: 100 })
    localStorage.setItem('auth_user', updated)
    window.dispatchEvent(new StorageEvent('storage', { key: 'auth_user', newValue: updated }))
    expect(pendingImage(owner)).toBe(source)
  })

  it('fails closed if the tab store and the active credential owner disagree', async () => {
    const source = prepareImage(owner, locator)
    const get = vi.spyOn(apiClient, 'get')
    localStorage.setItem('auth_user', '{')
    await expect(readImage(source, new AbortController().signal)).rejects.toThrow()
    expect(() => prepareImage(owner, locator)).toThrow()
    expect(get).not.toHaveBeenCalled(); expect(pendingImage(owner)).toBeNull()
  })

  it.each([403, 404, 410])('does not fall back to another key, thumbnail or cached bytes on %i', async status => {
    const get = vi.spyOn(apiClient, 'get').mockRejectedValueOnce({ status })
    const source = prepareImage(owner, locator)
    await expect(readImage(source, new AbortController().signal)).rejects.toEqual({ status })
    expect(get).toHaveBeenCalledOnce(); expect(pendingImage(owner)).toBeNull()
  })

  it.each([new Blob(['<svg/>'], { type: 'image/svg+xml' }), new Blob(['not a PNG'], { type: 'image/png' }),
    new Blob([], { type: 'image/png' }), new Blob([new Uint8Array(VIDEO_IMAGE_MAX_BYTES + 1)], { type: 'image/png' })])(
    'rejects an oversized, empty, unsupported or mislabeled original', async blob => {
      vi.spyOn(apiClient, 'get').mockResolvedValueOnce({ data: blob })
      const source = prepareImage(owner, locator)
      await expect(readImage(source, new AbortController().signal)).rejects.toThrow()
      expect(pendingImage(owner)).toBeNull()
    })

  it.each(['abort', 'clear', 'logout', 'expiry', 'replace', 'silent owner change'])('rejects late original bytes after %s and does not destroy a newer draft', async event => {
    const now = Date.now()
    vi.spyOn(Date, 'now').mockReturnValue(now)
    const caller = new AbortController()
    let finish: (result: { data: Blob }) => void = () => { throw new Error('read not started') }
    const get = vi.spyOn(apiClient, 'get').mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const first = prepareImage(owner, { ...locator, expiresAt: now + 1000 })
    const read = readImage(first, caller.signal)
    // A second caller cannot race or consume the first read's result.
    await expect(readImage(first, caller.signal)).rejects.toThrow(); expect(get).toHaveBeenCalledOnce()
    const replacement = event === 'replace' ? prepareImage(owner, { ...locator, index: 0 }) : null
    if (event === 'abort') caller.abort()
    if (event === 'clear') clearImage()
    if (event === 'logout') useAuthStore().$patch({ user: null, token: null })
    if (event === 'expiry') vi.mocked(Date.now).mockReturnValue(now + 1001)
    if (event === 'silent owner change') localStorage.setItem('auth_user', JSON.stringify({ id: 19 }))
    if (!['expiry', 'silent owner change'].includes(event)) expect(get.mock.calls[0][1]?.signal?.aborted).toBe(true)
    finish({ data: png }) // even a transport which ignores abort must not hand these bytes back
    await expect(read).rejects.toThrow(); expect(pendingImage(owner)).toBe(replacement)
  })
})

describe('owner-bound, one-use video tail-frame PNG handoff', () => {
  const videoId = '44444444-4444-4444-8444-444444444444'
  const videoBlob = new Blob([Uint8Array.of(0x1a, 0x45, 0xdf, 0xa3)], { type: 'video/webm' })
  const extractedPng = new File([Uint8Array.of(0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a)], 'tail.png', {
    type: 'image/png',
  })

  it('fetches private video content ONLY on explicit readImage, extracts position="last" PNG, revokes blob URL, and consumes once', async () => {
    const get = vi.spyOn(apiClient, 'get')
    const createUrl = vi.fn(() => 'blob:tail-video-1')
    const revokeUrl = vi.fn()
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, writable: true, value: createUrl })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, writable: true, value: revokeUrl })
    const extractSpy = vi.spyOn(upstreamAdapterModule, 'extractBoundedVideoFrame').mockResolvedValueOnce({
      file: extractedPng,
      width: 1280,
      height: 720,
      timeSeconds: 4.95,
      durationSeconds: 5,
    })

    const imgSource = prepareImage(owner, locator)
    expect(isTailFrameSource(imgSource)).toBe(false)

    const tailSource = prepareVideoTailFrame(owner, { videoId, label: '镜头 B1 · 真实尾帧（PNG）' })
    expect(tailSource).toEqual({ userId: owner, label: '镜头 B1 · 真实尾帧（PNG）' })
    expect(Object.keys(tailSource)).toEqual(['userId', 'label'])
    expect(isTailFrameSource(tailSource)).toBe(true)
    expect(pendingImage(owner)).toBe(tailSource)
    expect(get).not.toHaveBeenCalled()
    expect(extractSpy).not.toHaveBeenCalled()

    get.mockResolvedValueOnce({ data: videoBlob })
    await expect(readImage(tailSource, new AbortController().signal)).resolves.toBe(
      'data:image/png;base64,iVBORw0KGgo=',
    )

    expect(get).toHaveBeenCalledTimes(1)
    expect(get).toHaveBeenCalledWith(
      `/video-studio/jobs/${videoId}/content`,
      expect.objectContaining({ responseType: 'blob' }),
    )
    expect(extractSpy).toHaveBeenCalledWith({
      url: 'blob:tail-video-1',
      position: 'last',
      filename: `tail-frame-${videoId}.png`,
      maxPixels: TAIL_FRAME_MAX_PIXELS,
      signal: expect.any(AbortSignal),
    })
    expect(createUrl).toHaveBeenCalledOnce()
    expect(revokeUrl).toHaveBeenCalledWith('blob:tail-video-1')
    expect(pendingImage(owner)).toBeNull()
    await expect(readImage(tailSource, new AbortController().signal)).rejects.toThrow()
  })

  it.each([
    '00000000-0000-0000-0000-000000000000',
    'not-a-uuid',
    'https://private.invalid/video.webm',
    '../secret',
  ])('rejects unsafe or non-UUID videoId (%s)', badId => {
    expect(() => prepareVideoTailFrame(owner, { videoId: badId })).toThrow()
    expect(pendingImage(owner)).toBeNull()
  })

  it('fails closed without fallback and revokes temporary object URL on decode failure, >8 MiB PNG, or >40 MP frame', async () => {
    const createUrl = vi.fn(() => 'blob:tail-video-err')
    const revokeUrl = vi.fn()
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, writable: true, value: createUrl })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, writable: true, value: revokeUrl })
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: videoBlob })
    const extractSpy = vi.spyOn(upstreamAdapterModule, 'extractBoundedVideoFrame')

    // 1. Decode error
    extractSpy.mockRejectedValueOnce(new Error('Failed to load video frame'))
    const s1 = prepareVideoTailFrame(owner, { videoId })
    await expect(readImage(s1, new AbortController().signal)).rejects.toThrow()
    expect(revokeUrl).toHaveBeenLastCalledWith('blob:tail-video-err')
    expect(pendingImage(owner)).toBeNull()

    // 2. Oversized PNG (> 8 MiB)
    const hugePng = new File([new Uint8Array(VIDEO_IMAGE_MAX_BYTES + 1)], 'huge.png', { type: 'image/png' })
    extractSpy.mockResolvedValueOnce({
      file: hugePng,
      width: 1920,
      height: 1080,
      timeSeconds: 4.95,
      durationSeconds: 5,
    })
    const s2 = prepareVideoTailFrame(owner, { videoId })
    await expect(readImage(s2, new AbortController().signal)).rejects.toThrow(/8 MiB/)
    expect(revokeUrl).toHaveBeenCalledTimes(2)

    // 3. Exceeds pixel cap (> 40 MP)
    extractSpy.mockResolvedValueOnce({
      file: extractedPng,
      width: 8000,
      height: Math.ceil(TAIL_FRAME_MAX_PIXELS / 8000) + 10,
      timeSeconds: 4.95,
      durationSeconds: 5,
    })
    const s3 = prepareVideoTailFrame(owner, { videoId })
    await expect(readImage(s3, new AbortController().signal)).rejects.toThrow(/像素超出安全上限/)
    expect(revokeUrl).toHaveBeenCalledTimes(3)
    expect(get).toHaveBeenCalledTimes(3)
  })

  it('extractBoundedVideoFrame rejects immediately on media error, checks maxPixels BEFORE canvas allocation, and cleans up on abort', async () => {
    let canvasAllocations = 0
    let videoCleanups = 0
    const listeners = new Map<string, Set<() => void>>()
    let behavior: 'error' | 'oversize' | 'hang' = 'error'

    const fakeVideo = {
      preload: '',
      muted: false,
      playsInline: false,
      src: '',
      videoWidth: 10000,
      videoHeight: 5000,
      duration: 6,
      currentTime: 0,
      addEventListener(type: string, cb: () => void) {
        if (!listeners.has(type)) listeners.set(type, new Set())
        listeners.get(type)!.add(cb)
      },
      removeEventListener(type: string, cb: () => void) {
        listeners.get(type)?.delete(cb)
      },
      pause: vi.fn(),
      removeAttribute(attr: string) {
        if (attr === 'src') this.src = ''
      },
      load() {
        if (!this.src) {
          videoCleanups += 1
          return
        }
        if (behavior === 'error') {
          queueMicrotask(() => listeners.get('error')?.forEach(fn => fn()))
        } else if (behavior === 'oversize') {
          queueMicrotask(() => listeners.get('loadedmetadata')?.forEach(fn => fn()))
        }
      },
    }

    const origCreate = document.createElement.bind(document)
    vi.spyOn(document, 'createElement').mockImplementation(((tag: string) => {
      if (tag === 'video') return fakeVideo as unknown as HTMLVideoElement
      if (tag === 'canvas') {
        canvasAllocations += 1
      }
      return origCreate(tag)
    }) as typeof document.createElement)

    // 1. Corrupt video fires 'error' -> immediate reject, 0 canvas allocations, video cleaned
    await expect(
      upstreamAdapterModule.extractBoundedVideoFrame({
        url: 'blob:corrupt',
        maxPixels: TAIL_FRAME_MAX_PIXELS,
        signal: new AbortController().signal,
      }),
    ).rejects.toThrow(/无法解码/)
    expect(canvasAllocations).toBe(0)
    expect(videoCleanups).toBe(1)

    // 2. Oversized video (50 MP > 40 MP) -> rejects BEFORE creating <canvas>
    behavior = 'oversize'
    fakeVideo.src = ''
    await expect(
      upstreamAdapterModule.extractBoundedVideoFrame({
        url: 'blob:oversize',
        maxPixels: TAIL_FRAME_MAX_PIXELS,
        signal: new AbortController().signal,
      }),
    ).rejects.toThrow(/像素超出安全上限/)
    expect(canvasAllocations).toBe(0)
    expect(videoCleanups).toBe(2)

    // 3. Abort / timeout detaches listeners and cleans up <video> immediately
    behavior = 'hang'
    fakeVideo.src = ''
    const abortCtrl = new AbortController()
    const pending = upstreamAdapterModule.extractBoundedVideoFrame({
      url: 'blob:hang',
      maxPixels: TAIL_FRAME_MAX_PIXELS,
      signal: abortCtrl.signal,
    })
    abortCtrl.abort(new RangeError('超时取消'))
    await expect(pending).rejects.toThrow(/超时取消/)
    expect(listeners.get('loadedmetadata')?.size ?? 0).toBe(0)
    expect(listeners.get('error')?.size ?? 0).toBe(0)
    expect(videoCleanups).toBe(3)
  })
})
