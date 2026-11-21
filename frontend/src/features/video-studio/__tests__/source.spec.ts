import { createPinia, setActivePinia } from 'pinia'
import { effectScope } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import apiClient from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { workspaceAPI } from '@/features/image-studio/workspaceAPI'
import { studioWalletRoute } from '@/features/image-studio/api'
import { VIDEO_IMAGE_MAX_BYTES } from '../api'
import { prepareImage, clearImage, pendingImage, readImage } from '../source'

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
