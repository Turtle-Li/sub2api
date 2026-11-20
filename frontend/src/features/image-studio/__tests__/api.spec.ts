import { beforeEach, describe, expect, it, vi } from 'vitest'
import apiClient from '@/api/client'
import { listKeys, studioAPI, studioWalletRoute, taskDisplay, taskRoot, type StudioTask } from '../api'

vi.mock('@/api/client', () => ({ default: { get: vi.fn(), post: vi.fn() } }))
describe('Image Studio session adapter', () => {
  beforeEach(() => vi.clearAllMocks())
  it('uses the wallet route and no API key header for a keyless user', async () => {
    const signal = new AbortController().signal
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { items: [], pages: 1 }, headers: { 'x-image-studio-wallet': 'on' } })
    const configured = vi.fn()
    await expect(listKeys(signal, configured)).resolves.toEqual([])
    expect(configured).toHaveBeenCalledWith('on')
    const body = { model: 'wan2.7-image-pro', items: [{ custom_id: 'one', prompt: 'test' }] }
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: { id: 'wallet-job' } })
    await studioAPI(studioWalletRoute, signal).submit(body, 'once')
    expect(apiClient.post).toHaveBeenCalledOnce()
    expect(apiClient.post).toHaveBeenCalledWith('/image-studio/wallet/batches', body, expect.objectContaining({ signal, headers: { 'Idempotency-Key': 'once' } }))
  })
  it('does not enumerate further history until explicitly requested', async () => {
    // Given the first job page has additional history.
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { data: [], next_cursor: '12:3' } })
    // When loading the initial page.
    const page = await studioAPI(42, new AbortController().signal).tasks()
    // Then only that page is requested, with a continuation for an explicit action.
    expect(apiClient.get).toHaveBeenCalledTimes(1)
    expect(page.nextCursor).toBe('12:3')
  })
  it('trusts the expired history scope even if the browser clock is behind the server', async () => {
    const deadline = Math.floor(Date.now() / 1000) + 1800
    const job = { id: 'retired', status: 'completed', output_expires_at: deadline }
    const item = { custom_id: 'one', status: 'result_available', image_count: 0 }
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { data: [{ job, item }], next_cursor: '12:3' } })
    const page = await studioAPI(studioWalletRoute, new AbortController().signal).tasks('13:9', { status: 'expired', search: 'portrait' })
    expect(apiClient.get).toHaveBeenCalledOnce()
    expect(apiClient.get).toHaveBeenCalledWith('/image-studio/wallet/batches/gallery', expect.objectContaining({ params: { limit: 24, cursor: '13:9', status: 'expired', q: 'portrait' }, headers: {} }))
    expect(page.nextCursor).toBe('12:3')
    expect(taskDisplay(page.tasks[0])).toMatchObject({ label: '图片已过期', canViewOutput: false, canOpenArchive: false })
  })
  it('does not poll the server when no tasks are pending', async () => {
    // Given an empty visible history.
    const api = studioAPI(42, new AbortController().signal)
    // When polling is requested.
    const tasks = await api.refreshPending([])
    // Then the adapter performs no network request.
    expect(tasks).toEqual([])
    expect(apiClient.get).not.toHaveBeenCalled()
  })
  it('loads every page of owned non-secret keys', async () => {
    // Given a directory spanning two pages.
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { items: [{ id: 1 }], pages: 2 } }).mockResolvedValueOnce({ data: { items: [{ id: 2 }], pages: 2 } })
    // When the directory is loaded.
    const keys = await listKeys(new AbortController().signal)
    // Then the second page is included without a credential field.
    expect(keys).toEqual([{ id: 1 }, { id: 2 }])
    expect(apiClient.get).toHaveBeenLastCalledWith('/image-studio/keys', expect.objectContaining({ params: { page: 2, page_size: 100 } }))
  })
  it('preserves a caller submission identity and selected-key header', async () => {
    // Given a selected key and one unchanged request.
    vi.mocked(apiClient.post).mockResolvedValue({ data: { id: 'job-1' } })
    const signal = new AbortController().signal
    const body = { model: 'test-image', items: [{ custom_id: 'one', prompt: 'test' }] }
    // When the adapter submits with its stable retry identity.
    await studioAPI(42, signal).submit(body, 'attempt-1')
    // Then auth refresh remains the host client's responsibility; no raw key is introduced.
    expect(apiClient.post).toHaveBeenCalledWith('/image-studio/batches', body, expect.objectContaining({ signal, headers: { 'X-API-Key-ID': '42', 'Idempotency-Key': 'attempt-1' } }))
  })
  it('reads gallery metadata in one request without enumerating each batch', async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { data: [{ job: { id: 'job-1' }, item: { custom_id: 'one' } }], next_cursor: '1:2' } })
    const api = studioAPI(42, new AbortController().signal)
    const first = await api.tasks()
    expect(first.tasks.map(task => task.id)).toEqual(['job-1/one'])
    expect(apiClient.get).toHaveBeenCalledTimes(1)
    expect(first.itemContinuations).toEqual([])
    expect(first.nextCursor).toBe('1:2')
    expect(apiClient.get).toHaveBeenCalledWith('/image-studio/batches/gallery', expect.objectContaining({ params: { limit: 24, cursor: '', status: 'all', q: '' }, headers: { 'X-API-Key-ID': '42' } }))
  })
  it('requests thumbnails separately from full-resolution assets', async () => {
    const task = { job: { id: 'a' }, item: { custom_id: 'one' } } as StudioTask
    vi.mocked(apiClient.get).mockResolvedValue({ data: new Blob() })
    const api = studioAPI(42, new AbortController().signal)
    await api.thumbnail(task)
    expect(apiClient.get).toHaveBeenLastCalledWith('/image-studio/batches/a/items/one/thumbnail', expect.objectContaining({ responseType: 'blob' }))
    await api.image(task, 1)
    expect(apiClient.get).toHaveBeenLastCalledWith('/image-studio/batches/a/items/one/content', expect.objectContaining({ params: { image_index: 1 } }))
  })
  it('loads one explicitly chosen archive item via the owning key without forwarding auth to COS', async () => {
    const task = { job: { id: 'archive-job' }, item: { custom_id: 'one' } } as StudioTask
    const signal = new AbortController().signal
    const body = JSON.stringify({ key: 'one', status: { code: 'INVALID_ARGUMENT', message: 'invalid input' } }) + '\n'
    const url = 'https://image-1309919944.cos.ap-shanghai.myqcloud.com/fixture.jsonl?signature=fixture'
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { data: [{ index: 0, size: body.length, content_type: 'application/x-ndjson', url }] } })
    const fetch = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response(body))
    try {
      const result = await studioAPI(42, signal).archivePreview(task, signal)
      expect(result).toMatchObject({ status: 'failed', images: [], error: { code: 'INVALID_ARGUMENT' } })
      expect(apiClient.get).toHaveBeenCalledOnce()
      expect(apiClient.get).toHaveBeenCalledWith('/image-studio/batches/archive-job/result-files', expect.objectContaining({ signal, headers: { 'X-API-Key-ID': '42' } }))
      expect(fetch).toHaveBeenCalledOnce()
      expect(fetch).toHaveBeenCalledWith(url, expect.objectContaining({ credentials: 'omit', redirect: 'error', signal, headers: { Accept: 'application/x-ndjson, application/json' } }))
    } finally { fetch.mockRestore() }
  })
  it.each(['workspace', 'drawer'] as const)('cancels a private archive read when the %s closes', async scope => {
    const workspace = new AbortController()
    const drawer = new AbortController()
    const task = { job: { id: 'archive-job' }, item: { custom_id: 'one' } } as StudioTask
    const url = 'https://image-1309919944.cos.ap-shanghai.myqcloud.com/fixture.jsonl?signature=fixture'
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { data: [{ index: 0, size: 12, content_type: 'application/x-ndjson', url }] } })
    const fetch = vi.spyOn(globalThis, 'fetch').mockImplementation((_url, options) => new Promise<Response>((_resolve, reject) => {
      const signal = options?.signal
      if (!signal) { reject(new Error('Archive request must be abortable')); return }
      signal.addEventListener('abort', () => reject(signal.reason), { once: true })
    }))
    try {
      const pending = studioAPI(42, workspace.signal).archivePreview(task, drawer.signal)
      const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
      await vi.waitFor(() => expect(fetch).toHaveBeenCalledOnce())
      const cancelled = scope === 'workspace' ? workspace : drawer
      cancelled.abort()
      await rejected
      expect(apiClient.get).toHaveBeenCalledOnce()
    } finally { fetch.mockRestore() }
  })
  it('waits for a peer backfill and retries only the thumbnail', async () => {
    vi.useFakeTimers()
    try {
      const task = { job: { id: 'a' }, item: { custom_id: 'one' } } as StudioTask
      const image = new Blob(['preview'])
      vi.mocked(apiClient.get).mockRejectedValueOnce({ status: 503 }).mockResolvedValueOnce({ data: image })
      const read = studioAPI(42, new AbortController().signal).thumbnail(task)
      await vi.advanceTimersByTimeAsync(2000)
      expect(await read).toBe(image)
      expect(apiClient.get).toHaveBeenCalledTimes(2)
      expect(apiClient.get).toHaveBeenLastCalledWith('/image-studio/batches/a/items/one/thumbnail', expect.objectContaining({ timeout: 120000 }))
    } finally { vi.useRealTimers() }
  })
  it('does not retry a deleted thumbnail or read its original', async () => {
    const task = { job: { id: 'a' }, item: { custom_id: 'one' } } as StudioTask
    vi.mocked(apiClient.get).mockRejectedValueOnce({ status: 410 })
    await expect(studioAPI(42, new AbortController().signal).thumbnail(task)).rejects.toMatchObject({ status: 410 })
    expect(apiClient.get).toHaveBeenCalledTimes(1)
  })
  it('does not retry a failed-backfill cooldown or block on a card that left view', async () => {
    const task = { job: { id: 'a' }, item: { custom_id: 'one' } } as StudioTask
    vi.mocked(apiClient.get).mockRejectedValueOnce({ status: 409 })
    const api = studioAPI(42, new AbortController().signal)
    await expect(api.thumbnail(task)).rejects.toMatchObject({ status: 409 })
    const card = new AbortController()
    card.abort()
    await expect(api.thumbnail(task, card.signal)).rejects.toMatchObject({ name: 'AbortError' })
    expect(apiClient.get).toHaveBeenCalledTimes(1)
  })
  it('polls only loaded item pages and preserves the persisted editing root', async () => {
    const task = { id: 'child/one', job: { id: 'child', parent_batch_id: 'root', status: 'running' }, item: { custom_id: 'one' } } as StudioTask
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: task.job }).mockResolvedValueOnce({ data: { data: [{ custom_id: 'one', status: 'completed' }], has_more: true } })
    const refreshed = await studioAPI(42, new AbortController().signal).refreshPending([task])
    expect(apiClient.get).toHaveBeenCalledTimes(2)
    expect(refreshed[0].item.status).toBe('completed')
    expect(taskRoot(refreshed[0])).toBe('root')
    const params = vi.mocked(apiClient.get).mock.calls[1]?.[1]?.params
    expect(params).toBeInstanceOf(URLSearchParams)
    expect(params.getAll('custom_id')).toEqual(['one'])
  })

})
