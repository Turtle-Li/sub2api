import { beforeEach, describe, expect, it, vi } from 'vitest'
import apiClient from '@/api/client'
import { listKeys, studioAPI, taskRoot, type StudioTask } from '../api'

vi.mock('@/api/client', () => ({ default: { get: vi.fn(), post: vi.fn() } }))
describe('Image Studio session adapter', () => {
  beforeEach(() => vi.clearAllMocks())
  it('does not enumerate further history until explicitly requested', async () => {
    // Given the first job page has additional history.
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { data: [{ id: 'job-1' }], has_more: true } }).mockResolvedValueOnce({ data: { data: [], has_more: false } })
    // When loading the initial page.
    const page = await studioAPI(42, new AbortController().signal).tasks()
    // Then only that page is requested, with a continuation for an explicit action.
    expect(apiClient.get).toHaveBeenCalledTimes(2)
    expect(page.nextCursor).toBe('1')
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
  it('bounds initial item reads and exposes explicit continuation', async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { data: [{ id: 'job-1' }], has_more: false } }).mockResolvedValueOnce({ data: { data: [{ custom_id: 'one' }], has_more: true } })
    const api = studioAPI(42, new AbortController().signal)
    const first = await api.tasks()
    expect(first.tasks.map(task => task.id)).toEqual(['job-1/one'])
    expect(apiClient.get).toHaveBeenCalledTimes(2)
    expect(first.itemContinuations[0].cursor).toBe(1)
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { data: [{ custom_id: 'two' }], has_more: false } })
    const next = await api.items(first.itemContinuations[0].job, first.itemContinuations[0].cursor)
    expect(next.tasks.map(task => task.id)).toEqual(['job-1/two'])
    expect(next.next).toBeNull()
    expect(apiClient.get).toHaveBeenLastCalledWith('/image-studio/batches/job-1/items', expect.objectContaining({ params: { limit: 100, cursor: '1' } }))
  })
  it('polls only loaded item pages and preserves the persisted editing root', async () => {
    const task = { id: 'child/one', job: { id: 'child', parent_batch_id: 'root', status: 'running' }, item: { custom_id: 'one' } } as StudioTask
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: task.job }).mockResolvedValueOnce({ data: { data: [{ custom_id: 'one', status: 'completed' }], has_more: true } })
    const refreshed = await studioAPI(42, new AbortController().signal).refreshPending([task])
    expect(apiClient.get).toHaveBeenCalledTimes(2)
    expect(refreshed[0].item.status).toBe('completed')
    expect(taskRoot(refreshed[0])).toBe('root')
  })

})
