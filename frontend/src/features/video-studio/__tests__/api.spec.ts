// @vitest-environment node
import axios from 'axios'
import { afterEach, beforeEach, describe, expect, expectTypeOf, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import apiClient, { boundedBlobConfig } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { videoDurationMode, videoStudio, VIDEO_CONTENT_MAX_BYTES, type VideoDurationMode, type VideoJob, type VideoModel, type VideoSubmission } from '../api'

vi.mock('@/i18n', () => ({ getLocale: () => 'zh-CN' }))

const id = 'cde883f5-887d-4747-9f20-6236f2e81b85'
const body: VideoSubmission = { request_id: id, model: 'minimax/h3', quote_id: 'a'.repeat(64), prompt: '缓缓推近', seconds: 5,
  size: '1344x768', first_frame: 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC' }
const job: VideoJob = { id, model: body.model, status: 'queued', size: '1344x768', seconds: 5, created_at: '2026-11-20T00:10:00Z' }
const walletQuotes = [{ currency: 'USD', quote_id: 'b'.repeat(64) }, { currency: 'CNY', quote_id: 'c'.repeat(64) }] as const
const envelope = (value: unknown) => Response.json({ code: 0, data: value })
const fetch = vi.fn<typeof globalThis.fetch>()
const original = { baseURL: apiClient.defaults.baseURL, adapter: apiClient.defaults.adapter, env: apiClient.defaults.env }
function request(input: RequestInfo | URL): Request {
  if (!(input instanceof Request)) throw new TypeError('expected a Fetch Request')
  return input
}

beforeEach(() => {
  vi.stubGlobal('fetch', fetch)
  setActivePinia(createPinia())
  useAuthStore().$patch({ user: { id: 41 }, token: 'fixture-session' })
  localStorage.setItem('auth_token', 'fixture-session')
  localStorage.setItem('auth_user', JSON.stringify({ id: 41 }))
  Object.assign(apiClient.defaults, { baseURL: 'http://panel.invalid/api/v1', adapter: 'fetch', env: { ...original.env, fetch } })
})
afterEach(() => {
  Object.assign(apiClient.defaults, original)
  fetch.mockReset(); vi.restoreAllMocks(); vi.unstubAllGlobals(); localStorage.clear()
})

describe('video panel transport (real Axios interceptors, in-memory Fetch)', () => {
  it('unwraps cursor pages and status as metadata without reading video content', async () => {
    const page = { items: [job], next_cursor: 'next/+=' }
    fetch.mockResolvedValueOnce(envelope(page)).mockResolvedValueOnce(envelope({ items: [] })).mockResolvedValueOnce(envelope(job))
    await expect(videoStudio.list()).resolves.toEqual(page)
    await expect(videoStudio.list(page.next_cursor)).resolves.toEqual({ items: [] })
    await expect(videoStudio.get(id)).resolves.toEqual(job)
    const requests = fetch.mock.calls.map(([input]) => request(input))
    const urls = requests.map(r => new URL(r.url))
    expect(urls.map(u => u.pathname)).toEqual(['/api/v1/video-studio/jobs', '/api/v1/video-studio/jobs', `/api/v1/video-studio/jobs/${id}`])
    expect(urls[0]?.searchParams.has('cursor')).toBe(false)
    expect(urls[1]?.searchParams.get('cursor')).toBe(page.next_cursor)
    expect(requests.every(r => r.method === 'GET' && r.headers.get('Authorization') === 'Bearer fixture-session')).toBe(true)
  })

  it.each(walletQuotes)('preserves the $currency wallet quote and POSTs only its opaque quote ID once', async quote => {
    // Deliberately identical model and amount: neither identifies the settlement currency or signature.
    expectTypeOf<VideoModel['currency']>().toEqualTypeOf<'USD' | 'CNY'>()
    const model: VideoModel = { id: 'minimax/h3', label: 'H3', provider: 'goodput', min_seconds: 4, max_seconds: 15,
      default_seconds: 5, sizes: ['1344x768'], supports_frames: true, supports_seed: false, max_references: 2,
      quotes: { '5': 0.75 }, ...quote }
    const catalog = { enabled: true, items: [model] }
    fetch.mockResolvedValueOnce(envelope(catalog)).mockResolvedValueOnce(envelope(job))
    const directory = await videoStudio.models()
    expect(directory).toEqual(catalog)
    await expect(videoStudio.create({ ...body, quote_id: directory.items[0].quote_id })).resolves.toEqual(job)
    expect(fetch).toHaveBeenCalledTimes(2)
    const [lookup, sent] = fetch.mock.calls.map(([input]) => request(input))
    expect(lookup.method).toBe('GET')
    expect(new URL(lookup.url).pathname).toBe('/api/v1/video-studio/models')
    expect([lookup, sent].every(r => r.headers.get('Authorization') === 'Bearer fixture-session')).toBe(true)
    expect(sent.method).toBe('POST')
    expect(new URL(sent.url).pathname).toBe('/api/v1/video-studio/jobs')
    expect(sent.redirect).toBe('error')
    expect(sent.headers.get('X-API-Key-ID')).toBeNull()
    expect(await sent.json()).toEqual({ ...body, quote_id: quote.quote_id })
  })

  it('supports automatic duration_mode, optional submission seconds, and settled charge metadata', async () => {
    expectTypeOf<VideoModel['duration_mode']>().toEqualTypeOf<VideoDurationMode | undefined>()
    expectTypeOf<VideoSubmission['seconds']>().toEqualTypeOf<number | undefined>()
    expectTypeOf<VideoJob['charged_amount']>().toEqualTypeOf<number | undefined>()
    expectTypeOf<VideoJob['charged_currency']>().toEqualTypeOf<string | undefined>()
    expect(videoDurationMode({ id: 'muse-video', provider: 'muse' })).toBe('automatic')
    expect(videoDurationMode({ id: 'minimax/h3', provider: 'goodput' })).toBe('selectable')
    expect(videoDurationMode({ id: 'minimax/h3', provider: 'goodput', duration_mode: 'automatic' })).toBe('automatic')

    const museSubmission: VideoSubmission = { request_id: id, model: 'muse-video', quote_id: 'd'.repeat(64), prompt: '雾中行舟' }
    const settledJob: VideoJob = {
      ...job, model: 'muse-video', seconds: 6, charged_amount: 1.5, charged_currency: 'CNY',
      parameters: { material_mode: 'text', reference_count: 0, duration_mode: 'automatic' },
    }
    fetch.mockResolvedValueOnce(envelope(settledJob))
    await expect(videoStudio.create(museSubmission)).resolves.toEqual(settledJob)
    const sent = request(fetch.mock.calls[0][0])
    const payload = await sent.json()
    expect(payload).toEqual(museSubmission)
    expect('seconds' in payload).toBe(false)
  })

  it('never refreshes or replays the paid POST on a real 401 even with a usable refresh token', async () => {
    localStorage.setItem('refresh_token', 'fixture-refresh')
    const refresh = vi.spyOn(axios, 'post').mockResolvedValue({ data: { code: 0, data: { access_token: 'new', refresh_token: 'next', expires_in: 3600 } } })
    fetch.mockResolvedValueOnce(Response.json({ message: 'sign in again' }, { status: 401 })).mockResolvedValueOnce(envelope(job))
    await expect(videoStudio.create(body)).rejects.toMatchObject({ status: 401, message: 'sign in again' })
    expect(fetch).toHaveBeenCalledOnce(); expect(refresh).not.toHaveBeenCalled()
    expect(localStorage.getItem('auth_token')).toBe('fixture-session')
  })

  it('preserves ordinary 401 recovery on a read-only panel request', async () => {
    localStorage.setItem('refresh_token', 'fixture-refresh')
    const refresh = vi.spyOn(axios, 'post').mockResolvedValue({ data: { code: 0, data: { access_token: 'new', refresh_token: 'next', expires_in: 3600 } } })
    fetch.mockResolvedValueOnce(Response.json({}, { status: 401 })).mockResolvedValueOnce(envelope(job))
    await expect(videoStudio.get(id)).resolves.toEqual(job)
    expect(refresh).toHaveBeenCalledOnce(); expect(fetch).toHaveBeenCalledTimes(2)
    expect(request(fetch.mock.calls[1][0]).headers.get('Authorization')).toBe('Bearer new')
  })

  it.each(['https://private.invalid/secret', 'blob:preview', 'data:image/svg+xml;base64,PHN2Zy8+',
    'data:image/png;base64,YmFk', 'data:image/jpeg;base64,***='])('rejects invalid image inputs before dispatch: %s', async reference => {
    await expect(videoStudio.create({ ...body, first_frame: undefined, references: [reference] })).rejects.toThrow()
    expect(fetch).not.toHaveBeenCalled()
  })

  it('bounds H3 encoded request size, not just each decoded file', async () => {
    const image = 'data:image/png;base64,iVBORw0KGgoA' + 'AAAA'.repeat(1100000)
    await expect(videoStudio.create({ ...body, first_frame: image, last_frame: image })).rejects.toThrow(/8 MiB/)
    expect(fetch).not.toHaveBeenCalled()
  })

  it.each(['not-an-id', '00000000-0000-0000-0000-000000000000'])('neither submits nor invents a replacement for an invalid intent ID %s', async request_id => {
    await expect(videoStudio.create({ ...body, request_id })).rejects.toThrow()
    expect(fetch).not.toHaveBeenCalled()
  })

  it('does not dispatch an intent from a stale tab under another session', async () => {
    const submission = videoStudio.create(body)
    localStorage.setItem('auth_user', JSON.stringify({ id: 42 }))
    await expect(submission).rejects.toMatchObject({ code: 'ERR_CANCELED' })
    expect(fetch).not.toHaveBeenCalled()
  })

  it('reads a private JPEG poster through the original session without query credentials or content fallback', async () => {
    fetch.mockResolvedValueOnce(new Response(new Uint8Array([255, 216, 255, 217]), { headers: { 'Content-Type': 'image/jpeg' } }))
    const frame = await videoStudio.poster(id)
    expect(frame.type).toBe('image/jpeg'); expect(frame.size).toBe(4)
    const sent = request(fetch.mock.calls[0][0])
    const url = new URL(sent.url)
    expect(url.pathname).toBe(`/api/v1/video-studio/jobs/${id}/poster`); expect(url.searchParams.has('token')).toBe(false)
    expect(sent.credentials).toBe('include'); expect(sent.cache).toBe('no-store'); expect(sent.redirect).toBe('error')
    expect(sent.headers.get('Authorization')).toBe('Bearer fixture-session'); expect(sent.headers.get('X-API-Key-ID')).toBeNull()
    expect(fetch).toHaveBeenCalledOnce()
  })

  it.each([
    new Blob(['not-a-frame'], { type: 'image/png' }),
    new Blob([], { type: 'image/jpeg' }),
  ])('rejects an invalid or empty poster rather than displaying substitute media', async invalid => {
    fetch.mockResolvedValueOnce(new Response(invalid, { headers: { 'Content-Type': invalid.type } }))
    await expect(videoStudio.poster(id)).rejects.toThrow()
    expect(fetch).toHaveBeenCalledOnce(); expect(new URL(request(fetch.mock.calls[0][0]).url).pathname).not.toContain('/content')
  })

  it('poster denial stays on the poster route and never falls back to full video bytes', async () => {
    fetch.mockResolvedValueOnce(Response.json({}, { status: 403 }))
    await expect(videoStudio.poster(id)).rejects.toMatchObject({ status: 403 })
    expect(fetch).toHaveBeenCalledOnce()
  })

  it('reads a protected Blob only on demand, preserving the actual container type', async () => {
    fetch.mockResolvedValueOnce(new Response(new Uint8Array([0x1a, 0x45, 0xdf, 0xa3]), { headers: { 'Content-Type': 'video/webm' } }))
    const file = await videoStudio.content(id)
    expect(file.type).toBe('video/webm'); expect(file.size).toBe(4)
    const sent = request(fetch.mock.calls[0][0])
    expect(new URL(sent.url).pathname).toBe(`/api/v1/video-studio/jobs/${id}/content`)
    expect(sent.credentials).toBe('include'); expect(sent.cache).toBe('no-store'); expect(sent.redirect).toBe('error')
    expect(sent.headers.get('Authorization')).toBe('Bearer fixture-session')
  })

  it('rejects a declared oversize video before reading any video bytes', async () => {
    fetch.mockResolvedValueOnce(new Response(null, { headers: { 'Content-Length': String(VIDEO_CONTENT_MAX_BYTES + 1), 'Content-Type': 'video/mp4' } }))
    await expect(videoStudio.content(id)).rejects.toMatchObject({ status: 413 })
  })

  it.each([undefined, '2'])('also enforces the Blob limit on streamed bytes with content-length %s', async length => {
    fetch.mockResolvedValueOnce(new Response(new ReadableStream({ start(controller) {
      controller.enqueue(new Uint8Array(4)); controller.enqueue(new Uint8Array(3)); controller.close()
    } }), { headers: length === undefined ? undefined : { 'Content-Length': length } }))
    await expect(apiClient.get('/bounded-fixture', { ...boundedBlobConfig(6), sessionUserId: 41 })).rejects.toMatchObject({ status: 413 })
  })

  it.each([403, 410])('propagates protected media denial/expiry (%i), without trying another route', async status => {
    fetch.mockResolvedValueOnce(Response.json({}, { status }))
    await expect(videoStudio.content(id)).rejects.toMatchObject({ status })
    expect(fetch).toHaveBeenCalledOnce()
  })

  it('does not hand HTML error pages to a player or downloader', async () => {
    fetch.mockResolvedValueOnce(new Response('<html>login</html>', { headers: { 'Content-Type': 'text/html' } }))
    await expect(videoStudio.content(id)).rejects.toThrow()
  })

  it.each(['cancel', 'logout'])('aborts the actual media request on %s and never returns a late Blob', async action => {
    const caller = new AbortController()
    const started = new Promise<Request>(resolve => {
      fetch.mockImplementation(input => new Promise((_response, reject) => {
        const req = request(input); resolve(req)
        req.signal.addEventListener('abort', () => reject(req.signal.reason), { once: true })
      }))
    })
    const reading = videoStudio.content(id, caller.signal)
    const req = await started
    if (action === 'cancel') caller.abort()
    else useAuthStore().$patch({ user: null, token: null })
    await expect(reading).rejects.toMatchObject({ code: 'ERR_CANCELED' })
    expect(req.signal.aborted).toBe(true)
  })
})
