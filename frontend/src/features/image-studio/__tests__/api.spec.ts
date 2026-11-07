import { beforeEach, describe, expect, it, vi } from 'vitest'
import apiClient from '@/api/client'
import { listKeys, studioAPI, taskRoot, type StudioTask } from '../api'

vi.mock('@/api/client', () => ({ default: { get: vi.fn(), post: vi.fn() } }))
describe('Image Studio session adapter', () => {
  beforeEach(() => vi.clearAllMocks())
  it('does not enumerate further history until explicitly requested', async () => {
    // Given the first job page has additional history.
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { data: [], next_cursor: '12:3' } })
    // When loading the initial page.
    const page = await studioAPI(42, new AbortController().signal).tasks()
    // Then only that page is requested, with a continuation for an explicit action.
    expect(apiClient.get).toHaveBeenCalledTimes(1)
    expect(page.nextCursor).toBe('12:3')
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
